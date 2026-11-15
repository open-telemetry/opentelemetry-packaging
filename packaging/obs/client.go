// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package obs uploads, builds and releases packages on the Open Build Service
// through its HTTP API, the same requests osc sends, so CI needs neither osc
// nor its rpm Python bindings.
package obs

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultAPIURL is the API of the public Open Build Service instance.
const DefaultAPIURL = "https://api.opensuse.org"

const (
	// defaultRetries is how many times a repeatable request is sent again
	// after a transient failure. Waiting for the builds of one package asks
	// the API thousands of times over several hours, and api.opensuse.org is
	// a shared service that is briefly unavailable now and then.
	defaultRetries = 4
	defaultBackoff = 2 * time.Second
	maxBackoff     = 30 * time.Second
)

// Client talks to an OBS API with HTTP basic authentication.
type Client struct {
	APIURL   string
	Username string
	Password string
	HTTP     *http.Client
	// Retries is how many times a request that is safe to repeat is sent
	// again after a transient failure.
	Retries int
	// Backoff is the wait before the first retry; it doubles for each further
	// one, up to half a minute.
	Backoff time.Duration
	// Logf, when set, reports retries, which are otherwise invisible.
	Logf func(format string, args ...any)
}

// NewClient returns a client for apiURL. Uploads of the larger source archives
// take minutes, which the timeout allows for.
func NewClient(apiURL, username, password string) *Client {
	return &Client{
		APIURL:   strings.TrimSuffix(apiURL, "/"),
		Username: username,
		Password: password,
		HTTP:     &http.Client{Timeout: 15 * time.Minute},
		Retries:  defaultRetries,
		Backoff:  defaultBackoff,
	}
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// transientError is a failure that may well not happen again: the API was
// unreachable, or answered that it could not serve the request right now. It
// is the opposite of a package that failed to build or a project that does not
// exist, which no amount of asking again will change.
type transientError struct{ err error }

func (e transientError) Error() string { return e.err.Error() }
func (e transientError) Unwrap() error { return e.err }

// isTransient reports whether err is worth retrying.
func isTransient(err error) bool {
	var t transientError
	return errors.As(err, &t)
}

// Entry is a file in a package's sources.
type Entry struct {
	Name string `xml:"name,attr"`
	MD5  string `xml:"md5,attr"`
}

// Directory is a package's source listing, or the result of a commit.
type Directory struct {
	Rev     string  `xml:"rev,attr"`
	SrcMD5  string  `xml:"srcmd5,attr"`
	Error   string  `xml:"error,attr"`
	Entries []Entry `xml:"entry"`
}

// Result is one repository and architecture of a package's build results.
type Result struct {
	Repository string `xml:"repository,attr"`
	Arch       string `xml:"arch,attr"`
	State      string `xml:"state,attr"`
	Dirty      bool   `xml:"dirty,attr"`
	Status     []struct {
		Package string `xml:"package,attr"`
		Code    string `xml:"code,attr"`
	} `xml:"status"`
}

// Code returns the package's build status in this repository and
// architecture, or "" when the result does not list it.
func (r Result) Code(pkg string) string {
	for _, s := range r.Status {
		if s.Package == pkg {
			return s.Code
		}
	}
	return ""
}

// Job is the record of a finished build.
type Job struct {
	SrcMD5  string `xml:"srcmd5,attr"`
	Rev     string `xml:"rev,attr"`
	VersRel string `xml:"versrel,attr"`
	Code    string `xml:"code,attr"`
}

// ErrNotFound is returned for a project or package that does not exist.
var ErrNotFound = errors.New("not found")

// Sources lists the files of a package. It returns ErrNotFound when the package
// does not exist.
func (c *Client) Sources(ctx context.Context, project, pkg string) (*Directory, error) {
	var dir Directory
	if err := c.do(ctx, http.MethodGet, c.url(nil, "source", project, pkg), nil, &dir); err != nil {
		return nil, err
	}
	return &dir, nil
}

// CreatePackage creates an empty package in project.
func (c *Client) CreatePackage(ctx context.Context, project, pkg string) error {
	type meta struct {
		XMLName     xml.Name `xml:"package"`
		Name        string   `xml:"name,attr"`
		Project     string   `xml:"project,attr"`
		Title       string   `xml:"title"`
		Description string   `xml:"description"`
	}
	body, err := xml.Marshal(meta{Name: pkg, Project: project, Title: pkg})
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, c.url(nil, "source", project, pkg, "_meta"), strings.NewReader(string(body)), nil)
}

// Commit makes files, and only them, the package's sources, the way osc
// commit does: it sends the file list, uploads whichever files the server
// reports missing, and sends the list again. Files the package holds but files
// does not name are removed. paths maps each file name to its local path.
func (c *Client) Commit(ctx context.Context, project, pkg string, files []Entry, paths map[string]string, message string) (*Directory, error) {
	list, err := xml.Marshal(struct {
		XMLName xml.Name `xml:"directory"`
		Entries []Entry  `xml:"entry"`
	}{Entries: files})
	if err != nil {
		return nil, err
	}
	commit := c.url(url.Values{"cmd": {"commitfilelist"}, "comment": {message}}, "source", project, pkg)

	for attempt := 0; ; attempt++ {
		var dir Directory
		if err := c.do(ctx, http.MethodPost, commit, strings.NewReader(string(list)), &dir); err != nil {
			return nil, err
		}
		switch {
		case dir.Error == "":
			return &dir, nil
		case dir.Error != "missing":
			return nil, fmt.Errorf("committing %s/%s: %s", project, pkg, dir.Error)
		case attempt > 0:
			return nil, fmt.Errorf("committing %s/%s: files still missing after upload", project, pkg)
		}
		for _, missing := range dir.Entries {
			path, ok := paths[missing.Name]
			if !ok {
				return nil, fmt.Errorf("committing %s/%s: server asks for unknown file %q", project, pkg, missing.Name)
			}
			if err := c.upload(ctx, project, pkg, missing.Name, path); err != nil {
				return nil, err
			}
		}
	}
}

func (c *Client) upload(ctx context.Context, project, pkg, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := c.do(ctx, http.MethodPut, c.url(url.Values{"rev": {"repository"}}, "source", project, pkg, name), f, nil); err != nil {
		return fmt.Errorf("uploading %s: %w", name, err)
	}
	return nil
}

// Results returns a package's build results in every repository and
// architecture of its project.
func (c *Client) Results(ctx context.Context, project, pkg string) ([]Result, error) {
	var list struct {
		Results []Result `xml:"result"`
	}
	if err := c.do(ctx, http.MethodGet, c.url(url.Values{"package": {pkg}}, "build", project, "_result"), nil, &list); err != nil {
		return nil, err
	}
	return list.Results, nil
}

// LastJob returns the latest finished build of a package in one repository and
// architecture, or nil when it has never built there.
func (c *Client) LastJob(ctx context.Context, project, repository, arch, pkg string) (*Job, error) {
	var list struct {
		Jobs []Job `xml:"jobhist"`
	}
	query := url.Values{"package": {pkg}, "limit": {"1"}}
	if err := c.do(ctx, http.MethodGet, c.url(query, "build", project, repository, arch, "_jobhistory"), nil, &list); err != nil {
		return nil, err
	}
	if len(list.Jobs) == 0 {
		return nil, nil
	}
	return &list.Jobs[len(list.Jobs)-1], nil
}

// Release copies a package's sources and built binaries to the release
// targets of its project's repositories, as osc release does.
//
// Unlike a commit, it is retried: releasing the same revision twice copies the
// same binaries again, which is what -release-unchanged does on purpose, and
// it is the last step of a run, where failing means releasing by hand.
func (c *Client) Release(ctx context.Context, project, pkg string) error {
	return c.send(ctx, http.MethodPost, c.url(url.Values{"cmd": {"release"}}, "source", project, pkg), nil, nil, true)
}

func (c *Client) url(query url.Values, segments ...string) string {
	escaped := make([]string, len(segments))
	for i, s := range segments {
		escaped[i] = url.PathEscape(s)
	}
	u := c.APIURL + "/" + strings.Join(escaped, "/")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

// do sends a request and decodes an XML response into out, when given,
// retrying it while it fails transiently and is safe to repeat.
func (c *Client) do(ctx context.Context, method, u string, body io.Reader, out any) error {
	return c.send(ctx, method, u, body, out, repeatable(method))
}

// repeatable reports whether sending a request again is safe. Reading and
// uploading a file are. Committing is not: a repeat of a commit the server did
// carry out, but whose answer was lost, would make a second revision of the
// same sources and so a needless rebuild and release.
func repeatable(method string) bool {
	return method == http.MethodGet || method == http.MethodPut
}

// send sends a request, retrying a transient failure when retry allows it. The
// error it returns is the last failure, so a run that gives up still reports
// what went wrong rather than that it ran out of attempts.
func (c *Client) send(ctx context.Context, method, u string, body io.Reader, out any, retry bool) error {
	attempts := 1
	if retry {
		attempts += c.Retries
	}
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = defaultBackoff
	}

	for attempt := 1; ; attempt++ {
		err := c.sendOnce(ctx, method, u, body, out)
		if !isTransient(err) || attempt >= attempts {
			return err
		}
		// A body that cannot be wound back cannot be sent again, so the
		// failure stands.
		if rewindErr := rewind(body); rewindErr != nil {
			return err
		}
		c.logf("%v; trying again in %s (attempt %d of %d)", err, backoff, attempt+1, attempts)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// unavailable reports whether a status code says the API could not serve the
// request now, rather than that the request was wrong. OBS answers 502 and 503
// while it is restarting or overloaded, and 429 when a client asks too often.
func unavailable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return status >= 500
}

// rewind returns a request body to its start, so the request can be sent
// again.
func rewind(body io.Reader) error {
	if body == nil {
		return nil
	}
	seeker, ok := body.(io.Seeker)
	if !ok {
		return fmt.Errorf("a %T body cannot be sent again", body)
	}
	_, err := seeker.Seek(0, io.SeekStart)
	return err
}

// bodyLength returns how many bytes a request body still holds, or -1 when
// that cannot be known, which has the request sent in chunks.
func bodyLength(body io.Reader) int64 {
	switch b := body.(type) {
	case nil:
		return 0
	case *os.File:
		info, err := b.Stat()
		if err != nil {
			return -1
		}
		at, err := b.Seek(0, io.SeekCurrent)
		if err != nil {
			return -1
		}
		return info.Size() - at
	case *strings.Reader:
		return int64(b.Len())
	case *bytes.Reader:
		return int64(b.Len())
	}
	return -1
}

// sendOnce sends a request and decodes an XML response into out, when given.
// Errors carry the summary OBS puts in its status document, never the
// credentials.
func (c *Client) sendOnce(ctx context.Context, method, u string, body io.Reader, out any) error {
	// The transport closes a body that is a ReadCloser, such as the open file
	// of an upload, and a closed file cannot be wound back and sent again.
	// Hiding the body behind a NopCloser keeps it the caller's to close, at
	// the cost of the length net/http would otherwise work out itself.
	var reqBody io.Reader
	if body != nil {
		reqBody = io.NopCloser(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return err
	}
	req.ContentLength = bodyLength(body)
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("Accept", "application/xml")
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		// The request never got an answer: the network, or the API, was in
		// the way. Unless it is this run that is over.
		if ctx.Err() != nil {
			return err
		}
		return transientError{err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		return transientError{fmt.Errorf("%s %s: reading the response: %w", method, req.URL.Path, err)}
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s %s: %w", method, req.URL.Path, ErrNotFound)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var status struct {
			Code    string `xml:"code,attr"`
			Summary string `xml:"summary"`
		}
		detail := strings.TrimSpace(string(data))
		if xml.Unmarshal(data, &status) == nil && status.Summary != "" {
			detail = status.Code + ": " + status.Summary
		}
		err := fmt.Errorf("%s %s: %s: %s", method, req.URL.Path, resp.Status, detail)
		if unavailable(resp.StatusCode) {
			return transientError{err}
		}
		return err
	}
	if out == nil {
		return nil
	}
	if err := xml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s: decoding response: %w", method, req.URL.Path, err)
	}
	return nil
}
