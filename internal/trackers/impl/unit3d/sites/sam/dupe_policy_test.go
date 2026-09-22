// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

// TestSAMDuplicateAudioRules verifies SAM audio slot separation and coexistence.
func TestSAMDuplicateAudioRules(t *testing.T) {
	t.Parallel()

	target := api.TrackerDuplicateTarget{
		Names:          []string{"Example.2026.1080p.WEB-DL.DDP5.1.H.264-GRP"},
		Source:         "WEB-DL",
		Resolution:     "1080p",
		VideoCodec:     "H.264",
		AudioCodecs:    []string{"E-AC-3"},
		AudioChannels:  []string{"5.1"},
		AudioLanguages: []string{"English"},
	}
	policy := Profile().DupePolicy
	for _, test := range []struct {
		name       string
		candidate  string
		audioCodec string
		channels   string
		languages  []string
		want       api.DupeRelation
		wantAction bool
	}{
		{
			name:       "same audio is a duplicate",
			candidate:  "Other.Example.2026.1080p.WEB-DL.DDP5.1.H.264-GRP",
			audioCodec: "DDP",
			channels:   "6 channels",
			languages:  []string{"English"},
			want:       api.DupeRelationSameSlot,
			wantAction: true,
		},
		{
			name:       "new audio language coexists",
			candidate:  "Other.Example.2026.1080p.WEB-DL.DDP5.1.H.264-GRP",
			audioCodec: "E-AC-3",
			channels:   "5.1",
			languages:  []string{"English", "Portuguese"},
			want:       api.DupeRelationCoexists,
		},
		{
			name:       "new audio codec coexists",
			candidate:  "Other.Example.2026.1080p.WEB-DL.DTS5.1.H.264-GRP",
			audioCodec: "DTS",
			channels:   "5.1",
			languages:  []string{"English"},
			want:       api.DupeRelationCoexists,
		},
		{
			name:       "new channel layout coexists",
			candidate:  "Other.Example.2026.1080p.WEB-DL.DDP2.0.H.264-GRP",
			audioCodec: "E-AC-3",
			channels:   "2.0",
			languages:  []string{"English"},
			want:       api.DupeRelationCoexists,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := dupe.TrackerCandidate{
				Name:           test.candidate,
				AudioCodecs:    []string{test.audioCodec},
				AudioChannels:  []string{test.channels},
				AudioLanguages: test.languages,
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *policy, dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if len(result.Candidates) != 1 {
				t.Fatalf("candidate count=%d candidates=%#v", len(result.Candidates), result.Candidates)
			}
			if result.Candidates[0].Relation != test.want || result.RequiresAction != test.wantAction {
				t.Fatalf("relation=%v action=%t candidates=%#v", result.Candidates[0].Relation, result.RequiresAction, result.Candidates)
			}
		})
	}
}
