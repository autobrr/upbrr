// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"strings"
	"testing"
)

func TestReadScreenshotSourceEnforcesLimit(t *testing.T) {
	t.Parallel()

	data, err := readScreenshotSource(strings.NewReader("1234"), 4)
	if err != nil {
		t.Fatalf("read exact limit: %v", err)
	}
	if got := string(data); got != "1234" {
		t.Fatalf("data = %q, want %q", got, "1234")
	}
	if _, err := readScreenshotSource(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("expected oversized screenshot source error")
	}
}

func TestFitsScreenshotBatchEnforcesAggregateLimit(t *testing.T) {
	t.Parallel()

	if !fitsScreenshotBatch(azScreenshotBatchMax-1, 1) {
		t.Fatal("expected exact aggregate limit to fit")
	}
	if fitsScreenshotBatch(azScreenshotBatchMax-1, 2) {
		t.Fatal("expected aggregate overflow to be rejected")
	}
}
