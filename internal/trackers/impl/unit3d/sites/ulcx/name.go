// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	name = strings.Join(strings.Fields(name), " ")
	name = applyULCXTVDBDisambiguation(name, meta)
	name = removeULCXNameElement(name, meta.Edition)
	if isULCXFullDisc(meta) {
		name = insertULCXDiscDistributor(name, meta)
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") &&
		(strings.Contains(strings.ToLower(strings.TrimSpace(meta.Edition)), "hybrid") || meta.WebDV) {
		name = strings.Replace(name, "Hybrid ", "", 1)
	}
	name = correctULCXX265Token(name, meta)
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func applyULCXTVDBDisambiguation(name string, meta api.UploadSubject) string {
	if unit3d.Category(meta) != "TV" ||
		!meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) ||
		meta.ProviderMetadata.TVDB == nil {
		return name
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	title, alternate, tail, ok := unit3d.SplitTVDBName(name, meta, evidence)
	if !ok {
		return name
	}
	locale := ""
	if evidence.IncludeLocale {
		locale = strings.TrimSpace(evidence.Locale)
	}
	parts := []string{title, alternate, locale}
	if evidence.IncludeYear && evidence.SeriesYear > 0 && locale == "" {
		parts = append(parts, strconv.Itoa(evidence.SeriesYear))
	}
	parts = append(parts, tail)
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func removeULCXNameElement(name string, element string) string {
	element = strings.Join(strings.Fields(element), " ")
	index := findULCXLastNameElement(name, element)
	if index < 0 {
		return name
	}
	return strings.TrimSpace(name[:index] + " " + name[index+len(element):])
}

func insertULCXDiscDistributor(name string, meta api.UploadSubject) string {
	distributor := strings.Join(strings.Fields(meta.Distributor), " ")
	if distributor == "" || findULCXNameElement(name, distributor) >= 0 {
		return name
	}
	if resolution := strings.Join(strings.Fields(unit3d.Resolution(meta)), " "); resolution != "" {
		if index := findULCXLastNameElement(name, resolution); index >= 0 {
			end := index + len(resolution)
			return strings.TrimSpace(name[:end] + " " + distributor + " " + name[end:])
		}
	}
	if region := strings.Join(strings.Fields(meta.Region), " "); region != "" {
		if index := findULCXLastNameElement(name, region); index >= 0 {
			return strings.TrimSpace(name[:index] + distributor + " " + name[index:])
		}
	}
	return name
}

func correctULCXX265Token(name string, meta api.UploadSubject) string {
	if !strings.EqualFold(strings.TrimSpace(meta.VideoEncode), "x265") {
		return name
	}
	if findULCXNameElement(name, "x265") >= 0 {
		return name
	}
	for _, stale := range []string{"H.265", "HEVC"} {
		if index := findULCXLastNameElement(name, stale); index >= 0 {
			return name[:index] + "x265" + name[index+len(stale):]
		}
		if index := strings.LastIndex(name, " "+stale+"-"); index >= 0 {
			start := index + 1
			return name[:start] + "x265" + name[start+len(stale):]
		}
	}
	return name
}

func isULCXFullDisc(meta api.UploadSubject) bool {
	nameType := strings.TrimSpace(meta.Type)
	return strings.EqualFold(nameType, "DISC") || nameType == "" && unit3d.IsDiscType(meta.DiscType)
}

func findULCXNameElement(value string, element string) int {
	element = strings.Join(strings.Fields(element), " ")
	if element == "" {
		return -1
	}
	return strings.Index(" "+value+" ", " "+element+" ")
}

func findULCXLastNameElement(value string, element string) int {
	element = strings.Join(strings.Fields(element), " ")
	if element == "" {
		return -1
	}
	return strings.LastIndex(" "+value+" ", " "+element+" ")
}
