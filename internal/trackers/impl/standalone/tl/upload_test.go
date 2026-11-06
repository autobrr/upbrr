// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareUploadRequiresPasskeyDerivedAnnounce(t *testing.T) {
	t.Parallel()

	_, err := prepareUpload(context.Background(), trackers.PreparationInput{
		Tracker: "TL",
		Intent:  trackers.PreparationIntentUpload,
		Meta: api.UploadSubject{Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryMovie,
			IMDBID:   1234567,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "required passkey-derived announce URL is missing") {
		t.Fatalf("prepare upload error = %v", err)
	}
}

func TestAPIPayloadUsesSelectedProviderIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, field, value string
		category           api.CanonicalCategory
		anime              bool
	}{
		{"anime uses mapped AniList", "animeid", "https://anilist.co/anime/222", api.CanonicalCategoryTV, true},
		{"movie uses IMDb", "imdb", "tt0000456", api.CanonicalCategoryMovie, false},
		{"TV uses TVmaze", "tvmazeid", "789", api.CanonicalCategoryTV, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			torrentPath := filepath.Join(t.TempDir(), "prepared.torrent")
			if err := os.WriteFile(torrentPath, []byte("synthetic multipart file"), 0o600); err != nil {
				t.Fatal(err)
			}
			state, _, err := prepareUploadState(t.Context(), trackers.PreparationInput{
				Tracker:       "TL",
				TrackerConfig: config.TrackerConfig{APIUpload: true},
				Projection: &api.TrackerReleaseProjection{
					UploadReleaseName: "Example.Release.2026-GRP",
					Readiness:         api.ReadinessStatusReady,
					UploadReady:       true,
				},
				Assets: &trackers.DescriptionAssets{Final: true, Description: "Reviewed description"},
				Meta: api.UploadSubject{
					Anime:       test.anime,
					TorrentPath: torrentPath,
					Identity: api.ExternalIdentity{
						Category: test.category,
						MALID:    111,
						IMDBID:   456,
						TVmazeID: 789,
					},
					ProviderMetadata: api.SourceScopedMetadata{AniList: &api.AniListMetadata{MALID: 111, AniListID: 222}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if state.fields[test.field] != test.value {
				t.Fatalf("%s = %q, want %q", test.field, state.fields[test.field], test.value)
			}
			for _, field := range []string{"animeid", "imdb", "tvmazeid"} {
				if field != test.field && state.fields[field] != "" {
					t.Fatalf("unexpected sibling provider field %s", field)
				}
			}
		})
	}
}
