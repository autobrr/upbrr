// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBTNAPIHDRSlotEvaluation(t *testing.T) {
	t.Parallel()

	// Refs #470: exercise API evidence through the final review decision.
	for _, tt := range []struct {
		name          string
		tags          any
		omitTags      bool
		marker        string
		resolution    string
		origin        string
		targetOrigin  string
		formats       []api.HDRFormat
		targetFormats []api.HDRFormat
		targetMarker  string
		want          api.DupeRelation
	}{
		{
			name:    "DV only",
			tags:    []string{"Dolby Vision", "Subtitles"},
			marker:  ".DV",
			formats: []api.HDRFormat{api.HDRFormatDolbyVision},
			want:    api.DupeRelationCoexists,
		},
		{
			name:    "HDR10 only",
			tags:    []string{"HDR10 Compatible"},
			marker:  ".HDR",
			formats: []api.HDRFormat{api.HDRFormatHDR10},
			want:    api.DupeRelationCoexists,
		},
		{
			name:    "DV HDR10",
			tags:    []string{"Dolby Vision", "HDR10 Compatible"},
			marker:  ".DV.HDR",
			formats: []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
			want:    api.DupeRelationSameSlot,
		},
		{
			name:    "empty tags SDR",
			tags:    []string{},
			formats: []api.HDRFormat{api.HDRFormatSDR},
			want:    api.DupeRelationCoexists,
		},
		{
			name:    "audio tags SDR",
			tags:    []string{"Dolby Atmos", "5.1 Channels", "Subtitles"},
			formats: []api.HDRFormat{api.HDRFormatSDR},
			want:    api.DupeRelationCoexists,
		},
		{
			name:    "HLG",
			tags:    []string{"Hybrid Log Gamma"},
			marker:  ".HLG",
			formats: []api.HDRFormat{api.HDRFormatHLG},
			want:    api.DupeRelationCoexists,
		},
		{
			name:          "DV target versus hybrid",
			tags:          []string{"Dolby Vision", "HDR10 Compatible"},
			marker:        ".DV.HDR",
			formats:       []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
			targetFormats: []api.HDRFormat{api.HDRFormatDolbyVision},
			targetMarker:  ".DV",
			want:          api.DupeRelationCoexists,
		},
		{
			name:          "HDR target versus hybrid",
			tags:          []string{"Dolby Vision", "HDR10 Compatible"},
			marker:        ".DV.HDR",
			formats:       []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
			targetFormats: []api.HDRFormat{api.HDRFormatHDR10},
			targetMarker:  ".HDR",
			want:          api.DupeRelationCoexists,
		},
		{
			name:          "SDR target versus hybrid",
			tags:          []string{"Dolby Vision", "HDR10 Compatible"},
			marker:        ".DV.HDR",
			formats:       []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
			targetFormats: []api.HDRFormat{api.HDRFormatSDR},
			targetMarker:  ".SDR",
			want:          api.DupeRelationCoexists,
		},
		{
			name:       "1080p preserves slot",
			tags:       []string{},
			resolution: "1080p",
			formats:    []api.HDRFormat{api.HDRFormatSDR},
			want:       api.DupeRelationSameSlot,
		},
		{
			name:    "contradictory title",
			tags:    []string{},
			marker:  ".DV.HDR",
			formats: []api.HDRFormat{api.HDRFormatSDR},
			want:    api.DupeRelationManualReview,
		},
		{
			name: "malformed scalar",
			tags: "Dolby Vision",
			want: api.DupeRelationSameSlot,
		},
		{
			name: "null tags",
			tags: nil,
			want: api.DupeRelationSameSlot,
		},
		{
			name:     "legacy title DV",
			omitTags: true,
			marker:   ".DV",
			formats:  []api.HDRFormat{api.HDRFormatDolbyVision},
			want:     api.DupeRelationCoexists,
		},
		{
			name:     "legacy title HDR",
			omitTags: true,
			marker:   ".HDR",
			formats:  []api.HDRFormat{api.HDRFormatHDR10},
			want:     api.DupeRelationCoexists,
		},
		{
			name:     "legacy untagged SDR",
			omitTags: true,
			formats:  []api.HDRFormat{api.HDRFormatSDR},
			want:     api.DupeRelationCoexists,
		},
		{
			name: "malformed array",
			tags: []any{"Dolby Vision", 42},
			want: api.DupeRelationSameSlot,
		},
		{
			name:    "internal origin cannot erase HDR difference",
			tags:    []string{"Dolby Vision"},
			marker:  ".DV",
			origin:  "Internal",
			formats: []api.HDRFormat{api.HDRFormatDolbyVision},
			want:    api.DupeRelationCoexists,
		},
		{
			name:    "none origin cannot erase HDR difference",
			tags:    []string{},
			origin:  "None",
			formats: []api.HDRFormat{api.HDRFormatSDR},
			want:    api.DupeRelationCoexists,
		},
		{
			name:         "scene same type shares one slot across groups",
			tags:         []string{"Dolby Vision", "HDR10 Compatible"},
			marker:       ".DV.HDR",
			origin:       "Scene",
			targetOrigin: "Scene",
			formats:      []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
			want:         api.DupeRelationSameSlot,
		},
		{
			name:         "scene different HDR type coexists",
			tags:         []string{"Dolby Vision"},
			marker:       ".DV",
			origin:       "Scene",
			targetOrigin: "Scene",
			formats:      []api.HDRFormat{api.HDRFormatDolbyVision},
			want:         api.DupeRelationCoexists,
		},
		{
			name:         "scene and p2p same type share a slot",
			tags:         []string{"Dolby Vision", "HDR10 Compatible"},
			marker:       ".DV.HDR",
			targetOrigin: "Scene",
			formats:      []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
			want:         api.DupeRelationSameSlot,
		},
		{
			name:         "scene and classified internal same type share a slot",
			tags:         []string{"Dolby Vision", "HDR10 Compatible"},
			marker:       ".DV.HDR",
			origin:       "Internal",
			targetOrigin: "Scene",
			formats:      []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
			want:         api.DupeRelationSameSlot,
		},
		{
			name:         "pending internal and scene same type share a slot",
			tags:         []string{"Dolby Vision", "HDR10 Compatible"},
			marker:       ".DV.HDR",
			origin:       "Scene",
			targetOrigin: "None",
			formats:      []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
			want:         api.DupeRelationSameSlot,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolution := tt.resolution
			if resolution == "" {
				resolution = "2160p"
			}
			origin := tt.origin
			if origin == "" {
				origin = "P2P"
			}
			torrent := map[string]any{
				"TorrentID":   "777",
				"GroupID":     "333",
				"TvdbID":      "1234567",
				"ReleaseName": fmt.Sprintf("Example.Show.S01E01.%s.WEB-DL%s.H.265-GRP", resolution, tt.marker),
				"GroupName":   "S01E01",
				"Category":    "Episode",
				"Source":      "WEB-DL",
				"Resolution":  resolution,
				"Codec":       "H.265",
				"Container":   "MKV",
				"Origin":      origin,
				"Tags":        tt.tags,
			}
			if tt.omitTags {
				delete(torrent, "Tags")
			}
			raw, err := json.Marshal(map[string]any{"result": map[string]any{"results": "1", "torrents": map[string]any{"777": torrent}}})
			if err != nil {
				t.Fatal(err)
			}
			payloads := captureBTNPayloads(t, string(raw))
			adapter := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)
			result := adapter.Search(t.Context(), api.DuplicateSubject{Identity: api.ExternalIdentity{Category: "TV", TVDBID: 1234567}})
			if result.Cause() != nil || len(result.Entries()) != 1 {
				t.Fatalf("search failed: %s (%v)", result.SafeMessage(), result.Cause())
			}
			candidate := dupe.NormalizeCandidate(result.Entries()[0], "BTN")
			if tt.formats != nil {
				if candidate.HDR.Status != api.HDREvidenceComplete || len(candidate.HDR.Formats) != len(tt.formats) {
					t.Fatalf("unexpected API HDR evidence: %#v", candidate.HDR)
				}
				for _, format := range tt.formats {
					if !slices.Contains(candidate.HDR.Formats, format) {
						t.Fatalf("missing %s in %#v", format, candidate.HDR)
					}
				}
			} else if candidate.HDR.Status == api.HDREvidenceComplete {
				t.Fatalf("malformed tags became complete: %#v", candidate.HDR)
			}
			target := btnPolicyTarget("WEB-DL", resolution, "H.265", "P2P")
			if tt.targetOrigin != "" {
				target.ReleaseOrigin = tt.targetOrigin
			}
			target.Category, target.Episode = "TV", 1
			targetMarker, targetFormats := tt.targetMarker, tt.targetFormats
			if targetFormats == nil {
				targetMarker = ".DV.HDR"
				targetFormats = []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10}
			}
			target.Names = []string{fmt.Sprintf("Example.Show.S01E01.%s.WEB-DL%s.H.265-OTHER", resolution, targetMarker)}
			target.HDR = api.HDRFacts{
				Formats: targetFormats,
				Status:  api.HDREvidenceComplete,
				Origin:  api.HDREvidenceMediaInfo,
			}
			evaluation := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *duplicatePolicy(), result.SearchEvidence())
			if got := evaluation.Candidates[0]; got.Relation != tt.want {
				t.Fatalf("relation=%s want=%s winning=%s findings=%#v", got.Relation, tt.want, got.WinningRule, got.Findings)
			}
			if evaluation.Blocks || evaluation.RequiresAction != (tt.want != api.DupeRelationCoexists) {
				t.Fatalf("unexpected review decision: blocks=%t action=%t", evaluation.Blocks, evaluation.RequiresAction)
			}
		})
	}
}
