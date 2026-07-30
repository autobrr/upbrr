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
