// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func metadataEvidenceFixture(t *testing.T) api.MetadataEvidence {
	t.Helper()
	return api.MetadataEvidence{
		SourcePath:        filepath.Join(t.TempDir(), "Example.Release.2026.mkv"),
		SourceFingerprint: "source-fingerprint",
		Domain:            "tmdb.metadata",
		Key:               "query-hash",
		Outcome:           api.MetadataEvidenceSuccess,
		Payload:           json.RawMessage(`{"title":"Example Release","genres":[]}`),
	}
}

func loadMetadataEvidenceFixture(t *testing.T, repo *SQLiteRepository, want api.MetadataEvidence) api.MetadataEvidence {
	t.Helper()
	got, err := repo.LoadMetadataEvidence(t.Context(), want.SourcePath, want.SourceFingerprint, want.Domain, want.Key)
	if err != nil {
		t.Fatalf("load metadata evidence: %v", err)
	}
	return got
}

func TestMetadataEvidenceRoundTripAndReplace(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	want := metadataEvidenceFixture(t)
	for _, outcome := range []api.MetadataEvidenceOutcome{api.MetadataEvidenceSuccess, api.MetadataEvidenceEmpty, api.MetadataEvidenceFailed} {
		want.Outcome = outcome
		if outcome == api.MetadataEvidenceFailed {
			want.Payload = nil
		}
		if err := repo.SaveMetadataEvidence(t.Context(), want); err != nil {
			t.Fatalf("save %s metadata evidence: %v", outcome, err)
		}
		got := loadMetadataEvidenceFixture(t, repo, want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("metadata evidence = %#v, want %#v", got, want)
		}
	}
	for _, payload := range []string{"null", "[]", "{}"} {
		want.Outcome = api.MetadataEvidenceEmpty
		want.Payload = json.RawMessage(payload)
		if err := repo.SaveMetadataEvidence(t.Context(), want); err != nil {
			t.Fatalf("save empty payload %s: %v", payload, err)
		}
		got := loadMetadataEvidenceFixture(t, repo, want)
		if string(got.Payload) != payload {
			t.Fatalf("empty payload = %s, want %s", got.Payload, payload)
		}
	}
}

func TestMetadataEvidenceScopesEveryDependency(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	base := metadataEvidenceFixture(t)
	if err := repo.SaveMetadataEvidence(t.Context(), base); err != nil {
		t.Fatalf("save metadata evidence: %v", err)
	}
	tests := []struct {
		name   string
		change func(*api.MetadataEvidence)
	}{
		{name: "source", change: func(value *api.MetadataEvidence) { value.SourcePath = filepath.Join(t.TempDir(), "Other.Release.mkv") }},
		{name: "fingerprint", change: func(value *api.MetadataEvidence) { value.SourceFingerprint = "changed-source" }},
		{name: "domain", change: func(value *api.MetadataEvidence) { value.Domain = "tmdb.episode" }},
		{name: "query", change: func(value *api.MetadataEvidence) { value.Key = "changed-query-hash" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.change(&value)
			if _, err := repo.LoadMetadataEvidence(t.Context(), value.SourcePath, value.SourceFingerprint, value.Domain, value.Key); !errors.Is(err, internalerrors.ErrNotFound) {
				t.Fatalf("changed dependency load = %v, want not found", err)
			}
			value.Outcome = api.MetadataEvidenceFailed
			value.Payload = nil
			if err := repo.SaveMetadataEvidence(t.Context(), value); err != nil {
				t.Fatalf("save changed dependency: %v", err)
			}
			if got := loadMetadataEvidenceFixture(t, repo, base); !reflect.DeepEqual(got, base) {
				t.Fatalf("changed dependency replaced independent evidence: %#v", got)
			}
		})
	}
}

func TestMetadataEvidenceFailedPartialRoundTrip(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	want := metadataEvidenceFixture(t)
	want.Outcome = api.MetadataEvidenceFailed
	want.Payload = json.RawMessage(`{"tracker_id":"42","imdb_id":1234567}`)
	if err := repo.SaveMetadataEvidence(t.Context(), want); err != nil {
		t.Fatalf("retain failed partial result: %v", err)
	}
	if got := loadMetadataEvidenceFixture(t, repo, want); !reflect.DeepEqual(got, want) {
		t.Fatalf("partial evidence changed: %#v", got)
	}
}

func TestMetadataEvidenceRejectsInvalidRecords(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	base := metadataEvidenceFixture(t)
	tests := []struct {
		name   string
		change func(*api.MetadataEvidence)
	}{
		{name: "source", change: func(value *api.MetadataEvidence) { value.SourcePath = " " }},
		{name: "fingerprint", change: func(value *api.MetadataEvidence) { value.SourceFingerprint = " " }},
		{name: "domain", change: func(value *api.MetadataEvidence) { value.Domain = " " }},
		{name: "key", change: func(value *api.MetadataEvidence) { value.Key = " " }},
		{name: "outcome", change: func(value *api.MetadataEvidence) { value.Outcome = "unknown" }},
		{name: "missing payload", change: func(value *api.MetadataEvidence) { value.Payload = nil }},
		{name: "invalid json", change: func(value *api.MetadataEvidence) { value.Payload = json.RawMessage("{") }},
		{name: "invalid failed payload", change: func(value *api.MetadataEvidence) {
			value.Outcome = api.MetadataEvidenceFailed
			value.Payload = json.RawMessage("{")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.change(&value)
			if err := repo.SaveMetadataEvidence(t.Context(), value); !errors.Is(err, internalerrors.ErrInvalidInput) {
				t.Fatalf("save invalid evidence = %v, want invalid input", err)
			}
		})
	}
	if _, err := repo.LoadMetadataEvidence(t.Context(), " ", base.SourceFingerprint, base.Domain, base.Key); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("load invalid source = %v, want invalid input", err)
	}
}

func TestMetadataEvidencePayloadBound(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	value := metadataEvidenceFixture(t)
	value.Outcome = api.MetadataEvidenceFailed
	value.Payload = json.RawMessage(`"` + strings.Repeat("x", metadataEvidenceMaxPayloadBytes-2) + `"`)
	if err := repo.SaveMetadataEvidence(t.Context(), value); err != nil {
		t.Fatalf("save maximum payload: %v", err)
	}
	value.Payload = append(value.Payload, ' ')
	if err := repo.SaveMetadataEvidence(t.Context(), value); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("save oversized payload = %v, want invalid input", err)
	}
	if got := loadMetadataEvidenceFixture(t, repo, value); len(got.Payload) != metadataEvidenceMaxPayloadBytes {
		t.Fatalf("failed write changed retained payload length: %d", len(got.Payload))
	}
}

func TestMetadataEvidenceCancellationPreservesPriorResult(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	value := metadataEvidenceFixture(t)
	if err := repo.SaveMetadataEvidence(t.Context(), value); err != nil {
		t.Fatalf("save metadata evidence: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	failed := value
	failed.Outcome = api.MetadataEvidenceFailed
	failed.Payload = nil
	if err := repo.SaveMetadataEvidence(ctx, failed); !errors.Is(err, context.Canceled) {
		t.Fatalf("save canceled evidence = %v, want canceled", err)
	}
	if got := loadMetadataEvidenceFixture(t, repo, value); !reflect.DeepEqual(got, value) {
		t.Fatalf("canceled save replaced evidence: %#v", got)
	}
}

func TestMetadataEvidenceHistoryPurgeAndMigration(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	value := metadataEvidenceFixture(t)
	other := value
	other.SourcePath = filepath.Join(t.TempDir(), "Other.Release.mkv")
	changed := value
	changed.SourceFingerprint = "changed-source"
	for _, record := range []api.MetadataEvidence{value, other, changed} {
		if err := repo.SaveMetadataEvidence(t.Context(), record); err != nil {
			t.Fatalf("save metadata evidence: %v", err)
		}
	}
	if err := migrateAddMetadataEvidence(t.Context(), repo.RawDB()); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	paths, err := repo.ListStoredReleasePaths(t.Context())
	if err != nil {
		t.Fatalf("list evidence-only sources: %v", err)
	}
	if !slices.Contains(paths, value.SourcePath) || !slices.Contains(paths, other.SourcePath) {
		t.Fatalf("evidence-only sources absent from History cleanup: %v", paths)
	}
	if got := loadMetadataEvidenceFixture(t, repo, value); !reflect.DeepEqual(got, value) {
		t.Fatalf("repeat migration changed evidence: %#v", got)
	}
	if err := repo.PurgeContentData(t.Context(), value.SourcePath); err != nil {
		t.Fatalf("purge source history: %v", err)
	}
	for _, record := range []api.MetadataEvidence{value, changed} {
		if _, err := repo.LoadMetadataEvidence(t.Context(), record.SourcePath, record.SourceFingerprint, record.Domain, record.Key); !errors.Is(err, internalerrors.ErrNotFound) {
			t.Fatalf("load purged evidence = %v, want not found", err)
		}
	}
	if got := loadMetadataEvidenceFixture(t, repo, other); !reflect.DeepEqual(got, other) {
		t.Fatalf("history purge changed another source: %#v", got)
	}
}

func TestMetadataEvidenceRejectsCorruptStoredPayload(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	value := metadataEvidenceFixture(t)
	if err := repo.SaveMetadataEvidence(t.Context(), value); err != nil {
		t.Fatalf("save metadata evidence: %v", err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE metadata_evidence SET payload_json = '{'`); err != nil {
		t.Fatalf("corrupt payload: %v", err)
	}
	if _, err := repo.LoadMetadataEvidence(t.Context(), value.SourcePath, value.SourceFingerprint, value.Domain, value.Key); err == nil {
		t.Fatal("corrupt stored payload was accepted")
	}
}
