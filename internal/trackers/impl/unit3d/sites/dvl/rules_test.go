// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dvl

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func subject(genres, keywords string) api.TrackerValidationSubject {
	const sourcePath = "Example.Release.2026.1080p-GRP"
	return api.TrackerValidationSubject{
		SourcePath: sourcePath,
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 4,
			Category:   api.CanonicalCategoryMovie,
			TMDBID:     1234567,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: 4,
			TMDB: &api.TMDBMetadata{
				TMDBID:   1234567,
				Title:    "Example Release",
				Genres:   genres,
				Keywords: keywords,
			},
		},
	}
}

func TestHorrorGenrePasses(t *testing.T) {
	t.Parallel()
	failures, err := checkContent(context.Background(), subject("Horror, Mystery", ""), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if hasRule(failures, "genre") {
		t.Fatalf("exact horror genre was refused: %#v", failures)
	}
}

func TestCompoundHorrorKeywordPasses(t *testing.T) {
	t.Parallel()
	failures, err := checkContent(context.Background(), subject("Drama, Thriller", "psychological horror"), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if hasRule(failures, "genre") {
		t.Fatalf("compound horror keyword was refused: %#v", failures)
	}
}

func TestHorrorOnlyInKeywordsPasses(t *testing.T) {
	t.Parallel()
	failures, err := checkContent(context.Background(), subject("Crime, Drama", "serial killer, horror"), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if hasRule(failures, "genre") {
		t.Fatalf("horror keyword was refused: %#v", failures)
	}
}

func TestNonHorrorIsRefused(t *testing.T) {
	t.Parallel()
	failures, err := checkContent(context.Background(), subject("Action, Comedy", ""), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !hasRule(failures, "genre") {
		t.Fatalf("non-horror content was not refused: %#v", failures)
	}
}

func TestAdultContentIsWaivable(t *testing.T) {
	t.Parallel()
	failures, err := checkContent(context.Background(), subject("Horror", "porn"), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, failure := range failures {
		if failure.Rule == "block_adult" {
			if failure.Disposition != api.RuleDispositionWaivable {
				t.Fatalf("adult screen is not waivable: %#v", failure)
			}
			return
		}
	}
	t.Fatalf("adult keyword was not flagged: %#v", failures)
}

func TestIncidentalMatureKeywordsPass(t *testing.T) {
	t.Parallel()
	failures, err := checkContent(context.Background(), subject("Horror, Thriller", "adult animation, orgy, erotic"), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if hasRule(failures, "block_adult") {
		t.Fatalf("incidental keywords were flagged as porn: %#v", failures)
	}
}

func TestAdultGenreFromProviderIsFlagged(t *testing.T) {
	t.Parallel()
	failures, err := checkContent(context.Background(), subject("Horror, Adult", ""), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !hasRule(failures, "block_adult") {
		t.Fatalf("adult provider genre was not flagged: %#v", failures)
	}
}

func hasRule(failures []api.RuleFailure, rule string) bool {
	for _, failure := range failures {
		if failure.Rule == rule {
			return true
		}
	}
	return false
}
