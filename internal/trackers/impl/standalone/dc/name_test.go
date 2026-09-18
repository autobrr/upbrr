// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDCStructuredNameUsesTechnicalRolesAndExactScenePresentation(t *testing.T) {
	result := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "DD+ Story",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		Audio:      "DD+ 5.1 DTS:",
		HDR:        "HDR10+",
		Tag:        "-GRP",
	}, api.NopLogger{})
	subject := api.UploadSubject{
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
	}
	if got, want := dcReviewedName(t, subject), "DD Story 2026 1080p WEB-DL DDP 5.1 DTS- HDR10P-GRP"; got != want {
		t.Fatalf("DC name = %q, want %q", got, want)
	}
	subject.Scene, subject.SceneName = true, "Exact.Scene.Name-GRP"
	if got, want := dcReviewedName(t, subject), "Exact.Scene.Name-GRP [UNRAR]"; got != want {
		t.Fatalf("DC scene = %q, want %q", got, want)
	}
}

func dcReviewedName(t *testing.T, subject api.UploadSubject) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "DC", Meta: subject}, Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func TestDCPreservesCanonicalCleanCharacterSubstitutions(t *testing.T) {
	generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "A<B>C:D\"E/F\\G|H?I*J",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	}, api.NopLogger{})
	subject := api.UploadSubject{
		ReleaseName:      generated.Name,
		ReleaseNameClean: generated.CleanName,
		ReleaseNameNoTag: generated.NameNoTag,
		GeneratedName:    generated.GeneratedName,
	}
	want := generated.CleanName
	if !strings.HasPrefix(want, "A-B-C-D-E-F-G-H-I-J") || generated.Name == generated.CleanName {
		t.Fatalf("invalid clean-name fixture: name=%q clean=%q", generated.Name, generated.CleanName)
	}
	if got := dcReviewedName(t, subject); got != want {
		t.Fatalf("name=%q want=%q", got, want)
	}
}
