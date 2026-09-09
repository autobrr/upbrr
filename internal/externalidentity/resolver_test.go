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

func TestResolveDropsProviderMetadataConflictingWithExplicitCrossReference(t *testing.T) {
	t.Parallel()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	imdbID := 1234567
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
			return legacyEvidence{}, nil
		}),
		candidate: candidateSourceFunc(func(_ context.Context, request Request) (CandidateEvidence, error) {
			return CandidateEvidence{
				Identity: api.ExternalIdentity{
					SourcePath: request.SourcePath,
					TMDBID:     222333,
					IMDBID:     imdbID,
					TVmazeID:   444555,
				},
				Metadata: api.SourceScopedMetadata{
					SourcePath: request.SourcePath,
					TMDB: &api.TMDBMetadata{
						TMDBID: 222333,
						IMDBID: 7654321,
						Title:  "Conflicting TMDB",
					},
					TVmaze: &api.TVmazeMetadata{
						TVmazeID: 444555,
						IMDBID:   7654321,
						Name:     "Conflicting TVmaze",
					},
				},
			}, nil
		}),
		now: time.Now,
	}

	result, err := resolver.Resolve(context.Background(), Request{
		SourcePath:        sourcePath,
		SourceFingerprint: "source-fingerprint",
		Generation:        1,
		Intent: ResolutionIntent{ProviderOverrides: api.ExternalIDOverrides{
			IMDBID: &imdbID,
		}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Identity.TMDBID != 222333 || result.Identity.TVmazeID != 444555 || result.Identity.IMDBID != imdbID {
		t.Fatalf("identity = %#v", result.Identity)
	}
	if result.ProviderMetadata.TMDB != nil || result.ProviderMetadata.TVmaze != nil {
		t.Fatalf("conflicting metadata retained: %#v", result.ProviderMetadata)
	}
}

func TestResolveCrossReferenceMetadataHonorsStoredProvenancePins(t *testing.T) {
	for _, tc := range []struct {
		name               string
		provider           api.IdentityProvider
		provenance         api.IdentityProvenance
		wantTMDBMetadata   bool
		wantTVmazeMetadata bool
	}{
		{
			name:       "explicit IMDb provenance with unset override drops conflicting metadata",
			provider:   api.IdentityProviderIMDB,
			provenance: api.IdentityProvenanceExplicit,
		},
		{
			name:       "explicit TVDB provenance with unset override drops conflicting metadata",
			provider:   api.IdentityProviderTVDB,
			provenance: api.IdentityProvenanceExplicit,
		},
		{
			name:               "provider IMDb provenance retains conflicting metadata",
			provider:           api.IdentityProviderIMDB,
			provenance:         api.IdentityProvenanceProvider,
			wantTMDBMetadata:   true,
			wantTVmazeMetadata: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
			storedID := 1234567
			metadataIMDBID := 0
			metadataTVDBID := 0
			identity := api.ExternalIdentity{
				SourcePath: sourcePath,
				Overrides: api.IdentityOverrideState{
					IMDB: api.OverrideStateUnset,
					TVDB: api.OverrideStateUnset,
				},
			}
			if tc.provider == api.IdentityProviderIMDB {
				identity.IMDBID = storedID
				identity.Provenance.IMDB = tc.provenance
				metadataIMDBID = 9999999
			} else {
				identity.TVDBID = storedID
				identity.Provenance.TVDB = tc.provenance
				metadataTVDBID = 8888888
			}
			resolver := &Resolver{
				evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
					return legacyEvidence{
						identity: identity,
						hasIDs:   true,
					}, nil
				}),
				candidate: candidateSourceFunc(func(_ context.Context, request Request) (CandidateEvidence, error) {
					return CandidateEvidence{
						Identity: api.ExternalIdentity{
							SourcePath: request.SourcePath,
							TMDBID:     111222,
							TVmazeID:   333444,
						},
						Metadata: api.SourceScopedMetadata{
							SourcePath: request.SourcePath,
							TMDB: &api.TMDBMetadata{
								TMDBID: 111222,
								IMDBID: metadataIMDBID,
								TVDBID: metadataTVDBID,
							},
							TVmaze: &api.TVmazeMetadata{
								TVmazeID: 333444,
								IMDBID:   metadataIMDBID,
								TVDBID:   metadataTVDBID,
							},
						},
					}, nil
				}),
				now: time.Now,
			}

			result, err := resolver.Resolve(context.Background(), Request{
				SourcePath:        sourcePath,
				SourceFingerprint: "source-fingerprint",
				Generation:        1,
			})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if id, ok := result.Identity.ProviderID(tc.provider); !ok || id != storedID {
				t.Fatalf("stored %s identity = %#v", tc.provider, result.Identity)
			}
			if got := result.ProviderMetadata.TMDB != nil; got != tc.wantTMDBMetadata {
				t.Fatalf("TMDB metadata = %#v", result.ProviderMetadata.TMDB)
			}
			if got := result.ProviderMetadata.TVmaze != nil; got != tc.wantTVmazeMetadata {
				t.Fatalf("TVmaze metadata = %#v", result.ProviderMetadata.TVmaze)
			}
		})
	}
}

func TestResolveClearsStoredProviderGuessesFromAuthoritativeCandidate(t *testing.T) {
	t.Parallel()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	imdbID := 1234567
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
			return legacyEvidence{
				identity: api.ExternalIdentity{
					SourcePath: sourcePath,
					TMDBID:     999888,
					Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceProvider},
				},
				metadata: api.SourceScopedMetadata{
					SourcePath: sourcePath,
					TMDB:       &api.TMDBMetadata{TMDBID: 999888, Title: "Stale Provider Guess"},
				},
				hasIDs:  true,
				hasMeta: true,
			}, nil
		}),
		candidate: candidateSourceFunc(func(_ context.Context, request Request) (CandidateEvidence, error) {
			return CandidateEvidence{
				Identity: api.ExternalIdentity{
					SourcePath: request.SourcePath,
					IMDBID:     imdbID,
					Provenance: api.IdentityProvenanceSet{IMDB: api.IdentityProvenanceExplicit},
					Overrides:  api.IdentityOverrideState{IMDB: api.OverrideStateValue},
				},
				Metadata: api.SourceScopedMetadata{SourcePath: request.SourcePath},
			}, nil
		}),
		now: time.Now,
	}

	result, err := resolver.Resolve(context.Background(), Request{
		SourcePath:        sourcePath,
		SourceFingerprint: "source-fingerprint",
		Generation:        1,
		Intent: ResolutionIntent{ProviderOverrides: api.ExternalIDOverrides{
			IMDBID: &imdbID,
		}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Identity.IMDBID != imdbID || result.Identity.TMDBID != 0 || result.ProviderMetadata.TMDB != nil {
		t.Fatalf("stored provider guess restored: identity=%#v metadata=%#v", result.Identity, result.ProviderMetadata)
	}
}

func TestHasExplicitProviderCorrectionRecognizesZeroValuePins(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*api.ExternalIdentity)
		want bool
	}{
		{
			name: "TMDB value",
			set: func(identity *api.ExternalIdentity) {
				identity.TMDBID = 1
				identity.Provenance.TMDB = api.IdentityProvenanceExplicit
			},
			want: true,
		},
		{
			name: "IMDb",
			set: func(identity *api.ExternalIdentity) {
				identity.IMDBID = 1
				identity.Provenance.IMDB = api.IdentityProvenanceExplicit
			},
			want: true,
		},
		{
			name: "TVDB",
			set: func(identity *api.ExternalIdentity) {
				identity.TVDBID = 1
				identity.Provenance.TVDB = api.IdentityProvenanceExplicit
			},
			want: true,
		},
		{
			name: "TVmaze",
			set: func(identity *api.ExternalIdentity) {
				identity.TVmazeID = 1
				identity.Provenance.TVmaze = api.IdentityProvenanceExplicit
			},
			want: true,
		},
		{
			name: "MAL",
			set: func(identity *api.ExternalIdentity) {
				identity.MALID = 1
				identity.Provenance.MAL = api.IdentityProvenanceExplicit
			},
			want: true,
		},
		{
			name: "IMDb provenance clear",
			set: func(identity *api.ExternalIdentity) {
				identity.Provenance.IMDB = api.IdentityProvenanceExplicit
			},
			want: true,
		},
		{
			name: "TVDB override clear",
			set: func(identity *api.ExternalIdentity) {
				identity.Overrides.TVDB = api.OverrideStateClear
			},
			want: true,
		},
		{
			name: "inferred",
			set: func(identity *api.ExternalIdentity) {
				identity.IMDBID = 1
				identity.Provenance.IMDB = api.IdentityProvenanceProvider
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity := api.ExternalIdentity{}
			tc.set(&identity)
			if got := hasExplicitProviderCorrection(identity); got != tc.want {
				t.Fatalf("hasExplicitProviderCorrection() = %t, want %t for %#v", got, tc.want, identity)
			}
		})
	}
}

func TestResolveClearOnlyCandidateReplacesAutomaticEvidenceWithinSource(t *testing.T) {
	for _, tc := range []struct {
		name            string
		candidateSource func(string) string
		wantTMDBID      int
		wantTVDBID      int
		wantMetadata    bool
	}{
		{
			name:            "same source clears automatic IDs and metadata",
			candidateSource: func(sourcePath string) string { return sourcePath },
			wantMetadata:    false,
		},
		{
			name: "other source is ignored",
			candidateSource: func(sourcePath string) string {
				return filepath.Join(filepath.Dir(sourcePath), "Other.Release.2026.1080p-GRP.mkv")
			},
			wantTMDBID:   100,
			wantTVDBID:   200,
			wantMetadata: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
			resolver := &Resolver{
				evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
					return legacyEvidence{
						identity: api.ExternalIdentity{
							SourcePath: sourcePath,
							TMDBID:     100,
							IMDBID:     300,
							TVDBID:     200,
							Provenance: api.IdentityProvenanceSet{
								TMDB: api.IdentityProvenanceProvider,
								IMDB: api.IdentityProvenanceExplicit,
								TVDB: api.IdentityProvenanceProvider,
							},
							Overrides: api.IdentityOverrideState{IMDB: api.OverrideStateValue},
						},
						metadata: api.SourceScopedMetadata{
							SourcePath: sourcePath,
							TMDB:       &api.TMDBMetadata{TMDBID: 100, Title: "Stale TMDB"},
							TVDB:       &api.TVDBMetadata{TVDBID: 200, Name: "Stale TVDB"},
						},
						hasIDs:  true,
						hasMeta: true,
					}, nil
				}),
				candidate: candidateSourceFunc(func(_ context.Context, request Request) (CandidateEvidence, error) {
					candidateSource := tc.candidateSource(request.SourcePath)
					return CandidateEvidence{
						Identity: api.ExternalIdentity{
							SourcePath: candidateSource,
							Overrides:  api.IdentityOverrideState{TMDB: api.OverrideStateClear},
						},
						Metadata: api.SourceScopedMetadata{SourcePath: candidateSource},
					}, nil
				}),
				now: time.Now,
			}

			result, err := resolver.Resolve(context.Background(), Request{
				SourcePath:        sourcePath,
				SourceFingerprint: "source-fingerprint",
				Generation:        1,
			})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if result.Identity.TMDBID != tc.wantTMDBID || result.Identity.TVDBID != tc.wantTVDBID ||
				result.Identity.IMDBID != 300 || result.Identity.Overrides.IMDB != api.OverrideStateValue {
				t.Fatalf("identity = %#v", result.Identity)
			}
			if got := hasCandidateMetadata(result.ProviderMetadata); got != tc.wantMetadata {
				t.Fatalf("provider metadata = %#v", result.ProviderMetadata)
			}
		})
	}
}

func TestResolvePropagatesCandidateIdentityDependenciesWithinSource(t *testing.T) {
	for _, tc := range []struct {
		name            string
		candidateSource func(string) string
		wantTVDBID      int
		wantTVDB        api.IdentityDependency
	}{
		{
			name:            "same source replaces TVDB dependency",
			candidateSource: func(sourcePath string) string { return sourcePath },
			wantTVDBID:      300,
			wantTVDB:        api.IdentityDependency{ID: 300, IMDBID: 200},
		},
		{
			name: "other source retains stored dependency",
			candidateSource: func(sourcePath string) string {
				return filepath.Join(filepath.Dir(sourcePath), "Other.Release.2026.1080p-GRP.mkv")
			},
			wantTVDBID: 100,
			wantTVDB:   api.IdentityDependency{ID: 100, IMDBID: 200},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
			resolver := &Resolver{
				evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
					return legacyEvidence{identity: api.ExternalIdentity{
						SourcePath: sourcePath,
						IMDBID:     200,
						TVDBID:     100,
						Dependencies: api.IdentityDependencySet{
							IMDB: api.IdentityDependency{ID: 200},
							TVDB: api.IdentityDependency{ID: 100, IMDBID: 200},
						},
					}, hasIDs: true}, nil
				}),
				candidate: candidateSourceFunc(func(_ context.Context, request Request) (CandidateEvidence, error) {
					return CandidateEvidence{Identity: api.ExternalIdentity{
						SourcePath: tc.candidateSource(request.SourcePath),
						TVDBID:     300,
						Dependencies: api.IdentityDependencySet{
							TVDB: api.IdentityDependency{ID: 300, IMDBID: 200},
						},
					}}, nil
				}),
				now: time.Now,
			}

			result, err := resolver.Resolve(context.Background(), Request{
				SourcePath:        sourcePath,
				SourceFingerprint: "source-fingerprint",
				Generation:        1,
			})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if result.Identity.TVDBID != tc.wantTVDBID || result.Identity.Dependencies.TVDB != tc.wantTVDB ||
				result.Identity.Dependencies.IMDB != (api.IdentityDependency{ID: 200}) {
				t.Fatalf("identity = %#v", result.Identity)
			}
		})
	}
}

func TestResolveClearOnlyCandidateDropsTargetDependency(t *testing.T) {
	t.Parallel()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
			return legacyEvidence{identity: api.ExternalIdentity{
				SourcePath: sourcePath,
				TMDBID:     100,
				IMDBID:     200,
				Provenance: api.IdentityProvenanceSet{IMDB: api.IdentityProvenanceExplicit},
				Overrides:  api.IdentityOverrideState{IMDB: api.OverrideStateValue},
				Dependencies: api.IdentityDependencySet{
					TMDB: api.IdentityDependency{ID: 100, IMDBID: 200},
					IMDB: api.IdentityDependency{ID: 200},
				},
			}, hasIDs: true}, nil
		}),
		candidate: candidateSourceFunc(func(_ context.Context, request Request) (CandidateEvidence, error) {
			return CandidateEvidence{Identity: api.ExternalIdentity{
				SourcePath: request.SourcePath,
				Overrides:  api.IdentityOverrideState{TMDB: api.OverrideStateClear},
			}}, nil
		}),
		now: time.Now,
	}

	result, err := resolver.Resolve(context.Background(), Request{
		SourcePath:        sourcePath,
		SourceFingerprint: "source-fingerprint",
		Generation:        1,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Identity.TMDBID != 0 || result.Identity.Dependencies.TMDB != (api.IdentityDependency{}) ||
		result.Identity.IMDBID != 200 || result.Identity.Dependencies.IMDB != (api.IdentityDependency{ID: 200}) {
		t.Fatalf("identity = %#v", result.Identity)
	}
}

func TestResolveAuthoritativeCandidatePreservesOrReplacesStoredExplicitSibling(t *testing.T) {
	for _, tc := range []struct {
		name             string
		currentIMDBID    *int
		candidateIMDBID  int
		candidateIMDBRef int
		candidateState   api.OverrideState
		wantIMDBID       int
		wantState        api.OverrideState
		wantTMDBMetadata bool
	}{
		{
			name:             "omitted sibling preserves stored pin and rejects conflicting metadata",
			candidateIMDBID:  7654321,
			candidateIMDBRef: 7654321,
			wantIMDBID:       1234567,
			wantState:        api.OverrideStateValue,
		},
		{
			name:             "explicit clear replaces stored pin",
			currentIMDBID:    new(0),
			candidateIMDBRef: 7654321,
			candidateState:   api.OverrideStateClear,
			wantState:        api.OverrideStateClear,
			wantTMDBMetadata: true,
		},
		{
			name:             "explicit replacement replaces stored pin",
			currentIMDBID:    new(7777777),
			candidateIMDBID:  7777777,
			candidateIMDBRef: 7777777,
			candidateState:   api.OverrideStateValue,
			wantIMDBID:       7777777,
			wantState:        api.OverrideStateValue,
			wantTMDBMetadata: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
			tmdbID := 999888
			resolver := &Resolver{
				evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
					return legacyEvidence{
						identity: api.ExternalIdentity{
							SourcePath: sourcePath,
							IMDBID:     1234567,
							Provenance: api.IdentityProvenanceSet{IMDB: api.IdentityProvenanceExplicit},
							Overrides:  api.IdentityOverrideState{IMDB: api.OverrideStateValue},
						},
						hasIDs: true,
					}, nil
				}),
				candidate: candidateSourceFunc(func(_ context.Context, request Request) (CandidateEvidence, error) {
					return CandidateEvidence{
						Identity: api.ExternalIdentity{
							SourcePath: request.SourcePath,
							TMDBID:     tmdbID,
							IMDBID:     tc.candidateIMDBID,
							Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceExplicit, IMDB: api.IdentityProvenanceProvider},
							Overrides:  api.IdentityOverrideState{TMDB: api.OverrideStateValue, IMDB: tc.candidateState},
						},
						Metadata: api.SourceScopedMetadata{
							SourcePath: request.SourcePath,
							TMDB: &api.TMDBMetadata{
								TMDBID: tmdbID,
								IMDBID: tc.candidateIMDBRef,
								Title:  "Candidate TMDB",
							},
						},
					}, nil
				}),
				now: time.Now,
			}

			result, err := resolver.Resolve(context.Background(), Request{
				SourcePath:        sourcePath,
				SourceFingerprint: "source-fingerprint",
				Generation:        1,
				Intent: ResolutionIntent{ProviderOverrides: api.ExternalIDOverrides{
					TMDBID: &tmdbID,
					IMDBID: tc.currentIMDBID,
				}},
			})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if result.Identity.TMDBID != tmdbID || result.Identity.IMDBID != tc.wantIMDBID || result.Identity.Overrides.IMDB != tc.wantState {
				t.Fatalf("identity = %#v", result.Identity)
			}
			if got := result.ProviderMetadata.TMDB != nil; got != tc.wantTMDBMetadata {
				t.Fatalf("TMDB metadata = %#v", result.ProviderMetadata.TMDB)
			}
		})
	}
}

func TestResolveResetStoredIdentityPinRefreshesOnlyResetProvider(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	resetFields := []api.CorrectionField{api.CorrectionFieldIdentityTMDB}
	stored := api.ExternalIdentity{
		SourcePath: sourcePath,
		TMDBID:     100,
		IMDBID:     200,
		Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceExplicit, IMDB: api.IdentityProvenanceExplicit},
		Overrides:  api.IdentityOverrideState{TMDB: api.OverrideStateValue, IMDB: api.OverrideStateValue},
	}
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
			return legacyEvidence{identity: stored, hasIDs: true}, nil
		}),
		candidate: candidateSourceFunc(func(_ context.Context, request Request) (CandidateEvidence, error) {
			return CandidateEvidence{Identity: api.ExternalIdentity{
				SourcePath: request.SourcePath,
				TMDBID:     300,
				IMDBID:     400,
				Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceExplicit, IMDB: api.IdentityProvenanceProvider},
				Overrides:  api.IdentityOverrideState{TMDB: api.OverrideStateValue},
			}}, nil
		}),
		now: time.Now,
	}

	result, err := resolver.Resolve(context.Background(), Request{
		SourcePath:        sourcePath,
		SourceFingerprint: "source-fingerprint",
		Generation:        1,
		Intent:            ResolutionIntent{IdentityResetFields: resetFields},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Identity.TMDBID != 300 || result.Identity.Provenance.TMDB != api.IdentityProvenanceExplicit ||
		result.Identity.Overrides.TMDB != api.OverrideStateValue || result.Identity.IMDBID != 200 ||
		result.Identity.Provenance.IMDB != api.IdentityProvenanceExplicit || result.Identity.Overrides.IMDB != api.OverrideStateValue {
		t.Fatalf("identity = %#v", result.Identity)
	}
	if stored.TMDBID != 100 || stored.Provenance.TMDB != api.IdentityProvenanceExplicit || stored.Overrides.TMDB != api.OverrideStateValue {
		t.Fatalf("stored evidence mutated: %#v", stored)
	}
	if len(resetFields) != 1 || resetFields[0] != api.CorrectionFieldIdentityTMDB {
		t.Fatalf("reset fields mutated: %#v", resetFields)
	}
}

func TestResolveResetStoredExplicitClearRefreshesCrossReferenceMetadata(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
			return legacyEvidence{identity: api.ExternalIdentity{
				SourcePath: sourcePath,
				Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceExplicit},
				Overrides:  api.IdentityOverrideState{TMDB: api.OverrideStateClear},
			}, hasIDs: true}, nil
		}),
		candidate: candidateSourceFunc(func(_ context.Context, request Request) (CandidateEvidence, error) {
			return CandidateEvidence{
				Identity: api.ExternalIdentity{
					SourcePath: request.SourcePath,
					TMDBID:     300,
					IMDBID:     400,
					Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceProvider, IMDB: api.IdentityProvenanceExplicit},
					Overrides:  api.IdentityOverrideState{IMDB: api.OverrideStateValue},
				},
				Metadata: api.SourceScopedMetadata{
					SourcePath: request.SourcePath,
					TMDB:       &api.TMDBMetadata{
TMDBID: 300,
 IMDBID: 400,
 Title: "Refreshed candidate",
},
				},
			}, nil
		}),
		now: time.Now,
	}

	result, err := resolver.Resolve(context.Background(), Request{
		SourcePath:        sourcePath,
		SourceFingerprint: "source-fingerprint",
		Generation:        1,
		Intent: ResolutionIntent{IdentityResetFields: []api.CorrectionField{
			api.CorrectionFieldIdentityTMDB,
		}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Identity.TMDBID != 300 || result.Identity.Provenance.TMDB != api.IdentityProvenanceProvider ||
		result.Identity.Overrides.TMDB != api.OverrideStateUnset || result.ProviderMetadata.TMDB == nil ||
		result.ProviderMetadata.TMDB.IMDBID != 400 {
		t.Fatalf("refreshed result = %#v", result)
	}
}

func TestResolveResetKeepsAutomaticStoredEvidenceAndChangesIntentFingerprint(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	resolver := &Resolver{
		evidence: evidenceLoaderFunc(func(context.Context, string) (legacyEvidence, error) {
			return legacyEvidence{identity: api.ExternalIdentity{
				SourcePath: sourcePath,
				TMDBID:     100,
				Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceProvider},
			}, hasIDs: true}, nil
		}),
		now: time.Now,
	}
	request := Request{
SourcePath: sourcePath,
 SourceFingerprint: "source-fingerprint",
 Generation: 1,
}

	baseline, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatalf("resolve baseline: %v", err)
	}
	request.Intent.IdentityResetFields = []api.CorrectionField{api.CorrectionFieldIdentityTMDB}
	result, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatalf("resolve reset: %v", err)
	}
	if result.Identity.TMDBID != 100 || result.Identity.Provenance.TMDB != api.IdentityProvenanceProvider ||
		result.Identity.Overrides.TMDB != api.OverrideStateUnset {
		t.Fatalf("automatic stored evidence = %#v", result.Identity)
	}
	if result.Identity.Resolution.IntentFingerprint == baseline.Identity.Resolution.IntentFingerprint {
		t.Fatalf("reset intent did not change lineage fingerprint: %#v", result.Identity.Resolution)
	}
}

func TestNormalizeIdentityLineageFillsOnlyEmptyValues(t *testing.T) {
	t.Parallel()

	identity := api.ExternalIdentity{
		Provenance: api.IdentityProvenanceSet{
			IMDB:     api.IdentityProvenanceExplicit,
			TVDB:     api.IdentityProvenanceProvider,
			Category: api.IdentityProvenanceProvider,
		},
		Overrides: api.IdentityOverrideState{
			IMDB:     api.OverrideStateValue,
			TVDB:     api.OverrideStateClear,
			Category: api.OverrideStateValue,
		},
	}

	normalizeIdentityLineage(&identity)
	if identity.Provenance.TMDB != api.IdentityProvenanceUnknown || identity.Provenance.TVmaze != api.IdentityProvenanceUnknown ||
		identity.Provenance.MAL != api.IdentityProvenanceUnknown || identity.Overrides.TMDB != api.OverrideStateUnset ||
		identity.Overrides.TVmaze != api.OverrideStateUnset || identity.Overrides.MAL != api.OverrideStateUnset {
		t.Fatalf("empty lineage = %#v", identity)
	}
	if identity.Provenance.IMDB != api.IdentityProvenanceExplicit || identity.Provenance.TVDB != api.IdentityProvenanceProvider ||
		identity.Provenance.Category != api.IdentityProvenanceProvider || identity.Overrides.IMDB != api.OverrideStateValue ||
		identity.Overrides.TVDB != api.OverrideStateClear || identity.Overrides.Category != api.OverrideStateValue {
		t.Fatalf("non-empty lineage changed: %#v", identity)
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
	if identity.Dependencies.TMDB != (api.IdentityDependency{}) || identity.Dependencies.IMDB != (api.IdentityDependency{ID: overrideIMDB}) {
		t.Fatalf("dependencies = %#v", identity.Dependencies)
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
		SourcePath:        sourcePath,
		SourceFingerprint: "source-fingerprint",
		Generation:        1,
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
		case <-time.After(10 * time.Second):
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
	deadline := time.Now().Add(10 * time.Second)
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
