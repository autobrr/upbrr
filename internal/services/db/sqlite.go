// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type SQLiteRepository struct {
	db     *sql.DB
	logger Logger
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

	return &SQLiteRepository{db: db, logger: logger}, nil
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
	return retryBusyContext(ctx, r.logger, "migration", sqliteRetryAttempts, func() error {
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

func retryBusyContext(ctx context.Context, logger Logger, operation string, attempts int, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("db: retry busy context canceled: %w", err)
		}
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if !IsBusyError(err) || attempt == attempts {
			if IsBusyError(err) && logger != nil {
				logger.Warnf("db: %s busy lock persisted after %d attempts; returning retry exhaustion", operation, attempts)
			}
			return err
		}
		if logger != nil {
			logger.Infof("db: %s busy, retrying (%d/%d)", operation, attempt, attempts)
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
	_, err := r.db.ExecContext(ctx, `
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
		SELECT source_path, tmdb_id, imdb_id, tvdb_id, tvmaze_id, category,
			source_tmdb, source_imdb, source_tvdb, source_tvmaze, updated_at
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
		&ids.Category,
		&ids.SourceTMDB,
		&ids.SourceIMDB,
		&ids.SourceTVDB,
		&ids.SourceTVmaze,
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

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO external_ids (
			source_path, tmdb_id, imdb_id, tvdb_id, tvmaze_id, category,
			source_tmdb, source_imdb, source_tvdb, source_tvmaze, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			tmdb_id = excluded.tmdb_id,
			imdb_id = excluded.imdb_id,
			tvdb_id = excluded.tvdb_id,
			tvmaze_id = excluded.tvmaze_id,
			category = excluded.category,
			source_tmdb = excluded.source_tmdb,
			source_imdb = excluded.source_imdb,
			source_tvdb = excluded.source_tvdb,
			source_tvmaze = excluded.source_tvmaze,
			updated_at = excluded.updated_at
	`,
		ids.SourcePath,
		ids.TMDBID,
		ids.IMDBID,
		ids.TVDBID,
		ids.TVmazeID,
		ids.Category,
		ids.SourceTMDB,
		ids.SourceIMDB,
		ids.SourceTVDB,
		ids.SourceTVmaze,
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

	_, err := r.db.ExecContext(ctx, `
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
		SELECT source_path, tmdb_json, imdb_json, tvdb_json, tvmaze_json, bluray_json, updated_at
		FROM external_metadata
		WHERE source_path = ?
	`, path)

	var metadata ExternalMetadata
	var tmdbJSON string
	var imdbJSON string
	var tvdbJSON string
	var tvmazeJSON string
	var blurayJSON string
	var updatedAt string
	if err := row.Scan(
		&metadata.SourcePath,
		&tmdbJSON,
		&imdbJSON,
		&tvdbJSON,
		&tvmazeJSON,
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
	blurayJSON := encodeOptionalJSON(metadata.Bluray)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO external_metadata (
			source_path, tmdb_json, imdb_json, tvdb_json, tvmaze_json, bluray_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			tmdb_json = excluded.tmdb_json,
			imdb_json = excluded.imdb_json,
			tvdb_json = excluded.tvdb_json,
			tvmaze_json = excluded.tvmaze_json,
			bluray_json = excluded.bluray_json,
			updated_at = excluded.updated_at
	`,
		metadata.SourcePath,
		tmdbJSON,
		imdbJSON,
		tvdbJSON,
		tvmazeJSON,
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

	_, err := r.db.ExecContext(ctx, `
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
	if _, err := r.db.ExecContext(ctx, `DELETE FROM release_overrides WHERE source_path = ?`, path); err != nil {
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

	_, err := r.db.ExecContext(ctx, `
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

	if _, err := r.db.ExecContext(ctx, `DELETE FROM playlist_selections WHERE source_path = ?`, sourcePath); err != nil {
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

	_, err := r.db.ExecContext(ctx, `
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
	if _, err := r.db.ExecContext(ctx, `DELETE FROM description_overrides WHERE source_path = ? AND group_key = ?`, trimmed, trimmedGroup); err != nil {
		return fmt.Errorf("db delete description override: %w", err)
	}
	return nil
}

func normalizeDescriptionOverrideGroupKey(groupKey string) string {
	return strings.ToLower(strings.TrimSpace(groupKey))
}

func nullString(value *string) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func nullInt(value *int) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func nullBool(value *bool) interface{} {
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

	_, err := r.db.ExecContext(ctx, `
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

	result, err := r.db.ExecContext(ctx, `
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

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db save tracker rule failures: begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db save tracker rule failures: commit: %w", err)
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
	_, err := r.db.ExecContext(ctx, `
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
	_, err := r.db.ExecContext(ctx, `
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
	_, err := r.db.ExecContext(ctx, `
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
	if _, err := r.db.ExecContext(ctx, `DELETE FROM uploaded_images WHERE image_path = ?`, imagePath); err != nil {
		return fmt.Errorf("db delete screenshot: delete uploaded images: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM screenshots WHERE image_path = ?`, imagePath); err != nil {
		return fmt.Errorf("db delete screenshot: %w", err)
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

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db save final selections: begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db save final selections: commit: %w", err)
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
	_, err := r.db.ExecContext(ctx, `DELETE FROM screenshot_final_selections WHERE image_path = ?`, imagePath)
	if err != nil {
		return fmt.Errorf("db delete final selection: %w", err)
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

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db replace screenshot slots: begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db replace screenshot slots: commit: %w", err)
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

	stmt, err := r.db.PrepareContext(ctx, `
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

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db save uploaded images: begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db save uploaded images: commit: %w", err)
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
	result, err := r.db.ExecContext(ctx, `
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

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db purge content: begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db purge content: commit: %w", err)
	}
	if r.logger != nil {
		r.logger.Debugf("db: purge content data completed path=%s rows_removed=%d", trimmedPath, totalRemoved)
	}
	return nil
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

// SaveConfigSection persists a single config section as JSON.
func (r *SQLiteRepository) SaveConfigSection(ctx context.Context, section string, data interface{}) error {
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
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO config_settings (section, data, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(section) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at
	`, section, string(payload), now)
	if err != nil {
		return fmt.Errorf("db save config section %s: %w", section, err)
	}

	return nil
}

// LoadConfigSection retrieves a single config section from the database.
func (r *SQLiteRepository) LoadConfigSection(ctx context.Context, section string, dest interface{}) error {
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

// SaveFullConfig persists the entire config to all sections.
// This is called when exporting to database from YAML or UI updates.
func (r *SQLiteRepository) SaveFullConfig(ctx context.Context, cfg interface{}) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if cfg == nil {
		return internalerrors.ErrInvalidInput
	}

	// Marshal the full config to inspect its structure.
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("db save full config: marshal: %w", err)
	}

	var cfgMap map[string]interface{}
	if err := json.Unmarshal(cfgData, &cfgMap); err != nil {
		return fmt.Errorf("db save full config: unmarshal to map: %w", err)
	}

	// Insert each top-level key as a section.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for section, value := range cfgMap {
		payload, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("db save full config: marshal section %s: %w", section, err)
		}

		_, err = r.db.ExecContext(ctx, `
			INSERT INTO config_settings (section, data, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(section) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at
		`, section, string(payload), now)
		if err != nil {
			return fmt.Errorf("db save full config section %s: %w", section, err)
		}
	}

	return nil
}

// LoadFullConfig reconstructs the entire config from all sections.
func (r *SQLiteRepository) LoadFullConfig(ctx context.Context, dest interface{}) error {
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
