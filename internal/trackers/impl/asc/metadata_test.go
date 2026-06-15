// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveGenresPreservesUnknownGenres(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Release: api.ReleaseInfo{
			Genre: "Sci-Fi,MyCustomGenre",
		},
	}
	answers := map[string]string{}

	got := resolveGenres(meta, answers)
	expected := "Ficção científica, MyCustomGenre"
	if got != expected {
		t.Fatalf("expected genres %q, got %q", expected, got)
	}
}
