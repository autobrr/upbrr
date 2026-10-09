// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestOnlyAitherAllowsExplicitSingleSpecials(t *testing.T) {
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
			if blocked != (name != "AITHER") {
				t.Fatalf("season-zero default changed: %s blocked=%t", name, blocked)
			}
			if name == "AITHER" && descriptor.Validation.ID != "unit3d-aither-constructibility-v1+unit3d-aither-policy-v7/languages-v1" {
				t.Fatalf("effective AITHER validation version = %q", descriptor.Validation.ID)
			}
		})
	}
}
