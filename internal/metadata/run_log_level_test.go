// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestOperationLoggingPreservesLazyProviderClients(t *testing.T) {
	t.Parallel()
	root, err := logging.New(config.LoggingConfig{Level: "info"}, "")
	if err != nil {
		t.Fatal(err)
	}
	root.SetConsoleOutput(io.Discard, io.Discard)
	t.Cleanup(func() { _ = root.Close() })
	scoped, err := logging.NewOperationLogger(root, "debug")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	source := filepath.Join(base, "Example.Release.2026.1080p-GRP.mkv")
	if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(base, "metadata.sqlite")
	service := NewService(&fakeRepo{}, WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}), WithLogger(root), WithSRRDBPaths(dbPath), WithTagsPathFromDB(dbPath), WithMediaInfoExporter(stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}))
	zero := 0
	request := testCollectionRequest(t, api.Request{SourcePath: source, ExternalIDOverrides: api.ExternalIDOverrides{
		TMDBID:   &zero,
		IMDBID:   &zero,
		TVDBID:   &zero,
		TVmazeID: &zero,
		MALID:    &zero,
	}})
	ctx, cancel := context.WithTimeout(logging.WithOperationLogger(t.Context(), scoped), 5*time.Second)
	defer cancel()
	if _, err := service.CollectPreparationEvidence(ctx, request); err != nil {
		t.Fatal(err)
	}
	if service.imdb == nil || service.tvdb == nil || service.tvmaze == nil || service.anilist == nil {
		t.Fatal("collection discarded lazily initialized provider clients")
	}
	imdb, tvdb, tvmaze, anilist := service.imdb, service.tvdb, service.tvmaze, service.anilist
	if _, err := service.CollectPreparationEvidence(ctx, request); err != nil {
		t.Fatal(err)
	}
	if service.imdb != imdb || service.tvdb != tvdb || service.tvmaze != tvmaze || service.anilist != anilist {
		t.Fatal("operation logging replaced retained provider clients")
	}
	if service.logger != root {
		t.Fatal("collection changed the shared metadata logger")
	}
}

func TestArrLookupRejectsNilClient(t *testing.T) {
	t.Parallel()
	var client *httpArrLookupClient
	if _, err := client.Lookup(t.Context(), preparationstate.State{}); err == nil {
		t.Fatal("nil Arr client did not return an error")
	}
}
