// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"strings"
	"testing"

	descsvc "github.com/autobrr/upbrr/internal/description"
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

			got := buildTechnicalSheet(api.UploadSubject{}, &richMediaResponse{Homepage: tc.homepage})
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestBuildTechnicalSheetHomepageURLRejectsEncodedDelimiterThroughRender(t *testing.T) {
	t.Parallel()

	homepage := "https://example.com/movie%5D%5B/url%5D%5Burl=https://evil.test%5D"
	sheet := buildTechnicalSheet(api.UploadSubject{}, &richMediaResponse{Homepage: homepage})
	if sheet != "" {
		t.Fatalf("expected encoded delimiter homepage to be omitted, got %q", sheet)
	}
	rendered := descsvc.Render(sheet)
	if strings.Contains(rendered, "evil.test") || strings.Contains(rendered, "<a href=") {
		t.Fatalf("expected encoded delimiter homepage to be absent from rendered output, got %q", rendered)
	}
}

func TestDescriptionLinksStayTextOnly(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Identity: api.ExternalIdentity{
			Category: "MOVIE",
			IMDBID:   7654321,
			TMDBID:   123,
		},
		ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{Rating: 7.8}},
	}

	cast := buildCastSection(meta, []richCreditItem{
		{
			ID:          42,
			Name:        "Jane Example",
			Character:   "Hero",
			ProfilePath: "/profile.jpg",
		},
	})
	if cast != "[url=https://www.themoviedb.org/person/42?language=pt-BR]Jane Example[/url] como Hero" {
		t.Fatalf("unexpected cast section %q", cast)
	}

	ratings := buildRatingsBBCode(meta, &richMediaResponse{VoteAverage: 8.2})
	want := "[url=https://www.imdb.com/title/tt7654321]IMDb[/url]: 7.8/10\n[url=https://www.themoviedb.org/movie/123]TMDb[/url]: 8.2/10"
	if ratings != want {
		t.Fatalf("ratings = %q, want %q", ratings, want)
	}
	if hasForeignImages(cast + ratings) {
		t.Fatal("expected text-only cast and ratings sections")
	}
}

func TestStripForeignImagesKeepsSiteHostedImages(t *testing.T) {
	t.Parallel()

	input := "Notes\n[url=https://host.example/view][img]https://host.example/a.png[/img][/url]\n" +
		"[img=300]https://img.example/b.png[/img]\n[img]https://amigos-share.club/storage/torrent-images/x.webp[/img]"
	got := stripForeignImages(input)
	if strings.Contains(got, "example") {
		t.Fatalf("foreign images survived: %q", got)
	}
	if !strings.Contains(got, "https://amigos-share.club/storage/torrent-images/x.webp") {
		t.Fatalf("site-hosted image was removed: %q", got)
	}
	if !hasForeignImages(input) || hasForeignImages(got) {
		t.Fatalf("hasForeignImages misclassified input=%t output=%t", hasForeignImages(input), hasForeignImages(got))
	}
}
