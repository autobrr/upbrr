// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func siteLTProfile() unit3DSiteProfile {
	return unit3DSiteProfile{
		resolveCategoryID: resolveUnit3DLTCategoryID,
	}
}

var ltAsianCountries = map[string]bool{
	"AE": true, "AF": true, "AM": true, "AZ": true, "BD": true, "BH": true, "BN": true, "BT": true, "CN": true, "CY": true, "GE": true, "HK": true, "ID": true, "IL": true, "IN": true,
	"IQ": true, "IR": true, "JO": true, "JP": true, "KG": true, "KH": true, "KP": true, "KR": true, "KW": true, "KZ": true, "LA": true, "LB": true, "LK": true, "MM": true, "MN": true,
	"MO": true, "MV": true, "MY": true, "NP": true, "OM": true, "PH": true, "PK": true, "PS": true, "QA": true, "SA": true, "SG": true, "SY": true, "TH": true, "TJ": true, "TL": true,
	"TM": true, "TR": true, "TW": true, "UZ": true, "VN": true, "YE": true,
}

func resolveUnit3DLTCategoryID(meta api.PreparedMetadata) string {
	category := resolveUnit3DCategory(meta)

	categoryID := "1" // Default MOVIE
	if category == "TV" {
		categoryID = "2" // Default TV
	}

	if category == "TV" {
		if meta.Anime {
			return "5"
		}

		keywords := ""
		overview := ""
		genres := ""
		var originCountries []string

		if meta.ExternalMetadata.TMDB != nil {
			keywords = strings.ToLower(meta.ExternalMetadata.TMDB.Keywords)
			overview = strings.ToLower(meta.ExternalMetadata.TMDB.Overview)
			genres = strings.ToLower(meta.ExternalMetadata.TMDB.Genres)
			originCountries = meta.ExternalMetadata.TMDB.OriginCountry
		}

		soapKeywords := []string{"telenovela", "novela", "soap", "culebrón", "culebron"}
		hasSoap := false
		for _, kw := range soapKeywords {
			if strings.Contains(keywords, kw) || strings.Contains(overview, kw) {
				hasSoap = true
				break
			}
		}
		if hasSoap {
			return "8"
		}

		hasAsianCountry := false
		for _, c := range originCountries {
			if ltAsianCountries[strings.ToUpper(c)] {
				hasAsianCountry = true
				break
			}
		}

		if strings.Contains(genres, "drama") && hasAsianCountry {
			return "20"
		}
	}

	return categoryID
}
