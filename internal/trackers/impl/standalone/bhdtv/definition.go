// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func prepareDescription(ctx context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}

	description := buildDescription(assets)
	return trackers.DescriptionResult{
		Group:       "bhdtv",
		Description: description,
	}, nil
}

type bhdtvDuplicateAdapter struct{}

func (bhdtvDuplicateAdapter) Search(context.Context, api.DuplicateSubject) dupe.AdapterResult {
	return dupe.NotRun(dupe.NotRunManualCheckRequired, "BHDTV duplicate search requires a manual check", nil)
}
