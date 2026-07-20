// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

// DataLookupConfigured reports whether PTP metadata lookup credentials are available.
func (d *Definition) DataLookupConfigured(cfg config.Config) bool {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "PTP") {
			return strings.TrimSpace(entry.PTPAPIUser) != "" && strings.TrimSpace(entry.PTPAPIKey) != ""
		}
	}
	return false
}
func bannedGroups() []string {
	return []string{
		"aXXo",
		"BMDru",
		"BRrip",
		"CM8",
		"CrEwSaDe",
		"CTFOH",
		"d3g",
		"DNL",
		"FaNGDiNG0",
		"HD2DVD",
		"HDT",
		"HDTime",
		"ION10",
		"iPlanet",
		"KiNGDOM",
		"mHD",
		"mSD",
		"nHD",
		"nikt0",
		"nSD",
		"NhaNc3",
		"OFT",
		"PRODJi",
		"SANTi",
		"SPiRiT",
		"STUTTERSHIT",
		"ViSION",
		"VXT",
		"WAF",
		"x0r",
		"YIFY",
		"LAMA",
		"WORLD",
	}
}

func prepareDescription(ctx context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		resolvedAssets, err := trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return trackers.DescriptionResult{}, fmt.Errorf("trackers: %w", err)
			}
			if req.Logger != nil {
				req.Logger.Warnf("trackers: PTP description assets failed: %v", err)
			}
		} else {
			assets = resolvedAssets
		}
	}

	description := strings.TrimSpace(assets.Description)
	if !assets.Final {
		description = buildDescription(req.Meta, req.TrackerConfig, req.Runtime.DescriptionConfig(), assets)
	}
	if strings.TrimSpace(description) == "" && req.Logger != nil {
		req.Logger.Infof("trackers: PTP preparation description empty")
	}

	return trackers.DescriptionResult{
		Group:       "ptp",
		Description: description,
	}, nil
}
