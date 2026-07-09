// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/pkg/api"
)

var noGroupTagPattern = regexp.MustCompile(`(?i)-(nogrp|nogroup|unknown|-unk-)`)
var (
	languageTagLookupOnce sync.Once
	languageTagLookup     map[string]language.Tag
)

func buildUnit3DName(tracker string, meta api.PreparedMetadata, cfg config.TrackerConfig) string {
	trackerName := strings.ToUpper(strings.TrimSpace(tracker))
	if trackerName == "RHD" {
		return buildRHDName(meta)
	}

	name := baseReleaseName(meta)
	if name == "" {
		return ""
	}

	switch trackerName {
	case "AITHER":
		return BuildAitherName(meta)
	case "ACM":
		return buildACMName(meta)
	case "CBR":
		return BuildCBRName(meta, cfg.TagForCustomRelease)
	case "DP":
		return buildDPName(name, meta)
	case "LCD":
		return BuildCBRName(meta, cfg.TagForCustomRelease)
	case "LDU":
		return buildLDUName(name, meta)
	case "LT":
		return buildLTName(name, meta)
	case "RF":
		return addNoGroupSuffix(name, meta, "NoGroup")
	case "SAM":
		return BuildCBRName(meta, cfg.TagForCustomRelease)
	case "OE":
		return addNoGroupSuffix(name, meta, "NOGRP")
	case "ULCX":
		return buildULCXName(name, meta)
	case "ZNTH":
		return buildZNTHName(name, meta)
	default:
		return name
	}
}

func baseReleaseName(meta api.PreparedMetadata) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func buildDPName(name string, meta api.PreparedMetadata) string {
	audioLabel := resolveDPAudioLabel(meta.AudioLanguages)
	if audioLabel != "" {
		name = strings.Replace(name, "Dual-Audio", audioLabel, 1)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func buildULCXName(name string, meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") && (strings.Contains(strings.ToLower(strings.TrimSpace(meta.Edition)), "hybrid") || meta.WebDV) {
		name = strings.Replace(name, "Hybrid ", "", 1)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func resolveDPAudioLabel(languages []string) string {
	normalized := make(map[string]struct{}, len(languages))
	for _, language := range languages {
		trimmed := strings.TrimSpace(language)
		if trimmed == "" {
			continue
		}
		normalized[strings.ToUpper(trimmed)] = struct{}{}
	}

	switch len(normalized) {
	case 0:
		return ""
	case 1:
		for value := range normalized {
			return value
		}
		return ""
	case 2:
		return "Dual-Audio"
	default:
		return "MULTi"
	}
}

func addNoGroupSuffix(name string, meta api.PreparedMetadata, suffix string) string {
	tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))
	normalizedName := noGroupTagPattern.ReplaceAllString(name, "")
	normalizedName = strings.TrimSpace(strings.Join(strings.Fields(normalizedName), " "))
	if tag != "" && !isNoGroupTag(tag) {
		return normalizedName
	}
	if normalizedName == "" {
		return normalizedName
	}
	if strings.HasSuffix(strings.ToUpper(normalizedName), "-"+strings.ToUpper(suffix)) {
		return normalizedName
	}
	return normalizedName + "-" + suffix
}

func buildLDUName(name string, meta api.PreparedMetadata) string {
	catID := resolveUnit3DLDUCategoryID(meta)
	nonEnglishOriginal := !isEnglishLanguageToken(resolveOriginalLanguage(meta))
	isoAudio, nonEnglishAudio := firstAudioLanguageCode(meta.AudioLanguages)
	isoSubtitle := firstSubtitleLanguageCode(meta.SubtitleLanguages)

	if catID == "18" && isoSubtitle != "" {
		return strings.TrimSpace(strings.Join(strings.Fields(name+" [Subs "+isoSubtitle+"]"), " "))
	}

	if !nonEnglishOriginal && !nonEnglishAudio {
		return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	}

	languageParts := make([]string, 0, 2)
	if isoAudio != "" {
		languageParts = append(languageParts, "["+isoAudio+"]")
	}
	if isoSubtitle != "" {
		languageParts = append(languageParts, "[Subs "+isoSubtitle+"]")
	}
	if len(languageParts) == 0 {
		return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	}

	return strings.TrimSpace(strings.Join(strings.Fields(name+" "+strings.Join(languageParts, " ")), " "))
}

func firstAudioLanguageCode(languages []string) (string, bool) {
	for _, value := range languages {
		code, english, ok := languageCode(value)
		if ok {
			return code, !english
		}
	}
	return "", false
}

func firstSubtitleLanguageCode(languages []string) string {
	for _, value := range languages {
		code, _, ok := languageCode(value)
		if ok {
			return code
		}
	}
	return ""
}

func languageCode(value string) (string, bool, bool) {
	normalized := languageutil.NormalizeLanguageDisplay(value)
	if normalized == "" {
		normalized = strings.TrimSpace(value)
	}
	tag, ok := parseLanguageTag(normalized)
	if !ok {
		return "", false, false
	}
	base, _ := tag.Base()
	if base.String() == "und" {
		return "", false, false
	}
	code := base.ISO3()
	if code == "" {
		return "", false, false
	}
	return strings.ToUpper(code), isEnglishLanguageTag(base.String()), true
}

func parseLanguageTag(value string) (language.Tag, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return language.Tag{}, false
	}
	if tag, err := language.Parse(trimmed); err == nil && tag != language.Und {
		return tag, true
	}
	normalized := languageutil.NormalizeLanguageDisplay(trimmed)
	if normalized == "" {
		normalized = trimmed
	}
	languageTagLookupOnce.Do(buildLanguageTagLookup)
	tag, ok := languageTagLookup[strings.ToLower(strings.TrimSpace(normalized))]
	if ok {
		return tag, true
	}
	return language.Tag{}, false
}

func buildLanguageTagLookup() {
	languageTagLookup = make(map[string]language.Tag)
	namer := display.Languages(language.English)
	for _, tag := range display.Supported.Tags() {
		name := strings.ToLower(strings.TrimSpace(namer.Name(tag)))
		if name == "" {
			continue
		}
		if _, exists := languageTagLookup[name]; exists {
			continue
		}
		languageTagLookup[name] = tag
	}
}

func resolveOriginalLanguage(meta api.PreparedMetadata) string {
	switch {
	case meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalLanguage)
	case meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.OriginalLanguage)
	default:
		return ""
	}
}

func isEnglishLanguageToken(value string) bool {
	normalized := languageutil.NormalizeLanguageDisplay(value)
	if normalized != "" {
		value = normalized
	}
	return isEnglishLanguageTag(strings.TrimSpace(value))
}

func isEnglishLanguageTag(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "english", "en", "eng", "en-us", "en-gb":
		return true
	default:
		return false
	}
}

func isNoGroupTag(tag string) bool {
	value := strings.ToLower(strings.TrimSpace(tag))
	switch value {
	case "nogrp", "nogroup", "unknown", "-unk-":
		return true
	default:
		return false
	}
}

// buildZNTHName applies ZNTH release-name policy before upload.
// TV names drop episode-title text when it appears before the resolution, while
// non-TV names prefer the IMDb year when it disagrees with the parsed release year.
func buildZNTHName(name string, meta api.PreparedMetadata) string {
	category := resolveUnit3DCategory(meta)
	if category == "TV" && strings.TrimSpace(meta.EpisodeTitle) != "" {
		resolution := resolveResolution(meta)
		if resolution != "" {
			name = replaceZNTHEpisodeTitle(name, meta.EpisodeTitle, resolution)
		}
	}

	if category != "TV" {
		imdbYear := 0
		if meta.ExternalMetadata.IMDB != nil {
			imdbYear = meta.ExternalMetadata.IMDB.Year
		}
		year := meta.Release.Year
		if imdbYear > 0 && year > 0 && imdbYear != year {
			name = replaceZNTHMovieYear(name, meta, year, imdbYear)
		}
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

// replaceZNTHEpisodeTitle removes the episode-title segment only when its
// normalized text appears immediately before a matching resolution token.
func replaceZNTHEpisodeTitle(name string, episodeTitle string, resolution string) string {
	normalizedTitle := normalizeZNTHAlphaNum(episodeTitle)
	if normalizedTitle == "" {
		return name
	}

	for _, resolutionStart := range findZNTHTokenIndexes(name, resolution) {
		titleStart, ok := findZNTHTitleStartBefore(name[:resolutionStart], normalizedTitle)
		if !ok {
			continue
		}
		return name[:titleStart] + name[resolutionStart:]
	}
	return name
}

// findZNTHTitleStartBefore returns the byte offset of the trailing segment in
// prefix whose alphanumeric-normalized text matches normalizedTitle.
func findZNTHTitleStartBefore(prefix string, normalizedTitle string) (int, bool) {
	candidates := []int{0}
	for i, r := range prefix {
		if !isZNTHAlphaNum(r) {
			candidates = append(candidates, i+len(string(r)))
		}
	}

	for i := len(candidates) - 1; i >= 0; i-- {
		start := candidates[i]
		if normalizeZNTHAlphaNum(prefix[start:]) == normalizedTitle {
			return start, true
		}
	}
	return 0, false
}

// replaceZNTHMovieYear replaces the parsed release-year token before the first
// matching resolution token, or before a trailing metadata release-group suffix
// when no resolution is known.
func replaceZNTHMovieYear(name string, meta api.PreparedMetadata, year int, imdbYear int) string {
	yearToken := strconv.Itoa(year)
	yearIndexes := findZNTHTokenIndexes(name, yearToken)
	if len(yearIndexes) == 0 {
		return name
	}

	searchEnd := len(name)
	if resolution := resolveResolution(meta); resolution != "" {
		resolutionIndexes := findZNTHTokenIndexes(name, resolution)
		if len(resolutionIndexes) > 0 {
			searchEnd = resolutionIndexes[0]
		}
	} else if groupStart, ok := findZNTHReleaseGroupStart(name, meta.Release.Group); ok {
		searchEnd = groupStart
	}

	replaceStart := -1
	for _, yearStart := range yearIndexes {
		if yearStart < searchEnd {
			replaceStart = yearStart
		}
	}
	if replaceStart == -1 {
		return name
	}

	replacement := strconv.Itoa(imdbYear)
	return name[:replaceStart] + replacement + name[replaceStart+len(yearToken):]
}

// findZNTHTokenIndexes returns original-string byte offsets for
// case-insensitive token matches bounded by non-alphanumeric ZNTH separators.
func findZNTHTokenIndexes(value string, token string) []int {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}

	tokenRunes := utf8.RuneCountInString(token)
	indexes := []int{}
	for start := range value {
		end, ok := endAfterZNTHRunes(value, start, tokenRunes)
		if !ok {
			break
		}
		if strings.EqualFold(value[start:end], token) && hasZNTHTokenBoundaries(value, start, end) {
			indexes = append(indexes, start)
		}
	}
	return indexes
}

// findZNTHReleaseGroupStart returns the byte offset of a trailing "-group"
// suffix only when group is a real parsed release group.
func findZNTHReleaseGroupStart(name string, group string) (int, bool) {
	group = strings.TrimSpace(group)
	if group == "" || isNoGroupTag(group) {
		return 0, false
	}

	trimmedName := strings.TrimRightFunc(name, unicode.IsSpace)
	groupStart, ok := foldSuffixStart(trimmedName, group)
	if !ok {
		return 0, false
	}

	boundary := groupStart
	for boundary > 0 {
		r, size := utf8.DecodeLastRuneInString(trimmedName[:boundary])
		if !unicode.IsSpace(r) {
			break
		}
		boundary -= size
	}
	if boundary > 0 && trimmedName[boundary-1] == '-' {
		return boundary - 1, true
	}
	return 0, false
}

// foldSuffixStart returns the byte offset where suffix starts when value ends
// with suffix under Unicode case folding.
func foldSuffixStart(value string, suffix string) (int, bool) {
	start := len(value)
	for range suffix {
		if start == 0 {
			return 0, false
		}
		_, size := utf8.DecodeLastRuneInString(value[:start])
		start -= size
	}
	return start, strings.EqualFold(value[start:], suffix)
}

// endAfterZNTHRunes returns the byte offset after count runes from start.
func endAfterZNTHRunes(value string, start int, count int) (int, bool) {
	end := start
	for range count {
		if end >= len(value) {
			return 0, false
		}
		_, size := utf8.DecodeRuneInString(value[end:])
		end += size
	}
	return end, true
}

// hasZNTHTokenBoundaries reports whether start and end are outside adjacent
// letters or digits in value.
func hasZNTHTokenBoundaries(value string, start int, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(value[:start])
		if isZNTHAlphaNum(r) {
			return false
		}
	}
	if end < len(value) {
		r, _ := utf8.DecodeRuneInString(value[end:])
		if isZNTHAlphaNum(r) {
			return false
		}
	}
	return true
}

// normalizeZNTHAlphaNum lowercases value and drops every non-alphanumeric rune.
func normalizeZNTHAlphaNum(value string) string {
	var b strings.Builder
	for _, r := range value {
		if isZNTHAlphaNum(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func isZNTHAlphaNum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func buildLTName(name string, meta api.PreparedMetadata) string {
	aka := ""
	title := ""
	origTitle := ""
	origLang := ""
	if meta.ExternalMetadata.TMDB != nil {
		aka = meta.ExternalMetadata.TMDB.RetrievedAKA
		title = meta.ExternalMetadata.TMDB.Title
		origTitle = meta.ExternalMetadata.TMDB.OriginalTitle
		origLang = meta.ExternalMetadata.TMDB.OriginalLanguage
	}
	if title == "" && meta.ExternalMetadata.IMDB != nil {
		title = meta.ExternalMetadata.IMDB.Title
	}
	if origTitle == "" {
		origTitle = title
	}
	ltName := name
	ltName = strings.ReplaceAll(ltName, "Dual-Audio", "")
	ltName = strings.ReplaceAll(ltName, "Dubbed", "")

	// Find the end of the title block (start of year, resolution, source, etc.)
	titleEndIdx := len(ltName)
	markerRegex := regexp.MustCompile(`(?i)\.(19\d\d|20\d\d|3d|480p|576p|720p|1080p|1080i|2160p|4k|mkv|avi|mp4|remux|bluray|blu-ray|web-dl|webdl|web|hdtv|dvdrip|dvd|bd25|bd50|uhd)\b`)
	if loc := markerRegex.FindStringIndex(ltName); loc != nil {
		titleEndIdx = loc[0]
	}

	titleBlock := ltName[:titleEndIdx]
	restOfName := ltName[titleEndIdx:]

	// Determine the correct target title to use
	targetTitle := title
	if origLang == "es" {
		// Use Spanish title for Spanish original series/movies
		if aka != "" {
			akaClean := strings.TrimSpace(strings.ReplaceAll(aka, "AKA", ""))
			if akaClean != "" {
				targetTitle = akaClean
			}
		} else if origTitle != "" {
			targetTitle = origTitle
		}
	}
	targetTitleDotted := strings.ReplaceAll(targetTitle, " ", ".")

	// If the title block contains AKA, or we need to replace a mismatched title:
	akaRegex := regexp.MustCompile(`(?i)[. _-]aka[. _-]`)
	if akaRegex.MatchString(titleBlock) || !strings.EqualFold(strings.ReplaceAll(titleBlock, ".", " "), targetTitle) {
		titleBlock = targetTitleDotted
	}

	ltName = titleBlock + restOfName

	isDisc := false
	if normalizeUnit3DTypeCandidate(meta.Type) == "DISC" || normalizeUnit3DTypeCandidate(meta.Release.Type) == "DISC" || meta.DiscType != "" {
		isDisc = true
	}

	if !isDisc {
		type rawMediaInfoDoc struct {
			Media struct {
				Track []map[string]any `json:"track"`
			} `json:"media"`
		}

		var tracks []map[string]any
		if meta.MediaInfoJSONPath != "" {
			if payload, err := os.ReadFile(meta.MediaInfoJSONPath); err == nil {
				var doc rawMediaInfoDoc
				if err := json.Unmarshal(payload, &doc); err == nil {
					tracks = doc.Media.Track
				}
			}
		}

		hasSpanishAudio := false
		hasLatino := false
		hasCastilian := false

		audioLatinoCheck := map[string]bool{
			"es-419": true, "es-mx": true, "es-ar": true, "es-cl": true, "es-ve": true,
			"es-bo": true, "es-co": true, "es-cr": true, "es-do": true, "es-ec": true,
			"es-sv": true, "es-gt": true, "es-hn": true, "es-ni": true, "es-pa": true,
			"es-py": true, "es-pe": true, "es-pr": true, "es-uy": true,
		}
		audioCastilianCheck := map[string]bool{
			"es": true, "es-es": true,
		}
		latinoKeywords := []string{"latino", "latin america"}
		castilianKeywords := []string{"castellano"}

		hasTracks := false
		if len(tracks) > 0 {
			for _, track := range tracks {
				trackType := ""
				if val, ok := track["@type"]; ok {
					trackType = strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", val)))
				}
				if trackType != "audio" {
					continue
				}
				hasTracks = true
				lang := strings.ToLower(namesTrackString(track, "Language", "Language_String", "Language_String2", "Language_String3"))
				titleText := strings.ToLower(namesTrackString(track, "Title", "Title_String", "Title_String2", "Title_String3"))

				if strings.Contains(titleText, "commentary") {
					continue
				}

				isLatinoTitle := false
				for _, kw := range latinoKeywords {
					if strings.Contains(titleText, kw) {
						isLatinoTitle = true
						break
					}
				}
				isCastilianTitle := false
				for _, kw := range castilianKeywords {
					if strings.Contains(titleText, kw) {
						isCastilianTitle = true
						break
					}
				}

				if audioLatinoCheck[lang] || (lang == "es" && isLatinoTitle) {
					hasLatino = true
					hasSpanishAudio = true
				} else if (lang == "es" && isCastilianTitle) || audioCastilianCheck[lang] {
					hasCastilian = true
					hasSpanishAudio = true
				}
			}
		}

		if !hasTracks {
			// Fallback to PreparedMetadata.AudioLanguages if MediaInfo JSON wasn't parsed
			for _, lang := range meta.AudioLanguages {
				if strings.EqualFold(lang, "Spanish") || strings.EqualFold(lang, "es") {
					hasSpanishAudio = true
					hasLatino = true
					break
				}
			}
		}

		tag := strings.TrimSpace(meta.Tag)
		// insertTagBracket inserts the label just before "-<tag>" so the result
		// is "… [LABEL]-TAG" rather than "…- [LABEL]TAG".
		insertTagBracket := func(s, label string) string {
			if tag != "" {
				sep := "-" + tag
				if idx := strings.LastIndex(s, sep); idx != -1 {
					return s[:idx] + " [" + label + "]" + s[idx:]
				}
			}
			return s + " [" + label + "]"
		}
		if hasSpanishAudio {
			if !hasLatino && hasCastilian {
				if !strings.Contains(ltName, "[CAST]") {
					ltName = insertTagBracket(ltName, "CAST")
				}
			}
		} else {
			if !strings.Contains(ltName, "[SUBS]") {
				ltName = insertTagBracket(ltName, "SUBS")
			}
		}
	}

	multipleDots := regexp.MustCompile(`\.{2,}`)
	ltName = multipleDots.ReplaceAllString(ltName, ".")
	multipleSpaces := regexp.MustCompile(`\s{2,}`)
	ltName = multipleSpaces.ReplaceAllString(ltName, " ")
	return strings.Trim(ltName, ". ")
}

func namesTrackString(track map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := track[key]; ok {
			trimmed := strings.TrimSpace(fmt.Sprintf("%v", value))
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}
