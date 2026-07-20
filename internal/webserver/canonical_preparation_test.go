// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCanonicalPreparationUsesSharedRequestMapper(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value string
		want  api.CanonicalCategory
	}{
		{
			name:  "value",
			value: "TV",
			want:  api.CanonicalCategoryTV,
		},
		{
			name:  "explicit clear",
			value: "",
			want:  api.CanonicalCategoryUnknown,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input, err := api.MapPreparationRequest(api.Request{
				SourcePath:           "Example.Release.2026.mkv",
				ReleaseNameOverrides: api.ReleaseNameOverrides{Category: &test.value},
			}, api.PreparationIntentPreview)
			if err != nil {
				t.Fatalf("prepare input: %v", err)
			}
			if input.Instructions.Category == nil || *input.Instructions.Category != test.want {
				t.Fatalf("category = %#v, want %q", input.Instructions.Category, test.want)
			}
		})
	}
}

func TestCanonicalPreparationRejectsInvalidCategoryInstruction(t *testing.T) {
	t.Parallel()
	category := "music"
	_, err := api.MapPreparationRequest(api.Request{
		SourcePath:           "Example.Release.2026.mkv",
		ReleaseNameOverrides: api.ReleaseNameOverrides{Category: &category},
	}, api.PreparationIntentPreview)
	if err == nil {
		t.Fatal("expected invalid category error")
	}
}
