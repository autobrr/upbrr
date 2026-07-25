// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrivateArtifactVaultRestartIsolationIntegrityAndConsume(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	vault, err := NewPrivateArtifactVault(root)
	if err != nil {
		t.Fatalf("new private artifact vault: %v", err)
	}
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	workflowID := api.WorkflowID("workflow-vault")
	resourceID := "staged:opaque-1"
	content := StagedMediaContent{
		Name:        "Example.Release.2026.frame.png",
		Bytes:       []byte("private-image-bytes"),
		ContentType: "image/png",
	}
	if err := vault.Put(testOwnerID, workflowID, resourceID, content, now.Add(time.Hour)); err != nil {
		t.Fatalf("put private artifact: %v", err)
	}
	if _, err := vault.Get("different-owner", workflowID, resourceID, now); !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("foreign owner error=%v", err)
	}
	vault.InvalidateAll()

	restarted, err := NewPrivateArtifactVault(root)
	if err != nil {
		t.Fatalf("restart private artifact vault: %v", err)
	}
	value, err := restarted.Get(testOwnerID, workflowID, resourceID, now)
	if err != nil {
		t.Fatalf("get private artifact after restart: %v", err)
	}
	decoded, ok := value.(StagedMediaContent)
	if !ok || decoded.Name != content.Name || string(decoded.Bytes) != string(content.Bytes) {
		t.Fatalf("decoded private artifact=%#v", value)
	}
	consumed, err := restarted.Consume(testOwnerID, workflowID, resourceID, now)
	if err != nil {
		t.Fatalf("consume private artifact: %v", err)
	}
	consumedContent, ok := consumed.(StagedMediaContent)
	if !ok || consumedContent.Name != content.Name {
		t.Fatalf("consumed private artifact=%#v", consumed)
	}
	restarted.InvalidateAll()
	afterConsume, err := NewPrivateArtifactVault(root)
	if err != nil {
		t.Fatalf("reopen consumed private artifact vault: %v", err)
	}
	if _, err := afterConsume.Get(testOwnerID, workflowID, resourceID, now); !errors.Is(err, ErrPrivateResourceConsumed) {
		t.Fatalf("consumed artifact restart error=%v", err)
	}
}

func TestPrivateArtifactVaultDetectsDigestMismatchAndCleansExpiry(t *testing.T) {
	t.Parallel()

	vault, err := NewPrivateArtifactVault(t.TempDir())
	if err != nil {
		t.Fatalf("new private artifact vault: %v", err)
	}
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	key, err := newPrivateResourceKey(testOwnerID, "workflow-integrity", "preview:1")
	if err != nil {
		t.Fatalf("private resource key: %v", err)
	}
	if err := vault.Put(
		testOwnerID,
		key.workflowID,
		key.resourceID,
		MediaPreviewContent{
Bytes: []byte("private-preview"),
 ContentType: "image/png",
 Width: 10,
 Height: 10,
},
		now.Add(time.Minute),
	); err != nil {
		t.Fatalf("put private preview: %v", err)
	}
	vault.InvalidateAll()
	if err := os.WriteFile(vault.blobFilePath(key), []byte("tampered"), 0o600); err != nil {
		t.Fatalf("tamper private payload: %v", err)
	}
	if _, err := vault.Get(testOwnerID, key.workflowID, key.resourceID, now); !errors.Is(err, ErrPrivateResourceIntegrity) {
		t.Fatalf("tampered private payload error=%v", err)
	}

	expiredID := "preview:expired"
	if err := vault.Put(
		testOwnerID,
		key.workflowID,
		expiredID,
		MediaPreviewContent{Bytes: []byte("expired"), ContentType: "image/png"},
		now.Add(time.Second),
	); err != nil {
		t.Fatalf("put expiring private preview: %v", err)
	}
	expiredKey, err := newPrivateResourceKey(testOwnerID, key.workflowID, expiredID)
	if err != nil {
		t.Fatalf("expired private resource key: %v", err)
	}
	vault.InvalidateAll()
	if err := vault.CleanupExpired(now.Add(2 * time.Second)); err != nil {
		t.Fatalf("cleanup private artifact vault: %v", err)
	}
	if _, err := os.Stat(vault.metadataPath(expiredKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired metadata stat error=%v", err)
	}
	if _, err := os.Stat(vault.blobFilePath(expiredKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired payload stat error=%v", err)
	}
}

func TestPrivateArtifactVaultNonDurableReplacementCannotResurrectStaleAuthority(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	vault, err := NewPrivateArtifactVault(root)
	if err != nil {
		t.Fatalf("new private artifact vault: %v", err)
	}
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	workflowID := api.WorkflowID("workflow-replacement")
	resourceID := "private:replacement"
	if err := vault.Put(
		testOwnerID,
		workflowID,
		resourceID,
		MediaPreviewContent{Bytes: []byte("stale-durable-content"), ContentType: "image/png"},
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("put durable private artifact: %v", err)
	}
	if err := vault.Put(
		testOwnerID,
		workflowID,
		resourceID,
		struct{ Value string }{Value: "process-local-authority"},
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("replace with process-local private artifact: %v", err)
	}
	vault.InvalidateAll()

	restarted, err := NewPrivateArtifactVault(root)
	if err != nil {
		t.Fatalf("restart private artifact vault: %v", err)
	}
	if _, err := restarted.Get(testOwnerID, workflowID, resourceID, now); !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("stale durable artifact was resurrected: %v", err)
	}
}
