// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type UploadRequest struct {
	Tracker       string
	Meta          api.PreparedMetadata
	TrackerConfig config.TrackerConfig
	AppConfig     config.Config
	Logger        api.Logger
	Repo          db.MetadataRepository
	Images        api.ImageHostingService
	Assets        *DescriptionAssets
}

type Definition interface {
	Name() string
	Upload(ctx context.Context, req UploadRequest) (api.UploadSummary, error)
}
