// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// CommitPreparedRelease atomically replaces the current grouped release facts,
// canonical identity, and source-scoped provider metadata for one source.
func (r *SQLiteRepository) CommitPreparedRelease(ctx context.Context, release api.PreparedRelease) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	generation, err := validatePreparedReleaseForCommit(release)
	if err != nil {
		return err
	}

	preparedAt := release.PreparedAt.UTC()
	if preparedAt.IsZero() {
		preparedAt = time.Now().UTC()
		release.PreparedAt = preparedAt
	}
	if release.Identity.ResolvedAt.IsZero() {
		release.Identity.ResolvedAt = preparedAt
	}
	if release.ProviderMetadata.UpdatedAt.IsZero() {
		release.ProviderMetadata.UpdatedAt = preparedAt
	}

	sourceJSON, err := encodePreparedJSON(release.Source)
	if err != nil {
		return fmt.Errorf("db commit prepared release: encode source: %w", err)
	}
	namingJSON, err := encodePreparedJSON(release.Naming)
	if err != nil {
		return fmt.Errorf("db commit prepared release: encode naming: %w", err)
	}
	episodeJSON, err := encodePreparedJSON(release.Episode)
	if err != nil {
		return fmt.Errorf("db commit prepared release: encode episode: %w", err)
	}
	mediaJSON, err := encodePreparedJSON(release.Media)
	if err != nil {
		return fmt.Errorf("db commit prepared release: encode media: %w", err)
	}
	discJSON, err := encodePreparedJSON(release.Disc)
	if err != nil {
		return fmt.Errorf("db commit prepared release: encode disc: %w", err)
	}
	assessmentsJSON, err := encodePreparedJSON(release.Assessments)
	if err != nil {
		return fmt.Errorf("db commit prepared release: encode assessments: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db commit prepared release: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	compatibility := release.Compatibility
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO prepared_release_current (
			source_path, generation, source_fingerprint, fact_instruction_fingerprint,
			policy_fingerprint, contract_version, source_json, naming_json,
			episode_json, media_json, disc_json, assessments_json, prepared_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			generation = excluded.generation,
			source_fingerprint = excluded.source_fingerprint,
			fact_instruction_fingerprint = excluded.fact_instruction_fingerprint,
			policy_fingerprint = excluded.policy_fingerprint,
			contract_version = excluded.contract_version,
			source_json = excluded.source_json,
			naming_json = excluded.naming_json,
			episode_json = excluded.episode_json,
			media_json = excluded.media_json,
			disc_json = excluded.disc_json,
			assessments_json = excluded.assessments_json,
			prepared_at = excluded.prepared_at
	`,
		release.Source.SourcePath,
		generation,
		compatibility.SourceFingerprint,
		compatibility.FactInstructionFingerprint,
		compatibility.PolicyFingerprint,
		compatibility.ContractVersion,
		sourceJSON,
		namingJSON,
		episodeJSON,
		mediaJSON,
		discJSON,
		assessmentsJSON,
		preparedAt.Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("db commit prepared release: facts: %w", err)
	}
	if err := commitPreparedIdentityTx(ctx, tx, release.Identity, generation); err != nil {
		return err
	}
	if err := commitSourceScopedMetadataTx(ctx, tx, release.ProviderMetadata, generation); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db commit prepared release: commit: %w", err)
	}
	return nil
}

// LoadPreparedRelease returns the current complete generation for sourcePath.
// Missing or generation-mismatched identity/metadata is treated as corrupt
// prepared state rather than silently falling back to legacy rows.
func (r *SQLiteRepository) LoadPreparedRelease(ctx context.Context, sourcePath string) (api.PreparedRelease, error) {
	if r == nil || r.db == nil {
		return api.PreparedRelease{}, errors.New("db: repository not initialized")
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return api.PreparedRelease{}, internalerrors.ErrInvalidInput
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return api.PreparedRelease{}, fmt.Errorf("db load prepared release: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		SELECT generation, source_fingerprint, fact_instruction_fingerprint,
			policy_fingerprint, contract_version, source_json, naming_json,
			episode_json, media_json, disc_json, assessments_json, prepared_at
		FROM prepared_release_current
		WHERE source_path = ?
	`, sourcePath)
	var release api.PreparedRelease
	var generation int64
	var sourceJSON string
	var namingJSON string
	var episodeJSON string
	var mediaJSON string
	var discJSON string
	var assessmentsJSON string
	var preparedAt string
	if err := row.Scan(
		&generation,
		&release.Compatibility.SourceFingerprint,
		&release.Compatibility.FactInstructionFingerprint,
		&release.Compatibility.PolicyFingerprint,
		&release.Compatibility.ContractVersion,
		&sourceJSON,
		&namingJSON,
		&episodeJSON,
		&mediaJSON,
		&discJSON,
		&assessmentsJSON,
		&preparedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.PreparedRelease{}, internalerrors.ErrNotFound
		}
		return api.PreparedRelease{}, fmt.Errorf("db load prepared release: facts: %w", err)
	}
	if generation <= 0 {
		return api.PreparedRelease{}, errors.New("db load prepared release: invalid generation")
	}
	release.Generation = api.PreparedGeneration(generation)
	if err := decodePreparedJSON(sourceJSON, &release.Source); err != nil {
		return api.PreparedRelease{}, fmt.Errorf("db load prepared release: decode source: %w", err)
	}
	if err := decodePreparedJSON(namingJSON, &release.Naming); err != nil {
		return api.PreparedRelease{}, fmt.Errorf("db load prepared release: decode naming: %w", err)
	}
	if err := decodePreparedJSON(episodeJSON, &release.Episode); err != nil {
		return api.PreparedRelease{}, fmt.Errorf("db load prepared release: decode episode: %w", err)
	}
	if err := decodePreparedJSON(mediaJSON, &release.Media); err != nil {
		return api.PreparedRelease{}, fmt.Errorf("db load prepared release: decode media: %w", err)
	}
	if err := decodePreparedJSON(discJSON, &release.Disc); err != nil {
		return api.PreparedRelease{}, fmt.Errorf("db load prepared release: decode disc: %w", err)
	}
	if err := decodePreparedJSON(assessmentsJSON, &release.Assessments); err != nil {
		return api.PreparedRelease{}, fmt.Errorf("db load prepared release: decode assessments: %w", err)
	}
	parsedPreparedAt, err := time.Parse(time.RFC3339Nano, preparedAt)
	if err != nil {
		return api.PreparedRelease{}, fmt.Errorf("db load prepared release: parse prepared time: %w", err)
	}
	release.PreparedAt = parsedPreparedAt

	release.Identity, err = loadPreparedIdentityTx(ctx, tx, sourcePath, release.Generation)
	if err != nil {
		return api.PreparedRelease{}, err
	}
	release.ProviderMetadata, err = loadSourceScopedMetadataTx(ctx, tx, sourcePath, release.Generation)
	if err != nil {
		return api.PreparedRelease{}, err
	}
	if err := tx.Commit(); err != nil {
		return api.PreparedRelease{}, fmt.Errorf("db load prepared release: commit read: %w", err)
	}
	return release, nil
}

// PurgePreparedRelease deletes current prepared facts, canonical identity, and
// source-scoped provider metadata for one source in one transaction.
func (r *SQLiteRepository) PurgePreparedRelease(ctx context.Context, sourcePath string) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return internalerrors.ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db purge prepared release: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range []string{"prepared_release_current", "external_ids", "external_metadata"} {
		query := `DELETE FROM ` + table + ` WHERE source_path = ?` //nolint:gosec // Fixed internal table allowlist, no caller SQL.
		if _, err := tx.ExecContext(ctx, query, sourcePath); err != nil {
			return fmt.Errorf("db purge prepared release: %s: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db purge prepared release: commit: %w", err)
	}
	return nil
}

func validatePreparedReleaseForCommit(release api.PreparedRelease) (int64, error) {
	sourcePath := strings.TrimSpace(release.Source.SourcePath)
	if sourcePath == "" || release.Generation == 0 || uint64(release.Generation) > math.MaxInt64 {
		return 0, internalerrors.ErrInvalidInput
	}
	generation := int64(release.Generation) //nolint:gosec // Bounds checked immediately above.
	compatibility := release.Compatibility
	if strings.TrimSpace(compatibility.SourceFingerprint) == "" ||
		strings.TrimSpace(compatibility.FactInstructionFingerprint) == "" ||
		strings.TrimSpace(compatibility.PolicyFingerprint) == "" ||
		strings.TrimSpace(compatibility.ContractVersion) == "" {
		return 0, internalerrors.ErrInvalidInput
	}
	if release.Identity.SourcePath != sourcePath || release.Identity.Generation != release.Generation {
		return 0, internalerrors.ErrInvalidInput
	}
	if release.ProviderMetadata.SourcePath != sourcePath || release.ProviderMetadata.Generation != release.Generation {
		return 0, internalerrors.ErrInvalidInput
	}
	if release.Identity.Category == "" || release.Identity.Conflict == "" {
		return 0, internalerrors.ErrInvalidInput
	}
	if release.Assessments.MediaInfoUniqueID == "" || release.Assessments.MediaInfoEncodeSettings == "" ||
		release.Assessments.Naming.Status == "" {
		return 0, internalerrors.ErrInvalidInput
	}
	return generation, nil
}

func commitPreparedIdentityTx(ctx context.Context, tx *sql.Tx, identity api.ExternalIdentity, generation int64) error {
	overrideJSON, err := encodePreparedJSON(identity.Overrides)
	if err != nil {
		return fmt.Errorf("db commit prepared release: encode identity overrides: %w", err)
	}
	resolvedAt := identity.ResolvedAt.UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO external_ids (
			source_path, generation, tmdb_id, imdb_id, tvdb_id, tvmaze_id, mal_id,
			category, source_tmdb, source_imdb, source_tvdb, source_tvmaze,
			source_mal, category_provenance, override_json, conflict_status,
			source_fingerprint, intent_fingerprint, contract_version, resolved_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			generation = excluded.generation,
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
			category_provenance = excluded.category_provenance,
			override_json = excluded.override_json,
			conflict_status = excluded.conflict_status,
			source_fingerprint = excluded.source_fingerprint,
			intent_fingerprint = excluded.intent_fingerprint,
			contract_version = excluded.contract_version,
			resolved_at = excluded.resolved_at,
			updated_at = excluded.updated_at
	`,
		identity.SourcePath,
		generation,
		identity.TMDBID,
		identity.IMDBID,
		identity.TVDBID,
		identity.TVmazeID,
		identity.MALID,
		string(identity.Category),
		string(identity.Provenance.TMDB),
		string(identity.Provenance.IMDB),
		string(identity.Provenance.TVDB),
		string(identity.Provenance.TVmaze),
		string(identity.Provenance.MAL),
		string(identity.Provenance.Category),
		overrideJSON,
		string(identity.Conflict),
		identity.Resolution.SourceFingerprint,
		identity.Resolution.IntentFingerprint,
		identity.Resolution.ContractVersion,
		resolvedAt,
		resolvedAt,
	)
	if err != nil {
		return fmt.Errorf("db commit prepared release: identity: %w", err)
	}
	return nil
}

func commitSourceScopedMetadataTx(ctx context.Context, tx *sql.Tx, metadata api.SourceScopedMetadata, generation int64) error {
	updatedAt := metadata.UpdatedAt.UTC().Format(time.RFC3339Nano)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO external_metadata (
			source_path, generation, tmdb_json, imdb_json, tvdb_json,
			tvmaze_json, anilist_json, bluray_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			generation = excluded.generation,
			tmdb_json = excluded.tmdb_json,
			imdb_json = excluded.imdb_json,
			tvdb_json = excluded.tvdb_json,
			tvmaze_json = excluded.tvmaze_json,
			anilist_json = excluded.anilist_json,
			bluray_json = excluded.bluray_json,
			updated_at = excluded.updated_at
	`,
		metadata.SourcePath,
		generation,
		encodeOptionalJSON(metadata.TMDB),
		encodeOptionalJSON(metadata.IMDB),
		encodeOptionalJSON(metadata.TVDB),
		encodeOptionalJSON(metadata.TVmaze),
		encodeOptionalJSON(metadata.AniList),
		encodeOptionalJSON(metadata.Bluray),
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("db commit prepared release: provider metadata: %w", err)
	}
	return nil
}

func loadPreparedIdentityTx(
	ctx context.Context,
	tx *sql.Tx,
	sourcePath string,
	wantGeneration api.PreparedGeneration,
) (api.ExternalIdentity, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT generation, tmdb_id, imdb_id, tvdb_id, tvmaze_id, mal_id, category,
			source_tmdb, source_imdb, source_tvdb, source_tvmaze, source_mal,
			category_provenance, override_json, conflict_status, source_fingerprint,
			intent_fingerprint, contract_version, resolved_at
		FROM external_ids
		WHERE source_path = ?
	`, sourcePath)
	identity := api.ExternalIdentity{SourcePath: sourcePath}
	var generation int64
	var category string
	var sourceTMDB string
	var sourceIMDB string
	var sourceTVDB string
	var sourceTVmaze string
	var sourceMAL string
	var categoryProvenance string
	var overrideJSON string
	var conflictStatus string
	var resolvedAt string
	if err := row.Scan(
		&generation,
		&identity.TMDBID,
		&identity.IMDBID,
		&identity.TVDBID,
		&identity.TVmazeID,
		&identity.MALID,
		&category,
		&sourceTMDB,
		&sourceIMDB,
		&sourceTVDB,
		&sourceTVmaze,
		&sourceMAL,
		&categoryProvenance,
		&overrideJSON,
		&conflictStatus,
		&identity.Resolution.SourceFingerprint,
		&identity.Resolution.IntentFingerprint,
		&identity.Resolution.ContractVersion,
		&resolvedAt,
	); err != nil {
		return api.ExternalIdentity{}, fmt.Errorf("db load prepared release: identity: %w", err)
	}
	if generation <= 0 || api.PreparedGeneration(generation) != wantGeneration {
		return api.ExternalIdentity{}, errors.New("db load prepared release: identity generation mismatch")
	}
	identity.Generation = api.PreparedGeneration(generation)
	identity.Category = api.CanonicalCategory(category)
	identity.Provenance = api.IdentityProvenanceSet{
		TMDB:     api.IdentityProvenance(sourceTMDB),
		IMDB:     api.IdentityProvenance(sourceIMDB),
		TVDB:     api.IdentityProvenance(sourceTVDB),
		TVmaze:   api.IdentityProvenance(sourceTVmaze),
		MAL:      api.IdentityProvenance(sourceMAL),
		Category: api.IdentityProvenance(categoryProvenance),
	}
	identity.Conflict = api.IdentityConflictStatus(conflictStatus)
	if err := decodePreparedJSON(overrideJSON, &identity.Overrides); err != nil {
		return api.ExternalIdentity{}, fmt.Errorf("db load prepared release: decode identity overrides: %w", err)
	}
	parsedResolvedAt, err := time.Parse(time.RFC3339Nano, resolvedAt)
	if err != nil {
		return api.ExternalIdentity{}, fmt.Errorf("db load prepared release: parse identity time: %w", err)
	}
	identity.ResolvedAt = parsedResolvedAt
	return identity, nil
}

func loadSourceScopedMetadataTx(
	ctx context.Context,
	tx *sql.Tx,
	sourcePath string,
	wantGeneration api.PreparedGeneration,
) (api.SourceScopedMetadata, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT generation, tmdb_json, imdb_json, tvdb_json, tvmaze_json,
			anilist_json, bluray_json, updated_at
		FROM external_metadata
		WHERE source_path = ?
	`, sourcePath)
	metadata := api.SourceScopedMetadata{SourcePath: sourcePath}
	var generation int64
	var tmdbJSON string
	var imdbJSON string
	var tvdbJSON string
	var tvmazeJSON string
	var anilistJSON string
	var blurayJSON string
	var updatedAt string
	if err := row.Scan(
		&generation,
		&tmdbJSON,
		&imdbJSON,
		&tvdbJSON,
		&tvmazeJSON,
		&anilistJSON,
		&blurayJSON,
		&updatedAt,
	); err != nil {
		return api.SourceScopedMetadata{}, fmt.Errorf("db load prepared release: provider metadata: %w", err)
	}
	if generation <= 0 || api.PreparedGeneration(generation) != wantGeneration {
		return api.SourceScopedMetadata{}, errors.New("db load prepared release: provider metadata generation mismatch")
	}
	metadata.Generation = api.PreparedGeneration(generation)
	var err error
	if metadata.TMDB, err = decodeOptionalJSON[api.TMDBMetadata](tmdbJSON); err != nil {
		return api.SourceScopedMetadata{}, fmt.Errorf("db load prepared release: decode TMDB metadata: %w", err)
	}
	if metadata.IMDB, err = decodeOptionalJSON[api.IMDBMetadata](imdbJSON); err != nil {
		return api.SourceScopedMetadata{}, fmt.Errorf("db load prepared release: decode IMDb metadata: %w", err)
	}
	if metadata.TVDB, err = decodeOptionalJSON[api.TVDBMetadata](tvdbJSON); err != nil {
		return api.SourceScopedMetadata{}, fmt.Errorf("db load prepared release: decode TVDB metadata: %w", err)
	}
	if metadata.TVmaze, err = decodeOptionalJSON[api.TVmazeMetadata](tvmazeJSON); err != nil {
		return api.SourceScopedMetadata{}, fmt.Errorf("db load prepared release: decode TVmaze metadata: %w", err)
	}
	if metadata.AniList, err = decodeOptionalJSON[api.AniListMetadata](anilistJSON); err != nil {
		return api.SourceScopedMetadata{}, fmt.Errorf("db load prepared release: decode AniList metadata: %w", err)
	}
	if metadata.Bluray, err = decodeOptionalJSON[api.BlurayMetadata](blurayJSON); err != nil {
		return api.SourceScopedMetadata{}, fmt.Errorf("db load prepared release: decode Blu-ray metadata: %w", err)
	}
	parsedUpdatedAt, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return api.SourceScopedMetadata{}, fmt.Errorf("db load prepared release: parse provider metadata time: %w", err)
	}
	metadata.UpdatedAt = parsedUpdatedAt
	return metadata, nil
}

func encodePreparedJSON(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal JSON: %w", err)
	}
	return string(payload), nil
}

func decodePreparedJSON(payload string, target any) error {
	if err := json.Unmarshal([]byte(payload), target); err != nil {
		return fmt.Errorf("unmarshal JSON: %w", err)
	}
	return nil
}
