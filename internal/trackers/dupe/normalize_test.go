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

func TestCandidateEpisodeRangesRetainMembership(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		coordinates   string
		date          string
		season        int
		episode       int
		targetSeason  int
		targetEpisode int
		want          api.DupeRelation
	}{
		{
			name:          "disjoint special range",
			coordinates:   "S00E01-E02",
			episode:       1,
			targetEpisode: 3,
			want:          api.DupeRelationCoexists,
		},
		{
			name:          "overlapping special range",
			coordinates:   "S00E01-E02",
			episode:       1,
			targetEpisode: 2,
			want:          api.DupeRelationSameSlot,
		},
		{
			name:          "dated disjoint special range",
			coordinates:   "S00E01-E02",
			date:          "2026-01-01.",
			episode:       1,
			targetEpisode: 3,
			want:          api.DupeRelationCoexists,
		},
		{
			name:          "dated overlapping special range",
			coordinates:   "S00E01-E02",
			date:          "2026-01-01.",
			episode:       1,
			targetEpisode: 2,
			want:          api.DupeRelationSameSlot,
		},
		{
			name:          "conflicting season",
			coordinates:   "S00E01-E02",
			season:        1,
			episode:       1,
			targetEpisode: 3,
			want:          api.DupeRelationManualReview,
		},
		{
			name:          "conflicting first episode",
			coordinates:   "S00E01-E02",
			episode:       3,
			targetEpisode: 3,
			want:          api.DupeRelationManualReview,
		},
		{
			name:          "reversed range",
			coordinates:   "S00E02-E01",
			episode:       2,
			targetEpisode: 3,
			want:          api.DupeRelationManualReview,
		},
		{
			name:          "positive season overlapping range",
			coordinates:   "S01E01-E02",
			season:        1,
			episode:       1,
			targetSeason:  1,
			targetEpisode: 2,
			want:          api.DupeRelationSameSlot,
		},
		{
			name:          "positive season disjoint range",
			coordinates:   "S01E01-E02",
			season:        1,
			episode:       1,
			targetSeason:  1,
			targetEpisode: 3,
			want:          api.DupeRelationCoexists,
		},
		{
			name:          "repeated same season overlapping range",
			coordinates:   "S00E01-S00E02",
			targetEpisode: 2,
			want:          api.DupeRelationSameSlot,
		},
		{
			name:          "repeated same season disjoint range",
			coordinates:   "S00E01-S00E02",
			targetEpisode: 3,
			want:          api.DupeRelationCoexists,
		},
		{
			name:          "omitted episode prefix overlapping range",
			coordinates:   "S00E01-02",
			targetEpisode: 2,
			want:          api.DupeRelationSameSlot,
		},
		{
			name:          "versioned special is disjoint",
			coordinates:   "S00E01v2",
			date:          "2026-01-01.",
			targetEpisode: 2,
			want:          api.DupeRelationCoexists,
		},
		{
			name:          "versioned range is disjoint",
			coordinates:   "S00E01-E02v2",
			date:          "2026-01-01.",
			targetEpisode: 3,
			want:          api.DupeRelationCoexists,
		},
	} {
		for _, separator := range []string{".", "_"} {
			t.Run(test.name+separator, func(t *testing.T) {
				t.Parallel()
				candidate := NormalizeCandidate(api.DupeEntry{
					Name:          "Example.Series." + test.coordinates + separator + test.date + "1080p.WEB-DL-GRP",
					Season:        test.season,
					Episode:       test.episode,
					CanonicalType: "WEB-DL",
					Source:        "WEB",
					Res:           "1080p",
				}, "AITHER")
				result := Evaluate(api.TrackerDuplicateTarget{
					Season:     test.targetSeason,
					Episode:    test.targetEpisode,
					Type:       "WEB-DL",
					Source:     "WEB",
					Resolution: "1080p",
				}, []TrackerCandidate{candidate}, trackerspkg.DupePolicy{}, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
				got := result.Candidates[0]
				if got.Relation != test.want {
					t.Fatalf("range relation=%s reasons=%v, want %s", got.Relation, got.Reasons, test.want)
				}
				if test.want == api.DupeRelationCoexists && (result.RequiresAction || result.Blocks) {
					t.Fatalf("disjoint range remains actionable: action=%t blocks=%t", result.RequiresAction, result.Blocks)
				}
			})
		}
	}
}

func TestUnsupportedCandidateEpisodeExpressionsRemainActionable(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		coordinates   string
		targetSeason  int
		targetEpisode int
	}{
		{
			coordinates:   "S00E01-S01E02",
			targetSeason:  1,
			targetEpisode: 2,
		},
		{
			coordinates:   "S01E01-S02E02",
			targetSeason:  2,
			targetEpisode: 2,
		},
		{coordinates: "S00E01+E02", targetEpisode: 2},
		{coordinates: "S00E01&E02", targetEpisode: 2},
		{coordinates: "S00E01-E02-E03", targetEpisode: 3},
		{coordinates: "S00E01-E02E03", targetEpisode: 3},
		{coordinates: "S00E01E02", targetEpisode: 2},
		{coordinates: "S00E01.S00E02", targetEpisode: 2},
		{coordinates: "S00E01,S00E02", targetEpisode: 2},
		{coordinates: "S00E01,E02", targetEpisode: 2},
		{coordinates: "S00E01;E02", targetEpisode: 2},
		{coordinates: "S00E01/E02", targetEpisode: 2},
		{coordinates: "S00E01-E02,E03", targetEpisode: 3},
		{coordinates: "S00E01-E02v2", targetEpisode: 2},
		{coordinates: "S00E01-02v2", targetEpisode: 2},
		{coordinates: "S00E01v2-02", targetEpisode: 2},
		{coordinates: "S00E01v2E02v3", targetEpisode: 2},
		{coordinates: "S00E01-10000", targetEpisode: 2},
		{coordinates: "S00E01+10001v2", targetEpisode: 2},
		{coordinates: "S00E01&00000", targetEpisode: 2},
		{coordinates: "S00E10000-10001", targetEpisode: 2},
		{coordinates: "S00E01-S10000E02", targetEpisode: 2},
		{coordinates: "S00E01-S999999999999999999999999E01", targetEpisode: 2},
	} {
		for _, separator := range []string{".", "_"} {
			t.Run(test.coordinates+separator, func(t *testing.T) {
				t.Parallel()
				for _, date := range []string{"", "2026-01-01."} {
					// Unit3D approved and pending entries supply titles without episode coordinates.
					candidate := NormalizeCandidate(api.DupeEntry{
						Name:          "Example.Series." + test.coordinates + separator + date + "1080p.WEB-DL-GRP",
						CanonicalType: "WEB-DL",
						Source:        "WEB",
						Res:           "1080p",
					}, "AITHER")
					for _, exactOnly := range []bool{false, true} {
						result := Evaluate(api.TrackerDuplicateTarget{
							Season:     test.targetSeason,
							Episode:    test.targetEpisode,
							Type:       "WEB-DL",
							Source:     "WEB",
							Resolution: "1080p",
						}, []TrackerCandidate{candidate}, trackerspkg.DupePolicy{ExactMatchOnly: exactOnly},
							SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
						if result.Candidates[0].Relation == api.DupeRelationCoexists || !result.RequiresAction {
							t.Errorf("unsupported membership became safe: date=%q exactOnly=%t result=%+v", date, exactOnly, result)
						}
					}
				}
			})
		}
	}
}

func TestDatedTitleContentScope(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		coordinates string
		kind        contentScopeKind
		date        string
	}{
		{coordinates: "S00E02", kind: contentScopeEpisode},
		{coordinates: "S00E01-E02", kind: contentScopeEpisodeRange},
		{coordinates: "S00E01-1080p", kind: contentScopeEpisode},
		{coordinates: "S00E01-2160p", kind: contentScopeEpisode},
		{coordinates: "S00E01-10bit", kind: contentScopeEpisode},
		{
			coordinates: "S00E01-2026-01-01",
			kind:        contentScopeDaily,
			date:        "2026-01-01",
		},
		{
			coordinates: "S01E02",
			kind:        contentScopeDaily,
			date:        "2026-01-01",
		},
		{
			coordinates: "S01E02v2",
			kind:        contentScopeDaily,
			date:        "2026-01-01",
		},
		{
			coordinates: "S00E00",
			kind:        contentScopeDaily,
			date:        "2026-01-01",
		},
		{
			coordinates: "S00",
			kind:        contentScopeDaily,
			date:        "2026-01-01",
		},
		{kind: contentScopeDaily, date: "2026-01-01"},
	} {
		t.Run(test.coordinates, func(t *testing.T) {
			t.Parallel()
			name := "Example.Series." + test.coordinates + ".2026-01-01.1080p.WEB-DL-GRP"
			candidate := NormalizeCandidate(api.DupeEntry{Name: name}, "AITHER")
			parsed := parseReleaseTitle(name, FactOriginTrackerTitle)
			if candidate.Date != test.date || parsed.Content.Kind != test.kind || parsed.Content.Date != test.date {
				t.Fatalf("dated title scope: candidate=%+v parsed=%+v", candidate, parsed.Content)
			}
		})
	}
}

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

func TestNormalizeHigh10AVCCodecLabels(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"Hi10P x264", "Hi10P H.264", "Hi10P AVC"} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			title := "Example.Release.2026.720p.BluRay.x264-GRP"
			target := normalizeTargetFacts(api.TrackerDuplicateTarget{
				Names:       []string{title},
				VideoEncode: label,
				VideoCodec:  "AVC",
			})
			candidate := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{
				Name:  title,
				Codec: label,
			}, "TEST"))
			for _, fact := range []Fact{target.Codec, candidate.Codec} {
				if fact.Value != "h264" || fact.Status != FactComplete || len(fact.Contradictions) != 0 {
					t.Fatalf("codec label %q produced %#v", label, fact)
				}
			}
		})
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

func TestCanonicalProviderAliases(t *testing.T) {
	t.Parallel()

	// Real codes are required to verify the production alias mappings.
	for _, test := range []struct {
		value string
		want  string
	}{
		//nolint:misspell // ADN is the Animation Digital Network provider code.
		{value: "AND", want: "adn"},
		//nolint:misspell // ADN is the Animation Digital Network provider code.
		{value: "ADN", want: "adn"},
		{value: "Hotstar", want: "htsr"},
		{value: "HSTR", want: "htsr"},
		{value: "HTSR", want: "htsr"},
		{value: "PROVIDER", want: "provider"},
	} {
		if got := canonicalProvider(test.value); got != test.want {
			t.Errorf("canonicalProvider(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestNormalizeEquivalentStructuredAndTitleProviderAliases(t *testing.T) {
	t.Parallel()

	// This production alias must agree across title parsing and API evidence.
	facts := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{
		Name:     "Example.Release.2026.1080p.HTSR.WEB-DL.H.264-GRP",
		Type:     "WEBDL",
		Provider: "Hotstar",
	}, "LST"))
	if facts.Provider.Value != "htsr" || facts.Provider.Status != FactComplete ||
		facts.Provider.Origin != FactOriginTrackerAPI || len(facts.Provider.Contradictions) != 0 {
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

func TestNormalizeTitleEditionPreservesParsedMetadata(t *testing.T) {
	t.Parallel()
	for marker, want := range map[string]string{
		"Director's Cut":     "directors_cut",
		"Director s Cut":     "directors_cut",
		"Extended Cut":       "extended",
		"Extended Edition":   "extended",
		"Theatrical Cut":     "theatrical",
		"Theatrical Edition": "theatrical",
		"Final Cut":          "final_cut",
		"International Cut":  "international_cut",
		"Alternate Cut":      "alternate_cut",
		"Open Matte":         "open_matte",
		"OAR":                "oar",
		"IMAX":               "imax",
		"MAR":                "mar",
		"Uncut":              "uncut",
		"Unrated":            "unrated",
		"Special Edition":    "special_edition",
		"Super Duper Cut":    "super_duper_cut",
	} {
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			facts := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{
				Name: "Example Movie 2026 " + marker + " 1080p BluRay REMUX AVC-GRP",
			}, "LST"))
			if facts.Edition.Value != want || facts.Edition.Status != FactPartial {
				t.Fatalf("edition = %#v, want partial %q", facts.Edition, want)
			}
		})
	}
	t.Run("OAR work title is not presentation metadata", func(t *testing.T) {
		t.Parallel()
		facts := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{
			Name: "OAR 2026 1080p BluRay REMUX AVC-GRP",
		}, "LST"))
		if facts.Edition.Status != FactMissing {
			t.Fatalf("work title became edition evidence: %#v", facts.Edition)
		}
	})
}

func TestNormalizeCompoundTitleRetainsPartialCuts(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"Alpha 2026 Extended / Beta 2025 1080p BluRay REMUX AVC-GRP",
		"Alpha 2026 / Beta 2025 Extended Cut 1080p BluRay REMUX AVC-GRP",
	} {
		facts := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{Name: name}, "LST"))
		if facts.Edition.Status != FactPartial || facts.Edition.Value != "extended" {
			t.Fatalf("edition = %#v, want retained partial extended cut", facts.Edition)
		}
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
