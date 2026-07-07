// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestEvaluateRulesRequiresUniqueID(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true, ValidMediaInfo: false}
	failures := EvaluateRules(context.Background(), "AITHER", meta, nil)
	if len(failures) == 0 {
		t.Fatalf("expected rule failure")
	}
	if failures[0].Rule != "require_unique_id" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func hasMISettingsFailure(failures []api.RuleFailure) bool {
	for _, failure := range failures {
		if failure.Rule == "require_valid_mi_setting" {
			return true
		}
	}
	return false
}

func TestEvaluateRulesUnit3DEnforcesMediaInfoSettings(t *testing.T) {
	// RF is a known UNIT3D tracker but its RuleSet does not opt into
	// RequireValidMISetting; the UNIT3D upload rejects encodes without encoding
	// settings, so the rule must fire at prep time regardless.
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		ExternalIDs:            api.ExternalIDs{Category: "movie"},
		ValidMediaInfoSettings: false,
	}
	failures := EvaluateRules(context.Background(), "RF", meta, nil)
	if !hasMISettingsFailure(failures) {
		t.Fatalf("expected require_valid_mi_setting failure for RF, got %#v", failures)
	}
}

func TestEvaluateRulesUnit3DWithoutRuleSetEnforcesMediaInfoSettings(t *testing.T) {
	// ACM is a known UNIT3D tracker with no tracker-specific RuleSet. The early
	// "not found" return must not skip the MediaInfo-settings enforcement.
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true, ValidMediaInfoSettings: false}
	failures := EvaluateRules(context.Background(), "ACM", meta, nil)
	if !hasMISettingsFailure(failures) {
		t.Fatalf("expected require_valid_mi_setting failure for ACM, got %#v", failures)
	}

	meta.ValidMediaInfoSettings = true
	failures = EvaluateRules(context.Background(), "ACM", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures for ACM with valid MI settings, got %#v", failures)
	}
}

func TestEvaluateRulesUnit3DAllowsValidMediaInfoSettings(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		ExternalIDs:            api.ExternalIDs{Category: "movie"},
		ValidMediaInfoSettings: true,
	}
	failures := EvaluateRules(context.Background(), "RF", meta, nil)
	if hasMISettingsFailure(failures) {
		t.Fatalf("did not expect require_valid_mi_setting failure for RF, got %#v", failures)
	}
}

func TestEvaluateRulesNonUnit3DOptInMediaInfoSettings(t *testing.T) {
	// BHD is not a UNIT3D-upload tracker but opts in via its RuleSet, so the
	// per-tracker flag must still be honored.
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true, ValidMediaInfoSettings: false}
	failures := EvaluateRules(context.Background(), "BHD", meta, nil)
	if !hasMISettingsFailure(failures) {
		t.Fatalf("expected require_valid_mi_setting failure for BHD, got %#v", failures)
	}
}

func TestEvaluateRulesLanguageRulePasses(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		AudioLanguages:         []string{"french"},
		SubtitleLanguages:      nil,
		ValidMediaInfoSettings: true,
	}
	failures := EvaluateRules(context.Background(), "TOS", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesLanguageRuleMissingData(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true, ValidMediaInfoSettings: true}
	failures := EvaluateRules(context.Background(), "TOS", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "language_rule" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesLanguageRuleOriginalFallback(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
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
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
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
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
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
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
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
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true, ExternalIDs: api.ExternalIDs{Category: "tv"}}
	failures := EvaluateRules(context.Background(), "PTP", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_movie_only" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesPTPAllowsTVPacks(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true, ExternalIDs: api.ExternalIDs{Category: "tv"}, TVPack: true}
	failures := EvaluateRules(context.Background(), "PTP", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesANTRequiresMovie(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true, ExternalIDs: api.ExternalIDs{Category: "tv"}}
	failures := EvaluateRules(context.Background(), "ANT", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_movie_only" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesANTAllowsMovie(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true, ExternalIDs: api.ExternalIDs{Category: "movie"}}
	failures := EvaluateRules(context.Background(), "ANT", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesBHDRequiresValidMISettings(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true, ValidMediaInfoSettings: false}
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

	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
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
			meta:      api.PreparedMetadata{ValidMediaInfoTracks: true, Type: "WEBDL", Container: "avi", ValidMediaInfoSettings: true},
			wantBlock: true,
		},
		{
			name:      "hdtv allows ts",
			meta:      api.PreparedMetadata{ValidMediaInfoTracks: true, Type: "HDTV", Container: "ts", ValidMediaInfoSettings: true},
			wantBlock: false,
		},
		{
			name:      "dolby vision webdl allows mp4",
			meta:      api.PreparedMetadata{ValidMediaInfoTracks: true, Type: "WEBDL", Container: "mp4", WebDV: true, ValidMediaInfoSettings: true},
			wantBlock: false,
		},
		{
			name:      "disc skips container rule",
			meta:      api.PreparedMetadata{ValidMediaInfoTracks: true, DiscType: "BDMV", Type: "WEBDL", Container: "avi", ValidMediaInfoSettings: true},
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
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true, ExternalIDs: api.ExternalIDs{Category: "movie"}}
	failures := EvaluateRules(context.Background(), "NBL", meta, nil)
	if len(failures) != 2 {
		t.Fatalf("expected 2 failures, got %#v", failures)
	}
	if failures[0].Rule != "require_tv_only" {
		t.Fatalf("unexpected first rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesNBLAllowsTV(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true, ExternalIDs: api.ExternalIDs{Category: "tv"}}
	failures := EvaluateRules(context.Background(), "NBL", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure because language data is missing, got %#v", failures)
	}
	if failures[0].Rule != "language_rule" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesNBLAllowsTVWithOriginalAudioAndEnglishSubs(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
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
	bdmv := api.PreparedMetadata{ValidMediaInfoTracks: true,
		ExternalIDs: api.ExternalIDs{Category: "tv"},
		DiscType:    "BDMV",
	}
	if failures := EvaluateRules(context.Background(), "NBL", bdmv, nil); len(failures) != 0 {
		t.Fatalf("expected BDMV to skip NBL language rule, got %#v", failures)
	}

	dvd := api.PreparedMetadata{ValidMediaInfoTracks: true,
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

	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
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

	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		AudioLanguages:         []string{"English"},
		Release:                api.ReleaseInfo{Resolution: "1080p"},
		ValidMediaInfoSettings: true,
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

	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		AudioLanguages:         []string{"German"},
		Release:                api.ReleaseInfo{Resolution: "1080p"},
		ValidMediaInfoSettings: true,
	}
	failures := EvaluateRules(context.Background(), "RHD", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesRHDRequiresGermanAudioForDisc(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		Type:                   "DISC",
		DiscType:               "BDMV",
		AudioLanguages:         []string{"English"},
		Release:                api.ReleaseInfo{Resolution: "1080p"},
		ValidMediaInfoSettings: true,
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

	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		Type:                   "DISC",
		DiscType:               "BDMV",
		AudioLanguages:         []string{"German"},
		Release:                api.ReleaseInfo{Resolution: "1080p"},
		ValidMediaInfoSettings: true,
	}
	failures := EvaluateRules(context.Background(), "RHD", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesRHDRequiresSceneNFO(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		Scene:                  true,
		AudioLanguages:         []string{"German"},
		Release:                api.ReleaseInfo{Resolution: "1080p"},
		ValidMediaInfoSettings: true,
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

	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
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

	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		Scene:                  true,
		AudioLanguages:         []string{"French"},
		ValidMediaInfoSettings: true,
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
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		DiscType:               "",
		ValidMediaInfo:         true,
		ValidMediaInfoSettings: true,
		AudioLanguages:         []string{"Japanese"},
		SubtitleLanguages:      []string{"German"},
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
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		DiscType:               "BDMV",
		ValidMediaInfoSettings: true,
	}
	failures := EvaluateRules(context.Background(), "A4K", meta, nil)
	if len(failures) != 0 {
		t.Fatalf("expected no failures for disc upload, got %#v", failures)
	}
}

func TestEvaluateRulesLSTRequiresValidMIAndLanguage(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoTracks: true,
		DiscType:               "",
		ValidMediaInfoSettings: false,
	}
	failures := EvaluateRules(context.Background(), "LST", meta, nil)
	if len(failures) < 2 {
		t.Fatalf("expected at least 2 failures, got %#v", failures)
	}
}

func findRuleFailure(failures []api.RuleFailure, rule string) (api.RuleFailure, bool) {
	for _, f := range failures {
		if f.Rule == rule {
			return f, true
		}
	}
	return api.RuleFailure{}, false
}

func hasRuleFailure(failures []api.RuleFailure, rule string) bool {
	_, ok := findRuleFailure(failures, rule)
	return ok
}

func TestEvaluateRulesModifiedReleaseAcrossFamilies(t *testing.T) {
	t.Parallel()

	renamed := api.PreparedMetadata{ValidMediaInfoTracks: true,
		SourcePath: "/data/movies/Example Movie 2026 2160p MA WEB-DL DDP5 1 HDR H 265-GRP",
		Release:    api.ReleaseInfo{Group: "GRP"},
	}
	clean := api.PreparedMetadata{ValidMediaInfoTracks: true,
		SourcePath: "/data/movies/Example.Movie.2026.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-GRP",
		Release:    api.ReleaseInfo{Group: "GRP"},
	}

	// Covers a UNIT3D tracker (LST), a non-UNIT3D tracker (PTP), an AZ-family
	// tracker (PHD), and a tracker with no rule set of its own (HDB) to prove the
	// rule fires across every family.
	for _, tracker := range []string{"LST", "PTP", "PHD", "HDB"} {
		t.Run(tracker, func(t *testing.T) {
			t.Parallel()
			got := EvaluateRules(context.Background(), tracker, renamed, nil)
			failure, ok := findRuleFailure(got, "modified_release")
			if !ok {
				t.Fatalf("expected modified_release failure for %s, got %#v", tracker, got)
			}
			if !strings.Contains(failure.Reason, "renamed") {
				t.Fatalf("expected a meaningful reason mentioning 'renamed' for %s, got %q", tracker, failure.Reason)
			}
			if clean := EvaluateRules(context.Background(), tracker, clean, nil); hasRuleFailure(clean, "modified_release") {
				t.Fatalf("did not expect modified_release failure for clean release on %s, got %#v", tracker, clean)
			}
		})
	}
}

// TestEvaluateRulesPreservesNilForTrackerWithoutFailures guards the contract that
// applyTrackerRules relies on: a tracker with no rule set and no rule failure must
// return nil (not an empty slice), so the consumer preserves pre-existing failures
// (e.g. audio_bloat) instead of clearing them.
func TestEvaluateRulesPreservesNilForTrackerWithoutFailures(t *testing.T) {
	t.Parallel()

	clean := api.PreparedMetadata{ValidMediaInfoTracks: true,
		SourcePath: "/data/movies/Example.Movie.2026.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-GRP",
		Release:    api.ReleaseInfo{Group: "GRP"},
	}
	// MTV is non-UNIT3D, not PTP, and has no rule set of its own.
	if got := EvaluateRules(context.Background(), "MTV", clean, nil); got != nil {
		t.Fatalf("expected nil for a tracker without failures, got %#v", got)
	}
}

func TestEvaluateRulesModifiedReleaseExemptsManual(t *testing.T) {
	t.Parallel()

	renamed := api.PreparedMetadata{ValidMediaInfoTracks: true,
		SourcePath: "/data/movies/Example Movie 2026 2160p MA WEB-DL DDP5 1 HDR H 265-GRP",
		Release:    api.ReleaseInfo{Group: "GRP"},
	}
	if got := EvaluateRules(context.Background(), "MANUAL", renamed, nil); hasRuleFailure(got, "modified_release") {
		t.Fatalf("expected MANUAL to be exempt from modified_release, got %#v", got)
	}
}
