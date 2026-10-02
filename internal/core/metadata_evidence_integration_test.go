// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type metadataEvidenceTransport func(*http.Request) (*http.Response, error)

func (f metadataEvidenceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDefaultCoreMetadataEvidenceSurvivesServiceCorrection(t *testing.T) {
	// These are the two production repository ownership paths used by CLI and WebUI.
	for _, borrowed := range []bool{false, true} {
		name := "CLI-owned repository"
		if borrowed {
			name = "WebUI-borrowed repository"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "Example.Series.S01E04.1080p.WEB-DL.H264-GRP.mkv")
			if err := os.WriteFile(source, []byte("synthetic video"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadEmbeddedDefaultConfig()
			if err != nil {
				t.Fatal(err)
			}
			cfg.MainSettings.DBPath = filepath.Join(root, "metadata.sqlite")
			cfg.MainSettings.SceneDetection = true
			cfg.Metadata.SkipAutoTorrent = true
			cfg.Trackers.Trackers["AITHER"] = config.TrackerConfig{APIKey: "synthetic-key"}
			for name, client := range cfg.TorrentClients {
				client.URL, client.Username, client.Password = "http://127.0.0.1:1", "test", "test"
				cfg.TorrentClients[name] = client
			}
			tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
			if err != nil {
				t.Fatal(err)
			}
			temp, _, err := paths.ReleaseTempDirFor(tmpRoot, source, metadata.ParseReleaseInfo(source))
			if err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"mediainfo.txt": "General\nFormat : Matroska\n", "MediaInfo.json": `{"media":{"track":[{"@type":"General","Format":"Matroska"},{"@type":"Video","Format":"AVC","Width":"1920","Height":"1080"}]}}`} {
				if err := os.WriteFile(filepath.Join(temp, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var mu sync.Mutex
			calls := make(map[string]int)
			originalTransport := http.DefaultTransport
			http.DefaultTransport = metadataEvidenceTransport(func(request *http.Request) (*http.Response, error) {
				key := request.URL.Host + request.URL.RequestURI()
				mu.Lock()
				calls[key]++
				mu.Unlock()
				var body string
				status := http.StatusOK
				switch {
				case request.URL.Host == "aither.cc" && request.URL.Path == "/api/torrents/42":
					body = `{"data":[{"id":42,"attributes":{"category":"TV","description":"Release notes [img]https://93.184.216.34/failed.png[/img]"}}]}`
				case request.URL.Host == "93.184.216.34" && request.URL.Path == "/failed.png":
					status, body = http.StatusNotFound, "missing"
				case request.URL.Host == "api.srrdb.com":
					body = `{"resultsCount":0,"results":[]}`
				case request.URL.Host == "api.tvmaze.com" && request.URL.Path == "/shows/555001":
					body = `{"id":555001,"name":"Example Series","premiered":"2026-01-01","language":"English","type":"Scripted","externals":{}}`
				case request.URL.Host == "api.tvmaze.com" && request.URL.Path == "/shows/555001/images":
					body = `[]`
				case request.URL.Host == "api.tvmaze.com" && request.URL.Path == "/shows/555001/episodebynumber":
					body = `{"id":555104,"name":"The Fourth Signal","season":1,"number":4,"airdate":"2026-01-04"}`
				default:
					t.Errorf("unexpected metadata HTTP request: %s", key)
					body = `{}`
				}
				return &http.Response{
					StatusCode: status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    request,
				}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = originalTransport })
			deps := api.CoreDependencies{Config: cfg, SkipCookieMigration: true}
			if borrowed {
				repo, err := db.OpenContext(t.Context(), cfg.MainSettings.DBPath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = repo.Close() })
				if err := repo.MigrateContext(t.Context()); err != nil {
					t.Fatal(err)
				}
				deps.Repository, deps.RepositoryOwner = repo.RepositoryCapabilities(), repo
			}
			var c *Core
			if borrowed {
				c, err = NewWithContextAndCoordinator(t.Context(), deps, nil)
			} else {
				c, err = NewWithContext(t.Context(), deps)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.ShutdownWorkflowCoordinator(context.Background()); _ = c.Close() })
			input := api.PrepareInput{
				SourcePath:        source,
				ExternalFreshness: api.ExternalFreshnessLoad,
				Policy:            api.PreparationPolicy{KeepImages: true},
				Instructions: api.ReleaseFactInstructions{
					Category:     new(api.CanonicalCategoryTV),
					SourceLookup: "https://aither.cc/torrents/42",
					Identity: api.ExternalIDOverrides{
						TMDBID:   new(0),
						IMDBID:   new(0),
						TVDBID:   new(0),
						TVmazeID: new(555001),
						MALID:    new(0),
					},
				},
			}
			initial, err := c.preparedFacts.Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			firstCalls := maps.Clone(calls)
			mu.Unlock()
			if firstCalls["api.srrdb.com/v1/search/word:Example%20Series%20S01E04"] != 1 || firstCalls["api.tvmaze.com/shows/555001/episodebynumber?number=4&season=1"] != 1 || firstCalls["aither.cc/api/torrents/42"] != 1 || firstCalls["93.184.216.34/failed.png"] != 1 {
				t.Fatalf("initial provider/tracker/image lookup missing: %v", firstCalls)
			}
			// Expire the tracker cooldown so only retained evidence can suppress the failed image request.
			repo, ok := c.repoOwner.(*db.SQLiteRepository)
			if !ok {
				t.Fatal("default Core did not retain its SQLite owner")
			}
			for _, key := range []string{"AITHER", "AITHER:assets"} {
				if err := repo.SaveTrackerTimestamp(t.Context(), api.TrackerTimestamp{Tracker: key, UpdatedAt: time.Now().Add(-time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			resolved, err := c.preparedFacts.ResolveInput(t.Context(), api.PrepareInput{
				SourcePath:        source,
				ExternalFreshness: api.ExternalFreshnessReuse,
				Policy:            api.PreparationPolicy{KeepImages: true},
				Instructions:      api.ReleaseFactInstructions{SourceLookup: "https://aither.cc/torrents/42"},
			}, api.ReleaseCorrectionUpdate{
				Mode:  api.ReleaseCorrectionUpdatePatch,
				Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Service: new("NF")}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			refreshed, err := c.preparedFacts.PrepareResolved(t.Context(), resolved)
			if err != nil {
				t.Fatal(err)
			}
			if refreshed.Release.Generation <= initial.Release.Generation || !strings.Contains(refreshed.Release.Naming.ReleaseName, "NF") {
				t.Fatalf("service correction missing from new generation: %s", refreshed.Release.Naming.ReleaseName)
			}
			mu.Lock()
			after := maps.Clone(calls)
			mu.Unlock()
			if !reflect.DeepEqual(firstCalls, after) {
				t.Fatalf("service correction repeated metadata HTTP: before=%v after=%v", firstCalls, after)
			}
		})
	}
}
