// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveContainer(meta api.UploadSubject) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		return "5"
	case "DVD":
		return "15"
	}
	ext := strings.ToLower(strings.TrimSpace(meta.Container))
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(metautil.FirstNonEmptyTrimmed(meta.VideoPath, meta.SourcePath)), "."))
	}
	switch ext {
	case "mkv":
		return "6"
	case "mp4":
		return "8"
	default:
		return ""
	}
}

func resolveQuality(meta api.UploadSubject) string {
	if !strings.EqualFold(meta.Type, "DISC") {
		switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
		case "ENCODE":
			return "9"
		case "REMUX":
			return "39"
		case "WEBDL":
			return "23"
		case "WEBRIP":
			return "38"
		case "BDRIP":
			return "8"
		case "DVDRIP":
			return "3"
		default:
			return "0"
		}
	}
	if strings.EqualFold(meta.DiscType, "DVD") {
		if meta.SourceSize > 7_500_000_000 {
			return "46"
		}
		return "45"
	}
	if strings.EqualFold(meta.DiscType, "HDDVD") {
		return "15"
	}
	switch size := meta.SourceSize; {
	case size > 66<<30:
		return "43"
	case size > 50<<30:
		return "42"
	case size > 25<<30:
		return "41"
	default:
		return "40"
	}
}

func resolveResolution(meta api.UploadSubject) map[string]string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		heightStr := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(meta.Release.Resolution), "p"), "i")
		heightNum, err := strconv.Atoi(heightStr)
		if err == nil && heightNum > 0 {
			widthNum := int(math.Round((16.0 / 9.0) * float64(heightNum)))
			return map[string]string{
				"width":  strconv.Itoa(widthNum),
				"height": strconv.Itoa(heightNum),
			}
		}
	}

	if meta.MediaInfoJSONPath != "" {
		if payload, err := os.ReadFile(meta.MediaInfoJSONPath); err == nil {
			type mediaInfoDoc struct {
				Media struct {
					Track []map[string]any `json:"track"`
				} `json:"media"`
			}
			var doc mediaInfoDoc
			if err := json.Unmarshal(payload, &doc); err == nil {
				for _, track := range doc.Media.Track {
					trackType, _ := track["@type"].(string)
					if strings.ToLower(trackType) == "video" {
						widthVal := track["Width"]
						heightVal := track["Height"]

						widthStr := parseDimensionStr(widthVal)
						heightStr := parseDimensionStr(heightVal)

						if widthStr != "" && heightStr != "" {
							return map[string]string{
								"width":  widthStr,
								"height": heightStr,
							}
						}
					}
				}
			}
		}
	}

	height := parseResolutionHeight(meta.Release.Resolution)
	if height == 0 {
		height = parseResolutionHeight(meta.ReleaseName)
	}
	width := 0
	if height > 0 {
		width = int(float64(height) * (16.0 / 9.0))
	}
	return map[string]string{
		"width":  intString(width),
		"height": intString(height),
	}
}

func resolveVideoCodec(meta api.UploadSubject) string {
	codec := strings.ToUpper(strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.VideoEncode, meta.VideoCodec)))
	codecClean := codec
	if strings.Contains(codec, "264") {
		codecClean = "H264"
	} else if strings.Contains(codec, "265") {
		codecClean = "HEVC"
	}
	switch {
	case strings.Contains(strings.ToUpper(meta.HDR), "HDR") && (codecClean == "HEVC" || codecClean == "H265"):
		return "28"
	case strings.Contains(strings.ToUpper(meta.HDR), "HDR") && (codecClean == "AVC" || codecClean == "H264"):
		return "32"
	case strings.Contains(codec, "AV1"):
		return "29"
	case strings.Contains(codec, "HEVC"):
		return "27"
	case strings.Contains(codec, "H265"):
		return "18"
	case strings.Contains(codec, "AVC"):
		return "30"
	case strings.Contains(codec, "H264"):
		return "17"
	case strings.Contains(codec, "VC-1"):
		return "21"
	case strings.Contains(codec, "MPEG-2"):
		return "11"
	default:
		return "16"
	}
}

func resolveAudioCodec(meta api.UploadSubject) string {
	audio := strings.ToUpper(strings.TrimSpace(meta.Audio))
	switch {
	case strings.Contains(audio, "ATMOS"):
		return "43"
	case strings.Contains(audio, "DTS:X"):
		return "25"
	case strings.Contains(audio, "DTS-HD MA"):
		return "24"
	case strings.Contains(audio, "DTS-HD"):
		return "23"
	case strings.Contains(audio, "TRUEHD"):
		return "29"
	case strings.Contains(audio, "DD+"), strings.Contains(audio, "E-AC-3"):
		return "26"
	case strings.Contains(audio, "DD"), strings.Contains(audio, "AC3"):
		return "11"
	case strings.Contains(audio, "DTS"):
		return "12"
	case strings.Contains(audio, "FLAC"):
		return "13"
	case strings.Contains(audio, "LPCM"):
		return "21"
	case strings.Contains(audio, "PCM"):
		return "28"
	case strings.Contains(audio, "AAC"):
		return "10"
	case strings.Contains(audio, "OPUS"):
		return "27"
	case strings.Contains(audio, "MPEG"):
		return "17"
	default:
		return "20"
	}
}

func resolveAudio(meta api.UploadSubject) string {
	original := strings.ToLower(strings.TrimSpace(resolveOriginalLanguage(meta)))
	audioLangs := lowerStrings(meta.AudioLanguages)
	hasPTAudio := containsAny(audioLangs, []string{"portuguese", "português", "pt"})
	hasPTSubs := resolveSubtitle(meta) == "Embutida"
	isOriginalPT := containsAny([]string{original}, []string{"portuguese", "português", "pt"})
	switch {
	case hasPTAudio && isOriginalPT:
		return "4"
	case hasPTAudio && countNonPortuguese(audioLangs) > 0:
		return "2"
	case hasPTAudio:
		return "3"
	case hasPTSubs:
		return "1"
	default:
		return "7"
	}
}

func resolveSubtitle(meta api.UploadSubject) string {
	if containsAny(lowerStrings(meta.SubtitleLanguages), []string{"portuguese", "português", "pt", "brazilian portuguese"}) {
		return "Embutida"
	}
	return "S_legenda"
}

func resolveLanguage(meta api.UploadSubject) string {
	return mapLanguage(resolveOriginalLanguage(meta), map[string]string{
		"bg": "15",
		"da": "12",
		"de": "3",
		"en": "1",
		"es": "6",
		"fi": "14",
		"fr": "2",
		"hi": "23",
		"it": "4",
		"ja": "5",
		"ko": "20",
		"nl": "17",
		"no": "16",
		"pl": "19",
		"pt": "8",
		"ru": "7",
		"sv": "13",
		"th": "21",
		"tr": "25",
		"zh": "10",
	}, "11")
}

func resolveAnimeLanguage(meta api.UploadSubject) string {
	return mapLanguage(resolveOriginalLanguage(meta), map[string]string{
		"de": "3",
		"en": "4",
		"es": "1",
		"ja": "8",
		"ko": "11",
		"pt": "5",
		"ru": "2",
		"zh": "9",
	}, "6")
}

func resolveAnimeAudioLanguage(meta api.UploadSubject) string {
	if audio := resolveAudio(meta); audio == "2" || audio == "3" || audio == "4" {
		return "8"
	}
	return resolveLanguage(meta)
}

func resolveAnimeType(meta api.UploadSubject) string {
	if categoryOf(meta) == "TV" {
		return "118"
	}
	return "116"
}

func resolveUploadTitle(meta api.UploadSubject) string {
	base := resolveDisplayTitle(meta)
	if categoryOf(meta) == "TV" {
		return strings.TrimSpace(
			base + " - " + metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.SeasonStr)+strings.TrimSpace(meta.EpisodeStr), seasonEpisodeText(meta)),
		)
	}
	return base
}

func resolveDisplayTitle(meta api.UploadSubject) string {
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
	if tmdb := meta.ProviderMetadata.TMDB; tmdb != nil {
		main := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(ptBR.Title, tmdb.Title, meta.Release.Title))
		alt := strings.TrimSpace(tmdb.OriginalTitle)
		if categoryOf(meta) == "TV" {
			alt = strings.TrimSpace(metautil.FirstNonEmptyTrimmed(tmdb.Title, meta.Release.Title))
		}
		if main != "" && alt != "" && !strings.EqualFold(main, alt) {
			return main + " (" + alt + ")"
		}
		if main != "" {
			return main
		}
	}
	return strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName, pathutil.Base(meta.SourcePath)))
}

func resolvePoster(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		if meta.ProviderMetadata.TMDB.Localized != nil {
			if localized, ok := meta.ProviderMetadata.TMDB.Localized["pt-BR"]; ok && strings.TrimSpace(localized.Poster) != "" {
				return strings.TrimSpace(localized.Poster)
			}
		}
		if strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster) != "" {
			return strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster)
		}
	}
	switch {
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover) != "":
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.Poster) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.Poster)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Poster) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Poster)
	default:
		return ""
	}
}

func resolveOverview(meta api.UploadSubject, answers map[string]string) string {
	if strings.TrimSpace(answers["overview"]) != "" {
		return strings.TrimSpace(answers["overview"])
	}
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
	if shouldUseScopedTVOverview(meta) && ptBR.EpisodeOverview != "" {
		return strings.TrimSpace(ptBR.EpisodeOverview)
	}
	if ptBR.Overview != "" {
		return strings.TrimSpace(ptBR.Overview)
	}
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview)
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.Plot) != "":
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Plot)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.Overview) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.Overview)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Summary) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Summary)
	default:
		return strings.TrimSpace(meta.EpisodeOverview)
	}
}

// shouldUseScopedTVOverview reports whether ASC should prefer season or
// episode localized overview over title-level synopsis text.
func shouldUseScopedTVOverview(meta api.UploadSubject) bool {
	if meta.SeasonInt <= 0 {
		return false
	}
	if categoryOf(meta) != "TV" {
		return false
	}
	if meta.TVPack {
		return true
	}
	return meta.EpisodeInt > 0
}

func resolveGenres(meta api.UploadSubject, answers map[string]string) string {
	if strings.TrimSpace(answers["genre"]) != "" {
		return strings.TrimSpace(answers["genre"])
	}
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)

	// 1. Use localized if available
	if ptBR.Genres != "" {
		genres := strings.Split(strings.TrimSpace(ptBR.Genres), ",")
		out := make([]string, 0, len(genres))
		for _, genre := range genres {
			g := strings.TrimSpace(genre)
			capitalized := metautil.CapitalizeGenre(g)
			if capitalized != "" {
				out = append(out, capitalized)
			}
		}
		return strings.Join(out, ", ")
	}

	// 2. Use metautil.TranslateGenreToPortugueseStrict to translate
	var genreText string
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres) != "":
		genreText = strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres)
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres) != "":
		genreText = strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.Genres) != "":
		genreText = strings.TrimSpace(meta.ProviderMetadata.TVDB.Genres)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Genres) != "":
		genreText = strings.TrimSpace(meta.ProviderMetadata.TVmaze.Genres)
	default:
		genreText = strings.TrimSpace(meta.Release.Genre)
	}

	if genreText == "" {
		return ""
	}

	genres := strings.Split(genreText, ",")
	out := make([]string, 0, len(genres))
	for _, genre := range genres {
		g := strings.TrimSpace(genre)
		if g == "" {
			continue
		}
		translated := metautil.TranslateGenreToPortugueseStrict(g)
		if translated == "" {
			translated = g
		}
		capitalized := metautil.CapitalizeGenre(translated)
		if capitalized != "" {
			out = append(out, capitalized)
		}
	}
	return strings.Join(out, ", ")
}

func resolveTrailer(meta api.UploadSubject) string {
	value := ""
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
	if ptBR.TrailerURL != "" {
		value = ptBR.TrailerURL
	}
	if value == "" && meta.ProviderMetadata.TMDB != nil {
		value = strings.TrimSpace(meta.ProviderMetadata.TMDB.YouTube)
	}
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	return "https://www.youtube.com/watch?v=" + value
}

func resolveIMDbIDText(meta api.UploadSubject) string {
	if meta.Identity.IMDBID > 0 {
		return fmt.Sprintf("tt%07d", meta.Identity.IMDBID)
	}
	return ""
}

func resolveOriginalLanguage(meta api.UploadSubject) string {
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage)
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.OriginalLanguage)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.OriginalLanguage)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Language) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Language)
	default:
		return ""
	}
}

func resolveRuntime(meta api.UploadSubject) string {
	minutes := 0
	switch {
	case meta.ProviderMetadata.TMDB != nil:
		minutes = meta.ProviderMetadata.TMDB.Runtime
	case meta.ProviderMetadata.IMDB != nil:
		minutes = meta.ProviderMetadata.IMDB.RuntimeMinutes
	case meta.ProviderMetadata.TVmaze != nil:
		minutes = meta.ProviderMetadata.TVmaze.Runtime
	}
	if minutes <= 0 {
		return ""
	}
	hours := minutes / 60
	remain := minutes % 60
	if hours == 0 {
		return fmt.Sprintf("%02d minutos", remain)
	}
	return fmt.Sprintf("%d hora%s e %02d minutos", hours, pluralSuffix(hours), remain)
}

func resolveCountries(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && len(meta.ProviderMetadata.TMDB.ProductionCountries) > 0 {
		names := make([]string, 0, len(meta.ProviderMetadata.TMDB.ProductionCountries))
		for _, country := range meta.ProviderMetadata.TMDB.ProductionCountries {
			if strings.TrimSpace(country.Name) != "" {
				names = append(names, strings.TrimSpace(country.Name))
			}
		}
		return strings.Join(names, ", ")
	}
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.CountryList)
	}
	return ""
}

func resolveCast(meta api.UploadSubject) []string {
	switch {
	case meta.ProviderMetadata.TMDB != nil && len(meta.ProviderMetadata.TMDB.Cast) > 0:
		return append([]string{}, meta.ProviderMetadata.TMDB.Cast...)
	case meta.ProviderMetadata.IMDB != nil && len(meta.ProviderMetadata.IMDB.Stars) > 0:
		names := make([]string, 0, len(meta.ProviderMetadata.IMDB.Stars))
		for _, person := range meta.ProviderMetadata.IMDB.Stars {
			if strings.TrimSpace(person.Name) != "" {
				names = append(names, strings.TrimSpace(person.Name))
			}
		}
		return names
	default:
		return nil
	}
}

func resolveReleaseDate(meta api.UploadSubject) string {
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.ReleaseDate) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.ReleaseDate)
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.FirstAirDate) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.FirstAirDate)
	case meta.ProviderMetadata.TVDB != nil && strings.TrimSpace(meta.ProviderMetadata.TVDB.FirstAired) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVDB.FirstAired)
	case meta.ProviderMetadata.TVmaze != nil && strings.TrimSpace(meta.ProviderMetadata.TVmaze.Premiered) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TVmaze.Premiered)
	default:
		return ""
	}
}

func resolveYear(meta api.UploadSubject) int {
	switch {
	case meta.Release.Year > 0:
		return meta.Release.Year
	case meta.ProviderMetadata.TMDB != nil && meta.ProviderMetadata.TMDB.Year > 0:
		return meta.ProviderMetadata.TMDB.Year
	case meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Year > 0:
		return meta.ProviderMetadata.IMDB.Year
	case meta.ProviderMetadata.TVDB != nil && meta.ProviderMetadata.TVDB.Year > 0:
		return meta.ProviderMetadata.TVDB.Year
	default:
		return 0
	}
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return strings.ToUpper(string(category))
}

func seasonEpisodeText(meta api.UploadSubject) string {
	if meta.EpisodeInt > 0 {
		return fmt.Sprintf("S%02dE%02d", meta.SeasonInt, meta.EpisodeInt)
	}
	if meta.SeasonInt > 0 {
		return fmt.Sprintf("S%02d", meta.SeasonInt)
	}
	return ""
}

func boolFlag(ok bool) string {
	if ok {
		return "1"
	}
	return "2"
}

func parseResolutionHeight(value string) int {
	re := regexp.MustCompile(`(?i)(\d{3,4})(?:p|i)`)
	matches := re.FindStringSubmatch(value)
	if len(matches) != 2 {
		return 0
	}
	height, _ := strconv.Atoi(matches[1])
	return height
}

func intString(value int) string {
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func mapLanguage(value string, mappings map[string]string, fallback string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	if mapped, ok := mappings[key]; ok {
		return mapped
	}
	return fallback
}

func lowerStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, strings.ToLower(strings.TrimSpace(value)))
	}
	return out
}

func countNonPortuguese(values []string) int {
	count := 0
	for _, value := range values {
		if !containsAny([]string{value}, []string{"portuguese", "português", "pt"}) {
			count++
		}
	}
	return count
}

func containsAny(values []string, targets []string) bool {
	for _, value := range values {
		for _, target := range targets {
			if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
				return true
			}
		}
	}
	return false
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func pluralSuffix(value int) string {
	if value == 1 {
		return ""
	}
	return "s"
}

func readTextFile(path string) (string, error) {
	payload, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("trackers: ASC read text file: %w", err)
	}
	return strings.ReplaceAll(string(payload), "\r", ""), nil
}

func readTextFileNoErr(path string) string {
	value, _ := readTextFile(path)
	return value
}

func parseDimensionStr(val any) string {
	return metautil.ParseDimensionStr(val)
}
