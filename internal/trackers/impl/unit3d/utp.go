// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strconv"
	"strings"

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

	title := strings.TrimSpace(meta.Release.Title)
	aka := strings.TrimSpace(utpAKA(meta))
	year := utpYear(meta.Release.Year)
	threeD := strings.TrimSpace(meta.Is3D)
	uhd := strings.TrimSpace(meta.UHD)
	edition := strings.TrimSpace(meta.Edition)
	hybrid := ""
	if meta.WebDV {
		hybrid = "Hybrid"
	}
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

// utpAKA returns the retrieved AKA string, stored on the TMDB metadata with the
// "AKA " prefix already applied (empty when no AKA was retrieved).
func utpAKA(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return meta.ExternalMetadata.TMDB.RetrievedAKA
	}
	return ""
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
