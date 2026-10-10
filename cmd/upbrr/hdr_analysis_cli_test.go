// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHDRCLIRejectsDependentAndConflictingFlags(t *testing.T) {
	cases := []struct {
		opts    cliOptions
		visited map[string]bool
	}{
		{cliOptions{}, map[string]bool{"hdr-peak-source": true}},
		{cliOptions{HDRAnalysis: true, HDRAnalysisOnly: true}, nil},
		{cliOptions{HDRAnalysis: true}, map[string]bool{"hdr-playlist": true}},
		{cliOptions{HDRAnalysisOnly: true, HDROutputDir: "output"}, map[string]bool{"hdr-targets": true}},
		{cliOptions{HDRAnalysisOnly: true, HDROutputDir: "output"}, map[string]bool{"debug": true}},
		{cliOptions{HDRAnalysisOnly: true}, nil},
		{cliOptions{HDRAnalysis: true, HDRPeakSource: "unknown"}, nil},
	}
	for _, test := range cases {
		if err := validateCLIHDR(test.opts, test.visited); err == nil {
			t.Fatalf("invalid flags accepted: %#v", test)
		}
	}
	if err := validateCLIHDR(cliOptions{HDRAnalysisOnly: true, HDROutputDir: "output"}, map[string]bool{"hdr-analysis-only": true, "unattended": true}); err != nil {
		t.Fatal(err)
	}
}

func TestHDROutputContainmentBeforeCreation(t *testing.T) {
	source := t.TempDir()
	for _, output := range []string{source, filepath.Join(source, "new", "nested")} {
		if _, err := hdrOutputParent(source, output, true); err == nil {
			t.Fatalf("source output %q accepted", output)
		}
	}
	if _, err := os.Stat(filepath.Join(source, "new")); !os.IsNotExist(err) {
		t.Fatal("validation created an output directory")
	}
	external := filepath.Join(t.TempDir(), "new", "nested")
	if _, err := hdrOutputParent(source, external, true); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "source-alias")
	if err := os.Symlink(source, alias); err == nil {
		if _, err := hdrOutputParent(source, filepath.Join(alias, "output"), true); err == nil {
			t.Fatal("source alias output accepted")
		}
	}
}

type hdrCLIProgressWriter func([]byte) (int, error)

func (w hdrCLIProgressWriter) Write(data []byte) (int, error) { return w(data) }

func TestHDRCLIOnlyPublishesCompleteOutput(t *testing.T) {
	for _, failSummary := range []bool{false, true} {
		name := "complete"
		if failSummary {
			name = "summary failure"
		}
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			var stdout bytes.Buffer
			stderr := io.Discard
			blockedSummary := false
			if failSummary {
				stderr = hdrCLIProgressWriter(func(data []byte) (int, error) {
					if strings.Contains(string(data), "HDR analysis: saving") {
						entries, err := os.ReadDir(parent)
						if err != nil || len(entries) != 1 {
							t.Fatalf("active attempt directory: entries=%v err=%v", entries, err)
						}
						if err := os.Mkdir(filepath.Join(parent, entries[0].Name(), "summary.json"), 0o700); err != nil {
							t.Fatal(err)
						}
						blockedSummary = true
					}
					return len(data), nil
				})
			}
			err := runHDRAnalysisOnly(t.Context(), cliOptions{HDRAnalysisOnly: true, HDROutputDir: parent},
				[]string{filepath.Join("testdata", "synthetic-cli-hdr.mkv")}, cliIO{out: &stdout, errOut: stderr})
			entries, readErr := os.ReadDir(parent)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if failSummary {
				if !blockedSummary || !errors.Is(err, os.ErrExist) || len(entries) != 0 || stdout.Len() != 0 {
					t.Fatalf("failed summary retained or reported partial output: blocked=%t err=%v entries=%v stdout=%q", blockedSummary, err, entries, stdout.String())
				}
				return
			}
			if err != nil || len(entries) != 1 || !strings.Contains(stdout.String(), "HDR analysis output:") {
				t.Fatalf("complete output: err=%v entries=%v stdout=%q", err, entries, stdout.String())
			}
			for _, artifact := range []string{"hdr10plus.png", "summary.json"} {
				info, err := os.Stat(filepath.Join(parent, entries[0].Name(), artifact))
				if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
					t.Fatalf("complete %s: info=%v err=%v", artifact, info, err)
				}
			}
		})
	}
}
