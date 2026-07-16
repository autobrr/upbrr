// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package czt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const cztDefaultBaseURL = "https://czteam.me"

// cztHandler searches CZTeam for existing releases via its JSON search API:
//
//	GET {base}/api.php?action=search-torrents&type=name&query=...&passkey=<16-hex>
//
// The endpoint authenticates with the user's passkey (the same one used by
// upload, announce, and download) and normally returns torrent objects either
// as a bare array or under a small response wrapper.
type cztHandler struct {
	cfg     config.Config
	http    *http.Client
	logger  api.Logger
	baseURL string
}

func (definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return cztHandler{
		cfg:     cfg,
		http:    httpClient,
		logger:  logger,
		baseURL: cztDefaultBaseURL,
	}
}

// Search queries CZTeam by the most specific prepared release name available.
func (h cztHandler) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	tracker, ok := cztTrackerConfig(h.cfg)
	if !ok {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing passkey for tracker", nil)
	}
	passkey := strings.TrimSpace(tracker.Passkey)
	if passkey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing passkey for tracker", nil)
	}

	query := cztSearchQuery(meta)
	if query == "" {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing title for CZT dupe search", nil)
	}

	base := strings.TrimRight(strings.TrimSpace(h.baseURL), "/")
	if base == "" {
		base = cztDefaultBaseURL
	}

	params := url.Values{}
	params.Set("action", "search-torrents")
	params.Set("type", "name")
	params.Set("query", query)
	params.Set("passkey", passkey)
	params.Set("incldead", "1")

	status, payload, err := cztJSONGet(ctx, h.http, base+"/api.php", params)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return dupe.Failed(dupe.FailureRequest, "CZT search canceled", ctxErr)
		}
		return dupe.Failed(dupe.FailureRequest, "CZT search failed", redactedError(err))
	}
	if err := ctx.Err(); err != nil {
		return dupe.Failed(dupe.FailureRequest, "CZT search canceled", err)
	}
	if status < 200 || status >= 300 {
		return dupe.Failed(dupe.FailureResponseStatus, "CZT search failed", fmt.Errorf("HTTP status %d", status))
	}
	if payload == nil {
		return dupe.Failed(dupe.FailureResponseParse, "CZT search failed: empty response", errors.New("CZT search failed: empty response"))
	}

	items, ok := cztResultItems(payload)
	if !ok {
		return dupe.Failed(
			dupe.FailureResponseParse,
			"CZT search failed: unexpected response shape",
			errors.New("CZT search failed: unexpected response shape"),
		)
	}

	entries := make([]api.DupeEntry, 0, len(items))
	for _, raw := range items {
		if err := ctx.Err(); err != nil {
			return dupe.Failed(dupe.FailureRequest, "CZT search canceled", err)
		}
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringFromAny(firstValue(item,
			"name", "Name",
			"torrent_name", "torrentName", "torrentname",
			"release_name", "releaseName",
			"title", "Title",
			"filename", "fileName",
		)))
		if name == "" {
			continue
		}
		entry := api.DupeEntry{
			Name: name,
			ID:   stringFromAny(firstValue(item, "id", "ID", "torrent_id", "torrentId", "torrentid")),
			Link: stringFromAny(firstValue(item, "url", "URL", "details_url", "detailsUrl", "link")),
		}
		if size := intFromAny(firstValue(item, "size", "Size", "size_bytes", "sizeBytes")); size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = size
		}
		entries = append(entries, entry)
	}
	return dupe.Resolved(entries, nil)
}

// cztSearchQuery prefers exact upload/client names before media titles because
// CZTeam's name search matches torrent release names.
func cztSearchQuery(meta api.DuplicateSubject) string {
	return metautil.FirstNonEmptyTrimmed(
		meta.SceneName,
		meta.ReleaseName,
		meta.Release.Title,
		meta.Filename,
	)
}

// cztResultItems extracts torrent rows from the CZTeam search response. Auth or
// validation objects without a recognizable result container return false so
// callers fail the dupe check instead of treating the search as empty.
func cztResultItems(payload any) ([]any, bool) {
	if items, ok := payload.([]any); ok {
		return items, true
	}
	obj, ok := payload.(map[string]any)
	if !ok {
		return nil, false
	}
	for _, key := range []string{"data", "results", "torrents", "response"} {
		value, ok := obj[key]
		if !ok {
			continue
		}
		if items, ok := value.([]any); ok {
			return items, true
		}
		if nested, ok := cztResultItems(value); ok {
			return nested, true
		}
	}
	if values, ok := cztObjectItems(obj); ok {
		return values, true
	}
	return nil, false
}

// cztObjectItems accepts API shapes that key torrent rows by id. Mixed scalar
// fields are rejected because those shapes are usually error/status payloads.
func cztObjectItems(obj map[string]any) ([]any, bool) {
	if len(obj) == 0 {
		return nil, false
	}
	items := make([]any, 0, len(obj))
	for _, value := range obj {
		if _, ok := value.(map[string]any); !ok {
			return nil, false
		}
		items = append(items, value)
	}
	return items, true
}

// redactedError returns an error whose text has passed through the repository
// redactor, preventing passkeys embedded in transport errors from surfacing in
// duplicate-check results or progress messages.
func redactedError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(redaction.RedactValue(err.Error(), nil))
}

func cztJSONGet(ctx context.Context, client *http.Client, endpoint string, params url.Values) (int, any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("CZT create request: %w", err)
	}
	req.URL.RawQuery = params.Encode()
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("CZT request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, nil, nil
	}
	var payload any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return resp.StatusCode, nil, fmt.Errorf("dupechecking: decode JSON GET response: %w", err)
	}
	return resp.StatusCode, payload, nil
}

func cztTrackerConfig(cfg config.Config) (config.TrackerConfig, bool) {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "CZT") {
			return entry, true
		}
	}
	return config.TrackerConfig{}, false
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
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
