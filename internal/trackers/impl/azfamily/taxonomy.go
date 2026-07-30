// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func category(meta api.UploadSubject) string {
	value, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return string(value)
}

func categoryID(meta api.UploadSubject) string {
	switch strings.ToUpper(strings.TrimSpace(category(meta))) {
	case "MOVIE":
		return "1"
	case "TV":
		return "2"
	default:
		return ""
	}
}

func categorySlug(meta api.UploadSubject) string {
	switch strings.ToUpper(category(meta)) {
	case "TV":
		return "tv"
	case "MOVIE":
		return "movie"
	default:
		return ""
	}
}

func isTV(meta api.UploadSubject) bool {
	return strings.EqualFold(category(meta), "TV")
}

func detectResolution(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range []string{"4320p", "2160p", "1080p", "1080i", "720p", "576p", "576i", "480p", "480i"} {
		if strings.Contains(lower, candidate) {
			return candidate
		}
	}
	return ""
}

func resolutionValue(meta api.UploadSubject) string {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if resolution == "" {
		resolution = detectResolution(meta.ReleaseName)
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") && resolution != "" {
		height := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(resolution, "p"), "i"))
		if value, err := strconv.Atoi(height); err == nil && value > 0 {
			return fmt.Sprintf("%dx%d", int(float64(value)*16.0/9.0+0.5), value)
		}
	}
	return resolution
}

func videoQualityID(site siteDefinition, meta api.UploadSubject) string {
	resolution := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
	if resolution == "" {
		resolution = strings.ToLower(detectResolution(meta.ReleaseName))
	}
	if site.Name != "PHD" {
		resolutionInt, _ := strconv.Atoi(strings.NewReplacer("p", "", "i", "").Replace(resolution))
		if resolutionInt > 0 && resolutionInt < 720 {
			return "1"
		}
	}
	switch resolution {
	case "720p":
		return "2"
	case "1080p":
		return "3"
	case "2160p":
		return "6"
	case "1080i":
		return "7"
	case "4320p":
		return "8"
	default:
		return "0"
	}
}

func ripTypeName(meta api.UploadSubject) string {
	typeValue := strings.ToLower(strings.TrimSpace(meta.Type))
	source := strings.ToLower(strings.TrimSpace(meta.Source))
	discType := strings.ToLower(strings.TrimSpace(meta.DiscType))
	if typeValue == "disc" {
		switch discType {
		case "bdmv":
			return "BluRay Raw"
		case "dvd", "hddvd":
			return "DVD"
		}
	}
	if typeValue == "remux" {
		if strings.Contains(source, "dvd") {
			return "DVD Remux"
		}
		if strings.Contains(source, "blu") {
			return "BluRay REMUX"
		}
	}
	switch typeValue {
	case "bdrip":
		return "BDRip"
	case "encode":
		return "BluRay"
	case "brrip":
		return "BRRip"
	case "dvdrip":
		return "DVDRip"
	case "hdrip":
		return "HDRip"
	case "hdtv":
		return "HDTV"
	case "sdtv":
		return "SDTV"
	case "vcd":
		return "VCD"
	case "vcdrip":
		return "VCDRip"
	case "vhsrip":
		return "VHSRip"
	case "vodrip":
		return "VODRip"
	case "webdl":
		return "WEB-DL"
	case "webrip":
		return "WEBRip"
	default:
		return ""
	}
}

func ripTypeID(meta api.UploadSubject) string {
	switch ripTypeName(meta) {
	case "BDRip":
		return "1"
	case "BluRay":
		return "2"
	case "BRRip":
		return "3"
	case "DVD":
		return "4"
	case "DVDRip":
		return "5"
	case "HDRip":
		return "6"
	case "HDTV":
		return "7"
	case "VCD":
		return "8"
	case "VCDRip":
		return "9"
	case "VHSRip":
		return "10"
	case "VODRip":
		return "11"
	case "WEB-DL":
		return "12"
	case "WEBRip":
		return "13"
	case "BluRay REMUX":
		return "14"
	case "BluRay Raw":
		return "15"
	case "SDTV":
		return "16"
	case "DVD Remux":
		return "17"
	default:
		return "0"
	}
}
