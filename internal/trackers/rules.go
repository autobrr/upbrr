// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/additional"
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

func EvaluateRules(ctx context.Context, tracker string, meta api.PreparedMetadata, logger api.Logger) []api.RuleFailure {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	switch name {
	case "AZ":
		return evaluateAZFamilyRules(name, meta)
	case "CZ":
		return evaluateAZFamilyRules(name, meta)
	case "PHD":
		return evaluateAZFamilyRules(name, meta)
	}
	rules, ok := additional.RulesFor(name)
	if !ok && name != "PTP" {
		return nil
	}

	failures := make([]api.RuleFailure, 0)
	addFailure := func(rule, reason string) {
		trimmed := strings.TrimSpace(reason)
		if trimmed == "" {
			trimmed = "rule requirement not met"
		}
		failures = append(failures, api.RuleFailure{Rule: rule, Reason: trimmed})
	}

	if rules.RequireUniqueID && !meta.ValidMediaInfo {
		addFailure("require_unique_id", "missing MediaInfo Unique ID")
	}
	if rules.RequireValidMISetting && !meta.ValidMediaInfoSettings {
		addFailure("require_valid_mi_setting", "missing MediaInfo encode settings")
	}

	if rules.RequireDiscOnly && !isDiscType(meta.DiscType) {
		addFailure("require_disc_only", "requires disc upload")
	}
	if name == "PTP" && !meta.TVPack {
		category := resolveCategory(meta)
		if category != "" && category != "movie" {
			addFailure("require_movie_only", fmt.Sprintf("category %s is not movie", category))
		}
	}
	if rules.RequireMovieOnly || rules.RequireTVOnly {
		category := resolveCategory(meta)
		if category != "" {
			if rules.RequireMovieOnly && category != "movie" {
				addFailure("require_movie_only", fmt.Sprintf("category %s is not movie", category))
			}
			if rules.RequireTVOnly && category != "tv" {
				addFailure("require_tv_only", fmt.Sprintf("category %s is not tv", category))
			}
		} else if logger != nil {
			logger.Debugf("trackers: %s rule category check skipped (missing category)", name)
		}
	}

	typeValue := resolveType(meta)
	if len(rules.RequireHEVCForTypes) > 0 {
		if hasTypeRequirement(typeValue, rules.RequireHEVCForTypes) && !isHEVC(meta) {
			addFailure("require_hevc", fmt.Sprintf("%s requires HEVC for %s", name, typeValue))
		}
	}

	if rules.MinResolution != "" {
		minResolution := strings.ToLower(strings.TrimSpace(rules.MinResolution))
		value := resolveResolution(meta)
		if value == "" {
			addFailure("min_resolution", "resolution required for "+name)
		} else if ruleResolutionOrder[value] < ruleResolutionOrder[minResolution] {
			addFailure("min_resolution", fmt.Sprintf("resolution %s below %s", value, minResolution))
		}
	}

	if rules.BlockAdult && isAdultContent(meta) {
		message := strings.TrimSpace(rules.AdultMessage)
		if message == "" {
			message = "adult content not allowed at " + name
		}
		addFailure("block_adult", message)
	}

	if rules.BlockDVDRip && strings.EqualFold(typeValue, "DVDRIP") {
		addFailure("block_dvdrip", "DVDRip not allowed")
	}
	if rules.BlockExternalSubs && hasReleaseToken(meta, []string{"extsub", "ext-sub", "external subs", "external subtitles"}) {
		addFailure("block_external_subs", "external subtitles not allowed")
	}
	if rules.BlockHardcodedSubs && hasReleaseToken(meta, []string{"hardsub", "hard-sub", "hardcoded"}) {
		addFailure("block_hardcoded_subs", "hardcoded subtitles not allowed")
	}

	if rules.BlockSingleFileFolder && hasSingleFileFolder(meta) {
		addFailure("block_single_file_folder", "single-file folders are not allowed")
	}

	if len(rules.BlockGroups) > 0 {
		group := strings.ToUpper(strings.TrimSpace(resolveGroup(meta)))
		if group != "" && containsAny([]string{group}, rules.BlockGroups) {
			addFailure("block_group", fmt.Sprintf("group %s not allowed", group))
		}
	}

	if len(rules.BlockGroupUnlessType) > 0 {
		group := strings.ToUpper(strings.TrimSpace(resolveGroup(meta)))
		if group != "" {
			if allowedTypes, ok := rules.BlockGroupUnlessType[group]; ok {
				if !hasTypeRequirement(typeValue, allowedTypes) {
					addFailure("block_group_unless_type", fmt.Sprintf("group %s only allowed for %s", group, strings.Join(allowedTypes, ", ")))
				}
			}
		}
	}

	if rules.RequireSceneNFO && meta.Scene && strings.TrimSpace(meta.SceneNFOPath) == "" {
		addFailure("require_scene_nfo", "scene release missing NFO")
	}

	if rules.RequireAudioLanguages && len(meta.AudioLanguages) == 0 {
		addFailure("require_audio_languages", "missing audio language data")
	}

	if rules.Language != nil {
		if ok, reason := evaluateLanguageRule(meta, rules.Language); !ok {
			addFailure("language_rule", reason)
		}
	}

	if rules.ExtraCheck != nil {
		result := rules.ExtraCheck(ctx, meta, logger)
		if !result.Allowed {
			addFailure("extra_check", result.Reason)
		}
	}

	return failures
}

func evaluateAZFamilyRules(name string, meta api.PreparedMetadata) []api.RuleFailure {
	failures := make([]api.RuleFailure, 0)
	add := func(rule, reason string) {
		failures = append(failures, api.RuleFailure{Rule: rule, Reason: reason})
	}

	category := strings.ToUpper(strings.TrimSpace(resolveCategory(meta)))
	if category != "MOVIE" && category != "TV" {
		add("content_type", "only movies and TV shows are allowed")
	}
	if meta.Anime {
		add("anime_redirect", "anime should be uploaded to AnimeTorrents instead")
	}

	origin := originCountries(meta)
	switch name {
	case "AZ":
		if intersects(origin, phdCountries()) {
			add("country_redirect", "major English-language content belongs on PrivateHD")
		} else if intersects(origin, cinemaZCountries()) {
			add("country_redirect", "non-Asian western content belongs on CinemaZ")
		}
	case "CZ":
		switch {
		case intersects(origin, phdCountries()) && !isOlderThan50Years(meta):
			add("country_redirect", "recent mainstream English content belongs on PrivateHD")
		case intersects(origin, azCountries()):
			add("country_redirect", "Asian content belongs on AvistaZ")
		case len(origin) > 0 && !intersects(origin, czAllowedCountries()):
			add("country_block", "content origin is outside CinemaZ allowed regions")
		}
	case "PHD":
		if isOlderThan50Years(meta) {
			add("age_redirect", "50+ year-old content belongs on CinemaZ")
		}
		switch {
		case intersects(origin, cinemaZCountries()):
			add("country_redirect", "European, South American, and African content belongs on CinemaZ")
		case intersects(origin, azCountries()):
			add("country_redirect", "Asian content belongs on AvistaZ")
		case len(origin) > 0 && !intersects(origin, phdCountries()):
			add("country_block", "PrivateHD only allows major English-language territories")
		}
	}

	if name == "PHD" {
		evaluatePHDTechnicalRules(meta, add)
	}

	return failures
}

func evaluatePHDTechnicalRules(meta api.PreparedMetadata, add func(string, string)) {
	if strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "480p") ||
		strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "576p") ||
		strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "480i") ||
		strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "576i") {
		add("sd_forbidden", "SD content is forbidden")
	}

	if !isDiscType(meta.DiscType) {
		container := strings.ToLower(strings.TrimSpace(meta.Container))
		if container != "" && container != "mkv" && container != "mp4" {
			add("container", "allowed containers: MKV, MP4")
		}
	}

	group := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-"))
	switch group {
	case "RARBG", "FGT", "GRYM", "TBS":
		add("group_block", "RARBG, FGT, Grym, and TBS are not allowed")
	}
	if group == "EVO" && !strings.EqualFold(strings.TrimSpace(meta.Source), "WEB") {
		add("group_block", "non-web EVO releases are not allowed")
	}

	codec := strings.ToLower(strings.TrimSpace(meta.VideoCodec))
	encode := strings.ToLower(strings.TrimSpace(meta.VideoEncode))
	releaseType := strings.ToLower(strings.TrimSpace(resolveType(meta)))
	source := strings.ToLower(strings.TrimSpace(meta.Source))
	bitDepth := strings.TrimSpace(meta.BitDepth)

	if releaseType == "remux" && codec != "" && codec != "mpeg-2" && codec != "vc-1" && codec != "h.264" && codec != "h.265" && codec != "avc" {
		add("video_codec", "BluRay remuxes require MPEG-2, VC-1, H.264, or H.265")
	}
	if releaseType == "encode" && strings.Contains(source, "bluray") && encode != "" && encode != "h.264" && encode != "h.265" && encode != "x264" && encode != "x265" {
		add("video_encode", "BluRay encodes require H.264/H.265 with x264/x265")
	}
	if (releaseType == "webdl" || releaseType == "web-dl") && source == "web" && encode != "" && encode != "h.264" && encode != "h.265" && encode != "vp9" {
		add("video_encode", "WEB-DL requires H.264, H.265, or VP9")
	}
	if releaseType == "encode" && source == "web" && encode != "" && encode != "h.264" && encode != "h.265" && encode != "x264" && encode != "x265" {
		add("video_encode", "WEB encodes require H.264/H.265 with x264/x265")
	}
	if releaseType == "encode" && encode == "x265" && bitDepth != "10" {
		add("bit_depth", "x265 encodes must be 10-bit")
	}
	if res := strings.ToLower(strings.TrimSpace(resolveResolution(meta))); strings.HasSuffix(res, "p") {
		value := strings.TrimSuffix(res, "p")
		if height, err := strconv.Atoi(value); err == nil && height > 1080 && (encode == "h.264" || encode == "x264") {
			add("video_encode", "H.264/x264 is only allowed for 1080p and below")
		}
	}
}

func originCountries(meta api.PreparedMetadata) []string {
	if meta.ExternalMetadata.TMDB != nil && len(meta.ExternalMetadata.TMDB.OriginCountry) > 0 {
		return meta.ExternalMetadata.TMDB.OriginCountry
	}
	return nil
}

func isOlderThan50Years(meta api.PreparedMetadata) bool {
	year := meta.Release.Year
	if year == 0 && meta.ExternalMetadata.TMDB != nil {
		year = meta.ExternalMetadata.TMDB.Year
	}
	return year > 0 && time.Now().UTC().Year()-year >= 50
}

func intersects(left []string, right map[string]struct{}) bool {
	for _, value := range left {
		if _, ok := right[strings.ToUpper(strings.TrimSpace(value))]; ok {
			return true
		}
	}
	return false
}

func azCountries() map[string]struct{} {
	return countrySet("BD", "BN", "BT", "CN", "HK", "ID", "IN", "JP", "KH", "KP", "KR", "LA", "LK", "MM", "MN", "MO", "MY", "NP", "PH", "PK", "SG", "TH", "TL", "TW", "VN")
}

func phdCountries() map[string]struct{} {
	return countrySet("AG", "AI", "AU", "BB", "BM", "BS", "BZ", "CA", "CW", "DM", "GB", "GD", "IE", "JM", "KN", "KY", "LC", "MS", "NZ", "PR", "TC", "TT", "US", "VC", "VG", "VI")
}

func cinemaZCountries() map[string]struct{} {
	all := countrySet("AO", "BF", "BI", "BJ", "BW", "CD", "CF", "CG", "CI", "CM", "CV", "DJ", "DZ", "EG", "EH", "ER", "ET", "GA", "GH", "GM", "GN", "GQ", "GW", "IO", "KE", "KM", "LR", "LS", "LY", "MA", "MG", "ML", "MR", "MU", "MW", "MZ", "NA", "NE", "NG", "RE", "RW", "SC", "SD", "SH", "SL", "SN", "SO", "SS", "ST", "SZ", "TD", "TF", "TG", "TN", "TZ", "UG", "YT", "ZA", "ZM", "ZW",
		"AG", "AI", "AR", "AW", "BB", "BL", "BM", "BO", "BQ", "BR", "BS", "BV", "BZ", "CA", "CL", "CO", "CR", "CU", "CW", "DM", "DO", "EC", "FK", "GD", "GF", "GL", "GP", "GS", "GT", "GY", "HN", "HT", "JM", "KN", "KY", "LC", "MF", "MQ", "MS", "MX", "NI", "PA", "PE", "PM", "PR", "PY", "SR", "SV", "SX", "TC", "TT", "US", "UY", "VC", "VE", "VG", "VI",
		"AD", "AL", "AT", "AX", "BA", "BE", "BG", "BY", "CH", "CZ", "DE", "DK", "EE", "ES", "FI", "FO", "FR", "GB", "GG", "GI", "GR", "HR", "HU", "IE", "IM", "IS", "IT", "JE", "LI", "LT", "LU", "LV", "MC", "MD", "ME", "MK", "MT", "NL", "NO", "PL", "PT", "RO", "RS", "RU", "SE", "SI", "SJ", "SK", "SM", "SU", "UA", "VA", "XC",
		"AS", "AU", "CC", "CK", "CX", "FJ", "FM", "GU", "HM", "KI", "MH", "MP", "NC", "NF", "NR", "NU", "NZ", "PF", "PG", "PN", "PW", "SB", "TK", "TO", "TV", "UM", "VU", "WF", "WS")
	for code := range phdCountries() {
		delete(all, code)
	}
	for code := range azCountries() {
		delete(all, code)
	}
	return all
}

func czAllowedCountries() map[string]struct{} {
	return countrySet("AD", "AL", "AT", "AX", "BA", "BE", "BG", "BY", "CH", "CZ", "DE", "DK", "EE", "ES", "FI", "FO", "FR", "GI", "GR", "HR", "HU", "IS", "IT", "LI", "LT", "LU", "LV", "MC", "MD", "ME", "MK", "MT", "NL", "NO", "PL", "PT", "RO", "RS", "RU", "SE", "SI", "SJ", "SK", "SM", "SU", "UA", "VA", "XC",
		"AG", "AI", "AR", "AW", "BL", "BO", "BQ", "BR", "BV", "CL", "CO", "CU", "DO", "EC", "FK", "GF", "GL", "GP", "GS", "GT", "GY", "HN", "HT", "MF", "MQ", "MX", "NI", "PA", "PE", "PM", "PY", "SR", "SV", "SX", "UY", "VE",
		"AO", "BF", "BI", "BJ", "BW", "CD", "CF", "CG", "CI", "CM", "CV", "DJ", "DZ", "EG", "EH", "ER", "ET", "GA", "GH", "GM", "GN", "GQ", "GW", "IO", "KE", "KM", "LR", "LS", "LY", "MA", "MG", "ML", "MR", "MU", "MW", "MZ", "NA", "NE", "NG", "RE", "RW", "SC", "SD", "SH", "SL", "SN", "SO", "SS", "ST", "SZ", "TD", "TF", "TG", "TN", "TZ", "UG", "YT", "ZA", "ZM", "ZW",
		"AE", "BH", "CY", "IR", "IQ", "IL", "JO", "KW", "LB", "OM", "PS", "QA", "SA", "SY", "TR", "YE")
}

func countrySet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func resolveCategory(meta api.PreparedMetadata) string {
	if value := strings.ToLower(strings.TrimSpace(meta.ExternalIDs.Category)); value != "" {
		return value
	}
	if value := strings.ToLower(strings.TrimSpace(meta.MediaInfoCategory)); value != "" {
		return value
	}
	if meta.ExternalMetadata.TMDB != nil {
		if value := strings.ToLower(strings.TrimSpace(meta.ExternalMetadata.TMDB.Category)); value != "" {
			return value
		}
	}
	if value := strings.ToLower(strings.TrimSpace(meta.Release.Category)); value != "" {
		return value
	}
	return ""
}

func resolveType(meta api.PreparedMetadata) string {
	value := strings.ToUpper(strings.TrimSpace(meta.Type))
	if value == "" {
		value = strings.ToUpper(strings.TrimSpace(meta.Release.Type))
	}
	return value
}

func resolveGroup(meta api.PreparedMetadata) string {
	if group := strings.TrimSpace(meta.Release.Group); group != "" {
		return group
	}
	return strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-")
}

func resolveResolution(meta api.PreparedMetadata) string {
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

func isHEVC(meta api.PreparedMetadata) bool {
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

func hasSingleFileFolder(meta api.PreparedMetadata) bool {
	if isDiscType(meta.DiscType) {
		return false
	}
	if len(meta.FileList) != 1 {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(meta.FileList[0]), strings.TrimSpace(meta.SourcePath))
}

func hasReleaseToken(meta api.PreparedMetadata, tokens []string) bool {
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

func isAdultContent(meta api.PreparedMetadata) bool {
	candidates := append([]string{}, splitCSV(meta.Release.Genre)...)
	if meta.ExternalMetadata.TMDB != nil {
		candidates = append(candidates, splitCSV(meta.ExternalMetadata.TMDB.Genres)...)
		candidates = append(candidates, splitCSV(meta.ExternalMetadata.TMDB.Keywords)...)
	}
	if meta.ExternalMetadata.IMDB != nil {
		candidates = append(candidates, splitCSV(meta.ExternalMetadata.IMDB.Genres)...)
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

func evaluateLanguageRule(meta api.PreparedMetadata, rule *additional.LanguageRule) (bool, string) {
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

func resolveOriginalLanguage(meta api.PreparedMetadata) string {
	var raw string
	if meta.ExternalMetadata.TMDB != nil {
		raw = strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalLanguage)
	}
	if raw == "" && meta.ExternalMetadata.IMDB != nil {
		raw = strings.TrimSpace(meta.ExternalMetadata.IMDB.OriginalLanguage)
	}
	normalized := languageutil.NormalizeLanguageDisplay(raw)
	if normalized == "" {
		return ""
	}
	return strings.ToLower(normalized)
}
