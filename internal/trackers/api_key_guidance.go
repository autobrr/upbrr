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

// APIKeyGuidance retains the latest upload POST observation by credential digest.
// Observations are process-local and other responses must not be passed to Observe.
// It never changes authentication or upload eligibility. Message must return
// secret-free advisory text and may return empty when no guidance is applicable.
type APIKeyGuidance struct {
	Header   string
	Message  func(APIKeyExpiry, time.Time) string
	mu       sync.Mutex
	expiries map[[sha256.Size]byte]APIKeyExpiry
}

// Observe replaces this key's observation from an upload POST response, including
// rejected uploads. Missing and malformed headers replace any prior expiry.
func (g *APIKeyGuidance) Observe(key string, header http.Header, status int) {
	key = strings.TrimSpace(key)
	if g == nil || key == "" || status <= 0 {
		return
	}
	reported := len(header.Values(g.Header)) > 0
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

// Guidance returns advice for this key's latest observed upload POST, or empty
// before any POST response has been observed. No key is retained.
func (g *APIKeyGuidance) Guidance(key string, now time.Time) string {
	if g == nil || g.Message == nil {
		return ""
	}
	g.mu.Lock()
	expiry, observed := g.expiries[sha256.Sum256([]byte(strings.TrimSpace(key)))]
	g.mu.Unlock()
	if !observed {
		return ""
	}
	return g.Message(expiry, now)
}
