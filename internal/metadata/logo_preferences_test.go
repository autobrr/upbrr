// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

type logoOnlyTMDB struct {
	stubTMDB
	order []string
	err   error
}

func (c *logoOnlyTMDB) SelectLogo(_ context.Context, _ int, _ string, order []string) (string, string, error) {
	c.order = slices.Clone(order)
	if c.err != nil {
		return "", "", c.err
	}
	return "https://image.tmdb.org/t/p/original/chosen.png", "chosen.png", nil
}

func TestRetainedTMDBMetadataReselectsLogoWithoutTitleRefetch(t *testing.T) {
	source := filepath.Join(t.TempDir(), "example.mkv")
	client := &logoOnlyTMDB{}
	previous := &api.TMDBMetadata{
		TMDBID:              42,
		Category:            "movie",
		Title:               "Retained title",
		Logo:                "https://image.tmdb.org/t/p/original/old.png",
		TMDBLogo:            "old.png",
		LogoLookupAttempted: true,
	}
	svc := newTestService(t, &fakeRepo{},
		WithConfig(config.Config{Description: config.DescriptionSettingsConfig{LogoLanguage: "PT-BR, JA"}}),
		WithTMDBClient(client),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)

	input := preparationstate.State{
		SourcePath:      source,
		StoredDataFresh: true,
		Identity: api.ExternalIdentity{
			SourcePath: source,
			TMDBID:     42,
			Category:   api.CanonicalCategoryMovie,
		},
		ProviderMetadata: api.SourceScopedMetadata{SourcePath: source, TMDB: previous},
	}
	result, err := svc.resolveExternalIdentity(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if client.metaCalls != 0 || !slices.Equal(client.order, []string{"pt", "ja"}) {
		t.Fatalf("metadata fetches=%d preferred languages=%v", client.metaCalls, client.order)
	}
	got := result.ProviderMetadata.TMDB
	if got == nil || got.Title != "Retained title" || got.TMDBLogo != "chosen.png" || got.LogoLanguagePreference != "pt,ja" || !got.LogoLookupAttempted || previous.TMDBLogo != "old.png" {
		t.Fatalf("retained metadata was not refreshed without mutating its source: got=%+v original=%+v", got, previous)
	}

	client.err = errors.New("temporarily unavailable")
	result, err = svc.resolveExternalIdentity(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	got = result.ProviderMetadata.TMDB
	if got == nil || got.TMDBLogo != previous.TMDBLogo || got.LogoLanguagePreference != previous.LogoLanguagePreference {
		t.Fatalf("failed artwork lookup replaced retained logo or preference: %+v", got)
	}
}
