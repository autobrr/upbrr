// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	endpoint string
}

// newDuplicateAdapterAt returns a duplicate-search adapter bound to one
// immutable dependency set and base URL.
func newDuplicateAdapterAt(deps dupe.Dependencies, baseURL string) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		endpoint: strings.TrimRight(baseURL, "/") + ptpTorrentPath,
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	if s.http == nil {
		return dupe.Failed(dupe.FailureInternal, "PTP handler misconfigured: no HTTP client", nil)
	}
	apiUser, apiKey := ptpAPIKeys(s.cfg)
	if apiUser == "" || apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing ApiUser/ApiKey for tracker", nil)
	}
	if meta.Identity.IMDBID == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb id for PTP dupe search", nil)
	}
	headers := map[string]string{"ApiUser": apiUser, "ApiKey": apiKey}
	groupPayload, err := s.get(ctx, url.Values{
		"imdb": {providerid.IMDb(meta.Identity.IMDBID).Digits()},
		"json": {"noredirect"},
	}, headers)
	if err != nil || len(groupPayload) == 0 {
		return dupe.Failed(dupe.FailureRequest, "PTP group search failed", err)
	}
	groupID, empty, valid := ptpResolvedGroupID(groupPayload)
	if !valid {
		return dupe.Failed(dupe.FailureResponseParse, "PTP group search response was malformed", nil)
	}
	if empty {
		return dupe.ResolvedWithSearch(nil, nil, dupe.SearchEvidence{
			Complete: true,
			Pages:    1,
			Scope:    "work_identity",
		})
	}
	payload, err := s.get(ctx, url.Values{
		"id":            {groupID},
		"json":          {"1"},
		"jsontrumpable": {"1"},
	}, headers)
	if err != nil || len(payload) == 0 {
		return dupe.Failed(dupe.FailureRequest, "PTP torrent search failed", err)
	}
	entries, ok := ptpDupeEntries(payload, groupID, strings.TrimSuffix(s.endpoint, ptpTorrentPath))
	if !ok {
		return dupe.Failed(dupe.FailureResponseParse, "PTP torrent group response was malformed", nil)
	}
	return dupe.ResolvedWithSearch(entries, nil, dupe.SearchEvidence{
		Complete: true,
		Pages:    1,
		Scope:    "work_identity",
	})
}

func (s *dupeSearcher) get(ctx context.Context, params url.Values, headers map[string]string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("ptp dupe request: %w", err)
	}
	req.URL.RawQuery = params.Encode()
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ptp dupe request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, strconv.ErrSyntax
	}
	var payload map[string]any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("ptp dupe decode: %w", err)
	}
	return payload, nil
}

func ptpAPIKeys(cfg config.Config) (string, string) {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "PTP") {
			return strings.TrimSpace(entry.PTPAPIUser), strings.TrimSpace(entry.PTPAPIKey)
		}
	}
	return "", ""
}

func ptpResolvedGroupID(payload map[string]any) (string, bool, bool) {
	if rawMovies, exists := payload["Movies"]; exists {
		movies, ok := rawMovies.([]any)
		if !ok {
			return "", false, false
		}
		if len(movies) == 0 {
			return "", true, true
		}
		movie, ok := movies[0].(map[string]any)
		if !ok {
			return "", false, false
		}
		groupID := ptpString(movie["GroupId"])
		return groupID, false, groupID != ""
	}
	groupID := ptpString(payload["GroupId"])
	return groupID, false, groupID != ""
}

func ptpDupeEntries(payload map[string]any, groupID string, baseURL string) ([]api.DupeEntry, bool) {
	rawTorrents, exists := payload["Torrents"]
	if !exists {
		return nil, false
	}
	torrents, ok := rawTorrents.([]any)
	if !ok {
		return nil, false
	}
	entries := make([]api.DupeEntry, 0, len(torrents))
	for _, raw := range torrents {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, false
		}
		id := ptpString(item["Id"])
		if id == "" {
			return nil, false
		}
		releaseName := ptpString(item["ReleaseName"])
		if releaseName == "" {
			releaseName = "PTP torrent " + id
		}
		remasterTitle := ptpString(item["RemasterTitle"])
		entry := api.DupeEntry{
			Name:      releaseName,
			ID:        id,
			Link:      strings.TrimRight(baseURL, "/") + ptpTorrentPath + "?id=" + url.QueryEscape(groupID) + "&torrentid=" + url.QueryEscape(id),
			Download:  strings.TrimRight(baseURL, "/") + ptpTorrentPath + "?action=download&id=" + url.QueryEscape(id),
			Type:      ptpCandidateType(item),
			Res:       ptpString(item["Resolution"]),
			Source:    ptpString(item["Source"]),
			Codec:     ptpString(item["Codec"]),
			Container: ptpString(item["Container"]),
			Group:     ptpString(item["ReleaseGroup"]),
			Edition:   ptpMovieCut(remasterTitle),
			HDR:       ptpCandidateHDR(remasterTitle),
			Trumpable: ptpTrumpable(item["Trumpable"]),
		}
		if size := ptpInt64(item["Size"]); size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = size
		}
		entries = append(entries, entry)
	}
	return entries, true
}

func ptpTrumpable(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case json.Number:
		parsed, err := typed.Int64()
		return err == nil && parsed != 0
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		return normalized != "" && normalized != "0" && normalized != "false" && normalized != "null" && normalized != "[]"
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return false
	}
}

func ptpCandidateHDR(remasterTitle string) api.HDRFacts {
	facts := dupe.NormalizeTrackerTitleHDR(remasterTitle)
	if len(facts.Formats) == 0 {
		facts.Formats = []api.HDRFormat{api.HDRFormatSDR}
	}
	facts.Origin = api.HDREvidenceTrackerAPI
	facts.Status = api.HDREvidenceComplete
	facts.SourceFields = []string{"RemasterTitle"}
	return facts
}

func ptpCandidateType(item map[string]any) string {
	source := strings.ToUpper(ptpString(item["Source"]))
	container := strings.ToUpper(ptpString(item["Container"]))
	name := strings.ToUpper(ptpString(item["ReleaseName"]))
	switch {
	case strings.Contains(name, "REMUX"):
		return "REMUX"
	case strings.Contains(name, "WEB-DL"), strings.Contains(name, "WEBDL"):
		return "WEBDL"
	case strings.Contains(name, "WEBRIP"), strings.Contains(name, "WEB-RIP"):
		return "WEBRIP"
	case source == "DVD" && (strings.Contains(container, "VOB") || strings.Contains(container, "IFO")):
		return "DISC"
	case (strings.Contains(source, "BLU-RAY") || strings.Contains(source, "BLURAY") || strings.Contains(source, "HD-DVD")) &&
		(strings.Contains(container, "M2TS") || container == "ISO"):
		return "DISC"
	case strings.Contains(name, "BLURAY"), strings.Contains(name, "BLU-RAY"), strings.Contains(name, "BDRIP"):
		return "ENCODE"
	default:
		return ""
	}
}

func ptpMovieCut(value string) string {
	normalized := strings.NewReplacer(".", " ", "_", " ", "-", " ", "'", "").Replace(strings.ToLower(value))
	normalized = strings.Join(strings.Fields(normalized), " ")
	for _, cut := range []struct {
		marker string
		value  string
	}{
		{marker: "directors cut", value: "directors_cut"},
		{marker: "extended cut", value: "extended"},
		{marker: "extended edition", value: "extended"},
		{marker: "theatrical cut", value: "theatrical"},
		{marker: "final cut", value: "final_cut"},
		{marker: "international cut", value: "international_cut"},
		{marker: "uncut", value: "uncut"},
		{marker: "unrated", value: "unrated"},
	} {
		if strings.Contains(normalized, cut.marker) {
			return cut.value
		}
	}
	return ""
}

func ptpInt64(value any) int64 {
	switch typed := value.(type) {
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case float64:
		parsed, _ := strconv.ParseInt(strconv.FormatFloat(typed, 'f', -1, 64), 10, 64)
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func ptpString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}
