// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func evaluateNonMetadataRulesForTest(ctx context.Context, tracker string, meta api.PreparedMetadata) []api.RuleFailure {
	if strings.TrimSpace(meta.ExternalIDs.Category) == "" {
		switch strings.ToUpper(strings.TrimSpace(tracker)) {
		case "NBL", "BTN":
			meta.ExternalIDs.Category = "TV"
		default:
			meta.ExternalIDs.Category = "MOVIE"
		}
	}
	meta.ExternalIDs.TMDBID = 1
	meta.ExternalIDs.IMDBID = 1234567
	meta.ExternalIDs.TVDBID = 1
	meta.ExternalIDs.TVmazeID = 1
	if meta.ExternalMetadata.TMDB == nil {
		meta.ExternalMetadata.TMDB = &api.TMDBMetadata{}
	}
	meta.ExternalMetadata.TMDB.TMDBID = 1
	if strings.TrimSpace(meta.ExternalMetadata.TMDB.Title) == "" {
		meta.ExternalMetadata.TMDB.Title = "Example Release"
	}
	if meta.ExternalMetadata.IMDB == nil {
		meta.ExternalMetadata.IMDB = &api.IMDBMetadata{}
	}
	meta.ExternalMetadata.IMDB.IMDBID = 1234567
	if strings.TrimSpace(meta.ExternalMetadata.IMDB.Title) == "" {
		meta.ExternalMetadata.IMDB.Title = "Example Release"
	}
	if meta.ExternalMetadata.TVDB == nil {
		meta.ExternalMetadata.TVDB = &api.TVDBMetadata{}
	}
	meta.ExternalMetadata.TVDB.TVDBID = 1
	if strings.TrimSpace(meta.ExternalMetadata.TVDB.Name) == "" {
		meta.ExternalMetadata.TVDB.Name = "Example Series"
	}
	if meta.ExternalMetadata.TVmaze == nil {
		meta.ExternalMetadata.TVmaze = &api.TVmazeMetadata{}
	}
	meta.ExternalMetadata.TVmaze.TVmazeID = 1
	if strings.TrimSpace(meta.ExternalMetadata.TVmaze.Name) == "" {
		meta.ExternalMetadata.TVmaze.Name = "Example Series"
	}
	return EvaluateRules(ctx, tracker, meta, nil)
}

func TestEvaluateRulesRequiresUniqueID(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfo: false}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "AITHER", meta)
	if len(failures) == 0 {
		t.Fatalf("expected rule failure")
	}
	if failures[0].Rule != "require_unique_id" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestResolveCategoryIgnoresEmptyTVMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata api.ExternalMetadata
	}{
		{name: "TVDB", metadata: api.ExternalMetadata{TVDB: &api.TVDBMetadata{}}},
		{name: "TVmaze", metadata: api.ExternalMetadata{TVmaze: &api.TVmazeMetadata{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := api.PreparedMetadata{
				ExternalMetadata: tt.metadata,
				Release:          api.ReleaseInfo{Category: "movie"},
			}
			if got := resolveCategory(meta); got != "movie" {
				t.Fatalf("expected movie fallback, got %q", got)
			}
		})
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
	meta := api.PreparedMetadata{
		ExternalIDs:            api.ExternalIDs{Category: "movie"},
		ValidMediaInfoSettings: false,
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RF", meta)
	if !hasMISettingsFailure(failures) {
		t.Fatalf("expected require_valid_mi_setting failure for RF, got %#v", failures)
	}
}

func TestEvaluateRulesUnit3DWithoutRuleSetEnforcesMediaInfoSettings(t *testing.T) {
	// ACM is a known UNIT3D tracker with no tracker-specific RuleSet. The early
	// "not found" return must not skip the MediaInfo-settings enforcement.
	meta := api.PreparedMetadata{ValidMediaInfoSettings: false}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "ACM", meta)
	if !hasMISettingsFailure(failures) {
		t.Fatalf("expected require_valid_mi_setting failure for ACM, got %#v", failures)
	}

	meta.ValidMediaInfoSettings = true
	failures = evaluateNonMetadataRulesForTest(context.Background(), "ACM", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures for ACM with valid MI settings, got %#v", failures)
	}
}

func TestEvaluateRulesUnit3DAllowsValidMediaInfoSettings(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs:            api.ExternalIDs{Category: "movie"},
		ValidMediaInfoSettings: true,
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RF", meta)
	if hasMISettingsFailure(failures) {
		t.Fatalf("did not expect require_valid_mi_setting failure for RF, got %#v", failures)
	}
}

func TestEvaluateRulesNonUnit3DOptInMediaInfoSettings(t *testing.T) {
	// BHD is not a UNIT3D-upload tracker but opts in via its RuleSet, so the
	// per-tracker flag must still be honored.
	meta := api.PreparedMetadata{ValidMediaInfoSettings: false}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "BHD", meta)
	if !hasMISettingsFailure(failures) {
		t.Fatalf("expected require_valid_mi_setting failure for BHD, got %#v", failures)
	}
}

func TestEvaluateRulesLanguageRulePasses(t *testing.T) {
	meta := api.PreparedMetadata{
		AudioLanguages:         []string{"french"},
		SubtitleLanguages:      nil,
		ValidMediaInfoSettings: true,
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "TOS", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesLanguageRuleMissingData(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoSettings: true}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "TOS", meta)
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
	failures := evaluateNonMetadataRulesForTest(context.Background(), "LUME", meta)
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
	failures := evaluateNonMetadataRulesForTest(context.Background(), "LUME", meta)
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
	failures := evaluateNonMetadataRulesForTest(context.Background(), "LUME", meta)
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
	failures := evaluateNonMetadataRulesForTest(context.Background(), "LUME", meta)
	if len(failures) != 0 {
		t.Fatalf("expected disc upload to skip LUME container and resolution rules, got %#v", failures)
	}
}

func TestEvaluateRulesPTPRequiresMovieForNonPackTV(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "tv"}}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "PTP", meta)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_movie_only" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesPTPAllowsTVPacks(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "tv"}, TVPack: true}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "PTP", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesANTRequiresMovie(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "tv"}}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "ANT", meta)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_movie_only" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesANTAllowsMovie(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "movie"}}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "ANT", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesBHDRequiresValidMISettings(t *testing.T) {
	meta := api.PreparedMetadata{ValidMediaInfoSettings: false}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "BHD", meta)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_valid_mi_setting" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}

	meta.ValidMediaInfoSettings = true
	failures = evaluateNonMetadataRulesForTest(context.Background(), "BHD", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesBHDBlocksAdultContent(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ValidMediaInfoSettings: true,
		ExternalIDs:            api.ExternalIDs{Category: "movie", IMDBID: 1234567},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{Keywords: "adult"},
			IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"},
		},
	}

	failures := EvaluateRules(context.Background(), "BHD", meta, nil)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "block_adult" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
	if failures[0].Reason != "Porn/xxx is not allowed at BHD." {
		t.Fatalf("unexpected reason: %s", failures[0].Reason)
	}
}

func TestEvaluateRulesBHDIgnoresStaleAdultMetadata(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		SourcePath:             "current",
		ValidMediaInfoSettings: true,
		Release:                api.ReleaseInfo{Genre: "Drama"},
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: "other",
			TMDB:       &api.TMDBMetadata{Keywords: "adult", Genres: "pornography"},
			IMDB:       &api.IMDBMetadata{Genres: "xxx"},
		},
	}

	failures := EvaluateRules(context.Background(), "BHD", meta, nil)
	if hasRuleFailure(failures, "block_adult") {
		t.Fatalf("expected stale adult metadata to be ignored, got %#v", failures)
	}
}

func TestEvaluateRulesBHDBlocksAdultMetadataForExactSourcePath(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	meta := api.PreparedMetadata{
		SourcePath:             sourcePath,
		ValidMediaInfoSettings: true,
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: sourcePath,
			TMDB:       &api.TMDBMetadata{Keywords: "adult"},
		},
	}

	failures := EvaluateRules(context.Background(), "BHD", meta, nil)
	if !hasRuleFailure(failures, "block_adult") {
		t.Fatal("expected exact-source adult metadata to be applied")
	}
}

func TestEvaluateRulesBHDIgnoresCaseOnlyDistinctAdultMetadata(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	currentPath := filepath.Join(tmp, "Example.Release.2026.1080p-GRP.mkv")
	storedPath := filepath.Join(tmp, "example.release.2026.1080p-grp.mkv")
	if err := os.WriteFile(currentPath, []byte("current"), 0o600); err != nil {
		t.Fatalf("write current source fixture: %v", err)
	}
	if err := os.WriteFile(storedPath, []byte("stored"), 0o600); err != nil {
		t.Fatalf("write stored source fixture: %v", err)
	}
	currentInfo, err := os.Stat(currentPath)
	if err != nil {
		t.Fatalf("stat current source fixture: %v", err)
	}
	storedInfo, err := os.Stat(storedPath)
	if err != nil {
		t.Fatalf("stat stored source fixture: %v", err)
	}
	if os.SameFile(currentInfo, storedInfo) {
		t.Skip("filesystem does not distinguish case-only paths")
	}

	meta := api.PreparedMetadata{
		SourcePath:             currentPath,
		ValidMediaInfoSettings: true,
		Release:                api.ReleaseInfo{Genre: "Drama"},
		ExternalMetadata: api.ExternalMetadata{
			SourcePath: storedPath,
			TMDB:       &api.TMDBMetadata{Keywords: "adult", Genres: "pornography"},
			IMDB:       &api.IMDBMetadata{Genres: "xxx"},
		},
	}

	failures := EvaluateRules(context.Background(), "BHD", meta, nil)
	if hasRuleFailure(failures, "block_adult") {
		t.Fatal("expected case-only-distinct adult metadata to be ignored")
	}
}

func TestEvaluateRulesBHDRejectsInvalidContainerForUploadTypes(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ValidMediaInfoSettings: true,
		Type:                   "REMUX",
		Container:              "avi",
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "BHD", meta)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "extra_check" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}

	meta.Container = "mkv"
	failures = evaluateNonMetadataRulesForTest(context.Background(), "BHD", meta)
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
			meta:      api.PreparedMetadata{Type: "WEBDL", Container: "avi", ValidMediaInfoSettings: true},
			wantBlock: true,
		},
		{
			name:      "hdtv allows ts",
			meta:      api.PreparedMetadata{Type: "HDTV", Container: "ts", ValidMediaInfoSettings: true},
			wantBlock: false,
		},
		{
			name:      "dolby vision webdl allows mp4",
			meta:      api.PreparedMetadata{Type: "WEBDL", Container: "mp4", WebDV: true, ValidMediaInfoSettings: true},
			wantBlock: false,
		},
		{
			name:      "disc skips container rule",
			meta:      api.PreparedMetadata{DiscType: "BDMV", Type: "WEBDL", Container: "avi", ValidMediaInfoSettings: true},
			wantBlock: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			failures := evaluateNonMetadataRulesForTest(context.Background(), "BLU", tc.meta)
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
	failures := evaluateNonMetadataRulesForTest(context.Background(), "NBL", meta)
	if len(failures) != 2 {
		t.Fatalf("expected 2 failures, got %#v", failures)
	}
	if failures[0].Rule != "require_tv_only" {
		t.Fatalf("unexpected first rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesNBLAllowsTV(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "tv"}}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "NBL", meta)
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
	failures := evaluateNonMetadataRulesForTest(context.Background(), "NBL", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesNBLSkipsLanguageRuleForBDMVOnly(t *testing.T) {
	bdmv := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "tv"},
		DiscType:    "BDMV",
	}
	if failures := evaluateNonMetadataRulesForTest(context.Background(), "NBL", bdmv); len(failures) != 0 {
		t.Fatalf("expected BDMV to skip NBL language rule, got %#v", failures)
	}

	dvd := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "tv"},
		DiscType:    "DVD",
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "NBL", dvd)
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
	failures := evaluateNonMetadataRulesForTest(context.Background(), "DP", meta)
	for _, failure := range failures {
		if failure.Rule == "block_group" {
			t.Fatalf("expected FGT to be handled as a banned group, got rule failure %#v", failures)
		}
	}
}

func TestEvaluateRulesRHDRequiresGermanAudio(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		AudioLanguages:         []string{"English"},
		Release:                api.ReleaseInfo{Resolution: "1080p"},
		ValidMediaInfoSettings: true,
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RHD", meta)
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
		AudioLanguages:         []string{"German"},
		Release:                api.ReleaseInfo{Resolution: "1080p"},
		ValidMediaInfoSettings: true,
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RHD", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesRHDRequiresGermanAudioForDisc(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Type:                   "DISC",
		DiscType:               "BDMV",
		AudioLanguages:         []string{"English"},
		Release:                api.ReleaseInfo{Resolution: "1080p"},
		ValidMediaInfoSettings: true,
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RHD", meta)
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
		Type:                   "DISC",
		DiscType:               "BDMV",
		AudioLanguages:         []string{"German"},
		Release:                api.ReleaseInfo{Resolution: "1080p"},
		ValidMediaInfoSettings: true,
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RHD", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesRHDRequiresSceneNFO(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Scene:                  true,
		AudioLanguages:         []string{"German"},
		Release:                api.ReleaseInfo{Resolution: "1080p"},
		ValidMediaInfoSettings: true,
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RHD", meta)
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
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RHD", meta)
	for _, failure := range failures {
		if failure.Rule == "require_scene_nfo" {
			t.Fatalf("expected non-scene upload to avoid NFO blocker, got %#v", failures)
		}
	}
}

func TestEvaluateRulesTOSRequiresSceneNFO(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Scene:                  true,
		AudioLanguages:         []string{"French"},
		ValidMediaInfoSettings: true,
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "TOS", meta)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_scene_nfo" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesAitherRequiresLanguageForNonDisc(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:               "",
		ValidMediaInfo:         true,
		ValidMediaInfoSettings: true,
		AudioLanguages:         []string{"Japanese"},
		SubtitleLanguages:      []string{"German"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "AITHER", meta)
	if len(failures) == 0 {
		t.Fatalf("expected language failure")
	}
	if failures[0].Rule != "language_rule" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesA4KSkipsLanguageRuleForDisc(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:               "BDMV",
		ValidMediaInfoSettings: true,
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "A4K", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures for disc upload, got %#v", failures)
	}
}

func TestEvaluateRulesLSTRequiresValidMIAndLanguage(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:               "",
		ValidMediaInfoSettings: false,
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "LST", meta)
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

	renamed := api.PreparedMetadata{
		SourcePath: "/data/movies/Example Movie 2026 2160p MA WEB-DL DDP5 1 HDR H 265-GRP",
		Release:    api.ReleaseInfo{Group: "GRP"},
	}
	clean := api.PreparedMetadata{
		SourcePath: "/data/movies/Example.Movie.2026.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-GRP",
		Release:    api.ReleaseInfo{Group: "GRP"},
	}

	// Covers a UNIT3D tracker (LST), a non-UNIT3D tracker (PTP), an AZ-family
	// tracker (PHD), and a tracker with no rule set of its own (HDB) to prove the
	// rule fires across every family.
	for _, tracker := range []string{"LST", "PTP", "PHD", "HDB"} {
		t.Run(tracker, func(t *testing.T) {
			t.Parallel()
			got := evaluateNonMetadataRulesForTest(context.Background(), tracker, renamed)
			failure, ok := findRuleFailure(got, "modified_release")
			if !ok {
				t.Fatalf("expected modified_release failure for %s, got %#v", tracker, got)
			}
			if !strings.Contains(failure.Reason, "renamed") {
				t.Fatalf("expected a meaningful reason mentioning 'renamed' for %s, got %q", tracker, failure.Reason)
			}
			if clean := evaluateNonMetadataRulesForTest(context.Background(), tracker, clean); hasRuleFailure(clean, "modified_release") {
				t.Fatalf("did not expect modified_release failure for clean release on %s, got %#v", tracker, clean)
			}
		})
	}
}

// TestEvaluateRulesMetadataPolicyReturnsEvaluatedEmpty guards the contract that
// a configured metadata policy returns a non-nil empty slice after passing, so
// the consumer clears stale stored metadata failures.
func TestEvaluateRulesMetadataPolicyReturnsEvaluatedEmpty(t *testing.T) {
	t.Parallel()

	clean := api.PreparedMetadata{
		SourcePath: "/data/movies/Example.Movie.2026.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-GRP",
		Release:    api.ReleaseInfo{Group: "GRP"},
	}
	if got := evaluateNonMetadataRulesForTest(context.Background(), "MTV", clean); got == nil || len(got) != 0 {
		t.Fatalf("expected evaluated empty result, got %#v", got)
	}
}

func TestEvaluateRulesModifiedReleaseExemptsManual(t *testing.T) {
	t.Parallel()

	renamed := api.PreparedMetadata{
		SourcePath: "/data/movies/Example Movie 2026 2160p MA WEB-DL DDP5 1 HDR H 265-GRP",
		Release:    api.ReleaseInfo{Group: "GRP"},
	}
	if got := evaluateNonMetadataRulesForTest(context.Background(), "MANUAL", renamed); hasRuleFailure(got, "modified_release") {
		t.Fatalf("expected MANUAL to be exempt from modified_release, got %#v", got)
	}
}
