// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type cancellationPolicyLogger struct {
	api.NopLogger
	cancel context.CancelFunc
}

func (l cancellationPolicyLogger) Infof(message string, _ ...any) {
	if strings.Contains(message, "tracker policy validation") && strings.Contains(message, "decision=accepted") {
		l.cancel()
	}
}

func TestCancellationStopsBeforeTorrentWorkAndPublication(t *testing.T) {
	for _, boundary := range []string{"hashing", "verification", "publication"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
			writeTestFile(t, source, "synthetic media bytes")
			root := t.TempDir()
			destination, err := TempTorrentPath(root, source)
			if err != nil {
				t.Fatal(err)
			}
			retained := []byte("retained artifact")
			if err := os.WriteFile(destination, retained, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var hashingStarted atomic.Bool
			ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
				if update.TotalPieces > 0 {
					hashingStarted.Store(true)
				}
				if boundary == "hashing" && strings.HasPrefix(update.Message, "Hashing torrent:") {
					cancel()
				}
			})
			if boundary == "verification" {
				candidate := source + ".torrent"
				createTestTorrentFromExisting(t, source, candidate)
				cancel()
				err = verifyCandidateTorrentData(ctx, candidate, api.TorrentSubject{SourcePath: source})
			} else {
				meta := api.TorrentSubject{SourcePath: source}
				var logger api.Logger = api.NopLogger{}
				if boundary == "publication" {
					logger = cancellationPolicyLogger{cancel: cancel}
					meta.Trackers = []string{"PTP"}
				}
				_, err = NewService(logger, root).Create(ctx, meta)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation at %s boundary was not observed: %v", boundary, err)
			}
			if boundary != "publication" && hashingStarted.Load() {
				t.Fatal("piece hashing started after cancellation")
			}
			data, readErr := os.ReadFile(destination)
			if readErr != nil || string(data) != string(retained) {
				t.Fatalf("canceled work replaced retained output: %v", readErr)
			}
		})
	}
}

func TestCancellationWaitsForMkbrrAndStopsAtItsReturnBoundary(t *testing.T) {
	for _, verify := range []bool{false, true} {
		name := "creation"
		if verify {
			name = "verification"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source := filepath.Join(dir, "Example.Release.2026.mkv")
			writeTestFile(t, source, "synthetic media bytes")
			if verify {
				createTestTorrentFromExisting(t, source, source+".torrent")
			}
			root := t.TempDir()
			destination, err := TempTorrentPath(root, source)
			if err != nil {
				t.Fatal(err)
			}
			retained := []byte("previous artifact must survive cancelled work")
			if err := os.WriteFile(destination, retained, 0o600); err != nil {
				t.Fatal(err)
			}
			started, release := make(chan struct{}), make(chan struct{})
			var hold, releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var completedAfterCancel atomic.Bool
			ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
				if update.TotalPieces == 0 {
					return
				}
				hold.Do(func() { close(started); <-release })
				if ctx.Err() != nil && update.CompletedPieces == update.TotalPieces {
					completedAfterCancel.Store(true)
				}
			})
			type outcome struct {
				result api.TorrentResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := NewService(nil, root).Create(ctx, api.TorrentSubject{
					SourcePath:   source,
					ForceRecheck: verify,
				})
				done <- outcome{result, err}
			}()
			select {
			case <-started:
			case result := <-done:
				t.Fatalf("mkbrr did not enter controlled progress callback: %v", result.err)
			case <-t.Context().Done():
				t.Fatal("mkbrr progress did not start")
			}
			cancel()
			select {
			case <-done:
				t.Fatal("cancellation returned while mkbrr was still inside its callback")
			default:
			}
			releaseOnce.Do(func() { close(release) })
			result := <-done
			if !errors.Is(result.err, context.Canceled) || result.result.Path != "" {
				t.Fatalf("expected cancellation without a published/reused result: result=%+v err=%v", result.result, result.err)
			}
			if !completedAfterCancel.Load() {
				t.Fatal("mkbrr completion progress stopped after cancellation was requested")
			}
			data, err := os.ReadFile(destination)
			if err != nil || string(data) != string(retained) {
				t.Fatalf("cancelled work replaced a retained artifact: %v", err)
			}
			staged, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".generating-*.torrent"))
			if err != nil || len(staged) != 0 {
				t.Fatalf("cancelled work left staging artifacts: count=%d err=%v", len(staged), err)
			}
		})
	}
}
