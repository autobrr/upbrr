// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPreparationProgressUpdateJSONContract(t *testing.T) {
	update := NewPreparationProgressUpdate(PreparationPhaseBDInfo, PreparationProgressRunning, "Scanning playlist.")
	update.CorrelationID = "attempt-1"
	update.Timestamp = "2026-07-16T00:00:00Z"
	payload, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal preparation progress: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode preparation progress: %v", err)
	}
	if decoded["correlationID"] != "attempt-1" || decoded["phase"] != "bdinfo" || decoded["status"] != "running" {
		t.Fatal("preparation progress transport fields changed")
	}
	if decoded["order"] != float64(350) || decoded["label"] != "Analyze Blu-ray playlists" {
		t.Fatal("preparation progress presentation metadata changed")
	}
}

func TestPreparationProgressReporterAndUnknownPhase(t *testing.T) {
	var updates []PreparationProgressUpdate
	ctx := WithPreparationProgressReporter(context.Background(), func(update PreparationProgressUpdate) {
		updates = append(updates, update)
	})
	finish := BeginPreparationProgress(ctx, PreparationProgressPhase("future_phase"), "Starting.")
	finish(nil)
	if len(updates) != 2 {
		t.Fatalf("updates=%d, want 2", len(updates))
	}
	if updates[0].Order != 10000 || updates[0].Label != "future phase" {
		t.Fatalf("unknown phase presentation=%+v", updates[0])
	}
	if updates[1].Status != PreparationProgressCompleted {
		t.Fatalf("terminal status=%q", updates[1].Status)
	}
}
