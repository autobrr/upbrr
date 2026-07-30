// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/autobrr/upbrr/internal/architecturepolicy"
)

func main() {
	os.Exit(run(os.Stdout, os.Stderr, os.Getwd, architecturepolicy.CheckRepository))
}

func run(
	stdout io.Writer,
	stderr io.Writer,
	getwd func() (string, error),
	check func(string) ([]architecturepolicy.Violation, error),
) int {
	root, err := getwd()
	if err != nil {
		fmt.Fprintf(stderr, "architecturepolicy: determine working directory: %v\n", err)
		return 1
	}
	violations, err := check(root)
	if err != nil {
		fmt.Fprintf(stderr, "architecturepolicy: %v\n", err)
		return 1
	}
	if len(violations) == 0 {
		fmt.Fprintln(stdout, "architecturepolicy: no issues found")
		return 0
	}
	for _, violation := range violations {
		fmt.Fprintf(stderr, "%s:%d:%d: %s\n", violation.File, violation.Line, violation.Column, violation.Message)
	}
	return 1
}
