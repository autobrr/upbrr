// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// isHDBTVCategory reports whether HDB upload payloads may include TVDB fields.
// Canonical movie identity suppresses TVDB fields even when episode facts exist.
func isHDBTVCategory(meta api.UploadSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}

func hdbCategoryID(meta api.UploadSubject) int {
	category, _ := meta.Identity.RequireCategory()
	switch category {
	case api.CanonicalCategoryMovie:
		return 1
	case api.CanonicalCategoryTV:
		return 2
	case api.CanonicalCategoryUnknown:
	}
	genres := ""
	keywords := ""
	if meta.ProviderMetadata.TMDB != nil {
		genres = strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres))
		keywords = strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords))
	}
	if strings.Contains(genres, "documentary") || strings.Contains(keywords, "documentary") {
		return 3
	}
	if meta.ProviderMetadata.IMDB != nil {
		imdbType := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.IMDB.Type))
		imdbGenres := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres))
		if strings.Contains(imdbType, "concert") || (strings.Contains(imdbType, "video") && strings.Contains(imdbGenres, "music")) {
			return 4
		}
	}
	return 0
}

func hdbCodecID(meta api.UploadSubject) int {
	codec := strings.ToUpper(strings.TrimSpace(meta.VideoCodec))
	if codec == "" {
		codec = strings.ToUpper(strings.TrimSpace(meta.VideoEncode))
	}
	switch codec {
	case "AVC", "H.264":
		return 1
	case "MPEG-2":
		return 2
	case "VC-1":
		return 3
	case "XVID":
		return 4
	case "HEVC", "H.265":
		return 5
	case "VP9":
		return 6
	default:
		return 0
	}
}

func hdbMediumID(meta api.UploadSubject) int {
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	contentType := resolveHDBType(meta)
	if discType == "BDMV" || discType == "HD DVD" {
		return 1
	}
	if contentType == "HDTV" {
		if meta.HasEncodeSettings {
			return 3
		}
		return 4
	}
	switch contentType {
	case "ENCODE", "WEBRIP":
		return 3
	case "REMUX":
		return 5
	case "WEBDL":
		return 6
	default:
		return 0
	}
}

func isHDBCategoryType(value string) bool {
	upper := strings.ToUpper(strings.TrimSpace(value))
	return upper == "MOVIE" || upper == "TV"
}

func resolveHDBType(meta api.UploadSubject) string {
	typeValue := normalizeHDBType(meta.Type)
	if typeValue == "" || isHDBCategoryType(typeValue) {
		if meta.ReleaseNameOverrides.Type != nil {
			typeValue = normalizeHDBType(*meta.ReleaseNameOverrides.Type)
		}
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		typeValue = normalizeHDBType(meta.Release.Type)
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		if strings.TrimSpace(meta.DiscType) != "" {
			typeValue = "DISC"
		}
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		typeValue = inferHDBTypeFromSource(meta.Source)
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		typeValue = inferHDBTypeFromPath(meta.SourcePath)
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		if strings.TrimSpace(meta.VideoEncode) != "" {
			typeValue = "ENCODE"
		}
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		if strings.TrimSpace(meta.VideoCodec) != "" || strings.TrimSpace(meta.Release.Resolution) != "" || strings.TrimSpace(meta.Release.Ext) != "" {
			typeValue = "ENCODE"
		}
	}
	return typeValue
}

func normalizeHDBType(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	if upper == "" {
		return ""
	}
	upper = strings.ReplaceAll(upper, "-", "")
	upper = strings.ReplaceAll(upper, " ", "")
	upper = strings.ReplaceAll(upper, "_", "")
	switch upper {
	case "WEBDL":
		return "WEBDL"
	case "WEBRIP":
		return "WEBRIP"
	}
	return upper
}

func inferHDBTypeFromSource(source string) string {
	upper := normalizeHDBType(source)
	switch {
	case strings.Contains(upper, "WEBDL"):
		return "WEBDL"
	case strings.Contains(upper, "WEBRIP"):
		return "WEBRIP"
	case strings.Contains(upper, "HDTV"):
		return "HDTV"
	}
	return ""
}

func inferHDBTypeFromPath(path string) string {
	base := strings.ToUpper(strings.TrimSpace(path))
	compact := strings.NewReplacer(".", "", "-", "", "_", "", " ", "").Replace(base)
	switch {
	case strings.Contains(compact, "REMUX"):
		return "REMUX"
	case strings.Contains(compact, "WEBDL"):
		return "WEBDL"
	case strings.Contains(compact, "WEBRIP"):
		return "WEBRIP"
	case strings.Contains(compact, "HDTV"):
		return "HDTV"
	case strings.Contains(compact, "DVDRIP"):
		return "DVDRIP"
	}
	return ""
}
