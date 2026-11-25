// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	_ "embed"
	"fmt"
	"io"
	"strings"
	"text/template"

	"github.com/goreleaser/nfpm/v2"
)

var (
	//go:embed opentelemetry-deb.spec.tmpl
	debSpecTemplateSource string

	debSpecTemplate = template.Must(template.New("opentelemetry-deb.spec.tmpl").Parse(debSpecTemplateSource))
)

// DebNoarchDir is the payload directory of an architecture-independent
// component in the DEB source archive. It is the DEB architecture name such a
// package carries.
const DebNoarchDir = "all"

// debSpecData is the template input describing one component's spec.
type debSpecData struct {
	Name        string
	Version     string
	Release     string
	Summary     string
	Description string
	License     string
	URL         string
	Packager    string
	Section     string
	Noarch      bool
	// PayloadDir is the source archive directory that holds this build's
	// payload: a literal for noarch packages, the arch macro otherwise.
	PayloadDir string
	// PayloadArchs lists the architectures the source archive was staged for,
	// space separated for the ifnarch guard. Empty for a noarch package, whose
	// single payload serves every architecture.
	PayloadArchs string
	Component    string
	Provides     []string
	Requires     []string
	Recommends   []string
	Suggests     []string
	Conflicts    []string
	Replaces     []string
	Scriptlets   []specScriptlet
}

// DebVersionRelease returns the Version and Release fields of a debbuild spec.
//
// The version is the one nfpm writes into its DEB packages, a pre-release
// suffix after a tilde, so packages from both producers compare the same way.
// debbuild always joins the two fields with a hyphen, so the release cannot be
// empty the way nfpm allows; an explicit packaging revision wins over the
// default of 1. The Open Build Service replaces the Release field with its own
// commit and build counters, so there the revision grows with every commit and
// stays above the unrevised version nfpm writes.
func DebVersionRelease(cfg Config) (version, release string, err error) {
	v := strings.TrimPrefix(strings.TrimSpace(cfg.Version), "v")
	if v == "" {
		return "", "", fmt.Errorf("empty version")
	}

	info := nfpm.WithDefaults(&nfpm.Info{Version: v})
	version = info.Version
	if info.Prerelease != "" {
		version += "~" + info.Prerelease
	}
	if info.VersionMetadata != "" {
		version += "+" + info.VersionMetadata
	}

	release = cfg.Release
	if release == "" {
		release = "1"
	}
	return version, release, nil
}

// DebSourceArchiveName returns the file name of a component's source archive,
// which is also the spec's Source0.
func DebSourceArchiveName(comp Component, version string) string {
	return DebSourcePrefix(comp, version) + ".tar.gz"
}

// DebSourcePrefix returns the top directory of a component's source archive,
// where debbuild's setup macro expects it.
func DebSourcePrefix(comp Component, version string) string {
	return comp.PackageName + "-" + version
}

// WriteDebSpec renders the debbuild spec for one component.
//
// Every component gets its own spec, and therefore its own OBS package, so a
// component can be built and released on its own. Unlike WriteSpec, the spec
// builds nothing: the payload is staged ahead of time, by WriteDebSources, into
// the source archive, and the spec only copies it into place.
func WriteDebSpec(cfg Config, comp Component, w io.Writer) error {
	version, release, err := DebVersionRelease(cfg)
	if err != nil {
		return err
	}

	payloadDir := "%{_arch}"
	payloadArchs := strings.Join(debPayloadArchs(comp), " ")
	if comp.Noarch {
		payloadDir = DebNoarchDir
		payloadArchs = ""
	}

	scriptlets, err := componentScriptlets(cfg, comp)
	if err != nil {
		return err
	}

	data := &debSpecData{
		Name:    comp.PackageName,
		Version: version,
		Release: release,
		Summary: comp.Description,
		// debbuild has a Summary tag of its own, as rpm does, so the
		// description section holds the prose alone.
		Description:  comp.formatDescription("rpm"),
		License:      cfg.license(),
		URL:          cfg.homepage(),
		Packager:     cfg.maintainer(),
		Section:      cfg.section(),
		Noarch:       comp.Noarch,
		PayloadDir:   payloadDir,
		PayloadArchs: payloadArchs,
		Component:    comp.Name,
		Provides:     comp.Relations.Provides,
		Requires:     comp.Relations.Depends,
		Recommends:   comp.Relations.recommends("deb"),
		Suggests:     debbuildAlternatives(comp.Relations.suggests("deb")),
		Conflicts:    comp.Relations.Conflicts,
		Replaces:     comp.Relations.Replaces,
		Scriptlets:   scriptlets,
	}

	return debSpecTemplate.Execute(w, data)
}

// debbuildAlternatives lists each alternative of a "a | b" relation on its
// own. debbuild has no syntax for alternatives: it splits a relation list on
// commas only and keeps the last name of a "|" group, so the alternation the
// nfpm packages carry would come out as its final alternative alone. Only
// Suggests goes through this, where suggesting every alternative leaves the
// intent intact because a suggestion is never installed on its own; doing
// the same to Depends or Recommends would install all of them.
func debbuildAlternatives(relations []string) []string {
	var out []string
	for _, r := range relations {
		for _, alt := range strings.Split(r, "|") {
			out = append(out, strings.TrimSpace(alt))
		}
	}
	return out
}
