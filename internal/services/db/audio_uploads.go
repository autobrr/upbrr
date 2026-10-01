// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// CloneAudioAnalysisUploads preserves original links and copies only audio records
// from the verified prior generation. Account and usage scopes are unchanged;
// consumers must still verify the current hosting account before reusing a link.
// An absent current preparation permits copying retained records only; purged
// history has no source rows to copy. A different current generation is rejected.
func (r *SQLiteRepository) CloneAudioAnalysisUploads(
	ctx context.Context,
	prior api.ReleaseRef,
	binding api.PreparedMediaBinding,
	paths map[string]string,
) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	bound, err := normalizePreparedMediaBinding(binding)
	if err != nil || prior.SourcePath != bound.SourcePath || prior.Generation == 0 {
		return internalerrors.ErrInvalidInput
	}
	for from, to := range paths {
		if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" || from == to {
			return internalerrors.ErrInvalidInput
		}
	}
	return r.withWriteTx(ctx, "clone audio uploads", func(tx *sql.Tx) error {
		var current api.PreparedGeneration
		err := tx.QueryRowContext(ctx, `SELECT generation FROM prepared_release_current WHERE source_path = ?`, bound.SourcePath).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("db clone audio uploads: current generation: %w", err)
		}
		if err == nil && current != bound.PreparedGeneration {
			return internalerrors.ErrInvalidInput
		}
		for from, to := range paths {
			_, err := tx.ExecContext(ctx, `INSERT INTO uploaded_images (
    source_path, prepared_media_fingerprint, prepared_generation, disc_id,
    image_path, host, usage_scope, purpose, account_scope, img_url, raw_url, web_url, size_bytes, uploaded_at
   ) SELECT source_path, ?, ?, disc_id, ?, host, usage_scope, purpose, account_scope, img_url, raw_url, web_url, size_bytes, uploaded_at
    FROM uploaded_images WHERE source_path = ? AND prepared_generation = ? AND image_path = ? AND purpose = ?
    ON CONFLICT(source_path, usage_scope, host, image_path) DO NOTHING`,
				bound.PreparedMediaFingerprint, bound.PreparedGeneration, to, bound.SourcePath, prior.Generation, from,
				api.ScreenshotPurposeAudioAnalysis)
			if err != nil {
				return fmt.Errorf("db clone audio uploads: %w", err)
			}
		}
		return nil
	})
}
