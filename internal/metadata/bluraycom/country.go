// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"strings"
	"unicode"
)

var countryRegionCodes = map[string]string{
	"Argentina":            "ARG",
	"Australia":            "AUS",
	"Austria":              "AUT",
	"Belgium":              "BEL",
	"Brazil":               "BRA",
	"Canada":               "CAN",
	"Chile":                "CHI",
	"China":                "CHN",
	"Czech Republic":       "CZE",
	"Denmark":              "DEN",
	"Finland":              "FIN",
	"France":               "FRA",
	"Germany":              "GER",
	"Greece":               "GRE",
	"Hong Kong":            "HKG",
	"Hungary":              "HUN",
	"India":                "IND",
	"Ireland":              "IRL",
	"Italy":                "ITA",
	"Japan":                "JPN",
	"Mexico":               "MEX",
	"Netherlands":          "NLD",
	"New Zealand":          "NZL",
	"Norway":               "NOR",
	"Poland":               "POL",
	"Portugal":             "POR",
	"Russia":               "RUS",
	"Singapore":            "SIN",
	"South Africa":         "ZAF",
	"South Korea":          "KOR",
	"Spain":                "ESP",
	"Sweden":               "SWE",
	"Switzerland":          "SUI",
	"Taiwan":               "TWN",
	"Thailand":             "THA",
	"United Kingdom":       "GBR",
	"United States":        "USA",
	"United Arab Emirates": "UAE",
	"Vietnam":              "VIE",
}

func countryToRegion(country string) string {
	trimmed := strings.TrimSpace(country)
	if trimmed == "" || strings.EqualFold(trimmed, "Unknown") {
		return ""
	}
	if code := countryRegionCodes[trimmed]; code != "" {
		return code
	}
	letters := make([]rune, 0, 3)
	for _, r := range strings.ToUpper(trimmed) {
		if unicode.IsLetter(r) {
			letters = append(letters, r)
			if len(letters) == 3 {
				break
			}
		}
	}
	return string(letters)
}
