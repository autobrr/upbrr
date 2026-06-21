// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
			_, _ = w.Write([]byte(`{"translations":[{"iso_639_1":"pt","iso_3166_1":"BR","data":{"title":"Titulo Brasil"}},{"iso_639_1":"pt","iso_3166_1":"PT","data":{"title":"Titulo Portugal"}},{"iso_639_1":"de","iso_3166_1":"DE","data":{"title":"Deutscher Titel"}},{"iso_639_1":"en","iso_3166_1":"US","data":{"title":"American Title"}}]}`))
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
		t.Fatalf("expected deterministic generic portuguese title, got %q", got)
	}
	if got := result.LocalizedTitles["de"]; got != "Deutscher Titel" {
		t.Fatalf("expected generic german title, got %q", got)
	}
	if got := result.LocalizedTitles["de-DE"]; got != "Deutscher Titel" {
		t.Fatalf("expected regional german title, got %q", got)
	}
	if got := result.LocalizedTitles["en"]; got != "American Title" {
		t.Fatalf("expected generic english title from common regional fallback, got %q", got)
	}
	if got := result.LocalizedTitles["en-US"]; got != "American Title" {
		t.Fatalf("expected regional english title, got %q", got)
	}
}

func TestFetchMetadataUsesGermanDetailFallbackWhenTranslationsLackUsableGerman(t *testing.T) {
	var localizedRequested atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie/456":
			if r.URL.Query().Get("language") == "de-DE" {
				localizedRequested.Store(true)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"title":"Deutscher Fallback"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"title":"Original English","original_title":"Original English","release_date":"2024-01-01","runtime":100}`))
		case "/movie/456/translations":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"translations":[{"iso_639_1":"de","iso_3166_1":"DE","data":{"title":"   "}},{"iso_639_1":"pt","iso_3166_1":"BR","data":{"title":"Titulo Brasil"}}]}`))
		case "/movie/456/external_ids":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case "/movie/456/videos":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[]}`))
		case "/movie/456/keywords":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"keywords":[]}`))
		case "/movie/456/credits":
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
		TMDBID:   456,
		Category: "MOVIE",
	})
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}
	if !localizedRequested.Load() {
		t.Fatalf("expected german detail fallback to be requested")
	}
	if got := result.LocalizedTitles["de"]; got != "Deutscher Fallback" {
		t.Fatalf("expected german fallback title, got %q", got)
	}
	if got := result.LocalizedTitles["pt-BR"]; got != "Titulo Brasil" {
		t.Fatalf("expected existing non-german regional title to remain, got %q", got)
	}
}

func TestFetchMetadataLogsGermanDetailFallbackErrorWhenTranslationsLackUsableGerman(t *testing.T) {
	var localizedRequested atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie/789":
			if r.URL.Query().Get("language") == "de-DE" {
				localizedRequested.Store(true)
				http.Error(w, "detail service unavailable", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"title":"Original English","original_title":"Original English","release_date":"2024-01-01","runtime":100}`))
		case "/movie/789/translations":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"translations":[{"iso_639_1":"de","iso_3166_1":"DE","data":{"title":"   "}},{"iso_639_1":"pt","iso_3166_1":"PT","data":{"title":"Titulo Portugal"}}]}`))
		case "/movie/789/external_ids":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case "/movie/789/videos":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[]}`))
		case "/movie/789/keywords":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"keywords":[]}`))
		case "/movie/789/credits":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"crew":[],"cast":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	logger := &captureTMDBLogger{}
	client := NewClient(server.Client(), logger, "api-key")
	client.baseURL = server.URL

	result, err := client.FetchMetadata(context.Background(), MetadataInput{
		TMDBID:   789,
		Category: "MOVIE",
	})
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}
	if !localizedRequested.Load() {
		t.Fatalf("expected german detail fallback to be requested")
	}
	if got := result.LocalizedTitles["de"]; got != "" {
		t.Fatalf("expected unavailable german fallback to leave generic de empty, got %q", got)
	}
	if !logger.hasWarning("tmdb: german title fallback lookup failed:") {
		t.Fatalf("expected german fallback warning, got %#v", logger.warnings())
	}
}

func TestBuildLocalizedTitlesUsesDeterministicGenericRegionalCandidate(t *testing.T) {
	tests := []struct {
		name            string
		translations    []Translation
		genericLanguage string
		wantGeneric     string
		wantRegional    map[string]string
	}{
		{
			name:            "default region first",
			genericLanguage: "pt",
			wantGeneric:     "Titulo Portugal",
			wantRegional: map[string]string{
				"pt-BR": "Titulo Brasil",
				"pt-PT": "Titulo Portugal",
			},
			translations: []Translation{
				{ISO6391: "pt", ISO31661: "PT", Data: TranslationData{Title: "Titulo Portugal"}},
				{ISO6391: " pt ", ISO31661: " br ", Data: TranslationData{Title: "Titulo Brasil"}},
			},
		},
		{
			name:            "default region last",
			genericLanguage: "pt",
			wantGeneric:     "Titulo Portugal",
			wantRegional: map[string]string{
				"pt-BR": "Titulo Brasil",
				"pt-PT": "Titulo Portugal",
			},
			translations: []Translation{
				{ISO6391: " pt ", ISO31661: " br ", Data: TranslationData{Title: "Titulo Brasil"}},
				{ISO6391: "pt", ISO31661: "PT", Data: TranslationData{Title: "Titulo Portugal"}},
			},
		},
		{
			name:            "english common region",
			genericLanguage: "en",
			wantGeneric:     "American Title",
			wantRegional:    map[string]string{"en-US": "American Title"},
			translations: []Translation{
				{ISO6391: "en", ISO31661: "US", Data: TranslationData{Title: "American Title"}},
			},
		},
		{
			name:            "french canadian common region",
			genericLanguage: "fr",
			wantGeneric:     "Titre Canadien",
			wantRegional:    map[string]string{"fr-CA": "Titre Canadien"},
			translations: []Translation{
				{ISO6391: " fr ", ISO31661: " ca ", Data: TranslationData{Title: "Titre Canadien"}},
			},
		},
		{
			name:            "mexican spanish common region",
			genericLanguage: "es",
			wantGeneric:     "Serie Mexicana",
			wantRegional:    map[string]string{"es-MX": "Serie Mexicana"},
			translations: []Translation{
				{ISO6391: "es", ISO31661: "MX", Data: TranslationData{Name: "Serie Mexicana"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			titles := buildLocalizedTitles(tt.translations)

			if got := titles[tt.genericLanguage]; got != tt.wantGeneric {
				t.Fatalf("expected generic %s key to use regional candidate %q, got %q", tt.genericLanguage, tt.wantGeneric, got)
			}
			for key, want := range tt.wantRegional {
				if got := titles[key]; got != want {
					t.Fatalf("expected regional title %s=%q, got %q", key, want, got)
				}
			}
		})
	}
}

func TestBuildLocalizedTitlesKeepsNeutralGenericOverRegionalCandidate(t *testing.T) {
	titles := buildLocalizedTitles([]Translation{
		{ISO6391: "pt", ISO31661: "PT", Data: TranslationData{Title: "Titulo Portugal"}},
		{ISO6391: "pt", Data: TranslationData{Title: "Titulo Neutro"}},
		{ISO6391: "de", ISO31661: "DE", Data: TranslationData{Title: "Deutscher Titel"}},
		{ISO6391: "en", ISO31661: "US", Data: TranslationData{Title: "American English"}},
		{ISO6391: "en", Data: TranslationData{Title: "Neutral English"}},
	})

	if got := titles["pt"]; got != "Titulo Neutro" {
		t.Fatalf("expected neutral portuguese title to keep generic key, got %q", got)
	}
	if got := titles["de-DE"]; got != "Deutscher Titel" {
		t.Fatalf("expected regional german title, got %q", got)
	}
	if got := titles["de"]; got != "Deutscher Titel" {
		t.Fatalf("expected default regional german title to keep generic compatibility, got %q", got)
	}
	if got := titles["en"]; got != "Neutral English" {
		t.Fatalf("expected neutral language title to populate generic key, got %q", got)
	}
	if got := titles["en-US"]; got != "American English" {
		t.Fatalf("expected regional english title, got %q", got)
	}
}

func TestBuildLocalizedTitlesUsesTVTranslationName(t *testing.T) {
	titles := buildLocalizedTitles([]Translation{
		{ISO6391: "es", ISO31661: "ES", Data: TranslationData{Name: "Serie Espanola"}},
	})

	if got := titles["es"]; got != "Serie Espanola" {
		t.Fatalf("expected generic tv translation name, got %q", got)
	}
	if got := titles["es-ES"]; got != "Serie Espanola" {
		t.Fatalf("expected regional tv translation name, got %q", got)
	}
}

type captureTMDBLogger struct {
	mu    sync.Mutex
	warns []string
}

func (l *captureTMDBLogger) Tracef(string, ...any) {}
func (l *captureTMDBLogger) Debugf(string, ...any) {}
func (l *captureTMDBLogger) Infof(string, ...any)  {}
func (l *captureTMDBLogger) Errorf(string, ...any) {}

func (l *captureTMDBLogger) Warnf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, fmt.Sprintf(format, args...))
}

func (l *captureTMDBLogger) hasWarning(prefix string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, warning := range l.warns {
		if strings.HasPrefix(warning, prefix) {
			return true
		}
	}
	return false
}

func (l *captureTMDBLogger) warnings() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.warns...)
}
