// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package changes

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelect(t *testing.T) {
	components := []string{"injector", "java", "meta"}
	shared := append([]string{"cmd/tool"}, PackageInputs...)
	for _, tc := range []struct {
		name  string
		paths []string
		want  []string
	}{
		{"no changes", nil, []string{}},
		{"one pin", []string{"packaging/common/java/release.txt"}, []string{"java"}},
		{"kept in order", []string{"packaging/common/meta/release.txt", "packaging/common/injector/release.txt"}, []string{"injector", "meta"}},
		{"package input", []string{"packaging/builder/components.go"}, components},
		{"target input", []string{"cmd/tool/main.go"}, components},
		{"notice", []string{"NOTICE"}, components},
		{"go toolchain", []string{".go-version"}, components},
		{"unknown component", []string{"packaging/common/dotnet/release.txt", "README.md"}, []string{}},
		{"prefix collision", []string{"packaging/common/java-other/release.txt", "cmd/toolbox/main.go"}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, Select(components, shared, tc.paths))
		})
	}
}

func TestSelectDoesNotAliasComponents(t *testing.T) {
	components := []string{"injector", "java"}
	selected := Select(components, PackageInputs, []string{"go.mod"})
	selected[0] = "changed"
	require.Equal(t, "injector", components[0])
}

func TestInputs(t *testing.T) {
	shared := []string{"go.mod", "packaging/builder"}
	require.Equal(t, []string{"go.mod", "packaging/builder", "packaging/common/java"}, Inputs("java", shared))
	require.Equal(t, []string{"go.mod", "packaging/builder"}, shared, "Inputs must not append into the caller's slice")
}
