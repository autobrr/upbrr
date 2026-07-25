// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"path/filepath"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveNFO(meta api.UploadSubject) (commonhttp.FileField, bool) {
	dir := filepath.Dir(metautil.FirstNonEmptyTrimmed(meta.MediaInfoTextPath, meta.SourcePath))
	payload, path, err := commonhttp.ReadFirstMatching(dir, "*.nfo")
	if err != nil {
		return commonhttp.FileField{}, false
	}
	return commonhttp.FileField{
		FieldName: "nfo",
		FileName:  filepath.Base(path),
		Content:   payload,
	}, true
}
