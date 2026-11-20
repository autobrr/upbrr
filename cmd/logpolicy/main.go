package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/autobrr/upbrr/internal/logpolicy"
)

func main() {
	if len(os.Args) > 1 {
		os.Exit(runAdditions(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(run(os.Stdout, os.Stderr, os.Getwd, logpolicy.CheckRepository))
}

func runAdditions(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("logpolicy", flag.ContinueOnError)
	flags.SetOutput(stderr)
	base := flags.String("added-base", "", "full base commit SHA for added-statement analysis")
	head := flags.String("added-head", "", "full checked-out head commit SHA")
	source := flags.String("source", ".", "source checkout to analyze without executing it")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	sha := regexp.MustCompile(`^[0-9a-f]{40}$`)
	if flags.NArg() != 0 || !sha.MatchString(*base) || !sha.MatchString(*head) {
		fmt.Fprintln(stderr, "logpolicy: added counts require full --added-base and --added-head commits")
		return 1
	}
	root, err := filepath.Abs(*source)
	if err != nil {
		fmt.Fprintln(stderr, "logpolicy: cannot resolve source checkout")
		return 1
	}
	counts, err := logpolicy.CountAdded(context.Background(), root, *base, *head)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(counts); err != nil {
		fmt.Fprintln(stderr, "logpolicy: cannot write added counts")
		return 1
	}
	return 0
}

func run(stdout io.Writer, stderr io.Writer, getwd func() (string, error), check func(string) ([]logpolicy.Violation, error)) int {
	root, err := getwd()
	if err != nil {
		fmt.Fprintf(stderr, "logpolicy: determine working directory: %v\n", err)
		return 1
	}

	violations, err := check(root)
	if err != nil {
		fmt.Fprintf(stderr, "logpolicy: %v\n", err)
		return 1
	}
	if len(violations) == 0 {
		fmt.Fprintln(stdout, "logpolicy: no issues found")
		return 0
	}

	for _, violation := range violations {
		fmt.Fprintf(stderr, "%s:%d:%d: %s\n", violation.File, violation.Line, violation.Column, violation.Message)
	}
	return 1
}
