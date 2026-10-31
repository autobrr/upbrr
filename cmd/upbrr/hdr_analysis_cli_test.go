// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

func TestStandaloneHDRPlaylistUsesDefaultAngle(t *testing.T) {
	item := bd.PlayItem{Angles: []bd.Angle{{
		Source: video.Source{Kind: "m2ts", Path: "00001.M2TS"},
		STC:    &video.STCSequence{},
		Video: []video.Mapping{{
			Role:      video.Primary,
			Codec:     video.HEVC,
			EntryType: 1,
		}},
	}, {Err: errors.New("unsupported alternate")}}}
	result := bd.Result{Timelines: []bd.PlaylistTimeline{{
		Name:     "00001.MPLS",
		Complete: true,
		Items:    []bd.PlayItem{item},
	}}}
	if got, err := standaloneHDRPlaylist(result, "00001.MPLS"); err != nil || got != "00001.MPLS" {
		t.Fatalf("default angle playlist = %q, %v", got, err)
	}
	result.Timelines[0].Items[0].Angles = nil
	if _, err := standaloneHDRPlaylist(result, "00001.MPLS"); err == nil {
		t.Fatal("playlist without a default angle was accepted")
	}
}

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
	if err := validateCLIHDR(
		cliOptions{HDRAnalysisOnly: true, HDROutputDir: "output"},
		map[string]bool{"hdr-analysis-only": true, "unattended": true},
	); err != nil {
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

func TestHDRCLIRetainsCompletedImagesOnReportFailure(t *testing.T) {
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
			err := runHDRAnalysisOnly(t.Context(), cliOptions{
				HDRAnalysisOnly: true,
				HDRMetadataOnly: true,
				HDROutputDir:    parent,
			},
				[]string{filepath.Join("testdata", "synthetic-cli-hdr.mkv")}, cliIO{out: &stdout, errOut: stderr})
			entries, readErr := os.ReadDir(parent)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if failSummary {
				if !blockedSummary || !errors.Is(err, os.ErrExist) || len(entries) != 1 || stdout.Len() == 0 {
					t.Fatalf("failed summary lost completed output: blocked=%t err=%v entries=%v stdout=%q", blockedSummary, err, entries, stdout.String())
				}
				if image, imageErr := os.Stat(filepath.Join(parent, entries[0].Name(), "hdr10plus.png")); imageErr != nil || image.Size() == 0 {
					t.Fatalf("completed image missing: %v", imageErr)
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

func TestHDRCLIRenderCancellationRetainsMetadataReport(t *testing.T) {
	parent := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout bytes.Buffer
	stderr := hdrCLIProgressWriter(func(data []byte) (int, error) {
		if strings.Contains(string(data), "rendering") {
			cancel()
		}
		return len(data), nil
	})
	err := runHDRAnalysisOnly(ctx, cliOptions{HDRAnalysisOnly: true, HDROutputDir: parent},
		[]string{filepath.Join("testdata", "synthetic-cli-hdr.mkv")}, cliIO{out: &stdout, errOut: stderr})
	entries, readErr := os.ReadDir(parent)
	if !errors.Is(err, context.Canceled) || readErr != nil || len(entries) != 1 || stdout.Len() == 0 {
		t.Fatalf("canceled evidence: err=%v entries=%v read=%v stdout=%q", err, entries, readErr, stdout.String())
	}
	report, err := os.ReadFile(filepath.Join(parent, entries[0].Name(), "summary.json"))
	if err != nil || !bytes.Contains(report, []byte("render_canceled")) || !bytes.Contains(report, []byte(`"state":"complete"`)) ||
		!bytes.Contains(report, []byte(`"status":"canceled"`)) {
		t.Fatalf("canceled render lost native observations: report=%s err=%v", report, err)
	}
}
