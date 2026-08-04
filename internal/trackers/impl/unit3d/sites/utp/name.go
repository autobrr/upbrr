// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package utp

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// losslessAudioIndicators lists the codecs UTP keeps in the release name;
// lossy audio (AAC, DD, DD+, ...) is dropped entirely.
var losslessAudioIndicators = []string{"Atmos", "TrueHD", "DTS-HD MA", "DTS:X", "LPCM", "FLAC", "PCM"}

// buildName reconstructs a UTP-compliant release name from parsed metadata
// components rather than editing the base release name. The token order differs
// between Movie and TV (note the REPACK/Edition swap):
//
//	Movie: Title AKA Year Hybrid REPACK Edition Region 3D UHD Source Type Resolution HDR VCodec Audio-Tag
//	TV:    Title AKA S##E## Year Hybrid Edition REPACK Region 3D UHD Source Type Resolution HDR VCodec Audio-Tag
//
// Naming rules: https://utp.to/pages/33.
func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	category := unit3d.Category(meta)
	releaseType := unit3d.InferType(meta)

	title := utpTitle(meta, category)
	aka := utpAKA(meta, title)
	year := utpYear(meta.Release.Year)
	threeD := strings.TrimSpace(meta.Is3D)
	uhd := strings.TrimSpace(meta.UHD)
	edition, hybrid := splitHybridEdition(meta)
	repack := strings.TrimSpace(meta.Repack)
	resolution := strings.TrimSpace(unit3d.Resolution(meta))
	hdr := strings.TrimSpace(meta.HDR)
	service := strings.TrimSpace(meta.Service)
	audio := utpAudio(meta.Audio)
	videoCodec := strings.TrimSpace(meta.VideoCodec)
	videoEncode := strings.TrimSpace(meta.VideoEncode)
	tag := meta.Tag
	region := strings.TrimSpace(meta.Region)
	season := strings.TrimSpace(meta.SeasonStr)
	episode := strings.TrimSpace(meta.EpisodeStr)

	// The name-suppression toggles are naming-only: they never reach a metadata
	// field, so a from-scratch builder has to read them off the overrides.
	overrides := meta.ReleaseNameOverrides
	if isSet(overrides.NoYear) {
		year = ""
	}
	if isSet(overrides.NoSeason) {
		season, episode = "", ""
	}
	if isSet(overrides.NoAKA) {
		aka = ""
	}

	sourceTag := strings.TrimSpace(meta.Source)
	typeTag := ""
	vcodec := videoCodec // Default for DISC/REMUX (AVC, HEVC).

	switch releaseType {
	case "REMUX", "ENCODE":
		sourceTag = "" // BDRemux/BDRip replaces source.
		if releaseType == "REMUX" {
			typeTag = "BDRemux"
		} else {
			typeTag = "BDRip"
			vcodec = videoEncode
		}
	case "WEBDL", "WEBRIP":
		sourceTag = service // Service (NF, AMZN, ...) acts as source.
		if releaseType == "WEBDL" {
			typeTag = "WEB-DL"
		} else {
			typeTag = "WEBRip"
		}
		vcodec = videoEncode
	case "HDTV":
		vcodec = videoEncode
	}
	// DISC: source_tag stays as meta.Source (e.g. Blu-ray); no type tag is added.

	var name string
	switch category {
	case "MOVIE":
		name = strings.Join([]string{title, aka, year, hybrid, repack, edition, region, threeD, uhd, sourceTag, typeTag, resolution, hdr, vcodec, audio}, " ")
	case "TV":
		name = strings.Join(
			[]string{title, aka, season + episode, year, hybrid, edition, repack, region, threeD, uhd, sourceTag, typeTag, resolution, hdr, vcodec, audio},
			" ",
		)
	default:
		return baseReleaseName(meta)
	}

	name = collapseSpaces(name)
	if tag != "" {
		name += tag
	}
	return name
}

// utpTitle resolves the English name UTP requires. The parsed title is only a
// fallback: it is whatever the source directory happened to use, which for
// foreign releases is a romaji or transliterated name rather than the English
// one.
func utpTitle(meta api.UploadSubject, category string) string {
	candidates := make([]string, 0, 4)
	if category == "TV" && meta.ProviderMetadata.TVDB != nil {
		candidates = append(candidates, meta.ProviderMetadata.TVDB.NameEnglish)
	}
	if meta.ProviderMetadata.TMDB != nil {
		candidates = append(candidates, meta.ProviderMetadata.TMDB.Title)
	}
	if meta.ProviderMetadata.IMDB != nil {
		candidates = append(candidates, meta.ProviderMetadata.IMDB.Title)
	}
	candidates = append(candidates, meta.Release.Title)

	for _, candidate := range candidates {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return ""
}

// utpAKA returns the "AKA <original title>" segment that follows the English
// name: the romaji for anime, otherwise the TMDB original title. TMDB stores
// RetrievedAKA with the "AKA " prefix already applied.
//
// No other source qualifies. IMDb and the parsed release name carry a
// transliteration of the native title rather than the romaji, which is not a
// name UTP accepts: an anime whose romaji already equals its English title must
// get no AKA at all instead of a syllable-by-syllable rendering of the native
// one.
func utpAKA(meta api.UploadSubject, title string) string {
	candidates := make([]string, 0, 2)
	if meta.ProviderMetadata.TMDB != nil {
		candidates = append(candidates, meta.ProviderMetadata.TMDB.RetrievedAKA, meta.ProviderMetadata.TMDB.OriginalTitle)
	}

	for _, candidate := range candidates {
		value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(candidate), "AKA "))
		if value == "" || strings.EqualFold(value, strings.TrimSpace(title)) || !isLatinScript(value) {
			continue
		}
		return "AKA " + value
	}
	return ""
}

// isLatinScript reports whether every letter in value is Latin, so native
// titles (kanji, Cyrillic, ...) never reach the release name.
func isLatinScript(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			return false
		}
	}
	return true
}

// utpYear renders the release year, returning an empty string when absent so
// the template collapses the slot away.
func utpYear(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}

// utpAudio keeps the audio segment only when it names a lossless/object codec,
// then drops Dual-Audio/Dubbed markers and collapses whitespace. Lossy audio is
// omitted from the name entirely.
func utpAudio(audioRaw string) string {
	lossless := false
	for _, indicator := range losslessAudioIndicators {
		if strings.Contains(audioRaw, indicator) {
			lossless = true
			break
		}
	}
	if !lossless {
		return ""
	}
	audio := strings.ReplaceAll(audioRaw, "Dual-Audio", "")
	audio = strings.ReplaceAll(audio, "Dubbed", "")
	return collapseSpaces(audio)
}

// splitHybridEdition separates the hybrid marker from the edition. Hybrid is
// carried inside the edition (metadata folds the parsed token in there), but a
// name that builds from components renders it in its own slot.
func splitHybridEdition(meta api.UploadSubject) (edition string, hybrid string) {
	fields := strings.Fields(meta.Edition)
	kept := fields[:0]
	for _, value := range fields {
		if strings.EqualFold(value, "Hybrid") {
			hybrid = "Hybrid"
			continue
		}
		kept = append(kept, value)
	}
	if meta.WebDV {
		hybrid = "Hybrid"
	}
	return strings.Join(kept, " "), hybrid
}

// baseReleaseName is the generic fallback for categories the UTP template does
// not cover.
func baseReleaseName(meta api.UploadSubject) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	return collapseSpaces(name)
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// isSet reports whether an optional override flag is present and enabled.
func isSet(flag *bool) bool {
	return flag != nil && *flag
}
