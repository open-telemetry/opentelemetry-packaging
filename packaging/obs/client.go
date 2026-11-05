// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package obs uploads, builds and releases packages on the Open Build Service
// through its HTTP API, the same requests osc sends, so CI needs neither osc
// nor its rpm Python bindings.
package obs

import (
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

// Client talks to an OBS API with HTTP basic authentication.
type Client struct {
	APIURL   string
	Username string
	Password string
	HTTP     *http.Client
}

// NewClient returns a client for apiURL. Uploads of the larger source archives
// take minutes, which the timeout allows for.
func NewClient(apiURL, username, password string) *Client {
	return &Client{
		APIURL:   strings.TrimSuffix(apiURL, "/"),
		Username: username,
		Password: password,
		HTTP:     &http.Client{Timeout: 15 * time.Minute},
	}
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
func (c *Client) Release(ctx context.Context, project, pkg string) error {
	return c.do(ctx, http.MethodPost, c.url(url.Values{"cmd": {"release"}}, "source", project, pkg), nil, nil)
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

// do sends a request and decodes an XML response into out, when given. Errors
// carry the summary OBS puts in its status document, never the credentials.
func (c *Client) do(ctx context.Context, method, u string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	if f, ok := body.(*os.File); ok {
		if info, err := f.Stat(); err == nil {
			req.ContentLength = info.Size()
		}
	}
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("Accept", "application/xml")
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
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
		return fmt.Errorf("%s %s: %s: %s", method, req.URL.Path, resp.Status, detail)
	}
	if out == nil {
		return nil
	}
	if err := xml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s: decoding response: %w", method, req.URL.Path, err)
	}
	return nil
}
