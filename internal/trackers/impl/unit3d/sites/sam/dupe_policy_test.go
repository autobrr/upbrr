// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSAMDuplicateAudioRules(t *testing.T) {
	t.Parallel()

	target := api.TrackerDuplicateTarget{
		Names:          []string{"Example.2026.1080p.WEB-DL.DDP5.1.H.264-GRP"},
		Source:         "WEB-DL",
		Resolution:     "1080p",
		VideoCodec:     "H.264",
		AudioLanguages: []string{"English"},
	}
	policy := Profile().DupePolicy
	for _, test := range []struct {
		name       string
		candidate  string
		want       api.DupeRelation
		wantAction bool
	}{
		{
			name:       "same audio is a duplicate",
			candidate:  "Other.Example.2026.1080p.WEB-DL.DDP5.1.H.264-GRP",
			want:       api.DupeRelationSameSlot,
			wantAction: true,
		},
		{
			name:      "new audio language coexists",
			candidate: "Other.Example.2026.1080p.WEB-DL.DDP5.1.H.264-GRP",
			want:      api.DupeRelationCoexists,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := dupe.TrackerCandidate{Name: test.candidate}
			if test.want == api.DupeRelationCoexists {
				candidate.AudioLanguages = []string{"English", "Portuguese"}
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *policy, dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if len(result.Candidates) != 1 || result.Candidates[0].Relation != test.want || result.RequiresAction != test.wantAction {
				t.Fatalf("relation=%v action=%t candidates=%#v", result.Candidates[0].Relation, result.RequiresAction, result.Candidates)
			}
		})
	}
}
