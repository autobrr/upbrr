// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package externalidentity

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveObsoleteAutomaticIDsCannotSurviveEmptyCandidate(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Example.Movie.2026-GRP.mkv")
	stored := api.ExternalIdentity{
		SourcePath: source,
		IMDBID:     1,
		TMDBID:     22,
		TVDBID:     33,
		Category:   api.CanonicalCategoryMovie,
		Provenance: api.IdentityProvenanceSet{
			IMDB:     api.IdentityProvenanceTracker,
			TMDB:     api.IdentityProvenanceProvider,
			TVDB:     api.IdentityProvenanceExplicit,
			TVmaze:   api.IdentityProvenanceExplicit,
			Category: api.IdentityProvenanceExplicit,
		},
		Overrides: api.IdentityOverrideState{
			TVDB:     api.OverrideStateValue,
			TVmaze:   api.OverrideStateClear,
			Category: api.OverrideStateValue,
		},
		Dependencies: api.IdentityDependencySet{TMDB: api.IdentityDependency{ID: 22, IMDBID: 1}},
		Resolution:   api.IdentityResolutionKey{ContractVersion: "external-identity-v3", SourceFingerprint: "source-fingerprint"},
	}
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
			return legacyEvidence{
				identity: stored,
				hasIDs:   true,
				hasMeta:  true,
				metadata: api.SourceScopedMetadata{
					SourcePath: source,
					IMDB:       &api.IMDBMetadata{IMDBID: 1, Title: "Obsolete title"},
					TMDB:       &api.TMDBMetadata{TMDBID: 22, Title: "Previous title"},
				},
			}, nil
		}),
		now: time.Now,
	}
	request := Request{
		SourcePath:        source,
		SourceFingerprint: "source-fingerprint",
		Generation:        2,
	}
	for _, imdbID := range []int{0, 12345678} {
		resolver.candidate = candidateSourceFunc(func(context.Context, Request) (CandidateEvidence, error) {
			return CandidateEvidence{Identity: api.ExternalIdentity{SourcePath: source, IMDBID: imdbID}}, nil
		})
		got, err := resolver.Resolve(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if got.Identity.IMDBID != imdbID || got.Identity.TMDBID != 0 || got.Identity.Dependencies.TMDB != (api.IdentityDependency{}) ||
			got.Identity.TVDBID != 33 || got.Identity.TVmazeID != 0 || got.Identity.Overrides.TVmaze != api.OverrideStateClear ||
			got.Identity.Category != api.CanonicalCategoryMovie || got.ProviderMetadata.IMDB != nil {
			t.Fatalf("candidate IMDb=%d resurrected obsolete identity or lost pins: %+v", imdbID, got)
		}
	}
	// A current, valid small automatic ID is reusable; the migration is not an ID blacklist.
	stored.Resolution.ContractVersion = ContractVersion
	resolver.candidate = nil
	got, err := resolver.Resolve(t.Context(), request)
	if err != nil || got.Identity.IMDBID != 1 || got.Identity.TMDBID != 22 || got.ProviderMetadata.IMDB == nil {
		t.Fatalf("current ID 1 was discarded: %+v, %v", got, err)
	}
	for _, prior := range []string{"", "replaced-source"} {
		stored.Resolution.SourceFingerprint = prior
		request.Intent.ProviderOverrides.KeepTMDB = new(true)
		got, err := resolver.Resolve(t.Context(), request)
		if err != nil || got.Identity.IMDBID != 0 || got.Identity.TMDBID != 22 || got.Identity.Provenance.TMDB != api.IdentityProvenanceRetained ||
			got.Identity.Dependencies.TMDB != stored.Dependencies.TMDB || got.ProviderMetadata.IMDB != nil || got.ProviderMetadata.TMDB != nil ||
			got.Identity.TVDBID != 33 || got.Identity.Overrides.TVmaze != api.OverrideStateClear || got.Identity.Category != api.CanonicalCategoryMovie {
			t.Fatalf("source %q did not isolate automatic evidence from user choices: %+v, %v", prior, got, err)
		}
	}
	stored.Resolution.SourceFingerprint = request.SourceFingerprint
	stored.Generation = 1
	got, err = resolver.Resolve(t.Context(), request)
	if err != nil || got.Identity.TMDBID != 22 || got.ProviderMetadata.TMDB != nil {
		t.Fatalf("metadata from a different generation was admitted: %+v, %v", got, err)
	}
}

func TestResolveMigratesObsoleteAKAForManualIMDbID(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "Example.Movie.2026-GRP.mkv")
	for _, version := range []string{"external-identity-v18", ContractVersion} {
		stored := api.ExternalIdentity{
			SourcePath: source,
			IMDBID:     200,
			Overrides:  api.IdentityOverrideState{IMDB: api.OverrideStateValue},
			Resolution: api.IdentityResolutionKey{ContractVersion: version, SourceFingerprint: "source-fingerprint"},
		}
		resolver := &Resolver{
			evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
				return legacyEvidence{
					identity: stored,
					metadata: api.SourceScopedMetadata{
						SourcePath: source,
						IMDB: &api.IMDBMetadata{
							IMDBID: 200,
							Title:  "Example Movie",
							AKA:    "Other Title",
						},
					},
					hasIDs:  true,
					hasMeta: true,
				}, nil
			}),
			now: time.Now,
		}
		request := Request{
			SourcePath:        source,
			SourceFingerprint: "source-fingerprint",
			Generation:        1,
		}
		got, err := resolver.Resolve(t.Context(), request)
		imdb := got.ProviderMetadata.IMDB
		wantAKA := ""
		if version == ContractVersion {
			wantAKA = "Other Title"
		}
		if err != nil || got.Identity.IMDBID != 200 || imdb == nil || imdb.Title != "Example Movie" ||
			imdb.AKA != wantAKA {
			t.Fatalf("version %s: resolved IMDb %d with metadata %+v, error %v", version, got.Identity.IMDBID, imdb, err)
		}
	}
}

func TestResolveTurningKeepOffAfterSourceReplacement(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "Example.Movie.2026-GRP.mkv")
	stored := api.ExternalIdentity{
		SourcePath:   source,
		TMDBID:       22,
		Dependencies: api.IdentityDependencySet{TMDB: api.IdentityDependency{ID: 22}},
		Resolution:   api.IdentityResolutionKey{ContractVersion: ContractVersion, SourceFingerprint: "first-source"},
	}
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
			return legacyEvidence{identity: stored, hasIDs: true}, nil
		}),
		now: time.Now,
	}
	request := Request{
		SourcePath:        source,
		SourceFingerprint: "replacement-source",
		Generation:        2,
		Intent:            ResolutionIntent{ProviderOverrides: api.ExternalIDOverrides{KeepTMDB: new(true)}},
	}
	got, err := resolver.Resolve(t.Context(), request)
	if err != nil || got.Identity.TMDBID != 22 || got.Identity.Provenance.TMDB != api.IdentityProvenanceRetained {
		t.Fatalf("Keep did not carry TMDB to replacement: %+v, %v", got.Identity, err)
	}
	stored = got.Identity
	request.Intent.ProviderOverrides.KeepTMDB = new(false)
	request.Generation++
	for _, fingerprint := range []string{"replacement-source", "another-source"} {
		request.SourceFingerprint = fingerprint
		got, err = resolver.Resolve(t.Context(), request)
		if err != nil || got.Identity.TMDBID != 0 || got.Identity.Provenance.TMDB != api.IdentityProvenanceUnknown ||
			got.Identity.Dependencies.TMDB != (api.IdentityDependency{}) {
			t.Fatalf("Keep off retained the carried match for %q: %+v, %v", fingerprint, got.Identity, err)
		}
	}
}
