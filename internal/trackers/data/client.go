// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package data coordinates metadata lookups owned by tracker implementations.
package data

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Result is the normalized metadata returned by a tracker lookup.
type Result = trackers.DataLookupResult

// Client dispatches metadata lookups through tracker registry capabilities.
type Client struct {
	cfg    config.Config
	logger api.Logger
	http   *http.Client

	lookups  map[string]trackers.DataLookup
	registry *trackers.Registry
}

// NewClientWithRegistry snapshots registry data-lookup factories at construction.
// It substitutes a 30-second HTTP client and no-op logger and panics when
// registry is nil.
func NewClientWithRegistry(cfg config.Config, logger api.Logger, httpClient *http.Client, registry *trackers.Registry) *Client {
	if registry == nil {
		panic("tracker data: registry is required")
	}
	if logger == nil {
		logger = api.NopLogger{}
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	client := &Client{
		cfg:      cfg,
		logger:   logger,
		http:     httpClient,
		lookups:  make(map[string]trackers.DataLookup),
		registry: registry,
	}
	for _, name := range registry.Names() {
		if factory, ok := registry.LookupDataFactory(name); ok {
			client.lookups[name] = factory.NewDataLookup(cfg, httpClient, logger)
		}
	}
	return client
}

// CacheKey delegates query dependencies to the adapter. The returned value may
// contain credentials and must be hashed before persistence or diagnostics.
// Unsupported or inapplicable lookups return nil.
func (c *Client) CacheKey(tracker, trackerID string, meta api.UploadSubject, searchFileName string, onlyID, keepImages bool) any {
	normalized := strings.ToUpper(strings.TrimSpace(tracker))
	req := trackers.DataLookupRequest{
		TrackerID:  trackerID,
		Meta:       meta,
		SearchName: searchFileName,
		OnlyID:     onlyID,
		KeepImages: keepImages,
	}
	if lookup, ok := c.lookups[normalized]; ok {
		key := lookup.CacheKey(req)
		if key == nil {
			return nil
		}
		return []any{"tracker-data-v1", normalized, key}
	}
	family, registered := c.registry.LookupFamily(normalized)
	if !registered || family != trackers.FamilyUnit3D {
		return nil
	}
	baseURL, ok := baseURLForTrackerWithConfig(c.cfg, c.registry, normalized)
	if !ok {
		return nil
	}
	filter, value := "id", strings.TrimSpace(trackerID)
	if value == "" {
		filter, value = "file_name", searchFileName
		if strings.TrimSpace(value) == "" {
			return nil
		}
	}
	return []any{"tracker-data-v1", normalized, baseURL, TrackerAPIKey(c.cfg, normalized), filter, value, onlyID, keepImages}
}

// Lookup dispatches to a registered tracker lookup, then falls back to generic
// Unit3D lookup for Unit3D-family trackers. Unknown or unsupported trackers
// return an empty result without error; tracker lookup errors are wrapped with
// the normalized tracker name while preserving any partial result.
func (c *Client) Lookup(
	ctx context.Context,
	tracker string,
	trackerID string,
	meta api.UploadSubject,
	searchFileName string,
	onlyID bool,
	keepImages bool,
) (Result, error) {
	normalized := strings.ToUpper(strings.TrimSpace(tracker))
	if lookup, ok := c.lookups[normalized]; ok {
		result, err := lookup.Lookup(ctx, trackers.DataLookupRequest{
			TrackerID:  trackerID,
			Meta:       meta,
			SearchName: searchFileName,
			OnlyID:     onlyID,
			KeepImages: keepImages,
		})
		if err != nil {
			return result, fmt.Errorf("trackerdata: %s lookup: %w", normalized, err)
		}
		return result, nil
	}
	family, registered := c.registry.LookupFamily(normalized)
	if registered && family == trackers.FamilyUnit3D {
		return c.lookupUnit3D(ctx, normalized, trackerID, searchFileName, onlyID, keepImages)
	}

	return Result{}, nil
}
