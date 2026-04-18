// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	descriptionunit3d "github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	acmSceneNFOPattern = regexp.MustCompile(`(?is)\[center\]\[spoiler=Scene NFO:\].*?\[/center\]`)
	acmSubtitleCodes   = map[string]string{
		"arabic":                "Ara",
		"ara":                   "Ara",
		"ar":                    "Ara",
		"brazilian portuguese":  "Por-BR",
		"brazilian":             "Por-BR",
		"portuguese-br":         "Por-BR",
		"pt-br":                 "Por-BR",
		"bulgarian":             "Bul",
		"bul":                   "Bul",
		"bg":                    "Bul",
		"chinese":               "Chi",
		"chi":                   "Chi",
		"zh":                    "Chi",
		"chinese (simplified)":  "Chi",
		"chinese (traditional)": "Chi",
		"croatian":              "Cro",
		"hrv":                   "Cro",
		"hr":                    "Cro",
		"scr":                   "Cro",
		"czech":                 "Cze",
		"cze":                   "Cze",
		"cz":                    "Cze",
		"cs":                    "Cze",
		"danish":                "Dan",
		"dan":                   "Dan",
		"da":                    "Dan",
		"dutch":                 "Dut",
		"dut":                   "Dut",
		"nl":                    "Dut",
		"english":               "Eng",
		"eng":                   "Eng",
		"en":                    "Eng",
		"english (cc)":          "Eng",
		"english - sdh":         "Eng",
		"english - forced":      "Eng",
		"english (forced)":      "Eng",
		"en (forced)":           "Eng",
		"english intertitles":   "Eng",
		"english (intertitles)": "Eng",
		"english - intertitles": "Eng",
		"en (intertitles)":      "Eng",
		"estonian":              "Est",
		"est":                   "Est",
		"et":                    "Est",
		"finnish":               "Fin",
		"fin":                   "Fin",
		"fi":                    "Fin",
		"french":                "Fre",
		"fre":                   "Fre",
		"fr":                    "Fre",
		"german":                "Ger",
		"ger":                   "Ger",
		"de":                    "Ger",
		"greek":                 "Gre",
		"gre":                   "Gre",
		"el":                    "Gre",
		"hebrew":                "Heb",
		"heb":                   "Heb",
		"he":                    "Heb",
		"hindi":                 "Hin",
		"hin":                   "Hin",
		"hi":                    "Hin",
		"hungarian":             "Hun",
		"hun":                   "Hun",
		"hu":                    "Hun",
		"icelandic":             "Ice",
		"ice":                   "Ice",
		"is":                    "Ice",
		"indonesian":            "Ind",
		"ind":                   "Ind",
		"id":                    "Ind",
		"italian":               "Ita",
		"ita":                   "Ita",
		"it":                    "Ita",
		"japanese":              "Jpn",
		"jpn":                   "Jpn",
		"ja":                    "Jpn",
		"korean":                "Kor",
		"kor":                   "Kor",
		"ko":                    "Kor",
		"latvian":               "Lav",
		"lav":                   "Lav",
		"lv":                    "Lav",
		"lithuanian":            "Lit",
		"lit":                   "Lit",
		"lt":                    "Lit",
		"norwegian":             "Nor",
		"nor":                   "Nor",
		"no":                    "Nor",
		"persian":               "Per",
		"fa":                    "Per",
		"far":                   "Per",
		"polish":                "Pol",
		"pol":                   "Pol",
		"pl":                    "Pol",
		"portuguese":            "Por",
		"por":                   "Por",
		"pt":                    "Por",
		"romanian":              "Rom",
		"rum":                   "Rom",
		"ro":                    "Rom",
		"russian":               "Rus",
		"rus":                   "Rus",
		"ru":                    "Rus",
		"serbian":               "Ser",
		"srp":                   "Ser",
		"sr":                    "Ser",
		"scc":                   "Ser",
		"slovak":                "Slo",
		"slo":                   "Slo",
		"sk":                    "Slo",
		"slovenian":             "Slv",
		"slv":                   "Slv",
		"sl":                    "Slv",
		"spanish":               "Spa",
		"spa":                   "Spa",
		"es":                    "Spa",
		"swedish":               "Swe",
		"swe":                   "Swe",
		"sv":                    "Swe",
		"thai":                  "Tha",
		"tha":                   "Tha",
		"th":                    "Tha",
		"turkish":               "Tur",
		"tur":                   "Tur",
		"tr":                    "Tur",
		"ukrainian":             "Ukr",
		"ukr":                   "Ukr",
		"uk":                    "Ukr",
		"vietnamese":            "Vie",
		"vie":                   "Vie",
		"vi":                    "Vie",
	}
)

func siteACMProfile() unit3DSiteProfile {
	return unit3DSiteProfile{
		resolveTypeID:       resolveUnit3DACMTypeID,
		resolveResolutionID: resolveUnit3DACMResolutionID,
		applyAdditionalPayload: func(req trackers.UploadRequest, data map[string]string) {
			if regionID := numericValue(req.Meta.Region); regionID != "" {
				data["region_id"] = regionID
			}
			if distributorID := numericValue(req.Meta.Distributor); distributorID != "" {
				data["distributor_id"] = distributorID
			}
		},
	}
}

func resolveUnit3DACMTypeID(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		sizeBucket := acmDiscBucket(meta.SourceSize)
		if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && sizeBucket != 25 {
			switch sizeBucket {
			case 50:
				return "3"
			case 66:
				return "2"
			case 100:
				return "1"
			}
		}
		switch sizeBucket {
		case 25:
			return "5"
		case 50:
			return "4"
		}
		return "0"
	}

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		switch acmDVDType(meta) {
		case "DVD 5":
			return "14"
		case "DVD 9":
			return "16"
		default:
			return "0"
		}
	}

	switch acmNonDiscType(meta) {
	case "UHD REMUX":
		return "12"
	case "REMUX":
		return "7"
	case "WEBDL":
		return "9"
	case "SDTV":
		return "13"
	case "HDTV":
		return "17"
	default:
		return "0"
	}
}

func resolveUnit3DACMResolutionID(meta api.PreparedMetadata) string {
	switch strings.ToLower(strings.TrimSpace(resolveResolution(meta))) {
	case "2160p":
		return "1"
	case "1080p", "1080i":
		return "2"
	case "720p":
		return "3"
	case "576p", "576i":
		return "4"
	case "480p", "480i":
		return "5"
	default:
		return "10"
	}
}

func buildACMName(meta api.PreparedMetadata) string {
	name := baseReleaseName(meta)
	if name == "" {
		return ""
	}

	title := strings.TrimSpace(resolveACMTitle(meta))
	originalTitle := strings.TrimSpace(resolveACMOriginalTitle(meta))
	if title != "" && originalTitle != "" && !strings.EqualFold(title, originalTitle) {
		name = strings.Replace(name, title, title+" / "+originalTitle+" \u202A", 1)
	}

	audio := strings.TrimSpace(meta.Audio)
	if strings.Contains(audio, "AAC") {
		normalizedAudio := strings.Join(strings.Fields(audio), " ")
		name = strings.Replace(name, normalizedAudio, strings.ReplaceAll(normalizedAudio, "AAC ", "AAC"), 1)
	}
	name = strings.ReplaceAll(name, "DD+ ", "DD+")
	name = strings.ReplaceAll(name, "UHD BluRay REMUX", "Remux")
	name = strings.ReplaceAll(name, "BluRay REMUX", "Remux")
	name = strings.ReplaceAll(name, "H.265", "HEVC")
	name = strings.ReplaceAll(name, " Atmos", "")

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		source := strings.TrimSpace(meta.Source)
		resolution := strings.TrimSpace(resolveResolution(meta))
		if source != "" && resolution != "" {
			name = strings.ReplaceAll(name, source+" DVD5", resolution+" DVD "+source)
			name = strings.ReplaceAll(name, source+" DVD9", resolution+" DVD "+source)
		}
		if audio != "" && audio == strings.TrimSpace(meta.Channels) {
			name = strings.ReplaceAll(name, audio, "MPEG "+audio)
		}
	}

	return strings.TrimSpace(strings.Join(strings.Fields(name), " ")) + acmSubtitleTag(acmSubtitleCodesFor(meta))
}

func buildACMDescription(ctx context.Context, meta api.PreparedMetadata, appConfig config.Config, trackerConfig config.TrackerConfig, logger api.Logger, keptDescription string, menuImages []api.ScreenshotImage, screenshots []api.ScreenshotImage) (string, error) {
	base := acmSceneNFOPattern.ReplaceAllString(strings.TrimSpace(keptDescription), "")
	base = strings.ReplaceAll(base, "\r\n", "\n")
	base = strings.ReplaceAll(base, "[pre]", "[code]")
	base = strings.ReplaceAll(base, "[/pre]", "[/code]")
	base = strings.ReplaceAll(base, "[hide", "[spoiler")
	base = strings.ReplaceAll(base, "[/hide]", "[/spoiler]")
	base = convertACMComparisonToCollapse(base, 1000)
	base = strings.ReplaceAll(base, "[img]", "[img=300]")
	base = descriptionunit3d.AppendDVDVOBMediaInfoBlock(base, meta)

	cfg := appConfig
	if cfg.Description.ThumbnailSize <= 0 {
		cfg.Description.ThumbnailSize = 350
	}
	if len(screenshots) > 0 {
		cfg.Description.ScreensPerRow = strconv.Itoa(len(screenshots))
	} else if strings.TrimSpace(cfg.Description.ScreensPerRow) == "" {
		cfg.Description.ScreensPerRow = "1"
	}

	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") && strings.TrimSpace(meta.ServiceLongName) != "" {
		header := fmt.Sprintf(
			"[center][b][color=#ff00ff][size=18]This release is sourced from %s and is not transcoded, just remuxed from the direct %s stream[/size][/color][/b][/center]",
			strings.TrimSpace(meta.ServiceLongName),
			strings.TrimSpace(meta.ServiceLongName),
		)
		base = strings.TrimSpace(strings.Join([]string{header, base}, "\n"))
	}

	return descriptionunit3d.BuildDescription(ctx, meta, cfg, trackerConfig, logger, base, menuImages, screenshots)
}

func resolveACMKeywords(meta api.PreparedMetadata) string {
	raw := resolveKeywords(meta)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" || strings.Contains(trimmed, " ") {
			continue
		}
		filtered = append(filtered, trimmed)
		if len(filtered) >= 10 {
			break
		}
	}
	return strings.Join(filtered, ", ")
}

func acmDiscBucket(sourceSize int64) int {
	if sourceSize <= 0 {
		return 100
	}
	sizeGiB := float64(sourceSize) / float64(1<<30)
	for _, bucket := range []int{25, 50, 66, 100} {
		if sizeGiB < float64(bucket) {
			return bucket
		}
	}
	return 100
}

func acmDVDType(meta api.PreparedMetadata) string {
	name := strings.ToUpper(strings.TrimSpace(baseReleaseName(meta)))
	switch {
	case strings.Contains(name, "DVD5"):
		return "DVD 5"
	case strings.Contains(name, "DVD9"):
		return "DVD 9"
	case meta.SourceSize > 0 && meta.SourceSize <= 5*(1<<30):
		return "DVD 5"
	case meta.SourceSize > 0:
		return "DVD 9"
	default:
		return ""
	}
}

func acmNonDiscType(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(inferUnit3DType(meta)), "REMUX") && strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") {
		return "UHD REMUX"
	}
	switch strings.ToUpper(strings.TrimSpace(inferUnit3DType(meta))) {
	case "WEBDL", "WEB-DL":
		return "WEBDL"
	case "HDTV":
		if strings.HasPrefix(strings.TrimSpace(resolveResolution(meta)), "4") || strings.HasPrefix(strings.TrimSpace(resolveResolution(meta)), "5") {
			return "SDTV"
		}
		return "HDTV"
	case "REMUX":
		return "REMUX"
	default:
		return ""
	}
}

func resolveACMTitle(meta api.PreparedMetadata) string {
	for _, value := range []string{
		meta.Release.Title,
		resolveACMTMDBTitle(meta),
		resolveACMIMDBTitle(meta),
	} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func resolveACMOriginalTitle(meta api.PreparedMetadata) string {
	for _, value := range []string{
		resolveACMTMDBOriginalTitle(meta),
		resolveACMTMDBRetrievedAKA(meta),
		resolveACMIMDBAKA(meta),
		meta.Release.Alt,
	} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return strings.TrimPrefix(trimmed, "AKA ")
		}
	}
	return ""
}

func acmSubtitleCodesFor(meta api.PreparedMetadata) []string {
	out := make([]string, 0, len(meta.SubtitleLanguages))
	seen := make(map[string]struct{}, len(meta.SubtitleLanguages))
	for _, language := range meta.SubtitleLanguages {
		key := strings.ToLower(strings.TrimSpace(language))
		if key == "" {
			continue
		}
		code, ok := acmSubtitleCodes[key]
		if !ok {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out
}

func acmSubtitleTag(subtitles []string) string {
	if len(subtitles) == 0 {
		return " [No subs]"
	}
	for _, subtitle := range subtitles {
		if subtitle == "Eng" {
			return ""
		}
	}
	if len(subtitles) > 1 {
		return " [No Eng subs]"
	}
	return " [" + subtitles[0] + " subs only]"
}

func convertACMComparisonToCollapse(value string, maxWidth int) string {
	re := regexp.MustCompile(`(?is)\[comparison=[\s\S]*?\[/comparison\]`)
	return re.ReplaceAllStringFunc(value, func(block string) string {
		parts := strings.SplitN(block, "]", 2)
		if len(parts) < 2 {
			return block
		}
		sources := strings.Split(strings.ReplaceAll(strings.TrimPrefix(parts[0], "[comparison="), " ", ""), ",")
		if len(sources) == 0 {
			return block
		}
		imgSize := maxWidth / len(sources)
		if imgSize > 350 {
			imgSize = 350
		}
		body := strings.TrimSuffix(parts[1], "[/comparison]")
		fields := strings.Fields(strings.ReplaceAll(body, ",", " "))
		images := make([]string, 0, len(fields))
		for _, field := range fields {
			trimmed := strings.TrimSpace(field)
			if strings.HasPrefix(strings.ToLower(trimmed), "http://") || strings.HasPrefix(strings.ToLower(trimmed), "https://") {
				images = append(images, trimmed)
			}
		}
		if len(images) == 0 {
			return block
		}
		lines := make([]string, 0, len(images)/len(sources)+1)
		row := make([]string, 0, len(sources))
		for _, image := range images {
			row = append(row, fmt.Sprintf("[url=%s][img=%d]%s[/img][/url]", image, imgSize, image))
			if len(row) == len(sources) {
				lines = append(lines, strings.Join(row, ""))
				row = row[:0]
			}
		}
		if len(row) > 0 {
			lines = append(lines, strings.Join(row, ""))
		}
		return fmt.Sprintf("[spoiler=%s][center]%s[/center]\n%s[/spoiler]", strings.Join(sources, " vs "), strings.Join(sources, " | "), strings.Join(lines, "\n"))
	})
}

func resolveACMTMDBTitle(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB == nil {
		return ""
	}
	return meta.ExternalMetadata.TMDB.Title
}

func resolveACMTMDBOriginalTitle(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB == nil {
		return ""
	}
	return meta.ExternalMetadata.TMDB.OriginalTitle
}

func resolveACMTMDBRetrievedAKA(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB == nil {
		return ""
	}
	return meta.ExternalMetadata.TMDB.RetrievedAKA
}

func resolveACMIMDBTitle(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB == nil {
		return ""
	}
	return meta.ExternalMetadata.IMDB.Title
}

func resolveACMIMDBAKA(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB == nil {
		return ""
	}
	return meta.ExternalMetadata.IMDB.AKA
}
