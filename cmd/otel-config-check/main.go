// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// otel-config-check validates an OpenTelemetry declarative configuration file
// for use with the Python auto-instrumentation package. It is shipped inside
// the package and invoked by sitecustomize.py when OTEL_CONFIG_FILE is set,
// so that misconfigurations self-deactivate instrumentation with a clear
// message instead of crashing the SDK's file configurator at startup — and so
// that no YAML parser has to be added to the Python bundle for validation.
//
// Usage: otel-config-check <config-file>
//
// Exit codes: 0 the file is usable; 1 validation failed (a human-readable
// reason is printed to stdout); 2 usage error.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: otel-config-check <config-file>")
		os.Exit(2)
	}
	warning, err := checkFile(os.Args[1])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if warning != "" {
		fmt.Fprintln(os.Stderr, "warning: "+warning)
	}
}

// checkFile validates the declarative configuration file at path.
func checkFile(path string) (warning string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read the configuration file: %v", err)
	}
	return checkConfig(data)
}

// supportedFileFormat is the declarative configuration file format the
// packaged agents support.
const supportedFileFormat = "1.0"

// checkConfig validates the declarative configuration document. The checks
// mirror the failure modes observed with the Python SDK's file configurator:
// unparsable YAML and a missing or unsupported file_format crash it at
// startup, and the otlp_grpc exporter type resolves to the gRPC exporter
// package that this system package does not bundle.
func checkConfig(data []byte) (warning string, err error) {
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		// Dialect divergence: this parser rejects duplicate mapping keys,
		// but PyYAML — the parser the SDK actually uses at runtime — accepts
		// them (the last value wins). Rejecting here would deactivate
		// instrumentation over a file the SDK can load, so accept with a
		// warning; the decode aborts on the first duplicate, so the
		// remaining checks cannot run on such a file.
		if isDuplicateKeyOnly(err) {
			return "the configuration file contains duplicate mapping keys; validation skipped (the SDK parser accepts duplicates, the last value wins)", nil
		}
		return "", fmt.Errorf("not valid YAML: %v", firstLine(err.Error()))
	}
	if root == nil {
		return "", fmt.Errorf("the configuration file is empty")
	}
	format, ok := root["file_format"]
	if !ok {
		return "", fmt.Errorf(`missing the required top-level "file_format" key (e.g. file_format: "1.0")`)
	}
	formatString, ok := format.(string)
	if !ok {
		// An unquoted 1.0 parses as a YAML float and the SDK schemas reject it.
		return "", fmt.Errorf(`the "file_format" value must be the string "1.0" (quote it in YAML), got: %v`, format)
	}
	if formatString != supportedFileFormat {
		return "", fmt.Errorf(`unsupported file_format %q: the packaged agents support declarative configuration file format %q`,
			formatString, supportedFileFormat)
	}
	if path := findKey(root, "otlp_grpc", nil); path != nil {
		return "", fmt.Errorf(
			"%s selects the otlp_grpc exporter, but this package bundles only the OTLP/HTTP exporter; use otlp_http instead",
			strings.Join(path, "."))
	}
	return "", nil
}

// findKey walks the document depth-first and returns the path at which key
// occurs as a mapping key, or nil if it does not occur.
func findKey(node any, key string, path []string) []string {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			childPath := append(append([]string(nil), path...), k)
			if k == key {
				return childPath
			}
			if found := findKey(child, key, childPath); found != nil {
				return found
			}
		}
	case []any:
		for i, child := range v {
			childPath := append(append([]string(nil), path...), fmt.Sprintf("%d", i))
			if found := findKey(child, key, childPath); found != nil {
				return found
			}
		}
	}
	return nil
}

// isDuplicateKeyOnly reports whether the unmarshal error consists solely of
// duplicate-mapping-key complaints (yaml.TypeError with "already defined"
// messages).
func isDuplicateKeyOnly(err error) bool {
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) || len(typeErr.Errors) == 0 {
		return false
	}
	for _, msg := range typeErr.Errors {
		if !strings.Contains(msg, "already defined") {
			return false
		}
	}
	return true
}

// firstLine truncates multi-line YAML parser errors to their first line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
