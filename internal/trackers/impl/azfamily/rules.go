// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules returns the profile's country, age, and technical eligibility checks.
// Country routing and blocking, PHD SD content, and H.264 above 1080p are
// strict failures; remaining failures are waivable.
func (d *Definition) Rules() *trackers.RuleSet {
	return &trackers.RuleSet{Check: d.evaluateRules}
}

func (d *Definition) evaluateRules(ctx context.Context, meta api.RuleSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0)
	add := func(rule, reason string) {
		failures = append(failures, trackers.NewRuleFailure(rule, reason, api.RuleDispositionWaivable))
	}
	addStrict := func(rule, reason string) {
		failures = append(failures, trackers.NewRuleFailure(rule, reason, api.RuleDispositionStrict))
	}
	category := strings.ToUpper(strings.TrimSpace(trackers.ResolveRuleCategory(meta)))
	if category != "MOVIE" && category != "TV" {
		add("content_type", "only movies and TV shows are allowed")
	}
	if meta.Anime {
		add("anime_redirect", "anime should be uploaded to AnimeTorrents instead")
	}
	origin := originCountries(meta)
	switch d.site.Name {
	case "AZ":
		if intersects(origin, phdCountries()) {
			addStrict("country_redirect", "major English-language content belongs on PrivateHD")
		} else if intersects(origin, cinemaZCountries()) {
			addStrict("country_redirect", "non-Asian western content belongs on CinemaZ")
		}
	case "CZ":
		switch {
		case intersects(origin, phdCountries()) && !isOlderThan50Years(meta):
			addStrict("country_redirect", "recent mainstream English content belongs on PrivateHD")
		case intersects(origin, azCountries()):
			addStrict("country_redirect", "Asian content belongs on AvistaZ")
		case len(origin) > 0 && !intersects(origin, czAllowedCountries()):
			addStrict("country_block", "content origin is outside CinemaZ allowed regions")
		}
	case "PHD":
		if isOlderThan50Years(meta) {
			add("age_redirect", "50+ year-old content belongs on CinemaZ")
		}
		switch {
		case intersects(origin, cinemaZCountries()):
			addStrict("country_redirect", "European, South American, and African content belongs on CinemaZ")
		case intersects(origin, azCountries()):
			addStrict("country_redirect", "Asian content belongs on AvistaZ")
		case len(origin) > 0 && !intersects(origin, phdCountries()):
			addStrict("country_block", "PrivateHD only allows major English-language territories")
		}
		evaluatePHDTechnicalRules(meta, add, addStrict)
	}
	return failures, nil
}

func evaluatePHDTechnicalRules(meta api.RuleSubject, add func(string, string), addStrict func(string, string)) {
	resolution := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
	if resolution == "480p" || resolution == "576p" || resolution == "480i" || resolution == "576i" {
		addStrict("sd_forbidden", "SD content is forbidden")
	}
	if !trackers.IsDiscType(meta.DiscType) {
		container := strings.ToLower(strings.TrimSpace(meta.Container))
		if container != "" && container != "mkv" && container != "mp4" {
			add("container", "allowed containers: MKV, MP4")
		}
	}
	group := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-"))
	switch group {
	case "RARBG", "FGT", "GRYM", "TBS":
		add("group_block", "RARBG, FGT, Grym, and TBS are not allowed")
	}
	if group == "EVO" && !strings.EqualFold(strings.TrimSpace(meta.Source), "WEB") {
		add("group_block", "non-web EVO releases are not allowed")
	}
	codec := strings.ToLower(strings.TrimSpace(meta.VideoCodec))
	encode := strings.ToLower(strings.TrimSpace(meta.VideoEncode))
	releaseType := strings.ToLower(strings.TrimSpace(trackers.ResolveRuleType(meta)))
	source := strings.ToLower(strings.TrimSpace(meta.Source))
	if releaseType == "remux" && codec != "" && codec != "mpeg-2" && codec != "vc-1" && codec != "h.264" && codec != "h.265" && codec != "avc" {
		add("video_codec", "BluRay remuxes require MPEG-2, VC-1, H.264, or H.265")
	}
	if releaseType == "encode" && strings.Contains(source, "bluray") && encode != "" && encode != "h.264" && encode != "h.265" && encode != "x264" &&
		encode != "x265" {
		add("video_encode", "BluRay encodes require H.264/H.265 with x264/x265")
	}
	if (releaseType == "webdl" || releaseType == "web-dl") && source == "web" && encode != "" && encode != "h.264" && encode != "h.265" && encode != "vp9" {
		add("video_encode", "WEB-DL requires H.264, H.265, or VP9")
	}
	if releaseType == "encode" && source == "web" && encode != "" && encode != "h.264" && encode != "h.265" && encode != "x264" && encode != "x265" {
		add("video_encode", "WEB encodes require H.264/H.265 with x264/x265")
	}
	if releaseType == "encode" && encode == "x265" && strings.TrimSpace(meta.BitDepth) != "10" {
		add("bit_depth", "x265 encodes must be 10-bit")
	}
	if res := trackers.ResolveRuleResolution(meta); strings.HasSuffix(res, "p") {
		if height, err := strconv.Atoi(strings.TrimSuffix(res, "p")); err == nil && height > 1080 && (encode == "h.264" || encode == "x264") {
			addStrict("h264_resolution_limit", "H.264/x264 is only allowed for 1080p and below")
		}
	}
}

func originCountries(meta api.RuleSubject) []string {
	if meta.ProviderMetadata.TMDB != nil {
		return meta.ProviderMetadata.TMDB.OriginCountry
	}
	return nil
}

func isOlderThan50Years(meta api.RuleSubject) bool {
	year := meta.Release.Year
	if year == 0 && meta.ProviderMetadata.TMDB != nil {
		year = meta.ProviderMetadata.TMDB.Year
	}
	return year > 0 && time.Now().UTC().Year()-year >= 50
}

func intersects(left []string, right map[string]struct{}) bool {
	for _, value := range left {
		if _, ok := right[strings.ToUpper(strings.TrimSpace(value))]; ok {
			return true
		}
	}
	return false
}

func countrySet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func azCountries() map[string]struct{} {
	return countrySet(
		"BD",
		"BN",
		"BT",
		"CN",
		"HK",
		"ID",
		"IN",
		"JP",
		"KH",
		"KP",
		"KR",
		"LA",
		"LK",
		"MM",
		"MN",
		"MO",
		"MY",
		"NP",
		"PH",
		"PK",
		"SG",
		"TH",
		"TL",
		"TW",
		"VN",
	)
}
func phdCountries() map[string]struct{} {
	return countrySet(
		"AG",
		"AI",
		"AU",
		"BB",
		"BM",
		"BS",
		"BZ",
		"CA",
		"CW",
		"DM",
		"GB",
		"GD",
		"IE",
		"JM",
		"KN",
		"KY",
		"LC",
		"MS",
		"NZ",
		"PR",
		"TC",
		"TT",
		"US",
		"VC",
		"VG",
		"VI",
	)
}

func cinemaZCountries() map[string]struct{} {
	all := countrySet(
		"AO",
		"BF",
		"BI",
		"BJ",
		"BW",
		"CD",
		"CF",
		"CG",
		"CI",
		"CM",
		"CV",
		"DJ",
		"DZ",
		"EG",
		"EH",
		"ER",
		"ET",
		"GA",
		"GH",
		"GM",
		"GN",
		"GQ",
		"GW",
		"IO",
		"KE",
		"KM",
		"LR",
		"LS",
		"LY",
		"MA",
		"MG",
		"ML",
		"MR",
		"MU",
		"MW",
		"MZ",
		"NA",
		"NE",
		"NG",
		"RE",
		"RW",
		"SC",
		"SD",
		"SH",
		"SL",
		"SN",
		"SO",
		"SS",
		"ST",
		"SZ",
		"TD",
		"TF",
		"TG",
		"TN",
		"TZ",
		"UG",
		"YT",
		"ZA",
		"ZM",
		"ZW",
		"AG",
		"AI",
		"AR",
		"AW",
		"BB",
		"BL",
		"BM",
		"BO",
		"BQ",
		"BR",
		"BS",
		"BV",
		"BZ",
		"CA",
		"CL",
		"CO",
		"CR",
		"CU",
		"CW",
		"DM",
		"DO",
		"EC",
		"FK",
		"GD",
		"GF",
		"GL",
		"GP",
		"GS",
		"GT",
		"GY",
		"HN",
		"HT",
		"JM",
		"KN",
		"KY",
		"LC",
		"MF",
		"MQ",
		"MS",
		"MX",
		"NI",
		"PA",
		"PE",
		"PM",
		"PR",
		"PY",
		"SR",
		"SV",
		"SX",
		"TC",
		"TT",
		"US",
		"UY",
		"VC",
		"VE",
		"VG",
		"VI",
		"AD",
		"AL",
		"AT",
		"AX",
		"BA",
		"BE",
		"BG",
		"BY",
		"CH",
		"CZ",
		"DE",
		"DK",
		"EE",
		"ES",
		"FI",
		"FO",
		"FR",
		"GB",
		"GG",
		"GI",
		"GR",
		"HR",
		"HU",
		"IE",
		"IM",
		"IS",
		"IT",
		"JE",
		"LI",
		"LT",
		"LU",
		"LV",
		"MC",
		"MD",
		"ME",
		"MK",
		"MT",
		"NL",
		"NO",
		"PL",
		"PT",
		"RO",
		"RS",
		"RU",
		"SE",
		"SI",
		"SJ",
		"SK",
		"SM",
		"SU",
		"UA",
		"VA",
		"XC",
		"AS",
		"AU",
		"CC",
		"CK",
		"CX",
		"FJ",
		"FM",
		"GU",
		"HM",
		"KI",
		"MH",
		"MP",
		"NC",
		"NF",
		"NR",
		"NU",
		"NZ",
		"PF",
		"PG",
		"PN",
		"PW",
		"SB",
		"TK",
		"TO",
		"TV",
		"UM",
		"VU",
		"WF",
		"WS",
	)
	for code := range phdCountries() {
		delete(all, code)
	}
	for code := range azCountries() {
		delete(all, code)
	}
	return all
}

func czAllowedCountries() map[string]struct{} {
	return countrySet(
		"AD",
		"AL",
		"AT",
		"AX",
		"BA",
		"BE",
		"BG",
		"BY",
		"CH",
		"CZ",
		"DE",
		"DK",
		"EE",
		"ES",
		"FI",
		"FO",
		"FR",
		"GI",
		"GR",
		"HR",
		"HU",
		"IS",
		"IT",
		"LI",
		"LT",
		"LU",
		"LV",
		"MC",
		"MD",
		"ME",
		"MK",
		"MT",
		"NL",
		"NO",
		"PL",
		"PT",
		"RO",
		"RS",
		"RU",
		"SE",
		"SI",
		"SJ",
		"SK",
		"SM",
		"SU",
		"UA",
		"VA",
		"XC",
		"AG",
		"AI",
		"AR",
		"AW",
		"BL",
		"BO",
		"BQ",
		"BR",
		"BV",
		"CL",
		"CO",
		"CU",
		"DO",
		"EC",
		"FK",
		"GF",
		"GL",
		"GP",
		"GS",
		"GT",
		"GY",
		"HN",
		"HT",
		"MF",
		"MQ",
		"MX",
		"NI",
		"PA",
		"PE",
		"PM",
		"PY",
		"SR",
		"SV",
		"SX",
		"UY",
		"VE",
		"AO",
		"BF",
		"BI",
		"BJ",
		"BW",
		"CD",
		"CF",
		"CG",
		"CI",
		"CM",
		"CV",
		"DJ",
		"DZ",
		"EG",
		"EH",
		"ER",
		"ET",
		"GA",
		"GH",
		"GM",
		"GN",
		"GQ",
		"GW",
		"IO",
		"KE",
		"KM",
		"LR",
		"LS",
		"LY",
		"MA",
		"MG",
		"ML",
		"MR",
		"MU",
		"MW",
		"MZ",
		"NA",
		"NE",
		"NG",
		"RE",
		"RW",
		"SC",
		"SD",
		"SH",
		"SL",
		"SN",
		"SO",
		"SS",
		"ST",
		"SZ",
		"TD",
		"TF",
		"TG",
		"TN",
		"TZ",
		"UG",
		"YT",
		"ZA",
		"ZM",
		"ZW",
		"AE",
		"BH",
		"CY",
		"IR",
		"IQ",
		"IL",
		"JO",
		"KW",
		"LB",
		"OM",
		"PS",
		"QA",
		"SA",
		"SY",
		"TR",
		"YE",
	)
}
