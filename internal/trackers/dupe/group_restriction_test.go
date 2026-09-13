// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestGroupRestrictionIgnoresOnlyConfirmedDifferentGroups(t *testing.T) {
	t.Parallel()

	target := api.TrackerDuplicateTarget{
		Names:      []string{"Example.Show.S01E01.1080p.WEB-DL-NTb"},
		Type:       "WEB-DL",
		Resolution: "1080p",
		Group:      "NTb",
		Season:     1,
		Episode:    1,
	}
	policy := trackerspkg.DupePolicy{
		GroupRestriction: trackerspkg.DupeGroupRestriction{Enabled: true, Group: "NTb"},
	}
	tests := []struct {
		name       string
		candidate  TrackerCandidate
		want       api.DupeRelation
		wantReason string
	}{
		{
			name: "structured different group",
			candidate: TrackerCandidate{
				Name:       "Example.Show.S01E01.1080p.WEB-DL-Other",
				Group:      "Other",
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
			want:       api.DupeRelationCoexists,
			wantReason: "configured_other_group",
		},
		{
			name: "title-only different group",
			candidate: TrackerCandidate{
				Name:       "Example.Show.S01E01.1080p.WEB-DL-Other",
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
			want:       api.DupeRelationCoexists,
			wantReason: "configured_other_group",
		},
		{
			name: "same group",
			candidate: TrackerCandidate{
				Name:       "Example.Show.S01E01.1080p.WEB-DL-NTb",
				Group:      "NTb",
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
			want:       api.DupeRelationExactDuplicate,
			wantReason: "exact_identity",
		},
		{
			name: "unknown group",
			candidate: TrackerCandidate{
				Name:       "Example.Show.S01E01.1080p.WEB",
				Group:      "unknown",
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
			want:       api.DupeRelationSameSlot,
			wantReason: "same_tracker_slot",
		},
		{
			name: "source hyphen without group",
			candidate: TrackerCandidate{
				Name:       "Example.Show.S01E01.1080p.WEB-DL",
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
			want:       api.DupeRelationSameSlot,
			wantReason: "same_tracker_slot",
		},
		{
			name: "contradictory group",
			candidate: TrackerCandidate{
				Name:       "Example.Show.S01E01.1080p.WEB-DL-Third",
				Group:      "Other",
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
			want:       api.DupeRelationSameSlot,
			wantReason: "same_tracker_slot",
		},
		{
			name: "malformed structured group",
			candidate: TrackerCandidate{
				Name:       "Example.Show.S01E01.1080p.WEB-DL-Other",
				Group:      "Other / Third",
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
			want:       api.DupeRelationSameSlot,
			wantReason: "same_tracker_slot",
		},
		{
			name: "exact identity wins before group restriction",
			candidate: TrackerCandidate{
				Name:       target.Names[0],
				Group:      "Other",
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Episode:    1,
			},
			want:       api.DupeRelationExactDuplicate,
			wantReason: "exact_identity",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(target, []TrackerCandidate{tt.candidate}, policy, SearchEvidence{Complete: true}).Candidates[0]
			if got.Relation != tt.want || len(got.Reasons) == 0 || got.Reasons[0].Code != tt.wantReason {
				t.Fatalf("candidate evaluation = %#v", got)
			}
		})
	}
}

func TestGroupRestrictionKeepsConfiguredHyphenatedGroup(t *testing.T) {
	t.Parallel()

	target := api.TrackerDuplicateTarget{
		Names:      []string{"Example.Show.S01E01.1080p.WEB-DL.H.265-A-B"},
		Type:       "WEB-DL",
		Resolution: "1080p",
		Group:      "A-B",
		Season:     1,
		Episode:    1,
	}
	candidate := TrackerCandidate{
		Name:       "Example.Show.S01E01.1080p.WEB-DL.H.264-A-B",
		Type:       "WEB-DL",
		Resolution: "1080p",
		Season:     1,
		Episode:    1,
	}
	policy := trackerspkg.DupePolicy{
		GroupRestriction: trackerspkg.DupeGroupRestriction{Enabled: true, Group: "A-B"},
	}

	got := Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true}).Candidates[0]
	if got.WinningRule == "configured_other_group" || got.Relation == api.DupeRelationCoexists {
		t.Fatalf("candidate evaluation = %#v", got)
	}
}

func TestDuplicatePolicyResolvesRestrictionPerTracker(t *testing.T) {
	t.Parallel()

	service := &Service{cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"NBL": {DupeBypassGroups: config.CSVList{"NTb"}},
		"BTN": {InternalGroups: config.CSVList{"NTb"}},
		"HDB": {PersonalReleaseGroups: config.CSVList{"NTb"}},
	}}}}
	meta := api.DuplicateSubject{Tag: "-NTb"}
	if policy := service.duplicatePolicy("NBL", meta); !policy.GroupRestriction.Enabled || policy.GroupRestriction.Group != "NTb" {
		t.Fatalf("NBL restriction = %#v", policy.GroupRestriction)
	}
	if policy := service.duplicatePolicy("BTN", meta); !policy.GroupRestriction.Enabled {
		t.Fatalf("BTN internal restriction = %#v", policy.GroupRestriction)
	}
	if policy := service.duplicatePolicy("HDB", meta); policy.GroupRestriction.Enabled {
		t.Fatalf("HDB personal list enabled restriction = %#v", policy.GroupRestriction)
	}
}

func TestGroupRestrictionExcludesOtherGroupsFromSetCapacity(t *testing.T) {
	t.Parallel()

	policy := trackerspkg.DupePolicy{
		GroupRestriction: trackerspkg.DupeGroupRestriction{Enabled: true, Group: "NTb"},
		SetRules: []trackerspkg.DupeSetRule{{
			ID:       "tracker/capacity",
			Capacity: 1,
		}},
	}
	evaluation := Evaluate(
		api.TrackerDuplicateTarget{
			Names:   []string{"Example.Show.S01E01.1080p.WEB-DL-NTb"},
			Group:   "NTb",
			Season:  1,
			Episode: 1,
		},
		[]TrackerCandidate{{
			ID:      "other",
			Name:    "Example.Show.S01E01.1080p.WEB-DL-Other",
			Group:   "Other",
			Season:  1,
			Episode: 1,
		}},
		policy,
		SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID},
	)
	if evaluation.Blocks || evaluation.RequiresAction {
		t.Fatalf("different group affected set capacity: %#v", evaluation)
	}
	if len(evaluation.SetFindings) != 1 || evaluation.SetFindings[0].ExistingOccupancy != 0 {
		t.Fatalf("set findings = %#v", evaluation.SetFindings)
	}
	if evaluation.Candidates[0].Relation != api.DupeRelationCoexists || evaluation.Candidates[0].Reasons[0].Code != "configured_other_group" {
		t.Fatalf("candidate evaluation = %#v", evaluation.Candidates[0])
	}
}
