// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// bundledComponent is one software component physically included in a language
// package. It deliberately contains only identity data that can be derived from
// the staged payload or an existing release pin.
type bundledComponent struct {
	Name    string
	Version string
}

type cycloneDXBOM struct {
	BOMFormat   string               `json:"bomFormat"`
	SpecVersion string               `json:"specVersion"`
	Version     int                  `json:"version"`
	Metadata    cycloneDXMetadata    `json:"metadata"`
	Components  []cycloneDXComponent `json:"components"`
}

type cycloneDXMetadata struct {
	Component cycloneDXComponent `json:"component"`
}

type cycloneDXComponent struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// writeCycloneDXBOM writes a deterministic CycloneDX 1.7 component inventory.
// Timestamps and serial numbers are intentionally omitted: the same staged
// payload should produce byte-for-byte identical package contents.
func writeCycloneDXBOM(path, packageName, packageVersion string, components []bundledComponent) error {
	components = normalizeBundledComponents(components)

	cdxComponents := make([]cycloneDXComponent, 0, len(components))
	for _, component := range components {
		cdxComponents = append(cdxComponents, cycloneDXComponent{
			Type:    "library",
			Name:    component.Name,
			Version: component.Version,
		})
	}

	bom := cycloneDXBOM{
		BOMFormat:   "CycloneDX",
		SpecVersion: "1.7",
		Version:     1,
		Metadata: cycloneDXMetadata{
			Component: cycloneDXComponent{
				Type:    "application",
				Name:    packageName,
				Version: packageVersion,
			},
		},
		Components: cdxComponents,
	}

	data, err := json.MarshalIndent(bom, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding CycloneDX BOM: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing CycloneDX BOM: %w", err)
	}
	return nil
}

func normalizeBundledComponents(components []bundledComponent) []bundledComponent {
	seen := make(map[string]struct{}, len(components))
	result := make([]bundledComponent, 0, len(components))

	for _, component := range components {
		if component.Name == "" || component.Version == "" {
			continue
		}
		key := component.Name + "\x00" + component.Version
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, component)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Version < result[j].Version
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func releaseBundledComponent(cfg Config, componentDir, name string) ([]bundledComponent, error) {
	version, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "common", componentDir, "release.txt"))
	if err != nil {
		return nil, err
	}
	return []bundledComponent{{
		Name:    name,
		Version: strings.TrimPrefix(version, "v"),
	}}, nil
}

// nodeModulesComponents inventories the package roots npm installed under
// node_modules. Walking package roots rather than every package.json avoids
// mistaking fixtures or examples bundled inside a dependency for dependencies
// of the shipped Node.js distribution.
func nodeModulesComponents(installDir string) ([]bundledComponent, error) {
	var components []bundledComponent
	if err := collectNodeModules(filepath.Join(installDir, "node_modules"), &components); err != nil {
		return nil, err
	}
	return normalizeBundledComponents(components), nil
}

func collectNodeModules(nodeModulesDir string, components *[]bundledComponent) error {
	entries, err := os.ReadDir(nodeModulesDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", nodeModulesDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".bin" {
			continue
		}

		entryPath := filepath.Join(nodeModulesDir, entry.Name())
		if strings.HasPrefix(entry.Name(), "@") {
			scopedEntries, err := os.ReadDir(entryPath)
			if err != nil {
				return fmt.Errorf("reading npm scope %s: %w", entryPath, err)
			}
			for _, scopedEntry := range scopedEntries {
				if !scopedEntry.IsDir() {
					continue
				}
				if err := collectNodePackage(filepath.Join(entryPath, scopedEntry.Name()), components); err != nil {
					return err
				}
			}
			continue
		}

		if err := collectNodePackage(entryPath, components); err != nil {
			return err
		}
	}
	return nil
}

func collectNodePackage(packageDir string, components *[]bundledComponent) error {
	data, err := os.ReadFile(filepath.Join(packageDir, "package.json"))
	if err != nil {
		return fmt.Errorf("reading npm package metadata in %s: %w", packageDir, err)
	}

	var metadata struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("decoding npm package metadata in %s: %w", packageDir, err)
	}
	if metadata.Name == "" || metadata.Version == "" {
		return fmt.Errorf("npm package metadata in %s is missing name or version", packageDir)
	}

	*components = append(*components, bundledComponent{
		Name:    metadata.Name,
		Version: metadata.Version,
	})

	return collectNodeModules(filepath.Join(packageDir, "node_modules"), components)
}

// pythonDistInfoComponents inventories the distributions installed by pip.
// dist-info METADATA is the same source already used by all-dependencies.txt,
// keeping the runtime conflict manifest and the package BOM in agreement.
func pythonDistInfoComponents(installDir string) ([]bundledComponent, error) {
	entries, err := os.ReadDir(installDir)
	if err != nil {
		return nil, fmt.Errorf("reading Python install directory: %w", err)
	}

	var components []bundledComponent
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), ".dist-info") {
			continue
		}

		metadataPath := filepath.Join(installDir, entry.Name(), "METADATA")
		data, err := os.ReadFile(metadataPath)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", metadataPath, err)
		}
		name, version := parseMetadata(string(data))
		if name == "" || version == "" {
			return nil, fmt.Errorf("Python distribution metadata in %s is missing name or version", metadataPath)
		}
		components = append(components, bundledComponent{
			Name:    name,
			Version: version,
		})
	}

	return normalizeBundledComponents(components), nil
}
