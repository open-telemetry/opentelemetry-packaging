// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/goreleaser/nfpm/v2/files"
)

// stageUmask matches the nfpm default, so a staged tree gets the same
// permissions as the same content packaged directly by nfpm.
const stageUmask = 0o02

// ownedDirPrefixes lists project-owned trees that may be claimed in %files.
// Their distribution-owned parents must remain unclaimed to avoid conflicts.
var ownedDirPrefixes = []string{
	installDir,
	configDir,
}

// docDirParent is distribution-owned; only per-package children are claimed.
const docDirParent = "/usr/share/doc/"

// Stage materializes a component's payload under root and writes its RPM %files
// fragment to filelistDir/<component>.files.
//
// Like Build, it derives both outputs from ContentsFunc, keeping the staged
// payload and file list consistent. It calls the returned cleanup after staging.
func Stage(cfg Config, comp Component, root, filelistDir string) error {
	contents, cleanup, err := comp.ContentsFunc(cfg)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return fmt.Errorf("staging contents for %s: %w", comp.Name, err)
	}

	// Match nfpm's expansion of trees, globs, and implicit parent directories.
	prepared, err := files.PrepareForPackager(contents, stageUmask, "rpm", false, time.Now())
	if err != nil {
		return fmt.Errorf("preparing contents for %s: %w", comp.Name, err)
	}

	var lines []string
	for _, content := range prepared {
		line, err := stageContent(content, root)
		if err != nil {
			return fmt.Errorf("staging %s for %s: %w", content.Destination, comp.Name, err)
		}
		if line != "" {
			lines = append(lines, line)
		}
	}

	sort.Strings(lines)

	if err := os.MkdirAll(filelistDir, 0o755); err != nil {
		return err
	}
	listPath := filepath.Join(filelistDir, comp.Name+".files")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(listPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("writing file list %s: %w", listPath, err)
	}

	fmt.Printf("Staged %s: %d entries -> %s\n", comp.Name, len(lines), listPath)
	return nil
}

// stageContent writes a single content entry under root and returns the %files
// line describing it, or an empty string when the entry needs no line.
func stageContent(content *files.Content, root string) (string, error) {
	dst := content.Destination
	target := filepath.Join(root, filepath.FromSlash(dst))

	switch content.Type {
	case files.TypeImplicitDir:
		if err := os.MkdirAll(target, content.Mode()); err != nil {
			return "", err
		}
		// Distribution-owned parents must exist in the buildroot but remain
		// unclaimed; RPM permits unowned directories, though not unpackaged files.
		if !isOwnedDir(dst) {
			return "", nil
		}
		return dirLine(content), nil
	case files.TypeDir:
		if err := os.MkdirAll(target, content.Mode()); err != nil {
			return "", err
		}
		return dirLine(content), nil
	case files.TypeSymlink:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := os.Symlink(content.Source, target); err != nil && !os.IsExist(err) {
			return "", err
		}
		// A symlink has no meaningful mode of its own in RPM.
		return filelistPath(dst), nil
	case files.TypeConfig, files.TypeConfigNoReplace, files.TypeConfigMissingOK:
		if err := copyStagedFile(content, target); err != nil {
			return "", err
		}
		return configDirective(content.Type) + " " + attr(content) + " " + filelistPath(dst), nil
	case files.TypeFile, "":
		if err := copyStagedFile(content, target); err != nil {
			return "", err
		}
		return attr(content) + " " + filelistPath(dst), nil
	default:
		// Reject unused RPM-only types rather than silently omit their files.
		return "", fmt.Errorf("unsupported content type %q", content.Type)
	}
}

func copyStagedFile(content *files.Content, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := copyFile(content.Source, target); err != nil {
		return err
	}
	// copyFile preserves the source mode; the package mode is what counts.
	return os.Chmod(target, content.Mode())
}

func isOwnedDir(dst string) bool {
	clean := strings.TrimSuffix(dst, "/")
	for _, prefix := range ownedDirPrefixes {
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			return true
		}
	}
	// A per-package documentation directory sits one level below a
	// distribution-owned parent, so it is matched by name rather than by tree.
	if dir, name := path.Split(clean); dir == docDirParent && strings.HasPrefix(name, "opentelemetry") {
		return true
	}
	return false
}

func dirLine(content *files.Content) string {
	return "%dir " + attr(content) + " " + filelistPath(content.Destination)
}

func configDirective(contentType string) string {
	switch contentType {
	case files.TypeConfigNoReplace:
		return "%config(noreplace)"
	case files.TypeConfigMissingOK:
		return "%config(missingok)"
	default:
		return "%config"
	}
}

// attr renders %attr, defaulting ownership to root:root as nfpm does.
func attr(content *files.Content) string {
	owner := "root"
	group := "root"
	if content.FileInfo != nil {
		if content.FileInfo.Owner != "" {
			owner = content.FileInfo.Owner
		}
		if content.FileInfo.Group != "" {
			group = content.FileInfo.Group
		}
	}
	return fmt.Sprintf("%%attr(%04o,%s,%s)", content.Mode().Perm(), owner, group)
}

// filelistPath quotes whitespace and doubles percent signs so RPM parses the
// destination as one literal %files path.
func filelistPath(dst string) string {
	clean := strings.TrimSuffix(dst, "/")
	if clean == "" {
		clean = "/"
	}
	escaped := strings.ReplaceAll(clean, "%", "%%")
	if strings.ContainsAny(escaped, " \t") {
		return `"` + escaped + `"`
	}
	return escaped
}
