// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"database/sql"
	"testing"
)

func TestIdentityCandidatesMigrationPreservesLegacyMetadata(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(t.Context(), `CREATE TABLE external_metadata (source_path TEXT, tmdb_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(t.Context(), `INSERT INTO external_metadata VALUES ('synthetic', '{}')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrateIdentityCandidates(t.Context(), raw); err != nil {
			t.Fatal(err)
		}
	}
	var candidates, metadata string
	if err := raw.QueryRowContext(t.Context(), `SELECT identity_candidates_json, tmdb_json FROM external_metadata`).Scan(&candidates, &metadata); err != nil {
		t.Fatal(err)
	}
	if candidates != "" || metadata != "{}" {
		t.Fatalf("migration changed legacy evidence: %q %q", candidates, metadata)
	}
}
