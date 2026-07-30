package ulcx

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameRemovesHybridFromWebDV(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Example Release 2026 Hybrid 1080p WEB-DL DDP5.1 DV H.265-GRP",
		Type:        "WEBDL",
		Edition:     "Hybrid",
		WebDV:       true,
	}
	if got := Profile().Site.BuildName(meta, config.TrackerConfig{}); strings.Contains(got, "Hybrid") {
		t.Fatalf("name = %q", got)
	}
}

func TestBuildNameAppliesULCXTVDBDisambiguationMatrix(t *testing.T) {
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
			name: "same year locale omits year",
			evidence: api.TVDBNameDisambiguation{
				CanonicalName: "Example Series",
				SeriesYear:    2026,
				Locale:        "US",
				IncludeYear:   true,
				IncludeLocale: true,
				Status:        api.MetadataEvidenceStatusPartial,
			},
			want: "Example Series AKA Example Original US S01E02 Example Episode 1080p WEB-DL H.265-GRP",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := ulcxTVNameSubject(tt.evidence)
			if got := buildName(meta, config.TrackerConfig{}); got != tt.want {
				t.Fatalf("ULCX name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildNameAppliesULCXEditionDistributorAndX265Rules(t *testing.T) {
	t.Parallel()

	encode := api.UploadSubject{
		ReleaseName: "Example Release 2026 Limited 1080p BluRay H.265-GRP",
		Edition:     "Limited",
		Distributor: "Criterion",
		Type:        "ENCODE",
		VideoEncode: "x265",
	}
	const encodeWant = "Example Release 2026 1080p BluRay x265-GRP"
	if got := buildName(encode, config.TrackerConfig{}); got != encodeWant {
		t.Fatalf("ULCX encode name = %q, want %q", got, encodeWant)
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
		t.Fatalf("ULCX disc name = %q, want %q", got, discWant)
	}
}

func TestApplyULCXTVDBDisambiguationRejectsStaleGeneration(t *testing.T) {
	t.Parallel()

	const original = "Example Series 2026 AKA Example Original S01E02 Example Episode 1080p WEB-DL H.265-GRP"
	meta := ulcxTVNameSubject(api.TVDBNameDisambiguation{
		CanonicalName: "Example Series",
		SeriesYear:    2026,
		IncludeYear:   true,
	})
	meta.SourcePath = "current-source"
	meta.Identity.SourcePath = "current-source"
	meta.Identity.Generation = 3
	meta.ProviderMetadata.SourcePath = "current-source"
	meta.ProviderMetadata.Generation = 2

	if got := applyULCXTVDBDisambiguation(original, meta); got != original {
		t.Fatalf("stale ULCX TVDB metadata changed name: %q", got)
	}
}

func ulcxTVNameSubject(evidence api.TVDBNameDisambiguation) api.UploadSubject {
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
