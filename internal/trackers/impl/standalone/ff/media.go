// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

import (
	"context"
	"io"
	"net/http"
	"path/filepath"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveExtraFiles(ctx context.Context, meta api.UploadSubject) []commonhttp.FileField {
	files := make([]commonhttp.FileField, 0, 2)
	if ctx == nil {
		return files
	}
	if poster := resolvePoster(meta); poster != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, poster, nil)
		if err == nil {
			resp, err := httpclient.New(httpclient.DefaultTimeout).Do(req)
			if err == nil {
				defer resp.Body.Close()
				if body, err := io.ReadAll(resp.Body); err == nil && len(body) > 0 {
					files = append(files, commonhttp.FileField{
						FieldName: "poster",
						FileName:  "poster.jpg",
						Content:   body,
					})
				}
			}
		}
	}
	dir := filepath.Dir(metautil.FirstNonEmptyTrimmed(meta.MediaInfoTextPath, meta.SourcePath))
	if payload, path, err := commonhttp.ReadFirstMatching(dir, "*.nfo"); err == nil {
		files = append(files, commonhttp.FileField{
			FieldName: "nfo",
			FileName:  filepath.Base(path),
			Content:   payload,
		})
	}
	return files
}
