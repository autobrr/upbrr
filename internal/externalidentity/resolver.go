// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package externalidentity resolves one canonical, source-scoped external
// identity without persisting it. Prepared-release publication owns the commit.
package externalidentity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// ContractVersion changes whenever identity resolution semantics become
// incompatible with a previously resolved identity.
const ContractVersion = "external-identity-v1"

// Request contains normalized source scope and identity-resolution intent. It
// carries no gathered MediaInfo, scene, Arr, tracker, or provider evidence.
type Request struct {
	SourcePath        string
	SourceFingerprint string
	Generation        api.PreparedGeneration
	Intent            ResolutionIntent
}

// ResolutionIntent contains caller instructions that affect canonical identity
// resolution and its lineage fingerprint. Provider override pointers use nil
// to preserve evidence, non-positive values to clear an ID, and positive values
// to set one. CategoryOverride similarly preserves on nil, clears on empty or
// unknown, and accepts movie or TV as explicit values.
type ResolutionIntent struct {
	Title                   string
	Year                    int
	Season                  int
	Episode                 int
	TrackerContext          []string
	ProviderOverrides       api.ExternalIDOverrides
	CategoryOverride        *api.CanonicalCategory
	ReResolve               bool
	RefreshProviderMetadata bool
}

// EvidenceFingerprint identifies one non-secret evidence snapshot considered
// during resolution. Digest is a lowercase hexadecimal SHA-256 hash.
type EvidenceFingerprint struct {
	Kind   string
	Digest string
}

// Result contains the canonical identity candidate, source-scoped provider
// metadata, diagnostics, typed missing requirements, and evidence lineage.
// The prepared-release module decides whether and how to commit it.
type Result struct {
	Identity             api.ExternalIdentity
	ProviderMetadata     api.SourceScopedMetadata
	Diagnostics          []api.PreparationDiagnostic
	MissingRequirements  []api.MissingRequirementError
	EvidenceFingerprints []EvidenceFingerprint
}

// CandidateEvidence is temporary provider-resolution evidence supplied by a
// private adapter. Candidate lists remain diagnostics and never become
// canonical identity facts by themselves.
type CandidateEvidence struct {
	Identity   api.ExternalIdentity
	Metadata   api.SourceScopedMetadata
	Candidates []api.ExternalIdentityCandidate
}

// CandidateSource supplies provider evidence without persistence. It exists
// at the resolver boundary so provider clients remain private implementation
// details rather than request fields.
type CandidateSource interface {
	ResolveIdentityCandidate(context.Context, Request) (CandidateEvidence, error)
}

// Clone returns a detached result projection.
func (r Result) Clone() (Result, error) {
	var cloned Result
	payload, err := json.Marshal(r)
	if err != nil {
		return Result{}, fmt.Errorf("external identity: clone result: marshal: %w", err)
	}
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return Result{}, fmt.Errorf("external identity: clone result: unmarshal: %w", err)
	}
	return cloned, nil
}

type legacyEvidence struct {
	identity api.ExternalIdentity
	metadata api.SourceScopedMetadata
	hasIDs   bool
	hasMeta  bool
}

type evidenceLoader interface {
	load(context.Context, string) (legacyEvidence, error)
}

type repositoryEvidenceLoader struct {
	repo api.ReleaseStateRepository
}

func (l repositoryEvidenceLoader) load(ctx context.Context, sourcePath string) (legacyEvidence, error) {
	var evidence legacyEvidence
	ids, err := l.repo.GetExternalIdentity(ctx, sourcePath)
	if err == nil {
		evidence.identity = ids
		evidence.hasIDs = true
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return legacyEvidence{}, fmt.Errorf("load stored identity evidence: %w", err)
	}
	metadata, err := l.repo.GetExternalMetadata(ctx, sourcePath)
	if err == nil {
		evidence.metadata = metadata
		evidence.hasMeta = true
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return legacyEvidence{}, fmt.Errorf("load stored provider evidence: %w", err)
	}
	return evidence, nil
}

// Resolver owns identity evidence precedence, tri-state overrides, source
// serialization, diagnostics, and immutable result projection.
type Resolver struct {
	evidence  evidenceLoader
	candidate CandidateSource
	gates     sourceGates
	now       func() time.Time
}

// New constructs a resolver using the repository only as a private legacy
// evidence adapter. Resolve never writes through it.
func New(repo api.ReleaseStateRepository) (*Resolver, error) {
	return NewWithCandidateSource(repo, nil)
}

// NewWithCandidateSource constructs a resolver with an optional, read-only
// provider evidence source. Neither source can publish canonical state.
func NewWithCandidateSource(repo api.ReleaseStateRepository, candidate CandidateSource) (*Resolver, error) {
	if isNilRepository(repo) {
		return nil, errors.New("external identity: repository is required")
	}
	return &Resolver{
		evidence:  repositoryEvidenceLoader{repo: repo},
		candidate: candidate,
		now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

func isNilRepository(repo api.ReleaseStateRepository) bool {
	if repo == nil {
		return true
	}
	value := reflect.ValueOf(repo)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

// Resolve serializes work per normalized source, applies stored evidence then
// provider candidates then explicit overrides, and returns a detached,
// unpersisted result. Provider metadata whose ID no longer matches the final
// identity is removed; absent IDs and category are reported through
// Result.MissingRequirements rather than as an error.
func (r *Resolver) Resolve(ctx context.Context, request Request) (Result, error) {
	if r == nil || r.evidence == nil {
		return Result{}, errors.New("external identity: resolver is not initialized")
	}
	if ctx == nil {
		return Result{}, errors.New("external identity: context is required")
	}
	normalized, err := normalizeSourcePath(request.SourcePath)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(request.SourceFingerprint) == "" || request.Generation == 0 {
		return Result{}, internalerrors.ErrInvalidInput
	}
	request.SourcePath = normalized

	release, err := r.gates.acquire(ctx, normalized)
	if err != nil {
		return Result{}, fmt.Errorf("external identity: wait for source: %w", err)
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("external identity: resolve canceled: %w", err)
	}

	evidence, err := r.evidence.load(ctx, normalized)
	if err != nil {
		return Result{}, fmt.Errorf("external identity: %w", err)
	}
	intentFingerprint, err := fingerprintIntent(request.Intent)
	if err != nil {
		return Result{}, err
	}
	var candidate CandidateEvidence
	if r.candidate != nil {
		candidate, err = r.candidate.ResolveIdentityCandidate(ctx, request)
		if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
			return Result{}, fmt.Errorf("external identity: resolve provider evidence: %w", err)
		}
	}
	result, err := r.resolveCandidate(request, intentFingerprint, evidence, candidate)
	if err != nil {
		return Result{}, err
	}
	return result.Clone()
}

func (r *Resolver) resolveCandidate(
	request Request,
	intentFingerprint string,
	evidence legacyEvidence,
	candidate CandidateEvidence,
) (Result, error) {
	resolvedAt := r.now().UTC()
	identity := api.ExternalIdentity{
		SourcePath: request.SourcePath,
		Generation: request.Generation,
		Category:   api.CanonicalCategoryUnknown,
		Provenance: unknownProvenance(),
		Overrides:  unsetOverrides(),
		Conflict:   api.IdentityConflictNone,
		Resolution: api.IdentityResolutionKey{
			SourceFingerprint: request.SourceFingerprint,
			IntentFingerprint: intentFingerprint,
			ContractVersion:   ContractVersion,
		},
		ResolvedAt: resolvedAt,
	}
	metadata := api.SourceScopedMetadata{
		SourcePath: request.SourcePath,
		Generation: request.Generation,
		UpdatedAt:  resolvedAt,
	}
	result := Result{Identity: identity, ProviderMetadata: metadata}

	if evidence.hasIDs {
		applyStoredIdentity(&result.Identity, evidence.identity)
		if err := appendEvidenceFingerprint(&result, "stored_identity", evidence.identity); err != nil {
			return Result{}, err
		}
		result.Diagnostics = append(result.Diagnostics, api.PreparationDiagnostic{
			Code:     "legacy_identity_evidence",
			Severity: api.DiagnosticSeverityInfo,
			Message:  "stored external identity was considered as resolution evidence",
		})
	}
	if evidence.hasMeta && sourceEvidenceMatches(evidence.metadata.SourcePath, request.SourcePath) {
		result.ProviderMetadata = sourceScopedMetadata(evidence.metadata, request.SourcePath, request.Generation, resolvedAt)
		if err := appendEvidenceFingerprint(&result, "stored_provider_metadata", evidence.metadata); err != nil {
			return Result{}, err
		}
	}
	if err := applyCandidateEvidence(&result, candidate, request.SourcePath, request.Generation, resolvedAt); err != nil {
		return Result{}, err
	}
	if err := applyResolutionIntent(&result.Identity, request.Intent); err != nil {
		return Result{}, err
	}
	invalidateMismatchedMetadata(&result.ProviderMetadata, result.Identity)
	result.MissingRequirements = missingRequirements(result.Identity)
	return result, nil
}

func applyCandidateEvidence(
	result *Result,
	candidate CandidateEvidence,
	sourcePath string,
	generation api.PreparedGeneration,
	resolvedAt time.Time,
) error {
	if result == nil {
		return nil
	}
	if hasCandidateIdentity(candidate.Identity) && sourceEvidenceMatches(candidate.Identity.SourcePath, sourcePath) {
		applyCandidateIdentity(&result.Identity, candidate.Identity)
		if err := appendEvidenceFingerprint(result, "provider_candidate", candidate.Identity); err != nil {
			return err
		}
	}
	if hasCandidateMetadata(candidate.Metadata) && sourceEvidenceMatches(candidate.Metadata.SourcePath, sourcePath) {
		result.ProviderMetadata = sourceScopedMetadata(candidate.Metadata, sourcePath, generation, resolvedAt)
		if err := appendEvidenceFingerprint(result, "provider_metadata_candidate", candidate.Metadata); err != nil {
			return err
		}
	}
	if diagnostics := candidateDiagnostics(candidate.Candidates); len(diagnostics) > 0 {
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
	}
	return nil
}

func hasCandidateIdentity(value api.ExternalIdentity) bool {
	return value.TMDBID > 0 || value.IMDBID > 0 || value.TVDBID > 0 || value.TVmazeID > 0 || value.MALID > 0 || value.Category != ""
}

func hasCandidateMetadata(value api.SourceScopedMetadata) bool {
	return value.TMDB != nil || value.IMDB != nil || value.TVDB != nil || value.TVmaze != nil || value.AniList != nil || value.Bluray != nil
}

func applyCandidateIdentity(identity *api.ExternalIdentity, candidate api.ExternalIdentity) {
	if identity == nil {
		return
	}
	applyCandidateProvider(&identity.TMDBID, &identity.Provenance.TMDB, candidate.TMDBID, candidate.Provenance.TMDB)
	applyCandidateProvider(&identity.IMDBID, &identity.Provenance.IMDB, candidate.IMDBID, candidate.Provenance.IMDB)
	applyCandidateProvider(&identity.TVDBID, &identity.Provenance.TVDB, candidate.TVDBID, candidate.Provenance.TVDB)
	applyCandidateProvider(&identity.TVmazeID, &identity.Provenance.TVmaze, candidate.TVmazeID, candidate.Provenance.TVmaze)
	applyCandidateProvider(&identity.MALID, &identity.Provenance.MAL, candidate.MALID, candidate.Provenance.MAL)
	if category, err := api.NormalizeCanonicalCategory(string(candidate.Category)); err == nil && category != api.CanonicalCategoryUnknown {
		identity.Category = category
		identity.Provenance.Category = candidate.Provenance.Category
		if identity.Provenance.Category == "" || identity.Provenance.Category == api.IdentityProvenanceUnknown {
			identity.Provenance.Category = api.IdentityProvenanceProvider
		}
	}
}

func applyCandidateProvider(target *int, provenance *api.IdentityProvenance, value int, source api.IdentityProvenance) {
	if value <= 0 {
		return
	}
	*target = value
	*provenance = source
	if *provenance == "" || *provenance == api.IdentityProvenanceUnknown {
		*provenance = api.IdentityProvenanceProvider
	}
}

func candidateDiagnostics(value []api.ExternalIdentityCandidate) []api.PreparationDiagnostic {
	candidates := make([]api.ExternalIdentityCandidate, 0, len(value))
	for _, candidate := range value {
		provider := api.IdentityProvider(strings.ToLower(strings.TrimSpace(string(candidate.Provider))))
		if provider != api.IdentityProviderTMDB && provider != api.IdentityProviderIMDB {
			continue
		}
		category, _ := api.NormalizeCanonicalCategory(string(candidate.Category))
		candidates = append(candidates, api.ExternalIdentityCandidate{
			Provider:      provider,
			ID:            candidate.ID,
			Title:         candidate.Title,
			OriginalTitle: candidate.OriginalTitle,
			Year:          candidate.Year,
			Category:      category,
			MediaType:     candidate.MediaType,
			Overview:      candidate.Overview,
			PosterURL:     candidate.PosterURL,
			Similarity:    candidate.Similarity,
		})
	}
	if len(candidates) == 0 {
		return nil
	}
	return []api.PreparationDiagnostic{{
		Code:       "external_identity_candidates",
		Severity:   api.DiagnosticSeverityInfo,
		Message:    "provider candidates were considered during identity resolution",
		Candidates: candidates,
	}}
}

func applyStoredIdentity(identity *api.ExternalIdentity, stored api.ExternalIdentity) {
	if identity == nil || !sourceEvidenceMatches(stored.SourcePath, identity.SourcePath) {
		return
	}
	identity.TMDBID = stored.TMDBID
	identity.IMDBID = stored.IMDBID
	identity.TVDBID = stored.TVDBID
	identity.TVmazeID = stored.TVmazeID
	identity.MALID = stored.MALID
	if category, err := api.NormalizeCanonicalCategory(string(stored.Category)); err == nil {
		identity.Category = category
	}
	identity.Provenance = stored.Provenance
	ensureStoredProvenance(&identity.Provenance.TMDB, stored.TMDBID)
	ensureStoredProvenance(&identity.Provenance.IMDB, stored.IMDBID)
	ensureStoredProvenance(&identity.Provenance.TVDB, stored.TVDBID)
	ensureStoredProvenance(&identity.Provenance.TVmaze, stored.TVmazeID)
	ensureStoredProvenance(&identity.Provenance.MAL, stored.MALID)
	ensureStoredProvenance(&identity.Provenance.Category, boolInt(identity.Category != api.CanonicalCategoryUnknown))
}

func ensureStoredProvenance(provenance *api.IdentityProvenance, value int) {
	if value > 0 && (*provenance == "" || *provenance == api.IdentityProvenanceUnknown) {
		*provenance = api.IdentityProvenanceLegacy
	}
}

func applyResolutionIntent(identity *api.ExternalIdentity, intent ResolutionIntent) error {
	if identity == nil {
		return internalerrors.ErrInvalidInput
	}
	applyProviderOverride(&identity.TMDBID, &identity.Provenance.TMDB, &identity.Overrides.TMDB, intent.ProviderOverrides.TMDBID)
	applyProviderOverride(&identity.IMDBID, &identity.Provenance.IMDB, &identity.Overrides.IMDB, intent.ProviderOverrides.IMDBID)
	applyProviderOverride(&identity.TVDBID, &identity.Provenance.TVDB, &identity.Overrides.TVDB, intent.ProviderOverrides.TVDBID)
	applyProviderOverride(&identity.TVmazeID, &identity.Provenance.TVmaze, &identity.Overrides.TVmaze, intent.ProviderOverrides.TVmazeID)
	applyProviderOverride(&identity.MALID, &identity.Provenance.MAL, &identity.Overrides.MAL, intent.ProviderOverrides.MALID)
	if intent.CategoryOverride != nil {
		category := *intent.CategoryOverride
		switch category {
		case "", api.CanonicalCategoryUnknown:
			identity.Category = api.CanonicalCategoryUnknown
			identity.Overrides.Category = api.OverrideStateClear
		case api.CanonicalCategoryMovie, api.CanonicalCategoryTV:
			identity.Category = category
			identity.Overrides.Category = api.OverrideStateValue
		default:
			return fmt.Errorf("external identity: invalid category override %q: %w", category, internalerrors.ErrInvalidInput)
		}
		identity.Provenance.Category = api.IdentityProvenanceExplicit
	}
	return nil
}

func applyProviderOverride(id *int, provenance *api.IdentityProvenance, state *api.OverrideState, override *int) {
	if override == nil {
		return
	}
	*provenance = api.IdentityProvenanceExplicit
	if *override <= 0 {
		*id = 0
		*state = api.OverrideStateClear
		return
	}
	*id = *override
	*state = api.OverrideStateValue
}

func sourceScopedMetadata(
	stored api.SourceScopedMetadata,
	sourcePath string,
	generation api.PreparedGeneration,
	updatedAt time.Time,
) api.SourceScopedMetadata {
	return api.SourceScopedMetadata{
		SourcePath: sourcePath,
		Generation: generation,
		TMDB:       stored.TMDB,
		IMDB:       stored.IMDB,
		TVDB:       stored.TVDB,
		TVmaze:     stored.TVmaze,
		AniList:    stored.AniList,
		Bluray:     stored.Bluray,
		UpdatedAt:  updatedAt,
	}
}

func invalidateMismatchedMetadata(metadata *api.SourceScopedMetadata, identity api.ExternalIdentity) {
	if metadata == nil {
		return
	}
	if metadata.TMDB != nil && metadata.TMDB.TMDBID != identity.TMDBID {
		metadata.TMDB = nil
	}
	if metadata.IMDB != nil && metadata.IMDB.IMDBID != identity.IMDBID {
		metadata.IMDB = nil
	}
	if metadata.TVDB != nil && metadata.TVDB.TVDBID != identity.TVDBID {
		metadata.TVDB = nil
	}
	if metadata.TVmaze != nil && metadata.TVmaze.TVmazeID != identity.TVmazeID {
		metadata.TVmaze = nil
	}
	if metadata.AniList != nil && metadata.AniList.MALID != identity.MALID {
		metadata.AniList = nil
	}
}

func missingRequirements(identity api.ExternalIdentity) []api.MissingRequirementError {
	requirements := make([]api.MissingRequirementError, 0, 6)
	for _, provider := range []api.IdentityProvider{
		api.IdentityProviderTMDB,
		api.IdentityProviderIMDB,
		api.IdentityProviderTVDB,
		api.IdentityProviderTVmaze,
		api.IdentityProviderMAL,
	} {
		if _, ok := identity.ProviderID(provider); !ok {
			requirements = append(requirements, api.MissingRequirementError{
				Requirement: api.RequirementKindProviderID,
				Provider:    provider,
			})
		}
	}
	if _, err := identity.RequireCategory(); err != nil {
		requirements = append(requirements, api.MissingRequirementError{Requirement: api.RequirementKindCategory})
	}
	return requirements
}

func unknownProvenance() api.IdentityProvenanceSet {
	return api.IdentityProvenanceSet{
		TMDB:     api.IdentityProvenanceUnknown,
		IMDB:     api.IdentityProvenanceUnknown,
		TVDB:     api.IdentityProvenanceUnknown,
		TVmaze:   api.IdentityProvenanceUnknown,
		MAL:      api.IdentityProvenanceUnknown,
		Category: api.IdentityProvenanceUnknown,
	}
}

func unsetOverrides() api.IdentityOverrideState {
	return api.IdentityOverrideState{
		TMDB:     api.OverrideStateUnset,
		IMDB:     api.OverrideStateUnset,
		TVDB:     api.OverrideStateUnset,
		TVmaze:   api.OverrideStateUnset,
		MAL:      api.OverrideStateUnset,
		Category: api.OverrideStateUnset,
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func sourceEvidenceMatches(storedPath string, sourcePath string) bool {
	storedPath = strings.TrimSpace(storedPath)
	if storedPath == "" {
		return true
	}
	return filepath.Clean(storedPath) == filepath.Clean(sourcePath)
}

func normalizeSourcePath(sourcePath string) (string, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return "", internalerrors.ErrInvalidInput
	}
	normalized, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", fmt.Errorf("external identity: normalize source path: %w", err)
	}
	return filepath.Clean(normalized), nil
}

func fingerprintIntent(intent ResolutionIntent) (string, error) {
	normalized := intent
	normalized.Title = strings.TrimSpace(normalized.Title)
	normalized.TrackerContext = append([]string(nil), normalized.TrackerContext...)
	for index := range normalized.TrackerContext {
		normalized.TrackerContext[index] = strings.ToUpper(strings.TrimSpace(normalized.TrackerContext[index]))
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("external identity: fingerprint intent: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func appendEvidenceFingerprint(result *Result, kind string, evidence any) error {
	fingerprint, err := fingerprintEvidence(kind, evidence)
	if err != nil {
		return err
	}
	result.EvidenceFingerprints = append(result.EvidenceFingerprints, fingerprint)
	return nil
}

func fingerprintEvidence(kind string, evidence any) (EvidenceFingerprint, error) {
	payload, err := json.Marshal(evidence)
	if err != nil {
		return EvidenceFingerprint{}, fmt.Errorf("external identity: marshal %s evidence fingerprint: %w", kind, err)
	}
	digest := sha256.Sum256(payload)
	return EvidenceFingerprint{Kind: kind, Digest: hex.EncodeToString(digest[:])}, nil
}

type sourceWaiter struct {
	ready chan struct{}
}

type sourceGate struct {
	held    bool
	waiters []*sourceWaiter
}

type sourceGates struct {
	mu    sync.Mutex
	gates map[string]*sourceGate
}

func (g *sourceGates) acquire(ctx context.Context, sourcePath string) (func(), error) {
	g.mu.Lock()
	if g.gates == nil {
		g.gates = make(map[string]*sourceGate)
	}
	gate := g.gates[sourcePath]
	if gate == nil {
		gate = &sourceGate{held: true}
		g.gates[sourcePath] = gate
		g.mu.Unlock()
		return func() { g.release(sourcePath, gate) }, nil
	}
	waiter := &sourceWaiter{ready: make(chan struct{})}
	gate.waiters = append(gate.waiters, waiter)
	g.mu.Unlock()

	select {
	case <-waiter.ready:
		if err := ctx.Err(); err != nil {
			g.release(sourcePath, gate)
			return nil, fmt.Errorf("external identity: acquire source gate: %w", err)
		}
		return func() { g.release(sourcePath, gate) }, nil
	case <-ctx.Done():
		g.mu.Lock()
		removed := false
		for index, candidate := range gate.waiters {
			if candidate != waiter {
				continue
			}
			gate.waiters = append(gate.waiters[:index], gate.waiters[index+1:]...)
			removed = true
			break
		}
		g.mu.Unlock()
		if !removed {
			g.release(sourcePath, gate)
		}
		return nil, fmt.Errorf("external identity: wait for source gate: %w", ctx.Err())
	}
}

func (g *sourceGates) release(sourcePath string, gate *sourceGate) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(gate.waiters) > 0 {
		next := gate.waiters[0]
		gate.waiters = gate.waiters[1:]
		close(next.ready)
		return
	}
	gate.held = false
	delete(g.gates, sourcePath)
}
