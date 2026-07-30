// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/internal/jsondupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	endpoint string
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		endpoint: "https://digitalcore.club/api/v1/torrents",
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	apiKey := dcAPIKey(s.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	if meta.Identity.IMDBID == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb id for DC dupe search", nil)
	}
	return jsondupe.Search(ctx, s.http, jsondupe.ListSpec{
		Endpoint:       s.endpoint,
		Query:          url.Values{"searchText": {"tt" + strconv.Itoa(meta.Identity.IMDBID)}},
		Headers:        http.Header{"X-Api-Key": {apiKey}},
		IDField:        "id",
		NameField:      "name",
		SizeField:      "size",
		Link:           func(id string) string { return "https://digitalcore.club/torrent/" + id + "/" },
		FailureMessage: "DC search failed",
	})
}

func dcAPIKey(cfg config.Config) string {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "DC") {
			return strings.TrimSpace(entry.APIKey)
		}
	}
	return ""
}
