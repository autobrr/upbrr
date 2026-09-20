// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	privateResourceKindMediaPreview = "releaseworkflow/media-preview/v1"
	privateResourceKindStagedMedia  = "releaseworkflow/staged-media/v1"
	privateResourceKindDescriptions = "releaseworkflow/description-instructions/v1"
)

type privateArtifactMetadata struct {
	KeyDigest   string    `json:"keyDigest"`
	ScopeDigest string    `json:"scopeDigest"`
	Kind        string    `json:"kind"`
	Digest      string    `json:"digest"`
	ExpiresAt   time.Time `json:"expiresAt"`
	ConsumedAt  time.Time `json:"consumedAt,omitempty"`
	RefCount    uint64    `json:"refCount"`
}

// PrivateArtifactVault persists codec-supported private resources as
// owner/workflow-scoped opaque, digest-checked files. Unsupported runtime
// values remain available in memory and fail closed after restart.
type PrivateArtifactVault struct {
	root     string
	mu       sync.Mutex
	entries  map[privateResourceKey]privateResourceEntry
	consumed map[privateResourceKey]struct{}
	codecs   map[string]PrivateResourceCodec
}

// NewPrivateArtifactVault opens or creates one restricted private artifact root.
func NewPrivateArtifactVault(root string, codecs ...PrivateResourceCodec) (*PrivateArtifactVault, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("private artifact vault root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("private artifact vault resolve root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("private artifact vault create root: %w", err)
	}
	if err := os.Chmod(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("private artifact vault restrict root: %w", err)
	}
	vault := &PrivateArtifactVault{
		root:     absolute,
		entries:  make(map[privateResourceKey]privateResourceEntry),
		consumed: make(map[privateResourceKey]struct{}),
		codecs: map[string]PrivateResourceCodec{
			privateResourceKindMediaPreview: {
				Kind: privateResourceKindMediaPreview, Decode: decodeMediaPreviewContent,
			},
			privateResourceKindStagedMedia: {
				Kind: privateResourceKindStagedMedia, Decode: decodeStagedMediaContent,
			},
			privateResourceKindDescriptions: {
				Kind: privateResourceKindDescriptions, Decode: decodeDescriptionInstructions,
			},
			privateResourceKindOperationCommand: {
				Kind: privateResourceKindOperationCommand, Decode: decodeDurableOperationCommand,
			},
			privateResourceKindRegisteredArtifactAuthority: {
				Kind: privateResourceKindRegisteredArtifactAuthority, Decode: decodeRegisteredArtifactAuthority,
			},
		},
	}
	for _, codec := range codecs {
		kind := strings.TrimSpace(codec.Kind)
		if kind == "" || codec.Decode == nil {
			return nil, errors.New("private artifact vault codec kind and decoder are required")
		}
		if _, exists := vault.codecs[kind]; exists {
			return nil, fmt.Errorf("private artifact vault codec %q is duplicated", kind)
		}
		vault.codecs[kind] = codec
	}
	return vault, nil
}

// Put retains one owner-scoped private resource.
func (v *PrivateArtifactVault) Put(
	ownerID string,
	workflowID api.WorkflowID,
	resourceID string,
	value any,
	expiresAt time.Time,
) error {
	key, err := newPrivateResourceKey(ownerID, workflowID, resourceID)
	if err != nil {
		return err
	}
	if expiresAt.IsZero() {
		return errors.New("private resource expiry is required")
	}
	kind, payload, durable, err := encodePrivateResource(value)
	if err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if durable {
		if err := v.writeLocked(key, kind, payload, expiresAt); err != nil {
			return err
		}
	} else {
		if err := errors.Join(
			removeIfPresent(v.blobFilePath(key)),
			removeIfPresent(v.metadataPath(key)),
		); err != nil {
			return fmt.Errorf("private artifact vault remove stale durable resource: %w", err)
		}
	}
	v.entries[key] = privateResourceEntry{value: value, expiresAt: expiresAt}
	delete(v.consumed, key)
	return nil
}

// Get returns an unconsumed, unexpired owner-scoped resource.
func (v *PrivateArtifactVault) Get(
	ownerID string,
	workflowID api.WorkflowID,
	resourceID string,
	now time.Time,
) (any, error) {
	key, err := newPrivateResourceKey(ownerID, workflowID, resourceID)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.getLocked(key, now)
}

// Consume atomically returns and invalidates one single-use resource.
func (v *PrivateArtifactVault) Consume(
	ownerID string,
	workflowID api.WorkflowID,
	resourceID string,
	now time.Time,
) (any, error) {
	key, err := newPrivateResourceKey(ownerID, workflowID, resourceID)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	value, err := v.getLocked(key, now)
	if err != nil {
		return nil, err
	}
	metadata, metadataErr := v.readMetadataLocked(key)
	if metadataErr == nil {
		metadata.ConsumedAt = now
		if err := v.writeMetadataLocked(key, metadata); err != nil {
			return nil, err
		}
		if err := removeIfPresent(v.blobFilePath(key)); err != nil {
			return nil, err
		}
	}
	delete(v.entries, key)
	v.consumed[key] = struct{}{}
	return value, nil
}

// Delete removes one private resource and its persisted metadata.
func (v *PrivateArtifactVault) Delete(ownerID string, workflowID api.WorkflowID, resourceID string) {
	key, err := newPrivateResourceKey(ownerID, workflowID, resourceID)
	if err != nil {
		return
	}
	v.mu.Lock()
	entry, ok := v.entries[key]
	if !ok {
		metadata, metadataErr := v.readMetadataLocked(key)
		if metadataErr != nil && !errors.Is(metadataErr, ErrPrivateResourceUnavailable) {
			v.mu.Unlock()
			return
		}
		if metadataErr == nil {
			value, decodeErr := v.decodeResourceForReleaseLocked(v.metadataPath(key), metadata)
			if decodeErr != nil {
				v.mu.Unlock()
				return
			}
			if value != nil {
				entry = privateResourceEntry{value: value, expiresAt: metadata.ExpiresAt}
				ok = true
			}
		}
	}
	delete(v.entries, key)
	delete(v.consumed, key)
	_ = removeIfPresent(v.blobFilePath(key))
	_ = removeIfPresent(v.metadataPath(key))
	v.mu.Unlock()
	if ok {
		releasePrivateResource(entry.value)
	}
}

// InvalidateWorkflow removes every private resource owned by one workflow.
func (v *PrivateArtifactVault) InvalidateWorkflow(ownerID string, workflowID api.WorkflowID) {
	v.InvalidateWorkflowExcept(ownerID, workflowID)
}

// DeleteWorkflow removes every private resource owned by one workflow and
// reports durable cleanup failures so callers can keep the owning state for a
// retry.
func (v *PrivateArtifactVault) DeleteWorkflow(ownerID string, workflowID api.WorkflowID) error {
	resources, err := v.deleteWorkflowExcept(ownerID, workflowID, false)
	if err != nil {
		return err
	}
	for _, resource := range resources {
		releasePrivateResource(resource)
	}
	return nil
}

// InvalidateWorkflowExcept removes workflow resources except explicitly
// preserved IDs, including their durable payload and metadata.
func (v *PrivateArtifactVault) InvalidateWorkflowExcept(
	ownerID string,
	workflowID api.WorkflowID,
	preservedResourceIDs ...string,
) {
	resources, _ := v.deleteWorkflowExcept(ownerID, workflowID, true, preservedResourceIDs...)
	for _, resource := range resources {
		releasePrivateResource(resource)
	}
}

func (v *PrivateArtifactVault) deleteWorkflowExcept(
	ownerID string,
	workflowID api.WorkflowID,
	invalidateMemoryOnFailure bool,
	preservedResourceIDs ...string,
) ([]any, error) {
	ownerID = strings.TrimSpace(ownerID)
	preserved := make(map[privateResourceKey]struct{}, len(preservedResourceIDs))
	preservedDigests := make(map[string]struct{}, len(preservedResourceIDs))
	for _, resourceID := range preservedResourceIDs {
		key, err := newPrivateResourceKey(ownerID, workflowID, resourceID)
		if err != nil {
			continue
		}
		preserved[key] = struct{}{}
		preservedDigests[privateKeyDigest(key)] = struct{}{}
	}
	if ownerID == "" || strings.TrimSpace(string(workflowID)) == "" {
		if invalidateMemoryOnFailure {
			return v.removeWorkflowEntries(ownerID, workflowID, preserved),
				errors.New("private artifact vault workflow scope is required")
		}
		return nil, errors.New("private artifact vault workflow scope is required")
	}
	scopeDigest := privateScopeDigest(ownerID, workflowID)
	return func() (resources []any, err error) {
		v.mu.Lock()
		defer v.mu.Unlock()
		if invalidateMemoryOnFailure {
			defer func() {
				if err != nil {
					resources = v.removeWorkflowEntriesLocked(ownerID, workflowID, preserved)
				}
			}()
		}
		metadataFiles, metadataErr := v.metadataFilesLocked()
		if metadataErr != nil {
			return nil, metadataErr
		}
		metadataFilesToDelete := make([]string, 0)
		loadedDigests := make(map[string]struct{})
		for key := range v.entries {
			if key.ownerID == ownerID && key.workflowID == workflowID {
				loadedDigests[privateKeyDigest(key)] = struct{}{}
			}
		}
		for _, metadataFile := range metadataFiles {
			payload, readErr := os.ReadFile(metadataFile)
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if readErr != nil {
				return nil, fmt.Errorf("private artifact vault read workflow metadata: %w", readErr)
			}
			var metadata privateArtifactMetadata
			if json.Unmarshal(payload, &metadata) != nil || metadata.ScopeDigest != scopeDigest {
				continue
			}
			if _, ok := preservedDigests[metadata.KeyDigest]; ok {
				continue
			}
			if _, loaded := loadedDigests[metadata.KeyDigest]; !loaded {
				resource, decodeErr := v.decodeResourceForReleaseLocked(metadataFile, metadata)
				if decodeErr != nil {
					return nil, decodeErr
				}
				if resource != nil {
					resources = append(resources, resource)
				}
			}
			metadataFilesToDelete = append(metadataFilesToDelete, metadataFile)
		}
		for _, metadataFile := range metadataFilesToDelete {
			base := strings.TrimSuffix(metadataFile, ".json")
			if removeErr := removeIfPresent(base + ".blob"); removeErr != nil {
				return nil, removeErr
			}
			if removeErr := removeIfPresent(metadataFile); removeErr != nil {
				return nil, removeErr
			}
		}
		resources = append(resources, v.removeWorkflowEntriesLocked(ownerID, workflowID, preserved)...)
		return resources, nil
	}()
}

func (v *PrivateArtifactVault) removeWorkflowEntries(
	ownerID string,
	workflowID api.WorkflowID,
	preserved map[privateResourceKey]struct{},
) []any {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.removeWorkflowEntriesLocked(ownerID, workflowID, preserved)
}

func (v *PrivateArtifactVault) removeWorkflowEntriesLocked(
	ownerID string,
	workflowID api.WorkflowID,
	preserved map[privateResourceKey]struct{},
) []any {
	resources := make([]any, 0)
	for key, entry := range v.entries {
		if key.ownerID != ownerID || key.workflowID != workflowID {
			continue
		}
		if _, ok := preserved[key]; ok {
			continue
		}
		resources = append(resources, entry.value)
		delete(v.entries, key)
		delete(v.consumed, key)
	}
	for key := range v.consumed {
		if key.ownerID == ownerID && key.workflowID == workflowID {
			if _, ok := preserved[key]; !ok {
				delete(v.consumed, key)
			}
		}
	}
	return resources
}

// InvalidateAll clears process-local handles while preserving durable files,
// modeling a process restart.
func (v *PrivateArtifactVault) InvalidateAll() {
	v.mu.Lock()
	resources := make([]any, 0, len(v.entries))
	for _, entry := range v.entries {
		resources = append(resources, entry.value)
	}
	clear(v.entries)
	clear(v.consumed)
	v.mu.Unlock()
	for _, resource := range resources {
		if _, durable := resource.(DurablePrivateResource); !durable {
			releasePrivateResource(resource)
		}
	}
}

// CleanupExpired removes expired payloads and consumed tombstones before the cutoff.
func (v *PrivateArtifactVault) CleanupExpired(now time.Time) error {
	if now.IsZero() {
		return errors.New("private artifact vault cleanup time is required")
	}
	v.mu.Lock()
	metadataFiles, err := v.metadataFilesLocked()
	if err != nil {
		v.mu.Unlock()
		return err
	}
	type cleanupCandidate struct {
		metadataFile string
		metadata     privateArtifactMetadata
		resource     any
	}
	candidates := make([]cleanupCandidate, 0)
	for _, metadataFile := range metadataFiles {
		payload, err := os.ReadFile(metadataFile)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			v.mu.Unlock()
			return fmt.Errorf("private artifact vault read cleanup metadata: %w", err)
		}
		var metadata privateArtifactMetadata
		if err := json.Unmarshal(payload, &metadata); err != nil {
			v.mu.Unlock()
			return fmt.Errorf("private artifact vault decode cleanup metadata: %w", errors.Join(ErrPrivateResourceIntegrity, err))
		}
		base := strings.TrimSuffix(metadataFile, ".json")
		if metadata.KeyDigest == "" || filepath.Base(base) != metadata.KeyDigest || metadata.ScopeDigest == "" ||
			metadata.Kind == "" || metadata.Digest == "" || metadata.ExpiresAt.IsZero() || metadata.RefCount == 0 {
			v.mu.Unlock()
			return fmt.Errorf("private artifact vault validate cleanup metadata: %w", ErrPrivateResourceIntegrity)
		}
		if metadata.ExpiresAt.After(now) && metadata.ConsumedAt.IsZero() {
			continue
		}
		resource, decodeErr := v.decodeResourceForReleaseLocked(metadataFile, metadata)
		if decodeErr != nil {
			v.mu.Unlock()
			return fmt.Errorf("private artifact vault decode expired resource: %w", decodeErr)
		}
		candidates = append(candidates, cleanupCandidate{
			metadataFile: metadataFile,
			metadata:     metadata,
			resource:     resource,
		})
	}
	resources := make([]any, 0, len(candidates))
	resourceDigests := make(map[string]struct{}, len(candidates))
	expiredDigests := make(map[string]struct{}, len(candidates))
	var cleanupErr error
	for _, candidate := range candidates {
		if err := removePrivateResourceFiles(candidate.metadataFile); err != nil {
			cleanupErr = err
			break
		}
		expiredDigests[candidate.metadata.KeyDigest] = struct{}{}
		if candidate.resource != nil {
			resources = append(resources, candidate.resource)
			resourceDigests[candidate.metadata.KeyDigest] = struct{}{}
		}
	}
	for key, entry := range v.entries {
		if !entry.expiresAt.After(now) {
			digest := privateKeyDigest(key)
			if cleanupErr != nil {
				if _, removed := expiredDigests[digest]; !removed {
					continue
				}
			}
			if _, alreadyAdded := resourceDigests[digest]; !alreadyAdded {
				resources = append(resources, entry.value)
			}
			delete(v.entries, key)
			delete(v.consumed, key)
		}
	}
	for key := range v.consumed {
		if _, expired := expiredDigests[privateKeyDigest(key)]; expired {
			delete(v.consumed, key)
		}
	}
	v.mu.Unlock()
	for _, resource := range resources {
		releasePrivateResource(resource)
	}
	return cleanupErr
}

func removePrivateResourceFiles(metadataFile string) error {
	base := strings.TrimSuffix(metadataFile, ".json")
	return errors.Join(removeIfPresent(base+".blob"), removeIfPresent(metadataFile))
}

func (v *PrivateArtifactVault) decodeResourceForReleaseLocked(
	metadataFile string,
	metadata privateArtifactMetadata,
) (any, error) {
	if !metadata.ConsumedAt.IsZero() {
		return nil, nil
	}
	payload, err := os.ReadFile(strings.TrimSuffix(metadataFile, ".json") + ".blob")
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrPrivateResourceIntegrity
	}
	if err != nil {
		return nil, fmt.Errorf("private artifact vault read release payload: %w", err)
	}
	if privatePayloadDigest(payload) != metadata.Digest {
		return nil, ErrPrivateResourceIntegrity
	}
	codec, ok := v.codecs[metadata.Kind]
	if !ok {
		return nil, fmt.Errorf("private artifact vault release codec %q: %w", metadata.Kind, ErrPrivateResourceUnavailable)
	}
	decode := codec.DecodeForRelease
	if decode == nil {
		decode = codec.Decode
	}
	value, err := decode(payload)
	if err != nil {
		return nil, fmt.Errorf("private artifact vault decode release %s: %w", metadata.Kind, err)
	}
	return value, nil
}

func (v *PrivateArtifactVault) metadataFilesLocked() ([]string, error) {
	metadataFiles := make([]string, 0)
	if err := filepath.WalkDir(v.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			metadataFiles = append(metadataFiles, path)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("private artifact vault walk metadata: %w", err)
	}
	return metadataFiles, nil
}

func (v *PrivateArtifactVault) getLocked(key privateResourceKey, now time.Time) (any, error) {
	if _, ok := v.consumed[key]; ok {
		return nil, ErrPrivateResourceConsumed
	}
	if entry, ok := v.entries[key]; ok {
		if !entry.expiresAt.After(now) {
			delete(v.entries, key)
			releasePrivateResource(entry.value)
			_ = removeIfPresent(v.blobFilePath(key))
			_ = removeIfPresent(v.metadataPath(key))
			return nil, ErrPrivateResourceUnavailable
		}
		return entry.value, nil
	}
	metadata, err := v.readMetadataLocked(key)
	if err != nil {
		return nil, err
	}
	if !metadata.ConsumedAt.IsZero() {
		v.consumed[key] = struct{}{}
		return nil, ErrPrivateResourceConsumed
	}
	if !metadata.ExpiresAt.After(now) {
		value, decodeErr := v.decodeResourceForReleaseLocked(v.metadataPath(key), metadata)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if value != nil {
			releasePrivateResource(value)
		}
		_ = removeIfPresent(v.blobFilePath(key))
		_ = removeIfPresent(v.metadataPath(key))
		return nil, ErrPrivateResourceUnavailable
	}
	payload, err := os.ReadFile(v.blobFilePath(key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrPrivateResourceUnavailable
		}
		return nil, fmt.Errorf("private artifact vault read payload: %w", err)
	}
	if privatePayloadDigest(payload) != metadata.Digest {
		return nil, ErrPrivateResourceIntegrity
	}
	codec, ok := v.codecs[metadata.Kind]
	if !ok || codec.Decode == nil {
		return nil, ErrPrivateResourceUnavailable
	}
	value, err := codec.Decode(payload)
	if err != nil {
		return nil, fmt.Errorf("private artifact vault decode %s: %w", metadata.Kind, err)
	}
	v.entries[key] = privateResourceEntry{value: value, expiresAt: metadata.ExpiresAt}
	return value, nil
}

func (v *PrivateArtifactVault) writeLocked(
	key privateResourceKey,
	kind string,
	payload []byte,
	expiresAt time.Time,
) error {
	metadata := privateArtifactMetadata{
		KeyDigest:   privateKeyDigest(key),
		ScopeDigest: privateScopeDigest(key.ownerID, key.workflowID),
		Kind:        kind,
		Digest:      privatePayloadDigest(payload),
		ExpiresAt:   expiresAt,
		RefCount:    1,
	}
	if err := writeRestrictedFile(v.blobFilePath(key), payload); err != nil {
		return fmt.Errorf("private artifact vault write payload: %w", err)
	}
	if err := v.writeMetadataLocked(key, metadata); err != nil {
		_ = removeIfPresent(v.blobFilePath(key))
		return err
	}
	return nil
}

func (v *PrivateArtifactVault) readMetadataLocked(key privateResourceKey) (privateArtifactMetadata, error) {
	payload, err := os.ReadFile(v.metadataPath(key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return privateArtifactMetadata{}, ErrPrivateResourceUnavailable
		}
		return privateArtifactMetadata{}, fmt.Errorf("private artifact vault read metadata: %w", err)
	}
	var metadata privateArtifactMetadata
	if err := json.Unmarshal(payload, &metadata); err != nil {
		return privateArtifactMetadata{}, ErrPrivateResourceIntegrity
	}
	if metadata.KeyDigest != privateKeyDigest(key) || metadata.ScopeDigest != privateScopeDigest(key.ownerID, key.workflowID) ||
		metadata.Kind == "" || metadata.Digest == "" || metadata.ExpiresAt.IsZero() || metadata.RefCount == 0 {
		return privateArtifactMetadata{}, ErrPrivateResourceIntegrity
	}
	return metadata, nil
}

func (v *PrivateArtifactVault) writeMetadataLocked(key privateResourceKey, metadata privateArtifactMetadata) error {
	payload, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("private artifact vault encode metadata: %w", err)
	}
	if err := writeRestrictedFile(v.metadataPath(key), payload); err != nil {
		return fmt.Errorf("private artifact vault write metadata: %w", err)
	}
	return nil
}

func (v *PrivateArtifactVault) metadataPath(key privateResourceKey) string {
	return v.resourceBasePath(key) + ".json"
}

func (v *PrivateArtifactVault) blobFilePath(key privateResourceKey) string {
	return v.resourceBasePath(key) + ".blob"
}

func (v *PrivateArtifactVault) resourceBasePath(key privateResourceKey) string {
	digest := privateKeyDigest(key)
	return filepath.Join(v.root, digest[:2], digest)
}

func privateKeyDigest(key privateResourceKey) string {
	sum := sha256.Sum256([]byte(key.ownerID + "\x00" + string(key.workflowID) + "\x00" + key.resourceID))
	return hex.EncodeToString(sum[:])
}

func privateScopeDigest(ownerID string, workflowID api.WorkflowID) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(ownerID) + "\x00" + string(workflowID)))
	return hex.EncodeToString(sum[:])
}

func privatePayloadDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func writeRestrictedFile(path string, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("private artifact vault create resource directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".private-*")
	if err != nil {
		return fmt.Errorf("private artifact vault create temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("private artifact vault restrict temporary file: %w", err)
	}
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return fmt.Errorf("private artifact vault write temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("private artifact vault sync temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("private artifact vault close temporary file: %w", err)
	}
	if err := removeIfPresent(path); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("private artifact vault install temporary file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("private artifact vault restrict installed file: %w", err)
	}
	return nil
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("private artifact vault remove file: %w", err)
}

func encodePrivateResource(value any) (string, []byte, bool, error) {
	var (
		kind string
		data any
	)
	switch typed := value.(type) {
	case MediaPreviewContent:
		kind, data = privateResourceKindMediaPreview, typed
	case StagedMediaContent:
		kind, data = privateResourceKindStagedMedia, typed
	case api.DescriptionInstructions:
		kind, data = privateResourceKindDescriptions, typed
	case DurablePrivateResource:
		kind, payload, err := typed.MarshalPrivateResource()
		if err != nil {
			return "", nil, false, fmt.Errorf("private artifact vault marshal resource: %w", err)
		}
		if strings.TrimSpace(kind) == "" {
			return "", nil, false, errors.New("private artifact vault resource kind is required")
		}
		return kind, payload, true, nil
	default:
		return "", nil, false, nil
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return "", nil, false, fmt.Errorf("private artifact vault encode %s: %w", kind, err)
	}
	return kind, payload, true, nil
}

func decodeMediaPreviewContent(payload []byte) (any, error) {
	var value MediaPreviewContent
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, fmt.Errorf("private artifact vault decode media preview: %w", err)
	}
	return value, nil
}

func decodeStagedMediaContent(payload []byte) (any, error) {
	var value StagedMediaContent
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, fmt.Errorf("private artifact vault decode staged media: %w", err)
	}
	return value, nil
}

func decodeDescriptionInstructions(payload []byte) (any, error) {
	var value api.DescriptionInstructions
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, fmt.Errorf("private artifact vault decode description instructions: %w", err)
	}
	return value, nil
}
