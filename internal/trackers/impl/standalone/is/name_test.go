// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestISSceneNameOverridesGeneratedName(t *testing.T) {
	t.Parallel()
	generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Film",
		Year:       2026,
		Resolution: "1080p",
		Source:     "WEB-DL",
		Tag:        "-GRP",
	}, api.NopLogger{})
	if generated.GeneratedName == nil {
		t.Fatal("generated name has no document")
	}
	subject := api.UploadSubject{
		ReleaseName:   generated.Name,
		GeneratedName: generated.GeneratedName,
		Scene:         true,
		SceneName:     "Different.Scene.Name.2026.1080p.WEB-DL-GRP",
	}
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker: "IS",
		Meta:    subject,
	}, New().ReleaseNamePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	if got, want := prepared.Projection.UploadReleaseName, subject.SceneName; got != want {
		t.Fatalf("IS scene name = %q, want %q", got, want)
	}
}
