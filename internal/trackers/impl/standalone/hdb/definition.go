// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"

	"github.com/autobrr/upbrr/internal/trackers"
)

// DataLookupConfigured reports whether HDB metadata lookup credentials are available.
func (d *Definition) DataLookupConfigured(cfg config.Config) bool {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "HDB") {
			return strings.TrimSpace(entry.Username) != "" && strings.TrimSpace(entry.Passkey) != ""
		}
	}
	return false
}

func prepareDescription(ctx context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return trackers.DescriptionResult{}, fmt.Errorf("trackers: HDB description assets: %w", err)
		}
		if req.Logger != nil {
			req.Logger.Warnf("trackers: HDB description assets failed: %v", err)
		}
		assets = trackers.DescriptionAssets{}
	}

	description := strings.TrimSpace(assets.Description)
	if !assets.Final {
		description, err = BuildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets.Description, assets.MenuImages, assets.Screenshots)
		if err != nil {
			return trackers.DescriptionResult{}, fmt.Errorf("trackers: HDB description build: %w", err)
		}
	}

	if strings.TrimSpace(description) == "" && req.Logger != nil {
		req.Logger.Infof("trackers: HDB preparation description empty")
	}

	return trackers.DescriptionResult{
		Group:       "hdb",
		Description: description,
	}, nil
}
