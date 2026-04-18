// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metautil

import "testing"

func TestReleaseCategoryFromRLS(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "movie", input: "movie", want: "MOVIE"},
		{name: "episode", input: "episode", want: "TV"},
		{name: "season pack", input: "SEASONPACK", want: "TV"},
		{name: "tv show hyphen", input: "tv-show", want: "TV"},
		{name: "unknown", input: "documentary", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReleaseCategoryFromRLS(tt.input); got != tt.want {
				t.Fatalf("ReleaseCategoryFromRLS(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
