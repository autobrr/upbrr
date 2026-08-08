// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	trackerID string
	cfg       config.Config
	client    *trackerdata.Client
	maxPages  int
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func (d *Definition) NewDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	searcher := &dupeSearcher{
		trackerID: deps.Tracker(),
		cfg:       cfg,
		client:    trackerdata.NewClientWithRegistry(cfg, logger, httpClient, deps.Registry()),
		maxPages:  deps.MaxPages(100),
	}
	return searcher
}

// Search queries the site's bounded evidence scope and preserves completeness
// and pagination metadata for downstream duplicate decisions.
func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	tracker := s.trackerID
	if strings.TrimSpace(trackerdata.TrackerAPIKey(s.cfg, tracker)) == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	params := buildDupeSearchParams(meta, tracker)
	if len(params) == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing required metadata for dupe search", nil)
	}
	search, err := s.client.SearchTorrentsWithEvidenceBound(
		ctx,
		tracker,
		params,
		strings.TrimSpace(meta.DiscType) != "",
		s.maxPages,
	)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, "duplicate search failed", err)
	}
	notes := []string(nil)
	if search.Warning != "" {
		notes = []string{search.Warning}
	}
	return dupe.ResolvedWithSearch(search.Entries, notes, dupe.SearchEvidence{
		Complete:       search.Complete,
		WorkScope:      dupe.WorkScopeProviderID,
		Pages:          search.Pages,
		Scope:          "work_category",
		Warnings:       notes,
		WrongWorkCount: search.WrongWorkCount,
	})
}
