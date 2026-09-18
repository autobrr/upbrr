// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rf

import (
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRFStructuredReleaseNamePolicyIsCanonical(t *testing.T) {
	t.Parallel()
	result := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Release",
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
	if got := rfReviewedName(t, subject, nil); got != result.Name {
		t.Fatalf("canonical name = %q, want %q", got, result.Name)
	}
	manual := "Manual RF Name-GRP"
	if got := rfReviewedName(t, subject, &manual); got != manual {
		t.Fatalf("opaque name = %q, want %q", got, manual)
	}
	policy := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if policy.ID != "unit3d/rf/v3" || policy.Structured == nil || policy.Resolver != nil {
		t.Fatalf("RF policy = %#v", policy)
	}
}

func rfReviewedName(t *testing.T, subject api.UploadSubject, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "RF",
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
