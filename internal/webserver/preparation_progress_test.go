// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type cancellationAwarePlaylistCapability struct {
	observed chan error
}

func (c cancellationAwarePlaylistCapability) DiscoverPlaylists(ctx context.Context, _ string) ([]api.PlaylistInfo, error) {
	if err := ctx.Err(); err != nil {
		c.observed <- err
		return nil, fmt.Errorf("discover playlists canceled: %w", err)
	}
	return nil, nil
}

func TestPreparationProgressBoundaryCorrelatesAndSanitizesEvents(t *testing.T) {
	hub := newEventHub()
	backend := &Backend{hub: hub}
	events, unsubscribe := hub.Subscribe("session-1")
	t.Cleanup(unsubscribe)

	ctx, err := backend.withPreparationProgress(context.Background(), "session-1", "attempt-1")
	if err != nil {
		t.Fatalf("create preparation progress context: %v", err)
	}
	api.EmitPreparationProgress(ctx, api.NewPreparationProgressUpdate(
		api.PreparationPhaseClientDiscovery,
		api.PreparationProgressRunning,
		`source=C:\media\Example.Release.2026.1080p-GRP api_key=synthetic-secret`,
	))

	select {
	case event := <-events:
		if event.Name != "preparation:progress" {
			t.Fatalf("event name=%q", event.Name)
		}
		var update api.PreparationProgressUpdate
		if err := json.Unmarshal(event.Data, &update); err != nil {
			t.Fatalf("decode preparation progress: %v", err)
		}
		if update.CorrelationID != "attempt-1" || update.Timestamp == "" {
			t.Fatal("preparation event is missing correlation or timestamp")
		}
		if strings.Contains(update.Message, "synthetic-secret") || strings.Contains(update.Message, `C:\media`) {
			t.Fatal("preparation event exposed sensitive or local-path detail")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for preparation event")
	}
}

func TestPreparationProgressBoundaryRejectsEmptyCorrelation(t *testing.T) {
	backend := &Backend{hub: newEventHub()}
	if _, err := backend.withPreparationProgress(context.Background(), "session-1", " "); err == nil {
		t.Fatal("empty preparation correlation ID was accepted")
	}
}

func TestDiscoverPlaylistsPropagatesRequestCancellation(t *testing.T) {
	observed := make(chan error, 1)
	backend := &Backend{
		capabilities: CoreCapabilities{Playlists: cancellationAwarePlaylistCapability{observed: observed}},
		hub:          newEventHub(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = backend.DiscoverPlaylists(ctx, "synthetic-source")
	select {
	case err := <-observed:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("playlist capability did not receive request cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("playlist capability did not observe request cancellation")
	}
}
