// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	rtfTorrentEndpoint = "https://retroflix.club/api/torrent"
	rtfLoginEndpoint   = "https://retroflix.club/api/login"
	rtfBrowsePrefix    = "https://retroflix.club/browse/t/"
)

type dupeSearcher struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

// newDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:    cfg,
		http:   httpClient,
		logger: logger,
	}
}

func (h dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	cfg, ok := rtfTrackerConfig(h.cfg)
	if !ok || (rtfAPIKey(cfg) == "" && !rtfHasCredentials(cfg)) {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	if h.http == nil {
		return dupe.Failed(dupe.FailureInternal, "RTF handler misconfigured: no HTTP client", nil)
	}
	if !isRTFContentOldEnough(meta, time.Now().UTC()) {
		return dupe.NotRun(dupe.NotRunUnsupportedContent, "RTF requires content at least 10 years and 1 month old", nil)
	}

	params, searchScope, searchComplete, ok := buildRTFSearchParams(meta)
	if !ok {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb/title for RTF dupe search", nil)
	}

	apiKey, err := h.ensureAPIKey(ctx, cfg)
	if err != nil {
		return dupe.Failed(dupe.FailureAuthentication, "RTF api key refresh failed", err)
	}

	status, payload, err := h.search(ctx, params, apiKey)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "RTF request failed", err)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		if !rtfHasCredentials(cfg) {
			return dupe.NotRun(dupe.NotRunMissingCredentials, "RTF api key expired and username/password are missing", nil)
		}
		refreshedKey, refreshErr := h.refreshToken(ctx, cfg)
		if refreshErr != nil {
			return dupe.Failed(dupe.FailureAuthentication, "RTF api key refresh failed", refreshErr)
		}
		h.cacheRTFAPIKey(cfg, refreshedKey)
		status, payload, err = h.search(ctx, params, refreshedKey)
		if err != nil {
			return dupe.Failed(dupe.FailureRequest, "RTF request failed", err)
		}
	}
	if status < 200 || status >= 300 {
		return dupe.Failed(dupe.FailureResponseStatus, "RTF search failed", nil)
	}
	list, ok := anyToSlice(payload)
	if !ok {
		return dupe.Failed(dupe.FailureResponseParse, "RTF response parse failed", nil)
	}
	entries := make([]api.DupeEntry, 0, len(list))
	for _, raw := range list {
		item, ok := raw.(map[string]any)
		if !ok {
			return dupe.Failed(dupe.FailureResponseParse, "RTF response contained a malformed torrent", nil)
		}
		id := stringFromAny(item["id"])
		if id == "" || strings.TrimSpace(stringFromAny(item["name"])) == "" {
			return dupe.Failed(dupe.FailureResponseParse, "RTF response contained a malformed torrent", nil)
		}
		entry := api.DupeEntry{
			Name:      stringFromAny(item["name"]),
			ID:        id,
			Link:      buildRTFLink(item, id),
			Download:  buildRTFDownloadLink(id),
			Type:      stringFromAny(item["type"]),
			Res:       stringFromAny(item["resolution"]),
			Source:    stringFromAny(item["source"]),
			Codec:     stringFromAny(item["codec"]),
			Container: stringFromAny(item["container"]),
			Edition:   stringFromAny(item["edition"]),
			Region:    stringFromAny(item["region"]),
		}
		size := intFromAny(item["size"])
		if size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = size
		}
		if fileCount := positiveIntFromAny(item["fileCount"]); fileCount > 0 {
			entry.FileCount = fileCount
		}
		if files, ok := item["files"].([]any); ok {
			if entry.FileCount == 0 {
				entry.FileCount = len(files)
			}
			for _, f := range files {
				if fileMap, ok := f.(map[string]any); ok {
					name := stringFromAny(fileMap["name"])
					if name != "" {
						entry.Files = append(entry.Files, name)
					}
				}
			}
		}
		entries = append(entries, entry)
	}
	warnings := []string(nil)
	if !searchComplete {
		warnings = []string{"RTF title search completeness is not evidenced"}
	}
	return dupe.ResolvedWithSearch(entries, warnings, dupe.SearchEvidence{
		Complete:  searchComplete,
		WorkScope: rtfWorkScope(searchComplete),
		Pages:     1,
		Scope:     searchScope,
		Warnings:  warnings,
	})
}

func rtfWorkScope(providerBound bool) dupe.WorkScope {
	if providerBound {
		return dupe.WorkScopeProviderID
	}
	return dupe.WorkScopeTitle
}

func positiveIntFromAny(value any) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(stringFromAny(value)))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func buildRTFSearchParams(meta api.DuplicateSubject) (url.Values, string, bool, bool) {
	params := url.Values{}
	params.Set("includingDead", "1")
	if meta.Identity.IMDBID != 0 {
		params.Set("imdbId", providerid.IMDb(meta.Identity.IMDBID).Prefixed())
		return params, "work_identity", true, true
	}
	query := cleanRTFSearchTitle(meta)
	if query == "" {
		return nil, "", false, false
	}
	params.Set("search", query)
	return params, "title_year", false, true
}

func cleanRTFSearchTitle(meta api.DuplicateSubject) string {
	query := strings.TrimSpace(meta.Release.Title)
	if query == "" && meta.Projection != nil {
		query = dupe.ProjectedSearchName(meta)
	}
	if query == "" {
		query = strings.TrimSpace(meta.ReleaseName)
	}
	if query == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		":", " ",
		",", "",
		"'", "",
		"’", "",
	)
	query = replacer.Replace(query)
	return strings.Join(strings.Fields(query), " ")
}

func (h dupeSearcher) search(ctx context.Context, params url.Values, apiKey string) (int, any, error) {
	headers := map[string]string{
		"accept":        "application/json",
		"Authorization": strings.TrimSpace(apiKey),
	}
	return rtfJSONRequest(ctx, h.http, http.MethodGet, rtfTorrentEndpoint, params, nil, headers)
}

func (h dupeSearcher) ensureAPIKey(ctx context.Context, cfg config.TrackerConfig) (string, error) {
	apiKey := rtfAPIKey(cfg)
	if apiKey != "" {
		return apiKey, nil
	}
	if !rtfHasCredentials(cfg) {
		return "", errors.New("RTF api key unavailable: missing username/password for refresh")
	}
	token, err := h.refreshToken(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("RTF api key unavailable: %w", err)
	}
	h.cacheRTFAPIKey(cfg, token)
	return token, nil
}

func (h dupeSearcher) refreshToken(ctx context.Context, cfg config.TrackerConfig) (string, error) {
	body := map[string]any{
		"username": strings.TrimSpace(cfg.Username),
		"password": strings.TrimSpace(cfg.Password),
	}
	status, payload, err := rtfJSONRequest(ctx, h.http, http.MethodPost, rtfLoginEndpoint, nil, body, map[string]string{"accept": "application/json"})
	if err != nil {
		return "", fmt.Errorf("RTF login request failed: %w", err)
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("RTF login returned status %d", status)
	}
	obj, ok := payload.(map[string]any)
	if !ok {
		return "", errors.New("RTF login returned invalid payload")
	}
	token := strings.TrimSpace(stringFromAny(obj["token"]))
	if token == "" {
		return "", errors.New("RTF login response missing token")
	}
	return token, nil
}

// cacheRTFAPIKey updates the in-memory config map so later searches in this
// process can reuse a refreshed token. Writing back to durable config storage
// is handled elsewhere.
func (h dupeSearcher) cacheRTFAPIKey(cfg config.TrackerConfig, token string) {
	if strings.TrimSpace(token) == "" || len(h.cfg.Trackers.Trackers) == 0 {
		return
	}
	for key, current := range h.cfg.Trackers.Trackers {
		if !strings.EqualFold(key, "RTF") {
			continue
		}
		current.APIKey = token
		current.PTPAPIKey = token
		h.cfg.Trackers.Trackers[key] = current
		return
	}
	cfg.APIKey = token
	cfg.PTPAPIKey = token
	h.cfg.Trackers.Trackers["RTF"] = cfg
}

func rtfAPIKey(cfg config.TrackerConfig) string {
	if key := strings.TrimSpace(cfg.APIKey); key != "" {
		return key
	}
	return strings.TrimSpace(cfg.PTPAPIKey)
}

func rtfHasCredentials(cfg config.TrackerConfig) bool {
	return strings.TrimSpace(cfg.Username) != "" && strings.TrimSpace(cfg.Password) != ""
}

func buildRTFLink(item map[string]any, id string) string {
	if link := strings.TrimSpace(stringFromAny(item["url"])); link != "" {
		return link
	}
	if id == "" {
		return ""
	}
	return rtfBrowsePrefix + url.PathEscape(id)
}

func buildRTFDownloadLink(id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	return rtfTorrentEndpoint + "/" + url.PathEscape(id) + "/download"
}

func isRTFContentOldEnough(meta api.DuplicateSubject, now time.Time) bool {
	return rtfContentAgeEligibility(meta.Release, meta.ProviderMetadata, now) == rtfAgeEligible
}

func rtfJSONRequest(
	ctx context.Context,
	client *http.Client,
	method, endpoint string,
	params url.Values,
	body map[string]any,
	headers map[string]string,
) (int, any, error) {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("rtf: encode request: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("rtf: create request: %w", err)
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("rtf: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, nil, nil
	}
	var result any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return resp.StatusCode, nil, fmt.Errorf("rtf: decode response: %w", err)
	}
	return resp.StatusCode, result, nil
}

func rtfTrackerConfig(cfg config.Config) (config.TrackerConfig, bool) {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "RTF") {
			return entry, true
		}
	}
	return config.TrackerConfig{}, false
}
func anyToSlice(value any) ([]any, bool) {
	items, ok := value.([]any)
	if ok {
		return items, true
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"data", "results", "torrents", "response"} {
			if items, ok := object[key].([]any); ok {
				return items, true
			}
		}
	}
	return nil, false
}
func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
func intFromAny(value any) int64 {
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
