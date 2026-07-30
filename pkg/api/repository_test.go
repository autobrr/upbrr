// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestNormalizeCategory(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Category
		valid bool
	}{
		{
			name:  "movie",
			input: "movie",
			want:  CategoryMovie,
			valid: true,
		},
		{
			name:  "film alias",
			input: "Film",
			want:  CategoryMovie,
			valid: true,
		},
		{
			name:  "tv series alias",
			input: "tv-show",
			want:  CategoryTV,
			valid: true,
		},
		{
			name:  "episode alias",
			input: "episode",
			want:  CategoryTV,
			valid: true,
		},
		{
			name:  "empty",
			input: "",
			want:  CategoryUnknown,
			valid: false,
		},
		{
			name:  "unsupported",
			input: "Music",
			want:  Category("Music"),
			valid: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeCategory(tc.input)
			if got != tc.want {
				t.Fatalf("NormalizeCategory(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if got.IsValid() != tc.valid {
				t.Fatalf("NormalizeCategory(%q).IsValid() = %t, want %t", tc.input, got.IsValid(), tc.valid)
			}
		})
	}
}

func TestCategoryScanAndValue(t *testing.T) {
	var category Category
	if err := category.Scan([]byte("TV")); err != nil {
		t.Fatalf("Scan([]byte) failed: %v", err)
	}
	if category != CategoryTV {
		t.Fatalf("expected scanned category %q, got %q", CategoryTV, category)
	}

	value, err := Category(" film ").Value()
	if err != nil {
		t.Fatalf("Value() failed: %v", err)
	}
	if value != string(CategoryMovie) {
		t.Fatalf("expected canonical db value %q, got %#v", CategoryMovie, value)
	}
}
