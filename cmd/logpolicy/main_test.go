package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/logpolicy"
)

func TestRunPrintsSuccessWhenNoIssuesFound(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, func() (string, error) {
		return "repo", nil
	}, func(root string) ([]logpolicy.Violation, error) {
		if root != "repo" {
			t.Fatalf("unexpected root: %s", root)
		}
		return nil, nil
	})

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if got := stdout.String(); got != "logpolicy: no issues found\n" {
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
	}, func(string) ([]logpolicy.Violation, error) {
		return []logpolicy.Violation{{
			File:    "internal/example.go",
			Line:    12,
			Column:  4,
			Message: "bad log",
		}}, nil
	})

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); got != "internal/example.go:12:4: bad log\n" {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestRunPrintsCheckerErrorsToStderr(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, func() (string, error) {
		return "repo", nil
	}, func(string) ([]logpolicy.Violation, error) {
		return nil, errors.New("boom")
	})

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); !strings.Contains(got, "logpolicy: boom\n") {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestRunPrintsGetwdErrorsToStderr(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(&stdout, &stderr, func() (string, error) {
		return "", errors.New("cwd")
	}, func(string) ([]logpolicy.Violation, error) {
		return nil, nil
	})

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); !strings.Contains(got, "logpolicy: determine working directory: cwd\n") {
		t.Fatalf("unexpected stderr: %q", got)
	}
}