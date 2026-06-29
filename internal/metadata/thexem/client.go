// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thexem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

const defaultBaseURL = "https://thexem.info"

// maxHTTPErrorBodyBytes caps upstream error pages before sanitizing them.
const maxHTTPErrorBodyBytes = 16 * 1024
const maxHTTPErrorDetailLength = 240
const thexemUserAgent = "upbrr"

// ErrUnavailable reports that XEM blocked or refused an otherwise valid lookup.
var ErrUnavailable = errors.New("thexem: unavailable")

var (
	htmlScriptStylePattern = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`)
	htmlTagPattern         = regexp.MustCompile(`(?s)<[^>]+>`)
	ipv4Pattern            = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Pattern            = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}(?:%\w+)?\b`)
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     api.Logger
}

func NewClient(httpClient *http.Client, logger api.Logger) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 12 * time.Second}
	}
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Client{
		baseURL:    defaultBaseURL,
		httpClient: httpClient,
		logger:     logger,
	}
}

func (c *Client) MapAbsoluteEpisode(ctx context.Context, tvdbID, absoluteEp int) (int, int, error) {
	if tvdbID <= 0 || absoluteEp <= 0 {
		return 0, 0, errors.New("thexem: invalid ids")
	}

	params := url.Values{}
	params.Set("id", strconv.Itoa(tvdbID))
	params.Set("origin", "tvdb")
	params.Set("absolute", strconv.Itoa(absoluteEp))
	params.Set("destination", "scene")

	// XEM documents map/single as query parameters; keep this as GET to match
	// the public API shape and avoid form posts through Cloudflare.
	endpoint := c.baseURL + "/map/single?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("thexem: build map/single request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", thexemUserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("thexem: execute map/single request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := httpErrorDetail(resp.Body)
		if isUnavailableHTTP(resp.StatusCode, detail) {
			return 0, 0, fmt.Errorf("thexem: map/single: %w", ErrUnavailable)
		}
		return 0, 0, fmt.Errorf("thexem: map/single http %d: %s", resp.StatusCode, detail)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, 0, fmt.Errorf("thexem: decode map/single response: %w", err)
	}

	season, episode := parseSeasonEpisode(payload)
	if season == 0 || episode == 0 {
		return 0, 0, errors.New("thexem: mapping not found")
	}
	return season, episode, nil
}

func (c *Client) GetSeasonNames(ctx context.Context, tvdbID int) (map[int][]string, error) {
	if tvdbID <= 0 {
		return nil, errors.New("thexem: invalid tvdb id")
	}

	endpoint := fmt.Sprintf("%s/map/names?origin=tvdb&id=%d", c.baseURL, tvdbID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("thexem: build map/names request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", thexemUserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("thexem: execute map/names request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := httpErrorDetail(resp.Body)
		if isUnavailableHTTP(resp.StatusCode, detail) {
			return nil, fmt.Errorf("thexem: map/names: %w", ErrUnavailable)
		}
		return nil, fmt.Errorf("thexem: map/names http %d: %s", resp.StatusCode, detail)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("thexem: decode map/names response: %w", err)
	}
	return parseSeasonNames(payload), nil
}

func (c *Client) MatchSeasonByName(ctx context.Context, tvdbID int, title string) (int, error) {
	namesBySeason, err := c.GetSeasonNames(ctx, tvdbID)
	if err != nil {
		return 0, err
	}
	needle := normalize(title)
	if needle == "" {
		return 0, errors.New("thexem: empty title")
	}

	type candidate struct {
		season int
		score  float64
	}
	scores := make([]candidate, 0, len(namesBySeason))
	for season, names := range namesBySeason {
		best := 0.0
		for _, name := range names {
			score := similarity(needle, normalize(name))
			if score > best {
				best = score
			}
		}
		if best > 0 {
			scores = append(scores, candidate{season: season, score: best})
		}
	}
	if len(scores) == 0 {
		return 0, errors.New("thexem: no season name match")
	}
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].score == scores[j].score {
			return scores[i].season < scores[j].season
		}
		return scores[i].score > scores[j].score
	})
	if scores[0].score < 0.45 {
		return 0, errors.New("thexem: no confident season name match")
	}
	return scores[0].season, nil
}

// httpErrorDetail keeps upstream failure text useful without logging raw HTML,
// script content, IP addresses, or more than maxHTTPErrorBodyBytes from the
// response body.
func httpErrorDetail(body io.Reader) string {
	payload, _ := io.ReadAll(io.LimitReader(body, maxHTTPErrorBodyBytes))
	text := strings.TrimSpace(redaction.RedactValue(string(payload), nil))
	if text == "" {
		return "empty response body"
	}
	if strings.Contains(text, "Cloudflare") && strings.Contains(text, "you have been blocked") {
		return "Cloudflare block page"
	}
	text = htmlScriptStylePattern.ReplaceAllString(text, " ")
	text = htmlTagPattern.ReplaceAllString(text, " ")
	text = ipv4Pattern.ReplaceAllString(text, "[REDACTED_IP]")
	text = ipv6Pattern.ReplaceAllString(text, "[REDACTED_IP]")
	text = strings.Join(strings.Fields(redaction.RedactValue(text, nil)), " ")
	if len(text) <= maxHTTPErrorDetailLength {
		return text
	}
	return strings.TrimSpace(text[:maxHTTPErrorDetailLength]) + "..."
}

func isUnavailableHTTP(status int, detail string) bool {
	return status == http.StatusForbidden && strings.EqualFold(strings.TrimSpace(detail), "Cloudflare block page")
}

func parseSeasonEpisode(payload map[string]any) (int, int) {
	if payload == nil {
		return 0, 0
	}

	lookup := []any{
		payload["data"],
		payload["result"],
		payload["scene"],
		payload["tvdb"],
		payload,
	}
	for _, item := range lookup {
		season, episode := parseSeasonEpisodeFromAny(item)
		if season > 0 && episode > 0 {
			return season, episode
		}
	}
	return 0, 0
}

func parseSeasonEpisodeFromAny(raw any) (int, int) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return 0, 0
	}

	season := anyToInt(obj["season"])
	if season == 0 {
		season = anyToInt(obj["scene_season"])
	}
	episode := anyToInt(obj["episode"])
	if episode == 0 {
		episode = anyToInt(obj["scene_episode"])
	}
	if season > 0 && episode > 0 {
		return season, episode
	}

	for _, key := range []string{"scene", "tvdb", "xem"} {
		if nested, ok := obj[key].(map[string]any); ok {
			season = anyToInt(nested["season"])
			episode = anyToInt(nested["episode"])
			if season > 0 && episode > 0 {
				return season, episode
			}
		}
	}
	return 0, 0
}

func parseSeasonNames(payload map[string]any) map[int][]string {
	result := map[int][]string{}
	if payload == nil {
		return result
	}

	candidates := []any{payload["data"], payload["result"], payload}
	for _, raw := range candidates {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for key, value := range obj {
			season := anyToInt(key)
			if season == 0 {
				continue
			}
			names := anyToStringSlice(value)
			if len(names) > 0 {
				result[season] = append(result[season], names...)
			}
		}
		if len(result) > 0 {
			return dedupe(result)
		}
	}
	return dedupe(result)
}

func dedupe(input map[int][]string) map[int][]string {
	for season, names := range input {
		seen := map[string]struct{}{}
		deduped := make([]string, 0, len(names))
		for _, name := range names {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				continue
			}
			key := strings.ToLower(trimmed)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			deduped = append(deduped, trimmed)
		}
		input[season] = deduped
	}
	return input
}

func anyToStringSlice(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, each := range typed {
			if str := strings.TrimSpace(fmt.Sprint(each)); str != "" {
				out = append(out, str)
			}
		}
		return out
	case []string:
		return append([]string{}, typed...)
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	default:
		return nil
	}
}

func anyToInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		i, _ := typed.Int64()
		return int(i)
	case string:
		trimmed := strings.TrimSpace(typed)
		i, _ := strconv.Atoi(trimmed)
		return i
	default:
		i, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
		return i
	}
}

func normalize(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return ""
	}
	lower = strings.ReplaceAll(lower, "_", " ")
	lower = strings.ReplaceAll(lower, ".", " ")
	lower = strings.ReplaceAll(lower, "-", " ")
	return strings.Join(strings.Fields(lower), " ")
}

func similarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return 0.9
	}
	aTokens := strings.Fields(a)
	bTokens := strings.Fields(b)
	if len(aTokens) == 0 || len(bTokens) == 0 {
		return 0
	}
	common := 0
	for _, token := range aTokens {
		for _, other := range bTokens {
			if token == other {
				common++
				break
			}
		}
	}
	return (2.0 * float64(common)) / float64(len(aTokens)+len(bTokens))
}
