// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lst"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMediaTrackFactsAudioBitDepth(t *testing.T) {
	for _, test := range []struct {
		value any
		want  int
	}{
		{value: "24", want: 24},
		{value: float64(24), want: 24},
		{value: " 16 ", want: 16},
		{},
		{value: ""},
		{value: "0"},
		{value: "-24"},
		{value: "24 / 16"},
		{value: "24 bits"},
		{value: "24.5"},
	} {
		doc := mediaInfoDoc{}
		doc.Media.Track = []map[string]any{
			{"@type": "Video", "BitDepth": "10"},
			{
				"@type":       "Audio",
				"Title":       "Commentary",
				"Format":      "FLAC",
				"BitDepth":    "16",
				"StreamOrder": "1",
			},
			{
				"@type":       "Audio",
				"Title":       "Main",
				"Format":      "FLAC",
				"BitDepth":    test.value,
				"StreamOrder": "2",
			},
		}
		tracks, primaryID, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
		if err != nil {
			t.Fatal(err)
		}
		if len(tracks) != 2 || tracks[1].BitDepth != test.want || tracks[0].BitDepth != 16 || primaryID != tracks[1].ID {
			t.Fatalf("bit depth %v not retained independently of primary selection: tracks=%+v primary=%s", test.value, tracks, primaryID)
		}
	}
}

func TestLSTAudioGuidanceRequiresExactChannelEvidence(t *testing.T) {
	for _, test := range []struct {
		name         string
		channels     any
		original     string
		wantCount    int
		wantGuidance bool
	}{
		{
			name:         "mono",
			channels:     "1",
			wantCount:    1,
			wantGuidance: true,
		},
		{
			name:         "stereo",
			channels:     "2",
			wantCount:    2,
			wantGuidance: true,
		},
		{
			name:         "numeric stereo",
			channels:     float64(2),
			wantCount:    2,
			wantGuidance: true,
		},
		{
			name:         "original count",
			channels:     "6",
			original:     "2",
			wantCount:    2,
			wantGuidance: true,
		},
		{name: "unknown"},
		{
			name:      "compound",
			channels:  "2 / 6",
			wantCount: 2,
		},
		{
			name:      "negative",
			channels:  "-2",
			wantCount: 2,
		},
		{
			name:      "fraction",
			channels:  "2.5",
			wantCount: 2,
		},
		{
			name:      "partial original",
			channels:  "2",
			original:  "2 / 6",
			wantCount: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := mediaInfoDoc{}
			doc.Media.Track = []map[string]any{{
				"@type":             "Audio",
				"Title":             "Main",
				"Format":            "FLAC",
				"BitDepth":          "24",
				"StreamOrder":       "1",
				"Channels":          test.channels,
				"Channels_Original": test.original,
			}}
			tracks, primaryID, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
			if err != nil {
				t.Fatal(err)
			}
			if tracks[0].Channels != test.wantCount {
				t.Fatalf("existing channel value changed: %+v", tracks[0])
			}
			subject := api.NewTrackerValidationSubject(api.UploadSubject{
				Type:          "ENCODE",
				Release:       api.ReleaseInfo{Resolution: "1080p"},
				LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{PrimaryAudioTrackID: primaryID, Tracks: tracks}),
			}, "LST")
			failures, err := lst.Profile().ValidationPolicy.Check(t.Context(), subject, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "guidance_lst_flac_bit_depth" })
			if got != test.wantGuidance {
				t.Fatalf("guidance=%t want=%t for %+v", got, test.wantGuidance, tracks[0])
			}
		})
	}
}

func TestLSTAudioGuidanceUsesMeasuredPrimaryEvidence(t *testing.T) {
	for _, test := range []struct {
		name, order, otherOrder, otherTitle string
		want                                bool
	}{
		{
			name:  "measured",
			order: "1",
			want:  true,
		},
		{name: "missing order"},
		{name: "compound order", order: "1 / 2"},
		{
			name:       "unknown competing order",
			order:      "1",
			otherTitle: "Other main",
		},
		{
			name:       "earlier commentary",
			order:      "1",
			otherOrder: "0",
			otherTitle: "Commentary",
			want:       true,
		},
		{
			name:       "earlier isolated score",
			order:      "1",
			otherOrder: "0",
			otherTitle: "Isolated score",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := mediaInfoDoc{}
			doc.Media.Track = []map[string]any{{
				"@type":       "Audio",
				"Title":       "Main",
				"Format":      "FLAC",
				"BitDepth":    "24",
				"Channels":    "2",
				"StreamOrder": test.order,
			}}
			if test.otherTitle != "" {
				doc.Media.Track = append(doc.Media.Track, map[string]any{
					"@type":       "Audio",
					"Title":       test.otherTitle,
					"Format":      "FLAC",
					"BitDepth":    "24",
					"Channels":    "2",
					"StreamOrder": test.otherOrder,
				})
			}
			tracks, primaryID, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
			if err != nil {
				t.Fatal(err)
			}
			subject := api.NewTrackerValidationSubject(api.UploadSubject{
				Type:          "ENCODE",
				Release:       api.ReleaseInfo{Resolution: "1080p"},
				LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{PrimaryAudioTrackID: primaryID, Tracks: tracks}),
			}, "LST")
			failures, err := lst.Profile().ValidationPolicy.Check(t.Context(), subject, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "guidance_lst_flac_bit_depth" })
			if got != test.want {
				t.Fatalf("guidance=%t want=%t for primary=%s tracks=%+v", got, test.want, primaryID, tracks)
			}
		})
	}
}
