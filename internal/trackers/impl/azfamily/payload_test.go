// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCanonicalLanguagePayloadKeepsSiteGrouping(t *testing.T) {
	got := languageValues(api.UploadSubject{
		AudioLanguages:    languageutil.NormalizeLanguageList([]string{"nb", "fil", "tgl", "cmn"}),
		SubtitleLanguages: languageutil.NormalizeLanguageList([]string{"English (Full)", "Norwegian Bokmål", "pt-BR", "phi"}),
	})
	if !slices.Equal(got.Audio, []string{"136", "22", "33"}) || !slices.Equal(got.Subtitles, []string{"116", "22", "45"}) {
		t.Fatalf("language payload lost canonical languages or invented a collective member: %#v", got)
	}
}

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
