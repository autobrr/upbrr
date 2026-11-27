// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSearchUsesReviewedAnimeCriterion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	cookieDir := filepath.Join(dir, "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cookieDir, "BT.json"), []byte(`{"session":"synthetic-cookie"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	providers := api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{Title: "Provider Signal"},
		TVDB: &api.TVDBMetadata{Aliases: []string{"Canonical Alias"}},
	}
	for _, test := range []struct {
		name      string
		anime     bool
		projected bool
		blank     bool
		effective api.EffectiveMetadata
		want      string
	}{
		{
			name:      "reviewed provider title",
			anime:     true,
			projected: true,
			want:      "Provider Signal",
		},
		{
			name:      "reviewed blank skips request",
			anime:     true,
			projected: true,
			blank:     true,
		},
		{
			name:      "manual clear skips projected upload name fallback",
			anime:     true,
			projected: true,
			effective: api.EffectiveMetadata{TitleProvenance: api.FactProvenanceManualEmpty},
		},
		{
			name:  "direct anime fallback",
			anime: true,
			want:  "Canonical Alias",
		},
		{
			name:      "non-anime IMDb search",
			projected: true,
			want:      "tt0555002",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := api.DuplicateSubject{
				Anime:             test.anime,
				Release:           api.ReleaseInfo{Title: providers.TVDB.DisplayName()},
				Identity:          api.ExternalIdentity{IMDBID: 555002},
				ProviderMetadata:  providers,
				EffectiveMetadata: test.effective,
			}
			if test.projected {
				subject := api.UploadSubject{
					Anime:             test.anime,
					ReleaseName:       meta.Release.Title,
					ProviderMetadata:  providers,
					EffectiveMetadata: test.effective,
				}
				prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(
					trackers.PreparationInput{Tracker: "BT", Meta: subject}, New().ReleaseNamePolicy())
				if failure != nil {
					t.Fatal(failure)
				}
				meta.Projection = prepared.Projection
				meta.ReleaseName = prepared.Projection.DuplicateCriteria.Name
				if test.blank {
					meta.Projection.DuplicateCriteria.Name = ""
				}
			}
			var requested bool
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requested = true
				if req.Method != http.MethodGet || req.URL.Path != "/torrents.php" || req.URL.Query().Get("searchstr") != test.want {
					t.Errorf("unexpected BT search %s %s searchstr=%q", req.Method, req.URL.Path, req.URL.Query().Get("searchstr"))
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("<html></html>"))}, nil
			})}
			adapter := dupeSearcher{cfg: config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}, http: client}
			result := adapter.Search(t.Context(), meta)
			if test.want == "" {
				if requested || result.Code() != dupe.NotRunMissingMetadata {
					t.Fatalf("blank criterion requested=%t, code=%v", requested, result.Code())
				}
			} else if !requested || result.Cause() != nil {
				t.Fatalf("search requested=%t, error=%v", requested, result.Cause())
			}
		})
	}
}

func TestProcessBTGroupPageKeepsFolderBasedPackName(t *testing.T) {
	t.Parallel()

	root, err := html.Parse(strings.NewReader(`
<table><tr id="torrent42"><td><a onclick="gtoggle()">Season Pack</a></td></tr></table>
<div id="files_42">
  <div class="filelist_path">/Example.Show.S01.1080p-GRP/</div>
  <table class="filelist_table"><tr><td>Example.Show.S01E01.1080p-GRP.mkv</td></tr></table>
</div>`))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	var found []string
	processBTGroupPage(root, &found)
	if len(found) != 1 || found[0] != "Example.Show.S01.1080p-GRP" {
		t.Fatalf("found items = %#v", found)
	}
}
