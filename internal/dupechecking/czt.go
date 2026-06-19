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
	"github.com/autobrr/upbrr/pkg/api"
)

const cztDefaultBaseURL = "https://czteam.me"

// cztHandler searches CZTeam for existing releases via its JSON search API:
//
//	GET {base}/api.php?action=search-torrents&type=name&query=...&passkey=<16-hex>
//
// The endpoint authenticates with the user's 16-hex passkey (the same one used
// by announce/download and the upload fallback) and returns a bare JSON array
// of torrent objects.
type cztHandler struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

// Search queries CZTeam by release title. Missing tracker config, unsupported
// auth, or title returns skip notes; remote or response-shape failures return
// errors so duplicate filtering fails closed.
func (h cztHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	tracker, ok := trackerCfg(h.cfg, "CZT")
	if !ok {
		return nil, []string{noteSkip("missing passkey for tracker")}, nil
	}
	passkey := strings.TrimSpace(tracker.Passkey)
	if passkey == "" {
		if strings.TrimSpace(tracker.APIKey) != "" {
			return nil, []string{noteSkip("CZT dupe search requires passkey credentials; API key upload token is not supported by the search API")}, nil
		}
		return nil, []string{noteSkip("missing passkey for tracker")}, nil
	}

	query := strings.TrimSpace(meta.Release.Title)
	if query == "" {
		query = strings.TrimSpace(meta.ReleaseName)
	}
	if query == "" {
		return nil, []string{noteSkip("missing title for CZT dupe search")}, nil
	}

	base := strings.TrimRight(strings.TrimSpace(tracker.URL), "/")
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
		return nil, nil, fmt.Errorf("CZT search failed: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, nil, fmt.Errorf("CZT search failed: HTTP status %d", status)
	}
	if payload == nil {
		return nil, nil, errors.New("CZT search failed: empty response")
	}

	// On success the API returns a JSON array; an auth/validation failure returns
	// a JSON object ({error, status}) instead, which is a failed dupe check.
	items, ok := payload.([]any)
	if !ok {
		return nil, nil, errors.New("CZT search failed: unexpected response shape")
	}

	entries := make([]api.DupeEntry, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringFromAny(item["name"]))
		if name == "" {
			continue
		}
		entry := api.DupeEntry{
			Name: name,
			ID:   stringFromAny(item["id"]),
			Link: stringFromAny(item["url"]),
		}
		if size := intFromAny(item["size"]); size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = size
		}
		entries = append(entries, entry)
	}
	return entries, nil, nil
}
