// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// DebSources are the files of one component's OBS package.
type DebSources struct {
	Spec    string // Path to the generated spec
	Archive string // Path to the source archive it names as Source0
}

// WriteDebSources writes a component's debbuild spec and source archive into
// outputDir, ready to be committed to an OBS package.
//
// The archive holds one staged payload per architecture in Architectures, each
// under <prefix>/<arch>/root with its file list beside it, or a single one
// under <prefix>/all for an architecture-independent component. cfg.Arch is
// ignored. configCheckBinary returns the otel-config-check binary built for an
// architecture, which the Python payload bundles.
func WriteDebSources(cfg Config, comp Component, configCheckBinary func(arch string) string, outputDir string) (*DebSources, error) {
	version, _, err := DebVersionRelease(cfg)
	if err != nil {
		return nil, err
	}

	workDir, err := os.MkdirTemp("", "otel-deb-sources-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	prefix := DebSourcePrefix(comp, version)
	for _, arch := range debPayloadArchs(comp) {
		archCfg := cfg
		archCfg.Arch = arch
		archCfg.ConfigCheckBinary = configCheckBinary(arch)

		dir := filepath.Join(workDir, prefix, debPayloadDir(comp, arch))
		if err := StageDeb(archCfg, comp, filepath.Join(dir, "root"), dir); err != nil {
			return nil, err
		}
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, err
	}

	sources := &DebSources{
		Spec:    filepath.Join(outputDir, comp.PackageName+".spec"),
		Archive: filepath.Join(outputDir, DebSourceArchiveName(comp, version)),
	}
	if err := writeFileWith(sources.Spec, func(w io.Writer) error {
		return WriteDebSpec(cfg, comp, w)
	}); err != nil {
		return nil, fmt.Errorf("writing spec for %s: %w", comp.Name, err)
	}
	if err := writeFileWith(sources.Archive, func(w io.Writer) error {
		return writeTarGz(w, workDir, prefix)
	}); err != nil {
		return nil, fmt.Errorf("writing source archive for %s: %w", comp.Name, err)
	}
	return sources, nil
}

// debPayloadArchs returns the architectures a component is staged for. They
// are DEB architecture names, which is what debbuild expands %{_arch} to. An
// architecture-independent payload is the same for every one, so it is staged
// once.
func debPayloadArchs(comp Component) []string {
	if comp.Noarch {
		return Architectures[:1]
	}
	return Architectures
}

func debPayloadDir(comp Component, arch string) string {
	if comp.Noarch {
		return DebNoarchDir
	}
	return arch
}

// writeFileWith creates path and fills it with write, removing it again when
// either step fails so no partial output is left behind.
func writeFileWith(path string, write func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

// writeTarGz archives baseDir/top as a gzipped tar whose entries start with
// top/. Entries are written in lexical order, owned by root, and all carry the
// time sourceDateEpoch returns, so with SOURCE_DATE_EPOCH set identical
// payloads give identical archives and an unchanged component is no change to
// commit.
func writeTarGz(w io.Writer, baseDir, top string) error {
	mtime, err := sourceDateEpoch()
	if err != nil {
		return err
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	err = filepath.WalkDir(filepath.Join(baseDir, top), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uid, hdr.Gid = 0, 0
		hdr.Uname, hdr.Gname = "root", "root"
		hdr.ModTime = mtime
		// Only the modification time is reproducible; the others would make
		// the header PAX and differ between runs.
		hdr.AccessTime, hdr.ChangeTime = time.Time{}, time.Time{}

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// sourceDateEpoch returns the time every archive entry records.
//
// SOURCE_DATE_EPOCH is the reproducible-builds convention for the timestamp a
// build should record instead of the current time, as seconds since the Unix
// epoch (https://reproducible-builds.org/specs/source-date-epoch/). nfpm reads
// it for the same purpose. Set it where the archive is committed to OBS: a
// changed archive is a new OBS revision and a rebuild even when the payload is
// the same. Unset, the archive records the current time, which is fine for a
// local build, so it only warns.
func sourceDateEpoch() (time.Time, error) {
	v := os.Getenv("SOURCE_DATE_EPOCH")
	if v == "" {
		fmt.Fprintln(os.Stderr, "warning: SOURCE_DATE_EPOCH is not set; the source archive records the current time and is not reproducible")
		return time.Now().UTC().Truncate(time.Second), nil
	}
	secs, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid SOURCE_DATE_EPOCH %q: %w", v, err)
	}
	return time.Unix(secs, 0).UTC(), nil
}
