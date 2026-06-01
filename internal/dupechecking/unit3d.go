// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackerdata"
	"github.com/autobrr/upbrr/pkg/api"
)

type unit3dHandler struct {
	cfg     config.Config
	tracker *trackerdata.Client
}

func (h unit3dHandler) Search(ctx context.Context, meta api.PreparedMetadata, tracker string) ([]api.DupeEntry, []string, error) {
	if strings.TrimSpace(trackerdata.TrackerAPIKey(h.cfg, tracker)) == "" {
		return nil, []string{noteSkip("missing api_key for tracker")}, nil
	}
	params := buildUnit3DSearchParams(meta, tracker)
	if len(params) == 0 {
		return nil, []string{"missing required metadata for dupe search"}, nil
	}
	entries, warning, err := h.tracker.SearchTorrents(ctx, tracker, params, strings.TrimSpace(meta.DiscType) != "")
	if err != nil {
		return nil, nil, fmt.Errorf("dupechecking: %w", err)
	}
	if warning != "" {
		return entries, []string{warning}, nil
	}
	return entries, nil, nil
}
