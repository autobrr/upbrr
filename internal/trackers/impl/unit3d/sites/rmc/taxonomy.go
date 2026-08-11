// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	rmcUpscalePattern     = regexp.MustCompile(`(?i)(^|[^[:alnum:]])upscaled?([^[:alnum:]]|$)`)
	rmcRestorationPattern = regexp.MustCompile(`(?i)(^|[^[:alnum:]])restor(?:ation|ed)([^[:alnum:]]|$)`)
	rmcSoundtrackPattern  = regexp.MustCompile(`(?i)(^|[^[:alnum:]])sound[ ._-]*track([^[:alnum:]]|$)`)
	rmcLegacyPattern      = regexp.MustCompile(`(?i)(^|[^[:alnum:]])(?:vhs|laser[ ._-]*disc|woc)([^[:alnum:]]|$)`)
	rmcResolutionPattern  = regexp.MustCompile(`(?:^|[^[:alnum:]])(360p|540p)(?:[^[:alnum:]]|$)`)
)

// typeID maps prepared release facts and RMC-specific labels or name markers
// to the site's type IDs. Soundtrack, restoration, upscale, and legacy-media
// types take precedence over shared Unit3D format inference.
func typeID(meta api.UploadSubject) string {
	discType := rmcValue(meta.DiscType)
	if discType == "" {
		discType = rmcValue(meta.Disc.Type)
	}
	typeValue := rmcValue(meta.Type)
	if typeValue == "" {
		typeValue = rmcValue(meta.Release.Type)
	}
	source := rmcValue(meta.Source)
	if source == "" {
		source = rmcValue(meta.Release.Source)
	}
	name := markerName(meta)
	inferredType := strings.ToUpper(unit3d.InferType(meta))

	switch {
	case typeValue == "SOUNDTRACK" || rmcSoundtrackPattern.MatchString(name):
		return "17"
	case typeValue == "RESTORATION" || typeValue == "RESTORED" || rmcRestorationPattern.MatchString(name):
		return "15"
	case typeValue == "UPSCALE" || typeValue == "UPSCALED" || rmcUpscalePattern.MatchString(name):
		return "14"
	case typeValue == "VHSLDWOC" || typeValue == "VHS" || typeValue == "LD" || typeValue == "WOC" || typeValue == "LASERDISC" ||
		source == "VHS" || source == "LD" || source == "WOC" || source == "LASERDISC" || rmcLegacyPattern.MatchString(name):
		return "12"
	case typeValue == "FULLBLURAY" || discType == "BDMV":
		return "1"
	case typeValue == "BLURAYREMUX" || inferredType == "REMUX" && source == "BLURAY":
		return "2"
	case typeValue == "FULLDVD" || discType == "DVD":
		return "3"
	case typeValue == "DVDREMUX" || inferredType == "REMUX" && (source == "DVD" || source == "PALDVD" || source == "NTSCDVD"):
		return "4"
	case typeValue == "BLURAYENCODE" || inferredType == "ENCODE" && source == "BLURAY":
		return "5"
	case inferredType == "DVDRIP":
		return "6"
	case inferredType == "WEBDL":
		return "7"
	case inferredType == "WEBRIP":
		return "8"
	case typeValue == "UHDTV" || source == "UHDTV":
		return "9"
	case typeValue == "SDTV" || inferredType == "HDTV" && isRMCSDResolution(meta):
		return "11"
	case inferredType == "HDTV":
		return "10"
	default:
		return "0"
	}
}

// resolutionID maps prepared release facts to RMC's resolution IDs.
// Soundtracks default to FLAC; unmatched resolutions use Other RES.
func resolutionID(meta api.UploadSubject) string {
	if typeID(meta) == "17" {
		return "12"
	}
	resolution := rmcResolution(meta)
	if value, ok := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1080p": "3",
		"1080i": "4",
		"720p":  "5",
		"576p":  "6",
		"576i":  "7",
		"480p":  "8",
		"480i":  "9",
		"360p":  "10",
		"540p":  "11",
		"flac":  "12",
	}[resolution]; ok {
		return value
	}
	return "13"
}

// rmcResolution returns the normalized prepared resolution, recovering RMC's
// 360p and 540p markers when shared inference has none.
func rmcResolution(meta api.UploadSubject) string {
	resolution := strings.ToLower(strings.TrimSpace(unit3d.Resolution(meta)))
	if resolution == "" {
		if match := rmcResolutionPattern.FindStringSubmatch(strings.ToLower(markerName(meta))); len(match) > 1 {
			resolution = match[1]
		}
	}
	return resolution
}

// isRMCSDResolution reports whether RMC classifies the prepared resolution as SDTV.
func isRMCSDResolution(meta api.UploadSubject) bool {
	switch rmcResolution(meta) {
	case "360p", "480i", "480p", "540p", "576i", "576p":
		return true
	default:
		return false
	}
}

// rmcValue normalizes tracker labels for case- and separator-insensitive matching.
func rmcValue(value string) string {
	return strings.NewReplacer("-", "", "_", "", " ", "", "/", "").Replace(strings.ToUpper(strings.TrimSpace(value)))
}
