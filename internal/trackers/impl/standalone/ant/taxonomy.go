// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"strings"

	"golang.org/x/text/language"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveType(meta api.UploadSubject, answers map[string]string) (string, int) {
	if !movieContent(meta) {
		return "", 0
	}
	if text := normalizeTypeName(answers["type"]); text != "" {
		return text, antTypeID(text)
	}
	if meta.ProviderMetadata.IMDB != nil {
		imdbType := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.IMDB.Type))
		runtime := meta.ProviderMetadata.IMDB.RuntimeMinutes
		switch imdbType {
		case "movie", "tv movie", "tvmovie":
			if runtime >= 45 || runtime == 0 {
				return "Feature Film", 0
			}
			return "Short Film", 1
		case "short", "tv short", "tvshort":
			return "Short Film", 1
		}
	}
	runtime := 0
	if meta.ProviderMetadata.TMDB != nil {
		runtime = meta.ProviderMetadata.TMDB.Runtime
	}
	if runtime >= 45 || runtime == 0 {
		return "Feature Film", 0
	}
	return "Short Film", 1
}

func movieContent(meta api.UploadSubject) bool {
	if meta.ProviderMetadata.IMDB != nil {
		switch strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.IMDB.Type)) {
		case "movie", "tv movie", "tvmovie", "short", "tv short", "tvshort":
			return true
		case "":
		default:
			return false
		}
	}
	return meta.Identity.Category == api.CanonicalCategoryMovie
}

func normalizeTypeName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "feature film", "feature", "movie":
		return "Feature Film"
	case "short film", "short":
		return "Short Film"
	default:
		return ""
	}
}

func resolveFlags(meta api.UploadSubject) []string {
	flags := make([]string, 0, 12)
	edition := strings.ToUpper(strings.NewReplacer("'", "", " ", "").Replace(meta.EditionLabel()))
	for _, candidate := range []string{"Directors", "Extended", "Uncut", "Unrated", "4KRemaster", "IMAX"} {
		if strings.Contains(edition, strings.ToUpper(candidate)) {
			flags = append(flags, candidate)
		}
	}
	if !fullDisc(meta) && meta.LanguageFacts.HasEnglishDub() && meta.LanguageFacts.HasOriginalAudio() {
		flags = append(flags, "EnglishDub")
	}
	if strings.Contains(meta.Audio, "Atmos") {
		flags = append(flags, "Atmos")
	}
	if meta.HasCommentary {
		flags = append(flags, "Commentary")
	}
	if strings.EqualFold(strings.TrimSpace(meta.Is3D), "3D") {
		flags = append(flags, "3D")
	}
	if strings.Contains(strings.ToUpper(meta.HDR), "HDR") {
		flags = append(flags, "HDR10")
	}
	if strings.Contains(strings.ToUpper(meta.HDR), "DV") {
		flags = append(flags, "DV")
	}
	if strings.Contains(strings.ToUpper(meta.Distributor), "CRITERION") || strings.Contains(strings.ToUpper(meta.EditionLabel()), "CRITERION") {
		flags = append(flags, "Criterion")
	}
	if strings.Contains(strings.ToUpper(meta.Type), "REMUX") {
		flags = append(flags, "Remux")
	}
	return dedupeStrings(flags)
}

// resolveTags preserves explicit answers and genre corrections, including clears,
// before trying usable TMDB genres and then IMDb genres.
func resolveTags(meta api.UploadSubject, answers map[string]string) (string, bool) {
	if value, answered := answers["tags"]; answered {
		return normalizeTags(value), true
	}
	if meta.EffectiveMetadata.GenresProvenance.IsManual() {
		tags := genreTags(trackers.PreferredGenreText(meta, ""))
		return tags, tags == ""
	}
	if meta.ProviderMetadata.TMDB != nil {
		if tags := genreTags(meta.ProviderMetadata.TMDB.Genres); tags != "" {
			return tags, false
		}
	}
	if meta.ProviderMetadata.IMDB != nil {
		if tags := genreTags(meta.ProviderMetadata.IMDB.Genres); tags != "" {
			return tags, false
		}
	}
	return "", true
}

func genreTags(value string) string {
	values := splitTags(value)
	allowed := map[string]struct{}{
		"action":      {},
		"adventure":   {},
		"animation":   {},
		"comedy":      {},
		"crime":       {},
		"documentary": {},
		"drama":       {},
		"family":      {},
		"fantasy":     {},
		"history":     {},
		"horror":      {},
		"music":       {},
		"mystery":     {},
		"romance":     {},
		"sci.fi":      {},
		"thriller":    {},
		"war":         {},
		"western":     {},
	}
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		switch value {
		case "science.fiction", "sci-fi":
			value = "sci.fi"
		}
		if _, ok := allowed[value]; ok {
			filtered = append(filtered, value)
		}
	}
	return strings.Join(dedupeStrings(filtered), ",")
}

func detectAdult(meta api.UploadSubject) bool {
	candidates := []string{resolveKeywords(meta)}
	if meta.EffectiveMetadata.GenresProvenance.IsManual() {
		candidates = append(candidates, strings.Join(meta.EffectiveMetadata.Genres, ","))
	} else {
		candidates = append(candidates, meta.Release.Genre)
		if meta.ProviderMetadata.TMDB != nil {
			candidates = append(candidates, meta.ProviderMetadata.TMDB.Genres)
		}
	}
	for _, candidate := range candidates {
		lower := strings.ToLower(candidate)
		for _, token := range []string{"xxx", "erotic", "porn", "adult", "orgy"} {
			if strings.Contains(lower, token) {
				return true
			}
		}
	}
	return false
}

func resolveAdultScreensAllowed(answers map[string]string, adultContent bool) bool {
	if !adultContent {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(answers["adult_screens"])) {
	case "y", "yes", "true", "1":
		return true
	default:
		return false
	}
}

func antTypeID(value string) int {
	switch normalizeTypeName(value) {
	case "Short Film":
		return 1
	default:
		return 0
	}
}

func fullDisc(meta api.UploadSubject) bool {
	return mediafacts.IsFullDisc(meta.DiscType, meta.Type)
}

func resolveMediaSource(source string) string {
	switch strings.NewReplacer("-", "", " ", "", "_", "").Replace(strings.ToUpper(strings.TrimSpace(source))) {
	case "BLURAY", "BLURAY3D", "BD", "BDMV":
		return "BluRay"
	case "WEB", "WEBDL", "WEBRIP":
		return "WEB"
	case "DVD", "PAL", "NTSC", "PALDVD", "NTSCDVD":
		return "DVD"
	case "HDDVD":
		return "HDDVD"
	case "LASERDISC":
		return "LaserDisc"
	case "HDTV", "UHDTV":
		return "HDTV"
	case "TV":
		return "TV"
	case "VHS":
		return "VHS"
	case "", "UNKNOWN":
		return "Unknown"
	default:
		return "Other"
	}
}

// discCountry maps the prepared release Region country code to ANT's ISO alpha-2 field.
func discCountry(region string) string {
	code := strings.ToUpper(strings.TrimSpace(region))
	// These release-name codes differ from ISO alpha-3.
	aliases := map[string]string{
		"GER": "DEU",
		"CHI": "CHL",
		"DEN": "DNK",
		"GRE": "GRC",
		"SIN": "SGP",
		"SUI": "CHE",
		"UAE": "ARE",
		"VIE": "VNM",
		"UK":  "GB",
	}
	if iso := aliases[code]; iso != "" {
		code = iso
	}
	r, err := language.ParseRegion(code)
	if err != nil || !r.IsCountry() {
		return ""
	}
	return r.String()
}

func normalizeTags(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return strings.Join(dedupeStrings(splitTags(value)), ",")
}

func splitTags(value string) []string {
	items := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' })
	result := make([]string, 0, len(items))
	for _, item := range items {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(item), " ", "."))
		if normalized != "" {
			result = append(result, normalized)
		}
	}
	return result
}
