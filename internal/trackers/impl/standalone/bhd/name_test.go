// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveUploadNameAppliesBHDMovieNamingMatrix(t *testing.T) {
	t.Parallel()

	const sourceName = "Parsed Release AKA Parsed Original 2025 Director's Cut 2160p UHD BluRay REMUX HEVC TrueHD 7.1 Atmos-GRP"
	meta := generatedBHDNameSubject(sourceName)
	meta.Identity.Category = api.CanonicalCategoryMovie
	meta.Release = api.ReleaseInfo{
		Title: "Parsed Release",
		Alt:   "Parsed Original",
		Year:  2025,
		Group: "GRP",
	}
	meta.ProviderMetadata.TMDB = &api.TMDBMetadata{
		Title:         "Example Release",
		OriginalTitle: "Example Original",
		Year:          2026,
	}
	meta.ProviderMetadata.IMDB = &api.IMDBMetadata{Year: 2024}
	meta.Type = "REMUX"
	meta.Source = "BluRay"
	meta.VideoCodec = "HEVC"
	meta.Audio = "TrueHD 7.1 Atmos"
	meta.HDRFacts = api.HDRFacts{
		Formats: []api.HDRFormat{api.HDRFormatSDR},
		Status:  api.HDREvidenceComplete,
	}

	const want = "Example Release AKA Example Original 2024 Director's Cut 2160p UHD BluRay REMUX SDR HEVC TrueHD Atmos 7.1-GRP"
	if got := resolveUploadName(meta); got != want {
		t.Fatalf("BHD movie name = %q, want %q", got, want)
	}
}

func TestResolveUploadNameAppliesBHDTVDBCollisionYearMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		includeYear bool
		want        string
	}{
		{
			name: "unique omits series year",
			want: "Example Series AKA Example Original S01E02 Example Episode 1080p NF WEB-DL DDP Atmos 5.1 H.265-GRP",
		},
		{
			name:        "collision includes series year after AKA",
			includeYear: true,
			want:        "Example Series AKA Example Original 2026 S01E02 Example Episode 1080p NF WEB-DL DDP Atmos 5.1 H.265-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			const sourceName = "Example Series 2026 AKA Example Original S01E02 Example Episode 1080p NF WEB-DL DD+ 5.1 Atmos H.265-GRP"
			meta := generatedBHDNameSubject(sourceName)
			meta.Identity.Category = api.CanonicalCategoryTV
			meta.Release = api.ReleaseInfo{
				Category:   "TV",
				Title:      "Example Series",
				Alt:        "Example Original",
				Year:       2026,
				Resolution: "1080p",
				Group:      "GRP",
			}
			meta.ProviderMetadata.TVDB = &api.TVDBMetadata{
				Name:        "Example Original",
				NameEnglish: "Example Series",
				NameDisambiguation: api.TVDBNameDisambiguation{
					CanonicalName: "Example Series",
					SeriesYear:    2026,
					IncludeYear:   test.includeYear,
					Status:        api.MetadataEvidenceStatusPartial,
				},
			}
			meta.SeasonStr = "S01"
			meta.EpisodeStr = "E02"
			meta.Type = "WEBDL"
			meta.Source = "WEB"
			meta.Audio = "DD+ 5.1 Atmos"

			if got := resolveUploadName(meta); got != test.want {
				t.Fatalf("BHD TV name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveUploadNameAppliesBHDNoGroupDiscAndDVDMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "tagless non-disc uses uppercase no group",
			meta: func() api.UploadSubject {
				meta := generatedBHDNameSubject("Example Release 2026 1080p WEB-DL DDP 5.1-NOGRP")
				meta.Identity.Category = api.CanonicalCategoryMovie
				meta.Release = api.ReleaseInfo{
					Title:      "Example Release",
					Year:       2026,
					Resolution: "1080p",
				}
				meta.Type = "WEBDL"
				meta.Source = "WEB"
				meta.Audio = "DDP 5.1"
				return meta
			}(),
			want: "Example Release 2026 1080p WEB-DL DDP 5.1-NOGROUP",
		},
		{
			name: "tagless full DVD stays blank and orders video before audio",
			meta: func() api.UploadSubject {
				meta := generatedBHDNameSubject("Example Release 2026 PAL DVD DD 2.0-NOGRP")
				meta.Identity.Category = api.CanonicalCategoryMovie
				meta.Release = api.ReleaseInfo{Title: "Example Release", Year: 2026}
				meta.Type = "DISC"
				meta.DiscType = "DVD"
				meta.Source = "PAL DVD"
				meta.VideoCodec = "MPEG-2"
				meta.Audio = "DD 2.0"
				return meta
			}(),
			want: "Example Release 2026 PAL DVD MPEG-2 DD2.0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveUploadName(test.meta); got != test.want {
				t.Fatalf("BHD name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveUploadNameUsesBHDPolicyAndPreservesExactP2PNames(t *testing.T) {
	t.Parallel()

	t.Run("scene release uses normal name", func(t *testing.T) {
		t.Parallel()
		meta := generatedBHDNameSubject("Generated Replacement 2026 1080p WEB-DL DDP 5.1-NOGRP")
		meta.Scene = true
		meta.SceneName = "Exact.Scene.Name.2026.1080p.WEB-DL.DD+5.1-GRP"
		meta.Audio = "DDP 5.1"
		const want = "Generated Replacement 2026 1080p WEB-DL DDP 5.1-NOGROUP"
		if got := resolveUploadName(meta); got != want {
			t.Fatalf("BHD release name = %q, want %q", got, want)
		}
	})

	t.Run("non-generated source name", func(t *testing.T) {
		t.Parallel()
		meta := api.UploadSubject{
			ReleaseName: "Exact.P2P.Source.Name.2026.1080p.WEB-DL.DD+5.1-GRP",
			Type:        "WEBDL",
			Audio:       "DD+ 5.1",
		}
		if got := resolveUploadName(meta); got != meta.ReleaseName {
			t.Fatalf("BHD source name = %q, want %q", got, meta.ReleaseName)
		}
	})

	t.Run("hybrid edition is not provenance", func(t *testing.T) {
		t.Parallel()
		const sourceName = "Example Release 2026 2160p UHD BluRay REMUX HEVC TrueHD 7.1-GRP"
		meta := generatedBHDNameSubject(sourceName)
		meta.Identity.Category = api.CanonicalCategoryMovie
		meta.Release = api.ReleaseInfo{
			Title: "Example Release",
			Year:  2026,
			Group: "GRP",
		}
		meta.Type = "REMUX"
		meta.Source = "BluRay"
		meta.VideoCodec = "HEVC"
		meta.Audio = "TrueHD 7.1"
		meta.Edition = "Hybrid"
		if got := resolveUploadName(meta); got != sourceName {
			t.Fatalf("BHD remux name = %q, want unchanged %q", got, sourceName)
		}
	})
}

func TestBHDNamingPolicyVersion(t *testing.T) {
	t.Parallel()

	if got := New().ReleaseNamePolicy().ID; got != "standalone/bhd/v3" {
		t.Fatalf("BHD naming policy ID = %q, want %q", got, "standalone/bhd/v3")
	}
}

func generatedBHDNameSubject(name string) api.UploadSubject {
	return api.UploadSubject{
		ReleaseName: name,
		GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
			IncludeEpisodeTitle: api.ReleaseNameVariant{Name: name},
		},
	}
}
