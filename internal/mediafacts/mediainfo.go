// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// MediaInfoDocument is the JSON document shape shared by MediaInfo consumers.
type MediaInfoDocument struct {
	Media struct {
		Track []map[string]any `json:"track"`
	} `json:"media"`
}

var (
	mediaInfoSectionPattern        = regexp.MustCompile(`(?i)^(general|video|audio|text)(?:\s*#\d+)?$`)
	mediaInfoWhitespacePattern     = regexp.MustCompile(`\s+`)
	dolbyVisionNamedProfilePattern = regexp.MustCompile(`(?i)\bprofile(?:\s+id)?[\s:]+(\d+(?:\.\d+)?)`)
	dolbyVisionCodecProfilePattern = regexp.MustCompile(`(?i)\bdvhe\.(\d{2})`)
)

// HDRFromMediaInfoDocument derives authoritative HDR facts from the first
// MediaInfo video track. A usable video track without HDR characteristics is
// complete SDR evidence.
func HDRFromMediaInfoDocument(doc MediaInfoDocument) api.HDRFacts {
	for _, track := range doc.Media.Track {
		if strings.EqualFold(mediaInfoValue(track, "@type"), "video") {
			return hdrFromMediaInfoTrack(track)
		}
	}
	return missingHDRFacts()
}

// HDRFromMediaInfoText derives authoritative HDR facts from a MediaInfo text
// dump. Malformed text or text without a usable video section yields missing
// evidence so callers can apply a weaker fallback.
func HDRFromMediaInfoText(value string) api.HDRFacts {
	track := firstMediaInfoTextVideoTrack(value)
	if len(track) == 0 {
		return missingHDRFacts()
	}
	return hdrFromMediaInfoTrack(track)
}

func firstMediaInfoTextVideoTrack(value string) map[string]any {
	var track map[string]any
	inFirstVideo := false
	for line := range strings.SplitSeq(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(strings.ReplaceAll(strings.TrimSuffix(line, "\r"), "\u00a0", " "))
		if trimmed == "" {
			continue
		}
		if match := mediaInfoSectionPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			if inFirstVideo {
				break
			}
			inFirstVideo = strings.EqualFold(match[1], "video")
			if inFirstVideo {
				track = map[string]any{"@type": "Video"}
			}
			continue
		}
		if !inFirstVideo {
			continue
		}
		key, fieldValue, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = normalizeMediaInfoKey(key)
		fieldValue = strings.TrimSpace(fieldValue)
		if key != "" && fieldValue != "" {
			track[key] = fieldValue
		}
	}
	return track
}

func hdrFromMediaInfoTrack(track map[string]any) api.HDRFacts {
	compatibility := mediaInfoValue(track, "HDR_Format_Compatibility", "HDR format compatibility")
	formatString := mediaInfoValue(track, "HDR_Format_String", "HDR format string")
	formatValue := mediaInfoValue(track, "HDR_Format", "HDR format")
	formatCommercial := mediaInfoValue(track, "HDR_Format_Commercial", "HDR format commercial")
	formatProfile := mediaInfoValue(track, "HDR_Format_Profile", "HDR format profile")
	formatEvidence := strings.Join([]string{compatibility, formatString, formatValue, formatCommercial, formatProfile}, " ")
	upperFormat := strings.ToUpper(formatEvidence)
	transfer := mediaInfoValue(track, "transfer_characteristics", "transfer characteristics", "transfer_characteristics_Original")
	transferUpper := strings.ToUpper(transfer)
	primaries := mediaInfoValue(track, "colour_primaries", "color primaries", "colour primaries", "colour_primaries_Original")
	primariesUpper := strings.ToUpper(primaries)

	facts := api.HDRFacts{
		Origin: api.HDREvidenceMediaInfo,
		Status: api.HDREvidenceComplete,
	}
	addSourceField(&facts.SourceFields, "HDR_Format_Compatibility", compatibility)
	addSourceField(&facts.SourceFields, "HDR_Format_String", formatString)
	addSourceField(&facts.SourceFields, "HDR_Format", formatValue)
	addSourceField(&facts.SourceFields, "HDR_Format_Commercial", formatCommercial)
	addSourceField(&facts.SourceFields, "HDR_Format_Profile", formatProfile)
	addSourceField(&facts.SourceFields, "transfer_characteristics", transfer)
	addSourceField(&facts.SourceFields, "colour_primaries", primaries)

	if strings.Contains(upperFormat, "DOLBY VISION") {
		AddHDRFormat(&facts.Formats, api.HDRFormatDolbyVision)
		facts.DolbyVisionProfile = DolbyVisionProfile(formatEvidence)
	}
	switch {
	case strings.Contains(upperFormat, "HDR10+"):
		AddHDRFormat(&facts.Formats, api.HDRFormatHDR10Plus)
	case strings.Contains(upperFormat, "HDR10"), strings.Contains(upperFormat, "SMPTE ST 2094"):
		AddHDRFormat(&facts.Formats, api.HDRFormatHDR10)
	}
	if strings.Contains(upperFormat, "HLG") || strings.Contains(transferUpper, "HLG") {
		AddHDRFormat(&facts.Formats, api.HDRFormatHLG)
	}
	if strings.Contains(upperFormat, "HDR VIVID") || strings.Contains(upperFormat, "CUVA") {
		AddHDRFormat(&facts.Formats, api.HDRFormatHDRVivid)
	}
	if (primariesUpper == "BT.2020" || primariesUpper == "REC.2020") &&
		strings.TrimSpace(formatEvidence) == "" && strings.Contains(transferUpper, "PQ") {
		AddHDRFormat(&facts.Formats, api.HDRFormatPQ10)
	}
	if len(facts.Formats) == 0 &&
		(strings.Contains(transferUpper, "BT.2020 (10-BIT)") || primariesUpper == "BT.2020" || primariesUpper == "REC.2020") {
		AddHDRFormat(&facts.Formats, api.HDRFormatWCG)
	}
	if len(facts.Formats) == 0 {
		if !usableMediaInfoVideoTrack(track) {
			return missingHDRFacts()
		}
		facts.Formats = []api.HDRFormat{api.HDRFormatSDR}
	}
	AddHDRFallbacks(&facts)
	return facts
}

func mediaInfoValue(track map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := track[key]; ok {
			if trimmed := strings.TrimSpace(fmt.Sprint(value)); trimmed != "" && trimmed != "<nil>" {
				return trimmed
			}
		}
	}
	for rawKey, value := range track {
		normalizedKey := normalizeMediaInfoKey(rawKey)
		for _, key := range keys {
			if normalizedKey != normalizeMediaInfoKey(key) {
				continue
			}
			if trimmed := strings.TrimSpace(fmt.Sprint(value)); trimmed != "" && trimmed != "<nil>" {
				return trimmed
			}
		}
	}
	return ""
}

func normalizeMediaInfoKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", " ")
	return mediaInfoWhitespacePattern.ReplaceAllString(value, " ")
}

func usableMediaInfoVideoTrack(track map[string]any) bool {
	for _, field := range []string{
		"Format",
		"CodecID",
		"Width",
		"Height",
		"BitDepth",
		"HDR_Format",
		"HDR_Format_String",
		"HDR_Format_Compatibility",
		"HDR_Format_Commercial",
		"HDR_Format_Profile",
		"transfer_characteristics",
		"transfer_characteristics_Original",
		"colour_primaries",
		"colour_primaries_Original",
	} {
		if mediaInfoValue(track, field) != "" {
			return true
		}
	}
	return false
}

func missingHDRFacts() api.HDRFacts {
	return api.HDRFacts{
		Origin: api.HDREvidenceUnknown,
		Status: api.HDREvidenceMissing,
	}
}

func addSourceField(fields *[]string, name string, value string) {
	if strings.TrimSpace(value) != "" {
		*fields = append(*fields, name)
	}
}

// DolbyVisionProfile extracts a normalized Dolby Vision profile from MediaInfo or BDInfo text.
func DolbyVisionProfile(value string) string {
	if match := dolbyVisionNamedProfilePattern.FindStringSubmatch(value); len(match) == 2 {
		return strings.TrimLeft(match[1], "0")
	}
	if match := dolbyVisionCodecProfilePattern.FindStringSubmatch(value); len(match) == 2 {
		return strings.TrimLeft(match[1], "0")
	}
	return ""
}

// AddHDRFallbacks derives Dolby Vision compatibility fallbacks from structured formats.
func AddHDRFallbacks(facts *api.HDRFacts) {
	if facts == nil || !slices.Contains(facts.Formats, api.HDRFormatDolbyVision) {
		return
	}
	if slices.Contains(facts.Formats, api.HDRFormatHDR10Plus) {
		AddHDRFormat(&facts.FallbackFormats, api.HDRFormatHDR10Plus)
		AddHDRFormat(&facts.FallbackFormats, api.HDRFormatHDR10)
	} else if slices.Contains(facts.Formats, api.HDRFormatHDR10) {
		AddHDRFormat(&facts.FallbackFormats, api.HDRFormatHDR10)
	}
}

// AddHDRFormat appends a format once.
func AddHDRFormat(formats *[]api.HDRFormat, format api.HDRFormat) {
	if formats != nil && !slices.Contains(*formats, format) {
		*formats = append(*formats, format)
	}
}
