// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"fmt"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func aitherHDR(formats ...api.HDRFormat) api.HDRFacts {
	return api.HDRFacts{
		Formats: formats,
		Origin:  api.HDREvidenceTrackerAPI,
		Status:  api.HDREvidenceComplete,
	}
}

func aitherTarget(kind, resolution, codec string, size int64) api.TrackerDuplicateTarget {
	source := "BluRay"
	if kind == "WEBDL" || kind == "WEB-DL" || kind == "WEBRip" || kind == "WEB-RIP" {
		source = "WEB"
	}
	return api.TrackerDuplicateTarget{
		Type:       kind,
		Source:     source,
		Provider:   "AMZN",
		Resolution: resolution,
		VideoCodec: codec,
		SizeBytes:  size,
		HDR:        aitherHDR(api.HDRFormatSDR),
	}
}

func aitherCandidate(id string, target api.TrackerDuplicateTarget) dupe.TrackerCandidate {
	// Exercise the real adapter normalization contract, including codec aliases.
	return dupe.NormalizeCandidate(api.DupeEntry{
		ID:            id,
		CanonicalType: target.Type,
		Source:        target.Source,
		Provider:      target.Provider,
		Res:           target.Resolution,
		Codec:         target.VideoCodec,
		SizeBytes:     target.SizeBytes,
		SizeKnown:     target.SizeBytes > 0,
		HDR:           target.HDR,
		Edition:       target.Edition,
		Region:        target.Region,
		ThreeD:        target.ThreeD,
	}, "AITHER")
}

func aitherEvaluate(target api.TrackerDuplicateTarget, candidates ...dupe.TrackerCandidate) dupe.Evaluation {
	return dupe.Evaluate(target, candidates, *Profile().DupePolicy, dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
}

func TestDuplicateSingleSlotSizeNeverGrantsCoexistence(t *testing.T) {
	t.Parallel()
	for _, slot := range []struct{ kind, resolution, codec string }{
		{"ENCODE", "1080p", "x265"}, {"ENCODE", "2160p", "x265"},
		{"WEBDL", "1080p", "H.264"}, {"REMUX", "1080p", "H.264"},
	} {
		for _, sizes := range [][2]int64{{10, 20}, {20, 10}, {100, 99}} {
			t.Run(fmt.Sprintf("%s_%s_%d_%d", slot.kind, slot.resolution, sizes[0], sizes[1]), func(t *testing.T) {
				target := aitherTarget(slot.kind, slot.resolution, slot.codec, sizes[0])
				existing := target
				existing.SizeBytes = sizes[1]
				got := aitherEvaluate(target, aitherCandidate("existing", existing))
				if got.Candidates[0].Relation == api.DupeRelationCoexists || !got.RequiresAction {
					t.Fatalf("single occupied slot hidden: %#v", got)
				}
			})
		}
	}
}

func TestDuplicateX264ThresholdAndCapacity(t *testing.T) {
	t.Parallel()
	for _, size := range []int64{8001, 8000, 7999} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d_reverse_%t", size, reverse), func(t *testing.T) {
				target := aitherTarget("ENCODE", "1080p", "x264", 10000)
				existing := target
				existing.SizeBytes = size
				if reverse {
					target.SizeBytes, existing.SizeBytes = existing.SizeBytes, target.SizeBytes
				}
				got := aitherEvaluate(target, aitherCandidate("existing", existing))
				want := api.DupeRelationCoexists
				if size == 8001 {
					want = api.DupeRelationManualReview
				}
				if got.Candidates[0].Relation != want || got.RequiresAction != (size == 8001) {
					t.Fatalf("threshold: %#v", got)
				}
			})
		}
	}
	target := aitherTarget("ENCODE", "1080p", "x264", 10)
	first := target
	first.SizeBytes = 20
	second := target
	second.SizeBytes = 30
	candidates := []dupe.TrackerCandidate{aitherCandidate("first", first), aitherCandidate("second", second)}
	for range 2 {
		got := aitherEvaluate(target, candidates...)
		if !got.RequiresAction || got.Blocks || len(got.SetFindings) != 1 || got.SetFindings[0].ExistingOccupancy != 2 {
			t.Fatalf("third encode: %#v", got)
		}
		for _, candidate := range got.Candidates {
			if candidate.Relation == api.DupeRelationCoexists {
				t.Fatalf("third encode hidden: %#v", got)
			}
		}
		slices.Reverse(candidates)
	}
}

func TestDuplicateX264IncompleteEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		mutate     func(*api.TrackerDuplicateTarget, *dupe.TrackerCandidate)
		incomplete bool
	}{
		{name: "target size", mutate: func(target *api.TrackerDuplicateTarget, _ *dupe.TrackerCandidate) { target.SizeBytes = 0 }},
		{name: "candidate size", mutate: func(_ *api.TrackerDuplicateTarget, candidate *dupe.TrackerCandidate) {
			candidate.SizeBytes = 0
			candidate.SizeKnown = false
		}},
		{name: "candidate codec", mutate: func(_ *api.TrackerDuplicateTarget, candidate *dupe.TrackerCandidate) { candidate.Codec = "" }},
		{name: "candidate type", mutate: func(_ *api.TrackerDuplicateTarget, candidate *dupe.TrackerCandidate) {
			candidate.Type = ""
			candidate.CanonicalType = ""
		}},
		{name: "candidate resolution", mutate: func(_ *api.TrackerDuplicateTarget, candidate *dupe.TrackerCandidate) { candidate.Resolution = "" }},
		{name: "candidate conflicting codec", mutate: func(_ *api.TrackerDuplicateTarget, candidate *dupe.TrackerCandidate) {
			candidate.Name = "Example.Movie.2026.1080p.BluRay.x265-GRP"
		}},
		{name: "incomplete search", incomplete: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := aitherTarget("ENCODE", "1080p", "x264", 100)
			existing := target
			existing.SizeBytes = 50
			candidate := aitherCandidate("existing", existing)
			if test.mutate != nil {
				test.mutate(&target, &candidate)
			}
			got := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *Profile().DupePolicy, dupe.SearchEvidence{Complete: !test.incomplete, WorkScope: dupe.WorkScopeProviderID})
			if !got.RequiresAction || got.Candidates[0].Relation == api.DupeRelationCoexists {
				t.Fatalf("incomplete evidence granted vacancy: %#v", got)
			}
		})
	}
}

func TestDuplicateCodecSlotsAndAliases(t *testing.T) {
	t.Parallel()
	for _, codec := range []string{"x264", "H.264", "AVC"} {
		target := aitherTarget("ENCODE", "1080p", codec, 100)
		for _, existingCodec := range []string{"x264", "H.264", "AVC", "x265", "H.265", "HEVC"} {
			t.Run(codec+"_"+existingCodec, func(t *testing.T) {
				existing := target
				existing.VideoCodec = existingCodec
				got := aitherEvaluate(target, aitherCandidate("existing", existing))
				distinct := existingCodec == "x265" || existingCodec == "H.265" || existingCodec == "HEVC"
				if (got.Candidates[0].Relation == api.DupeRelationCoexists) != distinct || got.RequiresAction == distinct {
					t.Fatalf("codec slots: %#v", got)
				}
			})
		}
	}
}

func TestDuplicateX264HDRSharesCapacity(t *testing.T) {
	t.Parallel()
	target := aitherTarget("ENCODE", "1080p", "x264", 100)
	target.HDR = aitherHDR(api.HDRFormatHDR10)
	existing := target
	existing.SizeBytes = 50
	got := aitherEvaluate(target, aitherCandidate("existing", existing))
	if got.Candidates[0].Relation != api.DupeRelationCoexists || got.RequiresAction {
		t.Fatalf("complete matching HDR must not require SDR: %#v", got)
	}
	existing.HDR = aitherHDR(api.HDRFormatSDR)
	got = aitherEvaluate(target, aitherCandidate("existing", existing))
	if !got.RequiresAction || got.Candidates[0].Relation == api.DupeRelationCoexists {
		t.Fatalf("different HDR granted extra family: %#v", got)
	}
	second := existing
	second.SizeBytes = 30
	got = aitherEvaluate(target, aitherCandidate("existing", existing), aitherCandidate("second", second))
	if len(got.SetFindings) != 1 || got.SetFindings[0].ExistingOccupancy != 2 || !got.RequiresAction {
		t.Fatalf("HDR split capacity: %#v", got)
	}
}

func TestDuplicateHDRCompatibilityDirections(t *testing.T) {
	t.Parallel()
	for _, slot := range []struct{ kind, resolution string }{
		{"WEBDL", "1080p"}, {"WEBDL", "1440p"}, {"WEBDL", "2160p"},
		{"ENCODE", "1080p"}, {"ENCODE", "2160p"}, {"REMUX", "2160p"},
	} {
		for _, format := range []api.HDRFormat{api.HDRFormatHDR10, api.HDRFormatHDR10Plus} {
			if format == api.HDRFormatHDR10Plus && slot.resolution != "2160p" {
				continue
			}
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s_%s_%s_reverse_%t", slot.kind, slot.resolution, format, reverse), func(t *testing.T) {
					target := aitherTarget(slot.kind, slot.resolution, "H.265", 100)
					target.HDR = aitherHDR(api.HDRFormatDolbyVision, format)
					existing := target
					existing.HDR = aitherHDR(format)
					want := api.DupeRelationProposedTrumps
					if reverse {
						target.HDR, existing.HDR = existing.HDR, target.HDR
						want = api.DupeRelationExistingPreferred
					}
					if format == api.HDRFormatHDR10Plus && slot.kind != "WEBDL" {
						want = api.DupeRelationManualReview
					}
					got := aitherEvaluate(target, aitherCandidate("existing", existing))
					if got.Candidates[0].Relation != want {
						t.Fatalf("directional pair: %#v", got)
					}
				})
			}
		}
	}
}

func TestDuplicateHDRDistinctAndUnsupportedFamilies(t *testing.T) {
	t.Parallel()
	target := aitherTarget("WEBDL", "2160p", "H.265", 100)
	target.HDR = aitherHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10)
	for _, format := range []api.HDRFormat{api.HDRFormatSDR, api.HDRFormatDolbyVision, api.HDRFormatHDR10Plus, api.HDRFormatHLG} {
		existing := target
		existing.HDR = aitherHDR(format)
		got := aitherEvaluate(target, aitherCandidate("existing", existing))
		if format == api.HDRFormatHLG {
			if got.Candidates[0].Relation == api.DupeRelationCoexists || !got.RequiresAction {
				t.Fatalf("unlisted HLG slot: %#v", got)
			}
		} else if got.Candidates[0].Relation != api.DupeRelationCoexists || got.RequiresAction {
			t.Fatalf("distinct matrix family: %#v", got)
		}
	}
	for _, mutate := range []func(*api.TrackerDuplicateTarget){
		func(x *api.TrackerDuplicateTarget) { x.Type = "ENCODE"; x.Source = "BluRay"; x.Resolution = "720p" },
		func(x *api.TrackerDuplicateTarget) { x.Resolution = "1080p"; x.HDR = aitherHDR(api.HDRFormatHDR10Plus) },
		func(x *api.TrackerDuplicateTarget) { x.VideoCodec = "AV1" },
	} {
		unlisted := target
		mutate(&unlisted)
		got := aitherEvaluate(unlisted)
		if !got.RequiresAction {
			t.Fatalf("empty search authorized unlisted slot: %#v", got)
		}
	}
}

func TestDuplicateAvailabilityAndStaffEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		target api.TrackerDuplicateTarget
	}{
		{"SD web", aitherTarget("WEBDL", "480p", "H.264", 100)},
		{"SD dvdrip", aitherTarget("DVDRip", "576p", "x264", 100)},
		{"1440p", aitherTarget("WEBDL", "1440p", "H.264", 100)},
		{"WEBRip", aitherTarget("WEBRip", "1080p", "H.264", 100)},
		{"WEB-RIP", aitherTarget("WEB-RIP", "1080p", "H.264", 100)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := aitherEvaluate(test.target); !got.RequiresAction {
				t.Fatalf("search absence supplied external evidence: %#v", got)
			}
		})
	}
	for _, kind := range []string{"ENCODE", "REMUX"} {
		target := aitherTarget(kind, "2160p", "H.265", 100)
		target.HDR = aitherHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10Plus)
		if got := aitherEvaluate(target); !got.RequiresAction {
			t.Fatalf("missing hybrid evidence: %#v", got)
		}
	}
	for _, structured := range []bool{false, true} {
		target := aitherTarget("ENCODE", "1080p", "x265", 100)
		if structured {
			target.Edition = "Open Matte"
		} else {
			target.Names = []string{"Example.Movie.2026.Open.Matte.1080p.BluRay.x265-GRP"}
		}
		if got := aitherEvaluate(target); !got.RequiresAction {
			t.Fatalf("Open Matte did not establish staff approval: %#v", got)
		}
	}
}

func TestDuplicateCompoundOpenMatteStillNeedsApproval(t *testing.T) {
	t.Parallel()
	for _, edition := range []string{"Extended Cut Open Matte", "Unrated Open Matte"} {
		target := aitherTarget("ENCODE", "1080p", "x265", 100)
		target.Edition = edition
		if got := aitherEvaluate(target); !got.RequiresAction {
			t.Fatalf("compound Open Matte acquired approval: %#v", got)
		}
	}
}

func TestDuplicateWEBPrecedenceRequiresSameSource(t *testing.T) {
	t.Parallel()
	target := aitherTarget("WEBDL", "1080p", "H.264", 100)
	existing := target
	existing.Type = "WEBRip"
	// Unit3D entries provide the category and provider but no structured Source.
	existing.Source = ""
	for _, types := range [][2]string{{"WEBDL", "WEBRip"}, {"WEB-DL", "WEB-RIP"}} {
		target.Type, existing.Type = types[0], types[1]
		for _, reverse := range []bool{false, true} {
			proposed, candidate := target, existing
			want := api.DupeRelationProposedTrumps
			if reverse {
				proposed, candidate = candidate, proposed
				want = api.DupeRelationExistingPreferred
			}
			got := aitherEvaluate(proposed, aitherCandidate("existing", candidate))
			if got.Candidates[0].Relation != want {
				t.Fatalf("WEB direction: %#v", got)
			}
		}
	}
	for _, provider := range []string{"", "NF"} {
		existing.Provider = provider
		got := aitherEvaluate(target, aitherCandidate("existing", existing))
		if got.Candidates[0].Relation == api.DupeRelationProposedTrumps || got.Candidates[0].Relation == api.DupeRelationCoexists || !got.RequiresAction {
			t.Fatalf("unevidenced same source: %#v", got)
		}
	}
}

func TestDuplicateIdentityAndVariantPreservation(t *testing.T) {
	t.Parallel()
	target := aitherTarget("ENCODE", "1080p", "x265", 100)
	for _, test := range []struct {
		name     string
		mutate   func(*api.TrackerDuplicateTarget, *dupe.TrackerCandidate)
		relation api.DupeRelation
	}{
		{"exact", func(target *api.TrackerDuplicateTarget, candidate *dupe.TrackerCandidate) {
			target.Names = []string{"Example.Movie.2026.1080p.BluRay.x265-GRP"}
			candidate.Name = target.Names[0]
		}, api.DupeRelationExactDuplicate},
		{"cut", func(target *api.TrackerDuplicateTarget, candidate *dupe.TrackerCandidate) {
			target.Edition = "Theatrical"
			candidate.Edition = "Extended"
		}, api.DupeRelationCoexists},
		{"3D", func(target *api.TrackerDuplicateTarget, candidate *dupe.TrackerCandidate) {
			target.ThreeD = "2d"
			candidate.ThreeD = "3d"
		}, api.DupeRelationCoexists},
		{"region", func(target *api.TrackerDuplicateTarget, candidate *dupe.TrackerCandidate) {
			target.Region = "USA"
			candidate.Region = "GBR"
		}, api.DupeRelationCoexists},
	} {
		t.Run(test.name, func(t *testing.T) {
			proposed := target
			candidate := aitherCandidate("existing", target)
			test.mutate(&proposed, &candidate)
			got := aitherEvaluate(proposed, candidate)
			if got.Candidates[0].Relation != test.relation {
				t.Fatalf("preservation: %#v", got)
			}
		})
	}
	// Disc capacity labels do not establish different content or regions.
	disc := aitherTarget("BD50", "1080p", "H.264", 100)
	existing := disc
	existing.Type = "BD100"
	existing.SizeBytes = 200
	if got := aitherEvaluate(disc, aitherCandidate("existing", existing)); got.Candidates[0].Relation == api.DupeRelationCoexists || !got.RequiresAction {
		t.Fatalf("disc capacity became exception: %#v", got)
	}
}

func TestDuplicateVacantX264NeedsCompleteEvidence(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"size", "hdr", "codec", "type", "resolution"} {
		target := aitherTarget("ENCODE", "1080p", "x264", 100)
		switch missing {
		case "size":
			target.SizeBytes = 0
		case "hdr":
			target.HDR = api.HDRFacts{}
		case "codec":
			target.VideoCodec = ""
		case "type":
			target.Type = ""
		case "resolution":
			target.Resolution = ""
		}
		if got := aitherEvaluate(target); !got.RequiresAction {
			t.Fatalf("empty x264 search accepted missing %s: %#v", missing, got)
		}
	}
}

func TestDuplicateWEBSourceEncodeUsesEncodeCapacity(t *testing.T) {
	t.Parallel()
	target := aitherTarget("ENCODE", "1080p", "x264", 100)
	target.Source = "WEB"
	target.Names = []string{"Example.Movie.2026.1080p.WEBRip.x264-GRP"}
	existing := aitherTarget("ENCODE", "1080p", "x264", 50)
	got := aitherEvaluate(target, aitherCandidate("existing", existing))
	if len(got.SetFindings) != 1 || got.SetFindings[0].ExistingOccupancy != 1 || !got.RequiresAction {
		t.Fatalf("WEB-source encode escaped encode competition or comparison review: %#v", got)
	}
}

func TestDuplicateHDRPrecedencePreservesVariants(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"edition", "region", "3d"} {
		target := aitherTarget("ENCODE", "2160p", "x265", 100)
		target.HDR = aitherHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10)
		existing := target
		existing.HDR = aitherHDR(api.HDRFormatHDR10)
		switch variant {
		case "edition":
			target.Edition = "Theatrical"
			existing.Edition = "Extended"
		case "region":
			target.Region = "USA"
			existing.Region = "GBR"
		case "3d":
			target.ThreeD = "3d"
			existing.ThreeD = "2d"
		}
		got := aitherEvaluate(target, aitherCandidate("existing", existing))
		if got.Candidates[0].Relation != api.DupeRelationCoexists || got.RequiresAction {
			t.Fatalf("HDR precedence erased distinct %s: %#v", variant, got)
		}
	}
	target := aitherTarget("ENCODE", "2160p", "x265", 100)
	target.HDR = aitherHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10)
	target.Edition = "Extended"
	existing := target
	existing.HDR = aitherHDR(api.HDRFormatHDR10)
	existing.Edition = ""
	got := aitherEvaluate(target, aitherCandidate("existing", existing))
	if got.Candidates[0].Relation == api.DupeRelationProposedTrumps || !got.RequiresAction {
		t.Fatalf("missing cut comparison justified HDR trump: %#v", got)
	}
}

func TestDuplicateEmptySpecialsPackRequiresReview(t *testing.T) {
	t.Parallel()
	for _, codec := range []string{"x264", "x265"} {
		for _, name := range []string{"", "Example.Series.S00.1080p.BluRay." + codec + "-GRP"} {
			target := aitherTarget("ENCODE", "1080p", codec, 100)
			target.Pack = true
			target.Names = []string{name}
			got := aitherEvaluate(target)
			if !got.RequiresAction || len(got.ReviewReasons) == 0 {
				t.Fatalf("unresolved/S00 pack gained ordinary vacancy: %#v", got)
			}
			target.Names = nil
			target.Season = 1
			got = aitherEvaluate(target)
			if got.RequiresAction || len(got.ReviewReasons) != 0 {
				t.Fatalf("ordinary known season unexpectedly requires review: %#v", got)
			}
		}
	}
}

func TestDuplicateDocumentedVariantSurvivesMissingSlotEvidence(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"edition", "region", "3d"} {
		t.Run(variant, func(t *testing.T) {
			target := aitherTarget("ENCODE", "1080p", "x265", 100)
			existing := target
			switch variant {
			case "edition":
				target.Edition, existing.Edition = "Theatrical", "Extended"
			case "region":
				target.Region, existing.Region = "PAL", "NTSC"
			case "3d":
				target.ThreeD, existing.ThreeD = "2D", "3D"
			}
			candidate := aitherCandidate("existing", existing)
			candidate.HDR = api.HDRFacts{}
			got := aitherEvaluate(target, candidate)
			if got.Candidates[0].Relation != api.DupeRelationCoexists || got.RequiresAction {
				t.Fatalf("documented %s variation hidden by unrelated missing HDR: %#v", variant, got)
			}
		})
	}
}

func TestDuplicateX264TitleOnlyPackMembershipRequiresReview(t *testing.T) {
	t.Parallel()
	target := aitherTarget("ENCODE", "1080p", "x264", 100)
	target.Pack, target.Season = true, 1
	entry := api.DupeEntry{
		ID:            "existing",
		Name:          "Example.Series.S01.1080p.BluRay.x264-GRP",
		CanonicalType: "ENCODE",
		Source:        "BluRay",
		Res:           "1080p",
		Codec:         "x264",
		HDR:           aitherHDR(api.HDRFormatSDR),
		SizeBytes:     50,
		SizeKnown:     true,
	}
	got := aitherEvaluate(target, dupe.NormalizeCandidate(entry, "AITHER"))
	if got.Candidates[0].Relation == api.DupeRelationCoexists || !got.RequiresAction {
		t.Fatalf("title-only pack membership authorized capacity: %#v", got)
	}
	entry.Season, entry.Pack = 1, true
	got = aitherEvaluate(target, dupe.NormalizeCandidate(entry, "AITHER"))
	if got.Candidates[0].Relation != api.DupeRelationCoexists || got.RequiresAction {
		t.Fatalf("complete same-season membership did not preserve vacancy: %#v", got)
	}
}

func TestDuplicateX264AdapterWithoutStructuredSourceRetainsVacancy(t *testing.T) {
	t.Parallel()
	target := aitherTarget("ENCODE", "1080p", "x264", 100)
	candidate := dupe.NormalizeCandidate(api.DupeEntry{
		ID:            "existing",
		Name:          "Example.Movie.2026.1080p.BluRay.x264-GRP",
		CanonicalType: "ENCODE",
		Res:           "1080p",
		Codec:         "AVC",
		HDR:           aitherHDR(api.HDRFormatSDR),
		SizeBytes:     50,
		SizeKnown:     true,
	}, "AITHER")
	got := aitherEvaluate(target, candidate)
	if got.Candidates[0].Relation != api.DupeRelationCoexists || got.RequiresAction {
		t.Fatalf("complete encode family required unavailable disc/source distinction: %#v", got)
	}
}

func TestDuplicateTitleWEBEncodeStillNeedsSourceReview(t *testing.T) {
	t.Parallel()
	target := aitherTarget("ENCODE", "1080p", "x264", 100)
	target.Source = ""
	target.Names = []string{"Example.Movie.2026.1080p.WEBRip.x264-GRP"}
	got := aitherEvaluate(target)
	if !got.RequiresAction || !slices.ContainsFunc(got.ReviewReasons, func(reason api.DupeReason) bool {
		return reason.Code == "aither/web_encode_comparisons"
	}) {
		t.Fatalf("partial WEB source bypassed review: %#v", got)
	}
}
