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

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/redaction"
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

// Search queries CZTeam by the most specific prepared release name available.
// Missing tracker config, passkey, or title returns skip notes; remote or
// response-shape failures return errors so duplicate filtering fails closed.
func (h cztHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	tracker, ok := trackerCfg(h.cfg, "CZT")
	if !ok {
		return nil, []string{noteSkip("missing passkey for tracker")}, nil
	}
	passkey := strings.TrimSpace(tracker.Passkey)
	if passkey == "" {
		return nil, []string{noteSkip("missing passkey for tracker")}, nil
	}

	query := cztSearchQuery(meta)
	if query == "" {
		return nil, []string{noteSkip("missing title for CZT dupe search")}, nil
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

	status, payload, err := doJSONGetAny(ctx, h.http, base+"/api.php", params, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("CZT search failed: %w", redactedError(err))
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context canceled: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, nil, fmt.Errorf("CZT search failed: HTTP status %d", status)
	}
	if payload == nil {
		return nil, nil, errors.New("CZT search failed: empty response")
	}

	items, ok := cztResultItems(payload)
	if !ok {
		return nil, nil, errors.New("CZT search failed: unexpected response shape")
	}

	entries := make([]api.DupeEntry, 0, len(items))
	for _, raw := range items {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("context canceled: %w", err)
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
	return entries, nil, nil
}

// cztSearchQuery prefers exact upload/client names before media titles because
// CZTeam's name search matches torrent release names.
func cztSearchQuery(meta api.PreparedMetadata) string {
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
