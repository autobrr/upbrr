// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metautil

import "testing"

func TestNormalizeIMDbIDPadsValidNumericForms(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"456":       "tt0000456",
		"tt456":     "tt0000456",
		"TT456":     "tt0000456",
		"Tt456":     "tt0000456",
		"12345678":  "tt12345678",
		"malformed": "malformed",
		"0":         "",
	} {
		if got := NormalizeIMDbID(input); got != want {
			t.Fatalf("NormalizeIMDbID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestReleaseCategoryFromRLS(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "movie",
			input: "movie",
			want:  "MOVIE",
		},
		{
			name:  "movie uppercase",
			input: "MOVIE",
			want:  "MOVIE",
		},
		{
			name:  "movie title case",
			input: "Movie",
			want:  "MOVIE",
		},
		{
			name:  "movie surrounding spaces",
			input: " movie ",
			want:  "MOVIE",
		},
		{
			name:  "movie surrounding whitespace",
			input: "\tmovie\n",
			want:  "MOVIE",
		},
		{
			name:  "episode",
			input: "episode",
			want:  "TV",
		},
		{
			name:  "episode uppercase",
			input: "EPISODE",
			want:  "TV",
		},
		{
			name:  "episode title case",
			input: "Episode",
			want:  "TV",
		},
		{
			name:  "episode surrounding spaces",
			input: " episode ",
			want:  "TV",
		},
		{
			name:  "season pack",
			input: "SEASONPACK",
			want:  "TV",
		},
		{
			name:  "season pack lowercase",
			input: "seasonpack",
			want:  "TV",
		},
		{
			name:  "season pack surrounding whitespace",
			input: "\tSEASONPACK\n",
			want:  "TV",
		},
		{
			name:  "tv show hyphen",
			input: "tv-show",
			want:  "TV",
		},
		{
			name:  "tv show hyphen uppercase",
			input: "TV-SHOW",
			want:  "TV",
		},
		{
			name:  "tv show hyphen surrounding whitespace",
			input: " tv-show ",
			want:  "TV",
		},
		{
			name:  "unknown",
			input: "documentary",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReleaseCategoryFromRLS(tt.input); got != tt.want {
				t.Fatalf("ReleaseCategoryFromRLS(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
