package ldu

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameUsesFirstParseableLanguages(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName:       "Example.Release.2026.1080p.WEB-DL.DD5.1.H264-GRP",
		Identity:          api.ExternalIdentity{Category: "MOVIE"},
		AudioLanguages:    []string{"", "Japanese", "English"},
		SubtitleLanguages: []string{"", "English"},
		ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"}},
	}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	if !strings.Contains(got, "[JPN]") || !strings.Contains(got, "[Subs ENG]") {
		t.Fatalf("name = %q", got)
	}
}

func TestBuildNameDoesNotAddLanguageMarkersToDiscs(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		DiscType:          "BDMV",
		ReleaseName:       "Example Release 2026 1080p BluRay AVC-GRP",
		AudioLanguages:    []string{"Japanese"},
		SubtitleLanguages: []string{"English"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	if strings.Contains(got, "[JPN]") || strings.Contains(got, "[Subs ENG]") {
		t.Fatalf("disc name added language markers: %q", got)
	}
}

func TestOriginalLanguagePrefersManualFacts(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"}},
		EffectiveMetadata: api.EffectiveMetadata{
			OriginalLanguage: "en", OriginalLanguageProvenance: api.FactProvenanceManual,
		},
	}
	if got := originalLanguage(meta); got != "en" {
		t.Fatalf("manual original language = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguage = ""
	meta.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManualEmpty
	if got := originalLanguage(meta); got != "" {
		t.Fatalf("manual-empty original language = %q", got)
	}
}
