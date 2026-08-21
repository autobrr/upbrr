// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareUploadRequiresPasskeyDerivedAnnounce(t *testing.T) {
	t.Parallel()

	_, err := prepareUpload(context.Background(), trackers.PreparationInput{
		Tracker: "TL",
		Intent:  trackers.PreparationIntentUpload,
		Meta: api.UploadSubject{Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryMovie,
			IMDBID:   1234567,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "required passkey-derived announce URL is missing") {
		t.Fatalf("prepare upload error = %v", err)
	}
}
