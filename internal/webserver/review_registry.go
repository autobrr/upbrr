// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/webserver/jobs"
	"github.com/autobrr/upbrr/pkg/api"
)

const uploadReviewLifetime = 30 * time.Minute

var (
	// ErrUploadReviewNotFound intentionally covers unknown, expired, consumed,
	// and foreign-owner tokens without revealing token existence.
	ErrUploadReviewNotFound = errors.New("upload review not found")
)

// UploadReviewSnapshot is the owner-scoped handoff from review to execution.
type UploadReviewSnapshot struct {
	Execution jobs.UploadExecutionSnapshot
	Review    api.UploadReview
}

type uploadReviewEntry struct {
	owner     string
	expiresAt time.Time
	snapshot  UploadReviewSnapshot
}

// UploadReviewRegistry issues opaque, single-use, owner-scoped review tokens.
type UploadReviewRegistry struct {
	mu      sync.Mutex
	entries map[string]uploadReviewEntry
	now     func() time.Time
}

// NewUploadReviewRegistry constructs an empty registry.
func NewUploadReviewRegistry() *UploadReviewRegistry {
	return &UploadReviewRegistry{
		entries: make(map[string]uploadReviewEntry),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Issue stores an immutable snapshot and returns an unguessable token.
func (r *UploadReviewRegistry) Issue(owner string, snapshot UploadReviewSnapshot) (string, error) {
	owner = strings.TrimSpace(owner)
	if r == nil || owner == "" {
		return "", errors.New("upload review owner is required")
	}
	if err := validateUploadReviewSnapshot(snapshot); err != nil {
		return "", err
	}
	cloned, err := cloneUploadReviewSnapshot(snapshot)
	if err != nil {
		return "", err
	}
	token, err := newUploadReviewToken()
	if err != nil {
		return "", fmt.Errorf("create upload review token: %w", err)
	}
	cloned.Execution.ReviewToken = token
	r.mu.Lock()
	r.initializeLocked()
	now := r.now().UTC()
	r.pruneLocked(now)
	r.entries[token] = uploadReviewEntry{
		owner:     owner,
		expiresAt: now.Add(uploadReviewLifetime),
		snapshot:  cloned,
	}
	r.mu.Unlock()
	return token, nil
}

// Consume returns and deletes one snapshot. Foreign-owner, expired, and
// already-consumed tokens have the same result.
func (r *UploadReviewRegistry) Consume(owner string, token string) (UploadReviewSnapshot, error) {
	owner = strings.TrimSpace(owner)
	token = strings.TrimSpace(token)
	if r == nil || owner == "" || token == "" {
		return UploadReviewSnapshot{}, ErrUploadReviewNotFound
	}
	r.mu.Lock()
	r.initializeLocked()
	now := r.now().UTC()
	r.pruneLocked(now)
	entry, ok := r.entries[token]
	if !ok || entry.owner != owner {
		r.mu.Unlock()
		return UploadReviewSnapshot{}, ErrUploadReviewNotFound
	}
	delete(r.entries, token)
	r.mu.Unlock()
	cloned, err := cloneUploadReviewSnapshot(entry.snapshot)
	if err != nil {
		return UploadReviewSnapshot{}, fmt.Errorf("clone upload review snapshot: %w", err)
	}
	return cloned, nil
}

// PurgeOwner removes unconsumed reviews when an owner/session ends.
func (r *UploadReviewRegistry) PurgeOwner(owner string) {
	if r == nil {
		return
	}
	owner = strings.TrimSpace(owner)
	r.mu.Lock()
	for token, entry := range r.entries {
		if entry.owner == owner {
			delete(r.entries, token)
		}
	}
	r.mu.Unlock()
}

func (r *UploadReviewRegistry) initializeLocked() {
	if r.entries == nil {
		r.entries = make(map[string]uploadReviewEntry)
	}
	if r.now == nil {
		r.now = func() time.Time { return time.Now().UTC() }
	}
}

func (r *UploadReviewRegistry) pruneLocked(now time.Time) {
	for token, entry := range r.entries {
		if !entry.expiresAt.After(now) {
			delete(r.entries, token)
		}
	}
}

func validateUploadReviewSnapshot(snapshot UploadReviewSnapshot) error {
	execution := snapshot.Execution
	if execution.PreparedGeneration == 0 || execution.RuntimeGeneration == 0 || execution.Input.Release.Generation == 0 ||
		execution.PreparedGeneration != execution.Input.Release.Generation || strings.TrimSpace(execution.Input.Release.SourcePath) == "" {
		return errors.New("upload review snapshot has invalid generation lineage")
	}
	return nil
}

func cloneUploadReviewSnapshot(snapshot UploadReviewSnapshot) (UploadReviewSnapshot, error) {
	type clonePayload struct {
		Input   api.UploadReviewInput
		Outcome api.UploadReviewOutcome
		Review  api.UploadReview
	}
	payload, err := json.Marshal(clonePayload{
		Input:   snapshot.Execution.Input,
		Outcome: snapshot.Execution.Outcome,
		Review:  snapshot.Review,
	})
	if err != nil {
		return UploadReviewSnapshot{}, fmt.Errorf("clone upload review snapshot: marshal: %w", err)
	}
	var cloned clonePayload
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return UploadReviewSnapshot{}, fmt.Errorf("clone upload review snapshot: unmarshal: %w", err)
	}
	snapshot.Execution.Input = cloned.Input
	snapshot.Execution.Outcome = cloned.Outcome
	snapshot.Review = cloned.Review
	return snapshot, nil
}

func newUploadReviewToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate upload review token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}
