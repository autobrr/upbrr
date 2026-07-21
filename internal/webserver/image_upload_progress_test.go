// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestImageUploadProgressBoundaryCorrelatesAndSanitizesEvents(t *testing.T) {
	hub := newEventHub()
	backend := &Backend{hub: hub}
	events, unsubscribe := hub.Subscribe("session-1")
	t.Cleanup(unsubscribe)

	ctx, err := backend.withImageUploadProgress(context.Background(), "session-1", "image-upload-1")
	if err != nil {
		t.Fatalf("create image upload progress context: %v", err)
	}
	api.EmitImageUploadProgress(ctx, api.ImageUploadProgressUpdate{
		AttemptID:  "imgbox|global",
		Host:       "imgbox",
		UsageScope: "global",
		Trackers:   []string{"AITHER"},
		Completed:  1,
		Total:      4,
		Succeeded:  1,
		Status:     api.ImageUploadProgressRunning,
		Message:    `source=C:\media\Example.Release.2026.1080p-GRP api_key=synthetic-secret`,
	})

	select {
	case event := <-events:
		if event.Name != "image-upload:progress" {
			t.Fatalf("event name=%q", event.Name)
		}
		var update api.ImageUploadProgressUpdate
		if err := json.Unmarshal(event.Data, &update); err != nil {
			t.Fatalf("decode image upload progress: %v", err)
		}
		if update.CorrelationID != "image-upload-1" || update.Timestamp == "" {
			t.Fatal("image upload event is missing correlation or timestamp")
		}
		if strings.Contains(update.Message, "synthetic-secret") || strings.Contains(update.Message, `C:\media`) {
			t.Fatal("image upload event exposed sensitive or local-path detail")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for image upload event")
	}
}

func TestImageUploadProgressBoundaryRejectsEmptyCorrelation(t *testing.T) {
	backend := &Backend{hub: newEventHub()}
	if _, err := backend.withImageUploadProgress(context.Background(), "session-1", " "); err == nil {
		t.Fatal("empty image upload correlation ID was accepted")
	}
}
