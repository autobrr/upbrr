// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdmeta

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSourceForMetadataDoesNotFallbackToParsedSource(t *testing.T) {
	t.Parallel()

	got, ok := SourceForMetadata(api.PreparedMetadata{
		Release: api.ReleaseInfo{Source: "WEB"},
	})
	if ok || got != "" {
		t.Fatalf("expected missing canonical source to remain unsupported, got source=%q ok=%t", got, ok)
	}
}
