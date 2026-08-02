// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"slices"
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
	name = applyLumeTVDBDisambiguation(name, meta)
	if !isLumeFullDisc(meta) {
		name = removeLumeNameElement(name, meta.Edition)
	}
	if meta.HDRFacts.Status != "" && meta.HDRFacts.Status != api.HDREvidenceMissing {
		name = replaceLumeHDR(name, meta.HDR, lumeHDR(meta.HDRFacts))
	}
	fields := strings.Fields(name)
	fields = slices.DeleteFunc(fields, func(field string) bool {
		return strings.EqualFold(field, "Hybrid") || strings.EqualFold(field, "Hi10P")
	})
	return strings.Join(fields, " ")
}

func applyLumeTVDBDisambiguation(name string, meta api.UploadSubject) string {
	if unit3d.Category(meta) != "TV" || meta.ProviderMetadata.TVDB == nil {
		return name
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	title, alternateAndLocale, tail, ok := unit3d.SplitTVDBName(name, meta, evidence)
	if !ok {
		return name
	}
	parts := []string{title, alternateAndLocale}
	if evidence.IncludeYear && evidence.SeriesYear > 0 {
		parts = append(parts, strconv.Itoa(evidence.SeriesYear))
	}
	parts = append(parts, tail)
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func removeLumeNameElement(name string, element string) string {
	element = strings.Join(strings.Fields(element), " ")
	index := findLumeLastNameElement(name, element)
	if index < 0 {
		return name
	}
	return strings.TrimSpace(name[:index] + " " + name[index+len(element):])
}

func isLumeFullDisc(meta api.UploadSubject) bool {
	nameType := strings.TrimSpace(meta.Type)
	return strings.EqualFold(nameType, "DISC") || nameType == "" && unit3d.IsDiscType(meta.DiscType)
}

func findLumeLastNameElement(value string, element string) int {
	element = strings.Join(strings.Fields(element), " ")
	if element == "" {
		return -1
	}
	return strings.LastIndex(" "+value+" ", " "+element+" ")
}

func replaceLumeHDR(name string, current string, replacement string) string {
	current = strings.TrimSpace(current)
	if current != "" && strings.Contains(name, current) {
		return strings.Replace(name, current, replacement, 1)
	}
	return name
}

func lumeHDR(facts api.HDRFacts) string {
	hasDV := slices.Contains(facts.Formats, api.HDRFormatDolbyVision)
	hasHDR10Plus := slices.Contains(facts.Formats, api.HDRFormatHDR10Plus)
	hasHDR := hasHDR10Plus || slices.Contains(facts.Formats, api.HDRFormatHDR10) ||
		slices.Contains(facts.Formats, api.HDRFormatHLG) ||
		slices.Contains(facts.Formats, api.HDRFormatPQ10) ||
		slices.Contains(facts.Formats, api.HDRFormatHDRVivid)
	switch {
	case hasDV && hasHDR10Plus:
		return "DV HDR10+"
	case hasDV && hasHDR:
		return "DV HDR"
	case hasDV:
		return "DV"
	case hasHDR10Plus:
		return "HDR10+"
	case hasHDR:
		return "HDR"
	default:
		return ""
	}
}
