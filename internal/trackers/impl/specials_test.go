// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestOnlyOptedInUnit3DSitesAllowExplicitSingleSpecials(t *testing.T) {
	allowedPolicies := map[string]string{
		"AITHER": "unit3d-aither-constructibility-v1+unit3d-aither-policy-v11/languages-v2",
		"DP":     "unit3d-dp-constructibility-v1+unit3d-dp-policy-v4",
		"HHD":    "unit3d-hhd-constructibility-v1+unit3d-hhd-deterministic-v6/languages-v2",
		"LUME":   "unit3d-lume-constructibility-v1+unit3d-lume-constructibility-v5/languages-v2",
		"OTW":    "unit3d-otw-constructibility-v1+unit3d-otw-policy-v6",
		"ULCX":   "unit3d-ulcx-constructibility-v1+unit3d-ulcx-policy-v12/languages-v2",
		"YUS":    "unit3d-yus-constructibility-v1+unit3d-yus-policy-v3",
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range registry.NamesByFamily(trackers.FamilyUnit3D) {
		t.Run(name, func(t *testing.T) {
			descriptor, ok := registry.LookupDescriptor(name)
			if !ok {
				t.Fatal("missing descriptor")
			}
			failures, err := descriptor.Validation.Check(t.Context(), api.TrackerValidationSubject{
				Tracker:    name,
				Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryTV},
				SeasonStr:  "S00",
				EpisodeInt: 1,
				Type:       "WEBDL",
				Release:    api.ReleaseInfo{Resolution: "1080p"},
			}, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			blocked := false
			for _, failure := range failures {
				if failure.Rule == "canonical_tv_metadata" {
					blocked = true
				}
			}
			wantPolicy, allowed := allowedPolicies[name]
			if blocked == allowed {
				t.Fatalf("season-zero default changed: %s blocked=%t", name, blocked)
			}
			if allowed && descriptor.Validation.ID != wantPolicy {
				t.Fatalf("effective %s validation version = %q, want %q", name, descriptor.Validation.ID, wantPolicy)
			}
		})
	}
}
