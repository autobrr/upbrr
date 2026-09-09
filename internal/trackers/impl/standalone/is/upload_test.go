// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSuccessfulUploadResponseExtractsTorrentID(t *testing.T) {
	id, ok := successfulUploadResponse("https://immortalseed.me/details.php?hash=abc123", "")
	if !ok {
		t.Fatal("expected upload success")
	}
	if id != "abc123" {
		t.Fatalf("expected torrent id abc123, got %q", id)
	}
}

func TestSuccessfulUploadResponseAcceptsThankYouText(t *testing.T) {
	id, ok := successfulUploadResponse("https://immortalseed.me/upload.php", "<html>Thank you for uploading</html>")
	if !ok {
		t.Fatal("expected upload success")
	}
	if id != "" {
		t.Fatalf("expected no torrent id from success text, got %q", id)
	}
}

func TestSuccessfulUploadResponseRejectsMissingSuccessSignals(t *testing.T) {
	id, ok := successfulUploadResponse("https://immortalseed.me/upload.php", "<html>failed</html>")
	if ok {
		t.Fatalf("expected upload failure, got id %q", id)
	}
}

func TestResolveGenresPreservesAutomaticTMDBPresenceAndManualFacts(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Release: api.ReleaseInfo{Genre: "Release"}, ProviderMetadata: api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{Genres: ""}, IMDB: &api.IMDBMetadata{Genres: "IMDb"},
	}}
	if got := resolveGenres(meta); got != "" {
		t.Fatalf("blank TMDB genres = %q", got)
	}
	meta.ProviderMetadata.TMDB = nil
	if got := resolveGenres(meta); got != "IMDb" {
		t.Fatalf("IMDb genres = %q", got)
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{Genres: []string{"Manual"}, GenresProvenance: api.FactProvenanceManual}
	if got := resolveGenres(meta); got != "Manual" {
		t.Fatalf("manual genres = %q", got)
	}
}
