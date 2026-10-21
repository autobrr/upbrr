// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAssessmentEquivalentMetadataRebindRequiresPrivateProof(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"equivalent", "legacy proof", "changed config", "wrong predecessor", "changed identity", "changed provider metadata", "changed release", "changed projection", "in client", "corrupt proof"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			now := time.Date(2026, time.October, 16, 4, 0, 0, 0, time.UTC)
			path := filepath.Join(t.TempDir(), "source.mkv")
			previous := api.ReleaseRef{SourcePath: path, Generation: 1}
			projection := api.TrackerReleaseProjection{

				TrackerID:         "ALPHA",
				UploadReleaseName: "Example.2026-GRP",

				DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Example.2026-GRP"},

				Readiness: api.ReadinessStatusReady,
				DupeReady: true,

				DuplicateTargetFingerprint: "target",
				DuplicateSearchFingerprint: "search",
				DuplicatePolicyFingerprint: "policy",
			}
			old := api.DuplicateSubject{

				SourcePath:  path,
				ReleaseName: projection.UploadReleaseName,
				Projection:  &projection,

				Identity: api.ExternalIdentity{
					SourcePath: path,
					Generation: 1,
					TMDBID:     123,
					ResolvedAt: now,
				},

				ProviderMetadata: api.SourceScopedMetadata{
					SourcePath: path,
					Generation: 1,
					UpdatedAt:  now,
				},
			}
			cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ALPHA": {APIKey: "synthetic-private-config"}}}}
			assessment := NewAssessment(old, cfg, []AssessmentEvidence{{

				Tracker:     "ALPHA",
				Disposition: DispositionResolved,
				HasDupes:    true,

				Match: api.DupeMatch{MatchedID: "candidate", MatchedReason: "remote_candidate"},
			}})
			assessment, err := assessment.Authorize(old, cfg, []string{"ALPHA"})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := assessment.MarshalBinary()
			if err != nil || strings.Contains(string(payload), "synthetic-private-config") {
				t.Fatalf("private proof serialized config or failed: %v", err)
			}
			assessment, err = UnmarshalAssessment(payload)
			if err != nil {
				t.Fatal(err)
			}
			next := old
			next.Identity.Generation, next.ProviderMetadata.Generation = 2, 2
			next.Identity.ResolvedAt, next.ProviderMetadata.UpdatedAt = now.Add(time.Minute), now.Add(time.Minute)
			projections := api.TrackerReleaseProjectionSet{
				ReleaseRef: api.ReleaseRef{SourcePath: path, Generation: 2}, Projections: []api.TrackerReleaseProjection{projection},
			}
			switch name {
			case "legacy proof":
				entry := assessment.entries["ALPHA"]
				entry.bindingSubject = nil
				assessment.entries["ALPHA"] = entry
			case "changed config":
				cfg.Trackers.Trackers["ALPHA"] = config.TrackerConfig{APIKey: "different-synthetic-config"}
			case "wrong predecessor":
				previous.Generation = 3
			case "changed identity":
				next.Identity.TMDBID++
			case "changed provider metadata":
				next.ProviderMetadata.ProviderAvailability = []api.ProviderAvailabilityEvidence{{Provider: api.IdentityProviderTMDB, Source: "changed"}}
			case "changed release":
				next.Release.Title = "Changed title"
			case "changed projection":
				projections.Projections[0].DuplicateTargetFingerprint = "different-target"
			case "in client":
				next.MatchedTrackers = []string{"ALPHA"}
			case "corrupt proof":
				entry := assessment.entries["ALPHA"]
				entry.bindingSubject = []byte(`{}`)
				assessment.entries["ALPHA"] = entry
			}
			rebound, ok := (&Service{cfg: cfg}).RebindEquivalentMetadata(assessment, next, previous, projections)
			if ok != (name == "equivalent") {
				t.Fatalf("rebind=%t", ok)
			}
			if !ok {
				return
			}
			bound, ok := rebound.(Assessment)
			if !ok {
				t.Fatalf("assessment type = %T", rebound)
			}
			before, _ := assessment.Decision("ALPHA")
			after, _ := bound.Decision("ALPHA")
			if !reflect.DeepEqual(before, after) || bound.RetainValid(next, cfg).IsEmpty() {
				t.Fatal("rebind lost acknowledgement or did not bind current private authority")
			}
		})
	}
}
