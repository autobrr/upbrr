// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

type hdbHandler struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

func (h hdbHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	logger := h.logger
	if logger == nil {
		logger = api.NopLogger{}
	}
	if h.http == nil {
		return nil, []string{noteSkip("HDB handler misconfigured: no HTTP client")}, nil
	}
	cfg, ok := trackerCfg(h.cfg, "HDB")
	if !ok || strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Passkey) == "" {
		return nil, []string{noteSkip("missing username/passkey for tracker")}, nil
	}
	payload := map[string]any{"username": strings.TrimSpace(cfg.Username), "passkey": strings.TrimSpace(cfg.Passkey)}
	categoryID := hdbCategoryID(meta)
	codecID := hdbCodecID(meta)
	mediumID := hdbMediumID(meta)
	searchMethod := "id"
	payload["category"] = categoryID
	payload["codec"] = codecID
	payload["medium"] = mediumID
	if meta.ExternalIDs.IMDBID != 0 {
		payload["imdb"] = map[string]any{"id": formatHDBIMDbID(meta.ExternalIDs.IMDBID)}
	} else if isHDBTVCategory(meta) && meta.ExternalIDs.TVDBID != 0 {
		payload["tvdb"] = map[string]any{"id": meta.ExternalIDs.TVDBID}
	}
	if _, hasIMDB := payload["imdb"]; !hasIMDB {
		if _, hasTVDB := payload["tvdb"]; !hasTVDB {
			query := strings.TrimSpace(meta.ReleaseName)
			if query == "" {
				query = strings.TrimSpace(meta.Filename)
			}
			if query == "" {
				query = strings.TrimSpace(meta.Release.Title)
			}
			if query == "" {
				logger.Warnf("dupechecking: HDB missing imdb/tvdb IDs and search text for %s", meta.SourcePath)
				return nil, []string{noteSkip("missing imdb/tvdb id for HDB dupe search")}, nil
			}
			payload["search"] = query
			searchMethod = "text_fallback"
			logger.Debugf("dupechecking: HDB falling back to text search for %s", meta.SourcePath)
		}
	}
	logPayload := redaction.RedactPrivateInfo(payload, nil)
	logPayloadJSON, err := json.Marshal(logPayload)
	if err != nil {
		logger.Debugf("dupechecking: HDB search payload_marshal_failed=%v source=%s", err, meta.SourcePath)
	} else {
		logger.Debugf("dupechecking: HDB search payload=%s source=%s", string(logPayloadJSON), meta.SourcePath)
	}
	status, body, err := doJSONPost(ctx, h.http, "https://hdbits.org/api/torrents", payload, nil)
	if err != nil {
		logger.Warnf("dupechecking: HDB request failed for %s: %v", meta.SourcePath, err)
		return nil, []string{noteSkip("HDB request failed")}, nil
	}
	if status < 200 || status >= 300 || len(body) == 0 {
		logger.Warnf("dupechecking: HDB search failed for %s with status=%d body_len=%d", meta.SourcePath, status, len(body))
		return nil, []string{noteSkip("HDB search failed")}, nil
	}
	if intFromAny(body["status"]) != 0 {
		logger.Warnf("dupechecking: HDB API rejected search for %s with status=%v", meta.SourcePath, body["status"])
		return nil, []string{noteSkip("HDB api rejected search")}, nil
	}
	items, _ := body["data"].([]any)
	entries := make([]api.DupeEntry, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := stringFromAny(item["id"])
		name := stringFromAny(item["name"])
		filename := stringFromAny(item["filename"])
		entry := api.DupeEntry{
			Name:      name,
			ID:        id,
			Link:      "https://hdbits.org/details.php?id=" + id,
			Download:  "https://hdbits.org/download.php/" + url.QueryEscape(filename) + "?id=" + id + "&passkey=" + strings.TrimSpace(cfg.Passkey),
			FileCount: int(intFromAny(item["numfiles"])),
		}
		size := intFromAny(item["size"])
		if size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = size
		}
		entries = append(entries, entry)
	}
	logger.Debugf("dupechecking: HDB returned %d entries for %s method=%s", len(entries), meta.SourcePath, searchMethod)
	return entries, nil, nil
}

// isHDBTVCategory reports whether HDB dupe searches may use TVDB-specific filters.
// Explicit movie categories suppress TVDB even when MediaInfo or overrides classify the release as TV.
func isHDBTVCategory(meta api.PreparedMetadata) bool {
	if isHDBMovieCategory(meta) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(meta.ExternalIDs.Category), "TV") ||
		strings.EqualFold(strings.TrimSpace(meta.MediaInfoCategory), "TV") ||
		strings.EqualFold(strings.TrimSpace(meta.Release.Category), "TV") {
		return true
	}
	if meta.ReleaseNameOverrides.Category != nil {
		return strings.EqualFold(strings.TrimSpace(*meta.ReleaseNameOverrides.Category), "TV")
	}
	return false
}

func isHDBMovieCategory(meta api.PreparedMetadata) bool {
	if strings.EqualFold(strings.TrimSpace(meta.ExternalIDs.Category), "MOVIE") ||
		strings.EqualFold(strings.TrimSpace(meta.MediaInfoCategory), "MOVIE") ||
		strings.EqualFold(strings.TrimSpace(meta.Release.Category), "MOVIE") {
		return true
	}
	if meta.ReleaseNameOverrides.Category != nil {
		return strings.EqualFold(strings.TrimSpace(*meta.ReleaseNameOverrides.Category), "MOVIE")
	}
	return false
}

func formatHDBIMDbID(imdbID int) string {
	return fmt.Sprintf("%07d", imdbID)
}

func hdbCategoryID(meta api.PreparedMetadata) int {
	category := strings.ToUpper(strings.TrimSpace(meta.ExternalIDs.Category))
	if category == "" {
		category = strings.ToUpper(strings.TrimSpace(meta.MediaInfoCategory))
	}
	if category == "" && meta.ReleaseNameOverrides.Category != nil {
		category = strings.ToUpper(strings.TrimSpace(*meta.ReleaseNameOverrides.Category))
	}

	categoryID := 0
	switch category {
	case "MOVIE":
		categoryID = 1
	case "TV":
		categoryID = 2
	}

	genres := ""
	keywords := ""
	if meta.ExternalMetadata.TMDB != nil {
		genres = strings.ToLower(strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres))
		keywords = strings.ToLower(strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords))
	}
	if strings.Contains(genres, "documentary") || strings.Contains(keywords, "documentary") {
		categoryID = 3
	}

	if meta.ExternalMetadata.IMDB != nil {
		imdbType := strings.ToLower(strings.TrimSpace(meta.ExternalMetadata.IMDB.Type))
		imdbGenres := strings.ToLower(strings.TrimSpace(meta.ExternalMetadata.IMDB.Genres))
		if strings.Contains(imdbType, "concert") || (strings.Contains(imdbType, "video") && strings.Contains(imdbGenres, "music")) {
			categoryID = 4
		}
	}
	return categoryID
}

func hdbCodecID(meta api.PreparedMetadata) int {
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

func hdbMediumID(meta api.PreparedMetadata) int {
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

func resolveHDBType(meta api.PreparedMetadata) string {
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

func isHDBCategoryType(value string) bool {
	upper := strings.ToUpper(strings.TrimSpace(value))
	return upper == "MOVIE" || upper == "TV"
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
