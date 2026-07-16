// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestApplyMediaInfoIDsFromJSON(t *testing.T) {
	base := t.TempDir()
	jsonPath := filepath.Join(base, "MediaInfo.json")
	payload := `{"media":{"track":[{"@type":"General","extra":{"IMDB":"tt1234567","TMDB":"movie/765432","TVDB2":"movies/456789"}}]}}`
	if err := os.WriteFile(jsonPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write json: %v", err)
	}

	svc := &Service{}
	meta, err := svc.collectMediaInfoIdentityEvidence(context.Background(), preparationstate.State{MediaInfoJSONPath: jsonPath})
	if err != nil {
		t.Fatalf("apply mediainfo ids: %v", err)
	}
	if meta.MediaInfoTMDBID != 765432 {
		t.Fatalf("expected tmdb 765432, got %d", meta.MediaInfoTMDBID)
	}
	if meta.MediaInfoIMDBID != 1234567 {
		t.Fatalf("expected imdb 1234567, got %d", meta.MediaInfoIMDBID)
	}
	if meta.MediaInfoTVDBID != 456789 {
		t.Fatalf("expected tvdb 456789, got %d", meta.MediaInfoTVDBID)
	}
	if meta.MediaInfoCategory != "movie" {
		t.Fatalf("expected category movie, got %q", meta.MediaInfoCategory)
	}
}

func TestApplyMediaInfoIDsTextTVDBPriority(t *testing.T) {
	base := t.TempDir()
	textPath := filepath.Join(base, "mediainfo.txt")
	payload := "Writing library                          : libebml v1.4.5 + libmatroska v1.7.1\nIMDB                                     : tt7654321\nTMDB                                     : tv/765433\nTVDB                                     : 456790\nTVDB2                                    : episodes/456791\n"
	if err := os.WriteFile(textPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write text: %v", err)
	}

	svc := &Service{}
	meta, err := svc.collectMediaInfoIdentityEvidence(context.Background(), preparationstate.State{MediaInfoTextPath: textPath})
	if err != nil {
		t.Fatalf("apply mediainfo ids: %v", err)
	}
	if meta.MediaInfoTVDBID != 456790 {
		t.Fatalf("expected tvdb 456790, got %d", meta.MediaInfoTVDBID)
	}
}

func TestApplyMediaInfoIDsMismatch(t *testing.T) {
	base := t.TempDir()
	jsonPath := filepath.Join(base, "MediaInfo.json")
	payload := `{"media":{"track":[{"@type":"General","extra":{"TMDB":"movie/123"}}]}}`
	if err := os.WriteFile(jsonPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write json: %v", err)
	}

	svc := &Service{}
	meta, err := svc.collectMediaInfoIdentityEvidence(context.Background(), preparationstate.State{
		MediaInfoJSONPath: jsonPath,
		TrackerData:       []api.TrackerMetadata{{TMDBID: 999}},
	})
	if err != nil {
		t.Fatalf("apply mediainfo ids: %v", err)
	}
	if meta.MismatchedMediaInfoTMDBID != 123 {
		t.Fatalf("expected mismatched tmdb 123, got %d", meta.MismatchedMediaInfoTMDBID)
	}
}
