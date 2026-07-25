// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"fmt"
	"strings"

	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUnit3DTypeIDForTracker(tracker string, meta api.UploadSubject, profiles ...SiteProfile) (string, error) {
	trackerName := strings.ToUpper(strings.TrimSpace(tracker))
	profile := firstSiteProfile(profiles)
	if profile.ResolveTypeID == nil {
		return resolveUnit3DTypeID(meta)
	}
	typeID := profile.ResolveTypeID(meta)
	if strings.TrimSpace(typeID) == "" || typeID == "0" {
		resolvedType := inferUnit3DType(meta)
		if resolvedType == "" {
			resolvedType = strings.ToUpper(strings.TrimSpace(meta.Type))
		}
		return "", fmt.Errorf("trackers: %s unsupported type value %q", trackerName, resolvedType)
	}
	return typeID, nil
}

func resolveUnit3DTypeID(meta api.UploadSubject) (string, error) {
	typeValue := inferUnit3DType(meta)
	if value := trackerdata.TypeID(typeValue); value != "" {
		return value, nil
	}
	if typeValue == "" {
		typeValue = strings.ToUpper(strings.TrimSpace(meta.Type))
		if typeValue == "" {
			typeValue = strings.ToUpper(strings.TrimSpace(meta.Release.Type))
		}
	}
	return "", fmt.Errorf("trackers: unit3d unsupported type value %q", typeValue)
}

func resolveUnit3DResolutionIDForTracker(_ string, meta api.UploadSubject, profiles ...SiteProfile) string {
	profile := firstSiteProfile(profiles)
	if profile.ResolveResolutionID != nil {
		return profile.ResolveResolutionID(meta)
	}
	return resolveUnit3DResolutionID(meta)
}

func resolveUnit3DCategoryIDForTracker(_ string, meta api.UploadSubject, profiles ...SiteProfile) string {
	profile := firstSiteProfile(profiles)
	if profile.ResolveCategoryID != nil {
		return profile.ResolveCategoryID(meta)
	}
	return resolveUnit3DCategoryID(meta)
}

// resolveUnit3DCategory maps only the canonical prepared identity category.
func resolveUnit3DCategory(meta api.UploadSubject) string {
	return resolveExplicitUnit3DCategory(string(meta.Identity.Category))
}

// resolveExplicitUnit3DCategory accepts only finalized canonical values.
func resolveExplicitUnit3DCategory(value string) string {
	category, err := api.NormalizeCanonicalCategory(value)
	if err != nil {
		return ""
	}
	switch category {
	case api.CanonicalCategoryMovie:
		return "MOVIE"
	case api.CanonicalCategoryTV:
		return "TV"
	case api.CanonicalCategoryUnknown:
		return ""
	}
	return ""
}

func resolveUnit3DCategoryID(meta api.UploadSubject) string {
	return trackerdata.CategoryID(resolveUnit3DCategory(meta))
}

func isUnit3DCategoryType(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "MOVIE", "TV", "EPISODE", "SERIES", "SHOW", "FILM", "TVSHOW":
		return true
	default:
		return false
	}
}

func resolveUnit3DResolutionID(meta api.UploadSubject) string {
	resolution := resolveResolution(meta)
	if value := trackerdata.ResolutionID(resolution); value != "" {
		return value
	}
	return "10"
}

func resolveResolution(meta api.UploadSubject) string {
	return resolveResolutionValues(meta.Release, meta.ReleaseName)
}

func resolveResolutionValues(release api.ReleaseInfo, releaseName string) string {
	resolution := strings.TrimSpace(release.Resolution)
	if resolution == "" {
		resolution = detectResolution(releaseName)
	}
	return resolution
}

func detectResolution(value string) string {
	clean := strings.ToLower(value)
	for _, candidate := range []string{"8640p", "4320p", "2160p", "1440p", "1080p", "1080i", "720p", "576p", "576i", "480p", "480i"} {
		if strings.Contains(clean, candidate) {
			return candidate
		}
	}
	return ""
}

func isSDResolution(resolution string) bool {
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "480p", "480i", "576p", "576i":
		return true
	default:
		return false
	}
}

func boolFlag(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func inferUnit3DType(meta api.UploadSubject) string {
	for _, candidate := range []string{meta.Type, meta.Release.Type} {
		normalized := normalizeUnit3DTypeCandidate(candidate)
		if normalized != "" && !isUnit3DCategoryType(normalized) {
			return normalized
		}
	}

	releaseName := strings.ToUpper(strings.TrimSpace(meta.ReleaseName))
	source := strings.ToUpper(strings.TrimSpace(meta.Source))
	if source == "" {
		source = strings.ToUpper(strings.TrimSpace(meta.Release.Source))
	}

	switch {
	case strings.Contains(releaseName, "REMUX"):
		return "REMUX"
	case strings.Contains(releaseName, "WEB-DL") || strings.Contains(releaseName, "WEBDL"):
		return "WEBDL"
	case strings.Contains(releaseName, "WEBRIP") || strings.Contains(releaseName, "WEB-RIP"):
		return "WEBRIP"
	case strings.Contains(releaseName, "DVDRIP"):
		return "DVDRIP"
	case strings.Contains(releaseName, "HDTV"):
		return "HDTV"
	}

	if isDiscType(meta.DiscType) {
		return "DISC"
	}

	switch {
	case strings.Contains(source, "WEB-DL") || strings.Contains(source, "WEBDL"):
		return "WEBDL"
	case strings.Contains(source, "WEBRIP") || strings.Contains(source, "WEB-RIP"):
		return "WEBRIP"
	case strings.Contains(source, "HDTV") || strings.Contains(source, "UHDTV"):
		return "HDTV"
	case strings.Contains(source, "BLURAY") || strings.Contains(source, "BDRIP"):
		return "ENCODE"
	case strings.Contains(source, "WEB"):
		if strings.TrimSpace(meta.VideoEncode) != "" {
			return "WEBRIP"
		}
		return "WEBDL"
	}

	if strings.TrimSpace(meta.VideoEncode) != "" {
		return "ENCODE"
	}

	return ""
}

func normalizeUnit3DTypeCandidate(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	compact := strings.NewReplacer("-", "", "_", "", " ", "").Replace(upper)

	switch compact {
	case "DISC", "REMUX", "WEBDL", "WEBRIP", "HDTV", "ENCODE", "DVDRIP":
		return compact
	case "MOVIE", "TV", "EPISODE", "SERIES", "SHOW", "FILM", "TVSHOW":
		return compact
	}

	switch {
	case strings.Contains(compact, "WEBDL"):
		return "WEBDL"
	case strings.Contains(compact, "WEBRIP"):
		return "WEBRIP"
	case strings.Contains(compact, "DVDRIP"):
		return "DVDRIP"
	case strings.Contains(compact, "HDTV"):
		return "HDTV"
	case strings.Contains(compact, "REMUX"):
		return "REMUX"
	}

	return ""
}

func shouldIncludeUnit3DTVFields(_ api.UploadSubject, category string) bool {
	return strings.EqualFold(strings.TrimSpace(category), "TV")
}

func isDiscType(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "BDMV", "DVD", "HDDVD":
		return true
	default:
		return false
	}
}

func resolveKeywords(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
	}
	return ""
}

func resolveKeywordsForTracker(_ string, meta api.UploadSubject, profiles ...SiteProfile) string {
	profile := firstSiteProfile(profiles)
	if profile.ResolveKeywords != nil {
		return profile.ResolveKeywords(meta)
	}
	return resolveKeywords(meta)
}
