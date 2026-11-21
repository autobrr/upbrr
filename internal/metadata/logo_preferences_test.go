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
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

type logoOnlyTMDB struct {
	stubTMDB
	order []string
	calls int
	err   error
}

func (c *logoOnlyTMDB) SelectLogo(_ context.Context, _ int, _ string, order []string) (string, string, error) {
	c.order = slices.Clone(order)
	c.calls++
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

func TestOrdinaryPreparationRetriesFailedLogoPreferenceAfterRestart(t *testing.T) {
	fixture := newCorrectionEvidenceFixture(t)
	fixture.input.Instructions.Identity.KeepTMDB = new(true)
	fixture.input.ExternalFreshness = api.ExternalFreshnessReuse
	client := &logoOnlyTMDB{metadata: tmdb.MetadataResult{
		Title:               "Example Series",
		TMDBType:            "tv",
		Logo:                "https://image.tmdb.org/t/p/original/old.png",
		TMDBLogo:            "old.png",
		LogoLookupAttempted: true,
	}}
	initial, _ := fixture.open(t, WithTMDBClient(client))
	first, err := initial.Prepare(t.Context(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Release.ProviderMetadata.TMDB; got == nil || got.TMDBLogo != "old.png" {
		t.Fatalf("initial logo = %+v, want old.png", got)
	}

	opts := []Option{WithTMDBClient(client), WithConfig(config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: fixture.dbPath},
		Description:  config.DescriptionSettingsConfig{LogoLanguage: "pt"},
	})}
	changed, _ := fixture.open(t, opts...)
	fixture.input.LogoLanguagePreference = "pt"
	client.err = errors.New("temporarily unavailable")
	failed, err := changed.Prepare(t.Context(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	if got := failed.Release.ProviderMetadata.TMDB; got == nil || got.TMDBLogo != "old.png" || client.calls != 1 {
		t.Fatalf("failing lookup did not preserve the old logo: logo=%+v, attempts=%d", got, client.calls)
	}

	client.err = nil
	restarted, _ := fixture.open(t, opts...)
	requiredInput := fixture.input
	requiredInput.RequirePrepared = true
	required, err := restarted.Prepare(t.Context(), requiredInput)
	if err != nil || required.Release.Generation != failed.Release.Generation || client.calls != 1 {
		t.Fatalf("required preparation could not use the current fallback: generation=%d attempts=%d error=%v", required.Release.Generation, client.calls, err)
	}
	recovered, err := restarted.Prepare(t.Context(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	if got := recovered.Release.ProviderMetadata.TMDB; got == nil || got.TMDBLogo != "chosen.png" || client.calls != 2 ||
		recovered.Release.Generation == failed.Release.Generation {
		t.Fatalf("ordinary preparation did not retry after recovery: logo=%+v, attempts=%d", got, client.calls)
	}
	reused, err := restarted.Prepare(t.Context(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Release.Generation != recovered.Release.Generation || client.calls != 2 || client.metaCalls != 1 {
		t.Fatalf("successful retry did not restore reuse: generation=%d, artwork calls=%d, title calls=%d", reused.Release.Generation, client.calls, client.metaCalls)
	}
}
