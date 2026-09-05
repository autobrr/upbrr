// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestULCXHDRPrecedenceExplainsIncompleteProviderEvidence(t *testing.T) {
	t.Parallel()

	for _, direction := range []struct {
		name      string
		targetHDR api.HDRFacts
		otherHDR  api.HDRFacts
		complete  api.DupeRelation
	}{
		{
			name:      "proposed preferred",
			targetHDR: ulcxHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10),
			otherHDR:  ulcxHDR(api.HDRFormatHDR10),
			complete:  api.DupeRelationProposedTrumps,
		},
		{
			name:      "existing preferred",
			targetHDR: ulcxHDR(api.HDRFormatHDR10),
			otherHDR:  ulcxHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10),
			complete:  api.DupeRelationExistingPreferred,
		},
	} {
		for _, evidence := range []struct {
			name     string
			provider string
			title    string
			missing  string
		}{
			{name: "missing", missing: "candidate_provider"},
			{
				name:    "title only",
				title:   "Example.Release.2026.2160p.NF.WEB-DL.H.265-GRP",
				missing: "candidate_provider_partial",
			},
			{name: "structured", provider: "NF"},
		} {
			t.Run(direction.name+" "+evidence.name, func(t *testing.T) {
				t.Parallel()

				target := ulcxTarget("WEBDL", "2160p", "NF", "H.265", direction.targetHDR)
				candidate := ulcxCandidate("WEBDL", "2160p", evidence.provider, "HEVC", direction.otherHDR)
				if evidence.title != "" {
					candidate.Name = evidence.title
				}
				evaluation := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *Profile().DupePolicy, dupe.SearchEvidence{
					Complete:  true,
					Pages:     1,
					WorkScope: dupe.WorkScopeProviderID,
				})
				want := direction.complete
				if evidence.missing != "" {
					want = api.DupeRelationInsufficientEvidence
				}
				result := evaluation.Candidates[0]
				if result.Relation != want || result.Facts.HDR.Status != api.HDREvidenceComplete || len(result.Reasons) != 1 ||
					result.Reasons[0].Code != "ulcx_webdl_hdr_precedence" {
					t.Fatalf("HDR precedence result = %#v", result)
				}
				wantBlocks := want == api.DupeRelationExistingPreferred
				if evaluation.Blocks != wantBlocks || evaluation.RequiresAction == wantBlocks {
					t.Fatalf("blocks=%t action=%t, want blocks=%t action=%t", evaluation.Blocks, evaluation.RequiresAction, wantBlocks, !wantBlocks)
				}
				if evidence.missing != "" {
					if !strings.Contains(result.Reasons[0].Message, strings.ReplaceAll(evidence.missing, "_", " ")) {
						t.Fatalf("missing comparison dimension absent from explanation: %#v", result.Reasons)
					}
				} else if strings.Contains(result.Reasons[0].Message, "incomplete") {
					t.Fatalf("complete provider evidence described as incomplete: %#v", result.Reasons)
				}
			})
		}
	}
}

func TestULCXDuplicateSlots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		target    api.TrackerDuplicateTarget
		candidate dupe.TrackerCandidate
		want      api.DupeRelation
	}{
		{
			name:      "different WEB-DL provider coexists",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER-A", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "PROVIDER-B", "AVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "different WEB-DL provider coexists with missing codec",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER-A", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "PROVIDER-B", "", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "different WEB-DL codec coexists",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "PROVIDER", "HEVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "different WEB-DL codec coexists with missing provider",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "", "HEVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "same WEB-DL slot requires action",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "PROVIDER", "AVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationSameSlot,
		},
		{
			name:      "better WEB-DL HDR tier trumps",
			target:    ulcxTarget("WEBDL", "2160p", "PROVIDER", "H.265", ulcxHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10Plus)),
			candidate: ulcxCandidate("WEBDL", "2160p", "PROVIDER", "HEVC", ulcxHDR(api.HDRFormatHDR10Plus)),
			want:      api.DupeRelationProposedTrumps,
		},
		{
			name:      "DV HDR10 and HDR10 plus WEB-DLs coexist",
			target:    ulcxTarget("WEBDL", "2160p", "PROVIDER", "H.265", ulcxHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10)),
			candidate: ulcxCandidate("WEBDL", "2160p", "PROVIDER", "HEVC", ulcxHDR(api.HDRFormatHDR10Plus)),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "different encode codec coexists",
			target:    ulcxTarget("ENCODE", "1080p", "", "x264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("ENCODE", "1080p", "", "HEVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "1080p SDR and HDR encodes coexist",
			target:    ulcxTarget("ENCODE", "1080p", "", "x264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("ENCODE", "1080p", "", "AVC", ulcxHDR(api.HDRFormatHDR10)),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "same encode slot needs bitrate and cut review",
			target:    ulcxTarget("ENCODE", "1080p", "", "x264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("ENCODE", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationManualReview,
		},
		{
			name:      "same resolution remux needs colour and cut review",
			target:    ulcxTarget("REMUX", "2160p", "", "H.265", ulcxHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10)),
			candidate: ulcxCandidate("REMUX", "2160p", "", "HEVC", ulcxHDR(api.HDRFormatHDR10)),
			want:      api.DupeRelationManualReview,
		},
		{
			name:      "same resolution full disc needs content review",
			target:    ulcxTarget("DISC", "1080p", "", "", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("DISC", "1080p", "", "", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationManualReview,
		},
		{
			name:      "missing WEB-DL provider is insufficient evidence",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationInsufficientEvidence,
		},
		{
			name:      "missing WEB-DL codec is insufficient evidence",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "PROVIDER", "", ulcxHDR(api.HDRFormatSDR)),
			want:      api.DupeRelationInsufficientEvidence,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assertULCXRelation(t, test.target, test.candidate, test.want)
		})
	}
}

func TestULCXRemuxNamingSlots(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		targetCut    string
		candidateCut string
		want         api.DupeRelation
	}{
		{name: "both unmarked", want: api.DupeRelationSameSlot},
		{
			name:         "explicit theatrical matches default",
			candidateCut: "Theatrical Cut",
			want:         api.DupeRelationSameSlot,
		},
		{
			name:      "proposed theatrical matches default",
			targetCut: "Theatrical Cut",
			want:      api.DupeRelationSameSlot,
		},
		{
			name:         "existing extended differs from default",
			candidateCut: "Extended Cut",
			want:         api.DupeRelationCoexists,
		},
		{
			name:      "proposed extended differs from default",
			targetCut: "Extended Cut",
			want:      api.DupeRelationCoexists,
		},
		{
			name:         "same named cut",
			targetCut:    "Extended Cut",
			candidateCut: "Extended Cut",
			want:         api.DupeRelationSameSlot,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			target := ulcxTarget("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
			target.Names = []string{"Example Movie 2026 " + test.targetCut + " 1080p BluRay REMUX AVC DTS-HD MA 5.1-GRP"}
			candidate := ulcxCandidate("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
			candidate.Name = "Example Movie 2026 " + test.candidateCut + " 1080p BluRay REMUX AVC DTS-HD MA 5.1-OTHER"
			assertULCXRelation(t, target, candidate, test.want)
		})
	}
	t.Run("title cut conflicting with structured cut stays manual", func(t *testing.T) {
		t.Parallel()
		target := ulcxTarget("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
		target.Names = []string{"Example Movie 2026 1080p BluRay REMUX AVC DTS-HD MA 5.1-GRP"}
		candidate := ulcxCandidate("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
		candidate.Name = "Example Movie 2026 Extended Cut 1080p BluRay REMUX AVC DTS-HD MA 5.1-OTHER"
		candidate.Edition = "Theatrical"
		assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
	})
}

func TestULCXDeclaredRemuxCuts(t *testing.T) {
	t.Parallel()
	for _, cut := range []string{"Director's Cut", "Extended", "Special Edition", "Unrated", "Uncut", "Super Duper Cut"} {
		for _, variant := range []string{"same cut", "proposed cut", "existing cut", "contradictory cut"} {
			t.Run(cut+"/"+variant, func(t *testing.T) {
				t.Parallel()
				targetCut, candidateCut := cut, cut
				want := api.DupeRelationSameSlot
				switch variant {
				case "proposed cut":
					candidateCut, want = "", api.DupeRelationCoexists
				case "existing cut":
					targetCut, want = "", api.DupeRelationCoexists
				}
				target := ulcxTarget("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
				target.Names = []string{"Example Movie 2026 " + targetCut + " 1080p BluRay REMUX AVC-GRP"}
				target.Edition = targetCut
				candidate := ulcxCandidate("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
				candidate.Name = "Example Movie 2026 " + candidateCut + " 1080p BluRay REMUX AVC-OTHER"
				if variant == "contradictory cut" {
					candidate.Edition, want = "Theatrical", api.DupeRelationManualReview
				}
				assertULCXRelation(t, target, candidate, want)
			})
		}
	}
}

func TestULCXRemuxWorkTitleIsNotACut(t *testing.T) {
	t.Parallel()
	for _, title := range []string{
		"Director's Cut", "Extended", "Extended Cut", "Theatrical Cut", "Final Cut", "International Cut",
		"Alternate Cut", "Open Matte", "OAR", "Uncut", "Unrated", "Special Edition", "Super Duper Cut",
	} {
		for _, direction := range []string{"proposed", "existing"} {
			t.Run(title+" "+direction, func(t *testing.T) {
				t.Parallel()
				target := ulcxTarget("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
				target.Names = []string{"Example Movie 2026 1080p BluRay REMUX AVC-GRP"}
				candidate := ulcxCandidate("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
				candidate.Name = "Example Movie 2026 1080p BluRay REMUX AVC-OTHER"
				if direction == "proposed" {
					target.Names[0] = "Example " + title + " 2026 1080p BluRay REMUX AVC-GRP"
				} else {
					candidate.Name = "Example " + title + " 2026 1080p BluRay REMUX AVC-OTHER"
				}
				assertULCXRelation(t, target, candidate, api.DupeRelationSameSlot)
			})
		}
	}
}

func TestULCXRemuxNamingBoundaryRequired(t *testing.T) {
	t.Parallel()
	target := ulcxTarget("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
	target.Names = []string{"Example Movie 2026 1080p BluRay REMUX AVC-GRP"}
	candidate := ulcxCandidate("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
	candidate.Name = "Example Movie Extended Cut 1080p BluRay REMUX AVC-OTHER"
	assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
}

func TestULCXIncompleteNameCannotProveAnHDRSlot(t *testing.T) {
	t.Parallel()
	target := ulcxTarget("REMUX", "2160p", "", "AVC", ulcxHDR(api.HDRFormatHDR10))
	target.Names = []string{"Example Movie 2026 2160p BluRay REMUX HDR10 AVC-GRP"}
	candidate := ulcxCandidate("REMUX", "2160p", "", "AVC", api.HDRFacts{Status: api.HDREvidenceMissing})
	candidate.Name = "Example Movie 2026 2160p REMUX AVC-OTHER"
	assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
}

func TestULCXRemuxPresentationAndCompoundCutsRequireReview(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		proposed string
		existing string
	}{
		{
			proposed: "Example Movie 2026 1080p BluRay REMUX AVC-GRP",
			existing: "Example Movie 2026 Open Matte 1080p BluRay REMUX AVC-OTHER",
		},
		{
			proposed: "Example Movie 2026 1080p BluRay REMUX AVC-GRP",
			existing: "Example Movie 2026 OAR 1080p BluRay REMUX AVC-OTHER",
		},
		{
			proposed: "Example Movie 2026 Extended Cut 1080p BluRay REMUX AVC-GRP",
			existing: "Example Movie 2026 Extended Cut Open Matte 1080p BluRay REMUX AVC-OTHER",
		},
		{
			proposed: "Example Movie 2026 1080p BluRay REMUX AVC-GRP",
			existing: "Example Movie 2026 IMAX 1080p BluRay REMUX AVC-OTHER",
		},
		{
			proposed: "Example Movie 2026 1080p BluRay REMUX AVC-GRP",
			existing: "Example Movie 2026 MAR 1080p BluRay REMUX AVC-OTHER",
		},
		{
			proposed: "Example Movie 2026 Extended Cut 1080p BluRay REMUX AVC-GRP",
			existing: "Example Movie 2026 Extended Cut IMAX 1080p BluRay REMUX AVC-OTHER",
		},
		{
			proposed: "Example Movie 2026 Extended Cut 1080p BluRay REMUX AVC-GRP",
			existing: "Example Movie 2026 Extended Cut MAR 1080p BluRay REMUX AVC-OTHER",
		},
		{
			proposed: "Alpha 2026 / Beta 2025 1080p BluRay REMUX AVC-GRP",
			existing: "Alpha 2026 Extended / Beta 2025 1080p BluRay REMUX AVC-OTHER",
		},
		{
			proposed: "Alpha 2026 / Beta 2025 1080p BluRay REMUX AVC-GRP",
			existing: "Alpha 2026 / Beta 2025 Extended Cut 1080p BluRay REMUX AVC-OTHER",
		},
		{
			proposed: "Alpha 2026 Extended / Beta 2025 1080p BluRay REMUX AVC-GRP",
			existing: "Alpha 2026 / Beta 2025 Extended Cut 1080p BluRay REMUX AVC-OTHER",
		},
	} {
		for _, reverse := range []bool{false, true} {
			target := ulcxTarget("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
			target.Names = []string{test.proposed}
			candidate := ulcxCandidate("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
			candidate.Name = test.existing
			if reverse {
				target.Names[0], candidate.Name = candidate.Name, target.Names[0]
			}
			assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
		}
	}
}

func TestULCXRemuxNamingPrerequisitesUseMetadata(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		resolution string
		validName  string
	}{
		{
			name:       "Example Remux Movie 2026 1080p AVC-OTHER",
			resolution: "1080p",
			validName:  "Example Movie 2026 1080p BluRay REMUX AVC-GRP",
		},
		{
			name:       "Example 1080p Movie 2026 BluRay REMUX AVC-OTHER",
			resolution: "1080p",
			validName:  "Example Movie 2026 1080p BluRay REMUX AVC-GRP",
		},
		{
			name:       "Example DVD Movie 2026 REMUX AVC-OTHER",
			resolution: "480p",
			validName:  "Example Movie 2026 NTSC DVD REMUX AVC-GRP",
		},
		{
			name:       "Example Movie 2026 1080p AVC-REMUX",
			resolution: "1080p",
			validName:  "Example Movie 2026 1080p BluRay REMUX AVC-GRP",
		},
		{
			name:       "Example Movie 2026 REMUX AVC-DVD",
			resolution: "480p",
			validName:  "Example Movie 2026 NTSC DVD REMUX AVC-GRP",
		},
		{
			name:       "Example Movie 2026 MyDVD REMUX AVC-OTHER",
			resolution: "480p",
			validName:  "Example Movie 2026 NTSC DVD REMUX AVC-GRP",
		},
		{
			name:       "Example Movie 2026 1080p Remuxed AVC-OTHER",
			resolution: "1080p",
			validName:  "Example Movie 2026 1080p BluRay REMUX AVC-GRP",
		},
	} {
		for _, direction := range []string{"proposed", "existing"} {
			t.Run(test.name+" "+direction, func(t *testing.T) {
				t.Parallel()
				target := ulcxTarget("REMUX", test.resolution, "", "AVC", ulcxHDR(api.HDRFormatSDR))
				target.Names = []string{test.validName}
				candidate := ulcxCandidate("REMUX", test.resolution, "", "AVC", ulcxHDR(api.HDRFormatSDR))
				candidate.Name = test.name
				if direction == "proposed" {
					target.Names[0], candidate.Name = candidate.Name, target.Names[0]
				}
				assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
			})
		}
	}
}

func TestULCXRemuxHDRWorkTitleAliases(t *testing.T) {
	t.Parallel()
	for _, marker := range []string{"HDR", "WCG", "HDR Vivid"} {
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			target := ulcxTarget("REMUX", "2160p", "", "HEVC", ulcxHDR(api.HDRFormatSDR))
			target.Names = []string{"Example Movie 2026 2160p BluRay REMUX HEVC-GRP"}
			candidate := ulcxCandidate("REMUX", "2160p", "", "HEVC", ulcxHDR(api.HDRFormatSDR))
			candidate.Name = "Example " + marker + " Movie 2026 2160p BluRay REMUX HEVC-OTHER"
			assertULCXRelation(t, target, candidate, api.DupeRelationSameSlot)
		})
	}
}

func TestULCXSeriesNamingBoundary(t *testing.T) {
	t.Parallel()
	target := ulcxTarget("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
	target.Names = []string{"Example Show S01E01 1080p BluRay REMUX AVC-GRP"}
	candidate := ulcxCandidate("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
	candidate.Name = "Example OAR S01E01 1080p BluRay REMUX AVC-OTHER"
	assertULCXRelation(t, target, candidate, api.DupeRelationSameSlot)
	candidate.Name = "Example Show S01E01 Extended Cut 1080p BluRay REMUX AVC-OTHER"
	assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
}

func TestULCXEpisodeTitlesRetainDuplicateAction(t *testing.T) {
	t.Parallel()
	for _, episodeTitle := range []string{"Final Cut", "HDR", "Example Episode"} {
		t.Run(episodeTitle, func(t *testing.T) {
			t.Parallel()
			meta := ulcxTVNameSubject(api.TVDBNameDisambiguation{CanonicalName: "Example Series", SeriesYear: 2026})
			meta.EpisodeTitle = episodeTitle
			meta.ReleaseName = strings.Replace(meta.ReleaseName, "Example Episode", episodeTitle, 1)
			meta.ReleaseName = strings.Replace(meta.ReleaseName, "WEB-DL H.265", "BluRay REMUX AVC", 1)
			generated := buildName(meta, config.TrackerConfig{})
			if !strings.Contains(generated, "S01E02 "+episodeTitle+" 1080p") {
				t.Fatalf("generated name omitted episode title: %q", generated)
			}
			conforming := "Example Series AKA Example Original S01E02 1080p BluRay REMUX AVC-OTHER"
			target := ulcxTarget("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
			target.Names = []string{generated}
			candidate := ulcxCandidate("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
			candidate.Name = conforming
			assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
			target.Names, candidate.Name = []string{conforming}, generated
			assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
		})
	}
}

func TestULCXSeriesDescriptionsRetainDuplicateAction(t *testing.T) {
	t.Parallel()
	for _, descriptor := range []string{"S00 Final Cut Interviews", "S01 Final Cut Extras", "S01 OVA Final Cut"} {
		t.Run(descriptor, func(t *testing.T) {
			t.Parallel()
			name := "Example Show " + descriptor + " 1080p BluRay REMUX AVC-GRP"
			plain := "Example Show " + strings.Fields(descriptor)[0] + " 1080p BluRay REMUX AVC-OTHER"
			target := ulcxTarget("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
			target.Names = []string{name}
			candidate := ulcxCandidate("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
			candidate.Name = plain
			assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
			target.Names, candidate.Name = []string{plain}, name
			assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
		})
	}
}

func TestULCXDVDRemuxNamingSlots(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"neither", "proposed", "existing"} {
		t.Run(missing+" resolution missing", func(t *testing.T) {
			t.Parallel()
			target := ulcxTarget("REMUX", "480p", "", "MPEG-2", ulcxHDR(api.HDRFormatSDR))
			target.Names = []string{"Example Movie 2026 NTSC DVD REMUX DD2.0-GRP"}
			candidate := ulcxCandidate("REMUX", "480p", "", "MPEG-2", ulcxHDR(api.HDRFormatSDR))
			candidate.Name = "Example Movie 2026 NTSC DVD REMUX DD2.0-OTHER"
			want := api.DupeRelationSameSlot
			switch missing {
			case "proposed":
				target.Resolution, want = "", api.DupeRelationInsufficientEvidence
			case "existing":
				candidate.Resolution, want = "", api.DupeRelationInsufficientEvidence
			}
			assertULCXRelation(t, target, candidate, want)
		})
	}
}

func TestULCXObjectiveDuplicateSlots(t *testing.T) {
	t.Parallel()

	for _, mediaType := range []string{"ENCODE", "REMUX", "DISC"} {
		t.Run(mediaType, func(t *testing.T) {
			t.Parallel()

			target := ulcxTarget(mediaType, "2160p", "", "H.265", ulcxHDR(api.HDRFormatSDR))
			candidate := ulcxCandidate(mediaType, "2160p", "", "HEVC", ulcxHDR(api.HDRFormatHDR10))
			assertULCXRelation(t, target, candidate, api.DupeRelationCoexists)

			target.HDR, candidate.HDR = candidate.HDR, target.HDR
			assertULCXRelation(t, target, candidate, api.DupeRelationCoexists)

			target.HDR = candidate.HDR
			target.Edition, candidate.Edition = "Theatrical", "Extended"
			// A known cut difference must survive an unrelated missing codec.
			candidate.Codec = ""
			assertULCXRelation(t, target, candidate, api.DupeRelationCoexists)
		})
	}

	t.Run("different disc region", func(t *testing.T) {
		t.Parallel()
		target := ulcxTarget("DISC", "1080p", "", "", ulcxHDR(api.HDRFormatSDR))
		candidate := ulcxCandidate("DISC", "1080p", "", "", ulcxHDR(api.HDRFormatSDR))
		target.Region, candidate.Region = "USA", "GBR"
		assertULCXRelation(t, target, candidate, api.DupeRelationCoexists)
	})
	t.Run("different remux presentation", func(t *testing.T) {
		t.Parallel()
		target := ulcxTarget("REMUX", "1080p", "", "H.264", ulcxHDR(api.HDRFormatSDR))
		candidate := ulcxCandidate("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
		target.ThreeD, candidate.ThreeD = "2D", "3D"
		assertULCXRelation(t, target, candidate, api.DupeRelationCoexists)
	})
	t.Run("known WEB-DL cut survives missing provider", func(t *testing.T) {
		t.Parallel()
		target := ulcxTarget("WEBDL", "1080p", "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR))
		candidate := ulcxCandidate("WEBDL", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
		target.Edition, candidate.Edition = "Theatrical", "Extended"
		assertULCXRelation(t, target, candidate, api.DupeRelationCoexists)
	})
	t.Run("missing cut is not a distinct cut", func(t *testing.T) {
		t.Parallel()
		target := ulcxTarget("REMUX", "2160p", "", "H.265", ulcxHDR(api.HDRFormatSDR))
		candidate := ulcxCandidate("REMUX", "2160p", "", "HEVC", ulcxHDR(api.HDRFormatSDR))
		target.Edition = "Theatrical"
		assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
	})
	t.Run("explicit candidate title cut coexists", func(t *testing.T) {
		t.Parallel()
		target := ulcxTarget("REMUX", "1080p", "", "H.264", ulcxHDR(api.HDRFormatSDR))
		target.Edition = "Theatrical"
		candidate := ulcxCandidate("REMUX", "1080p", "", "AVC", ulcxHDR(api.HDRFormatSDR))
		candidate.Name = "Example.Release.2026.Extended.EXISTING"
		assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)

		candidate.Name = "Example.Release.2026.Extended.1080p.BluRay.REMUX.AVC-OTHER"
		assertULCXRelation(t, target, candidate, api.DupeRelationCoexists)

		candidate.Edition = "Theatrical"
		assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
	})
	for _, status := range []api.HDREvidenceStatus{api.HDREvidenceMissing, api.HDREvidencePartial, api.HDREvidenceContradictory} {
		t.Run("incomplete HDR "+string(status), func(t *testing.T) {
			t.Parallel()
			target := ulcxTarget("REMUX", "2160p", "", "H.265", ulcxHDR(api.HDRFormatSDR))
			hdr := ulcxHDR(api.HDRFormatHDR10)
			hdr.Status = status
			if status == api.HDREvidenceMissing {
				hdr.Formats = nil
			}
			candidate := ulcxCandidate("REMUX", "2160p", "", "HEVC", hdr)
			assertULCXRelation(t, target, candidate, api.DupeRelationManualReview)
		})
	}
}

func TestULCXResolutionEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                string
		targetResolution    string
		candidateResolution string
		candidateName       string
		want                api.DupeRelation
	}{
		{
			name:             "explicit title resolution differs",
			targetResolution: "2160p",
			candidateName:    "Example.Release.2026.1080p.EXISTING",
			want:             api.DupeRelationCoexists,
		},
		{
			name:             "same title resolution still needs complete evidence",
			targetResolution: "2160p",
			candidateName:    "Example.Release.2026.2160p.EXISTING",
			want:             api.DupeRelationInsufficientEvidence,
		},
		{
			name:             "missing resolution remains unknown",
			targetResolution: "2160p",
			want:             api.DupeRelationInsufficientEvidence,
		},
		{
			name:                "coarse SD resolution can overlap",
			targetResolution:    "480p",
			candidateResolution: "SD",
			want:                api.DupeRelationInsufficientEvidence,
		},
		{
			name:                "conflicting title and structured resolution remain unresolved",
			targetResolution:    "2160p",
			candidateResolution: "2160p",
			candidateName:       "Example.Release.2026.1080p.EXISTING",
			want:                api.DupeRelationInsufficientEvidence,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			target := ulcxTarget("WEBDL", test.targetResolution, "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR))
			candidate := ulcxCandidate("WEBDL", test.candidateResolution, "PROVIDER", "AVC", ulcxHDR(api.HDRFormatSDR))
			if test.candidateName != "" {
				candidate.Name = test.candidateName
			}
			assertULCXRelation(t, target, candidate, test.want)
		})
	}
}

func TestULCXSeasonPackPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		target       api.TrackerDuplicateTarget
		candidate    dupe.TrackerCandidate
		proposedPack bool
	}{
		{
			name:      "existing different-provider WEB-DL pack",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER-A", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "1080p", "PROVIDER-B", "AVC", ulcxHDR(api.HDRFormatSDR)),
		},
		{
			name:         "proposed same-provider WEB-DL pack",
			target:       ulcxTarget("WEBDL", "1080p", "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate:    ulcxCandidate("WEBDL", "1080p", "PROVIDER", "AVC", ulcxHDR(api.HDRFormatSDR)),
			proposedPack: true,
		},
		{
			name:      "existing different-codec encode pack",
			target:    ulcxTarget("ENCODE", "1080p", "", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("ENCODE", "1080p", "", "HEVC", ulcxHDR(api.HDRFormatSDR)),
		},
		{
			name:      "existing remux pack outranks subjective review",
			target:    ulcxTarget("REMUX", "2160p", "", "H.265", ulcxHDR(api.HDRFormatHDR10)),
			candidate: ulcxCandidate("REMUX", "2160p", "", "HEVC", ulcxHDR(api.HDRFormatHDR10)),
		},
		{
			name:         "proposed WEB-DL pack outranks better episode HDR",
			target:       ulcxTarget("WEBDL", "2160p", "PROVIDER", "H.265", ulcxHDR(api.HDRFormatHDR10)),
			candidate:    ulcxCandidate("WEBDL", "2160p", "PROVIDER", "HEVC", ulcxHDR(api.HDRFormatDolbyVision, api.HDRFormatHDR10)),
			proposedPack: true,
		},
		{
			name:      "existing pack outranks objective HDR slots",
			target:    ulcxTarget("REMUX", "2160p", "", "H.265", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("REMUX", "2160p", "", "HEVC", ulcxHDR(api.HDRFormatHDR10)),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.target.Season, test.candidate.Season = 1, 1
			want := api.DupeRelationExistingPreferred
			if test.proposedPack {
				test.target.Pack, test.candidate.Episode = true, 2
				want = api.DupeRelationCoexists
			} else {
				test.target.Episode, test.candidate.Pack = 2, true
			}
			assertULCXRelation(t, test.target, test.candidate, want)

			// A pack from another season cannot contain the episode.
			test.candidate.Season = 2
			assertULCXRelation(t, test.target, test.candidate, api.DupeRelationCoexists)
		})
	}
}

func TestULCXSeasonPackWithMissingResolutionRemainsActionable(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		target    api.TrackerDuplicateTarget
		candidate dupe.TrackerCandidate
	}{
		{
			name:      "different provider",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER-A", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "", "PROVIDER-B", "AVC", ulcxHDR(api.HDRFormatSDR)),
		},
		{
			name:      "different WEB-DL codec",
			target:    ulcxTarget("WEBDL", "1080p", "PROVIDER", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("WEBDL", "", "PROVIDER", "HEVC", ulcxHDR(api.HDRFormatSDR)),
		},
		{
			name:      "different encode codec",
			target:    ulcxTarget("ENCODE", "1080p", "", "H.264", ulcxHDR(api.HDRFormatSDR)),
			candidate: ulcxCandidate("ENCODE", "", "", "HEVC", ulcxHDR(api.HDRFormatSDR)),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.target.Season, test.candidate.Season = 1, 1
			test.target.Episode, test.candidate.Pack = 2, true
			assertULCXRelation(t, test.target, test.candidate, api.DupeRelationInsufficientEvidence)
		})
	}
}

func assertULCXRelation(t *testing.T, target api.TrackerDuplicateTarget, candidate dupe.TrackerCandidate, want api.DupeRelation) {
	t.Helper()
	evaluation := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *Profile().DupePolicy, dupe.SearchEvidence{
		Complete:  true,
		Pages:     1,
		WorkScope: dupe.WorkScopeProviderID,
	})
	result := evaluation.Candidates[0]
	if result.Relation != want {
		t.Fatalf("relation = %q, want %q; reasons=%#v", result.Relation, want, result.Reasons)
	}
	wantBlocks := want == api.DupeRelationExistingPreferred || want == api.DupeRelationExactDuplicate
	wantAction := want != api.DupeRelationCoexists && !wantBlocks
	if evaluation.Blocks != wantBlocks || evaluation.RequiresAction != wantAction {
		t.Fatalf("blocks=%t action=%t, want blocks=%t action=%t", evaluation.Blocks, evaluation.RequiresAction, wantBlocks, wantAction)
	}
}

func TestULCXDuplicatePolicyMetadata(t *testing.T) {
	t.Parallel()

	policy := Profile().DupePolicy
	if policy == nil || policy.ID != "ulcx/duplicate/v4" || policy.EvidenceID != ulcxDupeEvidenceID || policy.DefaultTitleEdition != "Theatrical" ||
		policy.SizeVariancePercent != 0 {
		t.Fatalf("ULCX duplicate policy = %#v", policy)
	}
}

func ulcxTarget(
	typeValue string,
	resolution string,
	provider string,
	codec string,
	hdr api.HDRFacts,
) api.TrackerDuplicateTarget {
	return api.TrackerDuplicateTarget{
		Names:       []string{"Example.Release.2026.PROPOSED"},
		Type:        typeValue,
		Provider:    provider,
		Resolution:  resolution,
		VideoEncode: codec,
		HDR:         hdr,
	}
}

func ulcxCandidate(
	typeValue string,
	resolution string,
	provider string,
	codec string,
	hdr api.HDRFacts,
) dupe.TrackerCandidate {
	return dupe.NormalizeCandidate(api.DupeEntry{
		Name:          "Example.Release.2026.EXISTING",
		Type:          typeValue,
		CanonicalType: typeValue,
		Res:           resolution,
		Provider:      provider,
		Codec:         codec,
		HDR:           hdr,
	}, "ULCX")
}

func ulcxHDR(formats ...api.HDRFormat) api.HDRFacts {
	return api.HDRFacts{
		Formats: formats,
		Origin:  api.HDREvidenceMediaInfo,
		Status:  api.HDREvidenceComplete,
	}
}
