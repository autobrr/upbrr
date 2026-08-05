// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/pkg/api"
)

// EvidencePredicatePolicy supplies the stable rule key and caller-selected
// dispositions for a proven violation and incomplete evidence.
type EvidencePredicatePolicy struct {
	// Rule is the stable result key; an empty value uses the predicate's
	// documented fallback key.
	Rule string
	// ViolationDisposition applies when available evidence proves a violation.
	ViolationDisposition api.RuleDisposition
	// MissingEvidenceDisposition applies when the predicate cannot safely
	// decide from the supplied facts.
	MissingEvidenceDisposition api.RuleDisposition
}

// PackageExtensionPolicy configures archive and extraneous-file validation.
type PackageExtensionPolicy struct {
	Evidence          EvidencePredicatePolicy
	BlockArchives     bool
	BlockedExtensions []string
	BlockedExtraKinds []api.PackageFileKind
}

// ValidatePackageExtensions rejects proven archive or extraneous entries and
// fails safely when the known file list is incomplete.
func ValidatePackageExtensions(facts api.PackageFacts, policy PackageExtensionPolicy) []api.RuleFailure {
	rule := predicateRule(policy.Evidence, "package_extensions")
	switch {
	case policy.BlockArchives && facts.ArchiveFileCount > 0:
		return predicateViolation(rule, "archive files are not allowed", facts.Status, policy.Evidence)
	case containsNormalizedExtension(facts.Extensions, policy.BlockedExtensions):
		return predicateViolation(rule, "package contains a blocked file extension", facts.Status, policy.Evidence)
	case containsPackageKind(facts.ExtraKinds, policy.BlockedExtraKinds):
		return predicateViolation(rule, "package contains a blocked extra-file kind", facts.Status, policy.Evidence)
	case !completeEvidence(facts.Status):
		return predicateMissing(rule, "complete package file evidence is required", facts.Status, policy.Evidence)
	default:
		return nil
	}
}

// ValidateMediaOnlyPackage requires every package entry to be a media file.
func ValidateMediaOnlyPackage(facts api.PackageFacts, policy EvidencePredicatePolicy) []api.RuleFailure {
	rule := predicateRule(policy, "media_only_package")
	switch {
	case facts.KnownFileCount > facts.MediaFileCount:
		return predicateViolation(rule, "package contains non-media files", facts.Status, policy)
	case !completeEvidence(facts.Status):
		return predicateMissing(rule, "complete package file evidence is required", facts.Status, policy)
	default:
		return nil
	}
}

// ValidateSingleFileFolder rejects a proven folder containing one file.
func ValidateSingleFileFolder(facts api.PackageFacts, policy EvidencePredicatePolicy) []api.RuleFailure {
	rule := predicateRule(policy, "single_file_folder")
	if !completeEvidence(facts.Status) {
		return predicateMissing(rule, "complete source-layout evidence is required", facts.Status, policy)
	}
	if facts.SingleFileFolder {
		return predicateViolation(rule, "single-file folders are not allowed", facts.Status, policy)
	}
	return nil
}

// ValidateMultiSeasonPackage rejects a package containing multiple detected
// seasons. A positive multi-season detection remains usable with partial
// evidence; a negative result requires complete package evidence.
func ValidateMultiSeasonPackage(facts api.PackageFacts, policy EvidencePredicatePolicy) []api.RuleFailure {
	rule := predicateRule(policy, "multi_season_package")
	switch {
	case len(facts.DetectedSeasons) > 1:
		return predicateViolation(rule, "multiple seasons are not allowed", facts.Status, policy)
	case !completeEvidence(facts.Status):
		return predicateMissing(rule, "complete season evidence is required", facts.Status, policy)
	default:
		return nil
	}
}

// EpisodeRangePolicy configures one required contiguous local episode range.
// Zero FirstEpisode or LastEpisode derives that boundary from local facts.
type EpisodeRangePolicy struct {
	Evidence     EvidencePredicatePolicy
	Season       int
	FirstEpisode int
	LastEpisode  int
}

// ValidateCompleteEpisodeRange requires a contiguous local episode sequence.
func ValidateCompleteEpisodeRange(facts api.PackageFacts, policy EpisodeRangePolicy) []api.RuleFailure {
	rule := predicateRule(policy.Evidence, "complete_episode_range")
	if !completeEvidence(facts.Status) {
		return predicateMissing(rule, "complete local episode evidence is required", facts.Status, policy.Evidence)
	}
	ranges := facts.DetectedEpisodes
	if policy.Season > 0 {
		ranges = slices.DeleteFunc(slices.Clone(ranges), func(candidate api.SeasonEpisodeFacts) bool {
			return candidate.Season != policy.Season
		})
	}
	if len(ranges) == 0 {
		return predicateViolation(rule, "required local episode range is missing", facts.Status, policy.Evidence)
	}
	for _, season := range ranges {
		if missing := missingEpisodeNumbers(season.Episodes, policy.FirstEpisode, policy.LastEpisode); len(missing) > 0 {
			return predicateViolation(
				rule,
				fmt.Sprintf("season %d local episode range is incomplete", season.Season),
				facts.Status,
				policy.Evidence,
			)
		}
	}
	return nil
}

func missingEpisodeNumbers(episodes []int, requestedFirst int, requestedLast int) []int {
	if len(episodes) == 0 {
		return []int{0}
	}
	ordered := slices.Clone(episodes)
	slices.Sort(ordered)
	ordered = slices.Compact(ordered)
	first := requestedFirst
	if first <= 0 {
		first = ordered[0]
	}
	last := requestedLast
	if last <= 0 {
		last = ordered[len(ordered)-1]
	}
	if last < first {
		return []int{first}
	}
	known := make(map[int]struct{}, len(ordered))
	for _, episode := range ordered {
		known[episode] = struct{}{}
	}
	missing := make([]int, 0)
	for episode := first; episode <= last; episode++ {
		if _, ok := known[episode]; !ok {
			missing = append(missing, episode)
		}
	}
	return missing
}

// MediaUniformityField identifies one per-file fact that must be uniform.
type MediaUniformityField string

const (
	MediaUniformityFieldUnknown           MediaUniformityField = ""
	MediaUniformityFieldContainer         MediaUniformityField = "container"
	MediaUniformityFieldSource            MediaUniformityField = "source"
	MediaUniformityFieldResolution        MediaUniformityField = "resolution"
	MediaUniformityFieldVideoCodec        MediaUniformityField = "video_codec"
	MediaUniformityFieldVideoEncode       MediaUniformityField = "video_encode"
	MediaUniformityFieldBitDepth          MediaUniformityField = "bit_depth"
	MediaUniformityFieldVideoTrackCount   MediaUniformityField = "video_track_count"
	MediaUniformityFieldAudioLanguages    MediaUniformityField = "audio_languages"
	MediaUniformityFieldSubtitleLanguages MediaUniformityField = "subtitle_languages"
)

// PerFileUniformityPolicy selects media fields that must match across files.
type PerFileUniformityPolicy struct {
	Evidence EvidencePredicatePolicy
	Fields   []MediaUniformityField
}

// ValidatePerFileUniformity rejects known per-file differences and requires
// complete all-media evidence before returning success.
func ValidatePerFileUniformity(facts api.MediaFileFacts, policy PerFileUniformityPolicy) []api.RuleFailure {
	rule := predicateRule(policy.Evidence, "per_file_uniformity")
	fields := slices.Clone(policy.Fields)
	if len(fields) == 0 {
		fields = []MediaUniformityField{
			MediaUniformityFieldContainer,
			MediaUniformityFieldSource,
			MediaUniformityFieldResolution,
			MediaUniformityFieldVideoCodec,
			MediaUniformityFieldBitDepth,
			MediaUniformityFieldVideoTrackCount,
		}
	}
	missingField := false
	for _, field := range fields {
		expected := ""
		for _, file := range facts.Files {
			value, known := mediaUniformityValue(file, field)
			if !known {
				missingField = true
				continue
			}
			if expected == "" {
				expected = value
				continue
			}
			if !strings.EqualFold(expected, value) {
				return predicateViolation(
					rule,
					fmt.Sprintf("media files have non-uniform %s", field),
					facts.Status,
					policy.Evidence,
				)
			}
		}
	}
	if missingField || facts.ExpectedFileCount <= 0 || len(facts.Files) != facts.ExpectedFileCount || !completeEvidence(facts.Status) {
		return predicateMissing(rule, "complete per-file media evidence is required", facts.Status, policy.Evidence)
	}
	return nil
}

func mediaUniformityValue(file api.MediaFileFact, field MediaUniformityField) (string, bool) {
	switch field {
	case MediaUniformityFieldContainer:
		return knownString(file.Container)
	case MediaUniformityFieldSource:
		return knownString(file.Source)
	case MediaUniformityFieldResolution:
		return knownString(file.Resolution)
	case MediaUniformityFieldVideoCodec:
		return knownString(file.VideoCodec)
	case MediaUniformityFieldVideoEncode:
		return knownString(file.VideoEncode)
	case MediaUniformityFieldBitDepth:
		return knownString(file.BitDepth)
	case MediaUniformityFieldVideoTrackCount:
		if file.VideoTrackCount <= 0 {
			return "", false
		}
		return strconv.Itoa(file.VideoTrackCount), true
	case MediaUniformityFieldAudioLanguages:
		return normalizedLanguageSet(file.AudioLanguages)
	case MediaUniformityFieldSubtitleLanguages:
		return normalizedLanguageSet(file.SubtitleLanguages)
	case MediaUniformityFieldUnknown:
		return "", false
	}
	return "", false
}

func knownString(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != ""
}

func normalizedLanguageSet(values []string) (string, bool) {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if language := languageutil.NormalizeLanguageDisplay(value); language != "" {
			normalized = append(normalized, strings.ToLower(language))
		}
	}
	slices.Sort(normalized)
	normalized = slices.Compact(normalized)
	if len(normalized) == 0 {
		return "", false
	}
	return strings.Join(normalized, ","), true
}

// MediaCombination is one allowed technical-fact row. Empty fields are
// wildcards.
type MediaCombination struct {
	Container  string
	VideoCodec string
	Source     string
	Resolution string
	BitDepth   string
}

// MediaConstraintPolicy configures per-file container, codec, source,
// resolution, bit-depth, video-track, and combination constraints.
type MediaConstraintPolicy struct {
	Evidence            EvidencePredicatePolicy
	AllowedContainers   []string
	AllowedVideoCodecs  []string
	AllowedSources      []string
	AllowedResolutions  []string
	AllowedBitDepths    []string
	MinBitDepth         int
	MinVideoTrackCount  int
	MaxVideoTrackCount  int
	AllowedCombinations []MediaCombination
}

// ValidateMediaConstraints applies tracker-neutral technical constraints to
// every supplied media file.
func ValidateMediaConstraints(facts api.MediaFileFacts, policy MediaConstraintPolicy) []api.RuleFailure {
	rule := predicateRule(policy.Evidence, "media_constraints")
	if len(facts.Files) == 0 {
		return predicateMissing(rule, "media-file evidence is unavailable", facts.TechnicalStatus, policy.Evidence)
	}
	missing := false
	for _, file := range facts.Files {
		if failureReason, unknown := validateMediaFileConstraints(file, policy); failureReason != "" {
			return predicateViolation(rule, failureReason, facts.TechnicalStatus, policy.Evidence)
		} else if unknown {
			missing = true
		}
	}
	if missing || facts.ExpectedFileCount <= 0 || len(facts.Files) != facts.ExpectedFileCount ||
		!completeEvidence(facts.TechnicalStatus) {
		return predicateMissing(rule, "complete technical media evidence is required", facts.TechnicalStatus, policy.Evidence)
	}
	return nil
}

func validateMediaFileConstraints(file api.MediaFileFact, policy MediaConstraintPolicy) (reason string, unknown bool) {
	checks := []struct {
		label   string
		value   string
		allowed []string
	}{
		{
			label:   "container",
			value:   file.Container,
			allowed: policy.AllowedContainers,
		},
		{
			label:   "video codec",
			value:   file.VideoCodec,
			allowed: policy.AllowedVideoCodecs,
		},
		{
			label:   "source",
			value:   file.Source,
			allowed: policy.AllowedSources,
		},
		{
			label:   "resolution",
			value:   file.Resolution,
			allowed: policy.AllowedResolutions,
		},
		{
			label:   "bit depth",
			value:   file.BitDepth,
			allowed: policy.AllowedBitDepths,
		},
	}
	for _, check := range checks {
		if len(check.allowed) == 0 {
			continue
		}
		if strings.TrimSpace(check.value) == "" {
			unknown = true
			continue
		}
		if !containsFolded(check.allowed, check.value) {
			return check.label + " is not allowed", unknown
		}
	}
	if policy.MinBitDepth > 0 {
		bitDepth, ok := parseBitDepth(file.BitDepth)
		if !ok {
			unknown = true
		} else if bitDepth < policy.MinBitDepth {
			return fmt.Sprintf("bit depth %d is below %d", bitDepth, policy.MinBitDepth), unknown
		}
	}
	if policy.MinVideoTrackCount > 0 || policy.MaxVideoTrackCount > 0 {
		switch {
		case file.VideoTrackCount <= 0:
			unknown = true
		case policy.MinVideoTrackCount > 0 && file.VideoTrackCount < policy.MinVideoTrackCount:
			return "video track count is below the required minimum", unknown
		case policy.MaxVideoTrackCount > 0 && file.VideoTrackCount > policy.MaxVideoTrackCount:
			return "video track count exceeds the allowed maximum", unknown
		}
	}
	if len(policy.AllowedCombinations) > 0 {
		combinationKnown, combinationAllowed := allowedMediaCombination(file, policy.AllowedCombinations)
		switch {
		case !combinationKnown:
			unknown = true
		case !combinationAllowed:
			return "technical media combination is not allowed", unknown
		}
	}
	return "", unknown
}

func allowedMediaCombination(file api.MediaFileFact, combinations []MediaCombination) (known bool, allowed bool) {
	known = true
	for _, combination := range combinations {
		if mediaCombinationNeedsUnknownFact(file, combination) {
			known = false
			continue
		}
		if mediaCombinationMatches(file, combination) {
			return true, true
		}
	}
	return known, false
}

func mediaCombinationNeedsUnknownFact(file api.MediaFileFact, combination MediaCombination) bool {
	return (strings.TrimSpace(combination.Container) != "" && strings.TrimSpace(file.Container) == "") ||
		(strings.TrimSpace(combination.VideoCodec) != "" && strings.TrimSpace(file.VideoCodec) == "") ||
		(strings.TrimSpace(combination.Source) != "" && strings.TrimSpace(file.Source) == "") ||
		(strings.TrimSpace(combination.Resolution) != "" && strings.TrimSpace(file.Resolution) == "") ||
		(strings.TrimSpace(combination.BitDepth) != "" && strings.TrimSpace(file.BitDepth) == "")
}

func mediaCombinationMatches(file api.MediaFileFact, combination MediaCombination) bool {
	return optionalFoldedMatch(combination.Container, file.Container) &&
		optionalFoldedMatch(combination.VideoCodec, file.VideoCodec) &&
		optionalFoldedMatch(combination.Source, file.Source) &&
		optionalFoldedMatch(combination.Resolution, file.Resolution) &&
		optionalFoldedMatch(combination.BitDepth, file.BitDepth)
}

func optionalFoldedMatch(expected string, actual string) bool {
	expected = strings.TrimSpace(expected)
	return expected == "" || strings.EqualFold(expected, strings.TrimSpace(actual))
}

func parseBitDepth(value string) (int, bool) {
	var digits strings.Builder
	for _, char := range strings.TrimSpace(value) {
		if char >= '0' && char <= '9' {
			digits.WriteRune(char)
		}
	}
	if digits.Len() == 0 {
		return 0, false
	}
	depth, err := strconv.Atoi(digits.String())
	return depth, err == nil && depth > 0
}

// AssetKind identifies one prepared resource channel.
type AssetKind string

const (
	AssetKindUnknown          AssetKind = ""
	AssetKindMediaInfoJSON    AssetKind = "mediainfo_json"
	AssetKindMediaInfoText    AssetKind = "mediainfo_text"
	AssetKindDVDVOBMediaInfo  AssetKind = "dvd_vob_mediainfo"
	AssetKindBDInfo           AssetKind = "bdinfo"
	AssetKindNFO              AssetKind = "nfo"
	AssetKindScreenshot       AssetKind = "screenshot"
	AssetKindHostedScreenshot AssetKind = "hosted_screenshot"
	AssetKindDVDMenu          AssetKind = "dvd_menu"
	AssetKindHostedDVDMenu    AssetKind = "hosted_dvd_menu"
)

// AssetRequirement identifies a required channel and minimum count. Counts
// below one are normalized to one.
type AssetRequirement struct {
	Kind         AssetKind
	MinimumCount int
}

// RequiredAssetPolicy configures prepared-resource requirements.
type RequiredAssetPolicy struct {
	Evidence     EvidencePredicatePolicy
	Requirements []AssetRequirement
}

// ValidateRequiredAssets returns one keyed failure per missing or unavailable
// prepared-resource channel.
func ValidateRequiredAssets(facts api.AssetFacts, policy RequiredAssetPolicy) []api.RuleFailure {
	failures := make([]api.RuleFailure, 0)
	baseRule := predicateRule(policy.Evidence, "required_asset")
	for _, requirement := range policy.Requirements {
		asset, ok := validationAssetEvidence(facts, requirement.Kind)
		rule := baseRule + "_" + string(requirement.Kind)
		if !ok {
			failures = append(failures, predicateMissing(
				baseRule+"_unknown",
				"required asset kind is unknown",
				api.MetadataEvidenceStatusUnavailable,
				policy.Evidence,
			)...)
			continue
		}
		if !completeEvidence(asset.Status) {
			failures = append(failures, predicateMissing(
				rule,
				fmt.Sprintf("%s readiness evidence is required", requirement.Kind),
				asset.Status,
				policy.Evidence,
			)...)
			continue
		}
		minimum := max(requirement.MinimumCount, 1)
		if !asset.Ready || asset.Count < minimum {
			failures = append(failures, predicateViolation(
				rule,
				fmt.Sprintf("%s requires at least %d prepared asset(s)", requirement.Kind, minimum),
				asset.Status,
				policy.Evidence,
			)...)
		}
	}
	return failures
}

func validationAssetEvidence(facts api.AssetFacts, kind AssetKind) (api.AssetEvidence, bool) {
	switch kind {
	case AssetKindMediaInfoJSON:
		return facts.MediaInfoJSON, true
	case AssetKindMediaInfoText:
		return facts.MediaInfoText, true
	case AssetKindDVDVOBMediaInfo:
		return facts.DVDVOBMediaInfo, true
	case AssetKindBDInfo:
		return facts.BDInfo, true
	case AssetKindNFO:
		return facts.NFO, true
	case AssetKindScreenshot:
		return facts.Screenshots, true
	case AssetKindHostedScreenshot:
		return facts.HostedScreenshots, true
	case AssetKindDVDMenu:
		return facts.DVDMenus, true
	case AssetKindHostedDVDMenu:
		return facts.HostedDVDMenus, true
	case AssetKindUnknown:
		return api.AssetEvidence{}, false
	}
	return api.AssetEvidence{}, false
}

// LanguageCombinationPolicy configures original- and English-language audio
// and subtitle requirements.
type LanguageCombinationPolicy struct {
	Evidence                           EvidencePredicatePolicy
	RequireOriginalAudio               bool
	RequireEnglishAudio                bool
	RequireOriginalOrEnglishAudio      bool
	RequireOriginalSubtitle            bool
	RequireEnglishSubtitle             bool
	RequireOriginalOrEnglishSubtitle   bool
	RequireEnglishSubtitleWithoutAudio bool
}

// ValidateLanguageCombination applies language requirements to every media
// file without inferring absent tracks from incomplete evidence.
func ValidateLanguageCombination(facts api.MediaFileFacts, policy LanguageCombinationPolicy) []api.RuleFailure {
	rule := predicateRule(policy.Evidence, "language_combination")
	original := languageutil.NormalizeLanguageDisplay(facts.OriginalLanguage)
	needsOriginal := policy.RequireOriginalAudio || policy.RequireOriginalOrEnglishAudio ||
		policy.RequireOriginalSubtitle || policy.RequireOriginalOrEnglishSubtitle
	if needsOriginal && original == "" {
		return predicateMissing(rule, "original-language evidence is required", facts.LanguageStatus, policy.Evidence)
	}
	if len(facts.Files) == 0 {
		return predicateMissing(rule, "media language evidence is unavailable", facts.LanguageStatus, policy.Evidence)
	}
	for _, file := range facts.Files {
		audio := normalizedLanguages(file.AudioLanguages)
		subtitles := normalizedLanguages(file.SubtitleLanguages)
		if languageEvidenceMissing(audio, subtitles, policy) {
			return predicateMissing(rule, "complete audio and subtitle evidence is required", facts.LanguageStatus, policy.Evidence)
		}
		if reason := languageViolationReason(audio, subtitles, original, policy); reason != "" {
			if !completeEvidence(facts.LanguageStatus) {
				return predicateMissing(rule, "complete audio and subtitle evidence is required", facts.LanguageStatus, policy.Evidence)
			}
			return predicateViolation(rule, reason, facts.LanguageStatus, policy.Evidence)
		}
	}
	if facts.ExpectedFileCount <= 0 || len(facts.Files) != facts.ExpectedFileCount || !completeEvidence(facts.LanguageStatus) {
		return predicateMissing(rule, "complete per-file language evidence is required", facts.LanguageStatus, policy.Evidence)
	}
	return nil
}

func normalizedLanguages(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if language := languageutil.NormalizeLanguageDisplay(value); language != "" {
			normalized = append(normalized, language)
		}
	}
	slices.Sort(normalized)
	return slices.Compact(normalized)
}

func languageEvidenceMissing(audio []string, subtitles []string, policy LanguageCombinationPolicy) bool {
	audioRequired := policy.RequireOriginalAudio || policy.RequireEnglishAudio || policy.RequireOriginalOrEnglishAudio ||
		policy.RequireEnglishSubtitleWithoutAudio
	subtitlesRequired := policy.RequireOriginalSubtitle || policy.RequireEnglishSubtitle ||
		policy.RequireOriginalOrEnglishSubtitle
	return (audioRequired && len(audio) == 0) || (subtitlesRequired && len(subtitles) == 0)
}

func languageViolationReason(
	audio []string,
	subtitles []string,
	original string,
	policy LanguageCombinationPolicy,
) string {
	hasOriginalAudio := containsFolded(audio, original)
	hasEnglishAudio := containsFolded(audio, "English")
	hasOriginalSubtitle := containsFolded(subtitles, original)
	hasEnglishSubtitle := containsFolded(subtitles, "English")
	switch {
	case policy.RequireOriginalAudio && !hasOriginalAudio:
		return "original-language audio is required"
	case policy.RequireEnglishAudio && !hasEnglishAudio:
		return "English audio is required"
	case policy.RequireOriginalOrEnglishAudio && !hasOriginalAudio && !hasEnglishAudio:
		return "original-language or English audio is required"
	case policy.RequireOriginalSubtitle && !hasOriginalSubtitle:
		return "original-language subtitles are required"
	case policy.RequireEnglishSubtitle && !hasEnglishSubtitle:
		return "English subtitles are required"
	case policy.RequireOriginalOrEnglishSubtitle && !hasOriginalSubtitle && !hasEnglishSubtitle:
		return "original-language or English subtitles are required"
	case policy.RequireEnglishSubtitleWithoutAudio && !hasEnglishAudio && !hasEnglishSubtitle:
		return "English subtitles are required when English audio is absent"
	default:
		return ""
	}
}

func predicateRule(policy EvidencePredicatePolicy, fallback string) string {
	if rule := strings.TrimSpace(policy.Rule); rule != "" {
		return rule
	}
	return fallback
}

func predicateViolation(
	rule string,
	reason string,
	status api.MetadataEvidenceStatus,
	policy EvidencePredicatePolicy,
) []api.RuleFailure {
	return []api.RuleFailure{NewEvidenceRuleFailure(
		rule,
		reason,
		policy.ViolationDisposition,
		status,
	)}
}

func predicateMissing(
	rule string,
	reason string,
	status api.MetadataEvidenceStatus,
	policy EvidencePredicatePolicy,
) []api.RuleFailure {
	return []api.RuleFailure{NewEvidenceRuleFailure(
		rule,
		reason,
		policy.MissingEvidenceDisposition,
		status,
	)}
}

func completeEvidence(status api.MetadataEvidenceStatus) bool {
	return normalizeRuleEvidenceStatus(status) == api.MetadataEvidenceStatusComplete
}

func containsNormalizedExtension(known []string, blocked []string) bool {
	for _, extension := range blocked {
		extension = strings.ToLower(strings.TrimSpace(extension))
		if extension != "" && !strings.HasPrefix(extension, ".") {
			extension = "." + extension
		}
		if slices.Contains(known, extension) {
			return true
		}
	}
	return false
}

func containsPackageKind(known []api.PackageFileKind, blocked []api.PackageFileKind) bool {
	return slices.ContainsFunc(known, func(kind api.PackageFileKind) bool {
		return slices.Contains(blocked, kind)
	})
}

func containsFolded(values []string, target string) bool {
	target = strings.TrimSpace(target)
	return target != "" && slices.ContainsFunc(values, func(value string) bool {
		return strings.EqualFold(strings.TrimSpace(value), target)
	})
}
