// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"

	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func (d *Definition) prepareDryRun(ctx context.Context, input trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	input.Intent = trackers.PreparationIntentDryRun
	plan, failure := trackers.PrepareAdapter(ctx, input, nil, func(ctx context.Context, input trackers.PreparationInput) (trackers.PreparedOperation, error) {
		return prepareUploadAt(ctx, input, d.baseURL)
	})
	if failure != nil {
		return api.TrackerDryRunEntry{}, failure
	}
	return plan.DryRun(), nil
}

func (d *Definition) submit(ctx context.Context, input trackers.PreparationInput) (api.UploadSummary, error) {
	return uploadAt(ctx, input, d.baseURL)
}

func TestDefinitionBuildDescriptionUsesPTPGroup(t *testing.T) {
	result, err := prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "PTP",
		Meta:    api.UploadSubject{},
		Logger:  api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Group != "ptp" {
		t.Fatalf("expected ptp group, got %q", result.Group)
	}
}

func TestDefinitionBuildDescriptionUsesResolvedAssetsAndMediaInfo(t *testing.T) {
	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "mediainfo.txt")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	result, err := prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "PTP",
		Meta: api.UploadSubject{
			MediaInfoTextPath: mediaInfoPath,
			Options:           api.UploadOptions{Screens: 1},
		},
		Assets: &trackers.DescriptionAssets{
			Description: "kept https://pixhost.to/show/encoded.png",
			Screenshots: []api.ScreenshotImage{
				{Host: "pixhost", RawURL: "https://pixhost.to/show/encoded.png"},
				{Host: "pixhost", RawURL: "https://pixhost.to/show/example-2.png"},
				{Host: "pixhost", RawURL: "https://pixhost.to/show/example-3.png"},
			},
		},
		Logger: api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Description, "[mediainfo]General\nUnique ID : 123[/mediainfo]") {
		t.Fatalf("expected mediainfo section, got %q", result.Description)
	}
	if !strings.Contains(result.Description, "kept") || !strings.Contains(result.Description, "[img]https://pixhost.to/show/encoded.png[/img]") {
		t.Fatalf("expected resolved asset description, got %q", result.Description)
	}
	if strings.Contains(result.Description, "lostimg.cc") {
		t.Fatalf("expected no stale image hosts, got %q", result.Description)
	}
}

func TestPTPFreshUploadTaxonomy(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Source:            "Web",
		VideoCodec:        "HEVC",
		HasEncodeSettings: true,
		ReleaseName:       "Example.Release.2026.1440p.WEB-DL.x265.HARDSUB-GRP",
		FileList:          []string{"Example.Release.2026.1440p.WEB-DL.x265.HARDSUB-GRP.mkv"},
		Release: api.ReleaseInfo{
			Resolution: "1440p",
		},
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{Type: "concert"},
			TMDB: &api.TMDBMetadata{Genres: "Science Fiction, Mystery"},
		},
		SubtitleLanguages: []string{"Malay", "Persian", "Welsh"},
	}
	fields, err := buildUploadFields(meta, "description", "123", map[string]string{
		"hardcoded_subtitle_languages": "Malay",
	}, "")
	if err != nil {
		t.Fatalf("build fields: %v", err)
	}
	for key, want := range map[string]string{
		"resolution":              "Other",
		"other_resolution_width":  "2560",
		"other_resolution_height": "1440",
		"type":                    "Live Performance",
		"codec":                   "x265",
		"container":               "MKV",
		"source":                  "WEB",
		"subtitles[]":             "54,52,55",
		"trumpable[]":             "4",
	} {
		if got := fields[key]; got != want {
			t.Fatalf("field %s=%q, want %q", key, got, want)
		}
	}
	if got := resolveTags(meta); got != "sci.fi, mystery" {
		t.Fatalf("tags=%q", got)
	}
	meta.ReleaseName = "Example.Release.2026.1080p.WEB-DL.x265.HARDSUB.FORCED-GRP"
	if got := resolveTrumpable(meta); len(got) != 1 || got[0] != 4 {
		t.Fatalf("forced hardcoded trumpable=%#v", got)
	}
	meta.ReleaseName = "Example.Release.2026.1080p.WEB-DL.x265-GRP"
	meta.FileList = nil
	meta.AudioLanguages = []string{"Japanese"}
	meta.SubtitleLanguages = []string{"French"}
	if got := resolveTrumpable(meta); len(got) != 1 || got[0] != 14 {
		t.Fatalf("no-English trumpable=%#v", got)
	}
	meta.ReleaseName = "Example.Release.2026.1080p.WEB-DL.x265.HARDSUB-GRP"
	if got := resolveTrumpable(meta); len(got) != 2 || got[0] != 4 || got[1] != 14 {
		t.Fatalf("hardcoded no-English trumpable=%#v", got)
	}
	meta.DiscType = "BDMV"
	meta.SourceSize = 50 << 30
	if got := resolveCodec(meta); got != "BD66" {
		t.Fatalf("50 GiB disc codec=%q", got)
	}
	if _, err := buildUploadFields(api.UploadSubject{
		Release: api.ReleaseInfo{Resolution: "Custom"},
	}, "description", "123", nil, ""); err == nil {
		t.Fatal("expected unresolved custom resolution to fail upload construction")
	}
}

func TestPTPHardcodedSubtitleQuestionnaire(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{ReleaseName: "Example.Release.2026.1080p.WEB-DL.x265.HARDSUB-GRP"}
	questionnaire := buildQuestionnaire(meta, "123")
	if questionnaire == nil || len(questionnaire.Fields) != 1 || questionnaire.Fields[0].Key != "hardcoded_subtitle_languages" {
		t.Fatalf("questionnaire=%#v", questionnaire)
	}
	if _, err := buildUploadFields(meta, "description", "123", nil, ""); err == nil {
		t.Fatal("expected missing hardcoded subtitle languages to fail")
	}
	fields, err := buildUploadFields(meta, "description", "123", map[string]string{
		"hardcoded_subtitle_languages": "English - Forced",
	}, "")
	if err != nil {
		t.Fatalf("build hardcoded fields: %v", err)
	}
	if fields["subtitles[]"] != "50" || fields["trumpable[]"] != "4" {
		t.Fatalf("hardcoded fields=%#v", fields)
	}
}

func TestDefinitionBuildDescriptionUsesAllResolvedPixhostScreenshots(t *testing.T) {
	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "mediainfo.txt")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	screenshots := []api.ScreenshotImage{
		{Host: "pixhost", RawURL: "https://pixhost.to/1.png"},
		{Host: "pixhost", RawURL: "https://pixhost.to/2.png"},
		{Host: "pixhost", RawURL: "https://pixhost.to/3.png"},
	}
	result, err := prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "PTP",
		Meta: api.UploadSubject{
			MediaInfoTextPath: mediaInfoPath,
			Options:           api.UploadOptions{Screens: 2},
		},
		Assets: &trackers.DescriptionAssets{Screenshots: screenshots},
		Logger: api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, screenshot := range screenshots {
		if !strings.Contains(result.Description, "[img]"+screenshot.RawURL+"[/img]") {
			t.Fatalf("expected screenshot %q in description, got %q", screenshot.RawURL, result.Description)
		}
	}
}

func TestDefinitionBuildDescriptionUsesAllowedNonPixhostRawScreenshots(t *testing.T) {
	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "mediainfo.txt")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	screenshots := []api.ScreenshotImage{
		{
			Host:   "imgbb",
			RawURL: "https://i.ibb.co/raw-1/source.png",
			ImgURL: "https://i.ibb.co/thumb-1/source.png",
		},
		{
			Host:   "onlyimage",
			RawURL: "https://onlyimage.org/images/raw-2.png",
			ImgURL: "https://onlyimage.org/images/medium-2.png",
		},
		{
			Host:   "ptscreens",
			RawURL: "https://ptscreens.com/images/raw-3.png",
			ImgURL: "https://ptscreens.com/images/medium-3.png",
		},
	}
	result, err := prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "PTP",
		Meta: api.UploadSubject{
			MediaInfoTextPath: mediaInfoPath,
			Options:           api.UploadOptions{Screens: 2},
		},
		Assets: &trackers.DescriptionAssets{Screenshots: screenshots},
		Logger: api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, screenshot := range screenshots {
		if !strings.Contains(result.Description, "[img]"+screenshot.RawURL+"[/img]") {
			t.Fatalf("expected raw screenshot %q in description, got %q", screenshot.RawURL, result.Description)
		}
		if strings.Contains(result.Description, screenshot.ImgURL) {
			t.Fatalf("expected PTP description to avoid non-raw URL %q, got %q", screenshot.ImgURL, result.Description)
		}
	}
}

func TestDefinitionBuildUploadDryRunForExistingGroup(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	createTestTorrent(t, filepath.Join(tmp, "source.bin"), torrentPath)

	requestErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case ptpTorrentPath:
			if r.URL.Query().Get("imdb") != "0000456" || r.URL.Query().Get("json") != "noredirect" {
				requestErr <- errors.New("unexpected PTP IMDb group query")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Movies": []map[string]any{{"GroupId": "1234"}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	entry, err := (&Definition{baseURL: server.URL}).prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "PTP",
		Meta: api.UploadSubject{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			ReleaseName: "Movie.2026.1080p.BluRay.x264",
			Source:      "BluRay",
			VideoCodec:  "AVC",
			Identity:    api.ExternalIdentity{Category: "MOVIE", IMDBID: 456},
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{
					Title:  "Movie",
					Year:   2026,
					Poster: "https://img.example/poster.jpg",
					Genres: "Action",
				},
			},
		},
		TrackerConfig: config.TrackerConfig{
			PTPAPIUser: "user",
			PTPAPIKey:  "key",
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:  api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected dry-run error: %v", err)
	}
	select {
	case err := <-requestErr:
		t.Fatal(err)
	default:
	}
	if got := entry.Payload["groupid"]; got != "1234" {
		t.Fatalf("expected existing group id, got %q", got)
	}
	if got := entry.Payload["imdb"]; got != "0000456" {
		t.Fatalf("expected padded imdb, got %q", got)
	}
	if _, exists := entry.Payload["title"]; exists {
		t.Fatal("did not expect new-group title field when group already exists")
	}
	if entry.Questionnaire != nil {
		t.Fatal("did not expect questionnaire for existing group upload")
	}
}

func TestDefinitionBuildUploadDryRunForNewGroupIncludesQuestionnaire(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	createTestTorrent(t, filepath.Join(tmp, "source.bin"), torrentPath)

	entry, err := New().prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "PTP",
		Meta: api.UploadSubject{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			ReleaseName: "Movie.2026.1080p.BluRay.x264",
			Source:      "BluRay",
			VideoCodec:  "AVC",
			Identity:    api.ExternalIdentity{Category: "MOVIE"},
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{
					Title:    "Movie",
					Year:     2026,
					Poster:   "https://img.example/poster.jpg",
					Genres:   "Action",
					Overview: "Plot",
				},
			},
		},
		TrackerConfig: config.TrackerConfig{},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected dry-run error: %v", err)
	}
	if entry.Questionnaire == nil {
		t.Fatal("expected questionnaire for new group upload")
	}
	if got := len(entry.Questionnaire.Fields); got == 0 {
		t.Fatal("expected questionnaire fields")
	}
	if entry.Questionnaire.Fields[0].Key != "title" {
		t.Fatalf("expected first questionnaire field to be title, got %q", entry.Questionnaire.Fields[0].Key)
	}
	if got := entry.Payload["imdb"]; got != "0" {
		t.Fatalf("expected missing IMDb dry-run payload value 0, got %q", got)
	}
}

func TestDefinitionUploadRejectsMissingAnnounceBeforeRequest(t *testing.T) {
	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("save PTP cookies: %v", err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	createTestTorrent(t, filepath.Join(tmp, "source.bin"), torrentPath)
	_, err := (&Definition{baseURL: server.URL}).submit(ctx, trackers.PreparationInput{
		Tracker: "PTP",
		Meta: api.UploadSubject{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			ReleaseName: "Movie.2026.1080p.BluRay.x264",
			Source:      "BluRay",
			VideoCodec:  "AVC",
			Identity:    api.ExternalIdentity{Category: "MOVIE", IMDBID: 1234567},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		Logger:  api.NopLogger{},
	})
	if err == nil || !strings.Contains(err.Error(), "required announce URL is missing") {
		t.Fatalf("missing announce error = %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("missing announce triggered %d PTP request(s)", got)
	}
}

func TestDefinitionUploadSuccess(t *testing.T) {
	tmp := t.TempDir()
	dbPath := newPTPAuthDB(t)
	baseTorrentPath := filepath.Join(tmp, "release.torrent")
	createTestTorrent(t, filepath.Join(tmp, "source.bin"), baseTorrentPath)
	markTorrentWithPrivateMetadata(t, baseTorrentPath)
	announceURL := "https://please.passthepopcorn.me/passkey/announce"
	meta := api.UploadSubject{
		SourcePath:  filepath.Join(tmp, "Movie.mkv"),
		ReleaseName: "Movie.2026.1080p.BluRay.x264",
		Source:      "BluRay",
		VideoCodec:  "AVC",
		Identity:    api.ExternalIdentity{Category: "MOVIE", IMDBID: 1234567},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				Title:    "Movie",
				Year:     2026,
				Poster:   "https://img.example/poster.jpg",
				Genres:   "Action",
				Overview: "Plot",
			},
		},
	}
	torrentPath, err := trackers.ResolveTrackerTorrentArtifactPath(meta, dbPath, "PTP")
	if err != nil {
		t.Fatalf("resolve PTP torrent artifact: %v", err)
	}
	meta.TorrentPath = torrentPath
	if err := trackers.WritePersonalizedTorrent(baseTorrentPath, torrentPath, announceURL, "PTP"); err != nil {
		t.Fatalf("prepare PTP torrent artifact: %v", err)
	}
	preparedMeta, err := metainfo.LoadFromFile(torrentPath)
	if err != nil {
		t.Fatalf("load prepared PTP torrent: %v", err)
	}
	preparedInfoHash := preparedMeta.HashInfoBytes().String()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RequestURI() == ptpLoginPath:
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse login: %v", err)
				return
			}
			if r.FormValue("username") != "user" || r.FormValue("password") != "pass" {
				t.Error("unexpected login credentials")
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name:  "session",
				Value: "cookievalue",
				Path:  "/",
			})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Result":        "Ok",
				"AntiCsrfToken": "csrf-token",
			})
		case r.Method == http.MethodGet && r.URL.Path == ptpUploadPath:
			_, _ = w.Write([]byte(`<div data-AntiCsrfToken="csrf-token"></div>`))
		default:
			switch r.URL.Path {
			case ptpUploadPath:
				if err := r.ParseMultipartForm(5 << 20); err != nil {
					t.Errorf("parse multipart: %v", err)
					return
				}
				files := r.MultipartForm.File["file_input"]
				if len(files) != 1 {
					t.Errorf("expected one torrent file, got %d", len(files))
					return
				}
				uploaded, err := files[0].Open()
				if err != nil {
					t.Errorf("open uploaded torrent: %v", err)
					return
				}
				defer uploaded.Close()
				payload, err := io.ReadAll(uploaded)
				if err != nil {
					t.Errorf("read uploaded torrent: %v", err)
					return
				}
				uploadedMeta, err := metainfo.Load(bytes.NewReader(payload))
				if err != nil {
					t.Errorf("load uploaded torrent: %v", err)
					return
				}
				if uploadedMeta.Comment != "uploaded with upbrr" {
					t.Errorf("expected cleaned upload torrent comment, got %q", uploadedMeta.Comment)
					return
				}
				if uploadedMeta.CreatedBy != "upbrr with mkbrr" {
					t.Errorf("expected cleaned upload torrent created-by, got %q", uploadedMeta.CreatedBy)
					return
				}
				if uploadedMeta.Announce != announceURL {
					t.Error("expected prepared tracker announce")
					return
				}
				info, err := uploadedMeta.UnmarshalInfo()
				if err != nil || info.Source != "PTP" {
					t.Error("expected prepared PTP source")
					return
				}
				if r.FormValue("AntiCsrfToken") != "csrf-token" {
					t.Error("expected csrf token")
					return
				}
				if r.FormValue("title") != "Movie" {
					t.Error("expected new-group title field")
					return
				}
				http.Redirect(w, r, "/torrents.php?id=555&torrentid=666", http.StatusFound)
			case ptpTorrentPath:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}
	}))
	defer server.Close()

	result, err := (&Definition{baseURL: server.URL}).submit(context.Background(), trackers.PreparationInput{
		Tracker: "PTP",
		Meta:    meta,
		TrackerConfig: config.TrackerConfig{
			Username:    "user",
			Password:    "pass",
			AnnounceURL: announceURL,
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		Logger:  api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 {
		t.Fatalf("expected uploaded=1, got %d", result.Uploaded)
	}
	if len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected uploaded torrent artifact")
	}
	if result.UploadedTorrents[0].TorrentID != "666" {
		t.Fatalf("expected torrent id 666, got %q", result.UploadedTorrents[0].TorrentID)
	}
	artifactPath := result.UploadedTorrents[0].TorrentPath
	if artifactPath != torrentPath {
		t.Fatalf("expected finalized prepared artifact %q, got %q", torrentPath, artifactPath)
	}
	registeredMeta, err := metainfo.LoadFromFile(artifactPath)
	if err != nil {
		t.Fatalf("load registered PTP torrent: %v", err)
	}
	if registeredMeta.HashInfoBytes().String() != preparedInfoHash {
		t.Fatal("expected PTP finalization to preserve infohash")
	}
	if registeredMeta.Announce != announceURL || len(registeredMeta.AnnounceList) != 1 ||
		len(registeredMeta.AnnounceList[0]) != 1 || registeredMeta.AnnounceList[0][0] != announceURL {
		t.Fatal("expected registered PTP announce and announce-list")
	}
	registeredInfo, err := registeredMeta.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal registered PTP torrent: %v", err)
	}
	if registeredInfo.Source != "PTP" {
		t.Fatalf("expected registered PTP source, got %q", registeredInfo.Source)
	}
	if registeredMeta.Comment != "uploaded with upbrr" {
		t.Fatalf("expected upbrr comment, got %q", registeredMeta.Comment)
	}
	expectedBase := filepath.Base(meta.SourcePath)
	expectedName := "[ptp]." + expectedBase + ".torrent"
	if got := filepath.Base(torrentPath); got != expectedName {
		t.Fatalf("expected canonical PTP torrent %q, got %q", expectedName, got)
	}
	legacyPath := filepath.Join(filepath.Dir(torrentPath), expectedBase+".ptp.torrent")
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no duplicate PTP torrent at %q, got %v", legacyPath, err)
	}
	legacyCookiePath, err := servicedb.CookiePath(dbPath, ptpCookieFile)
	if err != nil {
		t.Fatalf("resolve legacy PTP cookie path: %v", err)
	}
	if _, err := os.Stat(legacyCookiePath); !os.IsNotExist(err) {
		t.Fatalf("expected no legacy PTP cookie file after upload, got err=%v", err)
	}
}

func TestLoginAndFetchAntiCsrfTokenHandles2FA(t *testing.T) {
	secondLogin := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == ptpUploadPath {
			_, _ = w.Write([]byte(`<div data-AntiCsrfToken="csrf-token"></div>`))
			return
		}
		if r.URL.RequestURI() != ptpLoginPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse login: %v", err)
			return
		}
		if r.FormValue("username") != "user" || r.FormValue("password") != "pass" || r.FormValue("passkey") != "passkey" || r.FormValue("keeplogged") != "1" {
			t.Error("unexpected login form")
			return
		}
		if r.FormValue("TfaCode") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"Result": "TfaRequired"})
			return
		}
		secondLogin = true
		if r.FormValue("TfaType") != "normal" {
			t.Errorf("expected TfaType normal, got %q", r.FormValue("TfaType"))
			return
		}
		code := r.FormValue("TfaCode")
		if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
			t.Errorf("expected six digit TfaCode, got %q", code)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:  "session",
			Value: "cookievalue",
			Path:  "/",
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Result":        "Ok",
			"AntiCsrfToken": "csrf-token",
		})
	}))
	defer server.Close()

	dbPath := newPTPAuthDB(t)
	client, token, err := loginAndFetchAntiCsrfToken(context.Background(), config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
		OTPURI:      "otpauth://totp/PTP:user?secret=JBSWY3DPEHPK3PXP&issuer=PTP",
	}, dbPath, server.URL, api.NopLogger{}, api.TrackerAuthLoginRequest{})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if token != "csrf-token" {
		t.Fatalf("expected csrf token, got %q", token)
	}
	if client == nil {
		t.Fatal("expected authenticated client")
	}
	if !secondLogin {
		t.Fatal("expected second 2FA login request")
	}
}

func TestRehostPosterToSelectedHostUploadsPoster(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "ua.db")
	sourcePath := filepath.Join(tmp, "Movie.mkv")
	if err := os.WriteFile(sourcePath, []byte("movie"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	posterServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("poster-bytes"))
	}))
	defer posterServer.Close()
	originalClientFactory := newPosterHTTPClient
	newPosterHTTPClient = posterServer.Client
	t.Cleanup(func() {
		newPosterHTTPClient = originalClientFactory
	})

	images := &stubPTPImageHosting{
		uploaded: []api.UploadedImageLink{{
			Host:   "pixhost",
			RawURL: "https://pixhost.to/rehosted.png",
		}},
	}
	got := rehostPosterToSelectedHost(context.Background(), trackers.PreparationInput{
		Meta: api.UploadSubject{
			SourcePath: sourcePath,
		},
		Runtime:           trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		Logger:            api.NopLogger{},
		SelectedImageHost: "pixhost",
		UploadImages: func(ctx context.Context, uploaded []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			return images.Upload(ctx, api.ImageHostingSubject{}, "pixhost", "global", uploaded)
		},
	}, posterServer.URL+"/poster")

	if got != "https://pixhost.to/rehosted.png" {
		t.Fatalf("expected rehosted poster URL, got %q", got)
	}
	if images.host != "pixhost" {
		t.Fatalf("expected pixhost upload host, got %q", images.host)
	}
	if len(images.images) != 1 {
		t.Fatalf("expected one uploaded poster, got %d", len(images.images))
	}
	posterPath := images.images[0].Path
	if filepath.Base(posterPath) != "PTP_POSTER.png" {
		t.Fatalf("expected content-type poster extension, got %q", filepath.Base(posterPath))
	}
	body, err := os.ReadFile(posterPath)
	if err != nil {
		t.Fatalf("read poster: %v", err)
	}
	if string(body) != "poster-bytes" {
		t.Fatalf("unexpected poster body %q", string(body))
	}
}

func TestRehostPosterToSelectedHostRejectsLoopbackPoster(t *testing.T) {
	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Movie.mkv")
	if err := os.WriteFile(sourcePath, []byte("movie"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	posterServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("poster-bytes"))
	}))
	defer posterServer.Close()

	images := &stubPTPImageHosting{
		uploaded: []api.UploadedImageLink{{
			Host:   "pixhost",
			RawURL: "https://pixhost.to/rehosted.png",
		}},
	}
	got := rehostPosterToSelectedHost(context.Background(), trackers.PreparationInput{
		Meta: api.UploadSubject{
			SourcePath: sourcePath,
		},
		Runtime:           trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:            api.NopLogger{},
		SelectedImageHost: "pixhost",
		UploadImages: func(ctx context.Context, uploaded []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			return images.Upload(ctx, api.ImageHostingSubject{}, "pixhost", "global", uploaded)
		},
	}, posterServer.URL+"/poster")

	if got != posterServer.URL+"/poster" {
		t.Fatalf("expected original poster URL after blocked rehost, got %q", got)
	}
	if len(images.images) != 0 {
		t.Fatalf("expected no upload for blocked loopback poster")
	}
}

func TestIsPublicPosterIPRejectsPrivateRanges(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.20", "169.254.1.1", "::1", "fc00::1"} {
		if isPublicPosterIP(netip.MustParseAddr(value)) {
			t.Fatalf("expected %s to be rejected", value)
		}
	}
	if !isPublicPosterIP(netip.MustParseAddr("93.184.216.34")) {
		t.Fatal("expected public address to be accepted")
	}
}

func TestRehostPosterToSelectedHostSkipsSelectedHost(t *testing.T) {
	images := &stubPTPImageHosting{}
	got := rehostPosterToSelectedHost(context.Background(), trackers.PreparationInput{
		SelectedImageHost: "pixhost",
		UploadImages: func(ctx context.Context, uploaded []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
			return images.Upload(ctx, api.ImageHostingSubject{}, "pixhost", "global", uploaded)
		},
	}, "https://pixhost.to/existing.jpg")
	if got != "https://pixhost.to/existing.jpg" {
		t.Fatalf("expected original poster URL, got %q", got)
	}
	if len(images.images) != 0 {
		t.Fatalf("expected no upload for selected host poster")
	}
}

type stubPTPImageHosting struct {
	host     string
	scope    string
	images   []api.ScreenshotImage
	uploaded []api.UploadedImageLink
}

func (s *stubPTPImageHosting) ListCandidates(context.Context, api.ImageHostingSubject) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (s *stubPTPImageHosting) Upload(_ context.Context, _ api.ImageHostingSubject, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	s.host = host
	s.scope = usageScope
	s.images = append([]api.ScreenshotImage(nil), images...)
	return s.uploaded, nil
}

func createTestTorrent(t *testing.T, sourcePath string, torrentPath string) {
	t.Helper()

	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	_, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:       sourcePath,
		OutputPath: torrentPath,
		IsPrivate:  true,
	})
	if err != nil {
		t.Fatalf("create torrent: %v", err)
	}
}

func markTorrentWithPrivateMetadata(t *testing.T, torrentPath string) {
	t.Helper()

	torrentMeta, err := metainfo.LoadFromFile(torrentPath)
	if err != nil {
		t.Fatalf("load torrent: %v", err)
	}
	torrentMeta.Announce = "https://private.example/passkey/announce"
	torrentMeta.Comment = "Created by Upload Assistant https://private.example/download/1"
	file, err := os.Create(torrentPath)
	if err != nil {
		t.Fatalf("rewrite torrent: %v", err)
	}
	defer file.Close()
	if err := torrentMeta.Write(file); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
}
