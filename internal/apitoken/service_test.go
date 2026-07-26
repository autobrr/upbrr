// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package apitoken

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"
)

type memoryRepository struct {
	records map[string]StoredRecord
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{records: make(map[string]StoredRecord)}
}

func (r *memoryRepository) CreateAPIToken(_ context.Context, record StoredRecord) error {
	r.records[record.ID] = record
	return nil
}

func (r *memoryRepository) ListAPITokens(context.Context) ([]Record, error) {
	result := make([]Record, 0, len(r.records))
	for _, stored := range r.records {
		result = append(result, stored.Record)
	}
	return result, nil
}

func (r *memoryRepository) FindActiveAPITokenByHash(_ context.Context, hash [sha256.Size]byte) (Record, error) {
	for _, stored := range r.records {
		if stored.TokenHash == hash && stored.RevokedAt == nil {
			return stored.Record, nil
		}
	}
	return Record{}, ErrNotFound
}

func (r *memoryRepository) RevokeAPIToken(_ context.Context, id string, revokedAt time.Time) (bool, error) {
	record, ok := r.records[id]
	if !ok || record.RevokedAt != nil {
		return false, nil
	}
	record.RevokedAt = &revokedAt
	r.records[id] = record
	return true, nil
}

func TestServiceCreateAuthenticateListAndRevoke(t *testing.T) {
	t.Parallel()

	repository := newMemoryRepository()
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	created, err := service.Create(context.Background(), CreateInput{
		Name:    "Automation",
		OwnerID: "release-bot",
		Scopes:  []Scope{ScopeWorkflowRead, ScopeWorkflowRead},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Token == "" || created.Record.ID == "" {
		t.Fatal("created token or token ID is empty")
	}
	if !strings.HasPrefix(created.Record.ID, tokenIDPrefix) {
		t.Fatal("created token ID does not have the CLI-safe prefix")
	}
	stored := repository.records[created.Record.ID]
	if got := sha256.Sum256([]byte(created.Token)); stored.TokenHash != got {
		t.Fatal("stored token hash does not match generated plaintext")
	}
	if len(stored.Scopes) != 1 || stored.Scopes[0] != ScopeWorkflowRead {
		t.Fatalf("stored scopes = %v", stored.Scopes)
	}

	principal, ok, err := service.Authenticate(context.Background(), created.Token)
	if err != nil || !ok {
		t.Fatalf("authenticate ok=%t err=%v", ok, err)
	}
	if principal.OwnerID != "api:release-bot" || len(principal.Scopes) != 1 || principal.Scopes[0] != ScopeWorkflowRead {
		t.Fatalf("principal = %#v", principal)
	}
	if _, ok, err := service.Authenticate(context.Background(), created.Token+"different"); err != nil || ok {
		t.Fatalf("different token ok=%t err=%v", ok, err)
	}

	records, err := service.List(context.Background())
	if err != nil || len(records) != 1 {
		t.Fatalf("list records=%#v err=%v", records, err)
	}
	if err := service.Revoke(context.Background(), created.Record.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, ok, err := service.Authenticate(context.Background(), created.Token); err != nil || ok {
		t.Fatalf("revoked token ok=%t err=%v", ok, err)
	}
	if err := service.Revoke(context.Background(), created.Record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke error = %v", err)
	}
}

func TestServiceRejectsInvalidMetadata(t *testing.T) {
	t.Parallel()

	service, err := NewService(newMemoryRepository())
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	for _, input := range []CreateInput{
		{},
		{Name: "line\nbreak"},
		{Name: "Automation", OwnerID: "owner\tvalue"},
		{Name: "Automation", Scopes: []Scope{"unknown"}},
	} {
		if _, err := service.Create(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("create input %#v error = %v", input, err)
		}
	}
}
