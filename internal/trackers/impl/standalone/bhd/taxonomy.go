// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCategoryID(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(string(meta.Identity.Category)), "TV") {
		return "2"
	}
	return "1"
}

func resolveSource(meta api.UploadSubject) (string, bool) {
	return SourceForMetadata(meta)
}

func resolveType(meta api.UploadSubject) string {
	return Type(meta)
}

func resolveEdition(meta api.UploadSubject, tags []string) (bool, string) {
	edition := strings.TrimSpace(meta.Edition)
	if slices.Contains(tags, "Hybrid") {
		edition = strings.TrimSpace(strings.ReplaceAll(edition, "Hybrid", ""))
	}
	if edition == "" {
		return false, ""
	}
	for _, token := range []string{"collector", "director", "extended", "limited", "special", "theatrical", "uncut", "unrated"} {
		if strings.Contains(strings.ToLower(edition), token) {
			switch token {
			case "director":
				return false, "Director"
			default:
				return false, strings.ToUpper(token[:1]) + token[1:]
			}
		}
	}
	return true, edition
}

func resolveTags(meta api.UploadSubject) []string {
	tags := make([]string, 0, 12)
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "WEBRIP":
		tags = append(tags, "WEBRip")
	case "WEBDL", "WEB-DL":
		tags = append(tags, "WEBDL")
	}
	if strings.EqualFold(strings.TrimSpace(meta.Is3D), "3D") {
		tags = append(tags, "3D")
	}
	audio := strings.ToLower(strings.TrimSpace(meta.Audio))
	if strings.Contains(audio, "dual-audio") {
		tags = append(tags, "DualAudio")
	}
	if strings.Contains(audio, "dubbed") {
		tags = append(tags, "EnglishDub")
	}
	if strings.Contains(strings.ToLower(meta.Edition), "open matte") {
		tags = append(tags, "OpenMatte")
	}
	if meta.Scene {
		tags = append(tags, "Scene")
	}
	if meta.PersonalRelease {
		tags = append(tags, "Personal")
	}
	if strings.Contains(strings.ToLower(meta.Edition), "hybrid") {
		tags = append(tags, "Hybrid")
	}
	if meta.HasCommentary {
		tags = append(tags, "Commentary")
	}
	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	if strings.Contains(hdr, "DV") {
		tags = append(tags, "DV")
	}
	if strings.Contains(hdr, "HDR") {
		if strings.Contains(hdr, "HDR10+") {
			tags = append(tags, "HDR10+")
		} else {
			tags = append(tags, "HDR10")
		}
	}
	if strings.Contains(hdr, "HLG") {
		tags = append(tags, "HLG")
	}
	return dedupeStrings(tags)
}

func resolveRegion(region string) string {
	allowed := map[string]struct{}{
		"AUS": {},
		"CAN": {},
		"CEE": {},
		"CHN": {},
		"ESP": {},
		"EUR": {},
		"FRA": {},
		"GBR": {},
		"GER": {},
		"HKG": {},
		"ITA": {},
		"JPN": {},
		"KOR": {},
		"NOR": {},
		"NLD": {},
		"RUS": {},
		"TWN": {},
		"USA": {},
	}
	upper := strings.ToUpper(strings.TrimSpace(region))
	if _, ok := allowed[upper]; ok {
		return upper
	}
	return ""
}

func isSD(meta api.UploadSubject) bool {
	return IsSD(meta)
}

func boolFlag(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
