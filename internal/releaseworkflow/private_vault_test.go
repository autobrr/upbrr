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

func TestPrivateArtifactVaultRestartsRegisteredArtifactAuthority(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	vault, err := NewPrivateArtifactVault(root)
	if err != nil {
		t.Fatalf("new private artifact vault: %v", err)
	}
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	authority := RegisteredArtifactAuthority{
		ClientSubject: api.ClientSubject{SourcePath: `C:\releases\Example.Release.2026.mkv`},
		Torrents: map[api.TrackerID]api.TorrentResult{
			"ALPHA": {
				Tracker: "ALPHA",
				Path:    `C:\state\tmp\Example.Release.2026\[alpha].Example.Release.2026.torrent`,
			},
		},
	}
	resourceID := registeredArtifactAuthorityPrivateResourceID("upload-result-1")
	if err := vault.Put(testOwnerID, "workflow-registered", resourceID, authority, now.Add(time.Hour)); err != nil {
		t.Fatalf("put registered artifact authority: %v", err)
	}
	vault.InvalidateAll()

	restarted, err := NewPrivateArtifactVault(root)
	if err != nil {
		t.Fatalf("restart private artifact vault: %v", err)
	}
	value, err := restarted.Get(testOwnerID, "workflow-registered", resourceID, now)
	if err != nil {
		t.Fatalf("get registered artifact authority after restart: %v", err)
	}
	decoded, ok := value.(RegisteredArtifactAuthority)
	if !ok || decoded.ClientSubject.SourcePath != authority.ClientSubject.SourcePath ||
		decoded.Torrents["ALPHA"].Path != authority.Torrents["ALPHA"].Path {
		t.Fatalf("decoded registered artifact authority = %#v", value)
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
			Bytes:       []byte("private-preview"),
			ContentType: "image/png",
			Width:       10,
			Height:      10,
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

func TestPrivateArtifactVaultInvalidateWorkflowExceptPersistsPreservedResource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	vault, err := NewPrivateArtifactVault(root)
	if err != nil {
		t.Fatalf("new private artifact vault: %v", err)
	}
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	workflowID := api.WorkflowID("workflow-preserve")
	preservedID := "operation-command:operation-1"
	removedID := "preview:obsolete"
	if err := vault.Put(
		testOwnerID,
		workflowID,
		preservedID,
		MediaPreviewContent{Bytes: []byte("preserved"), ContentType: "image/png"},
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("put preserved private artifact: %v", err)
	}
	if err := vault.Put(
		testOwnerID,
		workflowID,
		removedID,
		MediaPreviewContent{Bytes: []byte("removed"), ContentType: "image/png"},
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("put removable private artifact: %v", err)
	}

	vault.InvalidateWorkflowExcept(testOwnerID, workflowID, preservedID)
	vault.InvalidateAll()
	restarted, err := NewPrivateArtifactVault(root)
	if err != nil {
		t.Fatalf("restart private artifact vault: %v", err)
	}
	if _, err := restarted.Get(testOwnerID, workflowID, preservedID, now); err != nil {
		t.Fatalf("get preserved private artifact after restart: %v", err)
	}
	if _, err := restarted.Get(testOwnerID, workflowID, removedID, now); !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("removed private artifact error=%v", err)
	}
}

func TestMemoryPrivateResourceStoreInvalidateWorkflowExcept(t *testing.T) {
	t.Parallel()

	store := NewMemoryPrivateResourceStore()
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	workflowID := api.WorkflowID("workflow-memory-preserve")
	preservedID := "operation-command:operation-1"
	removedID := "preview:obsolete"
	if err := store.Put(testOwnerID, workflowID, preservedID, "preserved", now.Add(time.Hour)); err != nil {
		t.Fatalf("put preserved private resource: %v", err)
	}
	if err := store.Put(testOwnerID, workflowID, removedID, "removed", now.Add(time.Hour)); err != nil {
		t.Fatalf("put removable private resource: %v", err)
	}

	store.InvalidateWorkflowExcept(testOwnerID, workflowID, preservedID)
	if value, err := store.Get(testOwnerID, workflowID, preservedID, now); err != nil || value != "preserved" {
		t.Fatalf("preserved private resource value=%v error=%v", value, err)
	}
	if _, err := store.Get(testOwnerID, workflowID, removedID, now); !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("removed private resource error=%v", err)
	}
}
