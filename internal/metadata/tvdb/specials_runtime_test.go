// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvdb

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestExplicitSpecialRejectsEpisodeZero(t *testing.T) {
	t.Parallel()
	episodes := []Episode{{
		SeasonKnown:    true,
		SeasonNumber:   0,
		Number:         0,
		AbsoluteNumber: 1,
		Aired:          "2026-01-01",
	}}
	for _, query := range []EpisodeQuery{
		{SeasonKnown: true},
		{
			SeasonKnown: true,
			Absolute:    1,
			AiredDate:   "2026-01-01",
		},
	} {
		if episodeIsPresent(episodes, query) {
			t.Fatal("episode zero satisfied an explicit special cache query")
		}
		if match, ok := GetSpecificEpisodeData(EpisodesData{Episodes: episodes}, query); ok {
			t.Fatalf("episode zero selected special metadata: %+v", match)
		}
	}
}

func TestExplicitSpecialReusesProvenEpisodeCache(t *testing.T) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: tvdbRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("proven special cache triggered an HTTP request")
		return nil, context.Canceled
	})}, api.NopLogger{}, "synthetic-key", t.TempDir())
	data := EpisodesData{
		SeriesTitle: "Example Series",
		Episodes: []Episode{{
			ID:           555002,
			SeasonKnown:  true,
			SeasonNumber: 0,
			Number:       1,
			Name:         "The Special Signal",
		}},
	}
	if err := writeEpisodesCache(client.cachePath("", 555001, "eng"), data); err != nil {
		t.Fatal(err)
	}
	query := EpisodeQuery{SeasonKnown: true, Episode: 1}
	cached, _, err := client.GetEpisodes(t.Context(), 555001, query)
	if err != nil {
		t.Fatal(err)
	}
	match, ok := GetSpecificEpisodeData(cached, query)
	if !ok || !match.SeasonKnown || match.SeasonNumber != 0 || match.EpisodeNumber != 1 || match.EpisodeName != "The Special Signal" {
		t.Fatalf("proven special was lost on cache reuse: %+v", match)
	}
}

func TestSpecialEvidenceVersionPreservesSeriesResponses(t *testing.T) {
	t.Parallel()
	repo, err := db.OpenContext(t.Context(), filepath.Join(t.TempDir(), "metadata.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, scope := evidence.WithScope(t.Context(), repo, filepath.Join(t.TempDir(), "source.mkv"), "fingerprint", api.ExternalFreshnessReuse, nil)
	client := NewClient(&http.Client{Transport: tvdbRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unaffected series evidence triggered an HTTP request")
		return nil, context.Canceled
	})}, api.NopLogger{}, "synthetic-key", "")
	path := "/series/555001/extended"
	seeded := seriesExtendedResponse{Data: seriesExtendedDataResponse{ID: 555001, Name: "Example Series"}}
	if err := evidence.JSON(ctx, "tvdb.response.v1", []any{client.baseURL, client.apiKey, path, map[string]string(nil)},
		&seeded, errNotFound, nil, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	var result seriesExtendedResponse
	if err := client.getJSON(ctx, path, nil, &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.ID != seeded.Data.ID || result.Data.Name != seeded.Data.Name {
		t.Fatalf("unaffected series evidence changed: %+v", result)
	}
	if err := scope.Err(); err != nil {
		t.Fatal(err)
	}
}
