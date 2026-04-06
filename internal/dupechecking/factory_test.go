// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

func TestBuildHandlersCoversKnownTrackers(t *testing.T) {
	t.Parallel()
	handlers := buildHandlers(handlerDeps{cfg: config.Config{}})

	for _, tracker := range trackers.KnownTrackers() {
		if tracker == "MANUAL" {
			continue
		}
		if _, ok := handlers[tracker]; !ok {
			t.Fatalf("expected handler for %s", tracker)
		}
	}
}
