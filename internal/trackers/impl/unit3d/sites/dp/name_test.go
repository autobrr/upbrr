// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameAppliesDPTVDBDisambiguationMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		evidence api.TVDBNameDisambiguation
		want     string
	}{
		{
			name: "unique",
			evidence: api.TVDBNameDisambiguation{
				CanonicalName: "Example Series",
				SeriesYear:    2026,
			},
			want: "Example Series AKA Example Original S01E02 Example Episode 1080p WEB-DL Dual-Audio DD+ 5.1 H.265-GRP",
		},
		{
			name: "different year",
			evidence: api.TVDBNameDisambiguation{
				CanonicalName: "Example Series",
				SeriesYear:    2026,
				IncludeYear:   true,
			},
			want: "Example Series AKA Example Original 2026 S01E02 Example Episode 1080p WEB-DL Dual-Audio DD+ 5.1 H.265-GRP",
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
			want: "Example Series AKA Example Original US 2026 S01E02 Example Episode 1080p WEB-DL Dual-Audio DD+ 5.1 H.265-GRP",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := dpTVNameSubject(tt.evidence)
			if got := buildName(meta, config.TrackerConfig{}); got != tt.want {
				t.Fatalf("DP name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildNameKeepsDPAudioLabelBehavior(t *testing.T) {
	t.Parallel()

	meta := dpTVNameSubject(api.TVDBNameDisambiguation{
		CanonicalName: "Example Series",
		SeriesYear:    2026,
	})
	meta.AudioLanguages = []string{"English", "Japanese", "French"}
	const want = "Example Series AKA Example Original S01E02 Example Episode 1080p WEB-DL MULTi DD+ 5.1 H.265-GRP"
	if got := buildName(meta, config.TrackerConfig{}); got != want {
		t.Fatalf("DP audio name = %q, want %q", got, want)
	}
}

func TestBuildNameDoesNotRewriteLanguageMarkersForDiscs(t *testing.T) {
	t.Parallel()

	meta := dpTVNameSubject(api.TVDBNameDisambiguation{
		CanonicalName: "Example Series",
		SeriesYear:    2026,
	})
	meta.DiscType = "DVD"
	meta.AudioLanguages = []string{"English", "Japanese", "French"}
	got := buildName(meta, config.TrackerConfig{})
	if !strings.Contains(got, "Dual-Audio") || strings.Contains(got, "MULTi") {
		t.Fatalf("disc name rewrote language marker: %q", got)
	}
}

func dpTVNameSubject(evidence api.TVDBNameDisambiguation) api.UploadSubject {
	return api.UploadSubject{
		ReleaseName:  "Example Series 2026 AKA Example Original S01E02 Example Episode 1080p WEB-DL Dual-Audio DD+ 5.1 H.265-GRP",
		SeasonStr:    "S01",
		EpisodeStr:   "E02",
		EpisodeTitle: "Example Episode",
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryTV,
		},
		Release: api.ReleaseInfo{
			Category:   "TV",
			Resolution: "1080p",
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TVDB: &api.TVDBMetadata{
				NameDisambiguation: evidence,
			},
		},
		AudioLanguages: []string{"English", "Japanese"},
	}
}

func TestBuildNameAppliesDPManualTVDBYear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		releaseName string
		year        int
		want        string
	}{
		{
			name:        "manual year",
			releaseName: "Example Series 2030 AKA Example Original S01E02 Example Episode 1080p WEB-DL Dual-Audio DD+ 5.1 H.265-GRP",
			year:        2030,
			want:        "Example Series AKA Example Original 2030 S01E02 Example Episode 1080p WEB-DL Dual-Audio DD+ 5.1 H.265-GRP",
		},
		{
			name:        "manual empty year",
			releaseName: "Example Series AKA Example Original S01E02 Example Episode 1080p WEB-DL Dual-Audio DD+ 5.1 H.265-GRP",
			want:        "Example Series AKA Example Original S01E02 Example Episode 1080p WEB-DL Dual-Audio DD+ 5.1 H.265-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			meta := dpTVNameSubject(api.TVDBNameDisambiguation{
				CanonicalName: "Example Series",
				SeriesYear:    2026,
				IncludeYear:   true,
			})
			meta.ReleaseName = test.releaseName
			meta.EffectiveMetadata = api.EffectiveMetadata{Year: test.year, YearProvenance: api.FactProvenanceManual}
			if got := buildName(meta, config.TrackerConfig{}); got != test.want {
				t.Fatalf("DP manual TVDB year name = %q, want %q", got, test.want)
			}
		})
	}
}
