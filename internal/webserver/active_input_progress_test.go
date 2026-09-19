// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestActiveInputVerificationReporterCorrelatesAndSanitizes(t *testing.T) {
	hub := newEventHub()
	events, unsubscribe := hub.Subscribe("owner-1")
	defer unsubscribe()
	report := activeInputVerificationReporter(hub, "owner-1", "open-1")
	report(api.PreparationProgressUpdate{
		Phase:          api.PreparationPhaseSourceInspection,
		Status:         api.PreparationProgressRunning,
		Label:          "Inspect C:\\private\\Release.mkv",
		Message:        "Verifying C:\\private\\Release.mkv",
		CompletedBytes: 2048,
		TotalBytes:     1024,
	})

	event := <-events
	if event.Name != "input:verification" {
		t.Fatalf("event name = %q", event.Name)
	}
	var update api.PreparationProgressUpdate
	if err := json.Unmarshal(event.Data, &update); err != nil {
		t.Fatal(err)
	}
	if update.CorrelationID != "open-1" || update.Timestamp == "" {
		t.Fatalf("correlated event = %#v", update)
	}
	if update.CompletedBytes != 1024 || update.TotalBytes != 1024 {
		t.Fatalf("normalized byte progress = %#v", update)
	}
	if strings.Contains(update.Label, "private") || strings.Contains(update.Message, "private") {
		t.Fatalf("source path leaked to event payload: %#v", update)
	}
}

func TestActiveInputVerificationReporterIgnoresOtherPhases(t *testing.T) {
	hub := newEventHub()
	events, unsubscribe := hub.Subscribe("owner-1")
	defer unsubscribe()
	report := activeInputVerificationReporter(hub, "owner-1", "open-1")
	report(api.NewPreparationProgressUpdate(
		api.PreparationPhaseExternalIdentity,
		api.PreparationProgressRunning,
		"Resolving external identity.",
	))
	select {
	case event := <-events:
		t.Fatalf("unexpected event = %#v", event)
	default:
	}
}
