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
		maxPages:  100,
	}
	if registry := deps.Registry(); registry != nil {
		if policy, ok := registry.LookupDupePolicy(deps.Tracker()); ok && policy.SearchScope.MaxPages > 0 {
			searcher.maxPages = policy.SearchScope.MaxPages
		}
	}
	return searcher
}

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
		Complete: search.Complete,
		Pages:    search.Pages,
		Scope:    "work_category",
		Warnings: notes,
	})
}
