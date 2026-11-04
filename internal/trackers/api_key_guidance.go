// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"crypto/sha256"
	"net/http"
	"strings"
	"sync"
	"time"
)

// APIKeyGuidanceCode identifies account maintenance advice in tracker projections.
const APIKeyGuidanceCode = "guidance_api_key_expiry"

// APIKeyExpiry records a reported instant without retaining the raw header.
// Reported with a zero ExpiresAt means the header was malformed; an unreported
// expiry does not distinguish legacy keys from genuinely non-expiring keys.
type APIKeyExpiry struct {
	Reported  bool
	ExpiresAt time.Time
}

// APIKeyGuidance retains process-local expiry observations by credential digest.
// It never changes authentication or upload eligibility. Message must return
// secret-free advisory text and may return empty when no guidance is applicable.
type APIKeyGuidance struct {
	Header   string
	Message  func(APIKeyExpiry, time.Time) string
	mu       sync.Mutex
	expiries map[[sha256.Size]byte]APIKeyExpiry
}

// Observe captures expiry headers from responses to authenticated requests,
// including rejected uploads. Only successful responses may replace a previous
// observation with a missing header; headerless failures preserve prior facts.
func (g *APIKeyGuidance) Observe(key string, header http.Header, status int) {
	key = strings.TrimSpace(key)
	if g == nil || key == "" || status <= 0 {
		return
	}
	reported := len(header.Values(g.Header)) > 0
	if !reported && (status < http.StatusOK || status >= http.StatusMultipleChoices) {
		return
	}
	value := strings.TrimSpace(header.Get(g.Header))
	expiry := APIKeyExpiry{Reported: reported}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		expiry.ExpiresAt = parsed
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.expiries == nil {
		g.expiries = make(map[[sha256.Size]byte]APIKeyExpiry)
	}
	g.expiries[sha256.Sum256([]byte(key))] = expiry
}

// Guidance returns current advice for exactly this key, including the policy's
// missing-header advice when no response has been observed. No key is retained.
func (g *APIKeyGuidance) Guidance(key string, now time.Time) string {
	if g == nil || g.Message == nil {
		return ""
	}
	g.mu.Lock()
	expiry := g.expiries[sha256.Sum256([]byte(strings.TrimSpace(key)))]
	g.mu.Unlock()
	return g.Message(expiry, now)
}

// ObserveAPIResponse dispatches optional account guidance without changing the
// caller's HTTP result. Trackers without this policy remain unaffected.
func (r *Registry) ObserveAPIResponse(tracker, key string, header http.Header, status int) {
	if policy, ok := r.LookupAuthPolicy(tracker); ok {
		policy.APIKeyGuidance.Observe(key, header, status)
	}
}
