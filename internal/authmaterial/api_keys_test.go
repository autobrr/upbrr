// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package authmaterial

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/apitoken"
)

func TestAPIKeysPersistAsHashesSurviveAuthUpgradeAndRevoke(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.saveLocked(Record{
		Username:          "tester",
		PasswordHash:      "initial-test-password-hash",
		EncryptionKeySeed: "initial-test-encryption-seed",
	}); err != nil {
		t.Fatalf("write auth fixture: %v", err)
	}
	service, err := apitoken.NewService(store)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	created, err := service.Create(ctx, apitoken.CreateInput{
		Name:    "Persistent automation",
		OwnerID: "automation",
		Scopes:  []apitoken.Scope{apitoken.ScopeWorkflowRead},
	})
	if err != nil {
		t.Fatalf("create API key: %v", err)
	}

	raw, err := os.ReadFile(AuthFilePath(dbPath))
	if err != nil {
		t.Fatalf("read auth file: %v", err)
	}
	if bytes.Contains(raw, []byte(created.Token)) {
		t.Fatal("auth file exposed plaintext API key")
	}
	var persisted Record
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decode auth file: %v", err)
	}
	if len(persisted.APIKeys) != 1 || persisted.APIKeys[0].APIKeyHash == "" {
		t.Fatal("auth file did not contain one API key hash")
	}
	storedHash, err := base64.RawStdEncoding.DecodeString(persisted.APIKeys[0].APIKeyHash)
	if err != nil {
		t.Fatalf("decode stored API key hash: %v", err)
	}
	wantHash := sha256.Sum256([]byte(created.Token))
	if !bytes.Equal(storedHash, wantHash[:]) {
		t.Fatal("stored value is not the generated API key hash")
	}

	current, err := store.Load()
	if err != nil {
		t.Fatalf("load current auth: %v", err)
	}
	target := current
	target.PasswordHash = "upgraded-test-password-hash"
	target.EncryptionKeySeed = "upgraded-test-encryption-seed"
	if err := store.BeginPendingUpgrade(current, target); err != nil {
		t.Fatalf("begin auth upgrade: %v", err)
	}
	pending, err := store.Load()
	if err != nil {
		t.Fatalf("load pending auth: %v", err)
	}
	if pending.PendingUpgrade == nil || len(pending.PendingUpgrade.Target.APIKeys) != 0 {
		t.Fatal("pending auth upgrade duplicated active API key records")
	}
	if _, err := store.FinalizePendingUpgrade(current.Username); err != nil {
		t.Fatalf("finalize auth upgrade: %v", err)
	}

	reopened, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	service, err = apitoken.NewService(reopened)
	if err != nil {
		t.Fatalf("new reopened service: %v", err)
	}
	principal, ok, err := service.Authenticate(ctx, created.Token)
	if err != nil || !ok || principal.OwnerID != "api:automation" {
		t.Fatalf("authenticate after reopen/upgrade ok=%t owner=%q err=%v", ok, principal.OwnerID, err)
	}
	if err := service.Revoke(ctx, created.Record.ID); err != nil {
		t.Fatalf("revoke API key: %v", err)
	}
	if _, ok, err := service.Authenticate(ctx, created.Token); err != nil || ok {
		t.Fatalf("authenticate revoked API key ok=%t err=%v", ok, err)
	}
	records, err := service.List(ctx)
	if err != nil || len(records) != 1 || records[0].RevokedAt == nil {
		t.Fatalf("list revoked API keys count=%d err=%v", len(records), err)
	}
}
