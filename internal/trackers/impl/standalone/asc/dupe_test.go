// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/internal/config"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestASCSearchDoesNotUseSourcePathAsTitle(t *testing.T) {
	t.Parallel()
	meta := api.DuplicateSubject{Anime: true, SourcePath: `C:\private\Example.Release.2026.mkv`}
	if got := resolveASCTitle(meta); got != "" {
		t.Fatalf("source path became a search title: %q", got)
	}
	result := (dupeSearcher{http: &http.Client{}}).Search(t.Context(), meta)
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingMetadata {
		t.Fatalf("missing title outcome = %v/%s", result.Disposition(), result.Code())
	}
}

func TestResolveASCTitleHonorsManualTitle(t *testing.T) {
	t.Parallel()

	projection := &api.TrackerReleaseProjection{DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Projected Release"}}
	manual := api.DuplicateSubject{
		Release:    api.ReleaseInfo{Title: "Automatic Release"},
		Projection: projection,
		EffectiveMetadata: api.EffectiveMetadata{
			Title:           "Manual Release",
			TitleProvenance: api.FactProvenanceManual,
		},
	}
	if got := resolveASCTitle(manual); got != "Manual Release" {
		t.Fatalf("manual ASC search title = %q", got)
	}
	manual.EffectiveMetadata = api.EffectiveMetadata{TitleProvenance: api.FactProvenanceManualEmpty}
	if got := resolveASCTitle(manual); got != "" {
		t.Fatalf("manual-empty ASC search title = %q", got)
	}
	if got := resolveASCTitle(api.DuplicateSubject{Projection: projection}); got != "Projected Release" {
		t.Fatalf("automatic ASC search title = %q", got)
	}
}

func inertiaHTML(t *testing.T, props any) string {
	t.Helper()

	payload, err := json.Marshal(map[string]any{"component": "test", "props": props})
	if err != nil {
		t.Fatalf("marshal props: %v", err)
	}
	return `<html><body><script data-page="app" type="application/json">` + string(payload) + `</script></body></html>`
}

func TestASCSearchParsesPagesAndDetails(t *testing.T) {
	t.Parallel()

	var queries []url.Values
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents":
			mu.Lock()
			queries = append(queries, r.URL.Query())
			mu.Unlock()
			page := r.URL.Query().Get("page")
			data := []map[string]any{{
				"id":         11,
				"name":       "Exemplo (Example) - S01",
				"size":       5000,
				"categoryId": 3,
			}}
			current := 1
			if page == "2" {
				data = []map[string]any{{
					"id":         12,
					"name":       "Exemplo (Example) - S01E02",
					"size":       700,
					"categoryId": 3,
					"internal":   true,
				}}
				current = 2
			}
			_, _ = w.Write([]byte(inertiaHTML(t, map[string]any{"torrents": map[string]any{
				"current_page": current,
				"last_page":    2,
				"data":         data,
			}})))
		case "/torrents/11":
			_, _ = w.Write([]byte(inertiaHTML(t, map[string]any{
				"files":      map[string]any{"current_page": 1, "data": []map[string]any{{"path": "Example.S01E01.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv"}, {"path": "Example.S01E02.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv"}}},
				"filesTotal": 2,
			})))
		case "/torrents/12":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	tmp := t.TempDir()
	cookieDir := filepath.Join(tmp, "cookies")
	if err := os.MkdirAll(cookieDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cookie := "# Netscape HTTP Cookie File\n.amigos-share.club\tTRUE\t/\tTRUE\t0\tamigos-share-club-session\tvalid\n"
	if err := os.WriteFile(filepath.Join(cookieDir, testCookieFileName), []byte(cookie), 0o600); err != nil {
		t.Fatalf("write cookie: %v", err)
	}
	searcher := dupeSearcher{
		cfg:    config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}},
		http:   &http.Client{Transport: rewriteTransport{target: target}},
		logger: api.NopLogger{},
	}
	result := searcher.Search(t.Context(), api.DuplicateSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV, IMDBID: 1234567}})
	if result.Disposition() != dupe.DispositionResolved {
		t.Fatalf("disposition = %v cause=%v", result.Disposition(), result.Cause())
	}
	entries := result.Entries()
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	pack, episode := entries[0], entries[1]
	if pack.Name != "Example.S01E01.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv" || !pack.Pack || pack.Season != 1 || pack.FileCount != 2 || pack.SizeBytes != 5000 {
		t.Fatalf("pack entry = %#v", pack)
	}
	if pack.Link != baseURL+"/torrents/11" {
		t.Fatalf("pack link = %q", pack.Link)
	}
	if episode.Name != "Exemplo (Example) - S01E02" || episode.Pack || episode.Season != 1 || episode.Episode != 2 || !episode.Internal {
		t.Fatalf("episode entry kept display name on detail failure: %#v", episode)
	}
	if len(queries) != 2 || queries[0].Get("q") != "tt1234567" || queries[0].Get("category") != categorySeries {
		t.Fatalf("search queries = %v", queries)
	}
	if !result.SearchEvidence().Complete || result.SearchEvidence().Pages != 2 {
		t.Fatalf("search evidence = %+v", result.SearchEvidence())
	}
}

func TestDecodePagePropsRejectsMissingPageData(t *testing.T) {
	t.Parallel()

	var out searchPage
	err := decodePageProps([]byte("<html>no data</html>"), &out)
	if _, ok := errors.AsType[parseError](err); !ok {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestDetailFilesAcceptsArrayAndPaginatedShapes(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"array":     `{"files":[{"path":"a.mkv"}],"filesTotal":1}`,
		"paginated": `{"files":{"current_page":1,"data":[{"path":"a.mkv"}]},"filesTotal":1}`,
	} {
		var page detailPage
		if err := json.Unmarshal([]byte(raw), &page); err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if len(page.Files) != 1 || page.Files[0].Path != "a.mkv" {
			t.Fatalf("%s: files = %+v", name, page.Files)
		}
	}
}
