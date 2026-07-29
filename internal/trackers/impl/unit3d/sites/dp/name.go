// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := baseName(meta)
	name = applyDPTVDBDisambiguation(name, meta)
	if !unit3d.IsDiscType(meta.DiscType) {
		if label := audioLabel(meta.AudioLanguages); label != "" {
			name = strings.Replace(name, "Dual-Audio", label, 1)
		}
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func applyDPTVDBDisambiguation(name string, meta api.UploadSubject) string {
	if unit3d.Category(meta) != "TV" || meta.ProviderMetadata.TVDB == nil {
		return name
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	title, alternate, tail, ok := splitDPTVName(name, meta, evidence)
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

func splitDPTVName(name string, meta api.UploadSubject, evidence api.TVDBNameDisambiguation) (string, string, string, bool) {
	title := strings.Join(strings.Fields(evidence.CanonicalName), " ")
	name = strings.Join(strings.Fields(name), " ")
	if title == "" || len(name) < len(title) || !strings.EqualFold(name[:len(title)], title) ||
		len(name) > len(title) && name[len(title)] != ' ' {
		return "", "", "", false
	}
	remainder := strings.TrimSpace(name[len(title):])
	remainder = trimDPLeadingYear(remainder, evidence.SeriesYear)
	tailStart := findDPTVTailStart(remainder, meta)
	if tailStart < 0 {
		return "", "", "", false
	}
	return title, strings.TrimSpace(remainder[:tailStart]), strings.TrimSpace(remainder[tailStart:]), true
}

func trimDPLeadingYear(value string, year int) string {
	if year <= 0 {
		return value
	}
	token := strconv.Itoa(year)
	if value == token {
		return ""
	}
	if strings.HasPrefix(value, token+" ") {
		return strings.TrimSpace(value[len(token):])
	}
	return value
}

func findDPTVTailStart(value string, meta api.UploadSubject) int {
	candidates := []string{
		strings.TrimSpace(meta.SeasonStr + meta.EpisodeStr),
		meta.SeasonStr,
		meta.DailyEpisodeDate,
		unit3d.Resolution(meta),
	}
	best := -1
	for _, candidate := range candidates {
		if index := findDPNameElement(value, candidate); index >= 0 && (best < 0 || index < best) {
			best = index
		}
	}
	return best
}

func findDPNameElement(value string, element string) int {
	element = strings.Join(strings.Fields(element), " ")
	if element == "" {
		return -1
	}
	return strings.Index(" "+value+" ", " "+element+" ")
}

func baseName(meta api.UploadSubject) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func audioLabel(values []string) string {
	unique := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			unique[strings.ToUpper(value)] = struct{}{}
		}
	}
	switch len(unique) {
	case 0:
		return ""
	case 1:
		for value := range unique {
			return value
		}
		return ""
	case 2:
		return "Dual-Audio"
	default:
		return "MULTi"
	}
}
