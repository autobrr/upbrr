// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lt

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProfileCategoryID(t *testing.T) {
	profile := Profile().Site

	// Movie
	if got := profile.ResolveCategoryID(api.UploadSubject{Type: "WEBDL", Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie}}); got != "1" {
		t.Fatalf("expected Movie category ID 1, got %q", got)
	}

	// TV
	if got := profile.ResolveCategoryID(api.UploadSubject{Type: "WEBDL", Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV}}); got != "2" {
		t.Fatalf("expected TV category ID 2, got %q", got)
	}

	// Anime
	if got := profile.ResolveCategoryID(api.UploadSubject{Type: "WEBDL", Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV}, Anime: true}); got != "5" {
		t.Fatalf("expected Anime category ID 5, got %q", got)
	}

	// Soap/Telenovela
	soapMeta := api.UploadSubject{
		Type:     "WEBDL",
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				Keywords: "telenovela",
			},
		},
	}
	if got := profile.ResolveCategoryID(soapMeta); got != "8" {
		t.Fatalf("expected Soap category ID 8, got %q", got)
	}

	// Turkish/Asian drama
	dramaMeta := api.UploadSubject{
		Type:     "WEBDL",
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				Genres:        "drama",
				OriginCountry: []string{"TR"},
			},
		},
	}
	if got := profile.ResolveCategoryID(dramaMeta); got != "20" {
		t.Fatalf("expected Drama category ID 20, got %q", got)
	}
}

func TestProfileName(t *testing.T) {
	profile := Profile().Site

	// Title stripping and clean dubs
	meta := api.UploadSubject{
		ReleaseName:  "The.World.Is.Dancing.S01E04.Differing.Scales.of.Progress.Dual-Audio.1080p.WEB-DL-kotopi",
		Tag:          "kotopi",
		Identity:     api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		EpisodeTitle: "Differing Scales of Progress",
	}
	got := profile.BuildName(meta, config.TrackerConfig{})
	want := "The.World.Is.Dancing.S01E04.1080p.WEB-DL [SUBS]-kotopi"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}

	// Original Spanish title mapping
	metaES := api.UploadSubject{
		ReleaseName: "La.Casa.de.Papel.S01E01.1080p.WEBDL-GRP",
		Tag:         "GRP",
		Identity:    api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		AudioLanguages: []string{"Spanish"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				Title:            "Money Heist",
				OriginalTitle:    "La Casa de Papel",
				OriginalLanguage: "es",
			},
		},
	}
	gotES := profile.BuildName(metaES, config.TrackerConfig{})
	wantES := "La.Casa.de.Papel.S01E01.1080p.WEBDL-GRP" // keeps Spanish
	if gotES != wantES {
		t.Fatalf("expected %q, got %q", wantES, gotES)
	}
}

func TestRulesSpanishRequirements(t *testing.T) {
	reg := trackers.NewRegistry()
	profile := Profile()
	definition := unit3d.NewWithProfile(profile)
	if err := reg.Register(definition); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Helper to create valid base rule subject that passes default unit3d metadata requirements
	newRuleSubject := func() api.RuleSubject {
		return api.RuleSubject{
			Identity: api.ExternalIdentity{
				TMDBID:   1,
				Category: api.CanonicalCategoryTV,
			},
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{
					TMDBID: 1,
					Title:  "Example",
				},
			},
			Assessments: api.ReleaseAssessments{
				MediaInfoEncodeSettings: api.EncodeSettingsStatusNotApplicable,
			},
		}
	}

	// 1. Spanish Audio - Should Pass
	metaAudio := newRuleSubject()
	metaAudio.AudioLanguages = []string{"Spanish"}
	failures, err := trackers.EvaluateRulesWithRegistry(context.Background(), reg, "LT", metaAudio, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(failures) > 0 {
		t.Fatalf("expected pass, got failures: %+v", failures)
	}

	// 2. Spanish Subtitles - Should Pass
	metaSubs := newRuleSubject()
	metaSubs.SubtitleLanguages = []string{"es"}
	failures, err = trackers.EvaluateRulesWithRegistry(context.Background(), reg, "LT", metaSubs, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(failures) > 0 {
		t.Fatalf("expected pass, got failures: %+v", failures)
	}

	// 3. Neither Spanish Audio nor Subtitles - Should Fail
	metaNeither := newRuleSubject()
	metaNeither.AudioLanguages = []string{"English"}
	metaNeither.SubtitleLanguages = []string{"French"}
	failures, err = trackers.EvaluateRulesWithRegistry(context.Background(), reg, "LT", metaNeither, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(failures) == 0 {
		t.Fatalf("expected failure for neither language")
	}

	hasLanguageRuleFailure := false
	for _, f := range failures {
		if f.Rule == "language_rule" {
			hasLanguageRuleFailure = true
			break
		}
	}
	if !hasLanguageRuleFailure {
		t.Fatalf("expected language_rule failure, got: %+v", failures)
	}
}
