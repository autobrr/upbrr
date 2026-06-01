package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/pathpolicy"
)

func TestRunPrintsSuccessWhenNoIssuesFound(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, func() (string, error) {
		return "repo", nil
	}, func(root string) ([]pathpolicy.Violation, error) {
		if root != "repo" {
			t.Fatalf("unexpected root: %s", root)
		}
		return nil, nil
	})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if got := stdout.String(); got != "pathpolicy: no issues found\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestRunPrintsViolationsToStderr(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, func() (string, error) {
		return "repo", nil
	}, func(string) ([]pathpolicy.Violation, error) {
		return []pathpolicy.Violation{{
			File:    "internal/example_test.go",
			Line:    12,
			Column:  4,
			Message: "bad path",
		}}, nil
	})

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); got != "internal/example_test.go:12:4: bad path\n" {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestRunPrintsCheckerErrorsToStderr(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, func() (string, error) {
		return "repo", nil
	}, func(string) ([]pathpolicy.Violation, error) {
		return nil, errors.New("boom")
	})

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); !strings.Contains(got, "pathpolicy: boom\n") {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestRunPrintsGetwdErrorsToStderr(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, func() (string, error) {
		return "", errors.New("cwd")
	}, func(string) ([]pathpolicy.Violation, error) {
		return nil, nil
	})

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); !strings.Contains(got, "pathpolicy: determine working directory: cwd\n") {
		t.Fatalf("unexpected stderr: %q", got)
	}
}
