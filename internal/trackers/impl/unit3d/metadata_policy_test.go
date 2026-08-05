// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMetadataPolicyDefaultsToStrictTMDB(t *testing.T) {
	t.Parallel()

	definition := NewWithProfile(Profile{Name: "EXAMPLE"})
	policy := definition.MetadataPolicy()
	if len(policy.Requirements) != 1 {
		t.Fatalf("requirements = %#v", policy.Requirements)
	}
	requirement := policy.Requirements[0]
	if requirement.Disposition != api.RuleDispositionStrict ||
		len(requirement.AnyOf) != 1 || requirement.AnyOf[0] != trackers.MetadataFieldTMDB {
		t.Fatalf("default requirement = %#v", requirement)
	}

	policy.Requirements[0].Disposition = api.RuleDispositionWaivable
	if got := definition.MetadataPolicy().Requirements[0].Disposition; got != api.RuleDispositionStrict {
		t.Fatalf("mutated default disposition = %q", got)
	}
}

func TestMetadataPolicyAllowsSiteOverride(t *testing.T) {
	t.Parallel()

	override := &trackers.TrackerMetadataPolicy{Requirements: []trackers.MetadataRequirement{{
		Scope:       trackers.MetadataScopeAny,
		AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDB},
		Disposition: api.RuleDispositionWaivable,
	}}}
	definition := NewWithProfile(Profile{Name: "EXAMPLE", MetadataPolicy: override})
	override.Requirements[0].Disposition = api.RuleDispositionStrict
	override.Requirements[0].AnyOf[0] = trackers.MetadataFieldIMDB

	policy := definition.MetadataPolicy()
	if policy.Requirements[0].Disposition != api.RuleDispositionWaivable ||
		policy.Requirements[0].AnyOf[0] != trackers.MetadataFieldTMDB {
		t.Fatalf("site override = %#v", policy.Requirements[0])
	}

	policy.Requirements[0].AnyOf[0] = trackers.MetadataFieldIMDB
	if got := definition.MetadataPolicy().Requirements[0].AnyOf[0]; got != trackers.MetadataFieldTMDB {
		t.Fatalf("mutated site override field = %q", got)
	}
}
