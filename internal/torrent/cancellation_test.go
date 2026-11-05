// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

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
