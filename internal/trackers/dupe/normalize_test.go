// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
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
