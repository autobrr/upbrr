// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package totp

import (
	"testing"
	"time"
)

func TestFromURIAtRFCVectorAndPeriodBoundary(t *testing.T) {
	t.Parallel()

	const otpURI = "otpauth://totp/Example:user?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, err := FromURIAt(otpURI, time.Unix(59, 0))
	if err != nil {
		t.Fatalf("FromURIAt: %v", err)
	}
	if code != "287082" {
		t.Fatalf("code = %q, want RFC 6238 truncated value", code)
	}
	next, err := FromURIAt(otpURI, time.Unix(60, 0))
	if err != nil {
		t.Fatalf("FromURIAt next period: %v", err)
	}
	if next == code {
		t.Fatalf("code did not change at period boundary: %q", code)
	}
}

func TestFromURIAtCustomPeriod(t *testing.T) {
	t.Parallel()

	const otpURI = "otpauth://totp/Example:user?secret=JBSWY3DPEHPK3PXP&period=60"
	before, err := FromURIAt(otpURI, time.Unix(59, 0))
	if err != nil {
		t.Fatalf("FromURIAt before boundary: %v", err)
	}
	after, err := FromURIAt(otpURI, time.Unix(60, 0))
	if err != nil {
		t.Fatalf("FromURIAt after boundary: %v", err)
	}
	if before == after {
		t.Fatalf("code did not change at custom period boundary: %q", before)
	}
}

func TestFromURIAtRejectsSecretAndPreEpochErrors(t *testing.T) {
	t.Parallel()

	if _, err := FromURIAt("otpauth://totp/Example", time.Unix(0, 0)); err == nil {
		t.Fatal("expected missing-secret error")
	}
	if _, err := FromURIAt(
		"otpauth://totp/Example?secret=JBSWY3DPEHPK3PXP",
		time.Unix(-1, 0),
	); err == nil {
		t.Fatal("expected pre-epoch error")
	}
}
