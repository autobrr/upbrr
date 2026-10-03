// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

const metadataEvidenceMaxPayloadBytes = 16 << 20

var _ api.MetadataEvidenceRepository = (*SQLiteRepository)(nil)

func migrateAddMetadataEvidence(ctx context.Context, exec migrationExecutor) error {
	if _, err := exec.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS metadata_evidence (
		source_path TEXT NOT NULL,
		source_fingerprint TEXT NOT NULL,
		domain TEXT NOT NULL,
		query_key TEXT NOT NULL,
		outcome TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		PRIMARY KEY (source_path, source_fingerprint, domain, query_key)
	)`); err != nil {
		return fmt.Errorf("db: add metadata evidence: %w", err)
	}
	return nil
}

// LoadMetadataEvidence returns the exact source and dependency-bound lookup
// record. Missing records return ErrNotFound; invalid stored records fail closed.
func (r *SQLiteRepository) LoadMetadataEvidence(
	ctx context.Context,
	sourcePath, sourceFingerprint, domain, key string,
) (api.MetadataEvidence, error) {
	if r == nil || r.db == nil {
		return api.MetadataEvidence{}, errors.New("db: repository not initialized")
	}
	evidence := api.MetadataEvidence{
		SourcePath:        strings.TrimSpace(sourcePath),
		SourceFingerprint: strings.TrimSpace(sourceFingerprint),
		Domain:            strings.TrimSpace(domain),
		Key:               strings.TrimSpace(key),
	}
	if !metadataEvidenceHasCoordinates(evidence) {
		return api.MetadataEvidence{}, internalerrors.ErrInvalidInput
	}
	var payload string
	if err := r.db.QueryRowContext(ctx, `
		SELECT outcome, payload_json FROM metadata_evidence
		WHERE source_path = ? AND source_fingerprint = ? AND domain = ? AND query_key = ?
	`, evidence.SourcePath, evidence.SourceFingerprint, evidence.Domain, evidence.Key).Scan(&evidence.Outcome, &payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.MetadataEvidence{}, internalerrors.ErrNotFound
		}
		return api.MetadataEvidence{}, fmt.Errorf("db metadata evidence: load: %w", err)
	}
	if payload != "" {
		evidence.Payload = json.RawMessage(payload)
	}
	if !validMetadataEvidence(evidence) {
		return api.MetadataEvidence{}, errors.New("db metadata evidence: invalid stored record")
	}
	return evidence, nil
}

// SaveMetadataEvidence replaces one lookup outcome without changing prepared
// generations. It rejects invalid or oversized payloads and canceled contexts.
func (r *SQLiteRepository) SaveMetadataEvidence(ctx context.Context, evidence api.MetadataEvidence) error {
	if r == nil || r.db == nil {
		return errors.New("db: repository not initialized")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("db metadata evidence: save canceled: %w", err)
	}
	evidence.SourcePath = strings.TrimSpace(evidence.SourcePath)
	evidence.SourceFingerprint = strings.TrimSpace(evidence.SourceFingerprint)
	evidence.Domain = strings.TrimSpace(evidence.Domain)
	evidence.Key = strings.TrimSpace(evidence.Key)
	if !validMetadataEvidence(evidence) {
		return internalerrors.ErrInvalidInput
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO metadata_evidence (source_path, source_fingerprint, domain, query_key, outcome, payload_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path, source_fingerprint, domain, query_key) DO UPDATE SET
			outcome = excluded.outcome, payload_json = excluded.payload_json
	`, evidence.SourcePath, evidence.SourceFingerprint, evidence.Domain, evidence.Key, evidence.Outcome, string(evidence.Payload)); err != nil {
		return fmt.Errorf("db metadata evidence: save: %w", err)
	}
	return nil
}

func metadataEvidenceHasCoordinates(evidence api.MetadataEvidence) bool {
	return evidence.SourcePath != "" && evidence.SourceFingerprint != "" && evidence.Domain != "" && evidence.Key != ""
}

func validMetadataEvidence(evidence api.MetadataEvidence) bool {
	if !metadataEvidenceHasCoordinates(evidence) || len(evidence.Payload) > metadataEvidenceMaxPayloadBytes {
		return false
	}
	switch evidence.Outcome {
	case api.MetadataEvidenceSuccess, api.MetadataEvidenceEmpty:
		return json.Valid(evidence.Payload)
	case api.MetadataEvidenceFailed:
		return len(evidence.Payload) == 0 || json.Valid(evidence.Payload)
	default:
		return false
	}
}
