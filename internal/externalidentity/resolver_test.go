// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package externalidentity

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type evidenceLoaderFunc func(context.Context, string) (legacyEvidence, error)

func (f evidenceLoaderFunc) load(ctx context.Context, sourcePath string) (legacyEvidence, error) {
	return f(ctx, sourcePath)
}

type candidateSourceFunc func(context.Context, Request) (CandidateEvidence, error)

func (f candidateSourceFunc) ResolveIdentityCandidate(ctx context.Context, request Request) (CandidateEvidence, error) {
	return f(ctx, request)
}

func TestResolvePromotesProviderEvidenceAndKeepsCandidateListsDiagnostic(t *testing.T) {
	t.Parallel()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
			return legacyEvidence{}, nil
		}),
		candidate: candidateSourceFunc(func(_ context.Context, request Request) (CandidateEvidence, error) {
			return CandidateEvidence{
				Identity: api.ExternalIdentity{
					SourcePath: request.SourcePath,
					TMDBID:     1234567,
					Category:   api.CanonicalCategoryMovie,
					Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceProvider, Category: api.IdentityProvenanceProvider},
				},
				Metadata: api.SourceScopedMetadata{
					SourcePath: request.SourcePath,
					TMDB:       &api.TMDBMetadata{TMDBID: 1234567, Title: "Example Release"},
				},
				Candidates: []api.ExternalIdentityCandidate{{
					Provider: api.IdentityProviderTMDB,
					ID:       7654321,
					Title:    "Example Candidate",
					Category: api.CanonicalCategoryMovie,
				}},
			}, nil
		}),
		now: time.Now,
	}

	result, err := resolver.Resolve(context.Background(), Request{
		SourcePath:        sourcePath,
		SourceFingerprint: "source-fingerprint",
		Generation:        3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Identity.TMDBID != 1234567 || result.Identity.Category != api.CanonicalCategoryMovie ||
		result.Identity.Provenance.TMDB != api.IdentityProvenanceProvider {
		t.Fatalf("identity = %#v", result.Identity)
	}
	if result.ProviderMetadata.TMDB == nil || result.ProviderMetadata.TMDB.TMDBID != 1234567 {
		t.Fatalf("provider metadata = %#v", result.ProviderMetadata)
	}
	if len(result.Diagnostics) != 1 || len(result.Diagnostics[0].Candidates) != 1 || result.Diagnostics[0].Candidates[0].ID != 7654321 {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	if result.Identity.TMDBID == result.Diagnostics[0].Candidates[0].ID {
		t.Fatal("diagnostic candidate became canonical identity")
	}
}

func TestResolveAppliesTriStateIntentWithoutPersisting(t *testing.T) {
	repoPath := filepath.Join(t.TempDir(), "external-identity.db")
	repo, err := db.OpenWithLogger(repoPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	stored := api.ExternalIdentity{
		SourcePath: sourcePath,
		TMDBID:     100,
		IMDBID:     200,
		Category:   api.CanonicalCategoryMovie,
		Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceTracker, IMDB: api.IdentityProvenanceProvider},
	}
	if err := repo.SaveExternalIdentity(context.Background(), stored); err != nil {
		t.Fatalf("save stored IDs: %v", err)
	}
	if err := repo.SaveExternalMetadata(context.Background(), api.SourceScopedMetadata{
		SourcePath: sourcePath,
		TMDB:       &api.TMDBMetadata{TMDBID: 100, Title: "Example Release 2026"},
		IMDB:       &api.IMDBMetadata{IMDBID: 200, Title: "Example Release 2026"},
	}); err != nil {
		t.Fatalf("save stored metadata: %v", err)
	}
	resolver, err := New(repo)
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	fixedNow := time.Date(2026, time.July, 14, 2, 3, 4, 0, time.UTC)
	resolver.now = func() time.Time { return fixedNow }
	clearTMDB := 0
	overrideIMDB := 300
	category := api.CanonicalCategoryTV

	result, err := resolver.Resolve(context.Background(), Request{
		SourcePath:        sourcePath,
		SourceFingerprint: "source-fingerprint",
		Generation:        5,
		Intent: ResolutionIntent{
			ProviderOverrides: api.ExternalIDOverrides{TMDBID: &clearTMDB, IMDBID: &overrideIMDB},
			CategoryOverride:  &category,
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	identity := result.Identity
	if identity.TMDBID != 0 || identity.IMDBID != 300 || identity.Category != api.CanonicalCategoryTV {
		t.Fatalf("identity = %#v", identity)
	}
	if identity.Overrides.TMDB != api.OverrideStateClear || identity.Overrides.IMDB != api.OverrideStateValue ||
		identity.Overrides.Category != api.OverrideStateValue {
		t.Fatalf("override state = %#v", identity.Overrides)
	}
	if identity.Provenance.TMDB != api.IdentityProvenanceExplicit || identity.Provenance.IMDB != api.IdentityProvenanceExplicit ||
		identity.Provenance.Category != api.IdentityProvenanceExplicit {
		t.Fatalf("provenance = %#v", identity.Provenance)
	}
	if result.ProviderMetadata.TMDB != nil || result.ProviderMetadata.IMDB != nil {
		t.Fatalf("mismatched provider metadata was retained: %#v", result.ProviderMetadata)
	}
	if identity.Resolution.ContractVersion != ContractVersion || identity.Resolution.IntentFingerprint == "" || identity.ResolvedAt != fixedNow {
		t.Fatalf("resolution lineage = %#v", identity.Resolution)
	}
	if len(result.EvidenceFingerprints) != 2 || len(result.Diagnostics) != 1 {
		t.Fatalf("evidence result = %#v", result)
	}

	loaded, err := repo.GetExternalIdentity(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("reload stored IDs: %v", err)
	}
	if loaded.TMDBID != stored.TMDBID || loaded.IMDBID != stored.IMDBID || loaded.Category != stored.Category {
		t.Fatalf("resolver persisted candidate identity: %#v", loaded)
	}
}

func TestResolveReturnsPartialIdentityAndTypedMissingRequirements(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.WEB-GRP.mkv")
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
			return legacyEvidence{}, nil
		}),
		now: func() time.Time { return time.Date(2026, time.July, 14, 0, 0, 0, 0, time.UTC) },
	}

	result, err := resolver.Resolve(context.Background(), Request{
		SourcePath:        sourcePath,
		SourceFingerprint: "source-fingerprint",
		Generation:        1,
	})
	if err != nil {
		t.Fatalf("resolve partial identity: %v", err)
	}
	if result.Identity.Category != api.CanonicalCategoryUnknown || result.Identity.Conflict != api.IdentityConflictNone {
		t.Fatalf("partial identity = %#v", result.Identity)
	}
	if len(result.MissingRequirements) != 6 {
		t.Fatalf("missing requirements = %#v", result.MissingRequirements)
	}
	if result.ProviderMetadata.SourcePath != sourcePath || result.ProviderMetadata.Generation != 1 {
		t.Fatalf("source-scoped metadata = %#v", result.ProviderMetadata)
	}
}

func TestResolveCancellationDoesNotReorderSourceWaiters(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.BluRay-GRP")
	normalized, err := normalizeSourcePath(sourcePath)
	if err != nil {
		t.Fatalf("normalize source: %v", err)
	}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var mu sync.Mutex
	calls := make([]string, 0, 2)
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(_ context.Context, path string) (legacyEvidence, error) {
			mu.Lock()
			calls = append(calls, path)
			call := len(calls)
			mu.Unlock()
			if call == 1 {
				close(firstEntered)
				<-releaseFirst
			}
			return legacyEvidence{}, nil
		}),
		now: time.Now,
	}
	request := Request{
SourcePath: sourcePath,
 SourceFingerprint: "source-fingerprint",
 Generation: 1,
}
	errs := make(chan error, 3)
	go func() {
		_, err := resolver.Resolve(context.Background(), request)
		errs <- err
	}()
	<-firstEntered

	canceledCtx, cancel := context.WithCancel(context.Background())
	go func() {
		_, err := resolver.Resolve(canceledCtx, request)
		errs <- err
	}()
	waitForSourceWaiters(t, &resolver.gates, normalized, 1)
	go func() {
		_, err := resolver.Resolve(context.Background(), request)
		errs <- err
	}()
	waitForSourceWaiters(t, &resolver.gates, normalized, 2)
	cancel()
	waitForSourceWaiters(t, &resolver.gates, normalized, 1)
	close(releaseFirst)

	var canceled bool
	for range 3 {
		err := <-errs
		if errors.Is(err, context.Canceled) {
			canceled = true
			continue
		}
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
	if !canceled {
		t.Fatal("canceled waiter returned no cancellation error")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0] != normalized || calls[1] != normalized {
		t.Fatalf("evidence calls = %v", calls)
	}
}

func TestResolveKeepsDifferentSourcesConcurrent(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(_ context.Context, path string) (legacyEvidence, error) {
			entered <- path
			<-release
			return legacyEvidence{}, nil
		}),
		now: time.Now,
	}
	requests := []Request{
		{
			SourcePath:        filepath.Join(t.TempDir(), "Example.Release.2026.A-GRP.mkv"),
			SourceFingerprint: "source-a",
			Generation:        1,
		},
		{
			SourcePath:        filepath.Join(t.TempDir(), "Example.Release.2026.B-GRP.mkv"),
			SourceFingerprint: "source-b",
			Generation:        1,
		},
	}
	errs := make(chan error, len(requests))
	for _, request := range requests {
		go func() {
			_, err := resolver.Resolve(context.Background(), request)
			errs <- err
		}()
	}
	for range requests {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("different source did not enter resolution concurrently")
		}
	}
	close(release)
	for range requests {
		if err := <-errs; err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
}

func waitForSourceWaiters(t *testing.T, gates *sourceGates, sourcePath string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		gates.mu.Lock()
		gate := gates.gates[sourcePath]
		got := 0
		if gate != nil {
			got = len(gate.waiters)
		}
		gates.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("source waiters did not reach %d", want)
}
