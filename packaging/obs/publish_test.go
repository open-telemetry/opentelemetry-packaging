// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package obs

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testProject = "home:example:stable:build"
	testPackage = "opentelemetry-java-autoinstrumentation"
)

// fakeOBS serves the part of the OBS API Publish uses, for one package built
// in two repository/architecture pairs.
type fakeOBS struct {
	t  *testing.T
	mu sync.Mutex

	exists   bool
	files    map[string][]byte // committed sources
	uploaded map[string][]byte // uploaded, not yet committed
	rev      int
	comments []string
	released int

	// build decides, per poll, the status and last job of each target, given
	// the committed srcmd5 and how many times results were requested.
	build func(target, srcmd5 string, poll int) (code string, dirty bool, job *Job)
	polls int
}

var targets = []string{"debian/x86_64", "ubuntu/aarch64"}

func newFakeOBS(t *testing.T) (*fakeOBS, *Client) {
	f := &fakeOBS{t: t, files: map[string][]byte{}, uploaded: map[string][]byte{}}
	// By default each build finishes on the second poll after a commit.
	f.build = func(_, srcmd5 string, poll int) (string, bool, *Job) {
		if poll < 2 {
			return "succeeded", false, &Job{SrcMD5: "previous", Code: "succeeded"}
		}
		return "succeeded", false, &Job{SrcMD5: srcmd5, Code: "succeeded"}
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, NewClient(srv.URL, "bot", "s3cret")
}

func (f *fakeOBS) srcmd5() string {
	names := make([]string, 0, len(f.files))
	for name := range f.files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := md5.New()
	for _, name := range names {
		fmt.Fprintf(h, "%s %x\n", name, md5.Sum(f.files[name]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (f *fakeOBS) directory() Directory {
	dir := Directory{Rev: fmt.Sprint(f.rev), SrcMD5: f.srcmd5()}
	for name, data := range f.files {
		sum := md5.Sum(data)
		dir.Entries = append(dir.Entries, Entry{Name: name, MD5: hex.EncodeToString(sum[:])})
	}
	return dir
}

func (f *fakeOBS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if user, pass, ok := r.BasicAuth(); !ok || user != "bot" || pass != "s3cret" {
		http.Error(w, `<status code="authentication_required"><summary>Authentication required</summary></status>`, http.StatusUnauthorized)
		return
	}
	reply := func(v any) {
		data, err := xml.Marshal(v)
		require.NoError(f.t, err)
		_, _ = w.Write(data)
	}

	source := "/source/" + testProject + "/" + testPackage
	switch {
	case r.Method == http.MethodGet && r.URL.Path == source:
		if !f.exists {
			http.Error(w, `<status code="unknown_package"><summary>`+testPackage+`</summary></status>`, http.StatusNotFound)
			return
		}
		reply(f.directory())

	case r.Method == http.MethodPut && r.URL.Path == source+"/_meta":
		f.exists = true
		_, _ = w.Write([]byte(`<status code="ok"/>`))

	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, source+"/"):
		require.Equal(f.t, "repository", r.URL.Query().Get("rev"))
		data, err := io.ReadAll(r.Body)
		require.NoError(f.t, err)
		f.uploaded[strings.TrimPrefix(r.URL.Path, source+"/")] = data
		_, _ = w.Write([]byte(`<status code="ok"/>`))

	case r.Method == http.MethodPost && r.URL.Path == source && r.URL.Query().Get("cmd") == "commitfilelist":
		var list Directory
		require.NoError(f.t, xml.NewDecoder(r.Body).Decode(&list))
		next := map[string][]byte{}
		missing := Directory{Error: "missing"}
		for _, e := range list.Entries {
			data, ok := f.uploaded[e.Name]
			if !ok {
				data, ok = f.files[e.Name]
			}
			if sum := md5.Sum(data); !ok || hex.EncodeToString(sum[:]) != e.MD5 {
				missing.Entries = append(missing.Entries, Entry{Name: e.Name, MD5: e.MD5})
				continue
			}
			next[e.Name] = data
		}
		if len(missing.Entries) > 0 {
			reply(missing)
			return
		}
		f.files, f.uploaded = next, map[string][]byte{}
		f.rev++
		f.polls = 0
		f.comments = append(f.comments, r.URL.Query().Get("comment"))
		reply(f.directory())

	case r.Method == http.MethodPost && r.URL.Path == source && r.URL.Query().Get("cmd") == "release":
		f.released++
		_, _ = w.Write([]byte(`<status code="ok"/>`))

	case r.Method == http.MethodGet && r.URL.Path == "/build/"+testProject+"/_result":
		f.polls++
		type status struct {
			Package string `xml:"package,attr"`
			Code    string `xml:"code,attr"`
		}
		type result struct {
			Repository string   `xml:"repository,attr"`
			Arch       string   `xml:"arch,attr"`
			Dirty      bool     `xml:"dirty,attr,omitempty"`
			Status     []status `xml:"status"`
		}
		var list struct {
			XMLName xml.Name `xml:"resultlist"`
			Results []result `xml:"result"`
		}
		for _, target := range targets {
			repo, arch, _ := strings.Cut(target, "/")
			code, dirty, _ := f.build(target, f.srcmd5(), f.polls)
			list.Results = append(list.Results, result{Repository: repo, Arch: arch, Dirty: dirty, Status: []status{{testPackage, code}}})
		}
		reply(list)

	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/_jobhistory"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/build/"+testProject+"/"), "/")
		require.Len(f.t, parts, 3)
		_, _, job := f.build(parts[0]+"/"+parts[1], f.srcmd5(), f.polls)
		var list struct {
			XMLName xml.Name `xml:"jobhistlist"`
			Jobs    []Job    `xml:"jobhist"`
		}
		if job != nil {
			list.Jobs = []Job{*job}
		}
		reply(list)

	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		http.Error(w, "unexpected", http.StatusBadRequest)
	}
}

func sourcesDir(t *testing.T, files map[string]string) string {
	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	return dir
}

func options(dir string) Options {
	return Options{
		Project: testProject, Package: testPackage, Dir: dir,
		Message: "java 2.33.1", Release: true,
		Interval: time.Millisecond, Grace: time.Hour,
	}
}

func TestPublishCreatesCommitsWaitsAndReleases(t *testing.T) {
	f, c := newFakeOBS(t)
	dir := sourcesDir(t, map[string]string{"pkg.spec": "spec", "pkg-2.33.1.tar.gz": "archive"})

	outcome, err := Publish(context.Background(), c, options(dir))
	require.NoError(t, err)
	require.Equal(t, &Outcome{Changed: true, Revision: "1", Released: true}, outcome)
	require.True(t, f.exists)
	require.Equal(t, map[string][]byte{"pkg.spec": []byte("spec"), "pkg-2.33.1.tar.gz": []byte("archive")}, f.files)
	require.Equal(t, []string{"java 2.33.1"}, f.comments)
	require.Equal(t, 1, f.released)
	// The first poll still saw the previous revision's results.
	require.GreaterOrEqual(t, f.polls, 2)
}

func TestPublishReplacesTheSources(t *testing.T) {
	f, c := newFakeOBS(t)
	_, err := Publish(context.Background(), c, options(sourcesDir(t, map[string]string{"pkg.spec": "v1", "pkg-1.tar.gz": "old"})))
	require.NoError(t, err)

	outcome, err := Publish(context.Background(), c, options(sourcesDir(t, map[string]string{"pkg.spec": "v2", "pkg-2.tar.gz": "new"})))
	require.NoError(t, err)
	require.Equal(t, "2", outcome.Revision)
	// The old archive is gone, not left next to the new one.
	require.Equal(t, map[string][]byte{"pkg.spec": []byte("v2"), "pkg-2.tar.gz": []byte("new")}, f.files)
	require.Equal(t, 2, f.released)
}

func TestPublishSkipsUnchangedSources(t *testing.T) {
	f, c := newFakeOBS(t)
	dir := sourcesDir(t, map[string]string{"pkg.spec": "spec"})
	_, err := Publish(context.Background(), c, options(dir))
	require.NoError(t, err)

	outcome, err := Publish(context.Background(), c, options(dir))
	require.NoError(t, err)
	require.Equal(t, &Outcome{Revision: "1"}, outcome)
	require.Equal(t, 1, f.rev)
	require.Equal(t, 1, f.released)
}

func TestPublishCanReleaseUnchangedSources(t *testing.T) {
	f, c := newFakeOBS(t)
	dir := sourcesDir(t, map[string]string{"pkg.spec": "spec"})
	opts := options(dir)
	opts.Release = false
	_, err := Publish(context.Background(), c, opts)
	require.NoError(t, err)
	require.Equal(t, 0, f.released)

	opts.Release, opts.ReleaseUnchanged = true, true
	outcome, err := Publish(context.Background(), c, opts)
	require.NoError(t, err)
	require.Equal(t, &Outcome{Revision: "1", Released: true}, outcome)
	require.Equal(t, 1, f.rev)
	require.Equal(t, 1, f.released)
}

func TestPublishWithoutRelease(t *testing.T) {
	f, c := newFakeOBS(t)
	opts := options(sourcesDir(t, map[string]string{"pkg.spec": "spec"}))
	opts.Release = false
	outcome, err := Publish(context.Background(), c, opts)
	require.NoError(t, err)
	require.Equal(t, &Outcome{Changed: true, Revision: "1"}, outcome)
	require.Equal(t, 0, f.released)
}

func TestPublishFailsWithoutReleasing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(target, srcmd5 string, poll int) (string, bool, *Job)
		grace time.Duration
		want  string
	}{
		{
			name: "build failed",
			build: func(target, srcmd5 string, _ int) (string, bool, *Job) {
				if target == "ubuntu/aarch64" {
					return "failed", false, &Job{SrcMD5: srcmd5, Code: "failed"}
				}
				return "succeeded", false, &Job{SrcMD5: srcmd5, Code: "succeeded"}
			},
			want: "failed on ubuntu/aarch64",
		},
		{
			name: "excluded",
			build: func(string, string, int) (string, bool, *Job) {
				return "excluded", false, nil
			},
			want: "excluded on debian/x86_64",
		},
		{
			name: "unresolvable",
			build: func(string, string, int) (string, bool, *Job) {
				return "unresolvable", false, nil
			},
			want: "unresolvable on debian/x86_64",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newFakeOBS(t)
			f.build = tc.build
			opts := options(sourcesDir(t, map[string]string{"pkg.spec": "spec"}))
			opts.Grace = 0
			outcome, err := Publish(context.Background(), c, opts)
			require.ErrorContains(t, err, tc.want)
			require.True(t, outcome.Changed)
			require.False(t, outcome.Released)
			require.Equal(t, 0, f.released)
		})
	}
}

// Right after a commit the results still describe the previous revision. A
// failure there must not fail the new one before it has had a chance to build.
func TestWaitIgnoresThePreviousRevisionsFailure(t *testing.T) {
	f, c := newFakeOBS(t)
	f.build = func(_, srcmd5 string, poll int) (string, bool, *Job) {
		switch {
		case poll < 3:
			return "failed", false, &Job{SrcMD5: "previous", Code: "failed"}
		case poll < 5:
			return "scheduled", true, &Job{SrcMD5: "previous", Code: "failed"}
		}
		return "succeeded", false, &Job{SrcMD5: srcmd5, Code: "succeeded"}
	}
	outcome, err := Publish(context.Background(), c, options(sourcesDir(t, map[string]string{"pkg.spec": "spec"})))
	require.NoError(t, err)
	require.True(t, outcome.Released)
}

func TestWaitTimesOut(t *testing.T) {
	f, c := newFakeOBS(t)
	f.build = func(string, string, int) (string, bool, *Job) {
		return "building", false, &Job{SrcMD5: "previous", Code: "succeeded"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Publish(ctx, c, options(sourcesDir(t, map[string]string{"pkg.spec": "spec"})))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 0, f.released)
}

func TestErrorsCarryTheServersSummaryButNotTheCredentials(t *testing.T) {
	_, c := newFakeOBS(t)
	c.Password = "wrong-password"
	_, err := Publish(context.Background(), c, options(sourcesDir(t, map[string]string{"pkg.spec": "spec"})))
	require.ErrorContains(t, err, "authentication_required: Authentication required")
	require.NotContains(t, err.Error(), "wrong-password")
}

func TestPublishRejectsEmptyOrNestedSources(t *testing.T) {
	_, c := newFakeOBS(t)
	_, err := Publish(context.Background(), c, options(t.TempDir()))
	require.ErrorContains(t, err, "holds no sources")

	dir := sourcesDir(t, map[string]string{"pkg.spec": "spec"})
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0o755))
	_, err = Publish(context.Background(), c, options(dir))
	require.ErrorContains(t, err, "only regular files")
}
