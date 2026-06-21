// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metautil

import "testing"

func TestTranslateGenreToPortuguese(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "action", input: "action", want: "ação"},
		{name: "action & adventure upper", input: "ACTION & ADVENTURE", want: "ação e aventura"},
		{name: "drama unchanged", input: "drama", want: "drama"},
		{name: "unknown unchanged", input: "some-unknown-genre", want: "some-unknown-genre"},
		{name: "comedy with space", input: "  Comedy  ", want: "comédia"},
		{name: "sci-fi", input: "sci-fi", want: "ficção científica"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TranslateGenreToPortuguese(tt.input); got != tt.want {
				t.Fatalf("TranslateGenreToPortuguese(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCapitalizeGenre(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "acao", input: "ação", want: "Ação"},
		{name: "action & adventure", input: "ação e aventura", want: "Ação e aventura"},
		{name: "comedy", input: "comedy", want: "Comedy"},
		{name: "with spaces", input: "  drama  ", want: "Drama"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CapitalizeGenre(tt.input); got != tt.want {
				t.Fatalf("CapitalizeGenre(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTranslateGenreToPortugueseStrict(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "action", input: "action", want: "ação"},
		{name: "action & adventure upper", input: "ACTION & ADVENTURE", want: "ação e aventura"},
		{name: "drama", input: "drama", want: "drama"},
		{name: "unknown returns empty", input: "some-unknown-genre", want: ""},
		{name: "comedy with space", input: "  Comedy  ", want: "comédia"},
		{name: "sci-fi", input: "sci-fi", want: "ficção científica"},
		{name: "adult", input: "adult", want: "adulto"},
		{name: "film-noir", input: "Film Noir", want: "filme noir"},
		{name: "musical", input: "musical", want: "musical"},
		{name: "talk-show", input: "talk-show", want: "talk show"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TranslateGenreToPortugueseStrict(tt.input); got != tt.want {
				t.Fatalf("TranslateGenreToPortugueseStrict(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestISO3166PortugueseName(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		fallback string
		want     string
	}{
		{name: "empty", code: "", fallback: "Unknown", want: "Unknown"},
		{name: "US to PT", code: "US", fallback: "US", want: "Estados Unidos da América"},
		{name: "US lowercase", code: "us", fallback: "US", want: "Estados Unidos da América"},
		{name: "BR to PT", code: "BR", fallback: "BR", want: "Brasil"},
		{name: "unknown fallback", code: "XYZ", fallback: "XYZ", want: "XYZ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ISO3166PortugueseName(tt.code, tt.fallback); got != tt.want {
				t.Fatalf("ISO3166PortugueseName(%q, %q) = %q, want %q", tt.code, tt.fallback, got, tt.want)
			}
		})
	}
}
