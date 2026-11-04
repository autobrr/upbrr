// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
)

func apiKeyGuidance() *trackers.APIKeyGuidance {
	return &trackers.APIKeyGuidance{
		Header: "X-Api-Key-Expires-At",
		Message: func(expiry trackers.APIKeyExpiry, now time.Time) string {
			if !expiry.Reported {
				return "LST did not report an API key expiry. Legacy LST API keys expire on 2027-01-01. Review your LST API key handling, replace any legacy key, and update the LST API key configured in upbrr as needed."
			}
			if expiry.ExpiresAt.IsZero() {
				return "LST reported an unreadable API key expiry. Review your LST API key handling and update the LST API key configured in upbrr as needed."
			}
			if expiry.ExpiresAt.After(now.Add(30 * 24 * time.Hour)) {
				return ""
			}
			return fmt.Sprintf(
				"LST API key expiry: %s. Renew or replace the key and update the LST API key configured in upbrr.",
				expiry.ExpiresAt.UTC().Format(time.RFC3339Nano),
			)
		},
	}
}
