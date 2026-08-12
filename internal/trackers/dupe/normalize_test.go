// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"slices"
	"strings"
	"testing"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNormalizeDiscEncodeFromStructuredTypeAndTitleSource(t *testing.T) {
	t.Parallel()

	facts := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{
		Name: "Example.Release.2026.1080p.BluRay.x264-GRP",
		Type: "ENCODE",
	}, "AR"))
	if facts.MediaKind != mediaKindDiscEncode || facts.MediaClass != mediaClassEncode || facts.SourceFamily != sourceFamilyDisc {
		t.Fatalf("disc encode facts = %#v", facts)
	}
	if facts.Source.Value != "bluray" || facts.Source.Status != FactPartial || facts.Source.Origin != FactOriginTrackerTitle {
		t.Fatalf("title source fact = %#v", facts.Source)
	}
	if facts.Codec.Value != "h264" || facts.Codec.Status != FactPartial || facts.Codec.Origin != FactOriginTrackerTitle {
		t.Fatalf("title codec fact = %#v", facts.Codec)
	}
	if facts.Group.Value != "GRP" || facts.Group.Status != FactPartial || facts.Group.Origin != FactOriginTrackerTitle {
		t.Fatalf("title group fact = %#v", facts.Group)
	}
}

func TestNormalizeTitleRemuxOutranksStructuredDiscSource(t *testing.T) {
	t.Parallel()

	facts := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{
		Name:   "Example.Release.2026.1080p.BluRay.REMUX.AVC-GRP",
		Source: "BluRay",
	}, "RTF"))
	if facts.MediaKind != mediaKindRemux || facts.MediaClass != mediaClassRemux || facts.SourceFamily != sourceFamilyDisc {
		t.Fatalf("remux facts = %#v", facts)
	}
}

func TestNormalizeFullDiscFromStructuredAndTrackerLabels(t *testing.T) {
	t.Parallel()

	for _, entry := range []api.DupeEntry{
		{Type: "DISC"},
		{Type: "Full Disc"},
		{Type: "BD 50"},
		{Type: "BluRay Raw"},
		{Name: "Example Release 2026 1080p Blu-ray AVC-GRP", Container: "m2ts"},
		{
			Name:      "Example Release 2026 576i DVD-GRP",
			Source:    "DVD",
			Container: "ISO",
		},
	} {
		facts := normalizeCandidateFacts(NormalizeCandidate(entry, "TEST"))
		if facts.MediaKind != mediaKindFullDisc || facts.MediaClass != mediaClassFullDisc || facts.SourceFamily != sourceFamilyDisc {
			t.Fatalf("full-disc facts = %#v for entry %#v", facts, entry)
		}
	}
}

func TestAmbiguousBluRayTitleDoesNotDisproveFullDiscDuplicate(t *testing.T) {
	t.Parallel()

	target := api.TrackerDuplicateTarget{
		Names:  []string{"Example Release 2026 Proposed-GRP"},
		Type:   "DISC",
		Source: "Blu-ray",
	}
	candidate := NormalizeCandidate(api.DupeEntry{Name: "Example Release 2026 1080p Blu-ray AVC TrueHD 7.1-GRP"}, "TEST")
	result := Evaluate(target, []TrackerCandidate{candidate}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true})
	if got := result.Candidates[0].Relation; got != api.DupeRelationSameSlot || !result.RequiresAction {
		t.Fatalf("ambiguous Blu-ray relation = %#v", result.Candidates[0])
	}
}

func TestNormalizeProviderFromAutobrrRLSCollection(t *testing.T) {
	t.Parallel()

	facts := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{
		Name: "Example.Release.2026.1080p.AMZN.WEB-DL.H.264-GRP",
		Type: "WEBDL",
	}, "LST"))
	if facts.Provider.Value != "amzn" || facts.Provider.Status != FactPartial || facts.Provider.Origin != FactOriginTrackerTitle {
		t.Fatalf("provider fact = %#v", facts.Provider)
	}
}

func TestCanonicalTitleEditionRecognizesLSTAspectRatioSlots(t *testing.T) {
	t.Parallel()

	if got := canonicalTitleEdition(nil, nil, "Example.Release.2026.Open.Matte.1080p-GRP"); got != "open_matte" {
		t.Fatalf("Open Matte edition = %q", got)
	}
	if got := canonicalTitleEdition(nil, nil, "Example.Release.2026.OAR.1080p-GRP"); got != "oar" {
		t.Fatalf("OAR edition = %q", got)
	}
}

func TestNormalizeStructuredTitleConflictIsContradictory(t *testing.T) {
	t.Parallel()

	facts := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{
		Name:   "Example.Release.2026.1080p.BluRay.x264-GRP",
		Source: "WEB",
		Codec:  "H.265",
		Group:  "OTHER",
	}, "AR"))
	for dimension, fact := range map[string]Fact{
		"source": facts.Source,
		"codec":  facts.Codec,
		"group":  facts.Group,
	} {
		if fact.Status != FactContradictory || len(fact.Contradictions) == 0 {
			t.Fatalf("%s conflict = %#v", dimension, fact)
		}
	}
}

func TestNormalizeResolutionCoarseSDRetainsConcreteTitleEvidence(t *testing.T) {
	t.Parallel()

	for _, resolution := range []string{"480p", "576p"} {
		facts := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{
			Name: "Example.Release.2026." + resolution + ".WEB-DL.H.264-GRP",
			Res:  "SD",
		}, "BTN"))
		if facts.Resolution.Value != resolution || facts.Resolution.Status != FactPartial ||
			facts.Resolution.Origin != FactOriginTrackerTitle ||
			!slices.Equal(facts.Resolution.SourceFields, []string{"resolution", "title"}) {
			t.Fatalf("%s resolution fact = %#v", resolution, facts.Resolution)
		}
	}
}

func TestCompareResolutionFactsDistinguishesOverlapFromConflict(t *testing.T) {
	t.Parallel()

	complete := func(value string) Fact { return completeFact(value, FactOriginTrackerAPI, "resolution") }
	for _, test := range []struct {
		name  string
		left  string
		right string
		want  DimensionComparison
	}{
		{
			name:  "coarse concrete overlap",
			left:  "SD",
			right: "480p",
			want:  DimensionUnknown,
		},
		{
			name:  "concrete values differ",
			left:  "480p",
			right: "576p",
			want:  DimensionDifferent,
		},
		{
			name:  "sd and hd differ",
			left:  "SD",
			right: "720p",
			want:  DimensionDifferent,
		},
		{
			name:  "same concrete value",
			left:  "576i",
			right: "576i",
			want:  DimensionEqual,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := compareDimensionFacts(trackerspkg.DupeDimensionResolution, complete(test.left), complete(test.right)); got != test.want {
				t.Fatalf("comparison = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSameSlotFindingRetainsOperandRichComparisons(t *testing.T) {
	t.Parallel()

	policy := trackerspkg.DupePolicy{
		ID:         "ar/duplicate/v2",
		EvidenceID: "ar-uploading-guidelines",
		SlotDimensions: []trackerspkg.DupeDimension{
			trackerspkg.DupeDimensionSource,
			trackerspkg.DupeDimensionResolution,
			trackerspkg.DupeDimensionCodec,
			trackerspkg.DupeDimensionGroup,
		},
	}
	target := api.TrackerDuplicateTarget{
		Source:      "BluRay",
		Resolution:  "1080p",
		VideoEncode: "x264",
		Group:       "GRP",
	}
	candidate := NormalizeCandidate(api.DupeEntry{
		Name: "Example.Release.2026.1080p.BluRay.x264-GRP",
	}, "AR")
	result := Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true}).Candidates[0]
	if result.Relation != api.DupeRelationSameSlot || result.WinningRule != "ar/duplicate/v2/slot" {
		t.Fatalf("same-slot evaluation = %#v", result)
	}
	findings := decisiveCandidateFindings(result)
	compared := dupeFindingComparisonValues(findings)
	for _, dimension := range []string{"source", "resolution", "codec", "group"} {
		if !strings.Contains(compared, dimension+"[target={") || !strings.Contains(compared, "result=equal") {
			t.Fatalf("comparison %s missing from %q", dimension, compared)
		}
	}
}

func TestExplicitSDRTitleEvidenceRemainsPartial(t *testing.T) {
	t.Parallel()

	facts := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{
		Name: "Example.Release.2026.1080p.BluRay.x264.SDR-GRP",
	}, "PTP"))
	if facts.HDR.Status != api.HDREvidencePartial || facts.HDR.Origin != api.HDREvidenceTrackerTitle ||
		len(facts.HDR.Formats) != 1 || facts.HDR.Formats[0] != api.HDRFormatSDR {
		t.Fatalf("explicit SDR title evidence = %#v", facts.HDR)
	}
}

func TestNormalizePreservesUnrecognizedStructuredEdition(t *testing.T) {
	t.Parallel()

	facts := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{
		Name:    "Example.Release.2026.1080p.BluRay.x264-GRP",
		Edition: "Archive Presentation",
	}, "PTP"))
	if facts.Edition.Value != "archive presentation" || facts.Edition.Status != FactComplete || facts.Edition.Origin != FactOriginTrackerAPI {
		t.Fatalf("structured edition fact = %#v", facts.Edition)
	}
}
