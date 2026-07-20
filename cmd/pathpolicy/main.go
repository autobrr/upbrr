package main

import (
	"fmt"
	"io"
	"os"

	pathpolicy "github.com/autobrr/upbrr/internal/pathing/policy"
)

func main() {
	os.Exit(run(os.Stdout, os.Stderr, os.Getwd, pathpolicy.CheckRepository))
}

func run(stdout io.Writer, stderr io.Writer, getwd func() (string, error), check func(string) ([]pathpolicy.Violation, error)) int {
	root, err := getwd()
	if err != nil {
		fmt.Fprintf(stderr, "pathpolicy: determine working directory: %v\n", err)
		return 1
	}

	violations, err := check(root)
	if err != nil {
		fmt.Fprintf(stderr, "pathpolicy: %v\n", err)
		return 1
	}
	if len(violations) == 0 {
		fmt.Fprintln(stdout, "pathpolicy: no issues found")
		return 0
	}

	for _, violation := range violations {
		fmt.Fprintf(stderr, "%s:%d:%d: %s\n", violation.File, violation.Line, violation.Column, violation.Message)
	}
	return 1
}
