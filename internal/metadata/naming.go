// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	namingTVPathHintPattern     = regexp.MustCompile(`(?i)[\\/](tv|tvshows?|series)[\\/]`)
	namingTVNameHintPattern     = regexp.MustCompile(`(?i)\bS\d{1,2}(?:E\d{1,3})?\b|\b\d{1,2}x\d{2,3}\b|\b(?:season|series)\s*\d+\b|\b(19\d{2}|20\d{2})[.-]\d{2}[.-]\d{2}\b`)
	namingSubsPleaseHintPattern = regexp.MustCompile(`(?i)subsplease`)
	namingAnimeEpisodeHint      = regexp.MustCompile(`(?i)-\s*\d{1,3}\s*\(1080p\)`)
	namingWebDLFilenamePattern  = regexp.MustCompile(`(?i)(^|[ ._-])web([ ._-]|$)|web-?dl`)
)

func BuildReleaseName(req api.ReleaseNameRequest, logger api.Logger) api.ReleaseNameResult {
	if logger == nil {
		logger = api.NopLogger{}
	}

	category := normalizeNamingCategory(req.Category)
	typeValue := strings.ToUpper(strings.TrimSpace(req.Type))
	typeValue = normalizeReleaseTypeForCategory(category, typeValue, strings.TrimSpace(req.Source), "")
	matchType := normalizeReleaseType(typeValue)
	logger.Tracef("metadata: release name input category=%q type=%q normalized_type=%q source=%q season=%q episode=%q date=%q manual_date=%t", category, typeValue, matchType, strings.TrimSpace(req.Source), strings.TrimSpace(req.Season), strings.TrimSpace(req.Episode), strings.TrimSpace(req.DailyDate), req.ManualDate)
	title := strings.TrimSpace(req.Title)
	altTitle := strings.TrimSpace(req.AltTitle)
	year := req.Year
	if req.ManualYear > 0 {
		year = req.ManualYear
	}

	resolution := strings.TrimSpace(req.Resolution)
	if strings.EqualFold(resolution, "OTHER") {
		resolution = ""
	}

	audio := strings.TrimSpace(req.Audio)
	service := strings.TrimSpace(req.Service)
	season := strings.TrimSpace(req.Season)
	episode := strings.TrimSpace(req.Episode)
	part := strings.TrimSpace(req.Part)
	repack := strings.TrimSpace(req.Repack)
	threeD := strings.TrimSpace(req.ThreeD)
	tag := strings.TrimSpace(req.Tag)
	source := strings.TrimSpace(req.Source)
	uhd := strings.TrimSpace(req.UHD)
	hdr := strings.TrimSpace(req.HDR)
	episodeTitle := strings.TrimSpace(req.EpisodeTitle)
	dailyDate := strings.TrimSpace(req.DailyDate)
	videoCodec := strings.TrimSpace(req.VideoCodec)
	videoEncode := strings.TrimSpace(req.VideoEncode)
	videoName := videoEncode
	if videoName == "" {
		videoName = videoCodec
	}
	region := strings.TrimSpace(req.Region)
	dvdSize := strings.TrimSpace(req.DVDSize)
	edition := strings.TrimSpace(req.Edition)

	edition = removeHybrid(edition)
	hybrid := ""
	if req.WebDV {
		hybrid = "Hybrid"
	}

	if req.ManualDate {
		season = ""
		episode = ""
		if dailyDate != "" {
			episodeTitle = dailyDate
		}
	}
	if req.NoSeason {
		season = ""
		episode = ""
	}
	if req.NoYear {
		year = 0
	}
	if req.NoAKA {
		altTitle = ""
	}

	if category == "TV" {
		searchYear := strings.TrimSpace(req.SearchYear)
		if parsedYear, err := strconv.Atoi(searchYear); err == nil && parsedYear > 0 {
			year = parsedYear
		} else {
			year = 0
		}
	}

	logger.Tracef("metadata: release name build category=%q type=%q source=%q disc=%q title=%q season=%q episode=%q", category, matchType, source, req.DiscType, title, season, episode)

	name := ""
	missing := make([]string, 0)
	seasonEpisode := strings.TrimSpace(season + episode)
	yearValue := ""
	if year > 0 {
		yearValue = strconv.Itoa(year)
	}

	switch category {
	case "MOVIE":
		switch {
		case matchType == "DISC":
			switch strings.ToUpper(strings.TrimSpace(req.DiscType)) {
			case "BDMV":
				name = joinParts(title, altTitle, yearValue, threeD, edition, hybrid, repack, resolution, region, uhd, source, hdr, videoCodec, audio)
				missing = []string{"edition", "region", "distributor"}
			case "DVD":
				name = joinParts(title, altTitle, yearValue, repack, edition, region, source, dvdSize, audio)
				missing = []string{"edition", "distributor"}
			case "HDDVD":
				name = joinParts(title, altTitle, yearValue, edition, repack, resolution, source, videoCodec, audio)
				missing = []string{"edition", "region", "distributor"}
			}
		case matchType == "REMUX" && sourceIn(source, "BluRay", "HDDVD"):
			name = joinParts(title, altTitle, yearValue, threeD, edition, hybrid, repack, resolution, uhd, source, "REMUX", hdr, videoCodec, audio)
			missing = []string{"edition", "description"}
		case matchType == "REMUX" && sourceIn(source, "PAL DVD", "NTSC DVD", "DVD"):
			name = joinParts(title, altTitle, yearValue, edition, repack, source, "REMUX", audio)
			missing = []string{"edition", "description"}
		case matchType == "ENCODE":
			name = joinParts(title, altTitle, yearValue, edition, hybrid, repack, resolution, uhd, source, audio, hdr, videoName)
			missing = []string{"edition", "description"}
		case matchType == "WEBDL":
			name = joinParts(title, altTitle, yearValue, edition, hybrid, repack, resolution, uhd, service, "WEB-DL", audio, hdr, videoName)
			missing = []string{"edition", "service"}
		case matchType == "WEBRIP":
			name = joinParts(title, altTitle, yearValue, edition, hybrid, repack, resolution, uhd, service, "WEBRip", audio, hdr, videoName)
			missing = []string{"edition", "service"}
		case matchType == "HDTV":
			name = joinParts(title, altTitle, yearValue, edition, repack, resolution, source, audio, videoName)
		case matchType == "DVDRIP":
			name = joinParts(title, altTitle, yearValue, source, videoName, "DVDRip", audio)
		}
	case "TV":
		switch {
		case matchType == "DISC":
			switch strings.ToUpper(strings.TrimSpace(req.DiscType)) {
			case "BDMV":
				name = joinParts(title, yearValue, altTitle, seasonEpisode, threeD, edition, hybrid, repack, resolution, region, uhd, source, hdr, videoCodec, audio)
				missing = []string{"edition", "region", "distributor"}
			case "DVD":
				name = joinParts(title, yearValue, altTitle, seasonEpisode+threeD, repack, edition, region, source, dvdSize, audio)
				missing = []string{"edition", "distributor"}
			case "HDDVD":
				name = joinParts(title, altTitle, yearValue, edition, repack, resolution, source, videoCodec, audio)
				missing = []string{"edition", "region", "distributor"}
			}
		case matchType == "REMUX" && sourceIn(source, "BluRay", "HDDVD"):
			name = joinParts(title, yearValue, altTitle, seasonEpisode, episodeTitle, part, threeD, edition, hybrid, repack, resolution, uhd, source, "REMUX", hdr, videoCodec, audio)
			missing = []string{"edition", "description"}
		case matchType == "REMUX" && sourceIn(source, "PAL DVD", "NTSC DVD", "DVD"):
			name = joinParts(title, yearValue, altTitle, seasonEpisode, episodeTitle, part, edition, repack, source, "REMUX", audio)
			missing = []string{"edition", "description"}
		case matchType == "ENCODE":
			name = joinParts(title, yearValue, altTitle, seasonEpisode, episodeTitle, part, edition, hybrid, repack, resolution, uhd, source, audio, hdr, videoName)
			missing = []string{"edition", "description"}
		case matchType == "WEBDL":
			name = joinParts(title, yearValue, altTitle, seasonEpisode, episodeTitle, part, edition, hybrid, repack, resolution, uhd, service, "WEB-DL", audio, hdr, videoName)
			missing = []string{"edition", "service"}
		case matchType == "WEBRIP":
			name = joinParts(title, yearValue, altTitle, seasonEpisode, episodeTitle, part, edition, hybrid, repack, resolution, uhd, service, "WEBRip", audio, hdr, videoName)
			missing = []string{"edition", "service"}
		case matchType == "HDTV":
			name = joinParts(title, yearValue, altTitle, seasonEpisode, episodeTitle, part, edition, repack, resolution, source, audio, videoName)
		case matchType == "DVDRIP":
			name = joinParts(title, yearValue, altTitle, season, source, "DVDRip", audio, videoName)
		}
	}

	nameNoTag := strings.TrimSpace(name)
	if nameNoTag == "" {
		logger.Tracef("metadata: release name build skipped (empty base) category=%q type=%q source=%q season=%q episode=%q", category, matchType, source, season, episode)
		return api.ReleaseNameResult{MissingFields: missing}
	}
	nameWithTag := strings.TrimSpace(nameNoTag + tag)
	cleanName := cleanFilename(nameWithTag)

	logger.Tracef("metadata: release name built name=%q clean=%q", nameWithTag, cleanName)
	return api.ReleaseNameResult{
		NameNoTag:     nameNoTag,
		Name:          nameWithTag,
		CleanName:     cleanName,
		MissingFields: missing,
	}
}

func releaseNameRequestFromMeta(meta api.PreparedMetadata, logger api.Logger) api.ReleaseNameRequest {
	if logger == nil {
		logger = api.NopLogger{}
	}

	category := normalizeNamingCategory(meta.ExternalIDs.Category)
	if category == "" {
		category = normalizeNamingCategory(meta.MediaInfoCategory)
	}
	if category == "" {
		category = normalizeNamingCategory(meta.Release.Category)
	}
	if category == "" {
		category = normalizeCategoryFromType(meta.Type)
	}
	if category == "" {
		category = inferCategoryFromMetadata(meta)
	}

	baseType := strings.TrimSpace(meta.Type)
	typeValue := baseType
	if typeValue == "" || isCategoryType(typeValue) {
		typeValue = strings.TrimSpace(meta.Release.Type)
	}
	if typeValue == "" || isCategoryType(typeValue) {
		if meta.DiscType != "" {
			typeValue = "DISC"
		}
	}

	source := strings.TrimSpace(meta.Source)
	if source == "" {
		source = strings.TrimSpace(meta.Release.Source)
	}
	if typeValue == "" || isCategoryType(typeValue) {
		if inferred := inferReleaseTypeFromSource(source); inferred != "" {
			typeValue = inferred
		}
	}
	if typeValue == "" || isCategoryType(typeValue) {
		if inferred := inferReleaseTypeFromName(meta.SourcePath); inferred != "" {
			typeValue = inferred
		}
	}
	if typeValue == "" || isCategoryType(typeValue) {
		if meta.VideoEncode != "" {
			typeValue = "ENCODE"
		}
	}
	if typeValue == "" || isCategoryType(typeValue) {
		if strings.TrimSpace(meta.VideoCodec) != "" || strings.TrimSpace(meta.Release.Resolution) != "" || strings.TrimSpace(meta.Release.Ext) != "" {
			typeValue = "ENCODE"
		}
	}
	if source == "" || !isKnownReleaseSource(source) {
		if inferred := inferReleaseSourceFromName(meta.SourcePath, typeValue); inferred != "" {
			source = inferred
		}
	}

	title, altTitle, year := resolveReleaseNameTitle(category, meta)
	searchYear := ""
	if strings.EqualFold(category, "TV") && year > 0 {
		searchYear = strconv.Itoa(year)
	}

	typeValue = normalizeReleaseTypeForCategory(category, typeValue, source, meta.SourcePath)

	logger.Tracef("metadata: release name request resolved category=%q type=%q base_type=%q source=%q season=%q episode=%q date=%q tv_pack=%t", category, typeValue, baseType, source, strings.TrimSpace(meta.SeasonStr), strings.TrimSpace(meta.EpisodeStr), strings.TrimSpace(meta.DailyEpisodeDate), meta.TVPack)

	dailyDate := strings.TrimSpace(meta.DailyEpisodeDate)
	manualDate := strings.EqualFold(category, "TV") && dailyDate != "" && !meta.TVPack

	return api.ReleaseNameRequest{
		Category:      category,
		Type:          typeValue,
		Title:         title,
		AltTitle:      altTitle,
		Year:          year,
		Resolution:    meta.Release.Resolution,
		Audio:         meta.Audio,
		Service:       meta.Service,
		Season:        strings.TrimSpace(meta.SeasonStr),
		Episode:       strings.TrimSpace(meta.EpisodeStr),
		Part:          "",
		Repack:        meta.Repack,
		ThreeD:        meta.Is3D,
		Tag:           meta.Tag,
		Source:        source,
		UHD:           meta.UHD,
		HDR:           meta.HDR,
		WebDV:         meta.WebDV,
		EpisodeTitle:  strings.TrimSpace(meta.EpisodeTitle),
		VideoCodec:    meta.VideoCodec,
		VideoEncode:   meta.VideoEncode,
		DiscType:      meta.DiscType,
		Region:        meta.Region,
		DVDSize:       meta.Release.Size,
		Edition:       meta.Edition,
		SearchYear:    searchYear,
		DailyDate:     dailyDate,
		ManualDate:    manualDate,
		TMDBDateMatch: meta.TMDBDateMatch,
	}
}

func resolveReleaseNameTitle(category string, meta api.PreparedMetadata) (string, string, int) {
	title := strings.TrimSpace(meta.Release.Title)
	altTitle := strings.TrimSpace(meta.Release.Alt)
	year := meta.Release.Year
	isTV := strings.EqualFold(strings.TrimSpace(category), "TV")

	if isTV && meta.ExternalMetadata.TVDB != nil {
		tvdb := meta.ExternalMetadata.TVDB
		if strings.TrimSpace(tvdb.NameEnglish) != "" {
			title = strings.TrimSpace(tvdb.NameEnglish)
		} else if strings.TrimSpace(tvdb.Name) != "" {
			title = strings.TrimSpace(tvdb.Name)
		}
		if tvdb.Year > 0 && tvdb.YearFromAlias {
			year = tvdb.Year
		} else {
			year = 0
		}
		return title, altTitle, year
	}

	switch {
	case meta.ExternalMetadata.TMDB != nil:
		tmdb := meta.ExternalMetadata.TMDB
		if strings.TrimSpace(tmdb.Title) != "" {
			title = strings.TrimSpace(tmdb.Title)
		}
		if altTitle == "" && strings.TrimSpace(tmdb.OriginalTitle) != "" && !strings.EqualFold(tmdb.Title, tmdb.OriginalTitle) {
			altTitle = "AKA " + strings.TrimSpace(tmdb.OriginalTitle)
		}
		if year == 0 && tmdb.Year > 0 {
			year = tmdb.Year
		}
	case meta.ExternalMetadata.IMDB != nil:
		imdb := meta.ExternalMetadata.IMDB
		if strings.TrimSpace(imdb.Title) != "" {
			title = strings.TrimSpace(imdb.Title)
		}
		if year == 0 && imdb.Year > 0 {
			year = imdb.Year
		}
	case meta.ExternalMetadata.TVDB != nil:
		if strings.TrimSpace(meta.ExternalMetadata.TVDB.Name) != "" {
			title = strings.TrimSpace(meta.ExternalMetadata.TVDB.Name)
		}
	case meta.ExternalMetadata.TVmaze != nil:
		if strings.TrimSpace(meta.ExternalMetadata.TVmaze.Name) != "" {
			title = strings.TrimSpace(meta.ExternalMetadata.TVmaze.Name)
		}
	}
	return title, altTitle, year
}

func inferReleaseTypeFromName(path string) string {
	base := strings.ToUpper(pathutil.Base(path))
	if webType := inferWebReleaseTypeFromFilename(pathutil.Base(path)); webType != "" {
		return webType
	}
	compact := strings.NewReplacer(".", "", "-", "", "_", "", " ", "").Replace(base)
	switch {
	case strings.Contains(compact, "REMUX"):
		return "REMUX"
	case strings.Contains(compact, "WEBDL"):
		return "WEBDL"
	case strings.Contains(compact, "WEBRIP"):
		return "WEBRIP"
	case strings.Contains(compact, "HDTV"):
		return "HDTV"
	case strings.Contains(compact, "DVDRIP"):
		return "DVDRIP"
	}
	return ""
}

func inferReleaseSourceFromName(path string, typeValue string) string {
	base := strings.ToUpper(pathutil.Base(path))
	compact := strings.NewReplacer(".", "", "-", "", "_", "", " ", "").Replace(base)
	switch {
	case strings.Contains(compact, "HDDVD"):
		return "HDDVD"
	case strings.Contains(compact, "BLURAY") || strings.Contains(compact, "BLU") && strings.Contains(compact, "RAY"):
		if strings.EqualFold(typeValue, "DISC") {
			return "Blu-ray"
		}
		return "BluRay"
	case strings.Contains(compact, "WEBDL") || strings.Contains(compact, "WEBRIP"):
		return "Web"
	case strings.Contains(compact, "HDTV"):
		return "HDTV"
	case strings.Contains(compact, "UHDTV"):
		return "UHDTV"
	case strings.Contains(compact, "PALDVD"):
		return "PAL DVD"
	case strings.Contains(compact, "NTSCDVD"):
		return "NTSC DVD"
	case strings.Contains(compact, "DVD"):
		return "DVD"
	}
	return ""
}

func isKnownReleaseSource(source string) bool {
	upper := strings.ToUpper(strings.TrimSpace(source))
	switch upper {
	case "BLURAY", "BLU-RAY", "BLU RAY", "BLU-RAY 3D", "BD", "BDMV", "DVD", "PAL DVD", "NTSC DVD", "HDDVD", "HD DVD", "WEB", "HDTV", "UHDTV":
		return true
	}
	return false
}

func joinParts(parts ...string) string {
	combined := strings.Join(parts, " ")
	return strings.Join(strings.Fields(combined), " ")
}

func sourceIn(source string, candidates ...string) bool {
	for _, value := range candidates {
		if strings.EqualFold(strings.TrimSpace(source), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func cleanFilename(name string) string {
	invalid := "<>:\"/\\|?*"
	result := name
	for _, char := range invalid {
		result = strings.ReplaceAll(result, string(char), "-")
	}
	return result
}

func removeHybrid(edition string) string {
	if edition == "" {
		return ""
	}
	fields := strings.Fields(edition)
	cleaned := fields[:0]
	for _, value := range fields {
		if strings.EqualFold(value, "Hybrid") {
			continue
		}
		cleaned = append(cleaned, value)
	}
	return strings.TrimSpace(strings.Join(cleaned, " "))
}

func normalizeNamingCategory(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	if upper == "MOVIE" || upper == "TV" {
		return upper
	}
	return ""
}

func normalizeCategoryFromType(value string) string {
	return normalizeNamingCategory(value)
}

func inferCategoryFromMetadata(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TVDB != nil || meta.ExternalMetadata.TVmaze != nil {
		return "TV"
	}
	if meta.SeasonInt > 0 || meta.EpisodeInt > 0 || meta.Release.Season > 0 || meta.Release.Episode > 0 {
		return "TV"
	}
	if category := normalizeNamingCategory(meta.Release.Category); category != "" {
		return category
	}
	if strings.TrimSpace(meta.DailyEpisodeDate) != "" {
		return "TV"
	}
	releaseType := strings.ToUpper(strings.TrimSpace(meta.Release.Type))
	if strings.Contains(releaseType, "TV") || strings.Contains(releaseType, "SERIES") || strings.Contains(releaseType, "EPISODE") {
		return "TV"
	}
	sourcePath := filepath.ToSlash(strings.TrimSpace(meta.SourcePath))
	pathHint := pathutil.Base(meta.SourcePath)
	if namingTVPathHintPattern.MatchString(sourcePath) || namingTVNameHintPattern.MatchString(pathHint) {
		return "TV"
	}
	if namingSubsPleaseHintPattern.MatchString(sourcePath) && namingAnimeEpisodeHint.MatchString(pathHint) {
		return "TV"
	}
	return "MOVIE"
}

func isCategoryType(value string) bool {
	upper := strings.ToUpper(strings.TrimSpace(value))
	return upper == "MOVIE" || upper == "TV"
}

func normalizeReleaseType(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	if upper == "" {
		return ""
	}
	upper = strings.ReplaceAll(upper, "-", "")
	upper = strings.ReplaceAll(upper, " ", "")
	switch upper {
	case "WEBDL", "WEB-DL", "WEB_DL":
		return "WEBDL"
	case "WEBRIP", "WEB-RIP", "WEB_RIP":
		return "WEBRIP"
	}
	return upper
}

func normalizeReleaseTypeForCategory(category string, typeValue string, source string, sourcePath string) string {
	normalizedType := normalizeReleaseType(typeValue)
	if webType := webReleaseTypeFromSignals(normalizedType, source, sourcePath); webType != "" {
		return webType
	}
	if !strings.EqualFold(strings.TrimSpace(category), "TV") {
		return normalizedType
	}

	switch normalizedType {
	case "EP", "EPS", "EPISODE", "SERIES", "SEASON", "SEASONPACK", "TV", "TVSHOW":
		if inferred := inferReleaseTypeFromSource(source); inferred != "" {
			return inferred
		}
		if inferred := inferReleaseTypeFromName(sourcePath); inferred != "" {
			return inferred
		}
		return "ENCODE"
	}

	if normalizedType == "" {
		if inferred := inferReleaseTypeFromSource(source); inferred != "" {
			return inferred
		}
		if inferred := inferReleaseTypeFromName(sourcePath); inferred != "" {
			return inferred
		}
	}

	return normalizedType
}

func webReleaseTypeFromSignals(typeValue string, source string, sourcePath string) string {
	normalizedType := normalizeReleaseType(typeValue)
	switch normalizedType {
	case "WEBDL", "WEBRIP":
		return normalizedType
	case "", "ENCODE", "EP", "EPS", "EPISODE", "SERIES", "SEASON", "SEASONPACK", "TV", "TVSHOW":
	default:
		return ""
	}

	if inferred := inferReleaseTypeFromSource(source); inferred == "WEBDL" || inferred == "WEBRIP" {
		return inferred
	}
	if inferred := inferReleaseTypeFromName(sourcePath); inferred == "WEBDL" || inferred == "WEBRIP" {
		return inferred
	}
	if isWebSourceValue(source) {
		return "WEBDL"
	}
	return ""
}

func inferWebReleaseTypeFromFilename(filename string) string {
	lower := strings.ToLower(strings.TrimSpace(filename))
	if lower == "" {
		return ""
	}
	if namingWebDLFilenamePattern.MatchString(lower) {
		return "WEBDL"
	}
	if strings.Contains(lower, "webrip") {
		return "WEBRIP"
	}
	return ""
}

func inferReleaseTypeFromSource(source string) string {
	upper := strings.ToUpper(strings.TrimSpace(source))
	upper = strings.ReplaceAll(upper, "-", "")
	upper = strings.ReplaceAll(upper, " ", "")
	upper = strings.ReplaceAll(upper, "_", "")
	switch {
	case strings.Contains(upper, "WEBDL"):
		return "WEBDL"
	case strings.Contains(upper, "WEBRIP"):
		return "WEBRIP"
	case strings.Contains(upper, "HDTV"):
		return "HDTV"
	}
	return ""
}

func isWebSourceValue(source string) bool {
	upper := strings.ToUpper(strings.TrimSpace(source))
	upper = strings.ReplaceAll(upper, "-", "")
	upper = strings.ReplaceAll(upper, " ", "")
	upper = strings.ReplaceAll(upper, "_", "")
	return upper == "WEB" || upper == "WEBDL" || upper == "WEBRIP"
}
