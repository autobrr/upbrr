// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareCandidatesSurviveReuseAndSQLiteRestart(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Example.S00E01.mkv", "Example.S01E01.mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("synthetic video"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	databasePath := filepath.Join(t.TempDir(), "candidates.db")
	repo, err := db.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	resolver := &candidateIdentityResolver{candidates: []api.ExternalIdentityCandidate{{
		Provider: api.IdentityProviderTMDB,
		ID:       42,
		Category: api.CanonicalCategoryMovie,
		Cast:     []string{"Example Actor"},
	}}}
	collector := newClientEvidenceTestCollector(clientEvidenceTestSnapshot("synthetic"))
	module, err := New(repo, resolver, collector)
	if err != nil {
		t.Fatal(err)
	}
	input := api.PrepareInput{SourcePath: root, Policy: api.PreparationPolicy{KeepFolder: true}}
	var generation api.PreparedGeneration
	check := func(result api.PrepareResult) {
		t.Helper()
		if result.Release.Generation != generation {
			t.Fatal("candidate preservation rebuilt a compatible generation")
		}
		counts := make(map[string]int)
		for _, diagnostic := range result.Diagnostics {
			counts[diagnostic.Code]++
			if diagnostic.Code == "external_identity_candidates" && (len(diagnostic.Candidates) != 1 || len(diagnostic.Candidates[0].Cast) != 1 || diagnostic.Candidates[0].Cast[0] != "Example Actor") {
				t.Fatalf("candidate diagnostic changed: %+v", diagnostic)
			}
		}
		if counts["external_identity_candidates"] != 1 || counts["multiple_source_seasons"] != 1 {
			t.Fatalf("lost or duplicated preparation diagnostics: %+v", result.Diagnostics)
		}
	}
	first, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	generation = first.Release.Generation
	check(first)
	first.Diagnostics[0].Candidates[0].Cast[0] = "caller mutation"
	reused, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	check(reused)
	if err := repo.SaveExternalMetadata(t.Context(), api.SourceScopedMetadata{SourcePath: root}); err != nil {
		t.Fatal(err)
	}
	legacy, err := repo.GetExternalMetadata(t.Context(), root)
	if err != nil || len(legacy.IdentityCandidates) != 0 {
		t.Fatalf("legacy source cache regained prepared candidates: %+v, error=%v", legacy, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = db.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	collector = newClientEvidenceTestCollector(clientEvidenceTestSnapshot("synthetic"))
	module, err = New(repo, resolver, collector)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	check(restarted)
	resolved, err := module.ResolveResult(t.Context(), api.ReleaseRef{SourcePath: root, Generation: generation})
	if err != nil {
		t.Fatal(err)
	}
	check(resolved)
	if resolver.calls != 1 || collector.collectCount() != 0 || collector.hydrateCount() != 1 {
		t.Fatalf("reuse repeated provider resolution or source collection: resolve=%d collect=%d hydrate=%d", resolver.calls, collector.collectCount(), collector.hydrateCount())
	}
	resolver.candidates = nil
	input.Force = true
	refreshed, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed.Release.ProviderMetadata.IdentityCandidates) != 0 {
		t.Fatal("fresh resolution revived stale candidates")
	}
	for _, diagnostic := range refreshed.Diagnostics {
		if diagnostic.Code == "external_identity_candidates" {
			t.Fatal("fresh resolution retained a stale candidate gate")
		}
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE external_metadata SET identity_candidates_json = '{' WHERE source_path = ?`, root); err != nil {
		t.Fatal(err)
	}
	input.Force = false
	if _, err := module.Prepare(t.Context(), input); err == nil {
		t.Fatal("malformed persisted candidates silently removed the selection gate")
	}
}

type candidateIdentityResolver struct {
	candidates []api.ExternalIdentityCandidate
	calls      int
}

func (r *candidateIdentityResolver) Resolve(ctx context.Context, request externalidentity.Request) (externalidentity.Result, error) {
	r.calls++
	result, err := (staticIdentityResolver{}).Resolve(ctx, request)
	result.Identity.TMDBID = 0
	result.ProviderMetadata.IdentityCandidates = r.candidates
	result.Diagnostics = externalidentity.CandidateDiagnostics(r.candidates)
	return result, err
}
