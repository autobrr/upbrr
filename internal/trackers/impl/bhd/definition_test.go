// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDefinitionBuildUploadDryRunBuildsPayload(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := New().prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "BHD",
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity: api.ExternalIdentity{
				TMDBID:   123,
				IMDBID:   456,
				Category: "TV",
			},
			ReleaseName: "Movie.2025.1080p.WEB-DL.DD+5.1.H264-GRP",
			Release:     api.ReleaseInfo{Resolution: "1080p"},
			Type:        "WEBDL",
			Source:      "WEB",
			Audio:       "DD+ 5.1",
			HDR:         "HDR10+ DV",
			Edition:     "Hybrid Director",
			TVPack:      true,
			SeasonStr:   "S00",
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token", DraftDefault: true},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"BHD": {DraftDefault: true},
				},
			},
		}),
		Logger: api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Payload["category_id"] != "2" {
		t.Fatalf("expected tv category 2, got %q", entry.Payload["category_id"])
	}
	if entry.Payload["type"] != "1080p" {
		t.Fatalf("expected type 1080p, got %q", entry.Payload["type"])
	}
	if entry.Payload["source"] != "WEB" {
		t.Fatalf("expected source WEB, got %q", entry.Payload["source"])
	}
	if entry.Payload["imdb_id"] != "456" {
		t.Fatalf("expected numeric imdb_id, got %q", entry.Payload["imdb_id"])
	}
	if entry.Payload["tmdb_id"] != "123" {
		t.Fatalf("expected numeric tmdb_id, got %q", entry.Payload["tmdb_id"])
	}
	if entry.Payload["live"] != "0" {
		t.Fatalf("expected live=0 for draft default, got %q", entry.Payload["live"])
	}
	if entry.Payload["pack"] != "1" {
		t.Fatalf("expected pack=1, got %q", entry.Payload["pack"])
	}
	if entry.Payload["special"] != "1" {
		t.Fatalf("expected special=1, got %q", entry.Payload["special"])
	}
	if entry.Payload["edition"] != "Director" {
		t.Fatalf("expected title-cased edition, got %q", entry.Payload["edition"])
	}
	if got := entry.Payload["tags"]; !strings.Contains(got, "WEBDL") || !strings.Contains(got, "HDR10+") || !strings.Contains(got, "DV") {
		t.Fatalf("expected BHD tags in payload, got %q", got)
	}
}

func TestDefinitionBuildUploadDryRunPrerequisiteMessagesIncludeAction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		req        trackers.PreparationInput
		wantCause  string
		wantAction string
	}{
		{
			name: "missing api key",
			req: trackers.PreparationInput{
				Tracker: "BHD",
				Meta:    api.UploadSubject{Identity: api.ExternalIdentity{TMDBID: 123}},
				Logger:  api.NopLogger{},
			},
			wantCause:  "missing api_key",
			wantAction: "configure the BHD api_key",
		},
		{
			name: "missing tmdb id",
			req: trackers.PreparationInput{
				Tracker:       "BHD",
				TrackerConfig: config.TrackerConfig{APIKey: "token"},
				Logger:        api.NopLogger{},
			},
			wantCause:  "missing tmdb id",
			wantAction: "refresh metadata or set a TMDB id",
		},
		{
			name: "missing mediainfo text",
			req: trackers.PreparationInput{
				Tracker:       "BHD",
				TrackerConfig: config.TrackerConfig{APIKey: "token"},
				Meta:          api.UploadSubject{Identity: api.ExternalIdentity{TMDBID: 123}},
				Logger:        api.NopLogger{},
			},
			wantCause:  "missing mediainfo text",
			wantAction: "generate or attach MediaInfo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := New().prepareDryRun(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("expected prerequisite error")
			}
			message := err.Error()
			if !strings.Contains(message, tc.wantCause) {
				t.Fatalf("expected cause %q in error, got %q", tc.wantCause, message)
			}
			if !strings.Contains(message, tc.wantAction) {
				t.Fatalf("expected action %q in error, got %q", tc.wantAction, message)
			}
		})
	}
}

func TestDefinitionBuildUploadDryRunRejectsInvalidContainer(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	_, err := New().prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "BHD",
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Movie.avi"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity:          api.ExternalIdentity{TMDBID: 123, Category: "MOVIE"},
			Type:              "REMUX",
			Container:         "avi",
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		Logger:        api.NopLogger{},
	})
	if err == nil || !strings.Contains(err.Error(), "container") {
		t.Fatalf("expected container validation error, got %v", err)
	}
}

func TestResolveTypeRejectsBHD576iResolution(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Release: api.ReleaseInfo{Resolution: "576i"}}
	if got := resolveType(meta); got != "Other" {
		t.Fatalf("expected BHD type Other for 576i, got %q", got)
	}
}

func TestResolveTypeRejectsHDDVDRemuxEvenWithUHD(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Type:   "REMUX",
		Source: "HDDVD",
		UHD:    "UHD",
	}
	if got := resolveType(meta); got != "Other" {
		t.Fatalf("expected BHD type Other for HD-DVD remux, got %q", got)
	}
}

func TestDefinitionBuildUploadDryRunRejectsInvalidSource(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Example.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	_, err := New().prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "BHD",
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Example.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity:          api.ExternalIdentity{TMDBID: 123, Category: "MOVIE"},
			Release:           api.ReleaseInfo{Resolution: "1080p"},
			Type:              "WEBDL",
			Source:            "CAM",
			Container:         "mkv",
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		Logger:        api.NopLogger{},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported source") {
		t.Fatalf("expected unsupported source error, got %v", err)
	}
}

func TestUploadRetriesInvalidIMDb(t *testing.T) {
	tmp := t.TempDir()
	mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nUnique ID : 123"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/upload/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(r.MultipartForm.File["mediainfo"]) != 1 {
			t.Errorf("expected mediainfo file part")
			return
		}
		if filename := r.MultipartForm.File["mediainfo"][0].Filename; filename != "upload" {
			t.Errorf("expected Upload-Assistant-compatible mediainfo filename, got %q", filename)
			return
		}
		if len(r.MultipartForm.File["file"]) != 1 {
			t.Errorf("expected torrent file part")
			return
		}
		if contentType := r.MultipartForm.File["file"][0].Header.Get("Content-Type"); contentType != "application/x-bittorrent" {
			t.Errorf("expected Upload-Assistant-compatible torrent content type, got %q", contentType)
			return
		}
		calls++
		imdbID := r.FormValue("imdb_id")
		tmdbID := r.FormValue("tmdb_id")
		if tmdbID != "123" {
			t.Errorf("expected tmdb_id 123, got %q", tmdbID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			if imdbID != "456" {
				t.Errorf("expected first imdb_id 456, got %q", imdbID)
				return
			}
			_, _ = fmt.Fprint(w, `{"status_code":0,"status_message":"Invalid imdb_id"}`)
			return
		}
		if imdbID != "1" {
			t.Errorf("expected retried imdb_id 1, got %q", imdbID)
			return
		}
		_, _ = fmt.Fprint(w, `{"status_code":1,"status_message":"https://beyond-hd.me/torrent/download/example.7890.torrent"}`)
	}))
	defer server.Close()

	originalBaseURL := bhdBaseURL
	bhdBaseURL = server.URL
	defer func() { bhdBaseURL = originalBaseURL }()

	summary, err := New().submit(context.Background(), trackers.PreparationInput{
		Tracker: "BHD",
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(tmp, "Movie.mkv"),
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Identity: api.ExternalIdentity{
				TMDBID:   123,
				IMDBID:   456,
				Category: "MOVIE",
			},
			ReleaseName: "Movie.2025.1080p.BluRay.DD+5.1.H264-GRP",
			Release:     api.ReleaseInfo{Resolution: "1080p"},
			Type:        "REMUX",
			Source:      "BluRay",
			Audio:       "DD+ 5.1",
		},
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{}),
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 upload attempts, got %d", calls)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected one upload, got %d", summary.Uploaded)
	}
	if len(summary.UploadedTorrents) != 1 || summary.UploadedTorrents[0].TorrentID != "7890" {
		t.Fatalf("unexpected uploaded torrents: %+v", summary.UploadedTorrents)
	}
}

func TestSendUploadRejectsOversizedResponseBody(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "Example.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, strings.Repeat("x", int(bhdUploadResponseMaxBytes)+1))
	}))
	defer server.Close()

	originalBaseURL := bhdBaseURL
	bhdBaseURL = server.URL
	defer func() { bhdBaseURL = originalBaseURL }()

	_, _, err := sendUpload(context.Background(), trackers.PreparationInput{
		Tracker:       "BHD",
		TrackerConfig: config.TrackerConfig{APIKey: "token"},
	}, uploadState{
		torrentPath: torrentPath,
		fields: map[string]string{
			"name":        "Example.Release.2026.1080p-GRP",
			"category_id": "1",
			"type":        "1080p",
			"source":      "WEB",
			"tmdb_id":     "movie/123456",
			"description": "Example description",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}

func TestSendUploadRejectsNonSuccessHTTPStatus(t *testing.T) {
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "Example.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	tests := []struct {
		name       string
		statusCode int
		body       string
		wantDetail string
	}{
		{
			name:       "valid-looking success body",
			statusCode: http.StatusInternalServerError,
			body:       `{"status_code":1,"status_message":"https://beyond-hd.me/torrent/download/example.123456.torrent"}`,
		},
		{
			name:       "ordinary error body",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"message":"upload rejected"}`,
			wantDetail: "upload rejected",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			originalBaseURL := bhdBaseURL
			bhdBaseURL = server.URL
			defer func() { bhdBaseURL = originalBaseURL }()

			_, responseBody, err := sendUpload(context.Background(), trackers.PreparationInput{
				Tracker:       "BHD",
				TrackerConfig: config.TrackerConfig{APIKey: "token"},
			}, uploadState{
				torrentPath: torrentPath,
				fields: map[string]string{
					"name":        "Example.Release.2026.1080p-GRP",
					"category_id": "1",
					"type":        "1080p",
					"source":      "WEB",
					"tmdb_id":     "movie/123456",
					"description": "Example description",
				},
			})
			if err == nil {
				t.Fatal("expected non-success HTTP status error")
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("status=%d", tt.statusCode)) {
				t.Fatal("expected HTTP status in upload error")
			}
			if tt.wantDetail != "" && !strings.Contains(err.Error(), tt.wantDetail) {
				t.Fatal("expected redacted response detail in upload error")
			}
			if string(responseBody) != tt.body {
				t.Fatal("expected bounded response body to be returned")
			}
		})
	}
}

func TestWriteFailureArtifactPreservesRepeatedFailures(t *testing.T) {
	tmp := t.TempDir()
	req := trackers.PreparationInput{
		Meta: api.UploadSubject{
			SourcePath: filepath.Join(tmp, "Example.Release.2026.1080p-GRP.mkv"),
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "upbrr.db")},
		}),
	}

	firstPayload := []byte(`{"message":"first failure"}`)
	firstPath, err := writeFailureArtifact(req, firstPayload, "upload_failure")
	if err != nil {
		t.Fatalf("write first failure artifact: %v", err)
	}
	secondPayload := []byte(`{"message":"second failure"}`)
	secondPath, err := writeFailureArtifact(req, secondPayload, "upload_failure")
	if err != nil {
		t.Fatalf("write second failure artifact: %v", err)
	}

	firstInfo, err := os.Stat(firstPath)
	if err != nil {
		t.Fatalf("stat first failure artifact: %v", err)
	}
	secondInfo, err := os.Stat(secondPath)
	if err != nil {
		t.Fatalf("stat second failure artifact: %v", err)
	}
	if os.SameFile(firstInfo, secondInfo) {
		t.Fatal("repeated failures must use distinct artifacts")
	}
	firstStored, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatalf("read first failure artifact: %v", err)
	}
	secondStored, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatalf("read second failure artifact: %v", err)
	}
	if string(firstStored) != string(firstPayload) || string(secondStored) != string(secondPayload) {
		t.Fatal("repeated failure artifacts did not preserve both payloads")
	}
}

func TestDefinitionBuildDescriptionUsesProvidedAssets(t *testing.T) {
	result, err := New().prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "BHD",
		Meta: api.UploadSubject{
			Options: api.UploadOptions{Screens: 4},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{}),
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: `[align=right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/align]`,
			Screenshots: []api.ScreenshotImage{{
				RawURL: "https://img.hdbits.org/full.jpg",
				ImgURL: "https://t.hdbits.org/thumb.jpg",
				WebURL: "https://img.hdbits.org/page",
			}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(result.Description, "Uploaded by upbrr") != 1 {
		t.Fatalf("expected single signature, got %q", result.Description)
	}
	if !strings.Contains(result.Description, "[img width=350]https://img.hdbits.org/full.jpg[/img]") {
		t.Fatalf("expected raw image url to be used, got %q", result.Description)
	}
}

func TestBuildScreenshotSectionUsesTwoImagesPerRow(t *testing.T) {
	images := []api.ScreenshotImage{
		{RawURL: "https://img.example/1.png", WebURL: "https://img.example/1"},
		{RawURL: "https://img.example/2.png", WebURL: "https://img.example/2"},
		{RawURL: "https://img.example/3.png", WebURL: "https://img.example/3"},
		{RawURL: "https://img.example/4.png", WebURL: "https://img.example/4"},
	}

	got := buildScreenshotSection(images, len(images))
	want := strings.Join([]string{
		`[align=center][url=https://img.example/1][img width=350]https://img.example/1.png[/img][/url] [url=https://img.example/2][img width=350]https://img.example/2.png[/img][/url]`,
		``,
		`[url=https://img.example/3][img width=350]https://img.example/3.png[/img][/url] [url=https://img.example/4][img width=350]https://img.example/4.png[/img][/url][/align]`,
	}, "\n")
	if got != want {
		t.Fatalf("unexpected BHD screenshot layout: %q", got)
	}
}

func TestDefinitionBuildDescriptionStripsLegacyCreatedFooter(t *testing.T) {
	result, err := New().prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "BHD",
		Meta: api.UploadSubject{
			Options: api.UploadOptions{Screens: 4},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{}),
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: strings.Join([]string{
				"Body",
				`[align=right][url=https://github.com/autobrr/upbrr]Created by upbrr[/url][/align]`,
			}, "\n\n"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Description, "Created by upbrr") {
		t.Fatalf("expected legacy footer stripped, got %q", result.Description)
	}
	if strings.Count(result.Description, "Uploaded by upbrr") != 1 {
		t.Fatalf("expected single current footer, got %q", result.Description)
	}
	if !strings.Contains(result.Description, "Body") {
		t.Fatalf("expected body preserved, got %q", result.Description)
	}
}

func TestDefinitionBuildDescriptionDoesNotRestoreRawImagesOnlyBody(t *testing.T) {
	result, err := New().prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "BHD",
		Meta: api.UploadSubject{
			Options: api.UploadOptions{Screens: 4},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{}),
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: strings.Join([]string{
				`[url=https://example.com/page][img]https://example.com/full.jpg[/img][/url]`,
				`[right]Created by Upload Assistant[/right]`,
			}, "\n\n"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Description, "Upload Assistant") {
		t.Fatalf("expected bot footer stripped, got %q", result.Description)
	}
	if strings.Count(result.Description, "https://example.com/full.jpg") != 1 {
		t.Fatalf("expected one screenshot instance, got %q", result.Description)
	}
	if strings.Count(result.Description, "Uploaded by upbrr") != 1 {
		t.Fatalf("expected single current footer, got %q", result.Description)
	}
}

func TestDefinitionBuildDescriptionStripsRightFormUpbrrFooter(t *testing.T) {
	result, err := New().prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "BHD",
		Meta: api.UploadSubject{
			Options: api.UploadOptions{Screens: 4},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{}),
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: strings.Join([]string{
				"Body",
				`[right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/right]`,
			}, "\n\n"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(result.Description, "Uploaded by upbrr") != 1 {
		t.Fatalf("expected only current BHD footer, got %q", result.Description)
	}
	if strings.Contains(result.Description, "[right]") || strings.Contains(result.Description, "[size=10]upbrr[/size]") {
		t.Fatalf("expected right-form footer stripped, got %q", result.Description)
	}
	if !strings.Contains(result.Description, "Body") {
		t.Fatalf("expected body preserved, got %q", result.Description)
	}
}

func TestDefinitionBuildDescriptionDoesNotRestoreBotOnlyBody(t *testing.T) {
	result, err := New().prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "BHD",
		Meta: api.UploadSubject{
			Options: api.UploadOptions{Screens: 4},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{}),
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: `[right]Created by Upload Assistant[/right]`,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Description, "Upload Assistant") {
		t.Fatalf("expected bot-only body stripped, got %q", result.Description)
	}
	if strings.Count(result.Description, "Uploaded by upbrr") != 1 {
		t.Fatalf("expected single current footer, got %q", result.Description)
	}
}
