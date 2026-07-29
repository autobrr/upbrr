// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNamePreservesHHDHDRVocabulary(t *testing.T) {
	t.Parallel()

	const want = "Example Release 2026 2160p WEB-DL PQ10 H.265-GRP"
	if got := buildName(api.UploadSubject{ReleaseName: want}, config.TrackerConfig{}); got != want {
		t.Fatalf("HHD name = %q", got)
	}
}

func TestBuildNameAppliesHHDTVDBDisambiguationMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		evidence api.TVDBNameDisambiguation
		want     string
	}{
		{
name: "unique",
 evidence: api.TVDBNameDisambiguation{CanonicalName: "Example Series", SeriesYear: 2026},
 want: "Example Series AKA Example Original S01E02 Example Episode 1080p WEB-DL H.265-GRP",
},
		{
name: "different year",
 evidence: api.TVDBNameDisambiguation{
CanonicalName: "Example Series",
 SeriesYear: 2026,
 IncludeYear: true,
},
 want: "Example Series AKA Example Original 2026 S01E02 Example Episode 1080p WEB-DL H.265-GRP",
},
		{
name: "same year",
 evidence: api.TVDBNameDisambiguation{
CanonicalName: "Example Series",
 SeriesYear: 2026,
 Locale: "US",
 IncludeYear: true,
 IncludeLocale: true,
 Status: api.MetadataEvidenceStatusPartial,
},
 want: "Example Series AKA Example Original US 2026 S01E02 Example Episode 1080p WEB-DL H.265-GRP",
},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := hhdTVNameSubject(tt.evidence)
			if got := buildName(meta, config.TrackerConfig{}); got != tt.want {
				t.Fatalf("HHD name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildNameOmitsHHDEditionAndRestrictsDistributorToDisc(t *testing.T) {
	t.Parallel()

	nonDisc := api.UploadSubject{
		ReleaseName: "Example Release 2026 Limited 1080p BluRay x265-GRP",
		Edition:     "Limited",
		Distributor: "Criterion",
		Type:        "ENCODE",
	}
	const nonDiscWant = "Example Release 2026 1080p BluRay x265-GRP"
	if got := buildName(nonDisc, config.TrackerConfig{}); got != nonDiscWant {
		t.Fatalf("HHD non-disc name = %q, want %q", got, nonDiscWant)
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
		t.Fatalf("HHD disc name = %q, want %q", got, discWant)
	}
}

func hhdTVNameSubject(evidence api.TVDBNameDisambiguation) api.UploadSubject {
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
