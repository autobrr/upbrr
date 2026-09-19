// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// migrateAddMediaReuseAssociations adds strong reusable-media evidence. It
// deliberately does not backfill weak historical media rows.
func migrateAddMediaReuseAssociations(ctx context.Context, exec migrationExecutor) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS media_reusable_assets (
			source_path TEXT NOT NULL,
			prepared_media_fingerprint TEXT NOT NULL,
			prepared_generation INTEGER NOT NULL,
			compatibility_key TEXT NOT NULL,
			capture_fingerprint TEXT NOT NULL,
			content_sha256 TEXT NOT NULL,
			kind TEXT NOT NULL,
			purpose TEXT NOT NULL,
			disc_id TEXT NOT NULL DEFAULT "",
			image_path TEXT NOT NULL,
			image_index INTEGER NOT NULL DEFAULT 0,
			timestamp_seconds REAL NOT NULL DEFAULT 0,
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			selected INTEGER NOT NULL DEFAULT 1,
			sort_order INTEGER NOT NULL DEFAULT 0,
			captured_at TEXT NOT NULL,
			PRIMARY KEY (source_path, prepared_media_fingerprint, prepared_generation, image_path)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_media_reusable_assets_compatibility
			ON media_reusable_assets (compatibility_key, capture_fingerprint, sort_order)`,
		`CREATE TABLE IF NOT EXISTS media_reusable_tombstones (
			compatibility_key TEXT NOT NULL,
			capture_fingerprint TEXT NOT NULL,
			content_sha256 TEXT NOT NULL,
			deleted_at TEXT NOT NULL,
			PRIMARY KEY (compatibility_key, capture_fingerprint, content_sha256)
		)`,
		`CREATE TABLE IF NOT EXISTS media_reusable_hosted_links (
			source_path TEXT NOT NULL,
			prepared_media_fingerprint TEXT NOT NULL,
			prepared_generation INTEGER NOT NULL,
			compatibility_key TEXT NOT NULL,
			capture_fingerprint TEXT NOT NULL,
			content_sha256 TEXT NOT NULL,
			host TEXT NOT NULL,
			usage_scope TEXT NOT NULL,
			account_scope TEXT NOT NULL,
			img_url TEXT NOT NULL DEFAULT "",
			raw_url TEXT NOT NULL DEFAULT "",
			web_url TEXT NOT NULL DEFAULT "",
			size_bytes INTEGER NOT NULL DEFAULT 0,
			uploaded_at TEXT NOT NULL,
			PRIMARY KEY (
				source_path, prepared_media_fingerprint, prepared_generation,
				capture_fingerprint, content_sha256, host, usage_scope, account_scope
			)
		)`,
	}
	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db: add media reuse associations: %w", err)
		}
	}
	return nil
}

func migrateAddReusableMediaCommits(ctx context.Context, exec migrationExecutor) error {
	_, err := exec.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS media_reusable_commits (
		source_path TEXT NOT NULL,
		prepared_media_fingerprint TEXT NOT NULL,
		prepared_generation INTEGER NOT NULL,
		workflow_id TEXT NOT NULL,
		media_id TEXT NOT NULL,
		media_revision INTEGER NOT NULL,
		committed_at TEXT NOT NULL,
		PRIMARY KEY (workflow_id, media_id, media_revision)
	)`)
	if err != nil {
		return fmt.Errorf("db: add reusable media commits: %w", err)
	}
	return nil
}

func migrateAddReusableMediaTombstoneSources(ctx context.Context, exec migrationExecutor) error {
	hasSourcePath, err := tableColumnExists(ctx, exec, "media_reusable_tombstones", "source_path")
	if err != nil {
		return fmt.Errorf("db: inspect reusable media tombstones: %w", err)
	}
	if hasSourcePath {
		if _, err := exec.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_media_reusable_tombstones_source_path
			ON media_reusable_tombstones (source_path)`); err != nil {
			return fmt.Errorf("db: index reusable media tombstone sources: %w", err)
		}
		return nil
	}
	for _, statement := range []string{
		`ALTER TABLE media_reusable_tombstones RENAME TO media_reusable_tombstones_legacy`,
		`CREATE TABLE media_reusable_tombstones (
			source_path TEXT NOT NULL DEFAULT '',
			compatibility_key TEXT NOT NULL,
			capture_fingerprint TEXT NOT NULL,
			content_sha256 TEXT NOT NULL,
			deleted_at TEXT NOT NULL,
			PRIMARY KEY (compatibility_key, capture_fingerprint, content_sha256, source_path)
		)`,
		`INSERT INTO media_reusable_tombstones (
			source_path, compatibility_key, capture_fingerprint, content_sha256, deleted_at
		)
		SELECT '', compatibility_key, capture_fingerprint, content_sha256, deleted_at
		FROM media_reusable_tombstones_legacy`,
		`DROP TABLE media_reusable_tombstones_legacy`,
		`CREATE INDEX IF NOT EXISTS idx_media_reusable_tombstones_source_path
			ON media_reusable_tombstones (source_path)`,
	} {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db: add reusable media tombstone sources: %w", err)
		}
	}
	return nil
}

// ReplaceReusableMediaAssets records only media independently verified under a
// strong source compatibility key. It replaces the association set for the
// exact prepared generation without touching historical hosted links.
func (r *SQLiteRepository) ReplaceReusableMediaAssets(
	ctx context.Context,
	binding api.PreparedMediaBinding,
	compatibilityKey api.MediaCompatibilityKey,
	assets []api.ReusableMediaAsset,
) error {
	return r.replaceReusableMediaAssets(ctx, binding, compatibilityKey, assets, nil)
}

// CommitReusableMedia atomically records reusable media and the exact durable
// workflow snapshot it represents.
func (r *SQLiteRepository) CommitReusableMedia(
	ctx context.Context,
	binding api.PreparedMediaBinding,
	compatibilityKey api.MediaCompatibilityKey,
	assets []api.ReusableMediaAsset,
	commit api.ReusableMediaCommit,
) error {
	if !commit.Valid() {
		return internalerrors.ErrInvalidInput
	}
	return r.replaceReusableMediaAssets(ctx, binding, compatibilityKey, assets, &commit)
}

func (r *SQLiteRepository) replaceReusableMediaAssets(
	ctx context.Context,
	binding api.PreparedMediaBinding,
	compatibilityKey api.MediaCompatibilityKey,
	assets []api.ReusableMediaAsset,
	commit *api.ReusableMediaCommit,
) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	bound, err := normalizePreparedMediaBinding(binding)
	if err != nil || !compatibilityKey.Valid() {
		return internalerrors.ErrInvalidInput
	}
	normalized, err := normalizeReusableMediaAssets(bound, compatibilityKey, assets)
	if err != nil {
		return internalerrors.ErrInvalidInput
	}
	return r.withWriteTx(ctx, "replace reusable media assets", func(tx *sql.Tx) error {
		if err := requireReusableMediaAuthority(ctx, tx, bound.SourcePath); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM media_reusable_assets
			WHERE source_path = ? AND prepared_media_fingerprint = ? AND prepared_generation = ?
		`, bound.SourcePath, bound.PreparedMediaFingerprint, bound.PreparedGeneration); err != nil {
			return fmt.Errorf("db reusable media: clear associations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM media_reusable_hosted_links
			WHERE source_path = ? AND prepared_media_fingerprint = ? AND prepared_generation = ?
		`, bound.SourcePath, bound.PreparedMediaFingerprint, bound.PreparedGeneration); err != nil {
			return fmt.Errorf("db reusable media: clear hosted associations: %w", err)
		}
		for _, asset := range normalized {
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM media_reusable_tombstones
				WHERE compatibility_key = ? AND capture_fingerprint = ? AND content_sha256 = ?
					AND source_path IN (?, '')
			`, compatibilityKey, asset.CaptureFingerprint, strings.ToLower(asset.ContentSHA256), bound.SourcePath); err != nil {
				return fmt.Errorf("db reusable media: clear restored tombstone: %w", err)
			}
		}
		assetStmt, err := tx.PrepareContext(ctx, `
			INSERT INTO media_reusable_assets (
				source_path, prepared_media_fingerprint, prepared_generation, compatibility_key,
				capture_fingerprint, content_sha256, kind, purpose, disc_id, image_path,
				image_index, timestamp_seconds, width, height, size_bytes, selected, sort_order, captured_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("db reusable media: prepare asset: %w", err)
		}
		defer assetStmt.Close()
		linkStmt, err := tx.PrepareContext(ctx, `
			INSERT INTO media_reusable_hosted_links (
				source_path, prepared_media_fingerprint, prepared_generation, compatibility_key,
				capture_fingerprint, content_sha256, host, usage_scope, account_scope,
				img_url, raw_url, web_url, size_bytes, uploaded_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(
				source_path, prepared_media_fingerprint, prepared_generation,
				capture_fingerprint, content_sha256, host, usage_scope, account_scope
			)
			DO UPDATE SET img_url = excluded.img_url, raw_url = excluded.raw_url, web_url = excluded.web_url,
				size_bytes = excluded.size_bytes, uploaded_at = excluded.uploaded_at
		`)
		if err != nil {
			return fmt.Errorf("db reusable media: prepare hosted link: %w", err)
		}
		defer linkStmt.Close()

		for _, asset := range normalized {
			image := asset.Image
			if _, err := assetStmt.ExecContext(
				ctx, bound.SourcePath, bound.PreparedMediaFingerprint, bound.PreparedGeneration, compatibilityKey,
				asset.CaptureFingerprint, strings.ToLower(asset.ContentSHA256), asset.Kind, image.Purpose,
				strings.TrimSpace(image.DiscID), strings.TrimSpace(image.Path), image.Index, image.TimestampSeconds,
				image.Width, image.Height, image.SizeBytes, boolToInt(asset.Selected), asset.Order,
				time.Now().UTC().Format(time.RFC3339Nano),
			); err != nil {
				return fmt.Errorf("db reusable media: insert asset: %w", err)
			}
			for _, link := range asset.HostedLinks {
				if _, err := linkStmt.ExecContext(
					ctx, bound.SourcePath, bound.PreparedMediaFingerprint, bound.PreparedGeneration, compatibilityKey,
					asset.CaptureFingerprint, strings.ToLower(asset.ContentSHA256),
					strings.ToLower(strings.TrimSpace(link.Host)), strings.TrimSpace(link.UsageScope), strings.TrimSpace(link.AccountScope),
					strings.TrimSpace(link.ImgURL), strings.TrimSpace(link.RawURL), strings.TrimSpace(link.WebURL), link.SizeBytes,
					link.UploadedAt.UTC().Format(time.RFC3339Nano),
				); err != nil {
					return fmt.Errorf("db reusable media: insert hosted link: %w", err)
				}
			}
		}
		if commit != nil {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO media_reusable_commits (
					source_path, prepared_media_fingerprint, prepared_generation,
					workflow_id, media_id, media_revision, committed_at
				) VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(workflow_id, media_id, media_revision) DO UPDATE SET
					source_path = excluded.source_path,
					prepared_media_fingerprint = excluded.prepared_media_fingerprint,
					prepared_generation = excluded.prepared_generation,
					committed_at = excluded.committed_at
			`, bound.SourcePath, bound.PreparedMediaFingerprint, bound.PreparedGeneration,
				commit.WorkflowID, commit.MediaID, commit.Revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return fmt.Errorf("db reusable media: record commit: %w", err)
			}
		}
		return nil
	})
}

// HasReusableMediaCommit reports whether reusable associations were committed
// for one exact workflow media snapshot.
func (r *SQLiteRepository) HasReusableMediaCommit(ctx context.Context, commit api.ReusableMediaCommit) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("db: repository not initialized")
	}
	if !commit.Valid() {
		return false, internalerrors.ErrInvalidInput
	}
	var exists int
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM media_reusable_commits
			WHERE workflow_id = ? AND media_id = ? AND media_revision = ?
		)
	`, commit.WorkflowID, commit.MediaID, commit.Revision).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("db reusable media: check commit: %w", err)
	}
	return exists != 0, nil
}

// LoadReusableMediaAssets returns private reusable evidence for one strong
// source key. Callers must still verify local bytes before re-materializing
// current-generation artifacts.
func (r *SQLiteRepository) LoadReusableMediaAssets(
	ctx context.Context,
	compatibilityKey api.MediaCompatibilityKey,
) ([]api.ReusableMediaAsset, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}
	if !compatibilityKey.Valid() {
		return nil, internalerrors.ErrInvalidInput
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT source_path, prepared_media_fingerprint, prepared_generation, capture_fingerprint, content_sha256,
			kind, purpose, disc_id, image_path, image_index, timestamp_seconds, width, height, size_bytes,
			selected, sort_order
		FROM media_reusable_assets AS asset
		WHERE compatibility_key = ? AND NOT EXISTS (
			SELECT 1 FROM media_reusable_tombstones AS tombstone
			WHERE tombstone.compatibility_key = asset.compatibility_key
				AND tombstone.capture_fingerprint = asset.capture_fingerprint
				AND tombstone.content_sha256 = asset.content_sha256
		)
		ORDER BY captured_at DESC, sort_order ASC
	`, compatibilityKey)
	if err != nil {
		return nil, fmt.Errorf("db reusable media: list assets: %w", err)
	}
	defer rows.Close()

	assets := make([]api.ReusableMediaAsset, 0)
	seen := make(map[string]struct{})
	for rows.Next() {
		var asset api.ReusableMediaAsset
		var selected int
		if err := rows.Scan(
			&asset.Binding.SourcePath, &asset.Binding.PreparedMediaFingerprint, &asset.Binding.PreparedGeneration,
			&asset.CaptureFingerprint, &asset.ContentSHA256, &asset.Kind, &asset.Image.Purpose, &asset.Image.DiscID,
			&asset.Image.Path, &asset.Image.Index, &asset.Image.TimestampSeconds, &asset.Image.Width, &asset.Image.Height,
			&asset.Image.SizeBytes, &selected, &asset.Order,
		); err != nil {
			return nil, fmt.Errorf("db reusable media: scan asset: %w", err)
		}
		asset.CompatibilityKey = compatibilityKey
		asset.Selected = selected != 0
		key := reusableMediaClaimKey(asset.CaptureFingerprint, asset.ContentSHA256)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db reusable media: list assets: %w", err)
	}
	if len(assets) == 0 {
		return nil, nil
	}
	links, err := r.loadReusableHostedLinks(ctx, compatibilityKey)
	if err != nil {
		return nil, err
	}
	for index := range assets {
		key := reusableMediaAssetKey(assets[index].Binding, assets[index].CaptureFingerprint, assets[index].ContentSHA256)
		assets[index].HostedLinks = slices.Clone(links[key])
	}
	return assets, nil
}

// DeleteReusableMediaAssets records deletion tombstones before removing exact
// associations. A later strong re-capture clears only its matching tombstone.
func (r *SQLiteRepository) DeleteReusableMediaAssets(
	ctx context.Context,
	binding api.PreparedMediaBinding,
	paths []string,
) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	bound, err := normalizePreparedMediaBinding(binding)
	if err != nil || len(paths) == 0 {
		return internalerrors.ErrInvalidInput
	}
	return r.withWriteTx(ctx, "delete reusable media assets", func(tx *sql.Tx) error {
		if err := requireReusableMediaAuthority(ctx, tx, bound.SourcePath); err != nil {
			return err
		}
		find, err := tx.PrepareContext(ctx, `
			SELECT compatibility_key, capture_fingerprint, content_sha256
			FROM media_reusable_assets
			WHERE source_path = ? AND prepared_media_fingerprint = ? AND prepared_generation = ? AND image_path = ?
		`)
		if err != nil {
			return fmt.Errorf("db reusable media: prepare deleted asset lookup: %w", err)
		}
		defer find.Close()
		for _, pathValue := range paths {
			rows, queryErr := find.QueryContext(ctx, bound.SourcePath, bound.PreparedMediaFingerprint, bound.PreparedGeneration, strings.TrimSpace(pathValue))
			if queryErr != nil {
				return fmt.Errorf("db reusable media: find deleted asset: %w", queryErr)
			}
			if err := func() (resultErr error) {
				defer func() {
					if closeErr := rows.Close(); closeErr != nil && resultErr == nil {
						resultErr = fmt.Errorf("db reusable media: close deleted assets: %w", closeErr)
					}
				}()
				for rows.Next() {
					var compatibilityKey, captureFingerprint, contentSHA256 string
					if scanErr := rows.Scan(&compatibilityKey, &captureFingerprint, &contentSHA256); scanErr != nil {
						return fmt.Errorf("db reusable media: scan deleted asset: %w", scanErr)
					}
					if _, insertErr := tx.ExecContext(ctx, `
						INSERT INTO media_reusable_tombstones (
							source_path, compatibility_key, capture_fingerprint, content_sha256, deleted_at
						) VALUES (?, ?, ?, ?, ?)
						ON CONFLICT(compatibility_key, capture_fingerprint, content_sha256, source_path)
						DO UPDATE SET deleted_at = excluded.deleted_at
					`, bound.SourcePath, compatibilityKey, captureFingerprint, contentSHA256, time.Now().UTC().Format(time.RFC3339Nano)); insertErr != nil {
						return fmt.Errorf("db reusable media: tombstone deleted asset: %w", insertErr)
					}
				}
				if rowErr := rows.Err(); rowErr != nil {
					return fmt.Errorf("db reusable media: read deleted assets: %w", rowErr)
				}
				return nil
			}(); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *SQLiteRepository) loadReusableHostedLinks(
	ctx context.Context,
	compatibilityKey api.MediaCompatibilityKey,
) (map[string][]api.UploadedImageLink, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT source_path, prepared_media_fingerprint, prepared_generation,
			capture_fingerprint, content_sha256, host, usage_scope, account_scope, img_url, raw_url, web_url, size_bytes, uploaded_at
		FROM media_reusable_hosted_links WHERE compatibility_key = ?
		ORDER BY host ASC, usage_scope ASC
	`, compatibilityKey)
	if err != nil {
		return nil, fmt.Errorf("db reusable media: list hosted links: %w", err)
	}
	defer rows.Close()
	links := make(map[string][]api.UploadedImageLink)
	for rows.Next() {
		var binding api.PreparedMediaBinding
		var captureFingerprint string
		var contentSHA256 string
		var link api.UploadedImageLink
		var uploadedAt string
		if err := rows.Scan(
			&binding.SourcePath, &binding.PreparedMediaFingerprint, &binding.PreparedGeneration,
			&captureFingerprint, &contentSHA256, &link.Host, &link.UsageScope, &link.AccountScope, &link.ImgURL, &link.RawURL,
			&link.WebURL, &link.SizeBytes, &uploadedAt,
		); err != nil {
			return nil, fmt.Errorf("db reusable media: scan hosted link: %w", err)
		}
		if parsed, parseErr := time.Parse(time.RFC3339Nano, uploadedAt); parseErr == nil {
			link.UploadedAt = parsed
		}
		key := reusableMediaAssetKey(binding, api.WorkflowFingerprint(captureFingerprint), contentSHA256)
		links[key] = append(links[key], link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db reusable media: list hosted links: %w", err)
	}
	return links, nil
}

func requireReusableMediaAuthority(ctx context.Context, tx *sql.Tx, sourcePath string) error {
	slot, err := loadActiveInput(ctx, tx)
	if err != nil {
		return err
	}
	if slot.Fence == 0 {
		return nil
	}
	if err := requireWorkflowInputMutation(ctx, tx, slot.OwnerID, slot.WorkflowID, true); err != nil {
		return err
	}
	var activePath string
	if err := tx.QueryRowContext(ctx, `SELECT canonical_path FROM input_records WHERE id = ?`, slot.InputID).Scan(&activePath); err != nil {
		return fmt.Errorf("db reusable media: active source: %w", err)
	}
	if !pathing.SamePath(activePath, sourcePath) {
		return api.ErrActiveInputChanged
	}
	return nil
}

func reusableMediaAssetKey(binding api.PreparedMediaBinding, captureFingerprint api.WorkflowFingerprint, contentSHA256 string) string {
	return strings.Join([]string{
		binding.SourcePath,
		binding.PreparedMediaFingerprint,
		fmt.Sprintf("%d", binding.PreparedGeneration),
		string(captureFingerprint),
		strings.ToLower(contentSHA256),
	}, "\x00")
}

func reusableMediaClaimKey(captureFingerprint api.WorkflowFingerprint, contentSHA256 string) string {
	return string(captureFingerprint) + "\x00" + strings.ToLower(contentSHA256)
}

func normalizeReusableMediaAssets(
	binding api.PreparedMediaBinding,
	compatibilityKey api.MediaCompatibilityKey,
	assets []api.ReusableMediaAsset,
) ([]api.ReusableMediaAsset, error) {
	normalized := slices.Clone(assets)
	paths := make(map[string]struct{}, len(normalized))
	for index := range normalized {
		asset := &normalized[index]
		if !asset.Binding.Equal(binding) || asset.CompatibilityKey != compatibilityKey || !asset.Valid() {
			return nil, errors.New("invalid reusable media asset")
		}
		asset.ContentSHA256 = strings.ToLower(strings.TrimSpace(asset.ContentSHA256))
		pathValue := strings.TrimSpace(asset.Image.Path)
		if _, exists := paths[pathValue]; exists {
			return nil, errors.New("duplicate reusable media path")
		}
		paths[pathValue] = struct{}{}
		for linkIndex := range asset.HostedLinks {
			link := &asset.HostedLinks[linkIndex]
			link.Host = strings.ToLower(strings.TrimSpace(link.Host))
			link.UsageScope = strings.TrimSpace(link.UsageScope)
			if link.Host == "" || link.UsageScope == "" || strings.TrimSpace(link.AccountScope) == "" ||
				(link.ImgURL == "" && link.RawURL == "" && link.WebURL == "") || link.UploadedAt.IsZero() {
				return nil, errors.New("invalid reusable hosted link")
			}
		}
	}
	return normalized, nil
}
