// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package redaction

import (
	"strings"
	"testing"
)

func TestTrackerPageURLPreservesIdentityWithoutExposingPrivateValues(t *testing.T) {
	t.Parallel()
	identity := strings.Repeat("a", 40)
	for _, tc := range []struct{ value, want string }{
		{"https://user:secret@tracker.invalid/details.php?hash=" + identity + "&token=private&custom=private#private", "https://tracker.invalid/details.php?hash=" + identity},
		{"https://tracker.invalid/details.php?id=" + identity, "https://tracker.invalid/details.php?id=" + identity},
		{"https://tracker.invalid/index.php?page=upload&auth=private", "https://tracker.invalid/index.php"},
		{"https://tracker.invalid/details.php?hash=abc&hash=def", "https://tracker.invalid/details.php"},
		{"https://tracker.invalid/details.php?id=abc%0Aprivate", "https://tracker.invalid/details.php"},
		{"https://tracker.invalid/details.php?id=abc%26token%3Dprivate", "https://tracker.invalid/details.php"},
		{"https://tracker.invalid/" + identity + "/announce?id=42", "https://tracker.invalid/[REDACTED]/announce?id=42"},
		{"https:opaque?hash=abc123", ""},
		{"/details.php?hash=abc123", ""},
	} {
		if got := TrackerPageURL(tc.value); got != tc.want {
			t.Errorf("public page URL = %q, want %q", got, tc.want)
		}
	}
	if got := RedactValue(identity, nil); got != "[REDACTED]" {
		t.Fatal("public link handling changed generic text redaction")
	}
}
