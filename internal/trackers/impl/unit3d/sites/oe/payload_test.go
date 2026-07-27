// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
)

func TestAdditionalPayloadDefaultsMissingTVDB(t *testing.T) {
	apply := Profile().Site.ApplyAdditionalPayload
	if apply == nil {
		t.Fatal("expected OE additional payload callback")
	}

	data := map[string]string{}
	apply(trackers.PreparationInput{}, data)

	if got := data["tvdb"]; got != "0" {
		t.Fatalf("expected missing tvdb to default to 0, got %q", got)
	}
}

func TestAdditionalPayloadPreservesResolvedTVDB(t *testing.T) {
	apply := Profile().Site.ApplyAdditionalPayload
	if apply == nil {
		t.Fatal("expected OE additional payload callback")
	}

	data := map[string]string{"tvdb": "12345"}
	apply(trackers.PreparationInput{}, data)

	if got := data["tvdb"]; got != "12345" {
		t.Fatalf("expected resolved tvdb to be preserved, got %q", got)
	}
}
