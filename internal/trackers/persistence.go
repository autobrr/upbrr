// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"

	"github.com/autobrr/upbrr/pkg/api"
)

// UploadPersistence exposes only persisted state used by tracker preparation,
// description assets, image-host resolution, and upload-ledger finalization.
// Implementations preserve canonical query ordering and atomic slot updates.
type UploadPersistence interface {
	GetDescriptionOverride(context.Context, string, string) (api.DescriptionOverride, error)
	ListDescriptionOverridesByPath(context.Context, string) ([]api.DescriptionOverride, error)
	ListFinalSelections(context.Context, string) ([]api.ScreenshotFinalSelection, error)
	ListTrackerMetadataByPath(context.Context, string) ([]api.TrackerMetadata, error)
	ListUploadedImagesByPath(context.Context, string) ([]api.UploadedImageLink, error)
	ListScreenshotSlotsByPath(context.Context, string) ([]api.ScreenshotSlot, error)
	ReplaceScreenshotSlots(context.Context, string, []api.ScreenshotSlot) error
	UpsertScreenshotSlotVariants(context.Context, string, []api.ScreenshotSlotVariant) error
	DeleteUploadedImage(context.Context, string, string, string) error
	CreateUploadRecord(context.Context, api.UploadRecord) error
	UpdateLatestUploadRecordStatus(context.Context, string, string, string) error
}
