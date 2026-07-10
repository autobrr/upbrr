// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/pkg/api"
)

type SQLiteRepository struct {
	db     *sql.DB
	logger Logger
	// path is empty for SQLite sentinel/URI inputs whose sibling helper-file
	// location is intentionally not inferred from the opened DB string.
	path string
}

const sqliteBusyTimeout = 5000
const sqliteRetryAttempts = 3

const (
	pragmaForeignKeysOnSQL  = "PRAGMA foreign_keys = ON"
	pragmaJournalModeSQL    = "PRAGMA journal_mode"
	pragmaJournalModeWALSQL = "PRAGMA journal_mode = WAL"
	pragmaBusyTimeoutPrefix = "PRAGMA busy_timeout = "
)

func Open(path string) (*SQLiteRepository, error) {
	return OpenWithLogger(path, nopLogger{})
}

func OpenContext(ctx context.Context, path string) (*SQLiteRepository, error) {
	return OpenWithLoggerContext(ctx, path, nopLogger{})
}

func OpenWithLogger(path string, logger Logger) (*SQLiteRepository, error) {
	return OpenWithLoggerContext(context.Background(), path, logger)
}

func OpenWithLoggerContext(ctx context.Context, path string, logger Logger) (*SQLiteRepository, error) {
	if ctx == nil {
		return nil, errors.New("db: context is required")
	}
	if logger == nil {
		logger = nopLogger{}
	}

	resolved, err := resolvePath(path)
	if err != nil {
		return nil, err
	}
	logger.Debugf("db: opening sqlite at %s", resolved)

	db, err := sql.Open("sqlite", resolved)
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	// Keep each repository handle on one connection so connection-local SQLite
	// PRAGMAs apply consistently to all app reads and writes through this repo.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}

	if _, err := db.ExecContext(ctx, pragmaForeignKeysOnSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("db pragma foreign_keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, pragmaBusyTimeoutPrefix+strconv.Itoa(sqliteBusyTimeout)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("db pragma busy_timeout: %w", err)
	}
	if isMemorySQLitePath(path) {
		// SQLite cannot use WAL for in-memory databases, so tests that use :memory:
		// intentionally run with different journaling semantics than on-disk production DBs.
		journalMode, err := queryCurrentJournalMode(ctx, db)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("db pragma journal_mode: %w", err)
		}
		logger.Debugf("db: sqlite journal_mode is %s for in-memory database", journalMode)
	} else {
		journalMode, err := enableWALJournalMode(ctx, db)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("db pragma journal_mode: %w", err)
		}
		if !strings.EqualFold(journalMode, "wal") {
			_ = db.Close()
			return nil, fmt.Errorf("db pragma journal_mode: expected WAL, got %s", journalMode)
		}
	}

	return &SQLiteRepository{db: db, logger: logger, path: repositoryDBPath(path, resolved)}, nil
}

// DBPath returns the resolved on-disk database path when the repository was
// opened from a normal filesystem path. It returns empty for SQLite sentinel or
// URI inputs so callers do not invent helper-file locations for in-memory DBs
// or opaque SQLite connection strings.
func (r *SQLiteRepository) DBPath() string {
	if r == nil {
		return ""
	}
	return r.path
}

func (r *SQLiteRepository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	if r.logger != nil {
		r.logger.Infof("db: closing sqlite")
	}
	if err := r.db.Close(); err != nil {
		return fmt.Errorf("db: close sqlite: %w", err)
	}
	return nil
}

// RawDB returns the underlying *sql.DB handle.
//
// Callers using RawDB bypass repository safeguards such as validation,
// busy-retry behavior, and repository-level error wrapping. Restrict usage to
// approved low-level paths (for example migrations, tests, or advanced
// operations that are not exposed through repository methods), and avoid
// casual direct query/exec calls in application code.
func (r *SQLiteRepository) RawDB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

func (r *SQLiteRepository) Migrate() error {
	return r.MigrateContext(context.Background())
}

func (r *SQLiteRepository) MigrateContext(ctx context.Context) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if r.logger != nil {
		r.logger.Debugf("db: running migrations")
	}
	return retryBusyContext(ctx, r.logger, "migration", func() error {
		return MigrateContext(ctx, r.db)
	})
}

func IsBusyError(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code() & 0xFF {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	default:
		return false
	}
}

// retryBusyContext reruns an operation when SQLite reports a transient busy or
// locked database and stops immediately for non-lock errors or context
// cancellation.
func retryBusyContext(ctx context.Context, logger Logger, operation string, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= sqliteRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("db: retry busy context canceled: %w", err)
		}
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if !IsBusyError(err) || attempt == sqliteRetryAttempts {
			if IsBusyError(err) && logger != nil {
				logger.Warnf("db: %s busy lock persisted after %d attempts; returning retry exhaustion", operation, sqliteRetryAttempts)
			}
			return err
		}
		if logger != nil {
			logger.Infof("db: %s busy, retrying (%d/%d)", operation, attempt, sqliteRetryAttempts)
		}
		delay := time.Duration(50*attempt) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("context canceled: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return lastErr
}

// execWrite runs a single write statement with the repository's busy-lock retry
// policy. It returns the final statement result when the write succeeds.
func (r *SQLiteRepository) execWrite(ctx context.Context, operation string, query string, args ...any) (sql.Result, error) {
	var result sql.Result
	err := retryBusyContext(ctx, r.logger, operation, func() error {
		var execErr error
		result, execErr = r.db.ExecContext(ctx, query, args...)
		if execErr != nil {
			return fmt.Errorf("db %s: %w", operation, execErr)
		}
		return nil
	})
	return result, err
}

// withWriteTx runs a complete write transaction under the repository's busy-lock
// retry policy. The callback must keep work DB-local because any busy or locked
// error retries the entire transaction.
func (r *SQLiteRepository) withWriteTx(ctx context.Context, operation string, fn func(*sql.Tx) error) error {
	return retryBusyContext(ctx, r.logger, operation, func() error {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("db %s begin: %w", operation, err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()

		if err := fn(tx); err != nil {
			_ = tx.Rollback()
			committed = true
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("db %s commit: %w", operation, err)
		}
		committed = true
		return nil
	})
}

func enableWALJournalMode(ctx context.Context, db *sql.DB) (string, error) {
	row := db.QueryRowContext(ctx, pragmaJournalModeWALSQL)
	var got string
	if err := row.Scan(&got); err != nil {
		return "", fmt.Errorf("db: enable WAL journal mode: %w", err)
	}
	return got, nil
}

func queryCurrentJournalMode(ctx context.Context, db *sql.DB) (string, error) {
	row := db.QueryRowContext(ctx, pragmaJournalModeSQL)
	var got string
	if err := row.Scan(&got); err != nil {
		return "", fmt.Errorf("db: query current journal mode: %w", err)
	}
	return got, nil
}

func isMemorySQLitePath(path string) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	if trimmed == ":memory:" {
		return true
	}
	normalized := strings.ToLower(trimmed)
	return strings.HasPrefix(normalized, "file:") && strings.Contains(normalized, "mode=memory")
}

func (r *SQLiteRepository) GetByPath(ctx context.Context, path string) (FileMetadata, error) {
	if r == nil || r.db == nil {
		return FileMetadata{}, errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return FileMetadata{}, internalerrors.ErrInvalidInput
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT path, info_hash, updated_at, disc_type, video_path, file_list, scene, scene_name, scene_imdb,
			release_category,
			release_type, release_artist, release_title, release_subtitle, release_alt, release_year, release_month, release_day,
			release_source, release_resolution, release_codec, release_audio, release_hdr, release_ext,
			release_language, release_site, release_genre, release_channels, release_collection,
			release_region, release_size, release_group, release_disc,
			release_edition, release_other, source_size
		FROM file_metadata
		WHERE path = ?
	`, path)

	var metadata FileMetadata
	var updatedAt string
	var fileList string
	var sceneValue int
	var releaseEdition string
	var releaseOther string
	var releaseCodec string
	var releaseAudio string
	var releaseHDR string
	var releaseLanguage string
	if err := row.Scan(
		&metadata.Path,
		&metadata.InfoHash,
		&updatedAt,
		&metadata.DiscType,
		&metadata.VideoPath,
		&fileList,
		&sceneValue,
		&metadata.SceneName,
		&metadata.SceneIMDB,
		&metadata.Category,
		&metadata.Type,
		&metadata.Artist,
		&metadata.Title,
		&metadata.Subtitle,
		&metadata.Alt,
		&metadata.Year,
		&metadata.Month,
		&metadata.Day,
		&metadata.Source,
		&metadata.Resolution,
		&releaseCodec,
		&releaseAudio,
		&releaseHDR,
		&metadata.Ext,
		&releaseLanguage,
		&metadata.Site,
		&metadata.Genre,
		&metadata.Channels,
		&metadata.Collection,
		&metadata.Region,
		&metadata.Size,
		&metadata.Group,
		&metadata.Disc,
		&releaseEdition,
		&releaseOther,
		&metadata.SourceSize,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FileMetadata{}, internalerrors.ErrNotFound
		}
		return FileMetadata{}, fmt.Errorf("db get: %w", err)
	}
	metadata.Scene = sceneValue != 0
	if fileList != "" {
		if parsed, err := decodeFileList(fileList); err == nil {
			metadata.FileList = parsed
		}
	}
	if releaseEdition != "" {
		if parsed, err := decodeStringList(releaseEdition); err == nil {
			metadata.Edition = parsed
		}
	}
	if releaseOther != "" {
		if parsed, err := decodeStringList(releaseOther); err == nil {
			metadata.Other = parsed
		}
	}
	if releaseCodec != "" {
		if parsed, err := decodeStringList(releaseCodec); err == nil {
			metadata.Codec = parsed
		}
	}
	if releaseAudio != "" {
		if parsed, err := decodeStringList(releaseAudio); err == nil {
			metadata.Audio = parsed
		}
	}
	if releaseHDR != "" {
		if parsed, err := decodeStringList(releaseHDR); err == nil {
			metadata.HDR = parsed
		}
	}
	if releaseLanguage != "" {
		if parsed, err := decodeStringList(releaseLanguage); err == nil {
			metadata.Language = parsed
		}
	}

	if updatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			metadata.UpdatedAt = parsed
		}
	}

	return metadata, nil
}

func (r *SQLiteRepository) Save(ctx context.Context, metadata FileMetadata) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(metadata.Path) == "" {
		return internalerrors.ErrInvalidInput
	}
	if metadata.Category.IsValid() {
		metadata.Category = metadata.Category.Canonical()
	} else {
		metadata.Category = api.CategoryUnknown
	}

	timestamp := metadata.UpdatedAt
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	fileList := encodeFileList(metadata.FileList)
	releaseEdition := encodeStringList(metadata.Edition)
	releaseOther := encodeStringList(metadata.Other)
	releaseCodec := encodeStringList(metadata.Codec)
	releaseAudio := encodeStringList(metadata.Audio)
	releaseHDR := encodeStringList(metadata.HDR)
	releaseLanguage := encodeStringList(metadata.Language)
	sceneValue := 0
	if metadata.Scene {
		sceneValue = 1
	}
	_, err := r.execWrite(ctx, "save file metadata", `
		INSERT INTO file_metadata (
			path, info_hash, updated_at, disc_type, video_path, file_list, scene, scene_name, scene_imdb,
			release_category,
			release_type, release_artist, release_title, release_subtitle, release_alt, release_year, release_month, release_day,
			release_source, release_resolution, release_codec, release_audio, release_hdr, release_ext,
			release_language, release_site, release_genre, release_channels, release_collection,
			release_region, release_size, release_group, release_disc,
			release_edition, release_other, source_size
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			info_hash = excluded.info_hash,
			updated_at = excluded.updated_at,
			disc_type = excluded.disc_type,
			video_path = excluded.video_path,
			file_list = excluded.file_list,
			scene = excluded.scene,
			scene_name = excluded.scene_name,
			scene_imdb = excluded.scene_imdb,
			release_category = excluded.release_category,
			release_type = excluded.release_type,
			release_artist = excluded.release_artist,
			release_title = excluded.release_title,
			release_subtitle = excluded.release_subtitle,
			release_alt = excluded.release_alt,
			release_year = excluded.release_year,
			release_month = excluded.release_month,
			release_day = excluded.release_day,
			release_source = excluded.release_source,
			release_resolution = excluded.release_resolution,
			release_codec = excluded.release_codec,
			release_audio = excluded.release_audio,
			release_hdr = excluded.release_hdr,
			release_ext = excluded.release_ext,
			release_language = excluded.release_language,
			release_site = excluded.release_site,
			release_genre = excluded.release_genre,
			release_channels = excluded.release_channels,
			release_collection = excluded.release_collection,
			release_region = excluded.release_region,
			release_size = excluded.release_size,
			release_group = excluded.release_group,
			release_disc = excluded.release_disc,
			release_edition = excluded.release_edition,
			release_other = excluded.release_other,
			source_size = excluded.source_size
	`,
		metadata.Path,
		metadata.InfoHash,
		timestamp.Format(time.RFC3339Nano),
		metadata.DiscType,
		metadata.VideoPath,
		fileList,
		sceneValue,
		metadata.SceneName,
		metadata.SceneIMDB,
		metadata.Category,
		metadata.Type,
		metadata.Artist,
		metadata.Title,
		metadata.Subtitle,
		metadata.Alt,
		metadata.Year,
		metadata.Month,
		metadata.Day,
		metadata.Source,
		metadata.Resolution,
		releaseCodec,
		releaseAudio,
		releaseHDR,
		metadata.Ext,
		releaseLanguage,
		metadata.Site,
		metadata.Genre,
		metadata.Channels,
		metadata.Collection,
		metadata.Region,
		metadata.Size,
		metadata.Group,
		metadata.Disc,
		releaseEdition,
		releaseOther,
		metadata.SourceSize,
	)
	if err != nil {
		return fmt.Errorf("db save: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) GetExternalIDs(ctx context.Context, path string) (ExternalIDs, error) {
	if r == nil || r.db == nil {
		return ExternalIDs{}, errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return ExternalIDs{}, internalerrors.ErrInvalidInput
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT source_path, tmdb_id, imdb_id, tvdb_id, tvmaze_id, mal_id, category,
			source_tmdb, source_imdb, source_tvdb, source_tvmaze, source_mal, updated_at
		FROM external_ids
		WHERE source_path = ?
	`, path)

	var ids ExternalIDs
	var updatedAt string
	if err := row.Scan(
		&ids.SourcePath,
		&ids.TMDBID,
		&ids.IMDBID,
		&ids.TVDBID,
		&ids.TVmazeID,
		&ids.MALID,
		&ids.Category,
		&ids.SourceTMDB,
		&ids.SourceIMDB,
		&ids.SourceTVDB,
		&ids.SourceTVmaze,
		&ids.SourceMAL,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExternalIDs{}, internalerrors.ErrNotFound
		}
		return ExternalIDs{}, fmt.Errorf("db get external ids: %w", err)
	}
	if updatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			ids.UpdatedAt = parsed
		}
	}

	return ids, nil
}

func (r *SQLiteRepository) SaveExternalIDs(ctx context.Context, ids ExternalIDs) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(ids.SourcePath) == "" {
		return internalerrors.ErrInvalidInput
	}

	timestamp := ids.UpdatedAt
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	_, err := r.execWrite(ctx, "save external ids", `
		INSERT INTO external_ids (
			source_path, tmdb_id, imdb_id, tvdb_id, tvmaze_id, mal_id, category,
			source_tmdb, source_imdb, source_tvdb, source_tvmaze, source_mal, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			tmdb_id = excluded.tmdb_id,
			imdb_id = excluded.imdb_id,
			tvdb_id = excluded.tvdb_id,
			tvmaze_id = excluded.tvmaze_id,
			mal_id = excluded.mal_id,
			category = excluded.category,
			source_tmdb = excluded.source_tmdb,
			source_imdb = excluded.source_imdb,
			source_tvdb = excluded.source_tvdb,
			source_tvmaze = excluded.source_tvmaze,
			source_mal = excluded.source_mal,
			updated_at = excluded.updated_at
	`,
		ids.SourcePath,
		ids.TMDBID,
		ids.IMDBID,
		ids.TVDBID,
		ids.TVmazeID,
		ids.MALID,
		ids.Category,
		ids.SourceTMDB,
		ids.SourceIMDB,
		ids.SourceTVDB,
		ids.SourceTVmaze,
		ids.SourceMAL,
		timestamp.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("db save external ids: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) GetDVDMediaInfo(ctx context.Context, path string) (DVDMediaInfo, error) {
	if r == nil || r.db == nil {
		return DVDMediaInfo{}, errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return DVDMediaInfo{}, internalerrors.ErrInvalidInput
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT source_path, ifo_path, vob_path, vob_set, width, height, frame_rate, scan_type,
			resolution, high_frame_rate, mediainfo_json, mediainfo_text, vob_mediainfo_raw, updated_at
		FROM dvd_mediainfo
		WHERE source_path = ?
	`, path)

	var info DVDMediaInfo
	var updatedAt string
	var highFrameRateValue int
	if err := row.Scan(
		&info.SourcePath,
		&info.IFOPath,
		&info.VOBPath,
		&info.VOBSet,
		&info.Width,
		&info.Height,
		&info.FrameRate,
		&info.ScanType,
		&info.Resolution,
		&highFrameRateValue,
		&info.MediaInfoJSON,
		&info.MediaInfoText,
		&info.VOBMediaInfoRaw,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DVDMediaInfo{}, internalerrors.ErrNotFound
		}
		return DVDMediaInfo{}, fmt.Errorf("db get dvd mediainfo: %w", err)
	}
	info.HighFrameRate = highFrameRateValue != 0
	if updatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			info.UpdatedAt = parsed
		}
	}

	return info, nil
}

func (r *SQLiteRepository) SaveDVDMediaInfo(ctx context.Context, info DVDMediaInfo) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(info.SourcePath) == "" {
		return internalerrors.ErrInvalidInput
	}

	timestamp := info.UpdatedAt
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	highFrameRateValue := 0
	if info.HighFrameRate {
		highFrameRateValue = 1
	}

	_, err := r.execWrite(ctx, "save dvd mediainfo", `
		INSERT INTO dvd_mediainfo (
			source_path, ifo_path, vob_path, vob_set, width, height, frame_rate, scan_type,
			resolution, high_frame_rate, mediainfo_json, mediainfo_text, vob_mediainfo_raw, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			ifo_path = excluded.ifo_path,
			vob_path = excluded.vob_path,
			vob_set = excluded.vob_set,
			width = excluded.width,
			height = excluded.height,
			frame_rate = excluded.frame_rate,
			scan_type = excluded.scan_type,
			resolution = excluded.resolution,
			high_frame_rate = excluded.high_frame_rate,
			mediainfo_json = excluded.mediainfo_json,
			mediainfo_text = excluded.mediainfo_text,
			vob_mediainfo_raw = excluded.vob_mediainfo_raw,
			updated_at = excluded.updated_at
	`,
		info.SourcePath,
		info.IFOPath,
		info.VOBPath,
		info.VOBSet,
		info.Width,
		info.Height,
		info.FrameRate,
		info.ScanType,
		info.Resolution,
		highFrameRateValue,
		info.MediaInfoJSON,
		info.MediaInfoText,
		info.VOBMediaInfoRaw,
		timestamp.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("db save dvd mediainfo: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) GetExternalMetadata(ctx context.Context, path string) (ExternalMetadata, error) {
	if r == nil || r.db == nil {
		return ExternalMetadata{}, errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return ExternalMetadata{}, internalerrors.ErrInvalidInput
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT source_path, tmdb_json, imdb_json, tvdb_json, tvmaze_json, anilist_json, bluray_json, updated_at
		FROM external_metadata
		WHERE source_path = ?
	`, path)

	var metadata ExternalMetadata
	var tmdbJSON string
	var imdbJSON string
	var tvdbJSON string
	var tvmazeJSON string
	var anilistJSON string
	var blurayJSON string
	var updatedAt string
	if err := row.Scan(
		&metadata.SourcePath,
		&tmdbJSON,
		&imdbJSON,
		&tvdbJSON,
		&tvmazeJSON,
		&anilistJSON,
		&blurayJSON,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExternalMetadata{}, internalerrors.ErrNotFound
		}
		return ExternalMetadata{}, fmt.Errorf("db get external metadata: %w", err)
	}

	var err error
	if metadata.TMDB, err = decodeOptionalJSON[TMDBMetadata](tmdbJSON); err != nil {
		return ExternalMetadata{}, fmt.Errorf("db decode tmdb metadata: %w", err)
	}
	if metadata.IMDB, err = decodeOptionalJSON[IMDBMetadata](imdbJSON); err != nil {
		return ExternalMetadata{}, fmt.Errorf("db decode imdb metadata: %w", err)
	}
	if metadata.TVDB, err = decodeOptionalJSON[TVDBMetadata](tvdbJSON); err != nil {
		return ExternalMetadata{}, fmt.Errorf("db decode tvdb metadata: %w", err)
	}
	if metadata.TVmaze, err = decodeOptionalJSON[TVmazeMetadata](tvmazeJSON); err != nil {
		return ExternalMetadata{}, fmt.Errorf("db decode tvmaze metadata: %w", err)
	}
	if metadata.AniList, err = decodeOptionalJSON[api.AniListMetadata](anilistJSON); err != nil {
		return ExternalMetadata{}, fmt.Errorf("db decode anilist metadata: %w", err)
	}
	if metadata.Bluray, err = decodeOptionalJSON[api.BlurayMetadata](blurayJSON); err != nil {
		return ExternalMetadata{}, fmt.Errorf("db decode bluray metadata: %w", err)
	}
	if updatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			metadata.UpdatedAt = parsed
		}
	}

	return metadata, nil
}

func (r *SQLiteRepository) SaveExternalMetadata(ctx context.Context, metadata ExternalMetadata) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(metadata.SourcePath) == "" {
		return internalerrors.ErrInvalidInput
	}

	timestamp := metadata.UpdatedAt
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	tmdbJSON := encodeOptionalJSON(metadata.TMDB)
	imdbJSON := encodeOptionalJSON(metadata.IMDB)
	tvdbJSON := encodeOptionalJSON(metadata.TVDB)
	tvmazeJSON := encodeOptionalJSON(metadata.TVmaze)
	anilistJSON := encodeOptionalJSON(metadata.AniList)
	blurayJSON := encodeOptionalJSON(metadata.Bluray)

	_, err := r.execWrite(ctx, "save external metadata", `
		INSERT INTO external_metadata (
			source_path, tmdb_json, imdb_json, tvdb_json, tvmaze_json, anilist_json, bluray_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			tmdb_json = excluded.tmdb_json,
			imdb_json = excluded.imdb_json,
			tvdb_json = excluded.tvdb_json,
			tvmaze_json = excluded.tvmaze_json,
			anilist_json = excluded.anilist_json,
			bluray_json = excluded.bluray_json,
			updated_at = excluded.updated_at
	`,
		metadata.SourcePath,
		tmdbJSON,
		imdbJSON,
		tvdbJSON,
		tvmazeJSON,
		anilistJSON,
		blurayJSON,
		timestamp.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("db save external metadata: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) GetReleaseNameOverrides(ctx context.Context, path string) (ReleaseNameOverrides, error) {
	if r == nil || r.db == nil {
		return ReleaseNameOverrides{}, errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return ReleaseNameOverrides{}, internalerrors.ErrInvalidInput
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT category, release_type, release_source, release_resolution,
			tag, service, edition, season, episode, episode_title,
			manual_year, manual_date, use_season_episode, no_season, no_year, no_aka, no_tag,
			no_edition, no_dub, no_dual, dual_audio, region
		FROM release_overrides
		WHERE source_path = ?
	`, path)

	var overrides ReleaseNameOverrides
	var category sql.NullString
	var releaseType sql.NullString
	var releaseSource sql.NullString
	var releaseResolution sql.NullString
	var tag sql.NullString
	var service sql.NullString
	var edition sql.NullString
	var season sql.NullString
	var episode sql.NullString
	var episodeTitle sql.NullString
	var manualYear sql.NullInt64
	var manualDate sql.NullString
	var useSeasonEpisode sql.NullBool
	var noSeason sql.NullBool
	var noYear sql.NullBool
	var noAKA sql.NullBool
	var noTag sql.NullBool
	var noEdition sql.NullBool
	var noDub sql.NullBool
	var noDual sql.NullBool
	var dualAudio sql.NullBool
	var region sql.NullString

	if err := row.Scan(
		&category,
		&releaseType,
		&releaseSource,
		&releaseResolution,
		&tag,
		&service,
		&edition,
		&season,
		&episode,
		&episodeTitle,
		&manualYear,
		&manualDate,
		&useSeasonEpisode,
		&noSeason,
		&noYear,
		&noAKA,
		&noTag,
		&noEdition,
		&noDub,
		&noDual,
		&dualAudio,
		&region,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReleaseNameOverrides{}, internalerrors.ErrNotFound
		}
		return ReleaseNameOverrides{}, fmt.Errorf("db get release overrides: %w", err)
	}

	overrides.Category = nullStringPtr(category)
	overrides.Type = nullStringPtr(releaseType)
	overrides.Source = nullStringPtr(releaseSource)
	overrides.Resolution = nullStringPtr(releaseResolution)
	overrides.Tag = nullStringPtr(tag)
	overrides.Service = nullStringPtr(service)
	overrides.Edition = nullStringPtr(edition)
	overrides.Season = nullStringPtr(season)
	overrides.Episode = nullStringPtr(episode)
	overrides.EpisodeTitle = nullStringPtr(episodeTitle)
	overrides.ManualYear = nullIntPtr(manualYear)
	overrides.ManualDate = nullStringPtr(manualDate)
	overrides.UseSeasonEpisode = nullBoolPtr(useSeasonEpisode)
	overrides.NoSeason = nullBoolPtr(noSeason)
	overrides.NoYear = nullBoolPtr(noYear)
	overrides.NoAKA = nullBoolPtr(noAKA)
	overrides.NoTag = nullBoolPtr(noTag)
	overrides.NoEdition = nullBoolPtr(noEdition)
	overrides.NoDub = nullBoolPtr(noDub)
	overrides.NoDual = nullBoolPtr(noDual)
	overrides.DualAudio = nullBoolPtr(dualAudio)
	overrides.Region = nullStringPtr(region)

	return overrides, nil
}

func (r *SQLiteRepository) SaveReleaseNameOverrides(ctx context.Context, path string, overrides ReleaseNameOverrides) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return internalerrors.ErrInvalidInput
	}

	timestamp := time.Now().UTC().Format(time.RFC3339Nano)

	_, err := r.execWrite(ctx, "save release overrides", `
		INSERT INTO release_overrides (
			source_path,
			category,
			release_type,
			release_source,
			release_resolution,
			tag,
			service,
			edition,
			season,
			episode,
			episode_title,
			manual_year,
			manual_date,
			use_season_episode,
			no_season,
			no_year,
			no_aka,
			no_tag,
			no_edition,
			no_dub,
			no_dual,
			dual_audio,
			region,
			updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			category = excluded.category,
			release_type = excluded.release_type,
			release_source = excluded.release_source,
			release_resolution = excluded.release_resolution,
			tag = excluded.tag,
			service = excluded.service,
			edition = excluded.edition,
			season = excluded.season,
			episode = excluded.episode,
			episode_title = excluded.episode_title,
			manual_year = excluded.manual_year,
			manual_date = excluded.manual_date,
			use_season_episode = excluded.use_season_episode,
			no_season = excluded.no_season,
			no_year = excluded.no_year,
			no_aka = excluded.no_aka,
			no_tag = excluded.no_tag,
			no_edition = excluded.no_edition,
			no_dub = excluded.no_dub,
			no_dual = excluded.no_dual,
			dual_audio = excluded.dual_audio,
			region = excluded.region,
			updated_at = excluded.updated_at
	`,
		path,
		nullString(overrides.Category),
		nullString(overrides.Type),
		nullString(overrides.Source),
		nullString(overrides.Resolution),
		nullString(overrides.Tag),
		nullString(overrides.Service),
		nullString(overrides.Edition),
		nullString(overrides.Season),
		nullString(overrides.Episode),
		nullString(overrides.EpisodeTitle),
		nullInt(overrides.ManualYear),
		nullString(overrides.ManualDate),
		nullBool(overrides.UseSeasonEpisode),
		nullBool(overrides.NoSeason),
		nullBool(overrides.NoYear),
		nullBool(overrides.NoAKA),
		nullBool(overrides.NoTag),
		nullBool(overrides.NoEdition),
		nullBool(overrides.NoDub),
		nullBool(overrides.NoDual),
		nullBool(overrides.DualAudio),
		nullString(overrides.Region),
		timestamp,
	)
	if err != nil {
		return fmt.Errorf("db save release overrides: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) DeleteReleaseNameOverrides(ctx context.Context, path string) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return internalerrors.ErrInvalidInput
	}
	if _, err := r.execWrite(ctx, "delete release overrides", `DELETE FROM release_overrides WHERE source_path = ?`, path); err != nil {
		return fmt.Errorf("db delete release overrides: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) GetPlaylistSelection(ctx context.Context, sourcePath string) (PlaylistSelection, error) {
	if r == nil || r.db == nil {
		return PlaylistSelection{}, errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(sourcePath) == "" {
		return PlaylistSelection{}, internalerrors.ErrInvalidInput
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT selected_playlists, use_all
		FROM playlist_selections
		WHERE source_path = ?
	`, sourcePath)

	var playlistsJSON string
	var useAll bool

	if err := row.Scan(&playlistsJSON, &useAll); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PlaylistSelection{}, internalerrors.ErrNotFound
		}
		return PlaylistSelection{}, fmt.Errorf("db get playlist selection: %w", err)
	}

	var playlists []string
	if playlistsJSON != "" && playlistsJSON != "[]" {
		if err := json.Unmarshal([]byte(playlistsJSON), &playlists); err != nil {
			return PlaylistSelection{}, fmt.Errorf("db parse playlist selection: %w", err)
		}
	}

	return PlaylistSelection{
		SourcePath:        sourcePath,
		SelectedPlaylists: playlists,
		UseAll:            useAll,
	}, nil
}

func (r *SQLiteRepository) SavePlaylistSelection(ctx context.Context, sourcePath string, playlists []string, useAll bool) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(sourcePath) == "" {
		return internalerrors.ErrInvalidInput
	}

	timestamp := time.Now().UTC().Format(time.RFC3339Nano)

	// Marshal playlists to JSON.
	playlistsJSON := "[]"
	if len(playlists) > 0 {
		data, err := json.Marshal(playlists)
		if err != nil {
			return fmt.Errorf("db marshal playlist selection: %w", err)
		}
		playlistsJSON = string(data)
	}

	_, err := r.execWrite(ctx, "save playlist selection", `
		INSERT INTO playlist_selections (source_path, selected_playlists, use_all, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			selected_playlists = excluded.selected_playlists,
			use_all = excluded.use_all,
			updated_at = excluded.updated_at
	`, sourcePath, playlistsJSON, useAll, timestamp)

	if err != nil {
		return fmt.Errorf("db save playlist selection: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) DeletePlaylistSelection(ctx context.Context, sourcePath string) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(sourcePath) == "" {
		return internalerrors.ErrInvalidInput
	}

	if _, err := r.execWrite(ctx, "delete playlist selection", `DELETE FROM playlist_selections WHERE source_path = ?`, sourcePath); err != nil {
		return fmt.Errorf("db delete playlist selection: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) GetDescriptionOverride(ctx context.Context, path string, groupKey string) (DescriptionOverride, error) {
	if r == nil || r.db == nil {
		return DescriptionOverride{}, errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return DescriptionOverride{}, internalerrors.ErrInvalidInput
	}
	trimmedGroup := normalizeDescriptionOverrideGroupKey(groupKey)

	row := r.db.QueryRowContext(ctx, `
		SELECT group_key, description, updated_at
		FROM description_overrides
		WHERE source_path = ? AND group_key = ?
	`, trimmed, trimmedGroup)

	var storedGroupKey string
	var description string
	var updatedAt string
	if err := row.Scan(&storedGroupKey, &description, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DescriptionOverride{}, internalerrors.ErrNotFound
		}
		return DescriptionOverride{}, fmt.Errorf("db get description override: %w", err)
	}

	override := DescriptionOverride{SourcePath: trimmed, GroupKey: normalizeDescriptionOverrideGroupKey(storedGroupKey), Description: description}
	if updatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			override.UpdatedAt = parsed
		}
	}

	return override, nil
}

func (r *SQLiteRepository) ListDescriptionOverridesByPath(ctx context.Context, path string) ([]DescriptionOverride, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT group_key, description, updated_at
		FROM description_overrides
		WHERE source_path = ?
		ORDER BY group_key ASC
	`, trimmed)
	if err != nil {
		return nil, fmt.Errorf("db list description overrides: %w", err)
	}
	defer rows.Close()

	overrides := make([]DescriptionOverride, 0)
	for rows.Next() {
		var override DescriptionOverride
		var updatedAt string
		override.SourcePath = trimmed
		if err := rows.Scan(&override.GroupKey, &override.Description, &updatedAt); err != nil {
			return nil, fmt.Errorf("db list description overrides: %w", err)
		}
		override.GroupKey = normalizeDescriptionOverrideGroupKey(override.GroupKey)
		if updatedAt != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
				override.UpdatedAt = parsed
			}
		}
		overrides = append(overrides, override)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db list description overrides: %w", err)
	}
	return overrides, nil
}

func (r *SQLiteRepository) SaveDescriptionOverride(ctx context.Context, override DescriptionOverride) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmedPath := strings.TrimSpace(override.SourcePath)
	if trimmedPath == "" {
		return internalerrors.ErrInvalidInput
	}
	trimmedGroup := normalizeDescriptionOverrideGroupKey(override.GroupKey)
	trimmedDescription := strings.TrimSpace(override.Description)
	if trimmedDescription == "" {
		return internalerrors.ErrInvalidInput
	}

	updatedAt := override.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	_, err := r.execWrite(ctx, "save description override", `
		INSERT INTO description_overrides (source_path, group_key, description, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(source_path, group_key) DO UPDATE SET
			description = excluded.description,
			updated_at = excluded.updated_at
	`, trimmedPath, trimmedGroup, trimmedDescription, updatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("db save description override: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) DeleteDescriptionOverride(ctx context.Context, path string, groupKey string) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return internalerrors.ErrInvalidInput
	}
	trimmedGroup := normalizeDescriptionOverrideGroupKey(groupKey)
	if _, err := r.execWrite(ctx, "delete description override", `DELETE FROM description_overrides WHERE source_path = ? AND group_key = ?`, trimmed, trimmedGroup); err != nil {
		return fmt.Errorf("db delete description override: %w", err)
	}
	return nil
}

func normalizeDescriptionOverrideGroupKey(groupKey string) string {
	return strings.ToLower(strings.TrimSpace(groupKey))
}

func nullString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullBool(value *bool) any {
	if value == nil {
		return nil
	}
	if *value {
		return 1
	}
	return 0
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	ptrValue := value.String
	return &ptrValue
}

func nullIntPtr(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	ptrValue := int(value.Int64)
	return &ptrValue
}

func nullBoolPtr(value sql.NullBool) *bool {
	if !value.Valid {
		return nil
	}
	ptrValue := value.Bool
	return &ptrValue
}

func encodeFileList(paths []string) string {
	if len(paths) == 0 {
		return "[]"
	}
	data, err := json.Marshal(paths)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func decodeFileList(value string) ([]string, error) {
	var result []string
	if strings.TrimSpace(value) == "" {
		return result, nil
	}
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return nil, fmt.Errorf("db: decode file list: %w", err)
	}
	return result, nil
}

func encodeStringList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func decodeStringList(value string) ([]string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var result []string
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("db: decode string list: %w", err)
	}
	return result, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func encodeOptionalJSON(value any) string {
	if value == nil {
		return ""
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(payload)
}

func decodeOptionalJSON[T any](value string) (*T, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var result T
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("db: decode optional JSON: %w", err)
	}
	return &result, nil
}

func (r *SQLiteRepository) ListHistoryEntries(ctx context.Context) ([]HistoryEntry, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT
			fm.path,
			fm.release_title,
			fm.release_source,
			fm.release_resolution,
			fm.updated_at,
			COALESCE(ur.status, ""),
			COALESCE(ur.created_at, ""),
			(
				SELECT COUNT(1)
				FROM tracker_rule_failures trf
				WHERE trf.source_path = fm.path
			)
		FROM file_metadata fm
		LEFT JOIN upload_records ur ON ur.id = (
			SELECT id
			FROM upload_records
			WHERE source_path = fm.path
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		)
		WHERE NOT (
			fm.source_size = 0
			AND TRIM(fm.disc_type) = ""
			AND TRIM(fm.video_path) = ""
			AND TRIM(fm.file_list) IN ("", "[]")
			AND TRIM(fm.release_type) = ""
			AND TRIM(fm.release_artist) = ""
			AND TRIM(fm.release_title) = ""
			AND TRIM(fm.release_subtitle) = ""
			AND TRIM(fm.release_alt) = ""
			AND fm.release_year = 0
			AND fm.release_month = 0
			AND fm.release_day = 0
			AND TRIM(fm.release_source) = ""
			AND TRIM(fm.release_resolution) = ""
			AND TRIM(fm.release_ext) = ""
			AND TRIM(fm.release_site) = ""
			AND TRIM(fm.release_genre) = ""
			AND TRIM(fm.release_channels) = ""
			AND TRIM(fm.release_collection) = ""
			AND TRIM(fm.release_region) = ""
			AND TRIM(fm.release_size) = ""
			AND TRIM(fm.release_group) = ""
			AND TRIM(fm.release_disc) = ""
		)
		ORDER BY fm.updated_at DESC, fm.path ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("db list history entries: %w", err)
	}
	defer rows.Close()

	entries := make([]HistoryEntry, 0)
	for rows.Next() {
		var entry HistoryEntry
		var metadataUpdatedAt string
		var uploadCreatedAt string
		if err := rows.Scan(
			&entry.SourcePath,
			&entry.ReleaseTitle,
			&entry.ReleaseSource,
			&entry.ReleaseResolution,
			&metadataUpdatedAt,
			&entry.LatestUploadStatus,
			&uploadCreatedAt,
			&entry.RuleFailureCount,
		); err != nil {
			return nil, fmt.Errorf("db list history entries: %w", err)
		}
		if metadataUpdatedAt != "" {
			if parsed, parseErr := time.Parse(time.RFC3339Nano, metadataUpdatedAt); parseErr == nil {
				entry.MetadataUpdatedAt = parsed
			}
		}
		if uploadCreatedAt != "" {
			if parsed, parseErr := time.Parse(time.RFC3339Nano, uploadCreatedAt); parseErr == nil {
				entry.LatestUploadAt = parsed
			}
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db list history entries: %w", err)
	}

	return entries, nil
}

func (r *SQLiteRepository) ListUploadHistoryByPath(ctx context.Context, sourcePath string) ([]UploadRecord, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT tracker, status, created_at, source_path
		FROM upload_records
		WHERE source_path = ?
		ORDER BY created_at DESC, id DESC
	`, trimmed)
	if err != nil {
		return nil, fmt.Errorf("db list upload history: %w", err)
	}
	defer rows.Close()

	records := make([]UploadRecord, 0)
	for rows.Next() {
		var record UploadRecord
		var createdAt string
		if err := rows.Scan(&record.Tracker, &record.Status, &createdAt, &record.SourcePath); err != nil {
			return nil, fmt.Errorf("db list upload history: %w", err)
		}
		if createdAt != "" {
			if parsed, parseErr := time.Parse(time.RFC3339Nano, createdAt); parseErr == nil {
				record.CreatedAt = parsed
			}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db list upload history: %w", err)
	}

	return records, nil
}

func (r *SQLiteRepository) ListPendingUploads(ctx context.Context) ([]UploadRecord, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT tracker, status, created_at, source_path
		FROM upload_records
		WHERE status IN ("pending", "pending-internal")
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("db list pending: %w", err)
	}
	defer rows.Close()

	var records []UploadRecord
	for rows.Next() {
		var record UploadRecord
		var createdAt string
		if err := rows.Scan(&record.Tracker, &record.Status, &createdAt, &record.SourcePath); err != nil {
			return nil, fmt.Errorf("db list pending: %w", err)
		}
		if createdAt != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
				record.CreatedAt = parsed
			}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db list pending: %w", err)
	}

	return records, nil
}

func (r *SQLiteRepository) CreateUploadRecord(ctx context.Context, record UploadRecord) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(record.Tracker) == "" {
		return internalerrors.ErrInvalidInput
	}
	if strings.TrimSpace(record.SourcePath) == "" {
		return internalerrors.ErrInvalidInput
	}
	status := strings.TrimSpace(record.Status)
	if status == "" {
		status = "pending"
	}
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	_, err := r.execWrite(ctx, "create upload record", `
		INSERT INTO upload_records (tracker, status, created_at, source_path)
		VALUES (?, ?, ?, ?)
	`, record.Tracker, status, createdAt.Format(time.RFC3339Nano), record.SourcePath)
	if err != nil {
		return fmt.Errorf("db create upload record: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) UpdateLatestUploadRecordStatus(ctx context.Context, sourcePath string, tracker string, status string) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmedPath := strings.TrimSpace(sourcePath)
	trimmedTracker := strings.TrimSpace(tracker)
	trimmedStatus := strings.TrimSpace(status)
	if trimmedPath == "" || trimmedTracker == "" || trimmedStatus == "" {
		return internalerrors.ErrInvalidInput
	}

	result, err := r.execWrite(ctx, "update upload status", `
		UPDATE upload_records
		SET status = ?
		WHERE id = (
			SELECT id
			FROM upload_records
			WHERE source_path = ? AND tracker = ?
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		)
	`, trimmedStatus, trimmedPath, trimmedTracker)
	if err != nil {
		return fmt.Errorf("db update upload status: %w", err)
	}

	updatedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("db update upload status: rows affected: %w", err)
	}
	if updatedRows == 0 {
		return internalerrors.ErrNotFound
	}

	return nil
}

func (r *SQLiteRepository) SaveTrackerRuleFailures(ctx context.Context, sourcePath string, tracker string, failures []TrackerRuleFailure) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmedPath := strings.TrimSpace(sourcePath)
	trimmedTracker := strings.TrimSpace(tracker)
	if trimmedPath == "" || trimmedTracker == "" {
		return internalerrors.ErrInvalidInput
	}

	if err := r.withWriteTx(ctx, "save tracker rule failures", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM tracker_rule_failures
			WHERE source_path = ? AND tracker = ?
		`, trimmedPath, strings.ToUpper(trimmedTracker)); err != nil {
			return fmt.Errorf("db save tracker rule failures: delete: %w", err)
		}

		if len(failures) > 0 {
			stmt, err := tx.PrepareContext(ctx, `
				INSERT INTO tracker_rule_failures (source_path, tracker, rule, reason, created_at)
				VALUES (?, ?, ?, ?, ?)
			`)
			if err != nil {
				return fmt.Errorf("db save tracker rule failures: prepare: %w", err)
			}
			defer stmt.Close()

			for _, failure := range failures {
				rule := strings.TrimSpace(failure.Rule)
				if rule == "" {
					continue
				}
				createdAt := failure.CreatedAt
				if createdAt.IsZero() {
					createdAt = time.Now().UTC()
				}
				if _, err := stmt.ExecContext(ctx,
					trimmedPath,
					strings.ToUpper(trimmedTracker),
					rule,
					strings.TrimSpace(failure.Reason),
					createdAt.Format(time.RFC3339Nano),
				); err != nil {
					return fmt.Errorf("db save tracker rule failures: insert: %w", err)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *SQLiteRepository) ListTrackerRuleFailuresByPath(ctx context.Context, path string) ([]TrackerRuleFailure, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT source_path, tracker, rule, reason, created_at
		FROM tracker_rule_failures
		WHERE source_path = ?
		ORDER BY id ASC
	`, trimmed)
	if err != nil {
		return nil, fmt.Errorf("db list tracker rule failures: %w", err)
	}
	defer rows.Close()

	var failures []TrackerRuleFailure
	for rows.Next() {
		var record TrackerRuleFailure
		var createdAt string
		if err := rows.Scan(&record.SourcePath, &record.Tracker, &record.Rule, &record.Reason, &createdAt); err != nil {
			return nil, fmt.Errorf("db list tracker rule failures: %w", err)
		}
		if createdAt != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
				record.CreatedAt = parsed
			}
		}
		failures = append(failures, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db list tracker rule failures: %w", err)
	}

	return failures, nil
}

func (r *SQLiteRepository) GetTrackerTimestamp(ctx context.Context, tracker string) (time.Time, error) {
	if r == nil || r.db == nil {
		return time.Time{}, errors.New("db: repository not initialized")
	}
	name := strings.TrimSpace(tracker)
	if name == "" {
		return time.Time{}, internalerrors.ErrInvalidInput
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT updated_at
		FROM tracker_timestamps
		WHERE tracker = ?
	`, name)
	var updatedAt string
	if err := row.Scan(&updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, internalerrors.ErrNotFound
		}
		return time.Time{}, fmt.Errorf("db tracker timestamp: %w", err)
	}
	if updatedAt == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("db tracker timestamp parse: %w", err)
	}
	return parsed, nil
}

func (r *SQLiteRepository) SaveTrackerTimestamp(ctx context.Context, timestamp TrackerTimestamp) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	name := strings.TrimSpace(timestamp.Tracker)
	if name == "" {
		return internalerrors.ErrInvalidInput
	}
	value := timestamp.UpdatedAt
	if value.IsZero() {
		value = time.Now().UTC()
	}
	_, err := r.execWrite(ctx, "save tracker timestamp", `
		INSERT INTO tracker_timestamps (tracker, updated_at)
		VALUES (?, ?)
		ON CONFLICT(tracker) DO UPDATE SET
			updated_at = excluded.updated_at
	`, name, value.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("db save tracker timestamp: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) SaveTrackerMetadata(ctx context.Context, metadata TrackerMetadata) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(metadata.SourcePath) == "" || strings.TrimSpace(metadata.Tracker) == "" {
		return internalerrors.ErrInvalidInput
	}
	if metadata.Category.IsValid() {
		metadata.Category = metadata.Category.Canonical()
	} else {
		metadata.Category = api.CategoryUnknown
	}
	updatedAt := metadata.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	imageURLs := encodeStringList(metadata.ImageURLs)
	matched := 0
	if metadata.Matched {
		matched = 1
	}
	_, err := r.execWrite(ctx, "save tracker metadata", `
		INSERT INTO tracker_metadata (
			source_path, tracker, tracker_id, info_hash, tmdb_id, imdb_id, tvdb_id, mal_id,
			category, description, image_urls, filename, matched, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path, tracker) DO UPDATE SET
			tracker_id = excluded.tracker_id,
			info_hash = excluded.info_hash,
			tmdb_id = excluded.tmdb_id,
			imdb_id = excluded.imdb_id,
			tvdb_id = excluded.tvdb_id,
			mal_id = excluded.mal_id,
			category = excluded.category,
			description = excluded.description,
			image_urls = excluded.image_urls,
			filename = excluded.filename,
			matched = excluded.matched,
			updated_at = excluded.updated_at
	`,
		metadata.SourcePath,
		metadata.Tracker,
		metadata.TrackerID,
		metadata.InfoHash,
		metadata.TMDBID,
		metadata.IMDBID,
		metadata.TVDBID,
		metadata.MALID,
		metadata.Category,
		metadata.Description,
		imageURLs,
		metadata.Filename,
		matched,
		updatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("db save tracker metadata: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) ListTrackerMetadataByPath(ctx context.Context, path string) ([]TrackerMetadata, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, internalerrors.ErrInvalidInput
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT tracker, tracker_id, info_hash, tmdb_id, imdb_id, tvdb_id, mal_id,
			category, description, image_urls, filename, matched, updated_at
		FROM tracker_metadata
		WHERE source_path = ?
		ORDER BY tracker ASC
	`, trimmed)
	if err != nil {
		return nil, fmt.Errorf("db list tracker metadata: %w", err)
	}
	defer rows.Close()

	var records []TrackerMetadata
	for rows.Next() {
		var record TrackerMetadata
		var imageURLs string
		var matched int
		var updatedAt string
		if err := rows.Scan(
			&record.Tracker,
			&record.TrackerID,
			&record.InfoHash,
			&record.TMDBID,
			&record.IMDBID,
			&record.TVDBID,
			&record.MALID,
			&record.Category,
			&record.Description,
			&imageURLs,
			&record.Filename,
			&matched,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("db list tracker metadata: %w", err)
		}
		record.SourcePath = trimmed
		record.Matched = matched != 0
		if imageURLs != "" {
			if parsed, err := decodeStringList(imageURLs); err == nil {
				record.ImageURLs = parsed
			}
		}
		if updatedAt != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
				record.UpdatedAt = parsed
			}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db list tracker metadata: %w", err)
	}
	return records, nil
}

func (r *SQLiteRepository) SaveScreenshot(ctx context.Context, screenshot Screenshot) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(screenshot.SourcePath) == "" || strings.TrimSpace(screenshot.ImagePath) == "" {
		return internalerrors.ErrInvalidInput
	}
	_, err := r.execWrite(ctx, "save screenshot", `
		INSERT OR REPLACE INTO screenshots (
			source_path, image_path, timestamp, frame_number, width, height, purpose, captured_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, screenshot.SourcePath, screenshot.ImagePath, screenshot.Timestamp, screenshot.FrameNumber,
		screenshot.Width, screenshot.Height, screenshot.Purpose, screenshot.CapturedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("db save screenshot: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) ListScreenshotsByPath(ctx context.Context, path string) ([]Screenshot, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, internalerrors.ErrInvalidInput
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT image_path, timestamp, frame_number, width, height, purpose, captured_at
		FROM screenshots
		WHERE source_path = ?
		ORDER BY timestamp ASC
	`, trimmed)
	if err != nil {
		return nil, fmt.Errorf("db list screenshots: %w", err)
	}
	defer rows.Close()

	var screenshots []Screenshot
	for rows.Next() {
		var shot Screenshot
		var purpose string
		var capturedAt string
		if err := rows.Scan(
			&shot.ImagePath,
			&shot.Timestamp,
			&shot.FrameNumber,
			&shot.Width,
			&shot.Height,
			&purpose,
			&capturedAt,
		); err != nil {
			return nil, fmt.Errorf("db list screenshots: %w", err)
		}
		shot.SourcePath = trimmed
		shot.Purpose = ScreenshotPurpose(purpose)
		if capturedAt != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, capturedAt); err == nil {
				shot.CapturedAt = parsed
			}
		}
		screenshots = append(screenshots, shot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db list screenshots: %w", err)
	}
	return screenshots, nil
}

func (r *SQLiteRepository) DeleteScreenshot(ctx context.Context, imagePath string) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(imagePath) == "" {
		return internalerrors.ErrInvalidInput
	}
	if err := r.withWriteTx(ctx, "delete screenshot", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM uploaded_images WHERE image_path = ?`, imagePath); err != nil {
			return fmt.Errorf("db delete screenshot: delete uploaded images: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM screenshots WHERE image_path = ?`, imagePath); err != nil {
			return fmt.Errorf("db delete screenshot: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *SQLiteRepository) SaveFinalSelections(ctx context.Context, path string, selections []ScreenshotFinalSelection) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return internalerrors.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("db save final selections: context canceled: %w", err)
	}

	if err := r.withWriteTx(ctx, "save final selections", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM screenshot_final_selections WHERE source_path = ?`, trimmed); err != nil {
			return fmt.Errorf("db save final selections: clear: %w", err)
		}

		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO screenshot_final_selections (
				source_path, image_path, sort_order, source, selected_at
			) VALUES (?, ?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("db save final selections: prepare: %w", err)
		}
		defer stmt.Close()

		for _, selection := range selections {
			if strings.TrimSpace(selection.ImagePath) == "" {
				return internalerrors.ErrInvalidInput
			}
			source := strings.TrimSpace(selection.Source)
			if source == "" {
				source = "unknown"
			}
			selectedAt := selection.SelectedAt
			if selectedAt.IsZero() {
				selectedAt = time.Now().UTC()
			}
			if _, err := stmt.ExecContext(
				ctx,
				trimmed,
				selection.ImagePath,
				selection.Order,
				source,
				selectedAt.UTC().Format(time.RFC3339Nano),
			); err != nil {
				return fmt.Errorf("db save final selections: insert: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *SQLiteRepository) ListFinalSelections(ctx context.Context, path string) ([]ScreenshotFinalSelection, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, internalerrors.ErrInvalidInput
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT image_path, sort_order, source, selected_at
		FROM screenshot_final_selections
		WHERE source_path = ?
		ORDER BY sort_order ASC
	`, trimmed)
	if err != nil {
		return nil, fmt.Errorf("db list final selections: %w", err)
	}
	defer rows.Close()

	var selections []ScreenshotFinalSelection
	for rows.Next() {
		var selection ScreenshotFinalSelection
		var selectedAt string
		if err := rows.Scan(&selection.ImagePath, &selection.Order, &selection.Source, &selectedAt); err != nil {
			return nil, fmt.Errorf("db list final selections: %w", err)
		}
		selection.SourcePath = trimmed
		if selectedAt != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, selectedAt); err == nil {
				selection.SelectedAt = parsed
			}
		}
		selections = append(selections, selection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db list final selections: %w", err)
	}
	return selections, nil
}

func (r *SQLiteRepository) DeleteFinalSelection(ctx context.Context, imagePath string) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(imagePath) == "" {
		return internalerrors.ErrInvalidInput
	}
	_, err := r.execWrite(ctx, "delete final selection", `DELETE FROM screenshot_final_selections WHERE image_path = ?`, imagePath)
	if err != nil {
		return fmt.Errorf("db delete final selection: %w", err)
	}
	return nil
}

// ReplaceNormalFinalSelections atomically replaces non-menu final selections
// for path while preserving manual and automatic disc-menu selections.
func (r *SQLiteRepository) ReplaceNormalFinalSelections(ctx context.Context, path string, selections []ScreenshotFinalSelection) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return internalerrors.ErrInvalidInput
	}
	for _, selection := range selections {
		if strings.TrimSpace(selection.SourcePath) != trimmed || strings.TrimSpace(selection.ImagePath) == "" || api.IsDiscMenuSelectionSource(strings.TrimSpace(selection.Source)) {
			return internalerrors.ErrInvalidInput
		}
	}

	return r.withWriteTx(ctx, "replace normal final selections", func(tx *sql.Tx) error {
		existing, err := listFinalSelectionsTx(ctx, tx, trimmed)
		if err != nil {
			return err
		}
		merged := make([]ScreenshotFinalSelection, 0, len(existing)+len(selections))
		for _, selection := range existing {
			if api.IsDiscMenuSelectionSource(strings.TrimSpace(selection.Source)) {
				merged = append(merged, selection)
			}
		}
		merged = append(merged, selections...)
		return rewriteFinalSelectionsTx(ctx, tx, trimmed, merged)
	})
}

// AppendManualMenuScreenshots atomically upserts manual menu screenshot records
// and appends their selections after existing manual-menu order values.
func (r *SQLiteRepository) AppendManualMenuScreenshots(ctx context.Context, path string, screenshots []Screenshot, selections []ScreenshotFinalSelection) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || !validMenuScreenshotBatch(trimmed, screenshots, selections, api.ScreenshotSelectionSourceMenu) {
		return internalerrors.ErrInvalidInput
	}

	return r.withWriteTx(ctx, "append manual menu screenshots", func(tx *sql.Tx) error {
		if err := upsertMenuScreenshotsTx(ctx, tx, trimmed, screenshots); err != nil {
			return err
		}
		existing, err := listFinalSelectionsTx(ctx, tx, trimmed)
		if err != nil {
			return err
		}
		maxManualOrder := -1
		for _, selection := range existing {
			if strings.TrimSpace(selection.Source) == api.ScreenshotSelectionSourceMenu && selection.Order > maxManualOrder {
				maxManualOrder = selection.Order
			}
		}
		for index := range selections {
			selections[index].Order = maxManualOrder + 1 + index
		}
		return rewriteFinalSelectionsTx(ctx, tx, trimmed, append(existing, selections...))
	})
}

// ReplaceDVDMenuScreenshots atomically replaces automatic DVD-menu records and
// selections while preserving manual menus and normal screenshots. It returns
// replaced local image paths for caller-owned filesystem cleanup.
func (r *SQLiteRepository) ReplaceDVDMenuScreenshots(ctx context.Context, path string, screenshots []Screenshot, selections []ScreenshotFinalSelection) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || !validMenuScreenshotBatch(trimmed, screenshots, selections, api.ScreenshotSelectionSourceDVDMenu) {
		return nil, internalerrors.ErrInvalidInput
	}

	var replaced []string
	err := r.withWriteTx(ctx, "replace DVD menu screenshots", func(tx *sql.Tx) error {
		existing, err := listFinalSelectionsTx(ctx, tx, trimmed)
		if err != nil {
			return err
		}
		preserved := make([]ScreenshotFinalSelection, 0, len(existing)+len(selections))
		for _, selection := range existing {
			if strings.TrimSpace(selection.Source) == api.ScreenshotSelectionSourceDVDMenu {
				replaced = append(replaced, selection.ImagePath)
				if err := deleteScreenshotReferencesTx(ctx, tx, trimmed, selection.ImagePath); err != nil {
					return err
				}
				continue
			}
			preserved = append(preserved, selection)
		}
		if err := upsertMenuScreenshotsTx(ctx, tx, trimmed, screenshots); err != nil {
			return err
		}
		preserved = append(preserved, selections...)
		return rewriteFinalSelectionsTx(ctx, tx, trimmed, preserved)
	})
	if err != nil {
		return nil, err
	}
	return replaced, nil
}

// DeleteDiscMenuScreenshot atomically removes one manual or automatic menu
// selection, its screenshot row, and local upload records. It rejects normal
// final selections and reports how many remote-host references were orphaned.
func (r *SQLiteRepository) DeleteDiscMenuScreenshot(ctx context.Context, path string, imagePath string) (api.DiscMenuDeleteResult, error) {
	if r == nil || r.db == nil {
		return api.DiscMenuDeleteResult{}, errors.New("db: repository not initialized")
	}
	trimmedPath := strings.TrimSpace(path)
	trimmedImage := strings.TrimSpace(imagePath)
	if trimmedPath == "" || trimmedImage == "" {
		return api.DiscMenuDeleteResult{}, internalerrors.ErrInvalidInput
	}

	var deleted api.DiscMenuDeleteResult
	err := r.withWriteTx(ctx, "delete disc menu screenshot", func(tx *sql.Tx) error {
		var selectedAt string
		err := tx.QueryRowContext(ctx, `
			SELECT sort_order, source, selected_at
			FROM screenshot_final_selections
			WHERE source_path = ? AND image_path = ?
		`, trimmedPath, trimmedImage).Scan(&deleted.Selection.Order, &deleted.Selection.Source, &selectedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return internalerrors.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("db delete disc menu screenshot: load selection: %w", err)
		}
		if !api.IsDiscMenuSelectionSource(strings.TrimSpace(deleted.Selection.Source)) {
			return internalerrors.ErrInvalidInput
		}
		deleted.Selection.SourcePath = trimmedPath
		deleted.Selection.ImagePath = trimmedImage
		if selectedAt != "" {
			if parsed, parseErr := time.Parse(time.RFC3339Nano, selectedAt); parseErr == nil {
				deleted.Selection.SelectedAt = parsed
			}
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM uploaded_images WHERE source_path = ? AND image_path = ?
		`, trimmedPath, trimmedImage).Scan(&deleted.UploadedLinks); err != nil {
			return fmt.Errorf("db delete disc menu screenshot: count uploaded images: %w", err)
		}
		if err := deleteScreenshotReferencesTx(ctx, tx, trimmedPath, trimmedImage); err != nil {
			return err
		}
		remaining, err := listFinalSelectionsTx(ctx, tx, trimmedPath)
		if err != nil {
			return err
		}
		filtered := remaining[:0]
		for _, selection := range remaining {
			if selection.ImagePath != trimmedImage {
				filtered = append(filtered, selection)
			}
		}
		return rewriteFinalSelectionsTx(ctx, tx, trimmedPath, filtered)
	})
	if err != nil {
		return api.DiscMenuDeleteResult{}, err
	}
	return deleted, nil
}

func validMenuScreenshotBatch(path string, screenshots []Screenshot, selections []ScreenshotFinalSelection, source string) bool {
	if len(screenshots) != len(selections) {
		return false
	}
	paths := make(map[string]struct{}, len(screenshots))
	for _, screenshot := range screenshots {
		if strings.TrimSpace(screenshot.SourcePath) != path || strings.TrimSpace(screenshot.ImagePath) == "" || screenshot.Purpose != api.ScreenshotPurposeMenu {
			return false
		}
		imagePath := strings.TrimSpace(screenshot.ImagePath)
		if _, exists := paths[imagePath]; exists {
			return false
		}
		paths[imagePath] = struct{}{}
	}
	for _, selection := range selections {
		if strings.TrimSpace(selection.SourcePath) != path || strings.TrimSpace(selection.ImagePath) == "" || strings.TrimSpace(selection.Source) != source {
			return false
		}
		imagePath := strings.TrimSpace(selection.ImagePath)
		if _, exists := paths[imagePath]; !exists {
			return false
		}
		delete(paths, imagePath)
	}
	return len(paths) == 0
}

func upsertMenuScreenshotsTx(ctx context.Context, tx *sql.Tx, path string, screenshots []Screenshot) error {
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO screenshots (
			source_path, image_path, timestamp, frame_number, width, height, purpose, captured_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(image_path) DO UPDATE SET
			timestamp = excluded.timestamp,
			frame_number = excluded.frame_number,
			width = excluded.width,
			height = excluded.height,
			purpose = excluded.purpose,
			captured_at = excluded.captured_at
		WHERE screenshots.source_path = excluded.source_path
	`)
	if err != nil {
		return fmt.Errorf("db save menu screenshots: prepare: %w", err)
	}
	defer stmt.Close()

	for _, screenshot := range screenshots {
		capturedAt := screenshot.CapturedAt
		if capturedAt.IsZero() {
			capturedAt = time.Now().UTC()
		}
		result, err := stmt.ExecContext(
			ctx,
			path,
			screenshot.ImagePath,
			screenshot.Timestamp,
			screenshot.FrameNumber,
			screenshot.Width,
			screenshot.Height,
			screenshot.Purpose,
			capturedAt.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			return fmt.Errorf("db save menu screenshots: insert: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("db save menu screenshots: rows affected: %w", err)
		}
		if rows == 0 {
			return internalerrors.ErrInvalidInput
		}
	}
	return nil
}

func listFinalSelectionsTx(ctx context.Context, tx *sql.Tx, path string) ([]ScreenshotFinalSelection, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT image_path, sort_order, source, selected_at
		FROM screenshot_final_selections
		WHERE source_path = ?
		ORDER BY sort_order ASC
	`, path)
	if err != nil {
		return nil, fmt.Errorf("db list final selections in transaction: %w", err)
	}
	defer rows.Close()

	selections := make([]ScreenshotFinalSelection, 0)
	for rows.Next() {
		var selection ScreenshotFinalSelection
		var selectedAt string
		if err := rows.Scan(&selection.ImagePath, &selection.Order, &selection.Source, &selectedAt); err != nil {
			return nil, fmt.Errorf("db list final selections in transaction: scan: %w", err)
		}
		selection.SourcePath = path
		if selectedAt != "" {
			if parsed, parseErr := time.Parse(time.RFC3339Nano, selectedAt); parseErr == nil {
				selection.SelectedAt = parsed
			}
		}
		selections = append(selections, selection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db list final selections in transaction: iterate: %w", err)
	}
	return selections, nil
}

func rewriteFinalSelectionsTx(ctx context.Context, tx *sql.Tx, path string, selections []ScreenshotFinalSelection) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM screenshot_final_selections WHERE source_path = ?`, path); err != nil {
		return fmt.Errorf("db rewrite final selections: clear: %w", err)
	}
	ordered := orderFinalSelections(path, selections)
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO screenshot_final_selections (
			source_path, image_path, sort_order, source, selected_at
		) VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("db rewrite final selections: prepare: %w", err)
	}
	defer stmt.Close()
	for _, selection := range ordered {
		if _, err := stmt.ExecContext(
			ctx,
			path,
			selection.ImagePath,
			selection.Order,
			selection.Source,
			selection.SelectedAt.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("db rewrite final selections: insert: %w", err)
		}
	}
	return nil
}

func orderFinalSelections(path string, selections []ScreenshotFinalSelection) []ScreenshotFinalSelection {
	groups := [3][]ScreenshotFinalSelection{}
	for _, selection := range selections {
		selection.ImagePath = strings.TrimSpace(selection.ImagePath)
		if selection.ImagePath == "" {
			continue
		}
		selection.Source = strings.TrimSpace(selection.Source)
		if selection.Source == "" {
			selection.Source = "unknown"
		}
		rank := 2
		switch selection.Source {
		case api.ScreenshotSelectionSourceDVDMenu:
			rank = 0
		case api.ScreenshotSelectionSourceMenu:
			rank = 1
		}
		groups[rank] = append(groups[rank], selection)
	}
	for idx := range groups {
		sort.SliceStable(groups[idx], func(left, right int) bool {
			return groups[idx][left].Order < groups[idx][right].Order
		})
	}

	ordered := make([]ScreenshotFinalSelection, 0, len(selections))
	seen := make(map[string]struct{}, len(selections))
	for _, group := range groups {
		for _, selection := range group {
			if _, exists := seen[selection.ImagePath]; exists {
				continue
			}
			seen[selection.ImagePath] = struct{}{}
			selection.SourcePath = path
			selection.Order = len(ordered)
			if selection.SelectedAt.IsZero() {
				selection.SelectedAt = time.Now().UTC()
			}
			ordered = append(ordered, selection)
		}
	}
	return ordered
}

func deleteScreenshotReferencesTx(ctx context.Context, tx *sql.Tx, path string, imagePath string) error {
	queries := []string{
		`DELETE FROM screenshot_slot_variants
		 WHERE source_path = ? AND (
			image_path = ? OR slot_order IN (
				SELECT slot_order FROM screenshot_slots WHERE source_path = ? AND image_path = ?
			)
		)`,
		`DELETE FROM screenshot_slots WHERE source_path = ? AND image_path = ?`,
		`DELETE FROM uploaded_images WHERE source_path = ? AND image_path = ?`,
		`DELETE FROM screenshots WHERE source_path = ? AND image_path = ? AND purpose = ?`,
	}
	for index, query := range queries {
		var args []any
		switch index {
		case 0:
			args = []any{path, imagePath, path, imagePath}
		case 1, 2:
			args = []any{path, imagePath}
		case 3:
			args = []any{path, imagePath, api.ScreenshotPurposeMenu}
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("db delete screenshot references: %w", err)
		}
	}
	return nil
}

func (r *SQLiteRepository) ReplaceScreenshotSlots(ctx context.Context, path string, slots []ScreenshotSlot) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return internalerrors.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("db replace screenshot slots: context canceled: %w", err)
	}

	if err := r.withWriteTx(ctx, "replace screenshot slots", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM screenshot_slot_variants WHERE source_path = ?`, trimmed); err != nil {
			return fmt.Errorf("db replace screenshot slots: clear variants: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM screenshot_slots WHERE source_path = ?`, trimmed); err != nil {
			return fmt.Errorf("db replace screenshot slots: clear slots: %w", err)
		}

		slotStmt, err := tx.PrepareContext(ctx, `
			INSERT INTO screenshot_slots (
				source_path, slot_order, source_kind, original_key, original_url, original_host,
				image_path, from_description, tracker, section_kind, render_in_screenshots
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("db replace screenshot slots: prepare slots: %w", err)
		}
		defer slotStmt.Close()

		variantStmt, err := tx.PrepareContext(ctx, `
			INSERT INTO screenshot_slot_variants (
				source_path, slot_order, host, usage_scope, image_path, img_url, raw_url, web_url, uploaded_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("db replace screenshot slots: prepare variants: %w", err)
		}
		defer variantStmt.Close()

		for _, slot := range slots {
			if _, err := slotStmt.ExecContext(
				ctx,
				trimmed,
				slot.SlotOrder,
				strings.TrimSpace(slot.SourceKind),
				strings.TrimSpace(slot.OriginalKey),
				strings.TrimSpace(slot.OriginalURL),
				strings.TrimSpace(slot.OriginalHost),
				strings.TrimSpace(slot.ImagePath),
				boolToInt(slot.FromDescription),
				strings.TrimSpace(slot.Tracker),
				strings.TrimSpace(slot.SectionKind),
				boolToInt(slot.RenderInScreenshots),
			); err != nil {
				return fmt.Errorf("db replace screenshot slots: insert slot %d: %w", slot.SlotOrder, err)
			}
			for _, variant := range slot.Variants {
				uploadedAt := ""
				if !variant.UploadedAt.IsZero() {
					uploadedAt = variant.UploadedAt.UTC().Format(time.RFC3339Nano)
				}
				if _, err := variantStmt.ExecContext(
					ctx,
					trimmed,
					slot.SlotOrder,
					strings.TrimSpace(variant.Host),
					strings.TrimSpace(variant.UsageScope),
					strings.TrimSpace(variant.ImagePath),
					strings.TrimSpace(variant.ImgURL),
					strings.TrimSpace(variant.RawURL),
					strings.TrimSpace(variant.WebURL),
					uploadedAt,
				); err != nil {
					return fmt.Errorf("db replace screenshot slots: insert variant slot=%d host=%s: %w", slot.SlotOrder, variant.Host, err)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *SQLiteRepository) ListScreenshotSlotsByPath(ctx context.Context, path string) ([]ScreenshotSlot, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	slotRows, err := r.db.QueryContext(ctx, `
		SELECT slot_order, source_kind, original_key, original_url, original_host, image_path,
			from_description, tracker, section_kind, render_in_screenshots
		FROM screenshot_slots
		WHERE source_path = ?
		ORDER BY slot_order ASC
	`, trimmed)
	if err != nil {
		return nil, fmt.Errorf("db list screenshot slots: %w", err)
	}
	defer slotRows.Close()

	slots := make([]ScreenshotSlot, 0)
	slotMap := make(map[int]int)
	for slotRows.Next() {
		var slot ScreenshotSlot
		var fromDescription int
		var renderInScreenshots int
		if err := slotRows.Scan(
			&slot.SlotOrder,
			&slot.SourceKind,
			&slot.OriginalKey,
			&slot.OriginalURL,
			&slot.OriginalHost,
			&slot.ImagePath,
			&fromDescription,
			&slot.Tracker,
			&slot.SectionKind,
			&renderInScreenshots,
		); err != nil {
			return nil, fmt.Errorf("db list screenshot slots: %w", err)
		}
		slot.SourcePath = trimmed
		slot.FromDescription = fromDescription != 0
		slot.RenderInScreenshots = renderInScreenshots != 0
		slots = append(slots, slot)
		slotMap[slot.SlotOrder] = len(slots) - 1
	}
	if err := slotRows.Err(); err != nil {
		return nil, fmt.Errorf("db list screenshot slots: %w", err)
	}
	if len(slots) == 0 {
		return nil, nil
	}

	variantRows, err := r.db.QueryContext(ctx, `
		SELECT slot_order, host, usage_scope, image_path, img_url, raw_url, web_url, uploaded_at
		FROM screenshot_slot_variants
		WHERE source_path = ?
		ORDER BY slot_order ASC
	`, trimmed)
	if err != nil {
		return nil, fmt.Errorf("db list screenshot slot variants: %w", err)
	}
	defer variantRows.Close()

	for variantRows.Next() {
		var variant ScreenshotSlotVariant
		var uploadedAt string
		if err := variantRows.Scan(
			&variant.SlotOrder,
			&variant.Host,
			&variant.UsageScope,
			&variant.ImagePath,
			&variant.ImgURL,
			&variant.RawURL,
			&variant.WebURL,
			&uploadedAt,
		); err != nil {
			return nil, fmt.Errorf("db list screenshot slot variants: %w", err)
		}
		variant.SourcePath = trimmed
		if uploadedAt != "" {
			if parsed, parseErr := time.Parse(time.RFC3339Nano, uploadedAt); parseErr == nil {
				variant.UploadedAt = parsed
			}
		}
		if slotIndex, ok := slotMap[variant.SlotOrder]; ok {
			slots[slotIndex].Variants = append(slots[slotIndex].Variants, variant)
		}
	}
	if err := variantRows.Err(); err != nil {
		return nil, fmt.Errorf("db list screenshot slot variants: %w", err)
	}

	return slots, nil
}

func (r *SQLiteRepository) UpsertScreenshotSlotVariants(ctx context.Context, path string, variants []ScreenshotSlotVariant) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return internalerrors.ErrInvalidInput
	}
	if len(variants) == 0 {
		return nil
	}

	if err := r.withWriteTx(ctx, "upsert screenshot slot variants", func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO screenshot_slot_variants (
				source_path, slot_order, host, usage_scope, image_path, img_url, raw_url, web_url, uploaded_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_path, slot_order, usage_scope, host)
			DO UPDATE SET
				image_path = excluded.image_path,
				img_url = excluded.img_url,
				raw_url = excluded.raw_url,
				web_url = excluded.web_url,
				uploaded_at = excluded.uploaded_at
		`)
		if err != nil {
			return fmt.Errorf("db upsert screenshot slot variants: prepare: %w", err)
		}
		defer stmt.Close()

		for _, variant := range variants {
			uploadedAt := ""
			if !variant.UploadedAt.IsZero() {
				uploadedAt = variant.UploadedAt.UTC().Format(time.RFC3339Nano)
			}
			if _, err := stmt.ExecContext(
				ctx,
				trimmed,
				variant.SlotOrder,
				strings.TrimSpace(variant.Host),
				strings.TrimSpace(variant.UsageScope),
				strings.TrimSpace(variant.ImagePath),
				strings.TrimSpace(variant.ImgURL),
				strings.TrimSpace(variant.RawURL),
				strings.TrimSpace(variant.WebURL),
				uploadedAt,
			); err != nil {
				return fmt.Errorf("db upsert screenshot slot variants: slot=%d host=%s: %w", variant.SlotOrder, variant.Host, err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *SQLiteRepository) SaveUploadedImages(ctx context.Context, path string, host string, images []UploadedImageLink) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return internalerrors.ErrInvalidInput
	}
	trimmedHost := strings.TrimSpace(host)
	if trimmedHost == "" {
		return internalerrors.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("db save uploaded images: context canceled: %w", err)
	}

	if err := r.withWriteTx(ctx, "save uploaded images", func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO uploaded_images (
				source_path, image_path, host, usage_scope, img_url, raw_url, web_url, size_bytes, uploaded_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_path, usage_scope, host, image_path)
			DO UPDATE SET
				img_url = excluded.img_url,
				raw_url = excluded.raw_url,
				web_url = excluded.web_url,
				size_bytes = excluded.size_bytes,
				uploaded_at = excluded.uploaded_at
		`)
		if err != nil {
			return fmt.Errorf("db save uploaded images: prepare: %w", err)
		}
		defer stmt.Close()

		for _, image := range images {
			if strings.TrimSpace(image.ImagePath) == "" {
				return internalerrors.ErrInvalidInput
			}
			usageScope := strings.TrimSpace(image.UsageScope)
			if usageScope == "" {
				usageScope = "global"
			}
			uploadedAt := image.UploadedAt
			if uploadedAt.IsZero() {
				uploadedAt = time.Now().UTC()
			}
			if _, err := stmt.ExecContext(
				ctx,
				trimmed,
				image.ImagePath,
				trimmedHost,
				usageScope,
				strings.TrimSpace(image.ImgURL),
				strings.TrimSpace(image.RawURL),
				strings.TrimSpace(image.WebURL),
				image.SizeBytes,
				uploadedAt.UTC().Format(time.RFC3339Nano),
			); err != nil {
				return fmt.Errorf("db save uploaded images: insert: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *SQLiteRepository) ListUploadedImagesByPath(ctx context.Context, path string) ([]UploadedImageLink, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT image_path, host, usage_scope, img_url, raw_url, web_url, size_bytes, uploaded_at
		FROM uploaded_images
		WHERE source_path = ?
		ORDER BY id ASC
	`, trimmed)
	if err != nil {
		return nil, fmt.Errorf("db list uploaded images: %w", err)
	}
	defer rows.Close()

	var images []UploadedImageLink
	for rows.Next() {
		var image UploadedImageLink
		var uploadedAt string
		if err := rows.Scan(
			&image.ImagePath,
			&image.Host,
			&image.UsageScope,
			&image.ImgURL,
			&image.RawURL,
			&image.WebURL,
			&image.SizeBytes,
			&uploadedAt,
		); err != nil {
			return nil, fmt.Errorf("db list uploaded images: %w", err)
		}
		image.SourcePath = trimmed
		if strings.TrimSpace(image.UsageScope) == "" {
			image.UsageScope = "global"
		}
		if uploadedAt != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, uploadedAt); err == nil {
				image.UploadedAt = parsed
			}
		}
		images = append(images, image)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db list uploaded images: %w", err)
	}
	return images, nil
}

func (r *SQLiteRepository) DeleteUploadedImage(ctx context.Context, path string, imagePath string, host string) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return internalerrors.ErrInvalidInput
	}
	trimmedImagePath := strings.TrimSpace(imagePath)
	if trimmedImagePath == "" {
		return internalerrors.ErrInvalidInput
	}
	trimmedHost := strings.TrimSpace(host)
	if trimmedHost == "" {
		return internalerrors.ErrInvalidInput
	}
	result, err := r.execWrite(ctx, "delete uploaded image", `
		DELETE FROM uploaded_images
		WHERE source_path = ? AND host = ? AND image_path = ?
	`, trimmedPath, trimmedHost, trimmedImagePath)
	if err != nil {
		return fmt.Errorf("db delete uploaded image: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("db delete uploaded image: rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("db delete uploaded image: no image found at path %q for host %q", trimmedImagePath, trimmedHost)
	}
	return nil
}

func (r *SQLiteRepository) ListStoredReleasePaths(ctx context.Context) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("db: repository not initialized")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT source_path FROM (
			SELECT path AS source_path FROM file_metadata
			UNION
			SELECT source_path FROM dvd_mediainfo
			UNION
			SELECT source_path FROM external_metadata
			UNION
			SELECT source_path FROM external_ids
			UNION
			SELECT source_path FROM release_overrides
			UNION
			SELECT source_path FROM description_overrides
			UNION
			SELECT source_path FROM playlist_selections
			UNION
			SELECT source_path FROM tracker_metadata
			UNION
			SELECT source_path FROM tracker_rule_failures
			UNION
			SELECT source_path FROM screenshot_final_selections
			UNION
			SELECT source_path FROM screenshots
			UNION
			SELECT source_path FROM screenshot_slots
			UNION
			SELECT source_path FROM screenshot_slot_variants
			UNION
			SELECT source_path FROM uploaded_images
			UNION
			SELECT source_path FROM upload_records
		)
		WHERE TRIM(source_path) <> ''
		ORDER BY source_path ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("db list stored release paths: %w", err)
	}
	defer rows.Close()

	paths := make([]string, 0)
	for rows.Next() {
		var sourcePath string
		if err := rows.Scan(&sourcePath); err != nil {
			return nil, fmt.Errorf("db list stored release paths: %w", err)
		}
		paths = append(paths, sourcePath)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db list stored release paths: %w", err)
	}
	return paths, nil
}

func (r *SQLiteRepository) PurgeContentData(ctx context.Context, path string) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return internalerrors.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("db purge content: context canceled: %w", err)
	}
	if r.logger != nil {
		r.logger.Debugf("db: purge content data started path=%s", trimmedPath)
	}

	queries := []struct {
		sql  string
		args []any
	}{
		{sql: `DELETE FROM dvd_mediainfo WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM external_metadata WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM external_ids WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM release_overrides WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM description_overrides WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM playlist_selections WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM tracker_metadata WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM tracker_rule_failures WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM screenshot_final_selections WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM screenshots WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM screenshot_slot_variants WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM screenshot_slots WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM uploaded_images WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM upload_records WHERE source_path = ?`, args: []any{trimmedPath}},
		{sql: `DELETE FROM file_metadata WHERE path = ?`, args: []any{trimmedPath}},
	}

	totalRemoved := int64(0)
	if err := r.withWriteTx(ctx, "purge content", func(tx *sql.Tx) error {
		totalRemoved = 0
		for _, query := range queries {
			result, err := tx.ExecContext(ctx, query.sql, query.args...)
			if err != nil {
				return fmt.Errorf("db purge content: %w", err)
			}
			rows, err := result.RowsAffected()
			if err == nil {
				totalRemoved += rows
			}
		}
		rows, err := r.purgeLegacyUIState(ctx, tx, trimmedPath)
		if err != nil {
			return err
		}
		totalRemoved += rows
		return nil
	}); err != nil {
		return err
	}
	if r.logger != nil {
		r.logger.Debugf("db: purge content data completed path=%s rows_removed=%d", trimmedPath, totalRemoved)
	}
	return nil
}

// purgeLegacyUIState removes rows from the retired ui_states table when an old
// installation still has it. It accepts both the later source_path schema and
// the shipped id/data schema, matching stored paths with host filesystem
// equivalence so slash, clean-path, and OS case variants are purged together.
// Current schemas do not create the table, so a missing table is a no-op.
func (r *SQLiteRepository) purgeLegacyUIState(ctx context.Context, tx *sql.Tx, path string) (int64, error) {
	var tableName string
	err := tx.QueryRowContext(ctx, `
		SELECT name
		FROM sqlite_master
		WHERE type = 'table' AND name = 'ui_states'
	`).Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("db purge content: check legacy ui_states: %w", err)
	}

	hasSourcePath, err := tableColumnExists(ctx, tx, "ui_states", "source_path")
	if err != nil {
		return 0, fmt.Errorf("db purge content: inspect legacy ui_states source_path: %w", err)
	}
	if hasSourcePath {
		rows, err := tx.QueryContext(ctx, `SELECT rowid, source_path FROM ui_states`)
		if err != nil {
			return 0, fmt.Errorf("db purge content: read legacy ui_states source_path: %w", err)
		}
		defer rows.Close()

		rowIDs := make([]int64, 0)
		for rows.Next() {
			var rowID int64
			var sourcePath string
			if err := rows.Scan(&rowID, &sourcePath); err != nil {
				return 0, fmt.Errorf("db purge content: scan legacy ui_states source_path: %w", err)
			}
			if legacyUIStatePathMatches(sourcePath, path) {
				rowIDs = append(rowIDs, rowID)
			}
		}
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("db purge content: iterate legacy ui_states source_path: %w", err)
		}

		return deleteLegacyUIStateRowIDs(ctx, tx, rowIDs)
	}

	rows, err := r.purgeLegacyUIStateIDData(ctx, tx, path)
	if err != nil {
		return 0, err
	}
	return rows, nil
}

// purgeLegacyUIStateIDData removes legacy rows whose id or JSON data names the
// target source path under host filesystem path semantics. Malformed legacy JSON
// is ignored so purge can still remove the current path's other content rows.
func (r *SQLiteRepository) purgeLegacyUIStateIDData(ctx context.Context, tx *sql.Tx, path string) (int64, error) {
	hasID, err := tableColumnExists(ctx, tx, "ui_states", "id")
	if err != nil {
		return 0, fmt.Errorf("db purge content: inspect legacy ui_states id: %w", err)
	}
	if !hasID {
		return 0, nil
	}
	hasData, err := tableColumnExists(ctx, tx, "ui_states", "data")
	if err != nil {
		return 0, fmt.Errorf("db purge content: inspect legacy ui_states data: %w", err)
	}
	if !hasData {
		rows, err := tx.QueryContext(ctx, `SELECT rowid, id FROM ui_states`)
		if err != nil {
			return 0, fmt.Errorf("db purge content: read legacy ui_states id: %w", err)
		}
		defer rows.Close()

		rowIDs := make([]int64, 0)
		for rows.Next() {
			var rowID int64
			var id string
			if err := rows.Scan(&rowID, &id); err != nil {
				return 0, fmt.Errorf("db purge content: scan legacy ui_states id: %w", err)
			}
			if legacyUIStatePathMatches(id, path) {
				rowIDs = append(rowIDs, rowID)
			}
		}
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("db purge content: iterate legacy ui_states id: %w", err)
		}
		return deleteLegacyUIStateRowIDs(ctx, tx, rowIDs)
	}

	rows, err := tx.QueryContext(ctx, `SELECT id, data FROM ui_states`)
	if err != nil {
		return 0, fmt.Errorf("db purge content: read legacy ui_states: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		var data string
		if err := rows.Scan(&id, &data); err != nil {
			return 0, fmt.Errorf("db purge content: scan legacy ui_states: %w", err)
		}
		if legacyUIStatePathMatches(id, path) || legacyUIStateDataMatchesPath(data, path) {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("db purge content: iterate legacy ui_states: %w", err)
	}

	var removed int64
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `DELETE FROM ui_states WHERE id = ?`, id)
		if err != nil {
			return 0, fmt.Errorf("db purge content: delete legacy ui_states id: %w", err)
		}
		rows, err := result.RowsAffected()
		if err == nil {
			removed += rows
		}
	}
	return removed, nil
}

// deleteLegacyUIStateRowIDs removes rows selected by rowid after path-equivalent
// matching. Deleting by rowid avoids requiring raw stored paths to equal the
// caller's current spelling.
func deleteLegacyUIStateRowIDs(ctx context.Context, tx *sql.Tx, rowIDs []int64) (int64, error) {
	var removed int64
	for _, rowID := range rowIDs {
		result, err := tx.ExecContext(ctx, `DELETE FROM ui_states WHERE rowid = ?`, rowID)
		if err != nil {
			return 0, fmt.Errorf("db purge content: delete legacy ui_states rowid: %w", err)
		}
		rows, err := result.RowsAffected()
		if err == nil {
			removed += rows
		}
	}
	return removed, nil
}

// legacyUIStatePathMatches compares retired UI-state paths as host filesystem
// paths rather than serialized strings.
func legacyUIStatePathMatches(stored string, target string) bool {
	return pathutil.SamePath(stored, target)
}

// legacyUIStateDataMatchesPath reports whether a legacy ui_states payload names
// path using one of the source path field names seen in older UI state rows. The
// payload may be nested, path comparisons use host filesystem semantics, and
// malformed JSON never matches.
func legacyUIStateDataMatchesPath(data string, path string) bool {
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return false
	}
	return legacyUIStateValueMatchesPath(payload, path)
}

// legacyUIStateValueMatchesPath walks legacy object/array payloads looking for
// known source path keys without treating arbitrary string values as paths.
func legacyUIStateValueMatchesPath(value any, path string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"sourcePath", "SourcePath", "source_path", "path", "Path"} {
			if value, ok := typed[key].(string); ok && legacyUIStatePathMatches(value, path) {
				return true
			}
		}
		for _, nested := range typed {
			if legacyUIStateValueMatchesPath(nested, path) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if legacyUIStateValueMatchesPath(nested, path) {
				return true
			}
		}
	}
	return false
}

func resolvePath(path string) (string, error) {
	if path == "" {
		defaultPath, err := DefaultPath()
		if err != nil {
			return "", fmt.Errorf("db path: %w", err)
		}
		path = defaultPath
	}

	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return path, nil
	}

	cleaned := filepath.Clean(path)
	if err := ensureDir(cleaned); err != nil {
		return "", fmt.Errorf("db path: %w", err)
	}

	return cleaned, nil
}

func repositoryDBPath(input, resolved string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == ":memory:" || strings.HasPrefix(trimmed, "file:") {
		return ""
	}
	return resolved
}

// SaveConfigSection persists a single config section as JSON.
func (r *SQLiteRepository) SaveConfigSection(ctx context.Context, section string, data any) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if section == "" {
		return internalerrors.ErrInvalidInput
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("db save config: marshal section %s: %w", section, err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = r.execWrite(ctx, "save config section", `
		INSERT INTO config_settings (section, data, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(section) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at
	`, section, string(payload), now)
	if err != nil {
		return fmt.Errorf("db save config section %s: %w", section, err)
	}

	return nil
}

// SaveConfigSections persists multiple prepared config sections in one
// transaction. The map keys are config root section names and values are
// marshaled before the transaction starts; if any section write fails, no
// selected section is committed.
func (r *SQLiteRepository) SaveConfigSections(ctx context.Context, sections map[string]any) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if len(sections) == 0 {
		return nil
	}

	prepared := make(map[string]string, len(sections))
	for section, data := range sections {
		if section == "" {
			return internalerrors.ErrInvalidInput
		}
		payload, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("db save config: marshal section %s: %w", section, err)
		}
		prepared[section] = string(payload)
	}

	if err := r.saveFullConfigSections(ctx, prepared, nil); err != nil {
		return err
	}

	return nil
}

// LoadConfigSection retrieves a single config section from the database.
func (r *SQLiteRepository) LoadConfigSection(ctx context.Context, section string, dest any) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if section == "" {
		return internalerrors.ErrInvalidInput
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT data FROM config_settings WHERE section = ?
	`, section)

	var payload string
	err := row.Scan(&payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return internalerrors.ErrNotFound
		}
		return fmt.Errorf("db load config section %s: %w", section, err)
	}

	if err := json.Unmarshal([]byte(payload), dest); err != nil {
		return fmt.Errorf("db load config section %s: unmarshal: %w", section, err)
	}

	return nil
}

// prepareFullConfigSections converts a full config into per-section JSON
// payloads matching config_settings section names.
func prepareFullConfigSections(cfg any) (map[string]string, error) {
	// Marshal the full config to inspect its structure.
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("db save full config: marshal: %w", err)
	}

	var cfgMap map[string]any
	if err := json.Unmarshal(cfgData, &cfgMap); err != nil {
		return nil, fmt.Errorf("db save full config: unmarshal to map: %w", err)
	}

	sections := make(map[string]string, len(cfgMap))
	for section, value := range cfgMap {
		payload, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("db save full config: marshal section %s: %w", section, err)
		}
		sections[section] = string(payload)
	}

	return sections, nil
}

// SaveFullConfig persists the entire config to all sections.
// This is called when exporting to database from YAML or UI updates.
func (r *SQLiteRepository) SaveFullConfig(ctx context.Context, cfg any) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if cfg == nil {
		return internalerrors.ErrInvalidInput
	}

	sections, err := prepareFullConfigSections(cfg)
	if err != nil {
		return err
	}

	if err := r.saveFullConfigSections(ctx, sections, nil); err != nil {
		return err
	}

	return nil
}

// SaveFullConfigWithPreSave persists the entire config and runs preSave in the
// same write transaction immediately before config sections are written. If
// preSave or any section write fails, the transaction is rolled back.
func (r *SQLiteRepository) SaveFullConfigWithPreSave(ctx context.Context, cfg any, preSave func(context.Context, *sql.Tx) error) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if cfg == nil {
		return internalerrors.ErrInvalidInput
	}

	sections, err := prepareFullConfigSections(cfg)
	if err != nil {
		return err
	}

	if err := r.saveFullConfigSections(ctx, sections, preSave); err != nil {
		return err
	}

	return nil
}

// saveFullConfigSections writes prepared section payloads in one transaction,
// optionally running preSave in that same transaction before section writes.
func (r *SQLiteRepository) saveFullConfigSections(ctx context.Context, sections map[string]string, preSave func(context.Context, *sql.Tx) error) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if err := r.withWriteTx(ctx, "save full config", func(tx *sql.Tx) error {
		if preSave != nil {
			if err := preSave(ctx, tx); err != nil {
				return err
			}
		}
		sectionNames := make([]string, 0, len(sections))
		for section := range sections {
			sectionNames = append(sectionNames, section)
		}
		sort.Strings(sectionNames)
		for _, section := range sectionNames {
			payload := sections[section]
			if _, err := tx.ExecContext(ctx, `
			INSERT INTO config_settings (section, data, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(section) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at
		`, section, payload, now); err != nil {
				return fmt.Errorf("db save full config section %s: %w", section, err)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	return nil
}

// LoadFullConfig reconstructs the entire config from all sections. Each raw
// section is rejected if it contains duplicate JSON object names before the
// sections are joined, so malformed stored data cannot collapse duplicate keys
// during unmarshaling.
func (r *SQLiteRepository) LoadFullConfig(ctx context.Context, dest any) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if dest == nil {
		return internalerrors.ErrInvalidInput
	}

	rows, err := r.db.QueryContext(ctx, `SELECT section, data FROM config_settings`)
	if err != nil {
		return fmt.Errorf("db load full config: query: %w", err)
	}
	defer rows.Close()

	sections := make(map[string]json.RawMessage)
	for rows.Next() {
		var section string
		var payload string
		if err := rows.Scan(&section, &payload); err != nil {
			return fmt.Errorf("db load full config: scan: %w", err)
		}
		if err := validateJSONNoDuplicateObjectNames([]byte(payload)); err != nil {
			return fmt.Errorf("db load full config section %s: %w", section, err)
		}
		sections[section] = json.RawMessage(payload)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db load full config: rows: %w", err)
	}

	if len(sections) == 0 {
		return internalerrors.ErrNotFound
	}

	// Reconstruct the config from sections.
	cfgPayload, err := json.Marshal(sections)
	if err != nil {
		return fmt.Errorf("db load full config: marshal sections: %w", err)
	}

	if err := json.Unmarshal(cfgPayload, dest); err != nil {
		return fmt.Errorf("db load full config: unmarshal to dest: %w", err)
	}

	return nil
}

// validateJSONNoDuplicateObjectNames scans one JSON value and rejects duplicate
// object member names before the standard decoder can collapse them into a map.
func validateJSONNoDuplicateObjectNames(payload []byte) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := validateJSONValueNoDuplicateObjectNames(dec, ""); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return fmt.Errorf("read trailing JSON token: %w", err)
	}
	return nil
}

// validateJSONValueNoDuplicateObjectNames consumes one JSON value from dec and
// reports duplicate object member names with a dotted path to the object.
func validateJSONValueNoDuplicateObjectNames(dec *json.Decoder, path string) error {
	token, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read JSON token at %q: %w", path, err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return fmt.Errorf("read JSON object key at %q: %w", path, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("invalid JSON object key at %q", path)
			}
			if _, exists := seen[key]; exists {
				if path == "" {
					return fmt.Errorf("duplicate JSON object key %q", key)
				}
				return fmt.Errorf("duplicate JSON object key %q at %q", key, path)
			}
			seen[key] = struct{}{}
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if err := validateJSONValueNoDuplicateObjectNames(dec, childPath); err != nil {
				return err
			}
		}
		return consumeJSONDelim(dec, '}')
	case '[':
		index := 0
		for dec.More() {
			childPath := fmt.Sprintf("%s[%d]", path, index)
			if err := validateJSONValueNoDuplicateObjectNames(dec, childPath); err != nil {
				return err
			}
			index++
		}
		return consumeJSONDelim(dec, ']')
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %q", delim, path)
	}
}

// consumeJSONDelim consumes and verifies the expected closing JSON delimiter.
func consumeJSONDelim(dec *json.Decoder, want json.Delim) error {
	token, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read JSON delimiter %q: %w", want, err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != want {
		return fmt.Errorf("expected JSON delimiter %q", want)
	}
	return nil
}

// ConfigSectionLastUpdated returns the last update time for a config section.
func (r *SQLiteRepository) ConfigSectionLastUpdated(ctx context.Context, section string) (time.Time, error) {
	if r == nil || r.db == nil {
		return time.Time{}, errors.New("db: repository not initialized")
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT updated_at FROM config_settings WHERE section = ?
	`, section)

	var updatedAt string
	err := row.Scan(&updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, internalerrors.ErrNotFound
		}
		return time.Time{}, fmt.Errorf("db config section last updated: %w", err)
	}

	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("db config section last updated: parse timestamp: %w", err)
	}

	return parsed, nil
}
