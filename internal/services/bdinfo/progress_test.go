// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	bdrunner "github.com/autobrr/go-bdinfo/pkg/bdinfo"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

type progressRecordingLogger struct {
	api.NopLogger
	entries []string
}

func (l *progressRecordingLogger) Debugf(format string, args ...any) {
	l.entries = append(l.entries, fmt.Sprintf(format, args...))
}

func TestExecuteLogsSuccessfulProgressWithoutReporter(t *testing.T) {
	originalRunner := runBDInfo
	runBDInfo = func(_ context.Context, req runRequest) (bdrunner.Result, error) {
		return bdrunner.Result{Report: "Example report", ReportPath: req.ReportPath}, nil
	}
	t.Cleanup(func() { runBDInfo = originalRunner })

	for _, playlist := range []string{"", "00001.MPLS"} {
		t.Run("playlist="+playlist, func(t *testing.T) {
			var fallback, operation progressRecordingLogger
			ctx := logging.WithOperationLogger(t.Context(), &operation)
			source := filepath.Join(t.TempDir(), "BDMV")
			_, err := New(&fallback).execute(ctx, source, playlist, filepath.Join(t.TempDir(), "report.txt"), playlist != "")
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("bdinfo: progress stage=done percent=100%% bdmvPath=%s playlist=%s", source, playlist)
			if !slices.Contains(operation.entries, want) {
				t.Fatalf("completion progress missing: %v", operation.entries)
			}
			if len(fallback.entries) != 0 {
				t.Fatalf("operation logs leaked to fallback: %v", fallback.entries)
			}
		})
	}
}

func TestScanProgressThresholds(t *testing.T) {
	tests := []struct {
		name   string
		events []bdrunner.ProgressEvent
		want   []string
	}{
		{
			name: "counts suppress duplicates and emit crossed thresholds",
			events: []bdrunner.ProgressEvent{
				{
					Stage:     bdrunner.StagePlaylist,
					Completed: 4,
					Total:     100,
				},
				{
					Stage:     bdrunner.StagePlaylist,
					Completed: 5,
					Total:     100,
				},
				{
					Stage:     bdrunner.StagePlaylist,
					Completed: 9,
					Total:     100,
				},
				{
					Stage:     bdrunner.StagePlaylist,
					Completed: 5,
					Total:     100,
				},
				{
					Stage:     bdrunner.StagePlaylist,
					Completed: 24,
					Total:     100,
				},
				{
					Stage:     bdrunner.StagePlaylist,
					Completed: 10,
					Total:     100,
				},
			},
			want: []string{"stage=playlist percent=5%", "stage=playlist percent=10%", "stage=playlist percent=15%", "stage=playlist percent=20%"},
		},
		{
			name: "bytes take precedence over counts",
			events: []bdrunner.ProgressEvent{
				{
					Stage:          bdrunner.StageStream,
					Completed:      10,
					Total:          10,
					ProcessedBytes: 49,
					TotalBytes:     1000,
				},
				{
					Stage:          bdrunner.StageStream,
					Completed:      10,
					Total:          10,
					ProcessedBytes: 50,
					TotalBytes:     1000,
				},
				{
					Stage:          bdrunner.StageStream,
					ProcessedBytes: 149,
					TotalBytes:     1000,
				},
				{
					Stage:          bdrunner.StageStream,
					ProcessedBytes: 149,
					TotalBytes:     1000,
				},
			},
			want: []string{"stage=stream percent=5%", "stage=stream percent=10%"},
		},
		{
			name: "each measured stage starts fresh",
			events: []bdrunner.ProgressEvent{
				{
					Stage:     bdrunner.StageClipInfo,
					Completed: 5,
					Total:     100,
				},
				{
					Stage:     bdrunner.StagePlaylist,
					Completed: 5,
					Total:     100,
				},
				{
					Stage:          bdrunner.StageStream,
					ProcessedBytes: 5,
					TotalBytes:     100,
				},
				{
					Stage:     bdrunner.StageInitialize,
					Completed: 5,
					Total:     100,
				},
			},
			want: []string{"stage=clipinfo percent=5%", "stage=playlist percent=5%", "stage=stream percent=5%", "stage=initialize percent=5%"},
		},
		{
			name: "unknown totals and synthetic completion have no percentages",
			events: []bdrunner.ProgressEvent{
				{Stage: bdrunner.StageStream, Completed: 1},
				{
					Stage:     bdrunner.StagePlaylist,
					Completed: -1,
					Total:     20,
				},
				{
					Stage:     bdrunner.StageScanComplete,
					Completed: 1,
					Total:     1,
				},
				{
					Stage:     bdrunner.StageDone,
					Completed: 1,
					Total:     1,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logger progressRecordingLogger
			progress := scanProgress{logger: &logger}
			for _, event := range tt.events {
				progress.emit(event)
			}
			if got := progressMessages(logger.entries); !slices.Equal(got, tt.want) {
				t.Fatalf("progress = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScanProgressConcurrentCallbacksReserveCompletion(t *testing.T) {
	var logger progressRecordingLogger
	progress := scanProgress{logger: &logger}
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			progress.emit(bdrunner.ProgressEvent{
				Stage:          bdrunner.StageStream,
				ProcessedBytes: uint64(i+1) * 100,
				TotalBytes:     1000,
			})
		})
	}
	wg.Wait()
	var want []string
	for percent := 5; percent < 100; percent += 5 {
		want = append(want, fmt.Sprintf("stage=stream percent=%d%%", percent))
	}
	if got := progressMessages(logger.entries); !slices.Equal(got, want) {
		t.Fatalf("progress = %v, want %v", got, want)
	}
}

func TestExecuteProgressPreservesReporterAndResetsPerScan(t *testing.T) {
	originalRunner := runBDInfo
	runBDInfo = func(_ context.Context, req runRequest) (bdrunner.Result, error) {
		if req.OnProgress == nil {
			t.Fatal("scanner progress callback is required without a user-facing reporter")
		}
		for range 2 {
			req.OnProgress(bdrunner.ProgressEvent{
				Stage:          bdrunner.StageStream,
				Completed:      1,
				Total:          10,
				ProcessedBytes: 100,
				TotalBytes:     1000,
			})
		}
		req.OnProgress(bdrunner.ProgressEvent{
			Stage:     bdrunner.StageScanComplete,
			Completed: 1,
			Total:     1,
		})
		req.OnProgress(bdrunner.ProgressEvent{Stage: bdrunner.StageDone})
		return bdrunner.Result{Report: "Example report", ReportPath: req.ReportPath}, nil
	}
	t.Cleanup(func() { runBDInfo = originalRunner })

	for _, withReporter := range []bool{false, true} {
		t.Run(fmt.Sprintf("reporter=%t", withReporter), func(t *testing.T) {
			var logger progressRecordingLogger
			svc := New(&logger)
			ctx := t.Context()
			var lines []string
			if withReporter {
				ctx = WithProgressReporter(ctx, func(line string) { lines = append(lines, line) })
			}
			source := filepath.Join(t.TempDir(), "BDMV")
			if _, err := svc.ExecuteFullScan(ctx, source, t.TempDir()); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.ExecuteForPlaylist(ctx, source, "00001.mpls", filepath.Join(t.TempDir(), "report.txt"), true); err != nil {
				t.Fatal(err)
			}
			want := []string{"stage=stream percent=5%", "stage=stream percent=10%", "stage=done percent=100%"}
			if got := progressMessages(logger.entries); !slices.Equal(got, append(slices.Clone(want), want...)) {
				t.Fatalf("progress = %v", got)
			}
			if withReporter {
				wantLines := []string{"STREAM: 1/10 (10.0%)", "STREAM: 1/10 (10.0%)", "Scan phase complete", "Scan complete"}
				if !slices.Equal(lines, append(slices.Clone(wantLines), wantLines...)) {
					t.Fatalf("user progress = %v", lines)
				}
			}
			if !slices.Contains(logger.entries, fmt.Sprintf("bdinfo: progress stage=stream percent=5%% bdmvPath=%s playlist=00001.MPLS", source)) {
				t.Fatal("selected playlist context missing")
			}
		})
	}
}

func TestExecuteProgressFailureDoesNotComplete(t *testing.T) {
	tests := []struct {
		name            string
		runErr          error
		cancel          bool
		report          string
		outputDirectory bool
	}{
		{
			name:   "runner error",
			runErr: errors.New("scan failed"),
			report: "Example report",
		},
		{
			name:   "runner canceled",
			runErr: context.Canceled,
			report: "Example report",
		},
		{
			name:   "canceled after done",
			cancel: true,
			report: "Example report",
		},
		{name: "empty report"},
		{
			name:            "persistence failed",
			report:          "Example report",
			outputDirectory: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			originalRunner := runBDInfo
			runBDInfo = func(_ context.Context, req runRequest) (bdrunner.Result, error) {
				req.OnProgress(bdrunner.ProgressEvent{
					Stage:          bdrunner.StageStream,
					ProcessedBytes: 1000,
					TotalBytes:     1000,
				})
				req.OnProgress(bdrunner.ProgressEvent{Stage: bdrunner.StageDone})
				if tt.cancel {
					cancel()
				}
				output := req.ReportPath
				if tt.outputDirectory {
					output = filepath.Dir(output)
				}
				return bdrunner.Result{Report: tt.report, ReportPath: output}, tt.runErr
			}
			t.Cleanup(func() { runBDInfo = originalRunner })
			var logger progressRecordingLogger
			_, err := New(&logger).ExecuteFullScan(ctx, filepath.Join(t.TempDir(), "BDMV"), t.TempDir())
			if err == nil {
				t.Fatal("expected scan failure")
			}
			if tt.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
			for _, entry := range logger.entries {
				if strings.Contains(entry, "percent=100%") || strings.Contains(entry, "successfully completed") {
					t.Fatalf("false success: %s", entry)
				}
			}
		})
	}
}

func TestExecuteProgressRetainsReportsWithScanErrors(t *testing.T) {
	for _, scan := range []bdrunner.ScanInfo{
		{ScanError: "scan failed"},
		{FileErrors: map[string]string{"00001.M2TS": "stream scan failed"}},
	} {
		originalRunner := runBDInfo
		runBDInfo = func(_ context.Context, req runRequest) (bdrunner.Result, error) {
			req.OnProgress(bdrunner.ProgressEvent{Stage: bdrunner.StageDone})
			return bdrunner.Result{
				Report:     "Example partial report",
				ReportPath: req.ReportPath,
				Scan:       scan,
			}, nil
		}
		t.Cleanup(func() { runBDInfo = originalRunner })
		var logger progressRecordingLogger
		result, err := New(&logger).ExecuteFullScan(t.Context(), filepath.Join(t.TempDir(), "BDMV"), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(result.ReportPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "Example partial report" || result.ReportText != string(content) {
			t.Fatal("partial report was not retained")
		}
		if got := progressMessages(logger.entries); len(got) != 0 {
			t.Fatalf("scan errors reported successful completion: %v", got)
		}
	}
}

func TestExecuteConcurrentScansKeepProgressIsolated(t *testing.T) {
	originalRunner := runBDInfo
	var entered sync.WaitGroup
	entered.Add(2)
	runBDInfo = func(_ context.Context, req runRequest) (bdrunner.Result, error) {
		req.OnProgress(bdrunner.ProgressEvent{
			Stage:     bdrunner.StagePlaylist,
			Completed: 1,
			Total:     20,
		})
		entered.Done()
		entered.Wait()
		req.OnProgress(bdrunner.ProgressEvent{
			Stage:     bdrunner.StagePlaylist,
			Completed: 2,
			Total:     20,
		})
		return bdrunner.Result{Report: "Example report", ReportPath: req.ReportPath}, nil
	}
	t.Cleanup(func() { runBDInfo = originalRunner })
	svc := New(api.NopLogger{})
	var wg sync.WaitGroup
	for _, playlist := range []string{"00001.MPLS", "00002.MPLS"} {
		source, output := filepath.Join(t.TempDir(), "BDMV"), filepath.Join(t.TempDir(), "report.txt")
		wg.Go(func() {
			var logger progressRecordingLogger
			ctx := logging.WithOperationLogger(t.Context(), &logger)
			if _, err := svc.ExecuteForPlaylist(ctx, source, playlist, output, true); err != nil {
				t.Error(err)
				return
			}
			want := []string{"stage=playlist percent=5%", "stage=playlist percent=10%", "stage=done percent=100%"}
			if got := progressMessages(logger.entries); !slices.Equal(got, want) {
				t.Errorf("%s progress = %v", playlist, got)
			}
			for _, entry := range logger.entries {
				if strings.HasPrefix(entry, "bdinfo: progress ") && !strings.HasSuffix(entry, "playlist="+playlist) {
					t.Errorf("progress mixed between scans: %s", entry)
				}
			}
		})
	}
	wg.Wait()
}

func TestRunBDInfoForwardsScannerProgress(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"PLAYLIST", "CLIPINF"} {
		if err := os.MkdirAll(filepath.Join(root, "BDMV", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var stages []bdrunner.Stage
	_, err := runBDInfo(t.Context(), runRequest{
		BDMVPath:   root,
		ReportPath: filepath.Join(t.TempDir(), "report.txt"),
		OnProgress: func(event bdrunner.ProgressEvent) { stages = append(stages, event.Stage) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) == 0 || stages[0] != bdrunner.StageStarting || stages[len(stages)-1] != bdrunner.StageDone {
		t.Fatalf("scanner progress = %v", stages)
	}
}

func progressMessages(entries []string) []string {
	var messages []string
	for _, entry := range entries {
		if progress, ok := strings.CutPrefix(entry, "bdinfo: progress "); ok {
			message, _, _ := strings.Cut(progress, " bdmvPath=")
			messages = append(messages, message)
		}
	}
	return messages
}
