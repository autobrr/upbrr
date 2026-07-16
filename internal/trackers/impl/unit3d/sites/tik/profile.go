// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tik

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns TIK's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "TIK",
		BaseURL: "https://cinematik.net",
		Rules:   Rules(),
		Site: unit3d.SiteProfile{
			ResolveTypeID:     typeID,
			ResolveCategoryID: categoryID,
		},
	}
}

func typeID(meta api.UploadSubject) string {
	return map[string]string{
		"CUSTOM":    "1",
		"BD100":     "3",
		"BD66":      "4",
		"BD50":      "5",
		"BD25":      "6",
		"NTSC DVD9": "7",
		"NTSC DVD5": "8",
		"PAL DVD9":  "9",
		"PAL DVD5":  "10",
		"3D":        "11",
	}[discType(meta)]
}

func discType(meta api.UploadSubject) string {
	if meta.TrackerSiteOverrides.TIK.DiscType != nil {
		return strings.ToUpper(strings.TrimSpace(*meta.TrackerSiteOverrides.TIK.DiscType))
	}
	if strings.EqualFold(strings.TrimSpace(meta.Is3D), "3D") {
		return "3D"
	}
	releaseName := strings.ToUpper(strings.TrimSpace(meta.ReleaseName))
	source := strings.ToUpper(strings.TrimSpace(meta.Source))
	if source == "" {
		source = strings.ToUpper(strings.TrimSpace(meta.Release.Source))
	}
	combined := releaseName + " " + source
	for _, marker := range []string{"BD100", "BD66", "BD50", "BD25"} {
		if strings.Contains(combined, marker) {
			return marker
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") || strings.Contains(combined, "DVD") {
		if strings.Contains(combined, "PAL") {
			if strings.Contains(combined, "DVD5") {
				return "PAL DVD5"
			}
			return "PAL DVD9"
		}
		if strings.Contains(combined, "NTSC") {
			if strings.Contains(combined, "DVD5") {
				return "NTSC DVD5"
			}
			return "NTSC DVD9"
		}
		if strings.Contains(combined, "DVD5") {
			return "NTSC DVD5"
		}
		return "NTSC DVD9"
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return "CUSTOM"
	}
	return ""
}

func categoryID(meta api.UploadSubject) string {
	category := unit3d.Category(meta)
	foreign, opera, asian := isForeign(meta), isOpera(meta), isAsian(meta)
	if strings.EqualFold(category, "MOVIE") {
		switch {
		case foreign:
			return "3"
		case opera:
			return "5"
		case asian:
			return "6"
		default:
			return "1"
		}
	}
	if strings.EqualFold(category, "TV") {
		switch {
		case foreign:
			return "4"
		case opera:
			return "5"
		default:
			return "2"
		}
	}
	return unit3d.DefaultCategoryID(meta)
}

func isForeign(meta api.UploadSubject) bool {
	if meta.TrackerSiteOverrides.TIK.Foreign != nil {
		return *meta.TrackerSiteOverrides.TIK.Foreign
	}
	if meta.ProviderMetadata.TMDB != nil {
		original := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage))
		if original != "" && original != "en" {
			return true
		}
	}
	return !unit3d.HasEnglishLanguage(meta.AudioLanguages) && !unit3d.HasEnglishLanguage(meta.SubtitleLanguages)
}

func isOpera(meta api.UploadSubject) bool {
	if meta.TrackerSiteOverrides.TIK.Opera != nil {
		return *meta.TrackerSiteOverrides.TIK.Opera
	}
	values := strings.ToLower(
		strings.Join([]string{strings.TrimSpace(meta.Release.Genre), unit3d.TMDBGenres(meta), unit3d.IMDBGenres(meta), unit3d.Keywords(meta)}, ","),
	)
	return strings.Contains(values, "opera") || strings.Contains(values, "musical")
}

func isAsian(meta api.UploadSubject) bool {
	if meta.TrackerSiteOverrides.TIK.Asian != nil {
		return *meta.TrackerSiteOverrides.TIK.Asian
	}
	if meta.ProviderMetadata.TMDB == nil {
		return false
	}
	for _, country := range meta.ProviderMetadata.TMDB.OriginCountry {
		if map[string]bool{
			"JP": true,
			"KR": true,
			"CN": true,
			"HK": true,
			"TW": true,
			"TH": true,
			"VN": true,
			"IN": true,
			"ID": true,
			"MY": true,
			"PH": true,
			"SG": true,
		}[strings.ToUpper(strings.TrimSpace(country))] {
			return true
		}
	}
	return map[string]bool{
		"ja": true,
		"ko": true,
		"zh": true,
		"th": true,
		"vi": true,
		"hi": true,
		"ta": true,
		"te": true,
		"ml": true,
		"id": true,
		"ms": true,
	}[strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage))]
}
