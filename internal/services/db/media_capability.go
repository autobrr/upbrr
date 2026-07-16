// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

// LoadMediaAssetSnapshot assembles the ordered media records for one release.
// Each query already returns caller-owned values; nested slot variants are
// cloned so callers cannot share mutable slice backing storage.
func (r *SQLiteRepository) LoadMediaAssetSnapshot(ctx context.Context, path string) (api.MediaAssetSnapshot, error) {
	snapshot := api.MediaAssetSnapshot{}
	var err error
	if snapshot.Screenshots, err = r.ListScreenshotsByPath(ctx, path); err != nil {
		return api.MediaAssetSnapshot{}, fmt.Errorf("db media snapshot screenshots: %w", err)
	}
	if snapshot.FinalSelections, err = r.ListFinalSelections(ctx, path); err != nil {
		return api.MediaAssetSnapshot{}, fmt.Errorf("db media snapshot final selections: %w", err)
	}
	if snapshot.ScreenshotSlots, err = r.ListScreenshotSlotsByPath(ctx, path); err != nil {
		return api.MediaAssetSnapshot{}, fmt.Errorf("db media snapshot screenshot slots: %w", err)
	}
	for index := range snapshot.ScreenshotSlots {
		snapshot.ScreenshotSlots[index].Variants = append([]api.ScreenshotSlotVariant(nil), snapshot.ScreenshotSlots[index].Variants...)
	}
	if snapshot.UploadedImages, err = r.ListUploadedImagesByPath(ctx, path); err != nil {
		return api.MediaAssetSnapshot{}, fmt.Errorf("db media snapshot uploaded images: %w", err)
	}
	return snapshot, nil
}
