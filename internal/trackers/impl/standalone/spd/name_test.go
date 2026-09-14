// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSPDStructuredNameNormalizesComponents(t *testing.T) {
	result := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example: Movie",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	}, api.NopLogger{})
	subject := api.UploadSubject{
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
	}
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "SPD", Meta: subject}, Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	if want := "Example - Movie 2026 1080p WEB-DL-GRP"; name != want {
		t.Fatalf("SPD name = %q, want %q", name, want)
	}
}
