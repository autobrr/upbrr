// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial/authfixture"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildDescriptionUsesPreparedDiscMenuAssets(t *testing.T) {
	t.Parallel()

	assets := trackers.DescriptionAssets{
		Description: "Body token",
		MenuImages:  []api.ScreenshotImage{{RawURL: "https://images.example.invalid/menu.png"}},
		Screenshots: []api.ScreenshotImage{{RawURL: "https://images.example.invalid/normal.png"}},
	}
	result, err := prepareDescription(context.Background(), trackers.PreparationInput{
		Tracker: "TL",
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{Description: config.DescriptionSettingsConfig{
			DiscMenuHeader:   "Disc menu token",
			ScreenshotHeader: "Screenshots token",
		}}),
		Assets: &assets,
	})
	if err != nil {
		t.Fatalf("build description: %v", err)
	}
	assertDescriptionTokensInOrder(t, result.Description, "Body token", "Disc menu token", "menu.png", "Screenshots token", "normal.png")

	final := trackers.DescriptionAssets{
		Description: " Authoritative final token ",
		Final:       true,
		MenuImages:  assets.MenuImages,
	}
	result, err = prepareDescription(context.Background(), trackers.PreparationInput{Tracker: "TL", Assets: &final})
	if err != nil {
		t.Fatalf("build final description: %v", err)
	}
	if result.Description != "Authoritative final token" {
		t.Fatalf("final description = %q", result.Description)
	}
}

func TestScreenshotModesSurviveFinalDescriptionHydration(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                  string
		api, rehost, embedded bool
	}{
		{"API rehost", true, true, true},
		{"cookie rehost", false, true, false},
		{"cookie no rehost", false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "upbrr.db")
			authfixture.Write(t, dbPath)
			repo, err := servicedb.OpenWithLoggerContext(t.Context(), dbPath, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.MigrateContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			if err := cookies.SaveTrackerCookieMap(t.Context(), dbPath, "TL", map[string]string{"session": "synthetic"}); err != nil {
				t.Fatal(err)
			}
			torrentPath := filepath.Join(t.TempDir(), "prepared.torrent")
			if err := os.WriteFile(torrentPath, []byte("synthetic multipart file"), 0o600); err != nil {
				t.Fatal(err)
			}
			assets := trackers.DescriptionAssets{
				Description: "User text <img src=\"https://images.example.invalid/user.png\">",
				MenuImages:  []api.ScreenshotImage{{RawURL: "https://images.example.invalid/menu.png"}},
				Screenshots: []api.ScreenshotImage{{RawURL: "https://images.example.invalid/menu.png"}, {ImgURL: "https://images.example.invalid/normal.png", Tonemapping: api.ScreenshotTonemapped}},
			}
			req := trackers.PreparationInput{
				Tracker:       "TL",
				TrackerConfig: config.TrackerConfig{APIUpload: test.api, ImgRehost: test.rehost},
				Assets:        &assets,
				Meta:          api.UploadSubject{TorrentPath: torrentPath},
				Projection: &api.TrackerReleaseProjection{
					UploadReleaseName: "Example.Release.2026-GRP",
					Readiness:         api.ReadinessStatusReady,
					UploadReady:       true,
				},
				Runtime: trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}, Description: config.DescriptionSettingsConfig{
					DiscMenuHeader:   "Menu heading",
					ScreenshotHeader: "Shots heading",
					TonemappedHeader: "Colour warning",
				}}),
			}
			preview, err := prepareDescription(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(preview.Description, "menu.png") != test.embedded || strings.Contains(preview.Description, "Menu heading") != test.embedded || strings.Contains(preview.Description, "Shots heading") != test.embedded || !strings.Contains(preview.Description, "user.png") {
				t.Fatalf("description mode: %s", preview.Description)
			}
			if strings.Contains(preview.Description, "normal.png") != test.embedded || !strings.Contains(preview.Description, "Screenshots: 1 tonemapped, 0 not tonemapped, 1 with unknown processing.") {
				t.Fatalf("screenshot and processing notice: %s", preview.Description)
			}
			assets.Description, assets.Final = preview.Description, true
			state, _, err := prepareUploadState(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if state.description != preview.Description {
				t.Fatal("final hydration changed reviewed description")
			}
			body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, state.files)
			if err != nil {
				t.Fatal(err)
			}
			_, params, err := mime.ParseMediaType(contentType)
			if err != nil {
				t.Fatal(err)
			}
			reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
			fields := make(map[string]string)
			for {
				part, err := reader.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				payload, err := io.ReadAll(part)
				if err != nil {
					t.Fatal(err)
				}
				fields[part.FormName()] = string(payload)
			}
			if !test.api && test.rehost {
				if fields["screenshots[0]"] != "https://images.example.invalid/menu.png" || fields["screenshots[1]"] != "https://images.example.invalid/normal.png" || fields["screenshots[2]"] != "" {
					t.Fatalf("native screenshots = %#v", fields)
				}
			} else if fields["screenshots[0]"] != "" {
				t.Fatal("unexpected native rehosting")
			}
			if final := buildDescription(req, trackers.DescriptionAssets{Final: true, Description: "User final <img src=\"explicit.png\">"}); !strings.Contains(final, "explicit.png") {
				t.Fatal("final user content removed")
			}
		})
	}
}

func assertDescriptionTokensInOrder(t *testing.T, description string, tokens ...string) {
	t.Helper()
	previous := -1
	for _, token := range tokens {
		position := strings.Index(description, token)
		if position <= previous {
			t.Fatalf("description tokens out of order at %q: %q", token, description)
		}
		previous = position
	}
}
