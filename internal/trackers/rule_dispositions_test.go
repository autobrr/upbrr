// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"context"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRequestedTrackerSpecificRulesAreStrict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tracker string
		rule    string
		subject api.RuleSubject
	}{
		{
			name:    "BHD container",
			tracker: "BHD",
			rule:    "container",
			subject: api.RuleSubject{Type: "REMUX", Container: "avi"},
		},
		{
			name:    "BLU container",
			tracker: "BLU",
			rule:    "container",
			subject: api.RuleSubject{Type: "WEBDL", Container: "avi"},
		},
		{
			name:    "LUME container",
			tracker: "LUME",
			rule:    "container",
			subject: api.RuleSubject{Container: "mp4", Release: api.ReleaseInfo{Resolution: "720p"}},
		},
		{
			name:    "TVC disc",
			tracker: "TVC",
			rule:    "disc_forbidden",
			subject: api.RuleSubject{DiscType: "BDMV", Release: api.ReleaseInfo{Resolution: "1080p"}},
		},
		{
			name:    "TVC remux",
			tracker: "TVC",
			rule:    "remux_forbidden",
			subject: api.RuleSubject{Type: "REMUX", Release: api.ReleaseInfo{Resolution: "1080p"}},
		},
		{
			name:    "SHRI region",
			tracker: "SHRI",
			rule:    "region_required",
			subject: api.RuleSubject{DiscType: "DVD"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.subject.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusPresent
			failures := evaluateNonMetadataRulesForTest(context.Background(), test.tracker, test.subject)
			failure, found := findRuleFailure(failures, test.rule)
			if !found {
				t.Fatalf("missing %s failure: %#v", test.rule, failures)
			}
			if failure.Disposition != api.RuleDispositionStrict {
				t.Fatalf("%s disposition = %q, want strict", test.rule, failure.Disposition)
			}
		})
	}
}

func TestProductionMetadataIdentityRequirementsAreStrictExceptPTP(t *testing.T) {
	t.Parallel()

	registry, err := impl.NewRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	checked := 0
	for _, tracker := range registry.Names() {
		policy, ok := registry.LookupMetadataPolicy(tracker)
		if !ok {
			continue
		}
		for index, requirement := range policy.Requirements {
			if !containsMetadataIdentityField(requirement.AnyOf) {
				continue
			}
			checked++
			want := api.RuleDispositionStrict
			if tracker == "PTP" {
				want = api.RuleDispositionAdvisory
			}
			if requirement.Disposition != want {
				t.Errorf("%s metadata requirement %d disposition = %q, want %q", tracker, index, requirement.Disposition, want)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no metadata identity requirements checked")
	}
}

func TestProductionMetadataTargetMatrix(t *testing.T) {
	t.Parallel()

	registry, err := impl.NewRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	tests := []struct {
		tracker              string
		requireKnownCategory bool
		requirements         []trackers.MetadataRequirement
	}{
		{
			tracker:              "AZ",
			requireKnownCategory: true,
			requirements: []trackers.MetadataRequirement{
				{Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDBIDOnly, trackers.MetadataFieldIMDBIDOnly}},
				{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTVDBIDOnly}},
				{Scope: trackers.MetadataScopeAny, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDBOriginCountries}},
				{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTVDBTitle}},
				{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTVDBYear}},
			},
		},
		{
			tracker:              "CZ",
			requireKnownCategory: true,
			requirements: []trackers.MetadataRequirement{
				{Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDBIDOnly, trackers.MetadataFieldIMDBIDOnly}},
				{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldIMDBIDOnly, trackers.MetadataFieldTVDBIDOnly}},
				{Scope: trackers.MetadataScopeAny, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDBOriginCountries}},
				{Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDBTitle, trackers.MetadataFieldIMDBTitle}},
				{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldIMDBTitle, trackers.MetadataFieldTVDBTitle}},
			},
		},
		{
			tracker:              "PHD",
			requireKnownCategory: true,
			requirements: []trackers.MetadataRequirement{
				{Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDBIDOnly, trackers.MetadataFieldIMDBIDOnly}},
				{
					Scope: trackers.MetadataScopeTV,
					AnyOf: []trackers.MetadataField{
						trackers.MetadataFieldTMDBIDOnly,
						trackers.MetadataFieldIMDBIDOnly,
						trackers.MetadataFieldTVDBIDOnly,
					},
				},
				{Scope: trackers.MetadataScopeAny, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDBOriginCountries}},
			},
		},
		{
			tracker:              "BHD",
			requireKnownCategory: true,
			requirements: []trackers.MetadataRequirement{
				{Scope: trackers.MetadataScopeAny, AnyOf: []trackers.MetadataField{trackers.MetadataFieldIMDBIDOnly}},
				{Scope: trackers.MetadataScopeAny, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDBIDOnly}},
			},
		},
		{
			tracker:              "MTV",
			requireKnownCategory: true,
			requirements: []trackers.MetadataRequirement{
				{Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB}},
				{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTVDB}},
				{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTVDBTitle}},
				{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTVDBDisambiguation}},
			},
		},
		{
			tracker:              "HDB",
			requireKnownCategory: true,
		},
		{
			tracker:              "OTW",
			requireKnownCategory: true,
			requirements: []trackers.MetadataRequirement{{
				Scope: trackers.MetadataScopeAny,
				AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB, trackers.MetadataFieldTVDB},
			}},
		},
		{
			tracker: "SP",
			requirements: []trackers.MetadataRequirement{{
				Scope: trackers.MetadataScopeAny,
				AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDBIDOnly},
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.tracker, func(t *testing.T) {
			t.Parallel()
			policy, ok := registry.LookupMetadataPolicy(test.tracker)
			if !ok {
				t.Fatal("metadata policy missing")
			}
			if policy.RequireKnownCategory != test.requireKnownCategory {
				t.Fatalf("RequireKnownCategory = %t, want %t", policy.RequireKnownCategory, test.requireKnownCategory)
			}
			if len(policy.Requirements) != len(test.requirements) {
				t.Fatalf("requirements = %#v, want %#v", policy.Requirements, test.requirements)
			}
			for index, want := range test.requirements {
				got := policy.Requirements[index]
				if got.Scope != want.Scope || !slices.Equal(got.AnyOf, want.AnyOf) {
					t.Fatalf("requirement %d = %#v, want %#v", index, got, want)
				}
			}
		})
	}
}

func containsMetadataIdentityField(fields []trackers.MetadataField) bool {
	for _, field := range fields {
		switch field {
		case trackers.MetadataFieldTMDBIDOnly,
			trackers.MetadataFieldIMDBIDOnly,
			trackers.MetadataFieldTVDBIDOnly,
			trackers.MetadataFieldTVmazeIDOnly,
			trackers.MetadataFieldTMDB,
			trackers.MetadataFieldIMDB,
			trackers.MetadataFieldTVDB,
			trackers.MetadataFieldTVmaze:
			return true
		case trackers.MetadataFieldTMDBTitle,
			trackers.MetadataFieldIMDBTitle,
			trackers.MetadataFieldTVDBTitle,
			trackers.MetadataFieldTVDBYear,
			trackers.MetadataFieldTVDBDisambiguation,
			trackers.MetadataFieldTMDBOriginCountries,
			trackers.MetadataFieldTMDBUnavailable,
			trackers.MetadataFieldIMDBUnavailable,
			trackers.MetadataFieldTVDBUnavailable,
			trackers.MetadataFieldPoster:
			continue
		}
	}
	return false
}
