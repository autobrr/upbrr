// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cliExecutionResult struct {
	stdout string
	stderr string
	err    error
	code   int
}

func executeCLIForTest(ctx context.Context, t *testing.T, args []string) cliExecutionResult {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := executeCLI(ctx, args, cliIO{
		in:     strings.NewReader(""),
		out:    &stdout,
		errOut: &stderr,
	})
	if err != nil {
		printCLIError(&stderr, err)
	}
	code := 0
	if err != nil {
		code = 1
		var exitErr *cliExitError
		if errors.As(err, &exitErr) {
			code = exitErr.code
		}
	}
	return cliExecutionResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
		err:    err,
		code:   code,
	}
}

func TestCommandHelpGoldens(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		golden string
	}{
		{
			name:   "root",
			args:   []string{"--help"},
			golden: "root.txt",
		},
		{
			name:   "serve",
			args:   []string{"serve", "--help"},
			golden: "serve.txt",
		},
		{
			name:   "api token",
			args:   []string{"api-token", "--help"},
			golden: "api-token.txt",
		},
		{
			name:   "api token list",
			args:   []string{"api-token", "list", "--help"},
			golden: "api-token-list.txt",
		},
		{
			name:   "api token revoke",
			args:   []string{"api-token", "revoke", "--help"},
			golden: "api-token-revoke.txt",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := executeCLIForTest(t.Context(), t, test.args)
			if result.code != 0 || result.err != nil || result.stderr != "" {
				t.Fatalf("help result: code=%d err=%v stderr=%q", result.code, result.err, result.stderr)
			}
			if want := readHelpGolden(t, test.golden); result.stdout != want {
				t.Fatalf("help output differs from %s\nwant:\n%s\ngot:\n%s", test.golden, want, result.stdout)
			}
		})
	}
}

func TestRootHelpSpellingsAndShortCircuitOrder(t *testing.T) {
	want := readHelpGolden(t, "root.txt")
	for _, args := range [][]string{
		{"-help"},
		{"--help"},
		{"-h"},
		{"--h"},
		{"--help", "--unknown"},
		{"--help=false", "--unknown"},
		{"Example.Release.2026.1080p-GRP", "--help", "--unknown"},
	} {
		result := executeCLIForTest(t.Context(), t, args)
		if result.code != 0 || result.stdout != want || result.stderr != "" {
			t.Fatalf("args %v: code=%d stdout_match=%t stderr=%q err=%v", args, result.code, result.stdout == want, result.stderr, result.err)
		}
	}

	result := executeCLIForTest(t.Context(), t, []string{"--unknown", "--help"})
	if result.code != 2 || !strings.Contains(result.stderr, "flag provided but not defined: -unknown") || result.stdout != "" {
		t.Fatalf("invalid-before-help result: %#v", result)
	}

	result = executeCLIForTest(t.Context(), t, []string{"---help"})
	if result.code != 2 || !strings.Contains(result.stderr, "bad flag syntax: ---help") || result.stdout != "" {
		t.Fatalf("malformed help result: %#v", result)
	}
}

func TestAPITokenFirstTokenDispatch(t *testing.T) {
	wantHelp := readHelpGolden(t, "api-token.txt")
	for _, args := range [][]string{
		{"api-token"},
		{"api-token", "help", "create"},
		{"api-token", "-h", "create"},
		{"api-token", "--h", "create"},
		{"api-token", "-help", "create"},
		{"api-token", "--help", "create"},
	} {
		result := executeCLIForTest(t.Context(), t, args)
		if result.code != 0 || result.stdout != wantHelp || result.stderr != "" {
			t.Fatalf("args %v: code=%d stdout_match=%t stderr=%q err=%v", args, result.code, result.stdout == wantHelp, result.stderr, result.err)
		}
	}

	result := executeCLIForTest(t.Context(), t, []string{"api-token", "bad", "--help"})
	if result.code != 2 || result.stdout != "" || !strings.Contains(result.stderr, `unknown api-token command "bad"`) {
		t.Fatalf("unknown API-token command result: %#v", result)
	}
}

func TestCommandExitCodesAndRouting(t *testing.T) {
	missingConfig := filepath.Join(t.TempDir(), "missing.yaml")
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantError  string
	}{
		{
			name:      "root no args",
			wantCode:  2,
			wantError: "at least one input path is required",
		},
		{
			name:      "root unknown flag",
			args:      []string{"--typo"},
			wantCode:  2,
			wantError: "parse CLI options: flag provided but not defined: -typo",
		},
		{
			name:      "root removed dry run",
			args:      []string{"--dry-run"},
			wantCode:  2,
			wantError: "flag provided but not defined: -dry-run",
		},
		{
			name:      "root unregistered shorthand",
			args:      []string{"-v"},
			wantCode:  2,
			wantError: "flag provided but not defined: -v",
		},
		{
			name:      "serve parse",
			args:      []string{"serve", "--typo"},
			wantCode:  1,
			wantError: "parse serve options: flag provided but not defined: -typo",
		},
		{
			name:      "serve positional",
			args:      []string{"serve", "accidental-value"},
			wantCode:  1,
			wantError: `unknown command "accidental-value" for "upbrr serve"`,
		},
		{
			name:      "API token positional",
			args:      []string{"api-token", "list", "extra"},
			wantCode:  2,
			wantError: "api-token list does not accept positional arguments",
		},
		{
			name:      "API token trailing flag is positional",
			args:      []string{"api-token", "revoke", "TOKEN_ID", "--config", "path"},
			wantCode:  2,
			wantError: "api-token revoke requires exactly one token ID",
		},
		{
			name:      "leading flag keeps root route",
			args:      []string{"--config", missingConfig, "serve"},
			wantCode:  1,
			wantError: "read provided config",
		},
		{
			name:       "version ignores command word",
			args:       []string{"--version", "serve"},
			wantCode:   0,
			wantStdout: "upbrr " + version + "\n",
		},
		{
			name:       "serve after path stays root",
			args:       []string{"Example.Release.2026.1080p-GRP", "serve", "--version"},
			wantCode:   0,
			wantStdout: "upbrr " + version + "\n",
		},
		{
			name:       "completion stays root path",
			args:       []string{"completion", "--version"},
			wantCode:   0,
			wantStdout: "upbrr " + version + "\n",
		},
		{
			name:       "hidden completion stays root path",
			args:       []string{"__complete", "--version"},
			wantCode:   0,
			wantStdout: "upbrr " + version + "\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := executeCLIForTest(t.Context(), t, test.args)
			if result.code != test.wantCode {
				t.Fatalf("code=%d, want %d; err=%v stderr=%q", result.code, test.wantCode, result.err, result.stderr)
			}
			if result.stdout != test.wantStdout {
				t.Fatalf("stdout=%q, want %q", result.stdout, test.wantStdout)
			}
			if test.wantError != "" && !strings.Contains(result.stderr, test.wantError) {
				t.Fatalf("stderr=%q, want substring %q", result.stderr, test.wantError)
			}
		})
	}
}

func TestNonRootHelpStopsAtFirstPositional(t *testing.T) {
	result := executeCLIForTest(t.Context(), t, []string{"api-token", "revoke", "TOKEN_ID", "--help"})
	if result.code != 2 || result.stdout != "" || !strings.Contains(result.stderr, "requires exactly one token ID") {
		t.Fatalf("result: %#v", result)
	}
}

func TestCommandContextReachesAPITokenHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := executeCLIForTest(ctx, t, []string{"api-token", "list"})
	if result.code != 1 || !strings.Contains(result.stderr, "context canceled") {
		t.Fatalf("result: %#v", result)
	}
}

func TestCommandFactoriesDoNotLeakFlagState(t *testing.T) {
	first := executeCLIForTest(t.Context(), t, []string{"--version"})
	second := executeCLIForTest(t.Context(), t, []string{"--version=false"})
	if first.code != 0 || first.stdout != "upbrr "+version+"\n" {
		t.Fatalf("first execution: %#v", first)
	}
	if second.stdout != "" || second.code == 0 {
		t.Fatalf("second execution retained version state: %#v", second)
	}
}

func readHelpGolden(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "help", name))
	if err != nil {
		t.Fatalf("read help golden %s: %v", name, err)
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text
}
