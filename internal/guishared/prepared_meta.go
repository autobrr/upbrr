// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guishared

import (
	"context"

	"github.com/autobrr/upbrr/pkg/api"
)

type PreparedMetaExporter interface {
	ExportGUICachedPreparedMeta(ctx context.Context, req api.Request) (api.PreparedMetadata, bool, error)
}

type PreparedMetaImporter interface {
	ImportPreparedMetadataForGUI(ctx context.Context, req api.Request, meta api.PreparedMetadata) error
}

func SeedRunCorePreparedMeta(ctx context.Context, source api.Core, target api.Core, req api.Request) error {
	exporter, ok := source.(PreparedMetaExporter)
	if !ok {
		return nil
	}
	importer, ok := target.(PreparedMetaImporter)
	if !ok {
		return nil
	}

	meta, found, err := exporter.ExportGUICachedPreparedMeta(ctx, req)
	if err != nil || !found {
		return err
	}
	return importer.ImportPreparedMetadataForGUI(ctx, req, meta)
}
