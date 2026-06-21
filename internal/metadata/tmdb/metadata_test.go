// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestFetchMetadataUsesGermanDetailFallbackWhenTranslationsFail(t *testing.T) {
	var translationsRequested atomic.Bool
	var localizedRequested atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie/123":
			if r.URL.Query().Get("language") == "de-DE" {
				localizedRequested.Store(true)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"title":"Deutscher Titel"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"title":"Original English","original_title":"Original English","release_date":"2024-01-01","runtime":100}`))
		case "/movie/123/translations":
			translationsRequested.Store(true)
			http.Error(w, "translation service unavailable", http.StatusInternalServerError)
		case "/movie/123/external_ids":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case "/movie/123/videos":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[]}`))
		case "/movie/123/keywords":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"keywords":[]}`))
		case "/movie/123/credits":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"crew":[],"cast":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key")
	client.baseURL = server.URL

	result, err := client.FetchMetadata(context.Background(), MetadataInput{
		TMDBID:   123,
		Category: "MOVIE",
	})
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}
	if !translationsRequested.Load() {
		t.Fatalf("expected translations endpoint to be requested")
	}
	if !localizedRequested.Load() {
		t.Fatalf("expected german detail fallback to be requested")
	}
	if got := result.LocalizedTitles["de"]; got != "Deutscher Titel" {
		t.Fatalf("expected german localized title fallback, got %q", got)
	}
}

func TestFetchMetadataStoresGenericAndRegionalLocalizedTitles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie/321":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"title":"Original English","original_title":"Original English","release_date":"2024-01-01","runtime":100}`))
		case "/movie/321/translations":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"translations":[{"iso_639_1":"pt","iso_3166_1":"BR","data":{"title":"Titulo Brasil"}},{"iso_639_1":"pt","iso_3166_1":"PT","data":{"title":"Titulo Portugal"}},{"iso_639_1":"de","iso_3166_1":"DE","data":{"title":"Deutscher Titel"}}]}`))
		case "/movie/321/external_ids":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case "/movie/321/videos":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[]}`))
		case "/movie/321/keywords":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"keywords":[]}`))
		case "/movie/321/credits":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"crew":[],"cast":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil, "api-key")
	client.baseURL = server.URL

	result, err := client.FetchMetadata(context.Background(), MetadataInput{
		TMDBID:   321,
		Category: "MOVIE",
	})
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}

	if got := result.LocalizedTitles["pt-BR"]; got != "Titulo Brasil" {
		t.Fatalf("expected regional brazilian portuguese title, got %q", got)
	}
	if got := result.LocalizedTitles["pt-PT"]; got != "Titulo Portugal" {
		t.Fatalf("expected regional portugal portuguese title, got %q", got)
	}
	if got := result.LocalizedTitles["pt"]; got != "Titulo Portugal" {
		t.Fatalf("expected generic portuguese key to preserve existing collapse behavior, got %q", got)
	}
	if got := result.LocalizedTitles["de"]; got != "Deutscher Titel" {
		t.Fatalf("expected generic german title, got %q", got)
	}
	if got := result.LocalizedTitles["de-DE"]; got != "Deutscher Titel" {
		t.Fatalf("expected regional german title, got %q", got)
	}
}
