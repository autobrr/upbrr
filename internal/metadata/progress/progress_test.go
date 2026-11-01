// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package progress

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSubstepWaitingCountsAndLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var updates []api.PreparationProgressUpdate
		ctx := api.WithPreparationProgressReporter(t.Context(), func(u api.PreparationProgressUpdate) { mu.Lock(); defer mu.Unlock(); updates = append(updates, u) })
		ctx, finish := StartStage(ctx, api.PreparationPhaseMediaFacts)
		discover := Begin(ctx, "Discover editions")
		discover.Update(2, 0, "7 editions discovered; total not yet known.")
		time.Sleep(6 * time.Second)
		synctest.Wait()
		mu.Lock()
		waiting := updates[len(updates)-1]
		mu.Unlock()
		if waiting.Phase != api.PreparationPhaseMediaFacts || waiting.Substep != "Discover editions" || waiting.CompletedItems != 2 || waiting.TotalItems != 0 || waiting.ElapsedSeconds < 5 {
			t.Fatalf("waiting = %#v", waiting)
		}
		discover.Finish("Discovery complete.", false)
		details := Begin(ctx, "Read edition details")
		details.Update(3, 7, "1 unavailable.")
		discover.Update(99, 99, "stale")
		details.Finish("Partial: 1 edition unavailable.", true)
		finish(nil)
		mu.Lock()
		n := len(updates)
		terminal := updates[n-1]
		mu.Unlock()
		details.Update(99, 99, "after completion")
		time.Sleep(10 * time.Second)
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		if len(updates) != n || terminal.Status != api.PreparationProgressPartial {
			t.Fatalf("terminal/lifetime = %#v", updates)
		}
		for _, u := range updates {
			if u.Message == "stale" || u.CompletedItems == 99 {
				t.Fatalf("stale event = %#v", u)
			}
		}
	})
}

func TestStageCancellationAndFailure(t *testing.T) {
	for _, failure := range []error{context.Canceled, errors.New("private error")} {
		var updates []api.PreparationProgressUpdate
		ctx := api.WithPreparationProgressReporter(t.Context(), func(u api.PreparationProgressUpdate) { updates = append(updates, u) })
		_, finish := StartStage(ctx, api.PreparationPhaseExternalIdentity)
		finish(failure)
		got := updates[len(updates)-1]
		want := api.PreparationProgressFailed
		if errors.Is(failure, context.Canceled) {
			want = api.PreparationProgressCanceled
		}
		if got.Status != want || got.Message == "private error" {
			t.Fatalf("terminal = %#v", got)
		}
	}
}

func TestStageReportsCanceledContextWhenCollectorReturnsNil(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
	}{
		{name: "cancellation during collection", timeout: time.Hour},
		{name: "expired deadline", timeout: -time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(tc.timeout))
			defer cancel()
			var updates []api.PreparationProgressUpdate
			ctx = api.WithPreparationProgressReporter(ctx, func(u api.PreparationProgressUpdate) { updates = append(updates, u) })
			_, finish := StartStage(ctx, api.PreparationPhaseTrackerEvidence)
			cancel()
			finish(nil)
			if got := updates[len(updates)-1]; got.Status != api.PreparationProgressCanceled {
				t.Fatalf("canceled context reported success: %#v", got)
			}
		})
	}
}

func TestConcurrentSubstepsResumeUnfinishedWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var updates []api.PreparationProgressUpdate
		ctx := api.WithPreparationProgressReporter(t.Context(), func(u api.PreparationProgressUpdate) { mu.Lock(); defer mu.Unlock(); updates = append(updates, u) })
		ctx, finish := StartStage(ctx, api.PreparationPhaseExternalIdentity)
		slow := Begin(ctx, "Read TMDB metadata")
		slow.Update(2, 5, "Waiting for remaining responses.")
		quick := Begin(ctx, "Read TVDB metadata")
		quick.Finish("Complete.", false)
		time.Sleep(6 * time.Second)
		synctest.Wait()
		mu.Lock()
		waiting := updates[len(updates)-1]
		mu.Unlock()
		if waiting.Substep != "Read TMDB metadata" || waiting.CompletedItems != 2 || waiting.TotalItems != 5 || waiting.ElapsedSeconds < 5 {
			t.Fatalf("remaining work = %#v", waiting)
		}
		slow.Finish("Complete.", false)
		finish(nil)
	})
}
