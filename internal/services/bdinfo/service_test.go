// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdinfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	bdrunner "github.com/autobrr/go-bdinfo/pkg/bdinfo"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestNew(t *testing.T) {
	svc := New(api.NopLogger{})
	if svc == nil {
		t.Fatal("expected service, got nil")
	}
}

func TestParseOutput(t *testing.T) {
	svc := New(api.NopLogger{})

	// Create a temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test_bdinfo.txt")

	content := `Disc Title: Test Disc
Disc Label: TESTLABEL123
Disc Size: 50,000,000,000 bytes
Length: 2:30:15.800

QUICK SUMMARY:
Video: MPEG-2 / 8000 kbps / 1920x1080 / 23.976 fps / 16:9 / High Profile / 8-bit / SDR / YUV
Audio: English / AC3 / 2.0 / 384 kbps / 48 kHz

********************`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	result, err := svc.ParseOutput(testFile)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if title, ok := result["title"].(string); !ok || title != "Test Disc" {
		t.Errorf("expected title 'Test Disc', got %v", result["title"])
	}

	if label, ok := result["label"].(string); !ok || label != "TESTLABEL123" {
		t.Errorf("expected label 'TESTLABEL123', got %v", result["label"])
	}

	if summary, ok := result["summary"].(string); !ok || !strings.Contains(summary, "MPEG-2") {
		t.Errorf("expected summary with MPEG-2, got %v", result["summary"])
	}
}

func TestParseOutputMissingFields(t *testing.T) {
	svc := New(api.NopLogger{})

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test_bdinfo_minimal.txt")

	// Minimal valid output
	content := `Some content
without specific fields`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	result, err := svc.ParseOutput(testFile)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	// Result should be a map but may be empty or have partial fields
	if result == nil {
		t.Error("expected non-nil result map")
	}
}

func TestContextCancellation(t *testing.T) {
	svc := New(api.NopLogger{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// ExecuteForPlaylist should respect context cancellation
	_, err := svc.ExecuteForPlaylist(ctx, "/nonexistent", "00001.mpls", "/nonexistent/BD_SUMMARY_00001.MPLS.txt", true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestExecuteForPlaylistUsesInProcessRunner(t *testing.T) {
	tmpDir := t.TempDir()
	expectedOutput := filepath.Join(tmpDir, "out", "BD_SUMMARY_00001.MPLS.txt")

	var captured runRequest
	originalRunner := runBDInfo
	runBDInfo = func(_ context.Context, req runRequest) (bdrunner.Result, error) {
		captured = req
		return bdrunner.Result{
			Report:     "QUICK SUMMARY:\n********************\n",
			ReportPath: req.ReportPath,
		}, nil
	}
	t.Cleanup(func() {
		runBDInfo = originalRunner
	})

	svc := New(api.NopLogger{})
	reported := false
	ctx := WithProgressReporter(context.Background(), func(_ string) {
		reported = true
	})
	outputPath, err := svc.ExecuteForPlaylist(ctx, `D:\Media\Movie\BDMV`, "00001", expectedOutput, true)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if outputPath != expectedOutput {
		t.Fatalf("expected output %s, got %s", expectedOutput, outputPath)
	}

	if captured.BDMVPath != `D:\Media\Movie\BDMV` {
		t.Fatalf("expected bdmv path to be forwarded, got: %s", captured.BDMVPath)
	}
	if captured.PlaylistName != "00001.MPLS" {
		t.Fatalf("expected normalized playlist, got: %s", captured.PlaylistName)
	}
	if captured.ReportPath != expectedOutput {
		t.Fatalf("expected output path in runner request, got: %s", captured.ReportPath)
	}
	if !captured.SummaryOnly {
		t.Fatalf("expected summary-only mode for playlist scans")
	}
	if captured.Reporter == nil {
		t.Fatalf("expected reporter to be passed to runner")
	}
	if reported {
		t.Fatalf("reporter should not be invoked by stubbed runner")
	}
}

func TestExecuteFullScanUsesFullReportMode(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "out")
	expectedOutput := filepath.Join(outputDir, "BD_FULL.txt")

	var captured runRequest
	originalRunner := runBDInfo
	runBDInfo = func(_ context.Context, req runRequest) (bdrunner.Result, error) {
		captured = req
		return bdrunner.Result{
			Report:     "full report content",
			ReportPath: req.ReportPath,
		}, nil
	}
	t.Cleanup(func() {
		runBDInfo = originalRunner
	})

	svc := New(api.NopLogger{})
	result, err := svc.ExecuteFullScan(context.Background(), `D:\Media\Movie\BDMV`, outputDir)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if result.ReportPath != expectedOutput {
		t.Fatalf("expected output %s, got %s", expectedOutput, result.ReportPath)
	}
	if result.ReportText != "full report content" {
		t.Fatalf("unexpected report text: %q", result.ReportText)
	}
	if captured.PlaylistName != "" {
		t.Fatalf("expected no playlist filter for full scan, got %q", captured.PlaylistName)
	}
	if captured.SummaryOnly {
		t.Fatalf("expected full report mode for multi-playlist scans")
	}
}

func TestExecuteForPlaylistNormalizesLowercaseAndPathPlaylist(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name         string
		playlistFile string
		want         string
	}{
		{
			name:         "lowercase extension",
			playlistFile: "00001.mpls",
			want:         "00001.MPLS",
		},
		{
			name:         "full windows path",
			playlistFile: `D:\Disc\BDMV\PLAYLIST\00002.mpls`,
			want:         "00002.MPLS",
		},
		{
			name:         "full slash path no ext",
			playlistFile: "BDMV/PLAYLIST/00003",
			want:         "00003.MPLS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured runRequest
			originalRunner := runBDInfo
			runBDInfo = func(_ context.Context, req runRequest) (bdrunner.Result, error) {
				captured = req
				return bdrunner.Result{
					Report:     "QUICK SUMMARY:\n********************\n",
					ReportPath: req.ReportPath,
				}, nil
			}
			t.Cleanup(func() {
				runBDInfo = originalRunner
			})

			svc := New(api.NopLogger{})
			outputPath := filepath.Join(tmpDir, tc.want+".txt")
			if _, err := svc.ExecuteForPlaylist(context.Background(), `D:\Media\Movie\BDMV`, tc.playlistFile, outputPath, true); err != nil {
				t.Fatalf("execute failed: %v", err)
			}

			if captured.PlaylistName != tc.want {
				t.Fatalf("expected playlist %s, got %s", tc.want, captured.PlaylistName)
			}
		})
	}
}

func TestNormalizePlaylistSelector(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "trim and add extension",
			input: " 00001 ",
			want:  "00001.MPLS",
		},
		{
			name:  "preserve extension uppercase",
			input: "00001.MPLS",
			want:  "00001.MPLS",
		},
		{
			name:  "normalize lowercase extension",
			input: "00001.mpls",
			want:  "00001.MPLS",
		},
		{
			name:  "strip windows path",
			input: `D:\Movie\BDMV\PLAYLIST\00004.mpls`,
			want:  "00004.MPLS",
		},
		{
			name:  "strip slash path",
			input: "BDMV/PLAYLIST/00005",
			want:  "00005.MPLS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizePlaylistSelector(tc.input)
			if got != tc.want {
				t.Fatalf("expected %s, got %s", tc.want, got)
			}
		})
	}
}

func TestEmitProgressEventIncludesNewStages(t *testing.T) {
	var lines []string
	reporter := func(line string) {
		lines = append(lines, line)
	}

	emitProgressEvent(reporter, bdrunner.ProgressEvent{Stage: bdrunner.StageScanComplete})
	emitProgressEvent(reporter, bdrunner.ProgressEvent{Stage: bdrunner.StageRenderingReport})

	if !slices.Contains(lines, "Scan phase complete") {
		t.Fatalf("expected scan complete stage message, got %#v", lines)
	}
	if !slices.Contains(lines, "Rendering report") {
		t.Fatalf("expected rendering stage message, got %#v", lines)
	}
}

func TestEmitDetailedProgressEventFormatsCounts(t *testing.T) {
	var lines []string
	reporter := func(line string) {
		lines = append(lines, line)
	}

	emitDetailedProgressEvent(reporter, bdrunner.ProgressEvent{
		Stage:          bdrunner.StageStream,
		Completed:      4,
		Total:          10,
		ProcessedBytes: 500,
		TotalBytes:     1000,
	})

	if len(lines) != 1 {
		t.Fatalf("expected one line, got %d", len(lines))
	}
	if lines[0] != "STREAM: 4/10 (50.0%)" {
		t.Fatalf("unexpected line: %q", lines[0])
	}
}
