// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// check-supported-python-versions verifies that the Python package can still
// be built for every interpreter in packaging/builder/supported_python_versions.json.
// It resolves packaging/common/python/requirements.txt for each supported
// version and architecture with the same pip flags the builder uses, and
// checks the requires-python of the vendored source requirements.
//
// Usage: check-supported-python-versions [-packaging-dir dir]
//
// Exit codes: 0 every supported version is buildable; 1 at least one is not,
// or the check itself failed.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/open-telemetry/opentelemetry-packaging/packaging/builder"
)

func main() {
	packagingDir := flag.String("packaging-dir", "packaging", "path to the packaging directory")
	flag.Parse()

	fmt.Printf("Supported Python versions: %s\n", strings.Join(builder.SupportedPythonVersions, ", "))
	problems, err := builder.CheckSupportedPythonVersions(*packagingDir, []string{"amd64", "arm64"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "The package can no longer be built for these supported Python versions:")
		for _, problem := range problems {
			fmt.Fprintf(os.Stderr, "  - %s\n", problem)
		}
		fmt.Fprintln(os.Stderr, "Either drop them from packaging/builder/supported_python_versions.json,"+
			" or revert the requirement that raised the minimum Python version.")
		os.Exit(1)
	}
	fmt.Println("Every supported Python version can be built.")
}
