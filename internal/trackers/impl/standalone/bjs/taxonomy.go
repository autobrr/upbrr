// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

func resolveType(meta api.UploadSubject) string {
	if meta.Anime {
		return "13"
	}
	if strings.EqualFold(categoryOf(meta), "TV") {
		return "1"
	}
	return "0"
}

func resolveContainer(meta api.UploadSubject) string {
	container := strings.ToLower(strings.TrimSpace(meta.Container))
	switch container {
	case "mkv", "mp4", "avi", "vob", "m2ts", "ts":
		return strings.ToUpper(container)
	default:
		return "Outro"
	}
}

func resolveLanguage(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return "Outro"
	}

	langCode := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage))
	if langCode == "" {
		return "Outro"
	}

	if langCode == "pt" {
		for _, country := range meta.ProviderMetadata.TMDB.OriginCountry {
			if strings.ToUpper(strings.TrimSpace(country)) == "PT" {
				return "Português (pt)"
			}
		}
		return "Português"
	}

	return metautil.ISO639PortugueseName(langCode, "Outro")
}

func resolveSubtitle(meta api.UploadSubject) string {
	for _, lang := range meta.SubtitleLanguages {
		lower := strings.ToLower(strings.TrimSpace(lang))
		if lower == "portuguese" || lower == "português" || lower == "pt" {
			return "Embutida"
		}
	}
	return "Nenhuma"
}

func resolveResolution(meta api.UploadSubject) (string, string) {
	height := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(meta.Release.Resolution), "p"), "i")
	switch height {
	case "2160":
		return "3840", "2160"
	case "1080":
		return "1920", "1080"
	case "720":
		return "1280", "720"
	default:
		return "0", "0"
	}
}

func resolveVideoCodec(meta api.UploadSubject) string {
	value := strings.ToLower(strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.VideoEncode, meta.VideoCodec)))
	switch {
	case strings.Contains(value, "265"), strings.Contains(value, "hevc"):
		return "H.265"
	case strings.Contains(value, "264"), strings.Contains(value, "avc"):
		return "H.264"
	case strings.Contains(value, "av1"):
		return "AV1"
	case strings.Contains(value, "vp9"):
		return "VP9"
	case strings.Contains(value, "xvid"):
		return "XviD"
	default:
		return metautil.FirstNonEmptyTrimmed(meta.VideoCodec, "Outro")
	}
}

func resolveAudioCodec(meta api.UploadSubject) string {
	audio := strings.ToUpper(strings.TrimSpace(meta.Audio))
	switch {
	case strings.Contains(audio, "DTS:X"):
		return "DTS-X"
	case strings.Contains(audio, "ATMOS"):
		return "E-AC-3 JOC"
	case strings.Contains(audio, "TRUEHD"):
		return "TrueHD"
	case strings.Contains(audio, "DTS-HD"):
		return "DTS-HD"
	case strings.Contains(audio, "FLAC"):
		return "FLAC"
	case strings.Contains(audio, "LPCM"), strings.Contains(audio, "PCM"):
		return "PCM"
	case strings.Contains(audio, "DTS"):
		return "DTS"
	case strings.Contains(audio, "DD+"), strings.Contains(audio, "E-AC-3"):
		return "E-AC-3"
	case strings.Contains(audio, "DD"), strings.Contains(audio, "AC3"):
		return "AC3"
	case strings.Contains(audio, "AAC"):
		return "AAC"
	default:
		return "Outro"
	}
}

func resolveQuality(meta api.UploadSubject) string {
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "DISC":
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
			if meta.SourceSize > 66<<30 {
				return "BD100"
			}
			if meta.SourceSize > 50<<30 {
				return "BD66"
			}
			if meta.SourceSize > 25<<30 {
				return "BD50"
			}
			return "BD25"
		}
		return "DVD9"
	case "REMUX":
		return "Remux"
	case "WEBDL":
		return "WEB-DL"
	case "WEBRIP":
		return "WEBRip"
	case "HDTV":
		return "HDTV"
	default:
		return "Outro"
	}
}

// resolveTags returns BJS tag text from localized genres or translated fallback
// genres, preserving unknown fallback genre names after tag normalization.
func resolveTags(meta api.UploadSubject, ptBR api.TMDBLocalizedData) string {
	// 1. Use localized if available
	if ptBR.Genres != "" {
		genres := strings.Split(strings.TrimSpace(ptBR.Genres), ",")
		out := make([]string, 0, len(genres))
		for _, g := range genres {
			g = strings.TrimSpace(g)
			t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
			g, _, _ = transform.String(t, g)
			g = strings.ReplaceAll(g, " ", ".")
			g = strings.ToLower(g)
			if g != "" {
				out = append(out, g)
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
	for _, g := range genres {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		translated := metautil.TranslateGenreToPortugueseStrict(g)
		if translated == "" {
			translated = g
		}
		t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
		tag, _, _ := transform.String(t, translated)
		tag = strings.ReplaceAll(strings.TrimSpace(tag), " ", ".")
		if tag != "" {
			out = append(out, strings.ToLower(tag))
		}
	}
	return strings.Join(out, ", ")
}

// resolveAdult returns the BJS adult flag from localized, TMDB, IMDB, and
// release genre text after accent-insensitive keyword matching.
func resolveAdult(meta api.UploadSubject) string {
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
	parts := []string{resolveTags(meta, ptBR), ptBR.Genres}
	if meta.ProviderMetadata.TMDB != nil {
		parts = append(parts, meta.ProviderMetadata.TMDB.Keywords, meta.ProviderMetadata.TMDB.Genres)
	}
	if meta.ProviderMetadata.IMDB != nil {
		parts = append(parts, meta.ProviderMetadata.IMDB.Genres)
	}
	parts = append(parts, meta.Release.Genre)
	genres := normalizeAdultText(strings.Join(parts, " "))
	if meta.Anime && strings.Contains(genres, "hentai") {
		return "1"
	}
	for _, keyword := range []string{"xxx", "erotic", "erotico", "porn", "adult", "adulto", "orgy", "orgia"} {
		if strings.Contains(genres, keyword) {
			return "1"
		}
	}
	return "2"
}

// normalizeAdultText folds case and diacritics before adult keyword matching.
func normalizeAdultText(value string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	normalized, _, _ := transform.String(t, strings.ToLower(value))
	return normalized
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return string(category)
}

func resolveAudio(meta api.UploadSubject) string {
	for _, lang := range meta.AudioLanguages {
		lower := strings.ToLower(strings.TrimSpace(lang))
		if lower == "portuguese" || lower == "português" || lower == "pt" {
			if len(meta.AudioLanguages) > 1 {
				return "Dual Áudio"
			}
			return "Dublado"
		}
	}
	return "Legendado"
}

func isTVUpload(meta api.UploadSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}
