// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
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

func TestUploadPreviewIncludesInMemoryNFO(t *testing.T) {
	t.Parallel()
	state := uploadState{files: []commonhttp.FileField{
		{FieldName: "torrent", Path: "example.torrent"},
		{
			FieldName: "nfo",
			FileName:  "description.txt",
			Content:   []byte("Example description"),
		},
	}}
	files := buildUploadPreview(state).Files
	if len(files) != 2 || files[1].Field != "nfo" || !files[1].Present || files[1].Path != "" {
		t.Fatalf("web upload NFO missing from preview: %#v", files)
	}
}
