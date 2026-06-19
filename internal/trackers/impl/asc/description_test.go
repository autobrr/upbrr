// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"strings"
	"testing"

	descsvc "github.com/autobrr/upbrr/internal/services/description"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildTechnicalSheetHomepageURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		homepage string
		want     string
	}{
		{
			name:     "valid https",
			homepage: "https://example.com/movie",
			want:     "Site: [url=https://example.com/movie]Clique aqui[/url]",
		},
		{
			name:     "valid http path and query",
			homepage: "http://example.com/path/to/movie?utm_source=tmdb&lang=pt-BR",
			want:     "Site: [url=http://example.com/path/to/movie?utm_source=tmdb&lang=pt-BR]Clique aqui[/url]",
		},
		{
			name:     "valid encoded path query and fragment",
			homepage: "https://example.com/path%20to/movie?name=A%2BB&ok=%25#frag%20ment",
			want:     "Site: [url=https://example.com/path%20to/movie?name=A%2BB&ok=%25#frag%20ment]Clique aqui[/url]",
		},
		{
			name:     "missing scheme",
			homepage: "example.com/movie",
		},
		{
			name:     "protocol relative",
			homepage: "//example.com/movie",
		},
		{
			name:     "missing host",
			homepage: "https:///movie",
		},
		{
			name:     "unsupported scheme",
			homepage: "javascript:alert(1)",
		},
		{
			name:     "bracket boundary",
			homepage: "https://example.com/movie]broken",
		},
		{
			name:     "quote boundary",
			homepage: "https://example.com/movie\"broken",
		},
		{
			name:     "newline boundary",
			homepage: "https://example.com/movie\n[url=https://evil.test]",
		},
		{
			name:     "encoded closing bracket boundary",
			homepage: "https://example.com/movie%5Dbroken",
		},
		{
			name:     "mixed case encoded opening bracket boundary",
			homepage: "https://example.com/movie%5bbroken",
		},
		{
			name:     "encoded quote boundary",
			homepage: "https://example.com/movie?title=%22broken",
		},
		{
			name:     "encoded single quote boundary",
			homepage: "https://example.com/movie?title=%27broken",
		},
		{
			name:     "encoded line feed boundary",
			homepage: "https://example.com/movie?title=good%0Abad",
		},
		{
			name:     "encoded carriage return boundary",
			homepage: "https://example.com/movie?title=good%0Dbad",
		},
		{
			name:     "encoded close tag payload",
			homepage: "https://example.com/movie%5D%5B/url%5D%5Burl=https://evil.test%5D",
		},
		{
			name:     "malformed percent escape",
			homepage: "https://example.com/movie?title=%zz",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := buildTechnicalSheet(api.PreparedMetadata{}, &richMediaResponse{Homepage: tc.homepage})
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestBuildTechnicalSheetHomepageURLRejectsEncodedDelimiterThroughRender(t *testing.T) {
	t.Parallel()

	homepage := "https://example.com/movie%5D%5B/url%5D%5Burl=https://evil.test%5D"
	sheet := buildTechnicalSheet(api.PreparedMetadata{}, &richMediaResponse{Homepage: homepage})
	if sheet != "" {
		t.Fatalf("expected encoded delimiter homepage to be omitted, got %q", sheet)
	}
	rendered := descsvc.Render(sheet)
	if strings.Contains(rendered, "evil.test") || strings.Contains(rendered, "<a href=") {
		t.Fatalf("expected encoded delimiter homepage to be absent from rendered output, got %q", rendered)
	}
}

func TestHomepageURLFixLeavesOtherASCLinksUnchanged(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{
			Category: "MOVIE",
			IMDBID:   7654321,
			TMDBID:   123,
		},
	}

	cast := buildCastSection(meta, []richCreditItem{
		{ID: 42, Name: "Jane Example", Character: "Hero", ProfilePath: "/profile.jpg"},
	})
	if !strings.Contains(cast, "[url=https://www.themoviedb.org/person/42?language=pt-BR]") {
		t.Fatalf("expected TMDB cast link to stay unchanged, got %q", cast)
	}

	ratings := buildRatingsBBCode(meta, []map[string]any{
		{"Source": "Internet Movie Database", "Value": "7.8/10"},
		{"Source": "TMDb", "Value": "8.2/10"},
	})
	if !strings.Contains(ratings, "[url=https://www.imdb.com/title/tt7654321]") {
		t.Fatalf("expected IMDb rating link to stay unchanged, got %q", ratings)
	}
	if !strings.Contains(ratings, "[url=https://www.themoviedb.org/movie/123]") {
		t.Fatalf("expected TMDB rating link to stay unchanged, got %q", ratings)
	}
}
