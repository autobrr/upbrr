// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import (
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestOEStructuredReleaseNamePolicyUsesGroupRole(t *testing.T) {
	t.Parallel()
	subject := oeGeneratedSubject(t, "")
	if got, want := oeReviewedName(t, subject, nil), "Example Movie 2026 1080p WEB-DL-NOGRP"; got != want {
		t.Fatalf("no-group name = %q, want %q", got, want)
	}
	known := oeGeneratedSubject(t, "-GRP")
	if got := oeReviewedName(t, known, nil); got != known.ReleaseName {
		t.Fatalf("known group changed: %q", got)
	}
	manual := subject
	markOEGroupManual(t, manual.GeneratedName)
	manual.ReleaseName = manual.GeneratedName.Render().Name
	if got := oeReviewedName(t, manual, nil); got != manual.ReleaseName {
		t.Fatalf("manual group changed: %q", got)
	}
	override := "Manual OE Name-GRP"
	if got := oeReviewedName(t, subject, &override); got != override {
		t.Fatalf("opaque override = %q, want %q", got, override)
	}
	policy := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if policy.ID != "unit3d/oe/v2" || policy.Structured == nil || policy.Resolver != nil {
		t.Fatalf("OE policy = %#v", policy)
	}
}

func oeGeneratedSubject(t *testing.T, tag string) api.UploadSubject {
	t.Helper()
	result := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Movie",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		Tag:        tag,
	}, api.NopLogger{})
	if result.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a structured document")
	}
	return api.UploadSubject{
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
		Tag:              tag,
	}
}

func oeReviewedName(t *testing.T, subject api.UploadSubject, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "OE",
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

func markOEGroupManual(t *testing.T, document *api.ReleaseNameDocument) {
	t.Helper()
	for index := range document.Components {
		if document.Components[index].Role == api.NameRoleGroup {
			document.Components[index].Manual = true
			return
		}
	}
	t.Fatal("generated document missing group")
}
