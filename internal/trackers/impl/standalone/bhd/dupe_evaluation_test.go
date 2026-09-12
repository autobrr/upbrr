// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBHDAPIResultsUseComparableMediaFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		title      string
		typeValue  string
		dv         int
		hdr        int
		plus       int
		pack       int
		resolution string
		want       api.DupeRelation
	}{
		{
			name:      "matching WEB",
			title:     "Example.Series.S01E04.2160p.WEB-DL-GRP",
			typeValue: "2160p",
			dv:        1,
			hdr:       1,
			want:      api.DupeRelationSameSlot,
		},
		{
			name:      "SDR slot",
			title:     "Example.Series.S01E04.2160p.WEB-DL-GRP",
			typeValue: "2160p",
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "DV only slot",
			title:     "Example.Series.S01E04.2160p.WEB-DL-GRP",
			typeValue: "2160p",
			dv:        1,
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "HDR10 plus does not hide overlap",
			title:     "Example.Series.S01E04.2160p.WEB-DL-GRP",
			typeValue: "2160p",
			dv:        1,
			hdr:       1,
			plus:      1,
			want:      api.DupeRelationSameSlot,
		},
		{
			name:      "live shaped HDR disagreement stays actionable",
			title:     "Example.Series.S01E04.2160p.WEB-DL.DV.HDR-GRP",
			typeValue: "2160p",
			dv:        1,
			hdr:       1,
			plus:      1,
			want:      api.DupeRelationManualReview,
		},
		{
			name:      "different resolution",
			title:     "Example.Series.S01E04.1080p.WEB-DL-GRP",
			typeValue: "1080p",
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "different episode",
			title:     "Example.Series.S01E03.2160p.WEB-DL-GRP",
			typeValue: "2160p",
			dv:        1,
			hdr:       1,
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "existing season pack",
			title:     "Example.Series.S01.2160p.WEB-DL-GRP",
			typeValue: "2160p",
			dv:        1,
			hdr:       1,
			pack:      1,
			want:      api.DupeRelationExistingPreferred,
		},
		{
			name:      "pack API flag contradicts episode title",
			title:     "Example.Series.S01E04.1080p.WEB-DL-GRP",
			typeValue: "1080p",
			pack:      1,
			want:      api.DupeRelationManualReview,
		},
		{
			name:      "remux is distinct",
			title:     "Example.Series.S01E04.2160p.BluRay-GRP",
			typeValue: "UHD Remux",
			dv:        1,
			hdr:       1,
			want:      api.DupeRelationCoexists,
		},
		{
			name:       "1080p size difference is not a free slot",
			title:      "Example.Series.S01E04.1080p.WEB-DL-GRP",
			typeValue:  "1080p",
			resolution: "1080p",
			want:       api.DupeRelationSameSlot,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := bhdEntries(map[string]any{"results": []any{map[string]any{
				"name":     tc.title,
				"type":     tc.typeValue,
				"category": "TV",
				"size":     5000,
				"dv":       tc.dv,
				"hdr10":    tc.hdr,
				"hdr10+":   tc.plus,
				"hlg":      0,
				"tv_pack":  tc.pack,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			resolution := "2160p"
			if tc.resolution != "" {
				resolution = tc.resolution
			}
			target := api.TrackerDuplicateTarget{
				Names:      []string{"Example.Series.S01E04.WEB-DL-OTHER"},
				Category:   "TV",
				Type:       "WEBDL",
				Source:     "WEB",
				Resolution: resolution,
				Season:     1,
				Episode:    4,
				SizeBytes:  1000,
				HDR: api.HDRFacts{
					Status:  api.HDREvidenceComplete,
					Origin:  api.HDREvidenceMediaInfo,
					Formats: []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
				},
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{dupe.NormalizeCandidate(entries[0], "BHD")}, *Profile().DupePolicy,
				dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if len(result.Candidates) != 1 || result.Candidates[0].Relation != tc.want {
				t.Fatalf("want %s: %#v", tc.want, result.Candidates)
			}
		})
	}
}

func TestBHDEntriesPreserveAPIIdentityAndType(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ raw, canonical, resolution string }{
		{"2160p", "", "2160p"}, {"1080p", "", "1080p"}, {"1080i", "", "1080i"},
		{"720p", "", "720p"}, {"576p", "", "576p"}, {"540p", "", "540p"}, {"480p", "", "480p"},
		{"BD Remux", "REMUX", ""}, {"UHD Remux", "REMUX", ""}, {"DVD Remux", "REMUX", ""},
		{"BD 25", "DISC", ""}, {"BD 50", "DISC", ""}, {"UHD 50", "DISC", ""}, {"UHD 66", "DISC", ""},
		{"UHD 100", "DISC", ""}, {"DVD 5", "DISC", ""}, {"DVD 9", "DISC", ""}, {"Other", "", ""},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			entries, err := bhdEntries(map[string]any{"results": []any{map[string]any{
				"name":    "Example.Series.S01-GRP",
				"type":    tc.raw,
				"source":  "WEB",
				"tv_pack": 1,
				"imdb_id": "tt1234567",
				"tmdb_id": "tv/123",
			}}})
			if err != nil {
				t.Fatal(err)
			}
			entry := entries[0]
			if entry.Type != tc.raw || entry.CanonicalType != tc.canonical || entry.Res != tc.resolution || !entry.Pack || entry.Source != "WEB" {
				t.Fatalf("API facts lost: %#v", entry)
			}
			if len(entry.ProviderIDs) != 2 || entry.ProviderIDs[0].Value != "tt1234567" || entry.ProviderIDs[1].Value != "123" {
				t.Fatalf("provider IDs lost: %#v", entry.ProviderIDs)
			}
		})
	}
}

func TestBHDConditionalWEBQualityAndEncodeSources(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, targetType, targetSource, candidateTitle, resolution string
		want                                                       api.DupeRelation
	}{
		{"WEBRip needs quality review", "WEBDL", "WEB", "Example.Series.S01E04.1080p.WEBRip-GRP", "1080p", api.DupeRelationManualReview},
		{"WEB-DL needs quality review", "WEBRIP", "WEB", "Example.Series.S01E04.1080p.WEB-DL-GRP", "1080p", api.DupeRelationManualReview},
		{"different WEB resolution", "WEBDL", "WEB", "Example.Series.S01E04.720p.WEBRip-GRP", "720p", api.DupeRelationCoexists},
		{"encode source does not create slot", "ENCODE", "HDDVD", "Example.Series.S01E04.1080p.BluRay.x264-GRP", "1080p", api.DupeRelationSameSlot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := bhdEntries(map[string]any{"results": []any{map[string]any{
				"name": tc.candidateTitle, "type": tc.resolution,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			target := api.TrackerDuplicateTarget{
				Names:      []string{"Example.Series.S01E04-OTHER"},
				Type:       tc.targetType,
				Source:     tc.targetSource,
				Resolution: "1080p",
				Season:     1,
				Episode:    4,
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{dupe.NormalizeCandidate(entries[0], "BHD")}, *Profile().DupePolicy,
				dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if result.Candidates[0].Relation != tc.want {
				t.Fatalf("want %s: %#v", tc.want, result.Candidates[0])
			}
		})
	}
}
