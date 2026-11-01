// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/pathing"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// InspectHDRComparison collects file-specific probe evidence without preparing
// a release or requesting provider metadata. The caller verifies source identity
// before and after this bounded inspection.
func (s *Service) InspectHDRComparison(ctx context.Context, release api.ReleaseRef, path, fingerprint string) (api.HDRSourceEvidence, error) {
	root, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return api.HDRSourceEvidence{}, fmt.Errorf("HDR inspection temp root: %w", err)
	}
	doc, _, err := inspectOwnedFileMediaInfoReports(ctx, s.mi, path, fingerprint, root, release.SourcePath, true)
	if err != nil {
		return api.HDRSourceEvidence{}, err
	}
	return mediafacts.HDRSourceFromMediaInfo(doc), nil
}

// InspectHDRFile shares bounded per-file evidence inspection with standalone CLI analysis.
func InspectHDRFile(ctx context.Context, exporter mediainfo.Exporter, path, fingerprint, root string) (api.HDRSourceEvidence, error) {
	doc, _, err := inspectOwnedFileMediaInfoReports(ctx, exporter, path, fingerprint, root, path, false)
	if err != nil {
		return api.HDRSourceEvidence{}, err
	}
	return mediafacts.HDRSourceFromMediaInfo(doc), nil
}

func inspectReleaseMediaInfoReports(
	ctx context.Context,
	exporter mediainfo.Exporter,
	meta preparationstate.State,
	path, fingerprint, root string,
) (mediaInfoDoc, mediainfo.Result, error) {
	return inspectOwnedFileMediaInfoReports(ctx, exporter, path, fingerprint, root, meta.SourcePath, !pathing.SamePath(path, meta.VideoPath))
}

func inspectOwnedFileMediaInfoReports(
	ctx context.Context,
	exporter mediainfo.Exporter,
	path, fingerprint, root, owner string,
	secondary bool,
) (mediaInfoDoc, mediainfo.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := exporter.Export(ctx, mediainfo.Request{
		SourcePath:        path,
		VideoPath:         path,
		TempRoot:          root,
		OwnerSource:       owner,
		SourceFingerprint: fingerprint,
		Secondary:         secondary,
		Release:           ParseReleaseInfo(path),
	})
	if err != nil {
		return mediaInfoDoc{}, mediainfo.Result{}, fmt.Errorf("HDR file inspection: %w", err)
	}
	file, err := os.Open(result.JSONPath)
	if err != nil {
		return mediaInfoDoc{}, mediainfo.Result{}, fmt.Errorf("open HDR file inspection: %w", err)
	}
	defer file.Close()
	const limit = 8 << 20
	payload, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return mediaInfoDoc{}, mediainfo.Result{}, fmt.Errorf("read HDR file inspection: %w", err)
	}
	if len(payload) > limit {
		return mediaInfoDoc{}, mediainfo.Result{}, errors.New("HDR file inspection exceeds report limit")
	}
	if err := ctx.Err(); err != nil {
		return mediaInfoDoc{}, mediainfo.Result{}, fmt.Errorf("HDR file inspection canceled: %w", err)
	}
	var doc mediafacts.MediaInfoDocument
	if err := json.Unmarshal(payload, &doc); err != nil {
		return mediaInfoDoc{}, mediainfo.Result{}, fmt.Errorf("decode HDR file inspection: %w", err)
	}
	return doc, result, nil
}
