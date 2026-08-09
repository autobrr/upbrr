// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSourceBackedDupeOverlaysResolveDeterministically(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	completeSDR := api.HDRFacts{
		Formats: []api.HDRFormat{api.HDRFormatSDR},
		Origin:  api.HDREvidenceTrackerAPI,
		Status:  api.HDREvidenceComplete,
	}
	tests := []struct {
		name      string
		tracker   string
		target    api.TrackerDuplicateTarget
		candidate dupe.TrackerCandidate
		relation  api.DupeRelation
	}{
		{
			name:    "AITHER WEB direction",
			tracker: "AITHER",
			target: api.TrackerDuplicateTarget{
				Type:       "WEB-DL",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			candidate: dupe.TrackerCandidate{
				Type:       "WEBRip",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			relation: api.DupeRelationProposedTrumps,
		},
		{
			name:    "ANT HDR compatibility",
			tracker: "ANT",
			target: api.TrackerDuplicateTarget{
				Type:       "REMUX",
				Resolution: "2160p",
				HDR: api.HDRFacts{
					Formats: []api.HDRFormat{api.HDRFormatHDR10Plus},
					Origin:  api.HDREvidenceTrackerAPI,
					Status:  api.HDREvidenceComplete,
				},
			},
			candidate: dupe.TrackerCandidate{
				Type:       "REMUX",
				Resolution: "2160p",
				HDR: api.HDRFacts{
					Formats: []api.HDRFormat{api.HDRFormatHDR10},
					Origin:  api.HDREvidenceTrackerAPI,
					Status:  api.HDREvidenceComplete,
				},
			},
			relation: api.DupeRelationProposedTrumps,
		},
		{
			name:    "LUME WEB over broadcast",
			tracker: "LUME",
			target: api.TrackerDuplicateTarget{
				Type:       "WEB-DL",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			candidate: dupe.TrackerCandidate{
				Type:       "HDTV",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			relation: api.DupeRelationProposedTrumps,
		},
		{
			name:    "SP season pack direction",
			tracker: "SP",
			target: api.TrackerDuplicateTarget{
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Episode:    2,
				HDR:        completeSDR,
			},
			candidate: dupe.TrackerCandidate{
				Type:       "WEB-DL",
				Resolution: "1080p",
				Season:     1,
				Pack:       true,
				HDR:        completeSDR,
			},
			relation: api.DupeRelationExistingPreferred,
		},
		{
			name:    "ULCX WEB direction",
			tracker: "ULCX",
			target: api.TrackerDuplicateTarget{
				Type:       "WEB-DL",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			candidate: dupe.TrackerCandidate{
				Type:       "WEBRip",
				Resolution: "1080p",
				HDR:        completeSDR,
			},
			relation: api.DupeRelationProposedTrumps,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			policy, ok := registry.LookupDupePolicy(test.tracker)
			if !ok {
				t.Fatalf("%s policy missing", test.tracker)
			}
			got := dupe.Evaluate(
				test.target,
				[]dupe.TrackerCandidate{test.candidate},
				policy,
				dupe.SearchEvidence{Complete: true},
			).Candidates[0]
			if got.Relation != test.relation {
				t.Fatalf("%s relation = %#v", test.tracker, got)
			}
		})
	}
}

func TestNamingOnlyTrackersUseCompatibilityDupePolicy(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	for _, tracker := range []string{"DP", "OE", "YUS"} {
		policy, ok := registry.LookupDupePolicy(tracker)
		if !ok || !strings.Contains(policy.ID, "/duplicate-compat/") || policy.SameSlotFallback == nil {
			t.Fatalf("%s compatibility policy = %#v, %t", tracker, policy, ok)
		}
	}
}

func TestARPTPRTFPoliciesAreEvidenceBackedAndConservative(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	for tracker, expected := range map[string]struct {
		id       string
		evidence string
	}{
		"AR":  {id: "ar/duplicate/v2", evidence: "ar-uploading-guidelines"},
		"PTP": {id: "ptp/duplicate/v2", evidence: "ptp-upload-rules-coexisting-trumping"},
		"RTF": {id: "rtf/duplicate/v2", evidence: "rtf-upload-rules"},
	} {
		policy, ok := registry.LookupDupePolicy(tracker)
		if !ok || policy.ID != expected.id || policy.EvidenceID != expected.evidence || strings.Contains(policy.ID, "duplicate-compat") {
			t.Fatalf("%s policy = %#v, %t", tracker, policy, ok)
		}
	}

	t.Run("AR tuple", func(t *testing.T) {
		policy, _ := registry.LookupDupePolicy("AR")
		target := api.TrackerDuplicateTarget{
			Source:      "BluRay",
			Resolution:  "1080p",
			VideoEncode: "x264",
			Group:       "GRP",
		}
		assertRelation := func(name string, candidate dupe.TrackerCandidate, want api.DupeRelation) {
			t.Helper()
			got := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, policy, dupe.SearchEvidence{Complete: true}).Candidates[0]
			if got.Relation != want {
				t.Fatalf("%s relation = %#v, want %s", name, got, want)
			}
		}
		assertRelation("equal", dupe.NormalizeCandidate(api.DupeEntry{
			Name: "Example.Release.2026.1080p.BluRay.x264-GRP",
		}, "AR"), api.DupeRelationSameSlot)
		assertRelation("different source", dupe.NormalizeCandidate(api.DupeEntry{
			Name: "Example.Release.2026.1080p.WEB-DL.H.264-GRP",
		}, "AR"), api.DupeRelationCoexists)
		assertRelation("missing group", dupe.NormalizeCandidate(api.DupeEntry{
			Name: "Example.Release.2026.1080p.BluRay.x264",
		}, "AR"), api.DupeRelationInsufficientEvidence)
		assertRelation("structured title conflict", dupe.NormalizeCandidate(api.DupeEntry{
			Name:   "Example.Release.2026.1080p.BluRay.x264-GRP",
			Source: "WEB",
		}, "AR"), api.DupeRelationManualReview)
		episodeTarget := target
		episodeTarget.Season = 1
		episodeTarget.Episode = 1
		assertAREvaluation := func(name string, candidate dupe.TrackerCandidate, want api.DupeRelation) {
			t.Helper()
			got := dupe.Evaluate(episodeTarget, []dupe.TrackerCandidate{candidate}, policy, dupe.SearchEvidence{Complete: true}).Candidates[0]
			if got.Relation != want {
				t.Fatalf("%s relation = %#v, want %s", name, got, want)
			}
		}
		assertAREvaluation("different episode", dupe.TrackerCandidate{
			Source: "BluRay",
 Resolution: "1080p",
 Codec: "x264",
 Group: "GRP",
 Season: 1,
 Episode: 2,
		}, api.DupeRelationCoexists)
		assertAREvaluation("season pack overlap", dupe.TrackerCandidate{
			Source: "BluRay",
 Resolution: "1080p",
 Codec: "x264",
 Group: "GRP",
 Season: 1,
 Pack: true,
		}, api.DupeRelationSameSlot)
		assertRelation("filename packaging is not authoritative", dupe.TrackerCandidate{
			Source: "BluRay",
 Resolution: "1080p",
 Codec: "x264",
 Group: "GRP",
			Files: []string{"Example.Release.2026.1080p.BluRay.x264-GRP.mkv"},
		}, api.DupeRelationSameSlot)
	})

	t.Run("RTF directional rules", func(t *testing.T) {
		policy, _ := registry.LookupDupePolicy("RTF")
		sdr := completeHDR(api.HDRFormatSDR)
		encode1080 := api.TrackerDuplicateTarget{
Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 HDR: sdr,
}
		assertRTF := func(name string, target api.TrackerDuplicateTarget, candidate dupe.TrackerCandidate, want api.DupeRelation) {
			t.Helper()
			got := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, policy, dupe.SearchEvidence{Complete: true}).Candidates[0]
			if got.Relation != want {
				t.Fatalf("%s relation = %#v, want %s", name, got, want)
			}
		}
		assertRTF("proposed resolution", encode1080, dupe.TrackerCandidate{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "720p",
 HDR: sdr,
		}, api.DupeRelationProposedTrumps)
		assertRTF("existing resolution", api.TrackerDuplicateTarget{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "720p",
 HDR: sdr,
		}, dupe.TrackerCandidate{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 HDR: sdr,
		}, api.DupeRelationExistingPreferred)
		assertRTF("proposed remux", encode1080, dupe.TrackerCandidate{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 HDR: sdr,
		}, api.DupeRelationSameSlot)
		assertRTF("remux over encode", api.TrackerDuplicateTarget{
			Type: "REMUX",
 Source: "BluRay",
 Resolution: "1080p",
 HDR: sdr,
		}, dupe.TrackerCandidate{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 HDR: sdr,
		}, api.DupeRelationProposedTrumps)
		assertRTF("existing remux over encode", encode1080, dupe.TrackerCandidate{
			Type: "REMUX",
 Source: "BluRay",
 Resolution: "1080p",
 HDR: sdr,
		}, api.DupeRelationExistingPreferred)
		assertRTF("PAL NTSC DVD", api.TrackerDuplicateTarget{
			Type: "DISC",
 Source: "DVD",
 Resolution: "480i",
 Region: "PAL",
 HDR: sdr,
		}, dupe.TrackerCandidate{
			Type: "DISC",
 Source: "DVD",
 Resolution: "480i",
 Region: "NTSC",
 HDR: sdr,
		}, api.DupeRelationCoexists)
		assertRTF("PAL NTSC encode remains review", api.TrackerDuplicateTarget{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 Region: "PAL",
 HDR: sdr,
		}, dupe.TrackerCandidate{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 Region: "NTSC",
 HDR: sdr,
		}, api.DupeRelationSameSlot)
		assertRTF("missing media", api.TrackerDuplicateTarget{
Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
},
			dupe.TrackerCandidate{Resolution: "1080p"}, api.DupeRelationInsufficientEvidence)
		assertRTF("container direction", api.TrackerDuplicateTarget{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 Container: "MKV",
		}, dupe.TrackerCandidate{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 Container: "MP4",
		}, api.DupeRelationManualReview)
		assertRTF("upscale title remains review", encode1080, dupe.TrackerCandidate{
			Name: "Example.Release.2026.1080p.BluRay.x264.Upscale-GRP",
 Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 HDR: sdr,
		}, api.DupeRelationSameSlot)
	})

	t.Run("PTP structural rules", func(t *testing.T) {
		policy, _ := registry.LookupDupePolicy("PTP")
		sdr := completeHDR(api.HDRFormatSDR)
		hdr := completeHDR(api.HDRFormatHDR10)
		assertPTP := func(name string, target api.TrackerDuplicateTarget, candidate dupe.TrackerCandidate, want api.DupeRelation) {
			t.Helper()
			got := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, policy, dupe.SearchEvidence{Complete: true}).Candidates[0]
			if got.Relation != want {
				t.Fatalf("%s relation = %#v, want %s", name, got, want)
			}
		}
		web1080 := api.TrackerDuplicateTarget{
Type: "WEBDL",
 Source: "WEB",
 Resolution: "1080p",
 Provider: "A",
 HDR: sdr,
}
		assertPTP("same WEB-DL slot across providers", web1080, dupe.TrackerCandidate{
			Type: "WEBDL",
 Source: "WEB",
 Resolution: "1080p",
 Provider: "B",
 HDR: sdr,
		}, api.DupeRelationSameSlot)
		assertPTP("HD WEB-DL over SD", web1080, dupe.TrackerCandidate{
			Type: "WEBDL",
 Source: "WEB",
 Resolution: "480p",
 HDR: sdr,
		}, api.DupeRelationProposedTrumps)
		assertPTP("existing HD WEB-DL", api.TrackerDuplicateTarget{
			Type: "WEBDL",
 Source: "WEB",
 Resolution: "480p",
 HDR: sdr,
		}, dupe.TrackerCandidate{
			Type: "WEBDL",
 Source: "WEB",
 Resolution: "1080p",
 HDR: sdr,
		}, api.DupeRelationExistingPreferred)
		assertPTP("media family", api.TrackerDuplicateTarget{
			Type: "REMUX",
 Source: "BluRay",
 Resolution: "1080p",
 HDR: sdr,
		}, dupe.TrackerCandidate{
			Type: "WEBDL",
 Source: "WEB",
 Resolution: "1080p",
 HDR: sdr,
		}, api.DupeRelationCoexists)
		assertPTP("PAL NTSC DVD", api.TrackerDuplicateTarget{
			Type: "DISC",
 Source: "DVD",
 Resolution: "480i",
 Region: "PAL",
 HDR: sdr,
		}, dupe.TrackerCandidate{
			Type: "DISC",
 Source: "DVD",
 Resolution: "480i",
 Region: "NTSC",
 HDR: sdr,
		}, api.DupeRelationCoexists)
		assertPTP("PAL NTSC encode remains review", api.TrackerDuplicateTarget{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 Region: "PAL",
 HDR: sdr,
		}, dupe.TrackerCandidate{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 Region: "NTSC",
 HDR: sdr,
		}, api.DupeRelationInsufficientEvidence)
		assertPTP("cut separation", api.TrackerDuplicateTarget{
			Type: "REMUX",
 Source: "BluRay",
 Resolution: "1080p",
 Edition: "Director's Cut",
 HDR: sdr,
		}, dupe.TrackerCandidate{
			Type: "REMUX",
 Source: "BluRay",
 Resolution: "1080p",
 Edition: "Theatrical Cut",
 HDR: sdr,
		}, api.DupeRelationCoexists)
		assertPTP("title-only cut is not authoritative", api.TrackerDuplicateTarget{
			Type: "REMUX",
 Source: "BluRay",
 Resolution: "1080p",
 Edition: "Director's Cut",
 HDR: sdr,
		}, dupe.NormalizeCandidate(api.DupeEntry{
			Name: "Example.Release.2026.1080p.BluRay.Remux.Theatrical.Cut.SDR-GRP",
			Type: "REMUX",
 Source: "BluRay",
 Res: "1080p",
 HDR: sdr,
		}, "PTP"), api.DupeRelationInsufficientEvidence)
		assertPTP("generic remaster does not create a cut slot", api.TrackerDuplicateTarget{
			Type: "REMUX",
 Source: "BluRay",
 Resolution: "1080p",
 Edition: "Director's Cut",
 HDR: sdr,
		}, dupe.NormalizeCandidate(api.DupeEntry{
			Name: "Example.Release.2026.1080p.BluRay.Remux.SDR-GRP",
			Type: "REMUX",
 Source: "BluRay",
 Res: "1080p",
 HDR: sdr,
		}, "PTP"), api.DupeRelationInsufficientEvidence)
		assertPTP("1080p HDR x265", api.TrackerDuplicateTarget{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 VideoEncode: "x265",
 HDR: hdr,
		}, dupe.TrackerCandidate{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 Codec: "x264",
 HDR: sdr,
		}, api.DupeRelationCoexists)
		assertPTP("2160p HDR SDR", api.TrackerDuplicateTarget{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "2160p",
 VideoEncode: "x265",
 HDR: hdr,
		}, dupe.TrackerCandidate{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "2160p",
 Codec: "x265",
 HDR: sdr,
		}, api.DupeRelationCoexists)

		targetDV := completeHDR(api.HDRFormatDolbyVision)
		targetDV.DolbyVisionProfile = "5"
		candidateDV := completeHDR(api.HDRFormatDolbyVision)
		candidateDV.DolbyVisionProfile = "8"
		assertPTP("DV Profile 5 slot", api.TrackerDuplicateTarget{
			Type: "WEBDL",
 Source: "WEB",
 Resolution: "2160p",
 HDR: targetDV,
		}, dupe.TrackerCandidate{
			Type: "WEBDL",
 Source: "WEB",
 Resolution: "2160p",
 HDR: candidateDV,
		}, api.DupeRelationCoexists)
		candidateDV.DolbyVisionProfile = ""
		assertPTP("DV profile missing", api.TrackerDuplicateTarget{
			Type: "WEBDL",
 Source: "WEB",
 Resolution: "2160p",
 HDR: targetDV,
		}, dupe.TrackerCandidate{
			Type: "WEBDL",
 Source: "WEB",
 Resolution: "2160p",
 HDR: candidateDV,
		}, api.DupeRelationInsufficientEvidence)
		partialProfile5 := api.HDRFacts{
			Formats: []api.HDRFormat{api.HDRFormatDolbyVision},
 DolbyVisionProfile: "5",
			Origin: api.HDREvidenceTrackerTitle,
 Status: api.HDREvidencePartial,
		}
		authoritativeProfile8 := completeHDR(api.HDRFormatDolbyVision)
		authoritativeProfile8.DolbyVisionProfile = "8"
		assertPTP("DV Profile 5 title marker is not authoritative", api.TrackerDuplicateTarget{
			Type: "WEBDL",
 Source: "WEB",
 Resolution: "2160p",
 HDR: authoritativeProfile8,
		}, dupe.TrackerCandidate{
			Type: "WEBDL",
 Source: "WEB",
 Resolution: "2160p",
 HDR: partialProfile5,
		}, api.DupeRelationInsufficientEvidence)
		assertPTP("encode capacity missing size", api.TrackerDuplicateTarget{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 VideoEncode: "x264",
 HDR: sdr,
		}, dupe.TrackerCandidate{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 Codec: "x264",
 HDR: sdr,
		}, api.DupeRelationInsufficientEvidence)
		assertPTP("encode capacity available", api.TrackerDuplicateTarget{
			Type:        "ENCODE",
			Source:      "BluRay",
			Resolution:  "1080p",
			VideoEncode: "x264",
			SizeBytes:   100,
			HDR:         sdr,
		}, dupe.TrackerCandidate{
			ID:         "candidate-1",
			Type:       "ENCODE",
			Source:     "BluRay",
			Resolution: "1080p",
			Codec:      "x264",
			SizeBytes:  80,
			SizeKnown:  true,
			HDR:        sdr,
		}, api.DupeRelationCoexists)
		assertPTP("tracker trumpable", api.TrackerDuplicateTarget{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 VideoEncode: "x264",
 HDR: sdr,
		}, dupe.TrackerCandidate{
			Type: "ENCODE",
 Source: "BluRay",
 Resolution: "1080p",
 Codec: "x264",
 HDR: sdr,
 Trumpable: true,
		}, api.DupeRelationProposedTrumps)
		assertPTP("missing evidence", web1080, dupe.TrackerCandidate{Type: "WEBDL", Resolution: "1080p"},
			api.DupeRelationInsufficientEvidence)
	})
}

func completeHDR(formats ...api.HDRFormat) api.HDRFacts {
	return api.HDRFacts{
		Formats: formats,
		Origin:  api.HDREvidenceMediaInfo,
		Status:  api.HDREvidenceComplete,
	}
}
