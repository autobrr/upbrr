// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type tlRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tlRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestDuplicateSearchTitleFallbackHonorsManualTitle(t *testing.T) {
	t.Parallel()

	requests := 0
	searcher := &dupeSearcher{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"TL": {Passkey: "passkey"}}}},
		http: &http.Client{Transport: tlRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			if !strings.HasSuffix(request.URL.Path, "/Projected Release") {
				t.Errorf("automatic TL search path = %q", request.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"torrentList":[]}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}
	projection := &api.TrackerReleaseProjection{DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Projected Release"}}
	result := searcher.Search(context.Background(), api.DuplicateSubject{
		Projection:        projection,
		EffectiveMetadata: api.EffectiveMetadata{TitleProvenance: api.FactProvenanceManualEmpty},
	})
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingMetadata || requests != 0 {
		t.Fatalf("manual-empty TL result=%v code=%q requests=%d", result.Disposition(), result.Code(), requests)
	}
	if result := searcher.Search(context.Background(), api.DuplicateSubject{Projection: projection}); result.Disposition() != dupe.DispositionResolved || requests != 1 {
		t.Fatalf("automatic TL result=%v requests=%d", result.Disposition(), requests)
	}
}
