// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/pkg/api"
)

var (
	azPHDLimitedPattern   = regexp.MustCompile(`(?i)\bLIMITED\b`)
	azPHDCriterionPattern = regexp.MustCompile(`(?i)\bCriterion Collection\b`)
	azPHDAnnivPattern     = regexp.MustCompile(`(?i)\b\d{1,3}(?:st|nd|rd|th)\s+Anniversary Edition\b`)
	azPHDDirCutPattern    = regexp.MustCompile("(?i)\\bDirector[’'`]s\\s+Cut\\b")
	azPHDExtCutPattern    = regexp.MustCompile(`(?i)\bExtended\s+Cut\b`)
	azPHDTheatrical       = regexp.MustCompile(`(?i)\bTheatrical\s+Cut\b`)
	azNoGroupPattern      = regexp.MustCompile(`(?i)-(?:nogrp|nogroup|unknown|unk)`)
)

// editName applies the site's naming policy only to generated names; explicit
// scene names and caller-provided names remain authoritative.
func editName(site siteDefinition, meta api.UploadSubject) string {
	name := selectedReleaseName(meta)
	if site.Name == "AZ" || site.Name == "CZ" {
		if sceneName := strings.TrimSpace(meta.SceneName); sceneName != "" {
			return sceneName
		}
		if !isGeneratedReleaseName(meta, name) {
			return name
		}
		name = editGeneratedName(site, meta, name)
	} else {
		name = editPHDName(meta, name)
	}

	tag := normalizedReleaseGroup(meta.Tag)
	if tag == "" || isNoGroupName(tag) {
		name = azNoGroupPattern.ReplaceAllString(name, "")
		switch site.Name {
		case "CZ":
			name += "-NoGroup"
		case "PHD":
			name += "-NOGROUP"
		}
	}
	return strings.Join(strings.Fields(name), " ")
}

func selectedReleaseName(meta api.UploadSubject) string {
	for _, candidate := range []string{meta.ReleaseName, meta.ReleaseNameNoTag, meta.Filename} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func isGeneratedReleaseName(meta api.UploadSubject, name string) bool {
	name = strings.TrimSpace(name)
	variants := []api.ReleaseNameVariant{
		meta.GeneratedReleaseNames.IncludeEpisodeTitle,
		meta.GeneratedReleaseNames.OmitEpisodeTitle,
	}
	for _, variant := range variants {
		for _, candidate := range []string{variant.Name, variant.NameNoTag, variant.CleanName} {
			if name != "" && name == strings.TrimSpace(candidate) {
				return true
			}
		}
	}
	return false
}

// editGeneratedName rebuilds the title/year/season prefix while preserving the
// generated release's technical suffix.
func editGeneratedName(site siteDefinition, meta api.UploadSubject, name string) string {
	title := avistaZEnglishTitle(meta)
	if site.Name == "CZ" {
		title = cinemaZTitle(meta)
	}
	if title == "" {
		title = strings.TrimSpace(meta.Release.Title)
	}
	year := releaseYear(meta)
	if isTV(meta) {
		seasonEpisode := releaseSeasonEpisode(meta)
		if seasonEpisode != "" {
			if suffix, ok := suffixAfterNameElement(name, seasonEpisode); ok {
				prefix := []string{title}
				switch {
				case site.Name == "AZ" && isSeasonPack(meta):
					prefix = append(prefix, seasonEpisode, formattedYear(year))
				case site.Name == "AZ":
					prefix = append(prefix, seasonEpisode)
				default:
					prefix = append(prefix, formattedYear(year), seasonEpisode)
				}
				name = joinNameWithSuffix(strings.Join(nonEmptyStrings(prefix), " "), suffix)
			}
		} else if year > 0 {
			if suffix, ok := suffixAfterNameElement(name, strconv.Itoa(year)); ok {
				prefix := title
				if site.Name == "CZ" {
					prefix = strings.TrimSpace(prefix + " " + strconv.Itoa(year))
				}
				name = joinNameWithSuffix(prefix, suffix)
			}
		}
	} else if !isTV(meta) && year > 0 {
		if suffix, ok := suffixAfterNameElement(name, strconv.Itoa(year)); ok {
			name = joinNameWithSuffix(strings.TrimSpace(title+" "+strconv.Itoa(year)), suffix)
		}
	}
	return removeGeneratedLanguageMarkers(name)
}

func editPHDName(meta api.UploadSubject, name string) string {
	if meta.ProviderMetadata.TMDB != nil {
		if originalTitle := strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalTitle); originalTitle != "" {
			name = strings.ReplaceAll(name, originalTitle, "")
		}
	}
	name = strings.ReplaceAll(name, "Dubbed", "")
	name = strings.ReplaceAll(name, "Dual-Audio", "")
	name = azPHDLimitedPattern.ReplaceAllString(name, "")
	name = azPHDCriterionPattern.ReplaceAllString(name, "")
	name = azPHDAnnivPattern.ReplaceAllString(name, "")
	name = azPHDDirCutPattern.ReplaceAllString(name, "DC")
	name = azPHDExtCutPattern.ReplaceAllString(name, "Extended")
	name = azPHDTheatrical.ReplaceAllString(name, "Theatrical")
	if meta.HasEncodeSettings {
		name = strings.ReplaceAll(name, "H.264", "x264")
		name = strings.ReplaceAll(name, "H.265", "x265")
	}
	if isTV(meta) && meta.Release.Year > 0 {
		name = strings.ReplaceAll(name, strconv.Itoa(meta.Release.Year), "")
	}
	source := strings.TrimSpace(meta.Source)
	if strings.EqualFold(strings.TrimSpace(meta.Type), "DVDRIP") && source != "" {
		name = replaceNameElements(name, source, "")
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		if region := strings.TrimSpace(meta.Region); region != "" {
			name = strings.ReplaceAll(name, region, "")
		}
		if resolution := strings.TrimSpace(meta.Release.Resolution); source != "" && resolution != "" {
			name = replaceNameElements(name, source, resolution)
		}
		if audio := strings.TrimSpace(meta.Audio); audio != "" {
			codec := strings.TrimSpace(meta.VideoCodec)
			name = strings.ReplaceAll(name, audio, strings.TrimSpace(audio+" "+codec))
		}
	}
	return name
}

func avistaZEnglishTitle(meta api.UploadSubject) string {
	if isTV(meta) && meta.ProviderMetadata.TVDB != nil {
		if title := strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish); title != "" {
			return title
		}
	}
	if meta.ProviderMetadata.TMDB != nil {
		if title := strings.TrimSpace(meta.ProviderMetadata.TMDB.Title); title != "" {
			return title
		}
	}
	if meta.ProviderMetadata.IMDB != nil {
		if title := strings.TrimSpace(meta.ProviderMetadata.IMDB.Title); title != "" {
			return title
		}
	}
	return strings.TrimSpace(meta.Release.Title)
}

// cinemaZTitle selects an English or transliterated title for CinemaZ routing,
// falling back to the original title when no safe alternative is available.
func cinemaZTitle(meta api.UploadSubject) string {
	if title := cinemaZEnglishCountryAKA(meta.ProviderMetadata.IMDB); title != "" {
		return title
	}
	original := cinemaZOriginalTitle(meta)
	if original == "" || !containsNonLatinLetter(original) {
		return original
	}
	transliterated := transliterateCinemaZTitle(original)
	if !containsNonLatinLetter(transliterated) {
		return transliterated
	}
	for _, candidate := range cinemaZRomanizedCandidates(meta) {
		if candidate != "" && !containsNonLatinLetter(candidate) {
			return candidate
		}
	}
	return original
}

func cinemaZEnglishCountryAKA(metadata *api.IMDBMetadata) string {
	if metadata == nil {
		return ""
	}
	countries := imdbCountries(metadata)
	for _, aka := range metadata.Akas {
		title := strings.TrimSpace(aka.Title)
		country := strings.TrimSpace(aka.Country)
		if title == "" || country == "" || !isEnglishName(aka.Language) {
			continue
		}
		if len(countries) == 0 || containsEqualFold(countries, country) {
			return title
		}
	}
	return ""
}

func imdbCountries(metadata *api.IMDBMetadata) []string {
	countries := make([]string, 0)
	if country := strings.TrimSpace(metadata.Country); country != "" {
		countries = append(countries, country)
	}
	for country := range strings.SplitSeq(metadata.CountryList, ",") {
		country = strings.TrimSpace(country)
		if country != "" && !containsEqualFold(countries, country) {
			countries = append(countries, country)
		}
	}
	return countries
}

func isEnglishName(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "en", "eng", "english":
		return true
	default:
		return false
	}
}

func containsEqualFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func cinemaZOriginalTitle(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil {
		if title := strings.TrimSpace(meta.ProviderMetadata.IMDB.AKA); title != "" {
			return title
		}
	}
	if meta.ProviderMetadata.TMDB != nil {
		if title := strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalTitle); title != "" {
			return title
		}
	}
	if isTV(meta) && meta.ProviderMetadata.TVDB != nil {
		if title := strings.TrimSpace(meta.ProviderMetadata.TVDB.Name); title != "" {
			return title
		}
	}
	return strings.TrimSpace(meta.Release.Title)
}

func cinemaZRomanizedCandidates(meta api.UploadSubject) []string {
	candidates := make([]string, 0, 5)
	if meta.ProviderMetadata.TMDB != nil {
		candidates = append(candidates, trimAKAPrefix(meta.ProviderMetadata.TMDB.RetrievedAKA))
	}
	if meta.ProviderMetadata.IMDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.IMDB.Title))
	}
	if meta.ProviderMetadata.TMDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TMDB.Title))
	}
	if isTV(meta) && meta.ProviderMetadata.TVDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish))
	}
	return append(candidates, strings.TrimSpace(meta.Release.Title))
}

func trimAKAPrefix(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > len("AKA ") && strings.EqualFold(value[:len("AKA ")], "AKA ") {
		return strings.TrimSpace(value[len("AKA "):])
	}
	return value
}

func releaseYear(meta api.UploadSubject) int {
	if meta.Release.Year > 0 {
		return meta.Release.Year
	}
	if meta.ProviderMetadata.IMDB != nil {
		if isTV(meta) && meta.ProviderMetadata.IMDB.TVYear > 0 {
			return meta.ProviderMetadata.IMDB.TVYear
		}
		if meta.ProviderMetadata.IMDB.Year > 0 {
			return meta.ProviderMetadata.IMDB.Year
		}
	}
	if isTV(meta) && meta.ProviderMetadata.TVDB != nil && meta.ProviderMetadata.TVDB.Year > 0 {
		return meta.ProviderMetadata.TVDB.Year
	}
	if meta.ProviderMetadata.TMDB != nil {
		return meta.ProviderMetadata.TMDB.Year
	}
	return 0
}

func releaseSeasonEpisode(meta api.UploadSubject) string {
	if value := strings.TrimSpace(meta.SeasonStr + meta.EpisodeStr); value != "" {
		return value
	}
	if meta.SeasonInt > 0 && meta.EpisodeInt > 0 {
		return fmtSeasonEpisode(meta.SeasonInt, meta.EpisodeInt)
	}
	if meta.SeasonInt > 0 {
		return fmtSeason(meta.SeasonInt)
	}
	return ""
}

func fmtSeasonEpisode(season, episode int) string {
	return "S" + twoDigitNumber(season) + "E" + twoDigitNumber(episode)
}

func fmtSeason(season int) string {
	return "S" + twoDigitNumber(season)
}

func twoDigitNumber(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func isSeasonPack(meta api.UploadSubject) bool {
	return meta.TVPack || (meta.SeasonInt > 0 && meta.EpisodeInt == 0 && strings.TrimSpace(meta.EpisodeStr) == "")
}

func formattedYear(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}

func nonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func suffixAfterNameElement(name, element string) (string, bool) {
	nameRunes := []rune(name)
	elementRunes := []rune(strings.TrimSpace(element))
	if len(nameRunes) == 0 || len(elementRunes) == 0 || len(elementRunes) > len(nameRunes) {
		return "", false
	}
	lastEnd := -1
	for start := 0; start <= len(nameRunes)-len(elementRunes); start++ {
		if start > 0 && !isNameSeparator(nameRunes[start-1]) {
			continue
		}
		end := start + len(elementRunes)
		if end < len(nameRunes) && !isNameSeparator(nameRunes[end]) {
			continue
		}
		if strings.EqualFold(string(nameRunes[start:end]), string(elementRunes)) {
			lastEnd = end
		}
	}
	if lastEnd < 0 {
		return "", false
	}
	rawSuffix := string(nameRunes[lastEnd:])
	suffix := strings.TrimLeftFunc(rawSuffix, isNameSeparator)
	if strings.HasPrefix(rawSuffix, "-") && suffix != "" && !strings.ContainsAny(suffix, " ._") {
		suffix = "-" + suffix
	}
	return suffix, true
}

func replaceNameElements(name, element, replacement string) string {
	nameRunes := []rune(name)
	elementRunes := []rune(strings.TrimSpace(element))
	if len(nameRunes) == 0 || len(elementRunes) == 0 || len(elementRunes) > len(nameRunes) {
		return name
	}
	var result strings.Builder
	last := 0
	replaced := false
	for start := 0; start <= len(nameRunes)-len(elementRunes); {
		end := start + len(elementRunes)
		if (start == 0 || isNameSeparator(nameRunes[start-1])) &&
			(end == len(nameRunes) || isNameSeparator(nameRunes[end])) &&
			strings.EqualFold(string(nameRunes[start:end]), string(elementRunes)) {
			result.WriteString(string(nameRunes[last:start]))
			result.WriteString(replacement)
			last = end
			start = end
			replaced = true
			continue
		}
		start++
	}
	if !replaced {
		return name
	}
	result.WriteString(string(nameRunes[last:]))
	return result.String()
}

func joinNameWithSuffix(prefix, suffix string) string {
	if strings.HasPrefix(suffix, "-") {
		return strings.TrimSpace(prefix) + suffix
	}
	return strings.TrimSpace(strings.Join(nonEmptyStrings([]string{prefix, suffix}), " "))
}

func isNameSeparator(value rune) bool {
	return unicode.IsSpace(value) || value == '.' || value == '_' || value == '-'
}

func removeGeneratedLanguageMarkers(name string) string {
	fields := strings.Fields(name)
	result := fields[:0]
	for _, field := range fields {
		if strings.EqualFold(field, "Dubbed") || strings.EqualFold(field, "Dual-Audio") {
			continue
		}
		result = append(result, field)
	}
	return strings.Join(result, " ")
}

func normalizedReleaseGroup(tag string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tag), "-"))
}

func isNoGroupName(tag string) bool {
	switch strings.ToLower(strings.TrimSpace(tag)) {
	case "nogrp", "nogroup", "unknown", "unk":
		return true
	default:
		return false
	}
}

func containsNonLatinLetter(value string) bool {
	for _, current := range value {
		if unicode.IsLetter(current) && !unicode.In(current, unicode.Latin) {
			return true
		}
	}
	return false
}

func transliterateCinemaZTitle(value string) string {
	var result strings.Builder
	for _, current := range value {
		replacement, ok := cinemaZTransliteration[unicode.ToLower(current)]
		if !ok {
			result.WriteRune(current)
			continue
		}
		if unicode.IsUpper(current) && replacement != "" {
			replacementRunes := []rune(replacement)
			replacementRunes[0] = unicode.ToUpper(replacementRunes[0])
			replacement = string(replacementRunes)
		}
		result.WriteString(replacement)
	}
	return strings.Join(strings.Fields(result.String()), " ")
}

// resolveSearchName returns the canonical AZ-family search key used for dupe
// lookup, independent of the upload display name.
func resolveSearchName(meta api.UploadSubject) string {
	if title := strings.TrimSpace(meta.Release.Title); title != "" {
		return title
	}
	if meta.ProviderMetadata.TMDB != nil {
		if title := strings.TrimSpace(meta.ProviderMetadata.TMDB.Title); title != "" {
			return title
		}
	}
	return strings.TrimSpace(meta.Filename)
}

var cinemaZTransliteration = map[rune]string{
	'а': "a",
	'б': "b",
	'в': "v",
	'г': "g",
	'д': "d",
	'е': "e",
	'ё': "yo",
	'ж': "zh",
	'з': "z",
	'и': "i",
	'й': "y",
	'к': "k",
	'л': "l",
	'м': "m",
	'н': "n",
	'о': "o",
	'п': "p",
	'р': "r",
	'с': "s",
	'т': "t",
	'у': "u",
	'ф': "f",
	'х': "kh",
	'ц': "ts",
	'ч': "ch",
	'ш': "sh",
	'щ': "shch",
	'ъ': "",
	'ы': "y",
	'ь': "",
	'э': "e",
	'ю': "yu",
	'я': "ya",
	'є': "ye",
	'і': "i",
	'ї': "yi",
	'ґ': "g",
	'α': "a",
	'β': "v",
	'γ': "g",
	'δ': "d",
	'ε': "e",
	'ζ': "z",
	'η': "i",
	'θ': "th",
	'ι': "i",
	'κ': "k",
	'λ': "l",
	'μ': "m",
	'ν': "n",
	'ξ': "x",
	'ο': "o",
	'π': "p",
	'ρ': "r",
	'σ': "s",
	'ς': "s",
	'τ': "t",
	'υ': "y",
	'φ': "f",
	'χ': "ch",
	'ψ': "ps",
	'ω': "o",
}
