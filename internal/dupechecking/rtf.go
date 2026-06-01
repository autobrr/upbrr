// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	rtfTorrentEndpoint = "https://retroflix.club/api/torrent"
	rtfLoginEndpoint   = "https://retroflix.club/api/login"
	rtfBrowsePrefix    = "https://retroflix.club/browse/t/"
	rtfAgeGraceDays    = 365*10 + 3
)

type rtfHandler struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

func (h rtfHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	cfg, ok := trackerCfg(h.cfg, "RTF")
	if !ok || (rtfAPIKey(cfg) == "" && !rtfHasCredentials(cfg)) {
		return nil, []string{noteSkip("missing api_key for tracker")}, nil
	}
	if h.http == nil {
		return nil, []string{noteSkip("RTF handler misconfigured: no HTTP client")}, nil
	}
	if !isRTFContentOldEnough(meta, time.Now().UTC()) {
		return nil, []string{noteSkip("RTF requires content older than 10 years")}, nil
	}

	params, ok := buildRTFSearchParams(meta)
	if !ok {
		return nil, []string{noteSkip("missing imdb/title for RTF dupe search")}, nil
	}

	apiKey, err := h.ensureAPIKey(ctx, cfg)
	if err != nil {
		return nil, []string{noteSkip(err.Error())}, nil
	}

	status, payload, err := h.search(ctx, params, apiKey)
	if err != nil {
		return nil, []string{noteSkip("RTF request failed")}, nil
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		if !rtfHasCredentials(cfg) {
			return nil, []string{noteSkip("RTF api key expired and username/password are missing")}, nil
		}
		refreshedKey, refreshErr := h.refreshToken(ctx, cfg)
		if refreshErr != nil {
			return nil, []string{noteSkip("RTF api key refresh failed")}, nil
		}
		h.cacheRTFAPIKey(cfg, refreshedKey)
		status, payload, err = h.search(ctx, params, refreshedKey)
		if err != nil {
			return nil, []string{noteSkip("RTF request failed")}, nil
		}
	}
	if status < 200 || status >= 300 {
		return nil, []string{noteSkip("RTF search failed")}, nil
	}
	list, ok := anyToSlice(payload)
	if !ok {
		return nil, nil, nil
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
	return entries, nil, nil
}

func buildRTFSearchParams(meta api.PreparedMetadata) (url.Values, bool) {
	params := url.Values{}
	params.Set("includingDead", "1")
	if meta.ExternalIDs.IMDBID != 0 {
		params.Set("imdbId", fmt.Sprintf("tt%07d", meta.ExternalIDs.IMDBID))
		return params, true
	}
	query := cleanRTFSearchTitle(meta)
	if query == "" {
		return nil, false
	}
	params.Set("search", query)
	return params, true
}

func cleanRTFSearchTitle(meta api.PreparedMetadata) string {
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

func (h rtfHandler) search(ctx context.Context, params url.Values, apiKey string) (int, any, error) {
	headers := map[string]string{
		"accept":        "application/json",
		"Authorization": strings.TrimSpace(apiKey),
	}
	return doJSONGetAny(ctx, h.http, rtfTorrentEndpoint, params, headers)
}

func (h rtfHandler) ensureAPIKey(ctx context.Context, cfg config.TrackerConfig) (string, error) {
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

func (h rtfHandler) refreshToken(ctx context.Context, cfg config.TrackerConfig) (string, error) {
	body := map[string]any{
		"username": strings.TrimSpace(cfg.Username),
		"password": strings.TrimSpace(cfg.Password),
	}
	status, payload, err := doJSONPostAny(ctx, h.http, rtfLoginEndpoint, body, map[string]string{"accept": "application/json"})
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
func (h rtfHandler) cacheRTFAPIKey(cfg config.TrackerConfig, token string) {
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

func isRTFContentOldEnough(meta api.PreparedMetadata, now time.Time) bool {
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

func rtfReferenceDate(meta api.PreparedMetadata) (time.Time, bool) {
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

func rtfMovieReleaseDate(meta api.PreparedMetadata) (time.Time, bool) {
	if meta.ExternalMetadata.TMDB == nil {
		return time.Time{}, false
	}
	return parseRTFDate(meta.ExternalMetadata.TMDB.ReleaseDate)
}

func rtfMostRecentTVDate(meta api.PreparedMetadata) (time.Time, bool) {
	candidates := make([]time.Time, 0, 8)
	if meta.ExternalMetadata.TMDB != nil {
		if date, ok := parseRTFDate(meta.ExternalMetadata.TMDB.LastAirDate); ok {
			candidates = append(candidates, date)
		}
		if date, ok := parseRTFDate(meta.ExternalMetadata.TMDB.FirstAirDate); ok {
			candidates = append(candidates, date)
		}
	}
	if meta.ExternalMetadata.TVmaze != nil {
		if date, ok := parseRTFDate(meta.ExternalMetadata.TVmaze.Premiered); ok {
			candidates = append(candidates, date)
		}
	}
	if meta.ExternalMetadata.IMDB != nil {
		for _, episode := range meta.ExternalMetadata.IMDB.Episodes {
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

func rtfReferenceYear(meta api.PreparedMetadata) int {
	if date, ok := rtfReferenceDate(meta); ok {
		return date.Year()
	}
	if meta.Release.Year > 0 {
		return meta.Release.Year
	}
	if meta.ExternalMetadata.TMDB != nil && meta.ExternalMetadata.TMDB.Year > 0 {
		return meta.ExternalMetadata.TMDB.Year
	}
	if meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.Year > 0 {
		return meta.ExternalMetadata.IMDB.Year
	}
	if meta.ExternalMetadata.TVmaze != nil {
		if date, ok := parseRTFDate(meta.ExternalMetadata.TVmaze.Premiered); ok {
			return date.Year()
		}
	}
	return 0
}

func rtfCategory(meta api.PreparedMetadata) string {
	candidates := []string{
		meta.ExternalIDs.Category,
		meta.MediaInfoCategory,
	}
	if meta.ExternalMetadata.TMDB != nil {
		candidates = append(candidates, meta.ExternalMetadata.TMDB.Category)
	}
	for _, candidate := range candidates {
		trimmed := strings.ToUpper(strings.TrimSpace(candidate))
		switch trimmed {
		case "MOVIE", "TV":
			return trimmed
		}
	}
	return ""
}
