// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

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
	name = applyHHDTVDBDisambiguation(name, meta)
	name = removeHHDNameElement(name, meta.Edition)
	if isHHDFullDisc(meta) {
		name = insertHHDDiscDistributor(name, meta)
	}
	return strings.Join(strings.Fields(name), " ")
}

func applyHHDTVDBDisambiguation(name string, meta api.UploadSubject) string {
	if unit3d.Category(meta) != "TV" || meta.ProviderMetadata.TVDB == nil {
		return name
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	title, alternate, tail, ok := unit3d.SplitTVDBName(name, meta, evidence)
	if !ok {
		return name
	}
	parts := []string{title, alternate}
	if evidence.IncludeLocale && strings.TrimSpace(evidence.Locale) != "" {
		parts = append(parts, evidence.Locale)
	}
	if evidence.IncludeYear && evidence.SeriesYear > 0 {
		parts = append(parts, strconv.Itoa(evidence.SeriesYear))
	}
	parts = append(parts, tail)
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func removeHHDNameElement(name string, element string) string {
	element = strings.Join(strings.Fields(element), " ")
	index := findHHDLastNameElement(name, element)
	if index < 0 {
		return name
	}
	return strings.TrimSpace(name[:index] + " " + name[index+len(element):])
}

func insertHHDDiscDistributor(name string, meta api.UploadSubject) string {
	distributor := strings.Join(strings.Fields(meta.Distributor), " ")
	if distributor == "" || findHHDNameElement(name, distributor) >= 0 {
		return name
	}
	if resolution := strings.Join(strings.Fields(unit3d.Resolution(meta)), " "); resolution != "" {
		if index := findHHDLastNameElement(name, resolution); index >= 0 {
			end := index + len(resolution)
			return strings.TrimSpace(name[:end] + " " + distributor + " " + name[end:])
		}
	}
	if region := strings.Join(strings.Fields(meta.Region), " "); region != "" {
		if index := findHHDLastNameElement(name, region); index >= 0 {
			return strings.TrimSpace(name[:index] + distributor + " " + name[index:])
		}
	}
	return name
}

func isHHDFullDisc(meta api.UploadSubject) bool {
	nameType := strings.TrimSpace(meta.Type)
	return strings.EqualFold(nameType, "DISC") || nameType == "" && unit3d.IsDiscType(meta.DiscType)
}

func findHHDNameElement(value string, element string) int {
	element = strings.Join(strings.Fields(element), " ")
	if element == "" {
		return -1
	}
	return strings.Index(" "+value+" ", " "+element+" ")
}

func findHHDLastNameElement(value string, element string) int {
	element = strings.Join(strings.Fields(element), " ")
	if element == "" {
		return -1
	}
	return strings.LastIndex(" "+value+" ", " "+element+" ")
}
