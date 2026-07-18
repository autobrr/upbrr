// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"context"
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
		case trackers.MetadataFieldTVDBTitle, trackers.MetadataFieldPoster:
			continue
		}
	}
	return false
}
