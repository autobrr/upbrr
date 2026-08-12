// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func (d *Definition) submit(ctx context.Context, input trackers.PreparationInput) (api.UploadSummary, error) {
	return uploadAt(ctx, input, d.baseURL, d.httpClient)
}

func TestDefinitionBuildDescriptionUsesHDBGroup(t *testing.T) {
	result, err := prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "HDB",
		Meta:    api.UploadSubject{},
		Logger:  api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Group != "hdb" {
		t.Fatalf("expected hdb group, got %q", result.Group)
	}
}

func TestDefinitionBuildDescriptionUsesProvidedAssets(t *testing.T) {
	result, err := prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "HDB",
		Meta:    api.UploadSubject{},
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: "kept description",
			Screenshots: []api.ScreenshotImage{{
				ImgURL: "https://t.hdbits.org/example.jpg",
				WebURL: "https://img.hdbits.org/example",
				RawURL: "https://img.hdbits.org/example",
			}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Description, "https://t.hdbits.org/example.jpg") {
		t.Fatalf("expected provided screenshot in description, got %q", result.Description)
	}
}

func TestDefinitionBuildDescriptionUsesProvidedMenuImages(t *testing.T) {
	result, err := prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "HDB",
		Meta:    api.UploadSubject{},
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			MenuImages: []api.ScreenshotImage{{
				Purpose: api.ScreenshotPurposeMenu,
				ImgURL:  "https://t.hdbits.org/menu.jpg",
				WebURL:  "https://img.hdbits.org/menu",
			}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Description, "https://t.hdbits.org/menu.jpg") {
		t.Fatalf("expected provided menu image in description, got %q", result.Description)
	}
}

func TestProfileReleaseNamePolicyOmitsOnlyGeneratedEpisodeTitles(t *testing.T) {
	t.Parallel()

	binding := Profile().ReleaseNamePolicy
	if binding.ID != "standalone/hdb/v3" {
		t.Fatalf("HDB release-name policy = %q, want standalone/hdb/v3", binding.ID)
	}
	elementPolicy := binding.Elements.Normalized()
	if elementPolicy.Version != api.ReleaseNameElementPolicyVersionV1 ||
		elementPolicy.EpisodeTitleMode != api.EpisodeTitleModeOmit {
		t.Fatalf("HDB element policy = %#v", elementPolicy)
	}

	const included = "Example.Show.S01E02.Example.Episode.1080p.WEB-DL-GRP"
	const omitted = "Example.Show.S01E02.1080p.WEB-DL-GRP"
	const sourcePath = "Example.Show.S01E02.1080p.WEB-DL-GRP"
	base := api.UploadSubject{
		SourcePath:  sourcePath,
		ReleaseName: included,
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Category:   api.CanonicalCategoryTV,
			IMDBID:     1234567,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			IMDB: &api.IMDBMetadata{
				IMDBID: 1234567,
				Title:  "Example Show",
				AKA:    "Example Show",
			},
		},
		Release:    api.ReleaseInfo{Resolution: "1080p"},
		Source:     "WEB-DL",
		VideoCodec: "H.264",
		Tag:        "-GRP",
		SeasonStr:  "S01",
		EpisodeStr: "E02",
		GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
			IncludeEpisodeTitle: api.ReleaseNameVariant{Name: included},
			OmitEpisodeTitle:    api.ReleaseNameVariant{Name: omitted},
		},
	}
	resolved, failure := trackers.PrepareInputWithReleaseNamePolicy(
		trackers.PreparationInput{Tracker: "HDB", Meta: base},
		binding,
	)
	if failure != nil {
		t.Fatalf("resolve generated name: %v", failure)
	}
	name, err := resolved.ReviewedUploadName()
	const wantGenerated = "Example Show S01E02 1080p WEB-DL H.264-GRP"
	if err != nil || name != wantGenerated {
		t.Fatalf("generated reviewed name = (%q, %v), want %q", name, err, wantGenerated)
	}

	base.SceneName = included
	resolved, failure = trackers.PrepareInputWithReleaseNamePolicy(
		trackers.PreparationInput{Tracker: "HDB", Meta: base},
		binding,
	)
	if failure != nil {
		t.Fatalf("resolve exact scene name: %v", failure)
	}
	name, err = resolved.ReviewedUploadName()
	if err != nil || name != included {
		t.Fatalf("exact scene reviewed name = (%q, %v), want %q", name, err, included)
	}
}

func TestDefinitionUploadMissingCredentials(t *testing.T) {
	d := New()
	_, err := d.submit(context.Background(), trackers.PreparationInput{Tracker: "HDB", Logger: api.NopLogger{}})
	if err == nil {
		t.Fatal("expected missing credentials error")
	}
}

func TestDefinitionUploadSuccess(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	dbPath := filepath.Join(tmp, "ua.db")
	cookieDir := filepath.Join(tmp, "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	cookieText := "# Netscape HTTP Cookie File\n.hdbits.org\tTRUE\t/\tTRUE\t0\tuid\tcookievalue\n"
	if err := os.WriteFile(filepath.Join(cookieDir, "HDB.txt"), []byte(cookieText), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}

	uploadSeen := false
	downloadSeen := false
	uploadedName := ""
	uploadedTorrentName := ""
	registeredTorrent := hdbRegisteredTorrentFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/upload/upload":
			uploadSeen = true
			if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "multipart/form-data") {
				t.Errorf("expected multipart content-type, got %q", ct)
				return
			}
			if err := r.ParseMultipartForm(5 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
				return
			}
			uploadedName = r.FormValue("name")
			if uploadedName == "" {
				t.Error("expected upload name field")
				return
			}
			if descr := r.FormValue("descr"); !strings.Contains(descr, "https://t.hdbits.org/rehosted.jpg") {
				t.Errorf("expected provided rehosted screenshot in description, got %q", descr)
				return
			}
			files := r.MultipartForm.File["file"]
			if len(files) == 0 {
				t.Error("expected torrent file in multipart form")
				return
			}
			uploadedTorrentName = files[0].Filename
			f, err := files[0].Open()
			if err != nil {
				t.Errorf("open uploaded file: %v", err)
				return
			}
			_, _ = io.ReadAll(f)
			_ = f.Close()
			http.Redirect(w, r, "/details.php?id=123&uploaded=1", http.StatusFound)
		case r.URL.Path == "/details.php":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case strings.HasPrefix(r.URL.Path, "/download.php/"):
			downloadSeen = true
			if r.URL.Query().Get("id") != "123" {
				t.Errorf("expected id=123, got %q", r.URL.Query().Get("id"))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(registeredTorrent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	d := &Definition{baseURL: server.URL, httpClient: server.Client()}
	result, err := d.submit(context.Background(), trackers.PreparationInput{
		Tracker: "HDB",
		Meta: api.UploadSubject{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			Identity: api.ExternalIdentity{
				SourcePath: filepath.Join(tmp, "Movie.mkv"),
				Category:   api.CanonicalCategoryTV,
				IMDBID:     1234567,
				TVDBID:     987650001,
			},
			ProviderMetadata: api.SourceScopedMetadata{
				SourcePath: filepath.Join(tmp, "Movie.mkv"),
				IMDB: &api.IMDBMetadata{
					IMDBID: 1234567,
					Title:  "Example Show",
					AKA:    "Example Show",
				},
			},
			Type:        "WEBDL",
			VideoCodec:  "HEVC",
			SeasonStr:   "S01",
			EpisodeStr:  "E02",
			Release:     api.ReleaseInfo{Resolution: "2160p"},
			ReleaseName: "Example.Show.S01E02.Example.Episode.2160p.WEBDL.HEVC",
			GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
				IncludeEpisodeTitle: api.ReleaseNameVariant{
					Name: "Example.Show.S01E02.Example.Episode.2160p.WEBDL.HEVC",
				},
				OmitEpisodeTitle: api.ReleaseNameVariant{
					Name: "Example.Show.S01E02.2160p.WEBDL.HEVC",
				},
			},
		},
		TrackerConfig: config.TrackerConfig{
			Username: "user",
			Passkey:  "pass",
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: "kept description",
			Screenshots: []api.ScreenshotImage{{
				ImgURL: "https://t.hdbits.org/rehosted.jpg",
				WebURL: "https://img.hdbits.org/rehosted",
				RawURL: "https://img.hdbits.org/rehosted",
			}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 {
		t.Fatalf("expected uploaded=1, got %d", result.Uploaded)
	}
	if len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected one uploaded torrent artifact, got %d", len(result.UploadedTorrents))
	}
	if result.UploadedTorrents[0].Tracker != "HDB" {
		t.Fatalf("expected tracker HDB, got %q", result.UploadedTorrents[0].Tracker)
	}
	if result.UploadedTorrents[0].TorrentID != "123" {
		t.Fatalf("expected torrent id 123, got %q", result.UploadedTorrents[0].TorrentID)
	}
	if result.UploadedTorrents[0].TorrentURL != server.URL+"/details.php?id=123&uploaded=1" {
		t.Fatal("expected direct HDB torrent page URL")
	}
	if !uploadSeen {
		t.Fatal("expected upload endpoint to be called")
	}
	if uploadedName != "Example Show S01E02 2160p WEB-DL HEVC" {
		t.Fatalf("uploaded name = %q, want structured generated name", uploadedName)
	}
	if uploadedTorrentName != "Example.Show.S01E02.2160p.WEB-DL.HEVC.torrent" {
		t.Fatalf("uploaded torrent name = %q, want dotted release name", uploadedTorrentName)
	}
	if !downloadSeen {
		t.Fatal("expected download endpoint to be called")
	}
	artifactPath := result.UploadedTorrents[0].TorrentPath
	if strings.TrimSpace(artifactPath) == "" {
		t.Fatal("expected tracker-specific torrent path")
	}
	updated, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read updated torrent: %v", err)
	}
	if !bytes.Equal(updated, registeredTorrent) {
		t.Fatal("downloaded torrent bytes differ from tracker response")
	}
	original, err := os.ReadFile(torrentPath)
	if err != nil {
		t.Fatalf("read original torrent: %v", err)
	}
	if string(original) != "dummy" {
		t.Fatalf("expected original torrent to remain unchanged, got %q", string(original))
	}
}

func hdbRegisteredTorrentFixture(t *testing.T) []byte {
	t.Helper()
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name:        "Example.Release.2026.mkv",
		PieceLength: 16 * 1024,
		Pieces:      make([]byte, 20),
		Length:      1,
	})
	if err != nil {
		t.Fatalf("marshal registered torrent info: %v", err)
	}
	var payload bytes.Buffer
	if err := (&metainfo.MetaInfo{
		Announce:  "https://tracker.invalid/announce",
		InfoBytes: infoBytes,
	}).Write(&payload); err != nil {
		t.Fatalf("write registered torrent fixture: %v", err)
	}
	return payload.Bytes()
}

func TestDefinitionBuildUploadDryRunUsesProvidedAssets(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	input := trackers.PreparationInput{
		Intent:  trackers.PreparationIntentDryRun,
		Tracker: "HDB",
		Meta: api.UploadSubject{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			Identity:    api.ExternalIdentity{Category: "MOVIE"},
			Type:        "WEBDL",
			VideoCodec:  "HEVC",
			ReleaseName: "My.Release.2026.2160p.WEBDL.HEVC",
		},
		TrackerConfig: config.TrackerConfig{
			Username: "user",
			Passkey:  "pass",
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: "kept description",
			Screenshots: []api.ScreenshotImage{{
				ImgURL: "https://t.hdbits.org/dryrun.jpg",
				WebURL: "https://img.hdbits.org/dryrun",
				RawURL: "https://img.hdbits.org/dryrun",
			}},
		},
	}
	plan, failure := New().Prepare(context.Background(), input)
	if failure != nil {
		t.Fatalf("unexpected dry-run error: %v", failure)
	}
	entry := plan.DryRun()
	if !strings.Contains(entry.Description, "https://t.hdbits.org/dryrun.jpg") {
		t.Fatalf("expected provided screenshot in dry-run description, got %q", entry.Description)
	}
}

func TestBuildUploadFieldsSkipsTVDBForMovie(t *testing.T) {
	fields := buildUploadFields(api.UploadSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE", TVDBID: 765432},
	}, config.Config{}, 1, 5, 6, "description", "Example.Release.2026.1080p-GRP")

	if _, ok := fields["tvdb"]; ok {
		t.Fatalf("did not expect tvdb for movie payload")
	}
	if _, ok := fields["tvdb_season"]; ok {
		t.Fatalf("did not expect tvdb_season for movie payload")
	}
	if _, ok := fields["tvdb_episode"]; ok {
		t.Fatalf("did not expect tvdb_episode for movie payload")
	}
}

func TestBuildUploadFieldsIncludesTVDBForTV(t *testing.T) {
	fields := buildUploadFields(api.UploadSubject{
		Identity:   api.ExternalIdentity{Category: "TV", TVDBID: 765432},
		SeasonInt:  2,
		EpisodeInt: 3,
	}, config.Config{}, 2, 5, 6, "description", "Example.Show.S02E03.1080p-GRP")

	if got := fields["tvdb"]; got != "765432" {
		t.Fatalf("expected tvdb=765432, got %q", got)
	}
	if got := fields["tvdb_season"]; got != "2" {
		t.Fatalf("expected tvdb_season=2, got %q", got)
	}
	if got := fields["tvdb_episode"]; got != "3" {
		t.Fatalf("expected tvdb_episode=3, got %q", got)
	}
}
