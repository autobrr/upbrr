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
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	rtfTorrentEndpoint = "https://retroflix.club/api/torrent"
	rtfLoginEndpoint   = "https://retroflix.club/api/login"
	rtfBrowsePrefix    = "https://retroflix.club/browse/t/"
	rtfAgeGraceDays    = 365*10 + 3
)

type dupeSearcher struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func (Definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
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
		return dupe.NotRun(dupe.NotRunUnsupportedContent, "RTF requires content older than 10 years", nil)
	}

	params, ok := buildRTFSearchParams(meta)
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
			continue
		}
		id := stringFromAny(item["id"])
		entry := api.DupeEntry{
			Name:     stringFromAny(item["name"]),
			ID:       id,
			Link:     buildRTFLink(item, id),
			Download: buildRTFDownloadLink(id),
		}
		size := intFromAny(item["size"])
		if size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = size
		}
		if files, ok := item["files"].([]any); ok {
			entry.FileCount = len(files)
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
	return dupe.Resolved(entries, nil)
}

func buildRTFSearchParams(meta api.DuplicateSubject) (url.Values, bool) {
	params := url.Values{}
	params.Set("includingDead", "1")
	if meta.Identity.IMDBID != 0 {
		params.Set("imdbId", fmt.Sprintf("tt%07d", meta.Identity.IMDBID))
		return params, true
	}
	query := cleanRTFSearchTitle(meta)
	if query == "" {
		return nil, false
	}
	params.Set("search", query)
	return params, true
}

func cleanRTFSearchTitle(meta api.DuplicateSubject) string {
	query := strings.TrimSpace(meta.Release.Title)
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
	cutoff := now.UTC().AddDate(0, 0, -rtfAgeGraceDays)
	if date, ok := rtfReferenceDate(meta); ok {
		return !date.After(cutoff)
	}
	year := rtfReferenceYear(meta)
	if year == 0 {
		return true
	}
	// Year-based fallback is intentionally looser than the date-based check
	// because month/day precision is unavailable when only year is known.
	return now.UTC().Year()-year > 9
}

func rtfReferenceDate(meta api.DuplicateSubject) (time.Time, bool) {
	category := rtfCategory(meta)
	switch category {
	case "MOVIE":
		return rtfMovieReleaseDate(meta)
	case "TV":
		return rtfMostRecentTVDate(meta)
	default:
		if date, ok := rtfMovieReleaseDate(meta); ok {
			return date, true
		}
		return rtfMostRecentTVDate(meta)
	}
}

func rtfMovieReleaseDate(meta api.DuplicateSubject) (time.Time, bool) {
	if meta.ProviderMetadata.TMDB == nil {
		return time.Time{}, false
	}
	return parseRTFDate(meta.ProviderMetadata.TMDB.ReleaseDate)
}

func rtfMostRecentTVDate(meta api.DuplicateSubject) (time.Time, bool) {
	candidates := make([]time.Time, 0, 8)
	if meta.ProviderMetadata.TMDB != nil {
		if date, ok := parseRTFDate(meta.ProviderMetadata.TMDB.LastAirDate); ok {
			candidates = append(candidates, date)
		}
		if date, ok := parseRTFDate(meta.ProviderMetadata.TMDB.FirstAirDate); ok {
			candidates = append(candidates, date)
		}
	}
	if meta.ProviderMetadata.TVmaze != nil {
		if date, ok := parseRTFDate(meta.ProviderMetadata.TVmaze.Premiered); ok {
			candidates = append(candidates, date)
		}
	}
	if meta.ProviderMetadata.IMDB != nil {
		for _, episode := range meta.ProviderMetadata.IMDB.Episodes {
			if date, ok := rtfEpisodeDate(episode); ok {
				candidates = append(candidates, date)
			}
		}
	}
	if len(candidates) == 0 {
		return time.Time{}, false
	}
	latest := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.After(latest) {
			latest = candidate
		}
	}
	return latest, true
}

func rtfEpisodeDate(episode api.IMDBEpisode) (time.Time, bool) {
	if episode.ReleaseDate.Year > 0 {
		month := episode.ReleaseDate.Month
		if month <= 0 {
			month = 1
		}
		day := episode.ReleaseDate.Day
		if day <= 0 {
			day = 1
		}
		return time.Date(episode.ReleaseDate.Year, time.Month(month), day, 0, 0, 0, 0, time.UTC), true
	}
	if episode.ReleaseYear > 0 {
		return time.Date(episode.ReleaseYear, time.January, 1, 0, 0, 0, 0, time.UTC), true
	}
	return time.Time{}, false
}

func parseRTFDate(raw string) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), true
	}
	return time.Time{}, false
}

func rtfReferenceYear(meta api.DuplicateSubject) int {
	if date, ok := rtfReferenceDate(meta); ok {
		return date.Year()
	}
	if meta.Release.Year > 0 {
		return meta.Release.Year
	}
	if meta.ProviderMetadata.TMDB != nil && meta.ProviderMetadata.TMDB.Year > 0 {
		return meta.ProviderMetadata.TMDB.Year
	}
	if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Year > 0 {
		return meta.ProviderMetadata.IMDB.Year
	}
	if meta.ProviderMetadata.TVmaze != nil {
		if date, ok := parseRTFDate(meta.ProviderMetadata.TVmaze.Premiered); ok {
			return date.Year()
		}
	}
	return 0
}

func rtfCategory(meta api.DuplicateSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return strings.ToUpper(string(category))
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
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}
