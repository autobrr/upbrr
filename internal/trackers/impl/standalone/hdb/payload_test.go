// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestMultipartTorrentFilename(t *testing.T) {
	torrentPath := filepath.Join(t.TempDir(), "prepared.torrent")
	content := []byte("prepared torrent bytes")
	if err := os.WriteFile(torrentPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want string
	}{
		{
			name: "Example Film 2026 WEB-DL DD+5.1 HDR10+ DTS:X-GRP",
			want: "Example.Film.2026.WEB-DL.DDP5.1.HDR10P.DTS-X-GRP.torrent",
		},
		{
			name: "Café & Someone’s 'Story' 2026-GRP",
			want: "Cafe.and.Someones.Story.2026-GRP.torrent",
		},
		{
			name: "../Example\\Film: [2026]\"\r\n\t日本語😀 / 1080p_GRP...",
			want: "Example.Film.2026.1080p_GRP.torrent",
		},
		{
			name: "Example.Film.2026.1080p.WEB-DL-GRP",
			want: "Example.Film.2026.1080p.WEB-DL-GRP.torrent",
		},
		{
			name: "日本語😀..._-",
			want: "release.torrent",
		},
	} {
		t.Run(tc.want, func(t *testing.T) {
			body, contentType, err := buildMultipartPayload(map[string]string{"name": tc.name}, torrentPath)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequestWithContext(t.Context(), "POST", "/upload", bytes.NewReader(body))
			req.Header.Set("Content-Type", contentType)
			if err := req.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := req.MultipartForm.RemoveAll(); err != nil {
					t.Error(err)
				}
			})
			if got := req.FormValue("name"); got != tc.name {
				t.Fatalf("display name = %q, want %q", got, tc.name)
			}
			file, header, err := req.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if header.Filename != tc.want {
				t.Fatalf("filename = %q, want %q", header.Filename, tc.want)
			}
			got, err := io.ReadAll(file)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, content) {
				t.Fatal("torrent content changed")
			}
		})
	}
}

func TestBuildUploadFieldsPreservesOnlyExplicitSeasonZero(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		season     int
		label      string
		wantSeason string
	}{
		{
			name:       "explicit special",
			label:      "S00",
			wantSeason: "0",
		},
		{name: "unknown season", wantSeason: "1"},
		{
			name:       "conflicting season label",
			label:      "S02",
			wantSeason: "1",
		},
		{
			name:       "negative season",
			season:     -1,
			label:      "S00",
			wantSeason: "1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := buildUploadFields(api.UploadSubject{
				Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryTV, TVDBID: 765432},
				SeasonInt:  test.season,
				SeasonStr:  test.label,
				EpisodeInt: 3,
			}, false, 2, 5, 6, "description", "Example.Series.S00E03.1080p-GRP")
			if got := fields["tvdb_season"]; got != test.wantSeason {
				t.Fatalf("tvdb_season = %q, want %q", got, test.wantSeason)
			}
			if got := fields["tvdb_episode"]; got != "3" {
				t.Fatalf("tvdb_episode = %q, want 3", got)
			}
		})
	}
}

func TestBuildUploadFieldsTVEpisodeCoordinates(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name             string
		season           int
		seasonLabel      string
		episode          int
		pack             bool
		multipleEpisodes bool
		wantSeason       string
		wantEpisode      string
	}{
		{
			name:        "ordinary pack",
			season:      2,
			seasonLabel: "S02",
			pack:        true,
			wantSeason:  "2",
			wantEpisode: "0",
		},
		{
			name:        "specials pack",
			seasonLabel: "S00",
			pack:        true,
			wantSeason:  "0",
			wantEpisode: "0",
		},
		{
			name:        "pack with stale episode",
			season:      2,
			seasonLabel: "S02",
			episode:     3,
			pack:        true,
			wantSeason:  "2",
			wantEpisode: "0",
		},
		{
			name:        "single special",
			seasonLabel: "S00",
			episode:     3,
			wantSeason:  "0",
			wantEpisode: "3",
		},
		{
			name:        "single missing episode",
			season:      2,
			seasonLabel: "S02",
			wantSeason:  "2",
			wantEpisode: "1",
		},
		{
			name:        "single negative episode",
			season:      2,
			seasonLabel: "S02",
			episode:     -1,
			wantSeason:  "2",
			wantEpisode: "1",
		},
		{
			name:             "combined non-pack episodes",
			season:           2,
			seasonLabel:      "S02",
			episode:          3,
			multipleEpisodes: true,
			wantSeason:       "2",
			wantEpisode:      "3",
		},
		{
			name:             "combined non-pack missing episode",
			seasonLabel:      "S00",
			multipleEpisodes: true,
			wantSeason:       "0",
			wantEpisode:      "1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := buildUploadFields(api.UploadSubject{
				Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryTV, TVDBID: 765432},
				SeasonInt:        test.season,
				SeasonStr:        test.seasonLabel,
				EpisodeInt:       test.episode,
				TVPack:           test.pack,
				MultipleEpisodes: test.multipleEpisodes,
			}, false, 2, 5, 6, "description", "Example.Series.1080p-GRP")
			if got := fields["tvdb_season"]; got != test.wantSeason {
				t.Errorf("tvdb_season = %q, want %q", got, test.wantSeason)
			}
			if got := fields["tvdb_episode"]; got != test.wantEpisode {
				t.Errorf("tvdb_episode = %q, want %q", got, test.wantEpisode)
			}
		})
	}
}
