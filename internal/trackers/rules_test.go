// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func evaluateNonMetadataRulesForTest(ctx context.Context, tracker string, meta api.RuleSubject) []api.RuleFailure {
	if strings.TrimSpace(string(meta.Identity.Category)) == "" {
		switch strings.ToUpper(strings.TrimSpace(tracker)) {
		case "NBL", "BTN":
			meta.Identity.Category = api.CanonicalCategoryTV
		default:
			meta.Identity.Category = api.CanonicalCategoryMovie
		}
	}
	meta.Identity.TMDBID = 1
	meta.Identity.IMDBID = 1234567
	meta.Identity.TVDBID = 1
	meta.Identity.TVmazeID = 1
	if meta.ProviderMetadata.TMDB == nil {
		meta.ProviderMetadata.TMDB = &api.TMDBMetadata{}
	}
	meta.ProviderMetadata.TMDB.TMDBID = 1
	if strings.TrimSpace(meta.ProviderMetadata.TMDB.Title) == "" {
		meta.ProviderMetadata.TMDB.Title = "Example Release"
	}
	if meta.ProviderMetadata.IMDB == nil {
		meta.ProviderMetadata.IMDB = &api.IMDBMetadata{}
	}
	meta.ProviderMetadata.IMDB.IMDBID = 1234567
	if strings.TrimSpace(meta.ProviderMetadata.IMDB.Title) == "" {
		meta.ProviderMetadata.IMDB.Title = "Example Release"
	}
	if meta.ProviderMetadata.TVDB == nil {
		meta.ProviderMetadata.TVDB = &api.TVDBMetadata{}
	}
	meta.ProviderMetadata.TVDB.TVDBID = 1
	if strings.TrimSpace(meta.ProviderMetadata.TVDB.Name) == "" {
		meta.ProviderMetadata.TVDB.Name = "Example Series"
	}
	if meta.ProviderMetadata.TVmaze == nil {
		meta.ProviderMetadata.TVmaze = &api.TVmazeMetadata{}
	}
	meta.ProviderMetadata.TVmaze.TVmazeID = 1
	if strings.TrimSpace(meta.ProviderMetadata.TVmaze.Name) == "" {
		meta.ProviderMetadata.TVmaze.Name = "Example Series"
	}
	registry, err := impl.NewRegistry()
	if err != nil {
		panic(err)
	}
	return trackers.EvaluateRulesWithRegistry(ctx, registry, tracker, meta, nil)
}

func evaluateBHDRulesWithRegistryForTest(ctx context.Context, meta api.RuleSubject) []api.RuleFailure {
	registry, err := impl.NewRegistry()
	if err != nil {
		panic(err)
	}
	return trackers.EvaluateRulesWithRegistry(ctx, registry, "BHD", meta, nil)
}

func TestEvaluateRulesRequiresUniqueID(t *testing.T) {
	meta := api.RuleSubject{Assessments: api.ReleaseAssessments{MediaInfoUniqueID: api.UniqueIDStatusMissing}}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "AITHER", meta)
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

func encodeAssessments(status api.EncodeSettingsStatus) api.ReleaseAssessments {
	return api.ReleaseAssessments{MediaInfoEncodeSettings: status}
}

func TestEvaluateRulesUnit3DEnforcesMediaInfoSettings(t *testing.T) {
	// RF is a known UNIT3D tracker but its RuleSet does not opt into
	// RequireValidMISetting; the UNIT3D upload rejects encodes without encoding
	// settings, so the rule must fire at prep time regardless.
	meta := api.RuleSubject{
		Identity:    api.ExternalIdentity{Category: "movie"},
		Assessments: encodeAssessments(api.EncodeSettingsStatusMissing),
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RF", meta)
	if !hasMISettingsFailure(failures) {
		t.Fatalf("expected require_valid_mi_setting failure for RF, got %#v", failures)
	}
}

func TestEvaluateRulesUnit3DWithoutRuleSetEnforcesMediaInfoSettings(t *testing.T) {
	// ACM is a known UNIT3D tracker with no tracker-specific RuleSet. The early
	// "not found" return must not skip the MediaInfo-settings enforcement.
	meta := api.RuleSubject{Assessments: encodeAssessments(api.EncodeSettingsStatusMissing)}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "ACM", meta)
	if !hasMISettingsFailure(failures) {
		t.Fatalf("expected require_valid_mi_setting failure for ACM, got %#v", failures)
	}

	meta.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusPresent
	failures = evaluateNonMetadataRulesForTest(context.Background(), "ACM", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures for ACM with valid MI settings, got %#v", failures)
	}
}

func TestEvaluateRulesUnit3DAllowsPresentMediaInfoEncodeSettings(t *testing.T) {
	meta := api.RuleSubject{
		Identity:    api.ExternalIdentity{Category: "movie"},
		Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RF", meta)
	if hasMISettingsFailure(failures) {
		t.Fatalf("did not expect require_valid_mi_setting failure for RF, got %#v", failures)
	}
}

func TestEvaluateRulesNonUnit3DOptInMediaInfoSettings(t *testing.T) {
	// BHD is not a UNIT3D-upload tracker but opts in via its RuleSet, so the
	// per-tracker flag must still be honored.
	meta := api.RuleSubject{Assessments: encodeAssessments(api.EncodeSettingsStatusMissing)}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "BHD", meta)
	if !hasMISettingsFailure(failures) {
		t.Fatalf("expected require_valid_mi_setting failure for BHD, got %#v", failures)
	}
}

func TestEvaluateRulesLanguageRulePasses(t *testing.T) {
	meta := api.RuleSubject{
		AudioLanguages:    []string{"french"},
		SubtitleLanguages: nil,
		Assessments:       encodeAssessments(api.EncodeSettingsStatusPresent),
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "TOS", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesLanguageRuleMissingData(t *testing.T) {
	meta := api.RuleSubject{Assessments: encodeAssessments(api.EncodeSettingsStatusPresent)}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "TOS", meta)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "language_rule" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesLanguageRuleOriginalFallback(t *testing.T) {
	meta := api.RuleSubject{
		AudioLanguages:    []string{"Japanese"},
		SubtitleLanguages: []string{"English"},
		Assessments:       encodeAssessments(api.EncodeSettingsStatusPresent),
		Container:         "mkv",
		Release:           api.ReleaseInfo{Resolution: "720p"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "LUME", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesLUMERequiresMKVForNonDisc(t *testing.T) {
	meta := api.RuleSubject{
		Container:         "mp4",
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		Assessments:       encodeAssessments(api.EncodeSettingsStatusPresent),
		Release:           api.ReleaseInfo{Resolution: "720p"},
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
	meta := api.RuleSubject{
		Container:         "mkv",
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		Assessments:       encodeAssessments(api.EncodeSettingsStatusPresent),
		Release:           api.ReleaseInfo{Resolution: "720p"},
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "LUME", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesLUMESkipsContainerRuleForDisc(t *testing.T) {
	meta := api.RuleSubject{
		DiscType:    "BDMV",
		Container:   "mp4",
		Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
		Release:     api.ReleaseInfo{Resolution: "480p"},
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "LUME", meta)
	if len(failures) != 0 {
		t.Fatalf("expected disc upload to skip LUME container and resolution rules, got %#v", failures)
	}
}

func TestEvaluateRulesPTPRequiresMovieForNonPackTV(t *testing.T) {
	meta := api.RuleSubject{Identity: api.ExternalIdentity{Category: "tv"}}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "PTP", meta)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_movie_only" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesPTPAllowsTVPacks(t *testing.T) {
	meta := api.RuleSubject{Identity: api.ExternalIdentity{Category: "tv"}, TVPack: true}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "PTP", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesANTRequiresMovie(t *testing.T) {
	meta := api.RuleSubject{Identity: api.ExternalIdentity{Category: "tv"}}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "ANT", meta)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_movie_only" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesANTAllowsMovie(t *testing.T) {
	meta := api.RuleSubject{Identity: api.ExternalIdentity{Category: "movie"}}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "ANT", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesBHDRequiresValidMISettings(t *testing.T) {
	meta := api.RuleSubject{Assessments: encodeAssessments(api.EncodeSettingsStatusMissing)}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "BHD", meta)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %#v", failures)
	}
	if failures[0].Rule != "require_valid_mi_setting" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}

	meta.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusPresent
	failures = evaluateNonMetadataRulesForTest(context.Background(), "BHD", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesBHDBlocksAdultContent(t *testing.T) {
	t.Parallel()

	meta := api.RuleSubject{
		Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
		Identity:    api.ExternalIdentity{Category: "movie", IMDBID: 1234567},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{Keywords: "adult"},
			IMDB: &api.IMDBMetadata{IMDBID: 1234567, Title: "Example Release"},
		},
	}

	failures := evaluateBHDRulesWithRegistryForTest(context.Background(), meta)
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

	meta := api.RuleSubject{
		SourcePath:  "current",
		Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
		Release:     api.ReleaseInfo{Genre: "Drama"},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "other",
			TMDB:       &api.TMDBMetadata{Keywords: "adult", Genres: "pornography"},
			IMDB:       &api.IMDBMetadata{Genres: "xxx"},
		},
	}

	failures := evaluateBHDRulesWithRegistryForTest(context.Background(), meta)
	if hasRuleFailure(failures, "block_adult") {
		t.Fatalf("expected stale adult metadata to be ignored, got %#v", failures)
	}
}

func TestEvaluateRulesBHDBlocksAdultMetadataForExactSourcePath(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	meta := api.RuleSubject{
		SourcePath:  sourcePath,
		Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			TMDB:       &api.TMDBMetadata{Keywords: "adult"},
		},
	}

	failures := evaluateBHDRulesWithRegistryForTest(context.Background(), meta)
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

	meta := api.RuleSubject{
		SourcePath:  currentPath,
		Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
		Release:     api.ReleaseInfo{Genre: "Drama"},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: storedPath,
			TMDB:       &api.TMDBMetadata{Keywords: "adult", Genres: "pornography"},
			IMDB:       &api.IMDBMetadata{Genres: "xxx"},
		},
	}

	failures := evaluateBHDRulesWithRegistryForTest(context.Background(), meta)
	if hasRuleFailure(failures, "block_adult") {
		t.Fatal("expected case-only-distinct adult metadata to be ignored")
	}
}

func TestEvaluateRulesBHDRejectsInvalidContainerForUploadTypes(t *testing.T) {
	t.Parallel()

	meta := api.RuleSubject{
		Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
		Type:        "REMUX",
		Container:   "avi",
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
		meta      api.RuleSubject
		wantBlock bool
	}{
		{
			name: "non disc defaults to mkv",
			meta: api.RuleSubject{
				Type:        "WEBDL",
				Container:   "avi",
				Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
			},
			wantBlock: true,
		},
		{
			name: "hdtv allows ts",
			meta: api.RuleSubject{
				Type:        "HDTV",
				Container:   "ts",
				Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
			},
			wantBlock: false,
		},
		{
			name: "dolby vision webdl allows mp4",
			meta: api.RuleSubject{
				Type:        "WEBDL",
				Container:   "mp4",
				WebDV:       true,
				Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
			},
			wantBlock: false,
		},
		{
			name: "disc skips container rule",
			meta: api.RuleSubject{
				DiscType:    "BDMV",
				Type:        "WEBDL",
				Container:   "avi",
				Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
			},
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
	meta := api.RuleSubject{Identity: api.ExternalIdentity{Category: "movie"}}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "NBL", meta)
	if len(failures) != 2 {
		t.Fatalf("expected 2 failures, got %#v", failures)
	}
	if failures[0].Rule != "require_tv_only" {
		t.Fatalf("unexpected first rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesNBLAllowsTV(t *testing.T) {
	meta := api.RuleSubject{Identity: api.ExternalIdentity{Category: "tv"}}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "NBL", meta)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure because language data is missing, got %#v", failures)
	}
	if failures[0].Rule != "language_rule" {
		t.Fatalf("unexpected rule key: %s", failures[0].Rule)
	}
}

func TestEvaluateRulesNBLAllowsTVWithOriginalAudioAndEnglishSubs(t *testing.T) {
	meta := api.RuleSubject{
		Identity:          api.ExternalIdentity{Category: "tv"},
		DiscType:          "",
		AudioLanguages:    []string{"Japanese"},
		SubtitleLanguages: []string{"English"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "NBL", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesNBLSkipsLanguageRuleForBDMVOnly(t *testing.T) {
	bdmv := api.RuleSubject{
		Identity: api.ExternalIdentity{Category: "tv"},
		DiscType: "BDMV",
	}
	if failures := evaluateNonMetadataRulesForTest(context.Background(), "NBL", bdmv); len(failures) != 0 {
		t.Fatalf("expected BDMV to skip NBL language rule, got %#v", failures)
	}

	dvd := api.RuleSubject{
		Identity: api.ExternalIdentity{Category: "tv"},
		DiscType: "DVD",
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

	meta := api.RuleSubject{
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

	meta := api.RuleSubject{
		AudioLanguages: []string{"English"},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
		Assessments:    encodeAssessments(api.EncodeSettingsStatusPresent),
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

	meta := api.RuleSubject{
		AudioLanguages: []string{"German"},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
		Assessments:    encodeAssessments(api.EncodeSettingsStatusPresent),
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RHD", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesRHDRequiresGermanAudioForDisc(t *testing.T) {
	t.Parallel()

	meta := api.RuleSubject{
		Type:           "DISC",
		DiscType:       "BDMV",
		AudioLanguages: []string{"English"},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
		Assessments:    encodeAssessments(api.EncodeSettingsStatusPresent),
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

	meta := api.RuleSubject{
		Type:           "DISC",
		DiscType:       "BDMV",
		AudioLanguages: []string{"German"},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
		Assessments:    encodeAssessments(api.EncodeSettingsStatusPresent),
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "RHD", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %#v", failures)
	}
}

func TestEvaluateRulesRHDRequiresSceneNFO(t *testing.T) {
	t.Parallel()

	meta := api.RuleSubject{
		Scene:          true,
		AudioLanguages: []string{"German"},
		Release:        api.ReleaseInfo{Resolution: "1080p"},
		Assessments:    encodeAssessments(api.EncodeSettingsStatusPresent),
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

	meta := api.RuleSubject{
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

	meta := api.RuleSubject{
		Scene:          true,
		AudioLanguages: []string{"French"},
		Assessments:    encodeAssessments(api.EncodeSettingsStatusPresent),
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
	meta := api.RuleSubject{
		DiscType: "",
		Assessments: api.ReleaseAssessments{
			MediaInfoUniqueID:       api.UniqueIDStatusPresent,
			MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent,
		},
		AudioLanguages:    []string{"Japanese"},
		SubtitleLanguages: []string{"German"},
		ProviderMetadata: api.SourceScopedMetadata{
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
	meta := api.RuleSubject{
		DiscType:    "BDMV",
		Assessments: encodeAssessments(api.EncodeSettingsStatusPresent),
	}
	failures := evaluateNonMetadataRulesForTest(context.Background(), "A4K", meta)
	if len(failures) != 0 {
		t.Fatalf("expected no failures for disc upload, got %#v", failures)
	}
}

func TestEvaluateRulesLSTRequiresValidMIAndLanguage(t *testing.T) {
	meta := api.RuleSubject{
		DiscType:    "",
		Assessments: encodeAssessments(api.EncodeSettingsStatusMissing),
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

	renamed := api.RuleSubject{
		SourcePath: "/data/movies/Example Movie 2026 2160p MA WEB-DL DDP5 1 HDR H 265-GRP",
		Release:    api.ReleaseInfo{Group: "GRP"},
	}
	clean := api.RuleSubject{
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

	clean := api.RuleSubject{
		SourcePath: "/data/movies/Example.Movie.2026.2160p.MA.WEB-DL.DDP5.1.HDR.H.265-GRP",
		Release:    api.ReleaseInfo{Group: "GRP"},
	}
	if got := evaluateNonMetadataRulesForTest(context.Background(), "MTV", clean); got == nil || len(got) != 0 {
		t.Fatalf("expected evaluated empty result, got %#v", got)
	}
}
