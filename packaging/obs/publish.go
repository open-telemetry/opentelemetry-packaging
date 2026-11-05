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

// WaitForBuilds waits until the sources with srcmd5 have built in every
// repository and architecture of the project, and fails as soon as one of them
// fails or cannot build.
//
// It goes by each repository's job history rather than the status alone: right
// after a commit the status still describes the previous revision until the
// scheduler marks the repository dirty, and a status of succeeded then would
// release the old binaries.
func WaitForBuilds(ctx context.Context, c *Client, opts Options, srcmd5 string) error {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	start := time.Now()
	last := ""
	for {
		results, err := c.Results(ctx, opts.Project, opts.Package)
		if err != nil {
			return err
		}
		if len(results) == 0 {
			return fmt.Errorf("%s has no repositories building %s", opts.Project, opts.Package)
		}

		pending := 0
		for _, r := range results {
			where := r.Repository + "/" + r.Arch
			code := r.Code(opts.Package)
			switch code {
			case "disabled":
				continue
			case "excluded":
				return fmt.Errorf("%s is excluded on %s: check the repository's build configuration", opts.Package, where)
			}

			job, err := c.LastJob(ctx, opts.Project, r.Repository, r.Arch, opts.Package)
			if err != nil {
				return err
			}
			if job != nil && job.SrcMD5 == srcmd5 {
				if slices.Contains(buildSucceeded, job.Code) {
					continue
				}
				return fmt.Errorf("%s %s on %s", opts.Package, job.Code, where)
			}
			if slices.Contains(buildBlocked, code) && !r.Dirty && time.Since(start) > opts.Grace {
				return fmt.Errorf("%s is %s on %s", opts.Package, code, where)
			}
			pending++
		}
		if pending == 0 {
			logf("%s built in every repository", opts.Package)
			return nil
		}

		if summary := fmt.Sprintf("waiting for %d of %d builds of %s", pending, len(results), opts.Package); summary != last {
			logf("%s", summary)
			last = summary
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s to build: %w", opts.Package, ctx.Err())
		case <-time.After(opts.Interval):
		}
	}
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
