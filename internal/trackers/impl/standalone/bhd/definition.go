// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

func bannedGroups() []string {
	return []string{
		"Sicario", "TOMMY", "x0r", "nikt0", "FGT", "d3g", "MeGusta", "YIFY", "tigole", "TEKNO3D",
		"C4K", "RARBG", "4K4U", "EASports", "ReaLHD", "Telly", "AOC", "WKS", "SasukeducK", "CRUCiBLE",
		"iFT", "ProRes", "MezRips", "Flights", "BiTOR", "iVy", "QxR", "SyncUP", "OFT", "TGS",
	}
}

// DataLookupConfigured reports whether BHD metadata lookup credentials are available.
func (d *Definition) DataLookupConfigured(cfg config.Config) bool {
	entry, _ := bhdConfig(cfg)
	return len(strings.TrimSpace(entry.APIKey)) >= minDataTokenLength && len(strings.TrimSpace(entry.BhdRSSKey)) >= minDataTokenLength
}

func prepareDescription(ctx context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	var err error
	var assets trackers.DescriptionAssets
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return trackers.DescriptionResult{}, fmt.Errorf("trackers: %w", err)
			}
			if req.Logger != nil {
				req.Logger.Warnf("trackers: BHD description assets failed: %v", err)
			}
			assets = trackers.DescriptionAssets{}
		}
	}

	description := buildDescription(req.Meta, req.Runtime.DescriptionConfig(), assets)
	return trackers.DescriptionResult{
		Group:       "bhd",
		Description: description,
	}, nil
}
