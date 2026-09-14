// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package znth

import (
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestZNTHStructuredReleaseNamePolicyOmitsOnlyEpisodeTitleRole(t *testing.T) {
	t.Parallel()
	result := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:     "TV",
		Type:         "WEBDL",
		Title:        "Episode Title Show",
		Season:       "S01",
		Episode:      "E02",
		EpisodeTitle: "Episode Title",
		Resolution:   "1080p",
		Source:       "Web",
		Tag:          "-GRP",
	}, api.NopLogger{})
	subject := api.UploadSubject{
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryTV},
	}
	if got, want := znthReviewedName(t, subject, nil), "Episode Title Show S01E02 1080p WEB-DL-GRP"; got != want {
		t.Fatalf("ZNTH name = %q, want %q", got, want)
	}
	movie := subject
	movie.Identity.Category = api.CanonicalCategoryMovie
	if got := znthReviewedName(t, movie, nil); got != movie.ReleaseName {
		t.Fatalf("movie episode title changed: %q", got)
	}
	manual := subject
	markZNTHManual(t, manual.GeneratedName, api.NameRoleEpisodeTitle)
	manual.ReleaseName = manual.GeneratedName.Render().Name
	if got := znthReviewedName(t, manual, nil); got != manual.ReleaseName {
		t.Fatalf("manual episode title changed: %q", got)
	}
	override := "Manual ZNTH Name-GRP"
	if got := znthReviewedName(t, subject, &override); got != override {
		t.Fatalf("opaque override = %q, want %q", got, override)
	}
	policy := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if policy.ID != "unit3d/znth/v2" || policy.Structured == nil || policy.Resolver != nil {
		t.Fatalf("ZNTH policy = %#v", policy)
	}
}

func znthReviewedName(t *testing.T, subject api.UploadSubject, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "ZNTH",
		Meta:                subject,
		RequestedUploadName: requested,
	}, unit3d.NewWithProfile(Profile()).ReleaseNamePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func markZNTHManual(t *testing.T, document *api.ReleaseNameDocument, role api.ReleaseNameRole) {
	t.Helper()
	for index := range document.Components {
		if document.Components[index].Role == role {
			document.Components[index].Manual = true
			return
		}
	}
	t.Fatalf("generated document missing %s", role)
}
