// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
)

func prepareDescription(_ context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	var (
		err    error
		assets trackers.DescriptionAssets
	)
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			assets = trackers.DescriptionAssets{}
		}
	}
	description := buildDescription(assets)
	return trackers.DescriptionResult{Group: "rtf", Description: description}, nil
}
