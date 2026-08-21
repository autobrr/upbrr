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
	czLimitedPattern      = regexp.MustCompile(`(?i)\bLIMITED\b`)
	czCriterionPattern    = regexp.MustCompile(`(?i)\bCriterion\s+Collection\b`)
	czResolutionTag       = regexp.MustCompile(`(?i)\b(?:2K|4K)\b`)
	czAnniversaryPattern  = regexp.MustCompile(`(?i)\b\d{1,3}(?:st|nd|rd|th)\s+Anniversary(?:\s+Edition)?\b`)
	czExtendedPattern     = regexp.MustCompile(`(?i)\b(?:Extended(?:\s+Cut)?|EXT)\b`)
	czDirectorCutPattern  = regexp.MustCompile("(?i)\\b(?:Director[’'`]s\\s+Cut|Directors\\s+Cut|DC)\\b")
	czTheatricalPattern   = regexp.MustCompile(`(?i)\b(?:Theatrical\s+Cut|TC)\b`)
	czUppercaseTagPattern = regexp.MustCompile(`(?i)\b(?:REPACK|PROPER|RESTORED|REMASTERED)\b`)
)

// editName applies AZ/CZ policy only to structurally generated names, preserving
// scene and caller-provided names; PHD retains its legacy normalization. CinemaZ
// returns an empty name when no Latin-safe generated result can be built.
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
		if site.Name == "CZ" && (name == "" || containsNonLatinLetter(name)) {
			return ""
		}
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

// selectedReleaseName returns the first retained name in upload, no-tag, then
// filename order.
func selectedReleaseName(meta api.UploadSubject) string {
	for _, candidate := range []string{meta.ReleaseName, meta.ReleaseNameNoTag, meta.Filename} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// isGeneratedReleaseName reports whether name matches a canonical structural
// variant. Empty variants leave the selected name exact and unmodified.
func isGeneratedReleaseName(meta api.UploadSubject, name string) bool {
	return releaseNameVariantMatches(meta.GeneratedReleaseNames.IncludeEpisodeTitle, name) ||
		releaseNameVariantMatches(meta.GeneratedReleaseNames.OmitEpisodeTitle, name)
}

func releaseNameVariantMatches(variant api.ReleaseNameVariant, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, candidate := range []string{variant.Name, variant.NameNoTag, variant.CleanName} {
		if name == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}

// editGeneratedName replaces the title/year/season prefix of an eligible
// generated name. CinemaZ then normalizes guide-owned tags and technical order
// while retaining protected title and episode-title text.
func editGeneratedName(site siteDefinition, meta api.UploadSubject, name string) string {
	title := avistaZEnglishTitle(meta)
	if site.Name == "CZ" {
		title = cinemaZTitle(meta)
	}
	if title == "" {
		if site.Name == "CZ" {
			return ""
		}
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
	name = removeGeneratedLanguageMarkers(name)
	if site.Name == "CZ" {
		name = normalizeCinemaZGeneratedName(meta, title, name)
	}
	return name
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

// cinemaZTitle selects the first permitted country-scoped English IMDb AKA, or
// the original title when none qualifies. A non-Latin original may fall back to
// provider romanization and then supported local transliteration; an empty
// result means no Latin-safe title is available.
func cinemaZTitle(meta api.UploadSubject) string {
	original := cinemaZOriginalTitle(meta)
	originalUsesNonLatin := containsNonLatinLetter(original)
	if title := cinemaZEnglishCountryAKA(meta.ProviderMetadata.IMDB, originalUsesNonLatin); title != "" {
		return title
	}
	if original == "" || !containsNonLatinLetter(original) {
		return original
	}
	for _, candidate := range cinemaZRomanizedCandidates(meta) {
		if candidate != "" && !containsNonLatinLetter(candidate) {
			return candidate
		}
	}
	transliterated := transliterateCinemaZTitle(original)
	if transliterated != "" && !containsNonLatinLetter(transliterated) {
		return transliterated
	}
	return ""
}

// cinemaZEnglishCountryAKA returns the first Latin-safe English IMDb AKA with a
// non-Worldwide country and permitted attributes. A transliterated AKA is
// eligible only when the original title contains non-Latin letters.
func cinemaZEnglishCountryAKA(metadata *api.IMDBMetadata, originalUsesNonLatin bool) string {
	if metadata == nil {
		return ""
	}
	for _, aka := range metadata.Akas {
		title := strings.TrimSpace(aka.Title)
		country := strings.TrimSpace(aka.Country)
		if title == "" || containsNonLatinLetter(title) || !isCinemaZCountry(country) || !isEnglishName(aka.Language) ||
			cinemaZAKAAttributesDisallowed(aka.Attributes, originalUsesNonLatin) {
			continue
		}
		return title
	}
	return ""
}

// isCinemaZCountry treats any nonblank, non-Worldwide IMDb country label as
// country-scoped.
func isCinemaZCountry(country string) bool {
	country = strings.ToLower(strings.TrimSpace(country))
	if country == "" {
		return false
	}
	country = strings.NewReplacer("-", "", "_", "", " ", "").Replace(country)
	return !strings.Contains(country, "worldwide")
}

// cinemaZAKAAttributesDisallowed rejects informal, working, and festival titles,
// plus transliterated titles when the original already uses the Latin alphabet.
func cinemaZAKAAttributesDisallowed(attributes []string, originalUsesNonLatin bool) bool {
	for _, attribute := range attributes {
		attribute = strings.ToLower(strings.TrimSpace(attribute))
		switch {
		case strings.Contains(attribute, "informal"),
			strings.Contains(attribute, "working"),
			strings.Contains(attribute, "festival"):
			return true
		case strings.Contains(attribute, "transliter") && !originalUsesNonLatin:
			return true
		}
	}
	return false
}

func isEnglishName(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "en", "eng", "english":
		return true
	default:
		return false
	}
}

// cinemaZOriginalTitle returns the IMDb original title when present, followed by
// the TMDB original, parsed alternate title, and parsed primary title.
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

// cinemaZRomanizedCandidates returns provider and parsed title candidates in
// the order CinemaZ uses before local transliteration.
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

// normalizeCinemaZGeneratedName applies CinemaZ's suffix-only rules to one
// structural name. It protects the title/year/season and selected episode title,
// normalizes release tags, and rebuilds disc/remux technical tails from canonical
// metadata; a name without the expected prefix is returned unchanged.
func normalizeCinemaZGeneratedName(meta api.UploadSubject, title, name string) string {
	title = strings.TrimSpace(title)
	prefix := cinemaZGeneratedNamePrefix(meta, title)
	suffix := strings.TrimSpace(strings.TrimPrefix(name, prefix))
	if suffix == name {
		return name
	}
	if isTV(meta) && selectedGeneratedNameUsesIncludeVariant(meta) {
		for _, candidate := range cinemaZEpisodeTitleCandidates(meta) {
			if episodeTitle, remainder, ok := cutLeadingNameElement(suffix, candidate); ok {
				prefix = joinNameWithSuffix(prefix, episodeTitle)
				suffix = remainder
				break
			}
		}
	}
	for _, pattern := range []*regexp.Regexp{czLimitedPattern, czCriterionPattern, czResolutionTag, czAnniversaryPattern} {
		suffix = pattern.ReplaceAllString(suffix, "")
	}
	suffix = czExtendedPattern.ReplaceAllString(suffix, "EXT")
	suffix = czDirectorCutPattern.ReplaceAllString(suffix, "DC")
	suffix = czTheatricalPattern.ReplaceAllString(suffix, "TC")
	suffix = czUppercaseTagPattern.ReplaceAllStringFunc(suffix, strings.ToUpper)
	suffix = moveCinemaZHybridAfterResolution(suffix, meta.Release.Resolution)

	typeValue := strings.ToUpper(strings.TrimSpace(meta.Type))
	if typeValue == "" {
		typeValue = strings.ToUpper(strings.TrimSpace(meta.Release.Type))
	}
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	source := strings.TrimSpace(meta.Source)
	if source == "" {
		source = strings.TrimSpace(meta.Release.Source)
	}
	dvdSource := strings.EqualFold(source, "DVD") || strings.EqualFold(source, "PAL DVD") || strings.EqualFold(source, "NTSC DVD")
	bluRaySource := strings.EqualFold(source, "BluRay") || strings.EqualFold(source, "Blu-ray")
	resolution := strings.TrimSpace(meta.Release.Resolution)
	region := strings.TrimSpace(meta.Region)
	if region == "" {
		region = strings.TrimSpace(meta.Release.Region)
	}
	hybrid := ""
	if _, ok := suffixAfterNameElement(suffix, "HYBRID"); ok {
		hybrid = "HYBRID"
	}
	uhd := strings.TrimSpace(meta.UHD)
	if uhd == "" {
		if _, ok := suffixAfterNameElement(suffix, "UHD"); ok {
			uhd = "UHD"
		}
	}
	video := strings.TrimSpace(meta.VideoCodec)
	switch {
	case typeValue == "DVDRIP":
		video = strings.TrimSpace(meta.VideoEncode)
		if video == "" {
			video = strings.TrimSpace(meta.VideoCodec)
		}
		suffix = removeCinemaZNameElements(suffix, source, "DVD", "DVDRip", meta.Audio, meta.VideoEncode, meta.VideoCodec, video)
		suffix = reorderCinemaZTechnicalTail(suffix, meta.Tag, []string{"DVDRip", meta.Audio, video})
	case typeValue == "REMUX" && (dvdSource || (source == "" && discType == "DVD")):
		video = cinemaZDVDVideo(video)
		suffix = removeCinemaZNameElements(suffix, source, "DVD", "REMUX", "DVD Remux", resolution, meta.Audio, meta.VideoCodec, video)
		suffix = reorderCinemaZTechnicalTail(suffix, meta.Tag, []string{resolution, "DVD Remux", meta.Audio, video})
	case typeValue == "REMUX" && (bluRaySource || (source == "" && discType == "BDMV")):
		suffix = removeCinemaZNameElements(
			suffix,
			source,
			"BluRay",
			"Blu-ray",
			"REMUX",
			"BluRay REMUX",
			resolution,
			hybrid,
			uhd,
			meta.HDR,
			video,
			meta.Audio,
		)
		suffix = reorderCinemaZTechnicalTail(
			suffix,
			meta.Tag,
			[]string{resolution, hybrid, uhd, "BluRay REMUX", meta.HDR, video, meta.Audio},
		)
	case typeValue == "REMUX":
		suffix = reorderCinemaZTechnicalTail(suffix, meta.Tag, []string{meta.HDR, video, meta.Audio})
	case discType == "DVD":
		video = cinemaZDVDVideo(video)
		dvdSize := strings.TrimSpace(meta.Release.Size)
		if dvdSize == "" {
			switch {
			case nameHasElement(suffix, "DVD9"):
				dvdSize = "DVD9"
			case nameHasElement(suffix, "DVD5"):
				dvdSize = "DVD5"
			default:
				dvdSize = "DVD"
			}
		}
		suffix = removeCinemaZNameElements(
			suffix,
			region,
			source,
			"DVD",
			"DVD5",
			"DVD9",
			resolution,
			meta.Audio,
			meta.VideoCodec,
			video,
		)
		suffix = reorderCinemaZTechnicalTail(suffix, meta.Tag, []string{resolution, dvdSize, meta.Audio, video})
	case discType == "BDMV":
		suffix = removeCinemaZNameElements(
			suffix,
			source,
			"BluRay",
			"Blu-ray",
			"RAW",
			"Blu-ray RAW",
			resolution,
			hybrid,
			region,
			uhd,
			meta.HDR,
			video,
			meta.Audio,
		)
		suffix = reorderCinemaZTechnicalTail(
			suffix,
			meta.Tag,
			[]string{resolution, hybrid, region, uhd, "Blu-ray RAW", meta.HDR, video, meta.Audio},
		)
	}
	name = joinNameWithSuffix(prefix, suffix)
	return strings.Join(strings.Fields(name), " ")
}

// cinemaZGeneratedNamePrefix returns the title/year/season segment that bounds
// CinemaZ suffix normalization.
func cinemaZGeneratedNamePrefix(meta api.UploadSubject, title string) string {
	parts := []string{title}
	year := formattedYear(releaseYear(meta))
	if isTV(meta) {
		if seasonEpisode := releaseSeasonEpisode(meta); seasonEpisode != "" {
			return strings.Join(nonEmptyStrings(append(parts, year, seasonEpisode)), " ")
		}
	}
	return strings.Join(nonEmptyStrings(append(parts, year)), " ")
}

// selectedGeneratedNameUsesIncludeVariant reports whether the selected name
// matches an include-episode-title variant. Include and omit variants may be
// identical when a manual episode title is authoritative.
func selectedGeneratedNameUsesIncludeVariant(meta api.UploadSubject) bool {
	return releaseNameVariantMatches(meta.GeneratedReleaseNames.IncludeEpisodeTitle, selectedReleaseName(meta))
}

// cinemaZEpisodeTitleCandidates returns the effective prepared episode title
// first, followed by provider titles that canonical generation may have chosen.
func cinemaZEpisodeTitleCandidates(meta api.UploadSubject) []string {
	candidates := []string{meta.EpisodeTitle}
	if meta.ProviderMetadata.TVDB != nil {
		candidates = append(candidates, meta.ProviderMetadata.TVDB.EpisodeNameEnglish, meta.ProviderMetadata.TVDB.EpisodeName)
	}
	return candidates
}

// cutLeadingNameElement removes a separator-delimited element only from the
// start of name and returns the spelling found there. A group-separating hyphen
// remains attached to the returned remainder.
func cutLeadingNameElement(name, element string) (string, string, bool) {
	nameRunes := []rune(strings.TrimSpace(name))
	elementRunes := []rune(strings.TrimSpace(element))
	if len(nameRunes) == 0 || len(elementRunes) == 0 || len(elementRunes) > len(nameRunes) {
		return "", name, false
	}
	end := len(elementRunes)
	if !strings.EqualFold(string(nameRunes[:end]), string(elementRunes)) || (end < len(nameRunes) && !isNameSeparator(nameRunes[end])) {
		return "", name, false
	}
	rawRemainder := string(nameRunes[end:])
	remainder := strings.TrimLeftFunc(rawRemainder, isNameSeparator)
	if strings.HasPrefix(rawRemainder, "-") && remainder != "" && !strings.ContainsAny(remainder, " ._") {
		remainder = "-" + remainder
	}
	return string(nameRunes[:end]), remainder, true
}

// moveCinemaZHybridAfterResolution places an exact HYBRID token immediately
// after resolution. When the resolution is absent or not found, HYBRID remains
// normalized in its original position.
func moveCinemaZHybridAfterResolution(name, resolution string) string {
	fields := strings.Fields(name)
	for index, field := range fields {
		if strings.EqualFold(field, "Hybrid") {
			fields[index] = "HYBRID"
		}
	}
	resolution = strings.TrimSpace(resolution)
	if resolution == "" {
		return strings.Join(fields, " ")
	}
	foundHybrid := false
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.EqualFold(field, "Hybrid") {
			foundHybrid = true
			continue
		}
		result = append(result, field)
	}
	if !foundHybrid {
		return strings.Join(result, " ")
	}
	for index, field := range result {
		if strings.EqualFold(field, resolution) {
			result = append(result, "")
			copy(result[index+2:], result[index+1:])
			result[index+1] = "HYBRID"
			return strings.Join(result, " ")
		}
	}
	return strings.Join(fields, " ")
}

func nameHasElement(name, element string) bool {
	_, ok := suffixAfterNameElement(name, element)
	return ok
}

func removeCinemaZNameElements(name string, elements ...string) string {
	for _, element := range elements {
		if element = strings.TrimSpace(element); element != "" {
			name = replaceNameElements(name, element, "")
		}
	}
	return name
}

// cinemaZDVDVideo normalizes accepted MPEG-2 spellings to CinemaZ's MPEG2 token.
func cinemaZDVDVideo(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "MPEG-2") || strings.EqualFold(value, "MPEG 2") {
		return "MPEG2"
	}
	return value
}

// reorderCinemaZTechnicalTail removes known elements from a generated suffix,
// appends them in the requested order, and restores its exact trailing group tag.
// Callers pass suffixes so title and episode-title text cannot be rewritten.
func reorderCinemaZTechnicalTail(name, tag string, elements []string) string {
	tag = strings.TrimSpace(tag)
	base := strings.TrimSpace(name)
	if tag != "" && len(base) >= len(tag) && strings.EqualFold(base[len(base)-len(tag):], tag) {
		base = strings.TrimSpace(base[:len(base)-len(tag)])
	} else {
		tag = ""
	}
	ordered := make([]string, 0, len(elements))
	for _, element := range elements {
		element = strings.TrimSpace(element)
		if element == "" {
			continue
		}
		base = replaceNameElements(base, element, "")
		ordered = append(ordered, element)
	}
	base = strings.Join(strings.Fields(base), " ")
	base = strings.TrimSpace(strings.Join(nonEmptyStrings(append([]string{base}, ordered...)), " "))
	return base + tag
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
	if value := strings.TrimSpace(meta.DailyEpisodeDate); value != "" {
		return value
	}
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

// transliterateCinemaZTitle converts supported Cyrillic and Greek runes while
// leaving unsupported runes intact so the caller can reject a non-Latin result.
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
