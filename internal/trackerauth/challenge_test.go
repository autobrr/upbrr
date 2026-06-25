// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"testing"
	"time"
)

func TestChallengeManagerExpiresChallenges(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	manager := NewChallengeManager(time.Minute)
	manager.now = func() time.Time { return now }
	manager.ids = func() (string, error) { return "challenge-1", nil }

	id := manager.Create(context.Background(), "mtv")
	if id != "challenge-1" {
		t.Fatalf("challenge id: got %q", id)
	}
	if challenge, ok := manager.Get(id); !ok || challenge.TrackerID != "MTV" {
		t.Fatalf("expected active MTV challenge, got %#v ok=%v", challenge, ok)
	}

	now = now.Add(time.Minute)
	if _, ok := manager.Get(id); ok {
		t.Fatal("expected challenge to expire at ttl")
	}
}

func TestChallengeManagerConsumesTrackerScopedChallenge(t *testing.T) {
	t.Parallel()

	manager := NewChallengeManager(time.Minute)
	manager.ids = func() (string, error) { return "challenge-1", nil }

	id := manager.Create(context.Background(), "PTP")
	if _, err := manager.Consume(id, "MTV"); err == nil {
		t.Fatal("expected tracker mismatch")
	}
	if _, err := manager.Consume(id, "PTP"); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if _, ok := manager.Get(id); ok {
		t.Fatal("expected consumed challenge to be removed")
	}
}
