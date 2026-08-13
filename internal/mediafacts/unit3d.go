// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// HDRFromUnit3DHDRDV normalizes one present Unit3D hdr_dv enum value.
// An empty value is authoritative SDR; unknown values remain partial.
func HDRFromUnit3DHDRDV(value string) api.HDRFacts {
	profile, formats, ok := parseUnit3DHDRDV(value)
	facts := api.HDRFacts{
		DolbyVisionProfile: profile,
		Origin:             api.HDREvidenceTrackerAPI,
		Status:             api.HDREvidenceComplete,
		SourceFields:       []string{"hdr_dv"},
	}
	if !ok {
		facts.Status = api.HDREvidencePartial
		return facts
	}
	facts.Formats = formats
	AddHDRFallbacks(&facts)
	return facts
}

// Unit3DHDRDVFromFacts returns the exact Unit3D hdr_dv enum value represented
// by complete HDR facts. Unsupported or ambiguous facts are not projected.
func Unit3DHDRDVFromFacts(facts api.HDRFacts) (string, bool) {
	if facts.Status != api.HDREvidenceComplete {
		return "", false
	}
	formats := make([]api.HDRFormat, 0, len(facts.Formats))
	for _, format := range facts.Formats {
		AddHDRFormat(&formats, format)
	}
	if slices.Equal(formats, []api.HDRFormat{api.HDRFormatSDR}) {
		return "", true
	}
	if !slices.Contains(formats, api.HDRFormatDolbyVision) {
		if len(formats) != 1 {
			return "", false
		}
		switch formats[0] {
		case api.HDRFormatHDR10:
			return "HDR10", true
		case api.HDRFormatHDR10Plus:
			return "HDR10+", true
		case api.HDRFormatHDRVivid:
			return "HDR Vivid", true
		case api.HDRFormatHLG:
			return "HLG", true
		case api.HDRFormatPQ10:
			return "PQ10", true
		case api.HDRFormatSDR, api.HDRFormatDolbyVision, api.HDRFormatWCG:
			return "", false
		}
	}

	profile := unit3DDolbyVisionProfile(facts.DolbyVisionProfile)
	if profile == "" {
		return "", false
	}
	compatibility := make([]api.HDRFormat, 0, len(formats)-1)
	for _, format := range formats {
		if format != api.HDRFormatDolbyVision {
			compatibility = append(compatibility, format)
		}
	}
	value := "DV P" + profile
	switch {
	case len(compatibility) == 0 && (profile == "5" || profile == "20"):
	case slices.Equal(compatibility, []api.HDRFormat{api.HDRFormatHDR10}) && slices.Contains([]string{"7", "8", "10"}, profile):
		value += " HDR"
	case slices.Equal(compatibility, []api.HDRFormat{api.HDRFormatHDR10Plus}) && slices.Contains([]string{"7", "8", "10"}, profile):
		value += " HDR10+"
	case slices.Equal(compatibility, []api.HDRFormat{api.HDRFormatHDRVivid}) && slices.Contains([]string{"5", "7", "8", "10", "20"}, profile):
		value += " HDR Vivid"
	default:
		return "", false
	}
	return value, true
}

func parseUnit3DHDRDV(value string) (string, []api.HDRFormat, bool) {
	normalized := strings.ToUpper(strings.Join(strings.Fields(value), " "))
	switch normalized {
	case "":
		return "", []api.HDRFormat{api.HDRFormatSDR}, true
	case "HDR10":
		return "", []api.HDRFormat{api.HDRFormatHDR10}, true
	case "HDR10+":
		return "", []api.HDRFormat{api.HDRFormatHDR10Plus}, true
	case "HDR VIVID":
		return "", []api.HDRFormat{api.HDRFormatHDRVivid}, true
	case "HLG":
		return "", []api.HDRFormat{api.HDRFormatHLG}, true
	case "PQ10":
		return "", []api.HDRFormat{api.HDRFormatPQ10}, true
	}

	parts := strings.Fields(normalized)
	if len(parts) < 2 || parts[0] != "DV" || !strings.HasPrefix(parts[1], "P") {
		return "", nil, false
	}
	profile := strings.TrimPrefix(parts[1], "P")
	suffix := strings.Join(parts[2:], " ")
	formats := []api.HDRFormat{api.HDRFormatDolbyVision}
	switch {
	case suffix == "" && (profile == "5" || profile == "20"):
	case suffix == "HDR" && slices.Contains([]string{"7", "8", "10"}, profile):
		formats = append(formats, api.HDRFormatHDR10)
	case suffix == "HDR10+" && slices.Contains([]string{"7", "8", "10"}, profile):
		formats = append(formats, api.HDRFormatHDR10Plus)
	case suffix == "HDR VIVID" && slices.Contains([]string{"5", "7", "8", "10", "20"}, profile):
		formats = append(formats, api.HDRFormatHDRVivid)
	default:
		return "", nil, false
	}
	return profile, formats, true
}

func unit3DDolbyVisionProfile(value string) string {
	value = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(value)), "P")
	if major, _, ok := strings.Cut(value, "."); ok {
		value = major
	}
	value = strings.TrimLeft(value, "0")
	if slices.Contains([]string{"5", "7", "8", "10", "20"}, value) {
		return value
	}
	return ""
}
