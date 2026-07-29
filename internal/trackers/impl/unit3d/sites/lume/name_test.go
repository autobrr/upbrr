// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameUsesLumeStructuredHDRVocabulary(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		ReleaseName: "Example Release 2026 Hybrid 2160p WEB-DL DV PQ10 Hi10P H.265-GRP",
		HDR:         "DV PQ10",
		HDRFacts: api.HDRFacts{
			Formats: []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatPQ10},
			Origin:  api.HDREvidenceMediaInfo,
			Status:  api.HDREvidenceComplete,
		},
	}
	got := buildName(meta, config.TrackerConfig{})
	if strings.Contains(got, "Hybrid") || strings.Contains(got, "Hi10P") || !strings.Contains(got, "DV HDR") {
		t.Fatalf("LUME name = %q", got)
	}
}

func TestBuildNameAppliesLumeTVDBYearMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		evidence api.TVDBNameDisambiguation
		want     string
	}{
		{
name: "unique",
 evidence: api.TVDBNameDisambiguation{CanonicalName: "Example Series", SeriesYear: 2026},
 want: "Example Series CA AKA Example Original S01E02 Example Episode 1080p WEB-DL H.265-GRP",
},
		{
name: "different year",
 evidence: api.TVDBNameDisambiguation{
CanonicalName: "Example Series",
 SeriesYear: 2026,
 IncludeYear: true,
},
 want: "Example Series CA AKA Example Original 2026 S01E02 Example Episode 1080p WEB-DL H.265-GRP",
},
		{
name: "same year ignores collision locale",
 evidence: api.TVDBNameDisambiguation{
CanonicalName: "Example Series",
 SeriesYear: 2026,
 Locale: "US",
 IncludeYear: true,
 IncludeLocale: true,
},
 want: "Example Series CA AKA Example Original 2026 S01E02 Example Episode 1080p WEB-DL H.265-GRP",
},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := lumeTVNameSubject(tt.evidence)
			if got := buildName(meta, config.TrackerConfig{}); got != tt.want {
				t.Fatalf("LUME name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildNameKeepsLumeEditionOnlyForFullDisc(t *testing.T) {
	t.Parallel()

	nonDisc := api.UploadSubject{
		ReleaseName: "Example Release 2026 Limited 1080p BluRay x265-GRP",
		Edition:     "Limited",
		Type:        "ENCODE",
	}
	const nonDiscWant = "Example Release 2026 1080p BluRay x265-GRP"
	if got := buildName(nonDisc, config.TrackerConfig{}); got != nonDiscWant {
		t.Fatalf("LUME non-disc name = %q, want %q", got, nonDiscWant)
	}

	disc := api.UploadSubject{
		ReleaseName: "Example Release 2026 Limited 1080p USA Blu-ray AVC-GRP",
		Edition:     "Limited",
		Type:        "DISC",
		DiscType:    "BDMV",
	}
	const discWant = "Example Release 2026 Limited 1080p USA Blu-ray AVC-GRP"
	if got := buildName(disc, config.TrackerConfig{}); got != discWant {
		t.Fatalf("LUME disc name = %q, want %q", got, discWant)
	}
}

func lumeTVNameSubject(evidence api.TVDBNameDisambiguation) api.UploadSubject {
	return api.UploadSubject{
		ReleaseName:  "Example Series 2026 CA AKA Example Original S01E02 Example Episode 1080p WEB-DL H.265-GRP",
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
