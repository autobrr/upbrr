// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBTNSceneSeasonPackAPISlots(t *testing.T) {
	t.Parallel()
	slots := []struct {
		name    string
		tags    []string
		formats []api.HDRFormat
	}{
		{
			name:    "SDR",
			tags:    []string{},
			formats: []api.HDRFormat{api.HDRFormatSDR},
		},
		{
			name:    "DV",
			tags:    []string{"Dolby Vision"},
			formats: []api.HDRFormat{api.HDRFormatDolbyVision},
		},
		{
			name:    "HDR",
			tags:    []string{"HDR10 Compatible"},
			formats: []api.HDRFormat{api.HDRFormatHDR10},
		},
		{
			name:    "DV_HDR",
			tags:    []string{"Dolby Vision", "HDR10 Compatible"},
			formats: []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
		},
		{name: "unknown", tags: nil},
	}
	for _, resolution := range []string{"1080p", "2160p"} {
		for _, targetSlot := range slots[:4] {
			for _, candidateSlot := range slots {
				for _, origin := range []string{"Scene", "P2P", "Internal"} {
					t.Run(resolution+"/"+targetSlot.name+"/"+candidateSlot.name+"/"+origin, func(t *testing.T) {
						t.Parallel()
						torrent := map[string]any{
							"TorrentID":   "777",
							"TvdbID":      "1234567",
							"ReleaseName": fmt.Sprintf("Example.Show.S01.%s.WEB-DL.H.265-GRP", resolution),
							"GroupName":   "Season 1",
							"Category":    "Season",
							"Source":      "WEB-DL",
							"Resolution":  resolution,
							"Codec":       "H.265",
							"Origin":      origin,
							"Tags":        candidateSlot.tags,
						}
						raw, err := json.Marshal(map[string]any{"result": map[string]any{"results": "1", "torrents": map[string]any{"777": torrent}}})
						if err != nil {
							t.Fatal(err)
						}
						payloads := captureBTNPayloads(t, string(raw))
						adapter := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)
						result := adapter.Search(t.Context(), api.DuplicateSubject{Identity: api.ExternalIdentity{Category: "TV", TVDBID: 1234567}})
						if result.Cause() != nil || len(result.Entries()) != 1 {
							t.Fatalf("search failed: %v", result.Cause())
						}
						candidate := dupe.NormalizeCandidate(result.Entries()[0], "BTN")
						if origin == "Internal" && (!candidate.Internal || candidate.ReleaseOrigin != "None") {
							t.Fatalf("internal origin normalization = %#v", candidate)
						}
						target := btnPolicyTarget("WEB-DL", resolution, "H.265", "Scene")
						target.Category, target.Pack = "TV", true
						target.Names = []string{fmt.Sprintf("Example.Show.S01.%s.WEB-DL.H.265-OTHER", resolution)}
						target.HDR = api.HDRFacts{
							Formats: targetSlot.formats,
							Status:  api.HDREvidenceComplete,
							Origin:  api.HDREvidenceMediaInfo,
						}
						evaluation := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *duplicatePolicy(), result.SearchEvidence())
						wantAction := resolution == "1080p" || candidateSlot.name == "unknown" || targetSlot.name == candidateSlot.name
						if evaluation.Blocks || evaluation.RequiresAction != wantAction {
							t.Fatalf("scene slot decision: blocks=%t action=%t wantAction=%t candidates=%#v sets=%#v", evaluation.Blocks, evaluation.RequiresAction, wantAction, evaluation.Candidates, evaluation.SetFindings)
						}
					})
				}
			}
		}
	}
}
