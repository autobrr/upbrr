// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const defaultChallengeTTL = 5 * time.Minute

var sharedChallengeManager = NewChallengeManager(defaultChallengeTTL)

// Challenge identifies one manual 2FA continuation for a tracker auth login.
type Challenge struct {
	// ID is the opaque token returned to the UI and later supplied to Submit2FA.
	ID string
	// TrackerID is the normalized tracker code that owns the challenge.
	TrackerID string
	// ExpiresAt is the UTC deadline after which the challenge is discarded.
	ExpiresAt time.Time
}

// ChallengeManager stores time-limited manual 2FA challenges for tracker auth.
type ChallengeManager struct {
	mu  sync.Mutex
	ttl time.Duration
	now func() time.Time
	ids func() (string, error)

	items map[string]Challenge
}

// NewChallengeManager returns a challenge manager with ttl, using the default TTL when ttl is not positive.
func NewChallengeManager(ttl time.Duration) *ChallengeManager {
	if ttl <= 0 {
		ttl = defaultChallengeTTL
	}
	return &ChallengeManager{
		ttl:   ttl,
		now:   time.Now,
		ids:   randomChallengeID,
		items: map[string]Challenge{},
	}
}

// Create registers a challenge for trackerID and returns its opaque ID.
func (m *ChallengeManager) Create(ctx context.Context, trackerID string) string {
	if ctx != nil && ctx.Err() != nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cleanupLocked()
	id, err := m.ids()
	if err != nil {
		return ""
	}
	trackerID = normalizeTrackerID(trackerID)
	m.items[id] = Challenge{
		ID:        id,
		TrackerID: trackerID,
		ExpiresAt: m.now().UTC().Add(m.ttl),
	}
	return id
}

// Get returns an active challenge by ID after pruning expired challenges.
func (m *ChallengeManager) Get(challengeID string) (Challenge, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cleanupLocked()
	challenge, ok := m.items[strings.TrimSpace(challengeID)]
	return challenge, ok
}

// Consume validates that challengeID belongs to trackerID, removes it, and returns the consumed challenge.
func (m *ChallengeManager) Consume(challengeID string, trackerID string) (Challenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cleanupLocked()
	challengeID = strings.TrimSpace(challengeID)
	challenge, ok := m.items[challengeID]
	if !ok {
		return Challenge{}, errors.New("tracker auth: no active manual 2FA challenge")
	}
	if !strings.EqualFold(challenge.TrackerID, strings.TrimSpace(trackerID)) {
		return Challenge{}, errors.New("tracker auth: challenge tracker mismatch")
	}
	delete(m.items, challengeID)
	return challenge, nil
}

func (m *ChallengeManager) cleanupLocked() {
	now := m.now().UTC()
	for id, challenge := range m.items {
		if !challenge.ExpiresAt.After(now) {
			delete(m.items, id)
		}
	}
}

func randomChallengeID() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("tracker auth: generate challenge id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
