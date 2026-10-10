// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTNativeSlotsRespectConflictingAndPartialAdapterEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, filename, resolution, codec, source, container string
		flags                                                any
		targetType, targetSource, targetEncoder              string
		want                                                 api.DupeRelation
	}{
		{"resolution conflict", "Example.2026.1080p.BluRay.x264-GRP.mkv", "720p", "H264", "BluRay", "mkv", []any{}, "ENCODE", "BluRay", "", api.DupeRelationManualReview},
		{"codec conflict", "Example.2026.1080p.BluRay.x264-GRP.mkv", "1080p", "AV1", "BluRay", "mkv", []any{}, "ENCODE", "BluRay", "", api.DupeRelationManualReview},
		{"source conflict", "Example.2026.1080p.WEB-DL.x264-GRP.mkv", "1080p", "H264", "BluRay", "mkv", []any{}, "ENCODE", "BluRay", "", api.DupeRelationManualReview},
		{"HDR conflict", "Example.2026.1080p.BluRay.DV.x264-GRP.mkv", "1080p", "H264", "BluRay", "mkv", []any{}, "ENCODE", "BluRay", "", api.DupeRelationManualReview},
		{"target codec conflict", "Example.2026.1080p.BluRay.x264-GRP.mkv", "1080p", "H264", "BluRay", "mkv", []any{}, "ENCODE", "BluRay", "AV1", api.DupeRelationManualReview},
		{"unknown flags same tier", "Example.2026.1080i.BluRay.x264-GRP.mkv", "1080i", "H264", "BluRay", "mkv", []any{"FUTURE_HDR"}, "ENCODE", "BluRay", "", api.DupeRelationManualReview},
		{"unknown flags WEB closing", "Example.2026.1080p.BluRay.x264-GRP.mkv", "1080p", "H264", "BluRay", "mkv", []any{"FUTURE_HDR"}, "WEBDL", "WEB", "", api.DupeRelationManualReview},
		{"malformed flags WEB closing", "Example.2026.1080p.BluRay.x264-GRP.mkv", "1080p", "H264", "BluRay", "mkv", "invalid", "WEBDL", "WEB", "", api.DupeRelationManualReview},
		{"malformed flag element WEB closing", "Example.2026.1080p.BluRay.x264-GRP.mkv", "1080p", "H264", "BluRay", "mkv", []any{nil}, "WEBDL", "WEB", "", api.DupeRelationManualReview},
		{"unknown flags different tier", "Example.2026.720p.BluRay.x264-GRP.mkv", "720p", "H264", "BluRay", "mkv", []any{"FUTURE_HDR"}, "ENCODE", "BluRay", "", api.DupeRelationCoexists},
		{"missing disc country distinct kind", "Example.2026.1080p.BluRay-GRP", "1080p", "H264", "BluRay", "BDMV", []any{}, "REMUX", "BluRay", "", api.DupeRelationCoexists},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := antDupeEntries(map[string]any{"item": []any{map[string]any{
				"files":      []any{map[string]any{"name": tc.filename}},
				"resolution": tc.resolution,
				"codec":      tc.codec,
				"media":      tc.source,
				"container":  tc.container,
				"flags":      tc.flags,
			}}})
			meta := api.UploadSubject{
				Type:       tc.targetType,
				Source:     tc.targetSource,
				VideoCodec: "H264",
				Release:    api.ReleaseInfo{Resolution: "1080p"},
			}
			target := api.TrackerDuplicateTarget{
				TrackerSlot: resolveTargetSlot(meta),
				Type:        meta.Type,
				Source:      meta.Source,
				Resolution:  "1080p",
				VideoCodec:  meta.VideoCodec,
				VideoEncode: tc.targetEncoder,
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{dupe.NormalizeCandidate(entries[0], "ANT")},
				*duplicatePolicy(), dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if got := result.Candidates[0].Relation; got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestHDR10PlusSharesSlotAndPreservesDolbyVisionSlot(t *testing.T) {
	t.Parallel()
	for _, withDV := range []bool{false, true} {
		for _, proposedPlus := range []bool{false, true} {
			base := api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatHDR10}}
			plus := api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatHDR10Plus}}
			if withDV {
				base.Formats = append(base.Formats, api.HDRFormatDolbyVision)
				plus.Formats = append(plus.Formats, api.HDRFormatDolbyVision)
			}
			flags := []string{"HDR10"}
			if withDV {
				flags = append(flags, "DV")
			}
			slot := antSlot("WEBDL", "WEB", "2160p", "H.265", flags, "", true)
			targetHDR, candidateHDR := base, plus
			if proposedPlus {
				targetHDR, candidateHDR = plus, base
			}
			result := dupe.Evaluate(api.TrackerDuplicateTarget{
				TrackerSlot: slot,
				Type:        "WEBDL",
				Source:      "WEB",
				Resolution:  "2160p",
				VideoCodec:  "H.265",
				HDR:         targetHDR,
			}, []dupe.TrackerCandidate{{
				TrackerSlot:   slot,
				CanonicalType: "WEBDL",
				Source:        "WEB",
				Resolution:    "2160p",
				Codec:         "H.265",
				HDR:           candidateHDR,
			}}, *Profile().DupePolicy, dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if got := result.Candidates[0].Relation; got != api.DupeRelationSameSlot {
				t.Fatalf("DV=%t proposed_plus=%t: relation %s, want same slot", withDV, proposedPlus, got)
			}
		}
	}
}

func TestANTNativeFlagsCheckReviewedNames(t *testing.T) {
	for _, tc := range []struct {
		name, targetEdition, target3D, targetMarker, candidateMarker string
		flags                                                        any
		want                                                         api.DupeRelation
	}{
		{"different cut conflict", "Extended", "", "", "Extended.Edition", []any{"IMAX"}, api.DupeRelationManualReview},
		{"directors alias conflict", "Directors", "", "", "DC", nil, api.DupeRelationManualReview},
		{"omitted edition flags", "IMAX", "", "", "IMAX", nil, api.DupeRelationManualReview},
		{"omitted 3D flag", "", "3D", "", "3D", nil, api.DupeRelationManualReview},
		{"BluRay3D source marker", "", "3D", "", "BluRay3D", nil, api.DupeRelationManualReview},
		{"Criterion conflict", "Criterion", "", "", "Criterion.Collection", nil, api.DupeRelationManualReview},
		{"4K remaster conflict", "4K Remaster", "", "", "4K.Remaster", nil, api.DupeRelationManualReview},
		{"reviewed target edition conflict", "", "", "IMAX", "", []any{"IMAX"}, api.DupeRelationManualReview},
		{"reviewed target 3D conflict", "", "", "3D", "", []any{"3D"}, api.DupeRelationManualReview},
		{"missing title markers valid", "IMAX", "", "", "", []any{"IMAX"}, api.DupeRelationSameSlot},
		{"ordinary remaster remains standard", "", "", "", "Remastered", nil, api.DupeRelationSameSlot},
		{"edition subsets valid", "IMAX Extended", "", "Extended", "Extended", []any{"IMAX", "Extended"}, api.DupeRelationSameSlot},
		{"missing 3D title marker valid", "", "3D", "", "", []any{"3D"}, api.DupeRelationSameSlot},
		{"title words do not contradict", "", "", "", "", nil, api.DupeRelationSameSlot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := "IMAX.3D.Extended.2026." + tc.candidateMarker + ".1080p.BluRay.x264-IMAX.mkv"
			item := map[string]any{
				"files":      []any{map[string]any{"name": filename}},
				"resolution": "1080p",
				"codec":      "H264",
				"media":      "BluRay",
				"container":  "mkv",
			}
			if tc.flags != nil {
				item["flags"] = tc.flags
			}
			entries := antDupeEntries(map[string]any{"item": []any{item}})
			meta := api.UploadSubject{
				Type:       "ENCODE",
				Source:     "BluRay",
				VideoCodec: "H264",
				Release:    api.ReleaseInfo{Resolution: "1080p"},
				Edition:    tc.targetEdition,
				Is3D:       tc.target3D,
			}
			target := api.TrackerDuplicateTarget{
				TrackerSlot: resolveTargetSlot(meta),
				Type:        meta.Type,
				Source:      meta.Source,
				Resolution:  "1080p",
				VideoCodec:  meta.VideoCodec,
				Edition:     meta.EditionLabel(),
				ThreeD:      meta.Is3D,
				Names:       []string{"Proposed.2026.1080p.BluRay.x264-OTHER"},
			}
			if tc.targetMarker != "" {
				target.Names = append(target.Names, "Proposed.2026."+tc.targetMarker+".1080p.BluRay.x264-OTHER")
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{dupe.NormalizeCandidate(entries[0], "ANT")}, *duplicatePolicy(), dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if got := result.Candidates[0].Relation; got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestANTFlagNamedGroupsPreserveDistinctTiers(t *testing.T) {
	for _, extension := range []string{".MKV", ".m2ts", ""} {
		for _, targetGroup := range []bool{false, true} {
			container := "mkv"
			candidateName := "Existing.2026.720p.BluRay.x264-IMAX" + extension
			targetName := "Proposed.2026.1080p.BluRay.x264-GRP"
			if targetGroup {
				candidateName = "Existing.2026.720p.BluRay.x264-GRP" + extension
				targetName = "Proposed.2026.1080p.BluRay.x264-IMAX" + extension
				if extension == ".m2ts" {
					targetName = "Proposed.2026.1080p.BluRay.AVC-IMAX.m2ts"
					candidateName = "Existing.2026.720p.BluRay.x264-GRP.mkv"
				}
			} else if extension == ".m2ts" {
				container = "m2ts"
			}
			entries := antDupeEntries(map[string]any{"item": []any{map[string]any{
				"files":      []any{map[string]any{"name": candidateName}},
				"resolution": "720p",
				"codec":      "H264",
				"media":      "BluRay",
				"container":  container,
			}}})
			meta := api.UploadSubject{
				Type:       "ENCODE",
				Source:     "BluRay",
				VideoCodec: "H264",
				Release:    api.ReleaseInfo{Resolution: "1080p"},
			}
			if targetGroup && extension == ".m2ts" {
				meta.Type, meta.DiscType, meta.Region = "DISC", "BDMV", "JP"
			}
			target := api.TrackerDuplicateTarget{
				Names:       []string{targetName},
				TrackerSlot: resolveTargetSlot(meta),
				Type:        meta.Type,
				Source:      meta.Source,
				Resolution:  "1080p",
				VideoCodec:  meta.VideoCodec,
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{dupe.NormalizeCandidate(entries[0], "ANT")}, *duplicatePolicy(), dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if got := result.Candidates[0].Relation; got != api.DupeRelationCoexists {
				t.Errorf("extension=%q target_group=%t: got %s, want coexists", extension, targetGroup, got)
			}
		}
	}
}
