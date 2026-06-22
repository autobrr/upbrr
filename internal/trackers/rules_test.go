// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestEvaluateRulesRequiresUniqueID(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfo: false}
	failures := EvaluateRules(context.Background(), "AITHER", meta, nil)
	if len(failures) == 0 {
		t.Fatalf("expected rule failure")
	}
	if failures[0].Rule != "require_unique_id" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesLanguageRulePasses(t *testing.T) {
	meta := api.PreparedMetadata{
		AudioLanguages:    []string{"french"},
		SubtitleLanguages: nil,
	}
	failures := EvaluateRules(context.Background(), "TOS", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesLanguageRuleMissingData(t *testing.T) {
	meta := api.PreparedMetadata{}
	failures := EvaluateRules(context.Background(), "TOS", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "language_rule" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesLanguageRuleOriginalFallback(t *testing.T) {
	meta := api.PreparedMetadata{
		AudioLanguages:         []string{"Japanese"},
		SubtitleLanguages:      []string{"English"},
		ValidMediaInfoSettings: true,
		Container:              "mkv",
		Release:                api.ReleaseInfo{Resolution: "720p"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}
	failures := EvaluateRules(context.Background(), "LUME", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesLUMERequiresMKVForNonDisc(t *testing.T) {
	meta := api.PreparedMetadata{
		Container:              "mp4",
		AudioLanguages:         []string{"English"},
		SubtitleLanguages:      []string{"English"},
		ValidMediaInfoSettings: true,
		Release:                api.ReleaseInfo{Resolution: "720p"},
	}
	failures := EvaluateRules(context.Background(), "LUME", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "extra_check" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
	if failures[0].Reason != "LUME only allows MKV containers for non-disc uploads." {
		t.Fatalf("unexpected failure reason: %s", failures[0].Reason)
	}
}

func TestEvaluateRulesLUMEAllowsMKVForNonDisc(t *testing.T) {
	meta := api.PreparedMetadata{
		Container:              "mkv",
		AudioLanguages:         []string{"English"},
		SubtitleLanguages:      []string{"English"},
		ValidMediaInfoSettings: true,
		Release:                api.ReleaseInfo{Resolution: "720p"},
	}
	failures := EvaluateRules(context.Background(), "LUME", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesLUMESkipsContainerRuleForDisc(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:               "BDMV",
		Container:              "mp4",
		ValidMediaInfoSettings: true,
		Release:                api.ReleaseInfo{Resolution: "480p"},
	}
	failures := EvaluateRules(context.Background(), "LUME", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected disc upload to skip LUME container and resolution rules, got %#v", failures)
	}
}

func TestEvaluateRulesPTPRequiresMovieForNonPackTV(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "tv"}}
	failures := EvaluateRules(context.Background(), "PTP", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_movie_only" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesPTPAllowsTVPacks(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "tv"}, TVPack: true}
	failures := EvaluateRules(context.Background(), "PTP", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesANTRequiresMovie(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "tv"}}
	failures := EvaluateRules(context.Background(), "ANT", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_movie_only" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesANTAllowsMovie(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "movie"}}
	failures := EvaluateRules(context.Background(), "ANT", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesBHDRequiresValidMISettings(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoSettings: false}
	failures := EvaluateRules(context.Background(), "BHD", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_valid_mi_setting" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}

	meta.ValidMediaInfoSettings = true
	failures = EvaluateRules(context.Background(), "BHD", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesBHDRejectsInvalidContainerForUploadTypes(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ValidMediaInfoSettings: true,
		Type:                   "REMUX",
		Container:              "avi",
	}
	failures := EvaluateRules(context.Background(), "BHD", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "extra_check" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}

	meta.Container = "mkv"
	failures = EvaluateRules(context.Background(), "BHD", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures for MKV, got %#v", failures)
	}
}

func TestEvaluateRulesBLUContainerRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		meta      api.PreparedMetadata
		wantBlock bool
	}{
		{
			name:      "non disc defaults to mkv",
			meta:      api.PreparedMetadata{Type: "WEBDL", Container: "avi"},
			wantBlock: true,
		},
		{
			name:      "hdtv allows ts",
			meta:      api.PreparedMetadata{Type: "HDTV", Container: "ts"},
			wantBlock: false,
		},
		{
			name:      "dolby vision webdl allows mp4",
			meta:      api.PreparedMetadata{Type: "WEBDL", Container: "mp4", WebDV: true},
			wantBlock: false,
		},
		{
			name:      "disc skips container rule",
			meta:      api.PreparedMetadata{DiscType: "BDMV", Type: "WEBDL", Container: "avi"},
			wantBlock: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			failures := EvaluateRules(context.Background(), "BLU", tc.meta, nil)
			if tc.wantBlock && len(failures) == 0 {
				t.Fatalf("expected BLU container failure")
			}
			if !tc.wantBlock && len(failures) != 0 {
				t.Fatalf("expected no BLU container failures, got %#v", failures)
			}
		})
	}
}

func TestEvaluateRulesNBLRequiresTV(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "movie"}}
	failures := EvaluateRules(context.Background(), "NBL", meta, nil)
	if len(failures) != 2 {
		t.Fatalf("expected 2 failures, got %#v", failures)
	}
	if failures[0].Rule != "require_tv_only" {
		t.Fatalf("unexpected first rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesNBLAllowsTV(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "tv"}}
	failures := EvaluateRules(context.Background(), "NBL", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure because language data is missing, got %#v", failures)
	}
	if failures[0].Rule != "language_rule" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesNBLAllowsTVWithOriginalAudioAndEnglishSubs(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs:       api.ExternalIDs{Category: "tv"},
		DiscType:          "",
		AudioLanguages:    []string{"Japanese"},
		SubtitleLanguages: []string{"English"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}
	failures := EvaluateRules(context.Background(), "NBL", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesNBLSkipsLanguageRuleForBDMVOnly(t *testing.T) {
	bdmv := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "tv"},
		DiscType:    "BDMV",
	}
	if failures := EvaluateRules(context.Background(), "NBL", bdmv, nil); len(failures) != 0 {
		t.Fatalf("expected BDMV to skip NBL language rule, got %#v", failures)
	}

	dvd := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "tv"},
		DiscType:    "DVD",
	}
	failures := EvaluateRules(context.Background(), "NBL", dvd, nil)
	if len(failures) != 1 {
		t.Fatalf("expected DVD to require NBL language data, got %#v", failures)
	}
	if failures[0].Rule != "language_rule" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesDPDoesNotSpecialCaseFGTEncodes(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Tag:               "-FGT",
		Type:              "ENCODE",
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
	}
	failures := EvaluateRules(context.Background(), "DP", meta, nil)
	for _, failure := range failures {
		if failure.Rule == "block_group" {
			t.Fatalf("expected FGT to be handled as a banned group, got rule failure %#v", failures)
		}
	}
}

func TestEvaluateRulesRHDRequiresGermanAudio(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		AudioLanguages: []string{"English"},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
	}
	failures := EvaluateRules(context.Background(), "RHD", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "language_rule" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesRHDAllowsGermanAudio(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		AudioLanguages: []string{"German"},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
	}
	failures := EvaluateRules(context.Background(), "RHD", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesRHDRequiresGermanAudioForDisc(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Type:           "DISC",
		DiscType:       "BDMV",
		AudioLanguages: []string{"English"},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
	}
	failures := EvaluateRules(context.Background(), "RHD", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "language_rule" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesRHDAllowsGermanAudioForDisc(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Type:           "DISC",
		DiscType:       "BDMV",
		AudioLanguages: []string{"German"},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
	}
	failures := EvaluateRules(context.Background(), "RHD", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesRHDRequiresSceneNFO(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Scene:          true,
		AudioLanguages: []string{"German"},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
	}
	failures := EvaluateRules(context.Background(), "RHD", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_scene_nfo" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesRHDAllowsNonSceneWithoutNFO(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		AudioLanguages: []string{"German"},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
	}
	failures := EvaluateRules(context.Background(), "RHD", meta, nil)
	for _, failure := range failures {
		if failure.Rule == "require_scene_nfo" {
			t.Fatalf("expected non-scene upload to avoid NFO blocker, got %#v", failures)
		}
	}
}

func TestEvaluateRulesTOSRequiresSceneNFO(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Scene:          true,
		AudioLanguages: []string{"French"},
	}
	failures := EvaluateRules(context.Background(), "TOS", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_scene_nfo" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesAitherRequiresLanguageForNonDisc(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:          "",
		ValidMediaInfo:    true,
		AudioLanguages:    []string{"Japanese"},
		SubtitleLanguages: []string{"German"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}
	failures := EvaluateRules(context.Background(), "AITHER", meta, nil)
	if len(failures) == 0 {
		t.Fatalf("expected language failure")
	}
	if failures[0].Rule != "language_rule" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesA4KSkipsLanguageRuleForDisc(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType: "BDMV",
	}
	failures := EvaluateRules(context.Background(), "A4K", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures for disc upload, got %#v", failures)
	}
}

func TestEvaluateRulesLSTRequiresValidMIAndLanguage(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:               "",
		ValidMediaInfoSettings: false,
	}
	failures := EvaluateRules(context.Background(), "LST", meta, nil)
	if len(failures) < 2 {
		t.Fatalf("expected at least 2 failures, got %#v", failures)
	}
}
