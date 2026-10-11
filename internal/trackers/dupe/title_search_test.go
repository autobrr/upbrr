// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type titleSearchDefinition struct{ bannedGroupDefinition }

func (titleSearchDefinition) TitleSearchPolicy() trackers.TitleSearchPolicy {
	return trackers.TitleSearchPolicy{ID: "test-title-v1"}
}

func TestReuseEmptyTitleSearchRequiresExactAuthority(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(titleSearchDefinition{}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	service := testService(map[string]Adapter{"DP": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
		calls++
		return ResolvedWithSearch(nil, nil, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	})})
	service.registry = registry
	identity := api.ExternalIdentity{
		SourcePath: filepath.Join(t.TempDir(), "Example.mkv"),
		Generation: 1,
		TMDBID:     123,
		Category:   api.CanonicalCategoryMovie,
	}
	fingerprint, err := api.TitleSearchIdentityFingerprint(identity)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	evidence := api.TrackerTitleSearchEvidence{
		Status:            api.MetadataEvidenceStatusComplete,
		TitleFingerprint:  fingerprint,
		ResultFingerprint: "result",
		ConfigFingerprint: "config",
		PolicyID:          "test-title-v1",
		CheckedAt:         now,
		FreshUntil:        now.Add(time.Minute),
	}
	meta := api.DuplicateSubject{Identity: identity, Projection: &api.TrackerReleaseProjection{
		TrackerID:           "DP",
		ConfigFingerprint:   "config",
		TitleSearchEvidence: &evidence,
	}}
	result, _, canceled := service.checkTracker(t.Context(), meta, "DP", CheckOptions{})
	if canceled || calls != 0 || !result.Search.Complete || result.HasDupes || !result.CheckedAt.Equal(now) {
		t.Fatalf("empty result not reused: %+v calls=%d", result, calls)
	}
	for _, test := range []struct {
		name   string
		change func(*api.TrackerTitleSearchEvidence)
	}{
		{"generation", func(e *api.TrackerTitleSearchEvidence) { e.TitleFingerprint = "other" }},
		{"configuration", func(e *api.TrackerTitleSearchEvidence) { e.ConfigFingerprint = "other" }},
		{"policy", func(e *api.TrackerTitleSearchEvidence) { e.PolicyID = "old" }},
		{"incomplete", func(e *api.TrackerTitleSearchEvidence) { e.Status = api.MetadataEvidenceStatusPartial }},
		{"positive", func(e *api.TrackerTitleSearchEvidence) { e.TorrentCount = 1 }},
		{"expired", func(e *api.TrackerTitleSearchEvidence) { e.FreshUntil = now.Add(-time.Minute) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := evidence
			test.change(&changed)
			projection := *meta.Projection
			projection.TitleSearchEvidence = &changed
			subject := meta
			subject.Projection = &projection
			if _, ok := service.reusableEmptyTitleSearch(subject, "DP", now); ok {
				t.Fatal("stale evidence reused")
			}
		})
	}
	meta.MatchedTrackers = []string{"DP"}
	result, _, _ = service.checkTracker(t.Context(), meta, "DP", CheckOptions{SkipRemote: true})
	if !result.HasDupes || result.Search.Scope != "local_client" || calls != 0 {
		t.Fatalf("title absence bypassed in-client match: %+v", result)
	}
}
