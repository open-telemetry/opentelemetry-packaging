// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package obs

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Options describe one package upload.
type Options struct {
	Project string
	Package string
	// Dir holds the package's sources: every regular file in it, and nothing
	// else, becomes the package's content.
	Dir     string
	Message string
	// Release copies the package to its project's release targets once it has
	// built everywhere.
	Release bool
	// ReleaseUnchanged also releases sources the package already holds, once
	// they have built. It recovers a run that committed but failed before
	// releasing, which a re-run otherwise skips as unchanged.
	ReleaseUnchanged bool
	// Interval is the time between build result checks.
	Interval time.Duration
	// Grace is how long after a commit a failure status is still taken for the
	// previous revision's, before the scheduler has caught up with the new one.
	Grace time.Duration
	Logf  func(format string, args ...any)
}

// Outcome is what Publish did.
type Outcome struct {
	// Changed reports whether the sources differed from the package's, so a
	// new revision was committed and built.
	Changed  bool
	Revision string
	Released bool
}

// Publish uploads a package's sources and, when they changed, waits for the
// new revision to build in every repository and architecture and then releases
// it if asked. Unchanged sources are neither committed nor, unless
// ReleaseUnchanged is set, released again: a regenerated, unchanged package is
// byte-identical to its last upload, which has been released already.
func Publish(ctx context.Context, c *Client, opts Options) (*Outcome, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	files, paths, err := localFiles(opts.Dir)
	if err != nil {
		return nil, err
	}

	current, err := c.Sources(ctx, opts.Project, opts.Package)
	if errors.Is(err, ErrNotFound) {
		logf("creating package %s/%s", opts.Project, opts.Package)
		if err := c.CreatePackage(ctx, opts.Project, opts.Package); err != nil {
			return nil, err
		}
		current = &Directory{}
	} else if err != nil {
		return nil, err
	}

	committed := current
	outcome := &Outcome{Revision: current.Rev}
	switch {
	case !sameFiles(current.Entries, files):
		if committed, err = c.Commit(ctx, opts.Project, opts.Package, files, paths, opts.Message); err != nil {
			return nil, err
		}
		logf("committed %s/%s revision %s (%s)", opts.Project, opts.Package, committed.Rev, committed.SrcMD5)
		outcome = &Outcome{Changed: true, Revision: committed.Rev}
	case opts.Release && opts.ReleaseUnchanged:
		logf("%s/%s already holds these sources at revision %s; releasing them again", opts.Project, opts.Package, current.Rev)
	default:
		logf("%s/%s already holds these sources at revision %s; nothing to commit or release", opts.Project, opts.Package, current.Rev)
		return outcome, nil
	}

	if err := WaitForBuilds(ctx, c, opts, committed.SrcMD5); err != nil {
		return outcome, err
	}
	if !opts.Release {
		return outcome, nil
	}
	if err := c.Release(ctx, opts.Project, opts.Package); err != nil {
		return outcome, err
	}
	logf("released %s/%s revision %s", opts.Project, opts.Package, committed.Rev)
	outcome.Released = true
	return outcome, nil
}

// buildSucceeded are the final codes of a build whose binaries can be
// released: "unchanged" is a rebuild that produced the same binaries.
var buildSucceeded = []string{"succeeded", "unchanged"}

// buildBlocked are statuses under which a package will not build without
// someone changing it or its project.
var buildBlocked = []string{"failed", "broken", "unresolvable"}

// maxPollFailures is how many checks in a row may fail before waiting for the
// builds gives up. The client retries each request already; this covers an
// outage that outlasts those retries. Giving up here fails a run that has
// committed its sources, which then has to be released by hand, so the wait
// sits it out for as long as the run's own deadline allows.
const maxPollFailures = 10

// WaitForBuilds waits until the sources with srcmd5 have built in every
// repository and architecture of the project, and fails as soon as one of them
// fails or cannot build.
//
// It goes by each repository's job history rather than the status alone: right
// after a commit the status still describes the previous revision until the
// scheduler marks the repository dirty, and a status of succeeded then would
// release the old binaries.
//
// A check that cannot reach the API is not a build that failed: it is tried
// again until maxPollFailures in a row have failed, or the context is done.
func WaitForBuilds(ctx context.Context, c *Client, opts Options, srcmd5 string) error {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	w := &buildWatch{
		c:      c,
		opts:   opts,
		srcmd5: srcmd5,
		logf:   logf,
		start:  time.Now(),
		built:  map[string]bool{},
	}

	failures := 0
	for {
		pending, err := w.poll(ctx)
		switch {
		case err == nil:
			failures = 0
			if pending == 0 {
				logf("%s built in every repository", opts.Package)
				return nil
			}
		case ctx.Err() != nil:
			return fmt.Errorf("waiting for %s to build: %w", opts.Package, ctx.Err())
		case !isTransient(err):
			return err
		default:
			failures++
			if failures > maxPollFailures {
				return fmt.Errorf("checking the builds of %s failed %d times in a row: %w", opts.Package, failures, err)
			}
			logf("checking the builds of %s failed, trying again: %v", opts.Package, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s to build: %w", opts.Package, ctx.Err())
		case <-time.After(opts.Interval):
		}
	}
}

// buildWatch is one wait for the builds of a revision.
type buildWatch struct {
	c      *Client
	opts   Options
	srcmd5 string
	logf   func(format string, args ...any)
	start  time.Time
	// built are the repository/architecture pairs that have built the
	// revision. A finished build does not come undone, so they are not asked
	// about again: the job history of every target, on every check, is most
	// of what this makes OBS serve.
	built map[string]bool
	last  string
}

// poll checks every repository and architecture once and returns how many have
// yet to build the revision.
func (w *buildWatch) poll(ctx context.Context) (int, error) {
	results, err := w.c.Results(ctx, w.opts.Project, w.opts.Package)
	if err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, fmt.Errorf("%s has no repositories building %s", w.opts.Project, w.opts.Package)
	}

	pending := 0
	enabled := 0
	for _, r := range results {
		where := r.Repository + "/" + r.Arch
		code := r.Code(w.opts.Package)
		switch code {
		case "disabled":
			continue
		case "excluded":
			return 0, fmt.Errorf("%s is excluded on %s: check the repository's build configuration", w.opts.Package, where)
		}
		enabled++
		if w.built[where] {
			continue
		}

		job, err := w.c.LastJob(ctx, w.opts.Project, r.Repository, r.Arch, w.opts.Package)
		if err != nil {
			return 0, err
		}
		if job != nil && job.SrcMD5 == w.srcmd5 {
			if slices.Contains(buildSucceeded, job.Code) {
				w.built[where] = true
				continue
			}
			return 0, fmt.Errorf("%s %s on %s", w.opts.Package, job.Code, where)
		}
		if slices.Contains(buildBlocked, code) && !r.Dirty && time.Since(w.start) > w.opts.Grace {
			return 0, fmt.Errorf("%s is %s on %s", w.opts.Package, code, where)
		}
		pending++
	}

	// Every repository skipped means nothing is building the revision, and
	// nothing would come of waiting: releasing then copies whatever the
	// release targets already hold and calls the run a success. A package
	// just created in a project whose repositories are not enabled for it
	// arrives here.
	if enabled == 0 {
		return 0, fmt.Errorf("%s is disabled in every repository of %s: nothing would be built to release",
			w.opts.Package, w.opts.Project)
	}

	if summary := fmt.Sprintf("waiting for %d of %d builds of %s", pending, len(results), w.opts.Package); pending > 0 && summary != w.last {
		w.logf("%s", summary)
		w.last = summary
	}
	return pending, nil
}

// localFiles lists the regular files in dir with their MD5 sums, which is how
// OBS identifies a source file.
func localFiles(dir string) ([]Entry, map[string]string, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var files []Entry
	paths := map[string]string{}
	for _, d := range dirEntries {
		if !d.Type().IsRegular() {
			return nil, nil, fmt.Errorf("%s: only regular files can be uploaded", filepath.Join(dir, d.Name()))
		}
		path := filepath.Join(dir, d.Name())
		sum, err := md5File(path)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, Entry{Name: d.Name(), MD5: sum})
		paths[d.Name()] = path
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("%s holds no sources", dir)
	}
	return files, paths, nil
}

func md5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sameFiles(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	sums := make(map[string]string, len(a))
	for _, e := range a {
		sums[e.Name] = e.MD5
	}
	for _, e := range b {
		if sum, ok := sums[e.Name]; !ok || sum != e.MD5 {
			return false
		}
	}
	return true
}
