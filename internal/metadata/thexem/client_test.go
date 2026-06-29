// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thexem

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestMapAbsoluteEpisode(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/map/single" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET request, got %s", r.Method)
		}
		if got := r.Header.Get("User-Agent"); got != thexemUserAgent {
			t.Fatalf("expected User-Agent %q, got %q", thexemUserAgent, got)
		}
		query := r.URL.Query()
		if query.Get("id") != "123" || query.Get("origin") != "tvdb" || query.Get("absolute") != "43" || query.Get("destination") != "scene" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"scene":{"season":2,"episode":5}}}`))
	}))
	defer server.Close()

	client := NewClient(server.Client(), api.NopLogger{})
	client.baseURL = server.URL

	season, episode, err := client.MapAbsoluteEpisode(context.Background(), 123, 43)
	if err != nil {
		t.Fatalf("map absolute: %v", err)
	}
	if season != 2 || episode != 5 {
		t.Fatalf("unexpected mapping season=%d episode=%d", season, episode)
	}
}

func TestGetSeasonNamesAndMatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/map/names" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("User-Agent"); got != thexemUserAgent {
			t.Fatalf("expected User-Agent %q, got %q", thexemUserAgent, got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"1":["Anime"],"2":["Anime Season 2","Second Season"]}}`))
	}))
	defer server.Close()

	client := NewClient(server.Client(), api.NopLogger{})
	client.baseURL = server.URL

	names, err := client.GetSeasonNames(context.Background(), 456)
	if err != nil {
		t.Fatalf("get season names: %v", err)
	}
	if len(names[2]) == 0 {
		t.Fatalf("expected names for season 2")
	}

	season, err := client.MatchSeasonByName(context.Background(), 456, "Anime Season 2")
	if err != nil {
		t.Fatalf("match season: %v", err)
	}
	if season != 2 {
		t.Fatalf("expected season 2, got %d", season)
	}
}

func TestHTTPErrorDetailCompactsCloudflareBlock(t *testing.T) {
	t.Parallel()

	body := []byte(`<html><head><title>Attention Required! | Cloudflare</title></head><body><h1>Sorry, you have been blocked</h1><span>Your IP: 103.95.115.127</span><script>var ip="103.95.115.127"</script></body></html>`)
	got := httpErrorDetail(body)

	if got != "Cloudflare block page" {
		t.Fatalf("expected compact cloudflare detail, got %q", got)
	}
	if strings.Contains(got, "103.95.115.127") || strings.Contains(got, "<html") {
		t.Fatalf("expected raw html and ip removed, got %q", got)
	}
}

func TestMapAbsoluteEpisodeCloudflareBlockIsUnavailable(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<html><title>Attention Required! | Cloudflare</title><body>Sorry, you have been blocked</body></html>`))
	}))
	defer server.Close()

	client := NewClient(server.Client(), api.NopLogger{})
	client.baseURL = server.URL

	season, episode, err := client.MapAbsoluteEpisode(context.Background(), 123, 43)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable error, got %v", err)
	}
	if season != 0 || episode != 0 {
		t.Fatalf("expected no mapping on unavailable response, got season=%d episode=%d", season, episode)
	}
}

func TestHTTPErrorDetailCompactsHTMLAndRedactsIP(t *testing.T) {
	t.Parallel()

	body := []byte(`<html><body><h1>Forbidden</h1><p>blocked ip 103.95.115.127</p><script>secret()</script></body></html>`)
	got := httpErrorDetail(body)

	if !strings.Contains(got, "Forbidden") {
		t.Fatalf("expected html text preserved, got %q", got)
	}
	if strings.Contains(got, "103.95.115.127") || strings.Contains(got, "secret()") || strings.Contains(got, "<body") {
		t.Fatalf("expected html noise redacted, got %q", got)
	}
}
