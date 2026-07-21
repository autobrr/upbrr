// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/releasepolicy"
	"github.com/autobrr/upbrr/pkg/api"
)

var ruleResolutionOrder = map[string]int{
	"480i":  1,
	"480p":  2,
	"576i":  3,
	"576p":  4,
	"720p":  5,
	"1080i": 6,
	"1080p": 7,
	"1440p": 8,
	"2160p": 9,
	"4320p": 10,
	"8640p": 11,
}

// EvaluateRules evaluates repository-wide release-modification policy without a
// tracker registry. Use [EvaluateRulesWithRegistry] for tracker and metadata
// policy.
func EvaluateRules(ctx context.Context, tracker string, meta api.RuleSubject, logger api.Logger) ([]api.RuleFailure, error) {
	return evaluateRules(ctx, nil, tracker, meta, logger)
}

// EvaluateRulesWithRegistry returns all applicable failures with their
// tracker-owned dispositions. Scene renames and structural requirements are
// strict; non-scene rename heuristics and explicitly waivable rules require
// authorization, while metadata requirements retain their site-specific
// disposition.
func EvaluateRulesWithRegistry(ctx context.Context, registry *Registry, tracker string, meta api.RuleSubject, logger api.Logger) ([]api.RuleFailure, error) {
	return evaluateRules(ctx, registry, tracker, meta, logger)
}

func evaluateRules(ctx context.Context, registry *Registry, tracker string, meta api.RuleSubject, logger api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: evaluate rules: %w", err)
	}
	name := strings.ToUpper(strings.TrimSpace(tracker))

	failures := make([]api.RuleFailure, 0)
	addFailure := func(rule, reason string, disposition api.RuleDisposition) {
		trimmed := strings.TrimSpace(reason)
		if trimmed == "" {
			trimmed = "rule requirement not met"
		}
		failures = append(failures, api.RuleFailure{
			Rule:        strings.TrimSpace(rule),
			Reason:      trimmed,
			Disposition: api.NormalizeRuleDisposition(disposition),
		})
	}
	addStrict := func(rule, reason string) { addFailure(rule, reason, api.RuleDispositionStrict) }
	addWaivable := func(rule, reason string) { addFailure(rule, reason, api.RuleDispositionWaivable) }

	// Renamed/modified releases are rejected by every supported tracker. Scene
	// renames are authoritative and remain strict; heuristic rename detections
	// may be explicitly authorized as a non-resolution policy exception.
	if renamed, reason := releasepolicy.DetectModifiedRelease(releasepolicy.ModifiedReleaseSubject{
		SourcePath:         meta.SourcePath,
		VideoPath:          meta.VideoPath,
		DiscType:           meta.DiscType,
		PersonalRelease:    meta.PersonalRelease,
		SceneRenamed:       meta.SceneRenamed,
		SceneRenamedReason: meta.SceneRenamedReason,
		Release:            meta.Release,
	}); renamed {
		disposition := api.RuleDispositionWaivable
		if meta.SceneRenamed {
			disposition = api.RuleDispositionStrict
		}
		addFailure("modified_release", reason, disposition)
	}
	metadataFailures, metadataEvaluated := evaluateMetadataRequirementsWithRegistry(registry, name, meta)
	failures = append(failures, metadataFailures...)

	rules, ok := registry.LookupRules(name)
	if !ok && !metadataEvaluated {
		// Preserve the nil contract for trackers without their own rule set: the
		// consumer (applyTrackerRules) treats a nil result as "not evaluated, keep
		// pre-existing failures" but an empty slice as "evaluated, clear failures".
		// Only return a slice when this rule actually produced a failure.
		if len(failures) > 0 {
			return failures, nil
		}
		return nil, nil
	}

	if rules.RequireUniqueID && !meta.Assessments.UniqueIDRequirementSatisfied() {
		addStrict("require_unique_id", "missing MediaInfo Unique ID")
	}
	if rules.RequireValidMISetting && !meta.Assessments.EncodeSettingsRequirementSatisfied() {
		addStrict("require_valid_mi_setting", "missing MediaInfo encode settings")
	}

	if rules.RequireDiscOnly && !isDiscType(meta.DiscType) {
		addStrict("require_disc_only", "requires disc upload")
	}
	if rules.RequireMovieUnlessTVPack && !meta.TVPack {
		category := resolveCategory(meta)
		if category != "" && category != "movie" {
			addStrict("require_movie_only", fmt.Sprintf("category %s is not movie", category))
		}
	}
	if rules.RequireMovieOnly || rules.RequireTVOnly {
		category := resolveCategory(meta)
		if category != "" {
			if rules.RequireMovieOnly && category != "movie" {
				addStrict("require_movie_only", fmt.Sprintf("category %s is not movie", category))
			}
			if rules.RequireTVOnly && category != "tv" {
				addStrict("require_tv_only", fmt.Sprintf("category %s is not tv", category))
			}
		} else if logger != nil {
			logger.Debugf("trackers: %s rule category check skipped (missing category)", name)
		}
	}

	typeValue := resolveType(meta)
	if len(rules.RequireHEVCForTypes) > 0 {
		if hasTypeRequirement(typeValue, rules.RequireHEVCForTypes) && !isHEVC(meta) {
			addStrict("require_hevc", fmt.Sprintf("%s requires HEVC for %s", name, typeValue))
		}
	}

	if rules.MinResolution != "" {
		minResolution := strings.ToLower(strings.TrimSpace(rules.MinResolution))
		value := resolveResolution(meta)
		if value == "" {
			addFailure("min_resolution", "resolution required for "+name, api.RuleDispositionStrict)
		} else if ruleResolutionOrder[value] < ruleResolutionOrder[minResolution] {
			addFailure("min_resolution", fmt.Sprintf("resolution %s below %s", value, minResolution), api.RuleDispositionStrict)
		}
	}

	if rules.BlockAdult && isAdultContent(meta) {
		message := strings.TrimSpace(rules.AdultMessage)
		if message == "" {
			message = "adult content not allowed at " + name
		}
		addWaivable("block_adult", message)
	}

	if rules.BlockDVDRip && strings.EqualFold(typeValue, "DVDRIP") {
		addStrict("block_dvdrip", "DVDRip not allowed")
	}
	if rules.BlockExternalSubs && hasReleaseToken(meta, []string{"extsub", "ext-sub", "external subs", "external subtitles"}) {
		addWaivable("block_external_subs", "external subtitles not allowed")
	}
	if rules.BlockHardcodedSubs && hasReleaseToken(meta, []string{"hardsub", "hard-sub", "hardcoded"}) {
		addStrict("block_hardcoded_subs", "hardcoded subtitles not allowed")
	}

	if rules.BlockSingleFileFolder && hasSingleFileFolder(meta) {
		addStrict("block_single_file_folder", "single-file folders are not allowed")
	}

	if len(rules.BlockGroups) > 0 {
		group := strings.ToUpper(strings.TrimSpace(resolveGroup(meta)))
		if group != "" && containsAny([]string{group}, rules.BlockGroups) {
			addWaivable("block_group", fmt.Sprintf("group %s not allowed", group))
		}
	}

	if len(rules.BlockGroupUnlessType) > 0 {
		group := strings.ToUpper(strings.TrimSpace(resolveGroup(meta)))
		if group != "" {
			if allowedTypes, ok := rules.BlockGroupUnlessType[group]; ok {
				if !hasTypeRequirement(typeValue, allowedTypes) {
					addWaivable("block_group_unless_type", fmt.Sprintf("group %s only allowed for %s", group, strings.Join(allowedTypes, ", ")))
				}
			}
		}
	}

	if rules.RequireSceneNFO && meta.Scene && strings.TrimSpace(meta.SceneNFOPath) == "" {
		addStrict("require_scene_nfo", "scene release missing NFO")
	}

	if rules.RequireAudioLanguages && len(meta.AudioLanguages) == 0 {
		addStrict("require_audio_languages", "missing audio language data")
	}

	if rules.Language != nil {
		if ok, reason := evaluateLanguageRule(meta, rules.Language); !ok {
			addStrict("language_rule", reason)
		}
	}

	if rules.Check != nil {
		customFailures, err := rules.Check(ctx, meta, logger)
		if err != nil {
			return nil, fmt.Errorf("trackers: %s rule check: %w", name, err)
		}
		for _, failure := range customFailures {
			addFailure(failure.Rule, failure.Reason, failure.Disposition)
		}
	}

	return failures, nil
}

// ResolveRuleCategory returns the common category used by tracker rules.
func ResolveRuleCategory(meta api.RuleSubject) string { return resolveCategory(meta) }

// ResolveRuleType returns the common release type used by tracker rules.
func ResolveRuleType(meta api.RuleSubject) string { return resolveType(meta) }

// ResolveRuleResolution returns the common resolution used by tracker rules.
func ResolveRuleResolution(meta api.RuleSubject) string { return resolveResolution(meta) }

// IsDiscType reports whether value identifies a supported disc source.
func IsDiscType(value string) bool { return isDiscType(value) }

func resolveCategory(meta api.RuleSubject) string {
	if sourceMatches(meta.Identity.SourcePath, meta.SourcePath) {
		if value := strings.ToLower(strings.TrimSpace(string(meta.Identity.Category))); value != "" && value != string(api.CanonicalCategoryUnknown) {
			return value
		}
	}
	return ""
}

func resolveType(meta api.RuleSubject) string {
	value := strings.ToUpper(strings.TrimSpace(meta.Type))
	if value == "" {
		value = strings.ToUpper(strings.TrimSpace(meta.Release.Type))
	}
	return value
}

func resolveGroup(meta api.RuleSubject) string {
	if group := strings.TrimSpace(meta.Release.Group); group != "" {
		return group
	}
	return strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-")
}

func resolveResolution(meta api.RuleSubject) string {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if resolution == "" {
		resolution = detectResolution(meta.ReleaseName)
	}
	return strings.ToLower(strings.TrimSpace(resolution))
}

func detectResolution(value string) string {
	clean := strings.ToLower(value)
	for _, candidate := range []string{"8640p", "4320p", "2160p", "1440p", "1080p", "1080i", "720p", "576p", "576i", "480p", "480i"} {
		if strings.Contains(clean, candidate) {
			return candidate
		}
	}
	return ""
}

func isDiscType(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "BDMV", "DVD", "HDDVD":
		return true
	default:
		return false
	}
}

func isHEVC(meta api.RuleSubject) bool {
	codec := strings.ToUpper(strings.TrimSpace(meta.VideoCodec))
	if codec == "" {
		for _, value := range meta.Release.Codec {
			if strings.EqualFold(strings.TrimSpace(value), "HEVC") || strings.EqualFold(strings.TrimSpace(value), "H.265") {
				return true
			}
		}
		return false
	}
	return codec == "HEVC" || codec == "H.265"
}

func hasTypeRequirement(value string, allowed []string) bool {
	if value == "" || len(allowed) == 0 {
		return false
	}
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), value) {
			return true
		}
	}
	return false
}

func hasSingleFileFolder(meta api.RuleSubject) bool {
	if isDiscType(meta.DiscType) {
		return false
	}
	if len(meta.FileList) != 1 {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(meta.FileList[0]), strings.TrimSpace(meta.SourcePath))
}

func hasReleaseToken(meta api.RuleSubject, tokens []string) bool {
	values := make([]string, 0, len(meta.Release.Other)+len(meta.Release.Edition)+2)
	values = append(values, meta.Release.Other...)
	values = append(values, meta.Release.Edition...)
	if meta.ReleaseName != "" {
		values = append(values, meta.ReleaseName)
	}
	if meta.ReleaseNameNoTag != "" {
		values = append(values, meta.ReleaseNameNoTag)
	}
	value := strings.ToLower(strings.Join(values, " "))
	for _, token := range tokens {
		if token == "" {
			continue
		}
		if strings.Contains(value, strings.ToLower(token)) {
			return true
		}
	}
	return false
}

func isAdultContent(meta api.RuleSubject) bool {
	candidates := append([]string{}, splitCSV(meta.Release.Genre)...)
	if meta.ProviderMetadata.TMDB != nil && externalMetadataMatchesCurrentSource(meta) {
		candidates = append(candidates, splitCSV(meta.ProviderMetadata.TMDB.Genres)...)
		candidates = append(candidates, splitCSV(meta.ProviderMetadata.TMDB.Keywords)...)
	}
	if meta.ProviderMetadata.IMDB != nil && externalMetadataMatchesCurrentSource(meta) {
		candidates = append(candidates, splitCSV(meta.ProviderMetadata.IMDB.Genres)...)
	}
	normalized := normalizeStrings(candidates)
	for _, token := range normalized {
		switch token {
		case "adult", "porn", "pornography", "xxx", "erotic", "hentai", "adult animation", "softcore":
			return true
		}
	}
	return false
}

func externalMetadataMatchesCurrentSource(meta api.RuleSubject) bool {
	storedSource := strings.TrimSpace(meta.ProviderMetadata.SourcePath)
	return storedSource == "" || pathutil.SamePath(storedSource, strings.TrimSpace(meta.SourcePath))
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func normalizeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func containsAny(values []string, targets []string) bool {
	if len(values) == 0 || len(targets) == 0 {
		return false
	}
	set := make(map[string]bool, len(targets))
	for _, target := range targets {
		trimmed := strings.ToLower(strings.TrimSpace(target))
		if trimmed != "" {
			set[trimmed] = true
		}
	}
	for _, value := range values {
		if set[strings.ToLower(strings.TrimSpace(value))] {
			return true
		}
	}
	return false
}

func evaluateLanguageRule(meta api.RuleSubject, rule *LanguageRule) (bool, string) {
	if rule == nil {
		return true, ""
	}
	if rule.ApplyIfNonBDMV && strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return true, ""
	}
	if rule.ApplyIfNonDisc && isDiscType(meta.DiscType) {
		return true, ""
	}

	audioLanguages := normalizeStrings(meta.AudioLanguages)
	subLanguages := normalizeStrings(meta.SubtitleLanguages)
	required := normalizeStrings(rule.Languages)
	if len(required) == 0 {
		return true, ""
	}

	checkAudio := rule.RequireAudio || rule.RequireBoth
	checkSubs := rule.RequireSubs || rule.RequireBoth
	if (checkAudio || checkSubs) && len(audioLanguages) == 0 && len(subLanguages) == 0 {
		return false, "missing language data"
	}

	audioOK := !checkAudio || containsAny(audioLanguages, required)
	subOK := !checkSubs || containsAny(subLanguages, required)

	originalOK := false
	if rule.AllowOriginal {
		original := resolveOriginalLanguage(meta)
		if original != "" {
			originalOK = containsAny(audioLanguages, []string{original})
		}
	}

	if !audioOK && originalOK {
		if subOK {
			return true, ""
		}
		return false, fmt.Sprintf("requires subtitles in %s with original audio", strings.Join(required, ", "))
	}

	if rule.RequireBoth {
		if audioOK && subOK {
			return true, ""
		}
		return false, "requires audio and subtitles in " + strings.Join(required, ", ")
	}
	if checkAudio && !checkSubs {
		if audioOK {
			return true, ""
		}
		return false, "requires audio in " + strings.Join(required, ", ")
	}
	if checkSubs && !checkAudio {
		if subOK {
			return true, ""
		}
		return false, "requires subtitles in " + strings.Join(required, ", ")
	}

	if audioOK || subOK {
		return true, ""
	}
	return false, "requires audio or subtitles in " + strings.Join(required, ", ")
}

func resolveOriginalLanguage(meta api.RuleSubject) string {
	var raw string
	if meta.ProviderMetadata.TMDB != nil {
		raw = strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage)
	}
	if raw == "" && meta.ProviderMetadata.IMDB != nil {
		raw = strings.TrimSpace(meta.ProviderMetadata.IMDB.OriginalLanguage)
	}
	normalized := languageutil.NormalizeLanguageDisplay(raw)
	if normalized == "" {
		return ""
	}
	return strings.ToLower(normalized)
}
