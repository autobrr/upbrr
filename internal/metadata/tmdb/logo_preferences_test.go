// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLogoPreferenceReusesUnfilteredMovieAndTVArtwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Has("language") || r.URL.Query().Has("include_image_language") {
			t.Errorf("artwork cache must store unfiltered evidence: %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/movie/42/images":
			_, _ = w.Write([]byte(`{"logos":[{"file_path":"/en.png","iso_639_1":"en"},{"file_path":"/ja.png","iso_639_1":"ja"},{"file_path":"/pt.png","iso_639_1":"pt"}]}`))
		case "/tv/42/images":
			_, _ = w.Write([]byte(`{"logos":[{"file_path":"/unrelated.png","iso_639_1":"fr"},{"file_path":"/neutral.png","iso_639_1":null}]}`))
		case "/tv/43/images":
			_, _ = w.Write([]byte(`{"logos":[{"file_path":"/unrelated.png","iso_639_1":"fr"}]}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	repo, err := db.OpenContext(t.Context(), filepath.Join(t.TempDir(), "logo-evidence.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	client := NewClient(server.Client(), nil, "test-key")
	client.baseURL = server.URL
	source := filepath.Join(t.TempDir(), "example.mkv")
	for _, tc := range []struct {
		id       int
		category string
		order    []string
		want     string
	}{
		{42, "MOVIE", []string{"pt", "ja"}, "pt.png"},
		{42, "MOVIE", []string{"ja", "pt"}, "ja.png"},
		{42, "MOVIE", nil, "en.png"},
		{42, "TV", []string{"ja"}, "neutral.png"},
		{43, "TV", []string{"pt"}, ""},
	} {
		ctx, scope := evidence.WithScope(t.Context(), repo, source, "same media", api.ExternalFreshnessReuse, nil)
		logo, name, err := client.SelectLogo(ctx, tc.id, tc.category, tc.order)
		if err != nil || name != tc.want || (tc.want == "") != (logo == "") {
			t.Fatalf("%s %v: logo=%q name=%q err=%v, want %q", tc.category, tc.order, logo, name, err, tc.want)
		}
		if err := scope.Err(); err != nil {
			t.Fatal(err)
		}
	}
	if count := requests.Load(); count != 3 {
		t.Fatalf("got %d image requests; expected exactly one per title/category", count)
	}
}
