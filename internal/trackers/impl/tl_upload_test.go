// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTLUploadTorrentAndRegisteredArtifact(t *testing.T) {
	root := t.TempDir()
	const releaseName = "Example.Movie.2026.1080p.BluRay.x264-GRP"
	source := filepath.Join(root, releaseName+".mkv")
	baseTorrent := filepath.Join(root, "base.torrent")
	if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:        source,
		OutputPath:  baseTorrent,
		TrackerURLs: []string{"https://base.example/announce", "https://backup.example/announce"},
		IsPrivate:   true,
		Quiet:       true,
	}); err != nil {
		t.Fatal(err)
	}
	base, err := metainfo.LoadFromFile(baseTorrent)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.AnnounceList) == 0 {
		t.Fatal("base torrent must contain an inherited announce list")
	}
	wantInfo, err := base.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	wantInfo.Source = "TorrentLeech.org"
	// The same source fits in one piece at either size, but the infohash changes.
	alternateInfo := wantInfo
	alternateInfo.PieceLength *= 2
	alternate := *base
	alternate.InfoBytes, err = bencode.Marshal(alternateInfo)
	if err != nil {
		t.Fatal(err)
	}
	alternateBytes, err := bencode.Marshal(&alternate)
	if err != nil {
		t.Fatal(err)
	}
	alternatePath := filepath.Join(root, "alternate.torrent")
	if err := os.WriteFile(alternatePath, alternateBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := MustNewRegistry()
	subject := api.UploadSubject{
		SourcePath:       source,
		TorrentPath:      baseTorrent,
		ReleaseName:      releaseName,
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 1234567},
		ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Movie"}},
	}
	for _, test := range []struct {
		name      string
		path      string
		apiUpload bool
	}{
		{"API", "/torrents/upload/apiupload", true},
		{"cookie form", "/torrents/upload/", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "upbrr.db")
			if !test.apiUpload {
				writeTextCookie(t, dbPath, "TL", ".torrentleech.org")
			}
			var posted *metainfo.MetaInfo
			requests := 0
			transport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = transport })
			http.DefaultTransport = connectionRoundTripper(func(req *http.Request) (*http.Response, error) {
				requests++
				defer req.Body.Close()
				if req.Method != http.MethodPost || req.URL.String() != "https://www.torrentleech.org"+test.path {
					return nil, fmt.Errorf("unexpected TL upload request: %s %s", req.Method, req.URL)
				}
				file, _, err := req.FormFile("torrent")
				if err != nil {
					return nil, fmt.Errorf("read TL multipart file: %w", err)
				}
				defer file.Close()
				posted, err = metainfo.Load(file)
				if err != nil {
					return nil, fmt.Errorf("decode TL multipart torrent: %w", err)
				}
				if test.apiUpload {
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("123"))}, nil
				}
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{"Location": {"/successfulupload?torrentID=123"}},
					Body:       http.NoBody,
				}, nil
			})
			cfg := config.Config{
				MainSettings: config.MainSettingsConfig{DBPath: dbPath},
				Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
					"TL": {APIUpload: test.apiUpload, Passkey: "synthetic"},
				}},
			}
			projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{
				Tracker:       "TL",
				TrackerConfig: cfg.Trackers.Trackers["TL"],
				Meta:          subject,
			}, "", "", "")
			if failure != nil {
				t.Fatal(failure)
			}
			service := trackers.NewServiceWithRegistry(cfg, nil, nil, registry)
			prepare := func(subject api.UploadSubject) *trackers.RetainedUploadPlan {
				t.Helper()
				plan, err := service.PrepareRetainedUploadPlan(t.Context(), subject, []api.TrackerReleaseProjection{projection}, api.WorkflowExecutionModeNormal)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = plan.Release() })
				return plan
			}
			first := prepare(subject)
			secondSubject := subject
			secondSubject.TorrentPath = alternatePath
			prepare(secondSubject)
			results, err := first.Execute(t.Context())
			if err != nil || len(results) != 1 || requests != 1 {
				t.Fatalf("TL upload returned %d results after %d requests, err = %v", len(results), requests, err)
			}
			result := results[0]
			if result.Failure != nil || result.Summary.Uploaded != 1 || len(result.Summary.UploadedTorrents) != 1 {
				t.Fatalf("TL upload summary = %+v, failure = %+v", result.Summary, result.Failure)
			}
			if posted == nil || posted.Announce != "https://fake.tracker" || posted.AnnounceList != nil {
				t.Fatal("TL multipart torrent must contain only the top-level placeholder announce")
			}
			info, err := posted.UnmarshalInfo()
			if err != nil || !reflect.DeepEqual(info, wantInfo) {
				t.Fatalf("TL multipart torrent changed the source payload or omitted the source flag: %v", err)
			}
			artifact := result.Summary.UploadedTorrents[0]
			// A subsequent dry run builds another upload plan before injection retries use the retained path.
			prepare(secondSubject)
			registered, err := metainfo.LoadFromFile(artifact.TorrentPath)
			if err != nil {
				t.Fatal(err)
			}
			wantAnnounces := []string{
				"https://tracker.torrentleech.org/a/synthetic/announce",
				"https://tracker.tleechreload.org/a/synthetic/announce",
			}
			if registered.Announce != wantAnnounces[0] || len(registered.AnnounceList) != 1 || !slices.Equal(registered.AnnounceList[0], wantAnnounces) {
				t.Fatal("registered TL torrent did not replace the placeholder with both seeding announces")
			}
			const torrentURL = "https://www.torrentleech.org/torrent/123"
			if registered.HashInfoBytes() != posted.HashInfoBytes() || artifact.TorrentURL != torrentURL || !strings.HasSuffix(registered.Comment, "\n"+torrentURL) {
				t.Fatal("registered TL torrent lost its submitted infohash or torrent page")
			}
		})
	}
}
