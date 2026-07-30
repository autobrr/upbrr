// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package yus

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNamePreservesYUSHDRVocabulary(t *testing.T) {
	t.Parallel()

	const want = "Example Release 2026 2160p WEB-DL HLG H.265-GRP"
	if got := buildName(api.UploadSubject{ReleaseName: want}, config.TrackerConfig{}); got != want {
		t.Fatalf("YUS name = %q", got)
	}
}

func TestBuildNameAppliesYUSTVDBDisambiguationMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		evidence api.TVDBNameDisambiguation
		want     string
	}{
		{
			name:     "unique",
			evidence: api.TVDBNameDisambiguation{CanonicalName: "Example Series", SeriesYear: 2026},
			want:     "Example Series AKA Example Original S01E02 Example Episode 1080p WEB-DL H.265-GRP",
		},
		{
			name: "different year",
			evidence: api.TVDBNameDisambiguation{
				CanonicalName: "Example Series",
				SeriesYear:    2026,
				IncludeYear:   true,
			},
			want: "Example Series AKA Example Original 2026 S01E02 Example Episode 1080p WEB-DL H.265-GRP",
		},
		{
			name: "same year",
			evidence: api.TVDBNameDisambiguation{
				CanonicalName: "Example Series",
				SeriesYear:    2026,
				Locale:        "US",
				IncludeYear:   true,
				IncludeLocale: true,
				Status:        api.MetadataEvidenceStatusPartial,
			},
			want: "Example Series AKA Example Original US 2026 S01E02 Example Episode 1080p WEB-DL H.265-GRP",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := yusTVNameSubject(tt.evidence)
			if got := buildName(meta, config.TrackerConfig{}); got != tt.want {
				t.Fatalf("YUS name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildNameUsesYUSTMDBMovieYear(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		ReleaseName: "Example Release AKA Example Original 2025 1080p BluRay x265-GRP",
		Identity:    api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		Release: api.ReleaseInfo{
			Category:   "MOVIE",
			Year:       2025,
			Resolution: "1080p",
		},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2026}},
	}
	const want = "Example Release AKA Example Original 2026 1080p BluRay x265-GRP"
	if got := buildName(meta, config.TrackerConfig{}); got != want {
		t.Fatalf("YUS movie name = %q, want %q", got, want)
	}
}

func TestApplyYUSTVDBDisambiguationRejectsStaleSource(t *testing.T) {
	t.Parallel()

	const original = "Example Series 2026 AKA Example Original S01E02 Example Episode 1080p WEB-DL H.265-GRP"
	meta := yusTVNameSubject(api.TVDBNameDisambiguation{
		CanonicalName: "Example Series",
		SeriesYear:    2026,
		IncludeYear:   true,
	})
	meta.SourcePath = "current-source"
	meta.Identity.SourcePath = "current-source"
	meta.Identity.Generation = 3
	meta.ProviderMetadata.SourcePath = "stale-source"
	meta.ProviderMetadata.Generation = 3

	if got := applyYUSTVDBDisambiguation(original, meta); got != original {
		t.Fatalf("stale YUS TVDB metadata changed name: %q", got)
	}
}

func TestApplyYUSTMDBMovieYearRejectsStaleGeneration(t *testing.T) {
	t.Parallel()

	const original = "Example Release AKA Example Original 2025 1080p BluRay x265-GRP"
	meta := api.UploadSubject{
		SourcePath:  "current-source",
		ReleaseName: original,
		Identity: api.ExternalIdentity{
			SourcePath: "current-source",
			Generation: 3,
			Category:   api.CanonicalCategoryMovie,
		},
		Release: api.ReleaseInfo{
			Category:   "MOVIE",
			Year:       2025,
			Resolution: "1080p",
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "current-source",
			Generation: 2,
			TMDB:       &api.TMDBMetadata{Year: 2026},
		},
	}

	if got := applyYUSTMDBMovieYear(original, meta); got != original {
		t.Fatalf("stale YUS TMDB metadata changed name: %q", got)
	}
}

func TestBuildNameOmitsYUSEditionAndRestrictsDistributorToDisc(t *testing.T) {
	t.Parallel()

	nonDisc := api.UploadSubject{
		ReleaseName: "Example Release 2026 Limited 1080p BluRay x265-GRP",
		Edition:     "Limited",
		Distributor: "Criterion",
		Type:        "ENCODE",
	}
	const nonDiscWant = "Example Release 2026 1080p BluRay x265-GRP"
	if got := buildName(nonDisc, config.TrackerConfig{}); got != nonDiscWant {
		t.Fatalf("YUS non-disc name = %q, want %q", got, nonDiscWant)
	}

	disc := api.UploadSubject{
		ReleaseName: "Example Release 2026 Limited 1080p USA Blu-ray AVC-GRP",
		Edition:     "Limited",
		Distributor: "Criterion",
		Type:        "DISC",
		DiscType:    "BDMV",
		Region:      "USA",
		Release:     api.ReleaseInfo{Resolution: "1080p"},
	}
	const discWant = "Example Release 2026 1080p Criterion USA Blu-ray AVC-GRP"
	if got := buildName(disc, config.TrackerConfig{}); got != discWant {
		t.Fatalf("YUS disc name = %q, want %q", got, discWant)
	}
}

func yusTVNameSubject(evidence api.TVDBNameDisambiguation) api.UploadSubject {
	return api.UploadSubject{
		ReleaseName:  "Example Series 2026 AKA Example Original S01E02 Example Episode 1080p WEB-DL H.265-GRP",
		SeasonStr:    "S01",
		EpisodeStr:   "E02",
		EpisodeTitle: "Example Episode",
		Identity:     api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		Release: api.ReleaseInfo{
			Category:   "TV",
			Resolution: "1080p",
		},
		ProviderMetadata: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{NameDisambiguation: evidence}},
	}
}
