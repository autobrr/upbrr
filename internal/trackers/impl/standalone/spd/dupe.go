// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/internal/jsondupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	endpoint string
}

// newDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		endpoint: "https://speedapp.io/api/torrent",
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	apiKey := spdAPIKey(s.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	params := url.Values{}
	workScope := dupe.WorkScopeProviderID
	switch {
	case meta.Identity.IMDBID != 0:
		params.Set("imdbId", providerid.IMDb(meta.Identity.IMDBID).Prefixed())
	case strings.TrimSpace(meta.Release.Title) != "":
		workScope = dupe.WorkScopeTitle
		params.Set("search", strings.TrimSpace(meta.Release.Title))
	case meta.Projection != nil:
		workScope = dupe.WorkScopeTitle
		params.Set("search", dupe.ProjectedSearchName(meta))
	default:
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb/title for SPD dupe search", nil)
	}
	return jsondupe.Search(ctx, s.http, jsondupe.ListSpec{
		Endpoint:       s.endpoint,
		Query:          params,
		Headers:        http.Header{"Authorization": {apiKey}, "Accept": {"application/json"}},
		IDField:        "id",
		NameField:      "name",
		SizeField:      "size",
		Link:           func(id string) string { return "https://speedapp.io/browse/" + id + "/" },
		FailureMessage: "SPD search failed",
		SearchEvidence: dupe.SearchEvidence{
			Complete:  true,
			WorkScope: workScope,
			Pages:     1,
			Scope:     "result_array",
		},
	})
}

func spdAPIKey(cfg config.Config) string {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "SPD") {
			return strings.TrimSpace(entry.APIKey)
		}
	}
	return ""
}
