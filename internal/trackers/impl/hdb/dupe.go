// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	logger   api.Logger
	endpoint string
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func (d *Definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		logger:   logger,
		endpoint: "https://hdbits.org/api/torrents",
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	if s.http == nil {
		return dupe.Failed(dupe.FailureInternal, "HDB handler misconfigured: no HTTP client", nil)
	}
	username, passkey := hdbCredentials(s.cfg)
	if username == "" || passkey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing username/passkey for tracker", nil)
	}
	payload := map[string]any{
		"username": username,
		"passkey":  passkey,
		"category": hdbDupeCategoryID(meta),
		"codec":    hdbDupeCodecID(meta),
		"medium":   hdbDupeMediumID(meta),
	}
	searchMethod := "id"
	if meta.Identity.IMDBID != 0 {
		payload["imdb"] = map[string]any{"id": fmt.Sprintf("%07d", meta.Identity.IMDBID)}
	} else if isHDBDupeTVCategory(meta) && meta.Identity.TVDBID != 0 {
		payload["tvdb"] = map[string]any{"id": meta.Identity.TVDBID}
	}
	if _, hasIMDB := payload["imdb"]; !hasIMDB {
		if _, hasTVDB := payload["tvdb"]; !hasTVDB {
			query := firstHDBText(meta.ReleaseName, meta.Filename, meta.Release.Title)
			if query == "" {
				s.logger.Warnf("dupechecking: HDB missing imdb/tvdb IDs and search text for %s", meta.SourcePath)
				return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb/tvdb id for HDB dupe search", nil)
			}
			payload["search"], searchMethod = query, "text_fallback"
			s.logger.Debugf("dupechecking: HDB falling back to text search for %s", meta.SourcePath)
		}
	}
	if logPayload, err := json.Marshal(redaction.RedactPrivateInfo(payload, nil)); err != nil {
		s.logger.Debugf("dupechecking: HDB search payload_marshal_failed=%v source=%s", err, meta.SourcePath)
	} else {
		s.logger.Debugf("dupechecking: HDB search payload=%s source=%s", string(logPayload), meta.SourcePath)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return dupe.Failed(dupe.FailureInternal, "HDB request failed", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(raw))
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "HDB request failed", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		s.logger.Warnf("dupechecking: HDB request failed for %s: %v", meta.SourcePath, err)
		return dupe.Failed(dupe.FailureRequest, "HDB request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		s.logger.Warnf("dupechecking: HDB search failed for %s with status=%d", meta.SourcePath, resp.StatusCode)
		return dupe.Failed(dupe.FailureResponseStatus, "HDB search failed", nil)
	}
	var body map[string]any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil || len(body) == 0 {
		return dupe.Failed(dupe.FailureResponseParse, "HDB search failed", err)
	}
	if hdbInt(body["status"]) != 0 {
		s.logger.Warnf("dupechecking: HDB API rejected search for %s", meta.SourcePath)
		return dupe.Failed(dupe.FailureResponseStatus, "HDB api rejected search", nil)
	}
	items, _ := body["data"].([]any)
	entries := make([]api.DupeEntry, 0, len(items))
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		id, filename := hdbString(item["id"]), hdbString(item["filename"])
		entry := api.DupeEntry{
			Name:      hdbString(item["name"]),
			ID:        id,
			Link:      "https://hdbits.org/details.php?id=" + id,
			Download:  "https://hdbits.org/download.php/" + url.QueryEscape(filename) + "?id=" + id + "&passkey=" + passkey,
			FileCount: hdbInt(item["numfiles"]),
		}
		if size := hdbInt(item["size"]); size > 0 {
			entry.SizeKnown, entry.SizeBytes = true, int64(size)
		}
		entries = append(entries, entry)
	}
	s.logger.Debugf("dupechecking: HDB returned %d entries for %s method=%s", len(entries), meta.SourcePath, searchMethod)
	return dupe.Resolved(entries, nil)
}

func firstHDBText(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func isHDBDupeTVCategory(meta api.DuplicateSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}

func hdbDupeCategoryID(meta api.DuplicateSubject) int {
	category, _ := meta.Identity.RequireCategory()
	switch category {
	case api.CanonicalCategoryMovie:
		return 1
	case api.CanonicalCategoryTV:
		return 2
	case api.CanonicalCategoryUnknown:
	}
	genres, keywords := "", ""
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

func hdbDupeCodecID(meta api.DuplicateSubject) int {
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

func hdbDupeMediumID(meta api.DuplicateSubject) int {
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	contentType := resolveHDBDupeType(meta)
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

func resolveHDBDupeType(meta api.DuplicateSubject) string {
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
