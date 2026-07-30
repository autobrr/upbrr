// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveType(meta api.UploadSubject) string {
	if meta.Anime {
		return "5"
	}
	if strings.EqualFold(categoryOf(meta), "TV") {
		return "1"
	}
	return "0"
}

func resolveContainer(meta api.UploadSubject) string {
	container := strings.ToLower(strings.TrimSpace(meta.Container))
	switch container {
	case "avi", "m2ts", "m4v", "mkv", "mp4", "ts", "vob", "wmv":
		return strings.ToUpper(container)
	default:
		return "Outro"
	}
}

func resolveSubtitle(meta api.UploadSubject) (string, []string) {
	hasPT := "Nao"
	ids := make([]string, 0)
	seen := make(map[string]struct{})

	for _, lang := range meta.SubtitleLanguages {
		cleanLang := strings.ToLower(strings.TrimSpace(lang))

		targetKey, ok := sourceAliasMap[cleanLang]
		if !ok {
			targetKey = cleanLang
		}

		if id, exists := targetSiteIDs[targetKey]; exists {
			if _, alreadySeen := seen[id]; !alreadySeen {
				seen[id] = struct{}{}
				ids = append(ids, id)

				if id == "49" {
					hasPT = "Sim"
				}
			}
		}
	}

	if len(ids) == 0 {
		return "Nao", []string{"44"}
	}

	return hasPT, ids
}

func resolveResolution(meta api.UploadSubject) (string, string) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		heightStr := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(meta.Release.Resolution), "p"), "i")
		heightNum, err := strconv.Atoi(heightStr)
		if err == nil && heightNum > 0 {
			widthNum := int(math.Round((16.0 / 9.0) * float64(heightNum)))
			return strconv.Itoa(widthNum), strconv.Itoa(heightNum)
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
							return widthStr, heightStr
						}
					}
				}
			}
		}
	}

	height := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(meta.Release.Resolution), "p"), "i")
	switch height {
	case "2160":
		return "3840", "2160"
	case "1080":
		return "1920", "1080"
	case "720":
		return "1280", "720"
	case "576":
		return "1024", "576"
	case "480":
		return "854", "480"
	default:
		return "", ""
	}
}

func resolveVideoCodec(meta api.UploadSubject) string {
	videoEncode := strings.ToLower(strings.TrimSpace(meta.VideoEncode))
	codecFinal := strings.TrimSpace(meta.VideoCodec)
	isHDR := meta.HDR != ""

	encodeMap := []struct{ Key, Value string }{
		{"x265", "x265"},
		{"h.265", "H.265"},
		{"x264", "x264"},
		{"h.264", "H.264"},
		{"vp9", "VP9"},
		{"xvid", "XviD"},
	}

	for _, item := range encodeMap {
		if strings.Contains(videoEncode, item.Key) {
			if (item.Value == "x265" || item.Value == "H.265") && isHDR {
				return item.Value + " HDR"
			}
			return item.Value
		}
	}

	codecLower := strings.ToLower(codecFinal)
	codecMap := []struct{ Key, Value string }{
		{"hevc", "x265"},
		{"265", "x265"},
		{"avc", "x264"},
		{"264", "x264"},
		{"mpeg-2", "MPEG-2"},
		{"vc-1", "VC-1"},
	}

	for _, item := range codecMap {
		if strings.Contains(codecLower, item.Key) {
			if item.Value == "x265" && isHDR {
				return "x265 HDR"
			}
			return item.Value
		}
	}

	if codecFinal != "" {
		return codecFinal
	}
	return "Outro"
}

func resolveAudioCodec(meta api.UploadSubject) string {
	priorityOrder := []string{
		"DTS-X", "E-AC-3 JOC", "TrueHD", "DTS-HD", "PCM", "FLAC", "DTS-ES",
		"DTS", "E-AC-3", "AC3", "AAC", "Opus", "Vorbis", "MP3", "MP2",
	}

	codecMap := map[string][]string{
		"DTS-X":      {"DTS:X"},
		"E-AC-3 JOC": {"DD+ 5.1 Atmos", "DD+ 7.1 Atmos", "ATMOS"},
		"TrueHD":     {"TRUEHD"},
		"DTS-HD":     {"DTS-HD"},
		"PCM":        {"LPCM", "PCM"},
		"FLAC":       {"FLAC"},
		"DTS-ES":     {"DTS-ES"},
		"DTS":        {"DTS"},
		"E-AC-3":     {"DD+", "E-AC-3"},
		"AC3":        {"DD", "AC3"},
		"AAC":        {"AAC"},
		"Opus":       {"OPUS"},
		"Vorbis":     {"VORBIS"},
		"MP2":        {"MP2"},
		"MP3":        {"MP3"},
	}

	audioDescription := strings.ToUpper(strings.TrimSpace(meta.Audio))
	if audioDescription == "" {
		return "Outro"
	}

	for _, codecName := range priorityOrder {
		searchTerms := codecMap[codecName]
		for _, term := range searchTerms {
			if strings.Contains(audioDescription, term) {
				return codecName
			}
		}
	}

	return "Outro"
}

func resolveBitrate(meta api.UploadSubject) string {
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	if strings.ToUpper(strings.TrimSpace(meta.Type)) == "DISC" || discType == "BDMV" || discType == "DVD" {
		if discType == "BDMV" {
			size := meta.SourceSize
			switch {
			case size > 66000000000:
				return "BD100"
			case size > 50000000000:
				return "BD66"
			case size > 25000000000:
				return "BD50"
			default:
				return "BD25"
			}
		}
		if discType == "DVD" {
			dvdSize := strings.ToUpper(strings.TrimSpace(meta.Release.Size))
			if dvdSize == "DVD9" || dvdSize == "DVD5" {
				return dvdSize
			}
			return "DVD9"
		}
	}

	sourceType := strings.ToLower(strings.TrimSpace(meta.Type))
	keywordMap := map[string]string{
		"remux":  "Remux",
		"webdl":  "WEB-DL",
		"webrip": "WEBRip",
		"web":    "WEB",
		"encode": "Blu-ray",
		"bdrip":  "BDRip",
		"brrip":  "BRRip",
		"hdtv":   "HDTV",
		"sdtv":   "SDTV",
		"dvdrip": "DVDRip",
		"hd-dvd": "HD-DVD",
		"tvrip":  "TVRip",
	}

	if val, ok := keywordMap[sourceType]; ok {
		return val
	}

	source := strings.ToLower(strings.TrimSpace(meta.Release.Source))
	if val, ok := keywordMap[source]; ok {
		return val
	}

	return "Outro"
}

func resolveEdition(meta api.UploadSubject) string {
	edition := strings.ToLower(strings.TrimSpace(meta.Edition))
	switch {
	case strings.Contains(edition, "director"):
		return "Director's Cut"
	case strings.Contains(edition, "theatrical"):
		return "Theatrical Cut"
	case strings.Contains(edition, "extended"):
		return "Extended"
	case strings.Contains(edition, "uncut"):
		return "Uncut"
	case strings.Contains(edition, "unrated"):
		return "Unrated"
	case strings.Contains(edition, "imax"):
		return "IMAX"
	case strings.Contains(edition, "noir"):
		return "Noir"
	case strings.Contains(edition, "remaster"):
		return "Remastered"
	default:
		return ""
	}
}

func resolveTags(meta api.UploadSubject, ptBR api.TMDBLocalizedData) string {
	// 1. Use localized if available
	if ptBR.Genres != "" {
		genres := strings.Split(strings.TrimSpace(ptBR.Genres), ",")
		out := make([]string, 0, len(genres))
		for _, genre := range genres {
			cleaned := removeDiacritics(genre)
			tag := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(cleaned), " ", "."))
			if tag != "" {
				out = append(out, tag)
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
	default:
		genreText = strings.TrimSpace(meta.Release.Genre)
	}

	if genreText == "" {
		return ""
	}

	genres := strings.Split(genreText, ",")
	out := make([]string, 0, len(genres))
	for _, genre := range genres {
		translated := metautil.TranslateGenreToPortugueseStrict(genre)
		if translated == "" {
			translated = genre
		}
		cleaned := removeDiacritics(translated)
		tag := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(cleaned), " ", "."))
		if tag != "" {
			out = append(out, tag)
		}
	}
	return strings.Join(out, ", ")
}

func resolveLanguage(meta api.UploadSubject) string {
	var lang string
	if meta.ProviderMetadata.TMDB != nil {
		lang = strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage)
	}
	if lang == "" {
		if len(meta.Release.Language) > 0 {
			lang = meta.Release.Language[0]
		}
	}
	lang = strings.ToLower(lang)
	if lang == "" {
		return ""
	}

	return metautil.ISO639PortugueseName(lang, lang)
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return string(category)
}

func resolveAudio(meta api.UploadSubject) string {
	pt := false
	for _, lang := range meta.AudioLanguages {
		lower := strings.ToLower(strings.TrimSpace(lang))
		if lower == "portuguese" || lower == "português" || lower == "pt" {
			pt = true
			break
		}
	}
	orig := ""
	if meta.ProviderMetadata.TMDB != nil {
		orig = strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage))
	}
	if pt {
		if orig == "pt" {
			return "Nacional"
		}
		if len(meta.AudioLanguages) > 1 {
			return "Dual Audio"
		}
		return "Dublado"
	}
	return "Legendado"
}

func isTVUpload(meta api.UploadSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}
