// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package yus

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
	switch unit3d.Category(meta) {
	case "TV":
		name = applyYUSTVDBDisambiguation(name, meta)
	case "MOVIE":
		name = applyYUSTMDBMovieYear(name, meta)
	}
	name = removeYUSNameElement(name, meta.Edition)
	if isYUSFullDisc(meta) {
		name = insertYUSDiscDistributor(name, meta)
	}
	return strings.Join(strings.Fields(name), " ")
}

func applyYUSTVDBDisambiguation(name string, meta api.UploadSubject) string {
	if !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) ||
		meta.ProviderMetadata.TVDB == nil {
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

func applyYUSTMDBMovieYear(name string, meta api.UploadSubject) string {
	if !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) ||
		meta.ProviderMetadata.TMDB == nil || meta.ProviderMetadata.TMDB.Year <= 0 ||
		meta.Release.Year <= 0 || meta.ProviderMetadata.TMDB.Year == meta.Release.Year {
		return name
	}
	searchEnd := len(name)
	if resolution := unit3d.Resolution(meta); resolution != "" {
		if index := findYUSNameElement(name, resolution); index >= 0 {
			searchEnd = index
		}
	}
	oldYear := strconv.Itoa(meta.Release.Year)
	index := findYUSLastNameElement(name[:searchEnd], oldYear)
	if index < 0 {
		return name
	}
	return name[:index] + strconv.Itoa(meta.ProviderMetadata.TMDB.Year) + name[index+len(oldYear):]
}

func removeYUSNameElement(name string, element string) string {
	element = strings.Join(strings.Fields(element), " ")
	index := findYUSLastNameElement(name, element)
	if index < 0 {
		return name
	}
	return strings.TrimSpace(name[:index] + " " + name[index+len(element):])
}

func insertYUSDiscDistributor(name string, meta api.UploadSubject) string {
	distributor := strings.Join(strings.Fields(meta.Distributor), " ")
	if distributor == "" || findYUSNameElement(name, distributor) >= 0 {
		return name
	}
	if resolution := strings.Join(strings.Fields(unit3d.Resolution(meta)), " "); resolution != "" {
		if index := findYUSLastNameElement(name, resolution); index >= 0 {
			end := index + len(resolution)
			return strings.TrimSpace(name[:end] + " " + distributor + " " + name[end:])
		}
	}
	if region := strings.Join(strings.Fields(meta.Region), " "); region != "" {
		if index := findYUSLastNameElement(name, region); index >= 0 {
			return strings.TrimSpace(name[:index] + distributor + " " + name[index:])
		}
	}
	return name
}

func isYUSFullDisc(meta api.UploadSubject) bool {
	nameType := strings.TrimSpace(meta.Type)
	return strings.EqualFold(nameType, "DISC") || nameType == "" && unit3d.IsDiscType(meta.DiscType)
}

func findYUSNameElement(value string, element string) int {
	element = strings.Join(strings.Fields(element), " ")
	if element == "" {
		return -1
	}
	return strings.Index(" "+value+" ", " "+element+" ")
}

func findYUSLastNameElement(value string, element string) int {
	element = strings.Join(strings.Fields(element), " ")
	if element == "" {
		return -1
	}
	return strings.LastIndex(" "+value+" ", " "+element+" ")
}
