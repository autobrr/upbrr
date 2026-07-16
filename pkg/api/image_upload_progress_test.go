// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestImageUploadProgressContextAndJSONContract(t *testing.T) {
	var updates []ImageUploadProgressUpdate
	ctx := WithImageUploadProgressReporter(context.Background(), func(update ImageUploadProgressUpdate) {
		updates = append(updates, update)
	})
	target := ImageUploadProgressTarget{
		AttemptID:  "imgbox|global",
		Host:       "IMGBOX",
		UsageScope: "global",
		Trackers:   []string{"AITHER", "ANT"},
		Total:      4,
		Reused:     1,
	}
	ctx = WithImageUploadProgressTarget(ctx, target)
	stored, ok := ImageUploadProgressTargetFromContext(ctx)
	if !ok || stored.Host != "imgbox" || stored.Reused != 1 {
		t.Fatalf("unexpected stored target: %#v", stored)
	}

	EmitImageUploadProgress(ctx, ImageUploadProgressUpdate{
		AttemptID: "imgbox|global",
		Host:      "imgbox",
		Completed: 2,
		Total:     4,
		Succeeded: 1,
		Reused:    1,
		Status:    ImageUploadProgressRunning,
	})
	if len(updates) != 1 || updates[0].Completed != 2 {
		t.Fatalf("unexpected updates: %#v", updates)
	}
	encoded, err := json.Marshal(updates[0])
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	jsonValue := string(encoded)
	for _, field := range []string{`"attemptID"`, `"completed"`, `"succeeded"`, `"fallback"`} {
		if !strings.Contains(jsonValue, field) {
			t.Fatalf("JSON missing %s: %s", field, jsonValue)
		}
	}
}
