// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package czt

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mkbrr "github.com/autobrr/mkbrr/torrent"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadSuccessPersistsReturnedTorrentAndUsesProvidedAssets(t *testing.T) {
	returnedTorrent := validTorrentBytes(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != uploadPath {
			t.Fatalf("expected upload path %q, got %q", uploadPath, r.URL.Path)
		}
		if err := r.ParseMultipartForm(5 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if got := r.FormValue("user_descr"); !strings.Contains(got, "https://img.example/rehosted.jpg") {
			t.Fatalf("expected provided screenshot URL in payload, got %q", got)
		}
		files := r.MultipartForm.File["file"]
		if len(files) != 1 {
			t.Fatalf("expected one torrent file, got %d", len(files))
		}
		file, err := files[0].Open()
		if err != nil {
			t.Fatalf("open multipart file: %v", err)
		}
		_, _ = io.ReadAll(file)
		_ = file.Close()
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(
			w,
			`{"id":123,"name":"Release","download_url":"download.php?id=123","torrent_b64":%q}`,
			base64.StdEncoding.EncodeToString(returnedTorrent),
		)
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.Uploaded != 1 || len(result.UploadedTorrents) != 1 {
		t.Fatalf("expected one uploaded torrent, got %+v", result)
	}
	uploaded := result.UploadedTorrents[0]
	if uploaded.TorrentURL != server.URL+"/details.php?id=123" {
		t.Fatalf("unexpected torrent URL: %q", uploaded.TorrentURL)
	}
	if uploaded.DownloadURL != server.URL+"/download.php?id=123" {
		t.Fatalf("unexpected download URL: %q", uploaded.DownloadURL)
	}
	if strings.TrimSpace(uploaded.TorrentPath) == "" {
		t.Fatal("expected persisted returned torrent path")
	}
	payload, err := os.ReadFile(uploaded.TorrentPath)
	if err != nil {
		t.Fatalf("read persisted torrent: %v", err)
	}
	if string(payload) != string(returnedTorrent) {
		t.Fatalf("persisted torrent did not match returned bytes")
	}
}

func TestUploadRejectsInvalidReturnedTorrentB64(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":123,"name":"Release","download_url":"/download.php?id=123","torrent_b64":"not-base64"}`))
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err == nil {
		t.Fatal("expected invalid returned torrent error")
	}
	if result.Uploaded != 0 || len(result.UploadedTorrents) != 0 {
		t.Fatalf("expected no uploaded result on persistence failure, got %+v", result)
	}
	if !strings.Contains(err.Error(), "persist returned torrent") {
		t.Fatalf("expected persistence error, got %v", err)
	}
}

func TestUploadRejectsEmptyReturnedTorrentB64(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":123,"name":"Release","download_url":"/download.php?id=123","torrent_b64":" "}`))
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err == nil {
		t.Fatal("expected empty returned torrent error")
	}
	if result.Uploaded != 0 || len(result.UploadedTorrents) != 0 {
		t.Fatalf("expected no uploaded result on empty returned torrent, got %+v", result)
	}
	if !strings.Contains(err.Error(), "empty torrent_b64") {
		t.Fatalf("expected empty torrent_b64 error, got %v", err)
	}
}

func TestUploadRejectsCorruptReturnedTorrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(
			w,
			`{"id":123,"name":"Release","download_url":"/download.php?id=123","torrent_b64":%q}`,
			base64.StdEncoding.EncodeToString([]byte("registered-torrent")),
		)
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err == nil {
		t.Fatal("expected corrupt returned torrent error")
	}
	if result.Uploaded != 0 || len(result.UploadedTorrents) != 0 {
		t.Fatalf("expected no uploaded result on corrupt returned torrent, got %+v", result)
	}
	if !strings.Contains(err.Error(), "returned torrent") {
		t.Fatalf("expected returned torrent validation error, got %v", err)
	}
}

func TestUploadRejectsOffsiteDownloadURL(t *testing.T) {
	returnedTorrent := validTorrentBytes(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(
			w,
			`{"id":123,"name":"Release","download_url":"https://evil.example/download.php?id=123","torrent_b64":%q}`,
			base64.StdEncoding.EncodeToString(returnedTorrent),
		)
	}))
	defer server.Close()

	result, err := upload(context.Background(), cztUploadRequest(t, server.URL))
	if err == nil {
		t.Fatal("expected offsite download URL error")
	}
	if result.Uploaded != 0 || len(result.UploadedTorrents) != 0 {
		t.Fatalf("expected no uploaded result on offsite download URL, got %+v", result)
	}
	if !strings.Contains(err.Error(), "download_url") {
		t.Fatalf("expected download_url error, got %v", err)
	}
}

func TestUploadResponseParseBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{
			name:       "malformed 201",
			statusCode: http.StatusCreated,
			body:       `{`,
			want:       "parse upload response",
		},
		{
			name:       "partial positive id with bad field type",
			statusCode: http.StatusCreated,
			body:       `{"id":123,"download_url":"/download.php?id=123","torrent_b64":123}`,
			want:       "parse upload response",
		},
		{
			name:       "malformed non-201 stays http failure",
			statusCode: http.StatusInternalServerError,
			body:       `{`,
			want:       "status=500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			_, err := upload(context.Background(), cztUploadRequest(t, server.URL))
			if err == nil {
				t.Fatal("expected upload error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestJoinCZTURL(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "leading slash", base: "https://czteam.me", raw: "/download.php?id=1", want: "https://czteam.me/download.php?id=1"},
		{name: "no leading slash", base: "https://czteam.me", raw: "download.php?id=1", want: "https://czteam.me/download.php?id=1"},
		{name: "same host absolute", base: "https://czteam.me", raw: "https://czteam.me/download.php?id=1", want: "https://czteam.me/download.php?id=1"},
		{name: "same host scheme relative", base: "https://czteam.me", raw: "//czteam.me/download.php?id=1", want: "https://czteam.me/download.php?id=1"},
		{name: "absolute offsite", base: "https://czteam.me", raw: "https://cdn.example/download/1", wantErr: true},
		{name: "scheme-relative offsite", base: "https://czteam.me", raw: "//cdn.example/download/1", wantErr: true},
		{name: "same host wrong scheme", base: "https://czteam.me", raw: "http://czteam.me/download.php?id=1", wantErr: true},
		{name: "pathless", base: "https://czteam.me", raw: "https://czteam.me", wantErr: true},
		{name: "empty", base: "https://czteam.me", raw: " ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := joinCZTURL(tt.base, tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestResolveCategoryMatrix(t *testing.T) {
	tests := []struct {
		name    string
		meta    api.PreparedMetadata
		want    string
		wantErr bool
	}{
		{name: "display name answer", meta: api.PreparedMetadata{TrackerQuestionnaireAnswers: map[string]map[string]string{trackerName: {"category": "Software"}}}, want: "22"},
		{name: "numeric answer", meta: api.PreparedMetadata{TrackerQuestionnaireAnswers: map[string]map[string]string{trackerName: {"category": "6"}}}, want: "6"},
		{name: "movie hd", meta: api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "MOVIE"}, Release: api.ReleaseInfo{Resolution: "1080p"}}, want: "29"},
		{name: "tv hd ro", meta: api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "TV"}, Release: api.ReleaseInfo{Resolution: "1080p"}, SeasonInt: 1, SubtitleLanguages: []string{"ro"}}, want: "34"},
		{name: "anime", meta: api.PreparedMetadata{Anime: true}, want: "23"},
		{name: "software", meta: api.PreparedMetadata{Release: api.ReleaseInfo{Category: "Software"}}, want: "22"},
		{name: "music", meta: api.PreparedMetadata{Release: api.ReleaseInfo{Category: "Music/Audio"}}, want: "6"},
		{name: "music video phrase", meta: api.PreparedMetadata{Release: api.ReleaseInfo{Category: "Music Video"}}, want: "30"},
		{name: "music video separator", meta: api.PreparedMetadata{Release: api.ReleaseInfo{Category: "music-video"}}, want: "30"},
		{name: "mvid", meta: api.PreparedMetadata{Release: api.ReleaseInfo{Category: "MVID"}}, want: "30"},
		{name: "generic video hint", meta: api.PreparedMetadata{Release: api.ReleaseInfo{Category: "Video", Resolution: "1080p"}}, want: "29"},
		{name: "unknown non-video", meta: api.PreparedMetadata{Release: api.ReleaseInfo{Category: "Other Data"}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveCategory(tt.meta)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected category error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected category %q, got %q", tt.want, got)
			}
		})
	}
}

func TestCategoryQuestionnaireRequiresUnknownNonVideo(t *testing.T) {
	questionnaire := categoryQuestionnaire(api.PreparedMetadata{Release: api.ReleaseInfo{Category: "Other Data"}})
	if questionnaire == nil || len(questionnaire.Fields) != 1 {
		t.Fatalf("expected one category questionnaire field, got %+v", questionnaire)
	}
	field := questionnaire.Fields[0]
	if !field.Required {
		t.Fatal("expected unknown non-video category to require an explicit answer")
	}
	if field.Value != "" {
		t.Fatalf("expected no default category value, got %q", field.Value)
	}
}

func TestBuildUploadDryRunBlocksMissingRequiredCategory(t *testing.T) {
	req := cztUploadRequest(t, defaultBaseURL)
	req.Meta.ExternalIDs.Category = ""
	req.Meta.Release.Category = "Other Data"

	entry, err := buildUploadDryRun(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected dry-run error: %v", err)
	}
	if entry.Status != "blocked" {
		t.Fatalf("expected blocked dry-run status, got %q", entry.Status)
	}
	if !strings.Contains(entry.Message, "category questionnaire") {
		t.Fatalf("expected category questionnaire message, got %q", entry.Message)
	}
	if _, ok := entry.Payload["category"]; ok {
		t.Fatalf("expected unresolved category omitted from payload, got %q", entry.Payload["category"])
	}
	if entry.Questionnaire == nil || len(entry.Questionnaire.Fields) != 1 || !entry.Questionnaire.Fields[0].Required {
		t.Fatalf("expected required category questionnaire, got %+v", entry.Questionnaire)
	}

	if _, err := prepareUploadState(context.Background(), req, true); err == nil {
		t.Fatal("expected actual upload state to still require category")
	}
}

func TestBuildDescriptionAndDryRunUseProvidedAssets(t *testing.T) {
	req := cztUploadRequest(t, defaultBaseURL)

	entry, err := buildUploadDryRun(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected dry-run error: %v", err)
	}
	if !strings.Contains(entry.Description, "https://img.example/rehosted.jpg") {
		t.Fatalf("expected provided screenshot in dry-run description, got %q", entry.Description)
	}
	if !strings.Contains(entry.Payload["user_descr"], "https://img.example/rehosted.jpg") {
		t.Fatalf("expected provided screenshot in dry-run payload, got %q", entry.Payload["user_descr"])
	}

	result, err := (definition{}).BuildDescription(context.Background(), trackers.DescriptionRequest{
		Tracker: "CZT",
		Meta:    req.Meta,
		Logger:  api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: "[center]final rewritten body[/center]",
			Screenshots: []api.ScreenshotImage{{
				ImgURL: "https://img.example/should-not-append.jpg",
			}},
			Final: true,
		},
	})
	if err != nil {
		t.Fatalf("unexpected description error: %v", err)
	}
	if result.Description != "[center]final rewritten body[/center]" {
		t.Fatalf("expected final description verbatim, got %q", result.Description)
	}
}

func validTorrentBytes(t *testing.T) []byte {
	t.Helper()
	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "source.bin")
	torrentPath := filepath.Join(tmp, "source.torrent")
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
	payload, err := os.ReadFile(torrentPath)
	if err != nil {
		t.Fatalf("read torrent: %v", err)
	}
	return payload
}

func cztUploadRequest(t *testing.T, trackerURL string) trackers.UploadRequest {
	t.Helper()
	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "Release.torrent")
	if err := os.WriteFile(torrentPath, []byte("source-torrent"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return trackers.UploadRequest{
		Tracker: "CZT",
		Meta: api.PreparedMetadata{
			SourcePath:  sourcePath,
			TorrentPath: torrentPath,
			ReleaseName: "Release.2026.1080p.WEB-DL",
			ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
			Release:     api.ReleaseInfo{Resolution: "1080p"},
		},
		TrackerConfig: config.TrackerConfig{
			URL:     trackerURL,
			Passkey: "pass",
		},
		AppConfig: config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "state", "upbrr.db")}},
		Logger:    api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: "kept description",
			Screenshots: []api.ScreenshotImage{{
				ImgURL: "https://img.example/rehosted.jpg",
				WebURL: "https://img.example/page",
				RawURL: "https://img.example/raw.jpg",
			}},
		},
	}
}
