// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/pkg/api"
)

func siteUTPProfile() unit3DSiteProfile {
	return unit3DSiteProfile{
		resolveTypeID:       resolveUnit3DUTPTypeID,
		resolveResolutionID: resolveUnit3DUTPResolutionID,
	}
}

// utpLosslessAudioIndicators lists the codecs UTOPIA keeps in the release name;
// lossy audio (AAC, DD, DD+, ...) is dropped entirely.
var utpLosslessAudioIndicators = []string{"Atmos", "TrueHD", "DTS-HD MA", "DTS:X", "LPCM", "FLAC", "PCM"}

// buildUTPName reconstructs a UTOPIA-compliant release name from parsed metadata
// components rather than editing the base release name. The token order differs
// between Movie and TV (note the REPACK/Edition swap):
//
//	Movie: Title AKA Year Hybrid REPACK Edition Region 3D UHD Source Type Resolution HDR VCodec Audio-Tag
//	TV:    Title AKA S##E## Year Hybrid Edition REPACK Region 3D UHD Source Type Resolution HDR VCodec Audio-Tag
//
// Naming rules: https://utp.to/pages/33.
func buildUTPName(meta api.PreparedMetadata) string {
	category := resolveUnit3DCategory(meta)
	releaseType := inferUnit3DType(meta)

	title := utpTitle(meta, category)
	aka := utpAKA(meta, title)
	year := utpYear(meta.Release.Year)
	threeD := strings.TrimSpace(meta.Is3D)
	uhd := strings.TrimSpace(meta.UHD)
	edition, hybrid := splitHybridEdition(meta)
	repack := strings.TrimSpace(meta.Repack)
	resolution := strings.TrimSpace(resolveResolution(meta))
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
		name = strings.Join([]string{title, aka, season + episode, year, hybrid, edition, repack, region, threeD, uhd, sourceTag, typeTag, resolution, hdr, vcodec, audio}, " ")
	default:
		name = baseReleaseName(meta)
	}

	name = collapseSpaces(name)
	if tag != "" {
		name += tag
	}
	return name
}

// utpTitle resolves the English name UTOPIA requires. The parsed title is only a
// fallback: it is whatever the source directory happened to use, which for foreign
// releases is a romaji or transliterated name rather than the English one.
func utpTitle(meta api.PreparedMetadata, category string) string {
	candidates := make([]string, 0, 4)
	if category == "TV" && meta.ExternalMetadata.TVDB != nil {
		candidates = append(candidates, meta.ExternalMetadata.TVDB.NameEnglish)
	}
	if meta.ExternalMetadata.TMDB != nil {
		candidates = append(candidates, meta.ExternalMetadata.TMDB.Title)
	}
	if meta.ExternalMetadata.IMDB != nil {
		candidates = append(candidates, meta.ExternalMetadata.IMDB.Title)
	}
	candidates = append(candidates, meta.Release.Title)

	for _, candidate := range candidates {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return ""
}

// utpAKA returns the "AKA <original title>" segment that follows the English name:
// the AniList romaji for anime, otherwise the TMDB original title. TMDB stores
// RetrievedAKA with the "AKA " prefix already applied.
//
// No other source qualifies. IMDb and the parsed release name carry a transliteration
// of the native title rather than the romaji, which is not a name UTOPIA accepts: an
// anime whose romaji already equals its English title must get no AKA at all instead
// of a syllable-by-syllable rendering of the native one.
func utpAKA(meta api.PreparedMetadata, title string) string {
	candidates := make([]string, 0, 2)
	if meta.ExternalMetadata.TMDB != nil {
		candidates = append(candidates, meta.ExternalMetadata.TMDB.RetrievedAKA, meta.ExternalMetadata.TMDB.OriginalTitle)
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

// isLatinScript reports whether every letter in value is Latin, so native titles
// (kanji, Cyrillic, ...) never reach the release name.
func isLatinScript(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			return false
		}
	}
	return true
}

// utpYear renders the release year, returning an empty string when absent so the
// template collapses the slot away.
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
	for _, indicator := range utpLosslessAudioIndicators {
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

// resolveUnit3DUTPTypeID uses the standard UNIT3D type IDs and falls back to
// ENCODE (3) when the type cannot be determined, which is UTOPIA's default.
func resolveUnit3DUTPTypeID(meta api.PreparedMetadata) string {
	return resolveUnit3DTypeIDWithFallback(meta, "3")
}

// resolveUnit3DUTPResolutionID keeps a UTP-specific map: UTOPIA only assigns IDs
// to 4320p/2160p/1080p/1080i and files every other resolution under Other (11).
func resolveUnit3DUTPResolutionID(meta api.PreparedMetadata) string {
	resolution := strings.ToLower(resolveResolution(meta))
	mapping := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1080p": "3",
		"1080i": "4",
	}
	return lookupUnit3DID(resolution, mapping, "11")
}

// swapUTPImageURLs remaps each screenshot so the Unit3D description builder renders
// [url=full][img]medium[/img]: the builder uses WebURL for the [url] link target
// and RawURL for the displayed [img], so the full-size RawURL moves to WebURL and
// the medium ImgURL moves to RawURL. Images without a medium thumbnail are left
// unchanged. The input slice is not mutated.
func swapUTPImageURLs(images []api.ScreenshotImage) []api.ScreenshotImage {
	if len(images) == 0 {
		return images
	}
	swapped := make([]api.ScreenshotImage, len(images))
	for i, image := range images {
		full := strings.TrimSpace(image.RawURL)
		medium := strings.TrimSpace(image.ImgURL)
		if full != "" && medium != "" {
			image.WebURL = full
			image.RawURL = medium
		}
		swapped[i] = image
	}
	return swapped
}
