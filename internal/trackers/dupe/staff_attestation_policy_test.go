// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// regression: #803
func TestProjectAdapterResultResolvesStaffAttestationWithoutChangingEvidence(t *testing.T) {
	for _, required := range []bool{false, true} {
		registry := trackerspkg.NewRegistry()
		called := false
		policy := &trackerspkg.DupePolicy{
			ID:                 "example/duplicate/v1",
			StaffTokenOverride: true,
			StaffTokenOverrideRequired: func(cfg config.TrackerConfig, subject api.DuplicateSubject, evaluations []api.DupeCandidateEvaluation) bool {
				called = true
				if !cfg.GuruTrump || subject.SourcePath != "/synthetic/movie" || len(evaluations) != 0 {
					t.Fatalf("callback lost current context: %#v %#v %#v", cfg, subject, evaluations)
				}
				return required
			},
		}
		if err := registry.RegisterDescriptor(trackerspkg.Descriptor{
			Name:        "DP",
			DisplayName: "Example",
			Family:      trackerspkg.FamilyStandalone,
			Definition:  bannedGroupDefinition{},
			DupePolicy:  policy,
		}); err != nil {
			t.Fatal(err)
		}
		service := testService(nil)
		service.registry = registry
		service.cfg.Trackers.Trackers = map[string]config.TrackerConfig{"DP": {GuruTrump: true}}
		result, _ := service.projectAdapterResult("DP", api.DuplicateSubject{SourcePath: "/synthetic/movie"},
			ResolvedWithSearch(nil, nil, SearchEvidence{Complete: false, Pages: 1}), time.Now().UTC())
		if !called || result.RequiresStaffToken != required || result.Search.Complete || len(result.Evaluations) != 0 {
			t.Fatalf("unexpected attestation/evidence: %#v", result)
		}
	}
}
