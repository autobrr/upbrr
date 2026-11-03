// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

func analysisTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger, err := logging.New(config.LoggingConfig{Level: "trace"}, "")
	if err != nil {
		t.Fatal(err)
	}
	logger.SetConsoleOutput(io.Discard, io.Discard)
	t.Cleanup(func() { _ = logger.Close() })
	return logger
}

func assertAnalysisLog(t *testing.T, logger *logging.Logger, level string, fragments ...string) {
	t.Helper()
	for _, entry := range logger.Recent(100) {
		if entry.Level != level {
			continue
		}
		matches := true
		for _, fragment := range fragments {
			matches = matches && strings.Contains(entry.Message, fragment)
		}
		if matches {
			t.Logf("%s %s", entry.Level, entry.Message)
			return
		}
	}
	t.Fatalf("missing %s log containing %q: %+v", level, fragments, logger.Recent(100))
}

func TestServiceLogsRespectOperationVerbosityAndSharedSinks(t *testing.T) {
	var baseline []api.WorkflowProgressUpdate
	for _, level := range []string{"info", "debug", "trace"} {
		t.Run(level, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "upbrr.db")
			root, err := logging.New(config.LoggingConfig{
				Level:          "info",
				FileEnabled:    true,
				MaxTotalSizeMB: 1,
				MaxFiles:       1,
			}, dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = root.Close() })
			var stdout, stderr bytes.Buffer
			root.SetConsoleOutput(&stdout, &stderr)
			subscription, entries := root.Subscribe(100)
			defer root.Unsubscribe(subscription)
			view, err := logging.NewOperationLogger(root, level)
			if err != nil {
				t.Fatal(err)
			}
			ctx := logging.WithOperationLogger(t.Context(), view)
			var progress []api.WorkflowProgressUpdate
			ctx = api.WithWorkflowProgressReporter(ctx, func(update api.WorkflowProgressUpdate) { progress = append(progress, update) })
			decoder := &fakeDecoder{
				streams: []inspectedStream{{
					codec:           "pcm",
					sampleRate:      48000,
					channels:        1,
					layout:          "mono",
					durationSeconds: 4.0 / 48000,
				}},
				pcm: encodePCM([][]float32{{0.25}, {-0.25}, {0.5}, {-0.5}}),
			}
			service := newService(root, decoder)
			results, err := service.AnalyzeFile(ctx, "Example.Audio.2026.mkv", api.AudioAnalysisSelectionAll, nil,
				[]api.AudioAnalysisVariant{api.AudioAnalysisWaveform, api.AudioAnalysisStats}, t.TempDir())
			if err != nil || decoder.decodes != 1 || len(results) != 1 || results[0].Public.Status != api.StageStatusCompleted || len(results[0].Artifacts) != 2 {
				t.Fatalf("analysis contract changed: decodes=%d results=%+v err=%v", decoder.decodes, results, err)
			}
			if level == "info" {
				baseline = progress
				if len(baseline) != 5 || baseline[0].Completed != 0 || baseline[4].Completed != 95 {
					t.Fatalf("progress contract changed: %+v", baseline)
				}
			} else if !reflect.DeepEqual(progress, baseline) {
				t.Fatalf("verbosity changed progress: %+v; want %+v", progress, baseline)
			}
			assertAnalysisLog(t, root, "info", "state=started", "track_count=1", "waveform", "statistics")
			if strings.Contains(stdout.String(), "spectrogram_fft_size=") || strings.Contains(stderr.String(), "spectrogram_fft_size=") {
				t.Fatal("technical settings leaked to info console")
			}
			if level != "info" {
				assertAnalysisLog(t, root, "debug", "decoder_threads=2", "spectrogram_fft_size=1024", "spectrogram_window=kaiser", "spectrogram_columns=3000")
			}
			root.Debugf("unscoped-debug-must-remain-filtered")
			// Exercise a secret-bearing synthetic diagnostic at the existing inspection boundary.
			decoder.err = errors.New("api_key=synthetic-secret\x1b[31m")
			_, err = service.AnalyzeFile(ctx, "Example.Audio.2026.mkv", api.AudioAnalysisSelectionAll, nil,
				[]api.AudioAnalysisVariant{api.AudioAnalysisWaveform}, t.TempDir())
			if !errors.Is(err, decoder.err) || decoder.decodes != 1 {
				t.Fatalf("inspection failure changed: %v", err)
			}
			// Use the prepared-service boundary, which logs a bounded safe failure rather than the raw error.
			_, err = service.inspectBindings(ctx, decoder, api.AudioAnalysisSubject{}, nil)
			if !errors.Is(err, decoder.err) {
				t.Fatalf("inspection error identity changed: %v", err)
			}
			assertAnalysisLog(t, root, "warn", "could not inspect", "reason=source_inspection_failed")
			logPath, err := logging.LogPath(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range root.Recent(100) {
				if strings.Contains(entry.Message, "synthetic-secret") || strings.Contains(entry.Message, "\x1b") || strings.Contains(entry.Message, "unscoped-debug") {
					t.Fatalf("unsafe or unscoped entry: %+v", entry)
				}
				if !strings.Contains(string(file), entry.Message) {
					t.Fatalf("entry missing from shared file sink: %+v", entry)
				}
				select {
				case got := <-entries:
					if got != entry {
						t.Fatalf("subscriber differs from retained entry: %+v; want %+v", got, entry)
					}
				default:
					t.Fatalf("subscriber missing entry: %+v", entry)
				}
				t.Logf("%s %s", entry.Level, entry.Message)
			}
		})
	}
}

func TestServiceFailedTrackWithNilErrorDoesNotLogSuccess(t *testing.T) {
	logger := analysisTestLogger(t)
	decoder := &fakeDecoder{streams: []inspectedStream{{
		codec:      "pcm",
		sampleRate: 48000,
		channels:   1,
		layout:     "mono",
	}}}
	results, err := newService(logger, decoder).AnalyzeFile(t.Context(), "Example.Audio.2026.mkv", api.AudioAnalysisSelectionAll, nil,
		[]api.AudioAnalysisVariant{api.AudioAnalysisWaveform}, t.TempDir())
	if err != nil || len(results) != 1 || results[0].Public.Status != api.StageStatusFailed || len(results[0].Artifacts) != 0 || decoder.decodes != 1 {
		t.Fatalf("empty decode contract changed: results=%+v err=%v decodes=%d", results, err, decoder.decodes)
	}
	assertAnalysisLog(t, logger, "warn", "state=failed", "completed_count=0", "partial_count=0", "failed_count=1", "artifact_count=0")
}
