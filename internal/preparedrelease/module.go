// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package preparedrelease owns immutable, source-scoped prepared-release
// generations and their private preparation resources.
package preparedrelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/externalidentity"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
)

// ContractVersion changes whenever prepared fact semantics or the private seed
// contract become incompatible with an earlier generation.
const ContractVersion = "prepared-release-v2"

// Store is the prepared-release persistence port. Implementations must commit
// facts, identity, and provider metadata as one generation transaction.
type Store interface {
	LoadPreparedRelease(context.Context, string) (api.PreparedRelease, error)
	CommitPreparedRelease(context.Context, api.PreparedRelease) error
	PurgePreparedRelease(context.Context, string) error
}

// IdentityResolver builds an unpersisted canonical identity candidate. The
// module commits it with the rest of the prepared generation.
type IdentityResolver interface {
	Resolve(context.Context, externalidentity.Request) (externalidentity.Result, error)
}

// Collector finalizes reusable source-derived facts. It does not persist,
// publish, or attach workflow choices/outcomes.
type Collector interface {
	Collect(context.Context, preparationstate.Request) (CollectedFacts, error)
}

// CollectedFacts is the collector-to-owner handoff. Identity and provider
// metadata are supplied only by IdentityResolver.
type CollectedFacts struct {
	Naming      api.NamingFacts
	Episode     api.EpisodeFacts
	Media       api.MediaFacts
	Disc        api.DiscFacts
	Assessments api.ReleaseAssessments
	Identity    externalidentity.ResolutionIntent
	Diagnostics []api.PreparationDiagnostic
	Resources   CollectedResources
}

// CollectedResources is the collector-to-owner handoff for local artifacts
// required by later operations. Resources remain inside the prepared-release
// module and are never included in PreparedRelease. Path fields are host
// filesystem paths.
type CollectedResources struct {
	SourcePath            string
	VideoPath             string
	FileList              []string
	MediaInfoJSONPath     string
	MediaInfoTextPath     string
	DVDIFOPath            string
	DVDVOBPath            string
	DVDVOBMediaInfoJSON   string
	DVDVOBMediaInfoText   string
	SceneNFOPath          string
	DescriptionTemplate   string
	SelectedBDMVPlaylists []api.PlaylistInfo
	ClientEvidence        preparationstate.ClientEvidenceSnapshot
}

type clientEvidenceHydrator interface {
	HydrateClientEvidence(context.Context, preparationstate.Request) (preparationstate.ClientEvidenceSnapshot, error)
}

// Module owns one current immutable generation and private envelope per
// normalized source. It serializes mutation for the same source while allowing
// different sources to prepare concurrently.
type Module struct {
	store     Store
	identity  IdentityResolver
	collector Collector
	now       func() time.Time

	mu        sync.RWMutex
	envelopes map[string]envelope
	gates     sourceGates
}

// New requires and borrows all persistence, identity, and collection ports.
func New(store Store, identity IdentityResolver, collector Collector) (*Module, error) {
	if store == nil {
		return nil, errors.New("prepared release: store is required")
	}
	if identity == nil {
		return nil, errors.New("prepared release: identity resolver is required")
	}
	if collector == nil {
		return nil, errors.New("prepared release: fact collector is required")
	}
	return &Module{
		store:     store,
		identity:  identity,
		collector: collector,
		now:       func() time.Time { return time.Now().UTC() },
		envelopes: make(map[string]envelope),
	}, nil
}

// Prepare serializes same-source work and returns a detached copy of the exact
// compatible generation when reuse is allowed. Otherwise it commits a new
// generation before publishing it; failed collection or commit leaves the
// prior published generation intact.
func (m *Module) Prepare(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
	if m == nil || m.store == nil || m.identity == nil || m.collector == nil {
		return api.PrepareResult{}, errors.New("prepared release: module is not initialized")
	}
	if ctx == nil {
		return api.PrepareResult{}, errors.New("prepared release: context is required")
	}
	normalized, err := normalizePrepareInput(input)
	if err != nil {
		return api.PrepareResult{}, err
	}
	input = normalized.input

	releaseGate, err := m.gates.acquire(ctx, normalized.sourceKey)
	if err != nil {
		return api.PrepareResult{}, fmt.Errorf("prepared release: wait for source: %w", err)
	}
	defer releaseGate()
	if err := ctx.Err(); err != nil {
		return api.PrepareResult{}, fmt.Errorf("prepared release: prepare canceled: %w", err)
	}

	api.EmitPreparationProgress(
		ctx,
		api.NewPreparationProgressUpdate(api.PreparationPhaseSourceInspection, api.PreparationProgressRunning, "Inspecting source layout."),
	)
	layout, err := sourcelayout.Resolve(ctx, input.SourcePath)
	if err != nil {
		api.EmitPreparationProgress(
			ctx,
			api.NewPreparationProgressUpdate(api.PreparationPhaseSourceInspection, api.PreparationProgressFailed, "Source inspection failed."),
		)
		return api.PrepareResult{}, fmt.Errorf("prepared release: resolve source layout: %w", err)
	}
	manifest, sourceFingerprint, err := inspectSource(ctx, input, layout)
	if err != nil {
		api.EmitPreparationProgress(
			ctx,
			api.NewPreparationProgressUpdate(api.PreparationPhaseSourceInspection, api.PreparationProgressFailed, "Source inspection failed."),
		)
		return api.PrepareResult{}, err
	}
	api.EmitPreparationProgress(
		ctx,
		api.NewPreparationProgressUpdate(api.PreparationPhaseSourceInspection, api.PreparationProgressCompleted, "Source inspection complete."),
	)
	api.EmitPreparationProgress(
		ctx,
		api.NewPreparationProgressUpdate(api.PreparationPhasePreparedCache, api.PreparationProgressRunning, "Checking prepared generation compatibility."),
	)
	compatibility, err := preparationCompatibility(input, sourceFingerprint)
	if err != nil {
		api.EmitPreparationProgress(
			ctx,
			api.NewPreparationProgressUpdate(api.PreparationPhasePreparedCache, api.PreparationProgressFailed, "Prepared cache check failed."),
		)
		return api.PrepareResult{}, err
	}

	current, hasCurrent, err := m.loadCurrent(ctx, input.SourcePath)
	if err != nil {
		api.EmitPreparationProgress(
			ctx,
			api.NewPreparationProgressUpdate(api.PreparationPhasePreparedCache, api.PreparationProgressFailed, "Prepared cache check failed."),
		)
		return api.PrepareResult{}, err
	}
	// A remembered BDMV selection is repository state, not part of the input
	// fingerprint. Keep that compatibility fallback non-cacheable so a changed
	// remembered selection cannot silently reuse an older generation. Normal CLI
	// and WebUI calls carry their resolved selection directly and remain reusable.
	reuseAllowed := layout.DiscType != "BDMV" || input.Instructions.Playlist.Set
	forceClientRefresh := input.Controls.ForceRecheck != nil && *input.Controls.ForceRecheck
	if hasCurrent && reuseAllowed && !input.Force && !forceClientRefresh && current.Compatibility == compatibility {
		if !m.hasPublishedGeneration(current.Source.SourcePath, current.Generation) {
			hydrator, ok := m.collector.(clientEvidenceHydrator)
			if !ok {
				return api.PrepareResult{}, errors.New("prepared release: collector cannot hydrate private client evidence")
			}
			finish := api.BeginPreparationProgress(ctx, api.PreparationPhaseClientDiscovery, "Hydrating private client evidence.")
			snapshot, hydrateErr := hydrator.HydrateClientEvidence(ctx, preparationstate.Request{
				Input:    input,
				Manifest: current.Source,
				Layout:   layout,
			})
			finish(hydrateErr)
			if hydrateErr != nil {
				return api.PrepareResult{}, fmt.Errorf("prepared release: hydrate persisted client evidence: %w", hydrateErr)
			}
			owned := envelopeFromPersisted(current, input)
			owned.resources.clientEvidence = preparationstate.CloneClientEvidenceSnapshot(snapshot)
			m.publish(owned)
		}
		api.EmitPreparationProgress(
			ctx,
			api.NewPreparationProgressUpdate(api.PreparationPhasePreparedCache, api.PreparationProgressCompleted, "Reused the compatible prepared generation."),
		)
		skipReusedPreparationStages(ctx)
		return cloneResult(api.PrepareResult{Release: current})
	}
	api.EmitPreparationProgress(
		ctx,
		api.NewPreparationProgressUpdate(api.PreparationPhasePreparedCache, api.PreparationProgressCompleted, "A new prepared generation is required."),
	)

	generation := api.PreparedGeneration(1)
	if hasCurrent {
		generation = current.Generation + 1
		if generation == 0 {
			return api.PrepareResult{}, errors.New("prepared release: generation overflow")
		}
	}
	collected, err := m.collector.Collect(ctx, preparationstate.Request{
		Input:    input,
		Manifest: manifest,
		Layout:   layout,
	})
	if err != nil {
		return api.PrepareResult{}, fmt.Errorf("prepared release: collect facts: %w", err)
	}
	manifest.SelectedPlaylists = clonePreparedPlaylists(collected.Resources.SelectedBDMVPlaylists)
	identityIntent := collected.Identity
	mergeFactInstructions(&identityIntent, input.Instructions)
	identityFinish := api.BeginPreparationProgress(ctx, api.PreparationPhaseCanonicalIdentity, "Resolving canonical identity.")
	identityResult, err := m.identity.Resolve(ctx, externalidentity.Request{
		SourcePath:        manifest.SourcePath,
		SourceFingerprint: sourceFingerprint,
		Generation:        generation,
		Intent:            identityIntent,
	})
	if err != nil {
		identityFinish(err)
		return api.PrepareResult{}, fmt.Errorf("prepared release: resolve identity: %w", err)
	}
	identityFinish(nil)

	preparedAt := m.now().UTC()
	release := api.PreparedRelease{
		Generation:       generation,
		Compatibility:    compatibility,
		Source:           manifest,
		Naming:           collected.Naming,
		Episode:          collected.Episode,
		Media:            collected.Media,
		Disc:             collected.Disc,
		Identity:         identityResult.Identity,
		ProviderMetadata: identityResult.ProviderMetadata,
		Assessments:      normalizeAssessments(collected.Assessments),
		PreparedAt:       preparedAt,
	}
	commitFinish := api.BeginPreparationProgress(ctx, api.PreparationPhaseGenerationCommit, "Committing prepared generation.")
	if err := validateGeneration(release); err != nil {
		commitFinish(err)
		return api.PrepareResult{}, err
	}
	if err := m.store.CommitPreparedRelease(ctx, release); err != nil {
		commitFinish(err)
		return api.PrepareResult{}, fmt.Errorf("prepared release: commit generation: %w", err)
	}

	diagnostics := append([]api.PreparationDiagnostic(nil), collected.Diagnostics...)
	diagnostics = append(diagnostics, identityResult.Diagnostics...)
	owned := envelope{
		result: api.PrepareResult{
			Release:     release,
			Diagnostics: diagnostics,
		},
		resources: mergePreparationResources(resourcesFromManifest(manifest, input), resourcesFromCollected(collected.Resources)),
	}
	m.publish(owned)
	commitFinish(nil)
	return cloneResult(owned.result)
}

// skipReusedPreparationStages emits terminal skipped updates for work supplied
// by a compatible prepared generation.
func skipReusedPreparationStages(ctx context.Context) {
	for _, phase := range []api.PreparationProgressPhase{
		api.PreparationPhaseSourceEvidence,
		api.PreparationPhaseBDInfo,
		api.PreparationPhaseClientDiscovery,
		api.PreparationPhaseTrackerEvidence,
		api.PreparationPhaseMediaInfoIdentity,
		api.PreparationPhaseArrIdentity,
		api.PreparationPhaseExternalIdentity,
		api.PreparationPhaseMediaFacts,
		api.PreparationPhaseCanonicalIdentity,
		api.PreparationPhaseGenerationCommit,
	} {
		api.SkipPreparationProgress(ctx, phase, "Reused from the compatible prepared generation.")
	}
}

// Export returns an opaque immutable seed for one exact published generation.
func (m *Module) Export(ctx context.Context, ref api.ReleaseRef) (Seed, error) {
	if m == nil {
		return Seed{}, errors.New("prepared release: module is not initialized")
	}
	if ctx == nil {
		return Seed{}, errors.New("prepared release: context is required")
	}
	normalized, err := normalizeSourcePath(ref.SourcePath)
	if err != nil || ref.Generation == 0 {
		return Seed{}, internalerrors.ErrInvalidInput
	}
	key := canonicalSourceKey(normalized)
	if err := ctx.Err(); err != nil {
		return Seed{}, fmt.Errorf("prepared release: export canceled: %w", err)
	}
	m.mu.RLock()
	owned, ok := m.envelopes[key]
	m.mu.RUnlock()
	if !ok || owned.result.Release.Generation != ref.Generation {
		return Seed{}, &StalePreparationError{
			SourcePath: key,
			Generation: ref.Generation,
			Reason:     StaleReasonGeneration,
		}
	}
	cloned, err := cloneEnvelope(owned)
	if err != nil {
		return Seed{}, err
	}
	return Seed{payload: cloned}, nil
}

// ResolveResult validates one exact published generation and returns a cloned
// public projection without exposing preparation resources.
func (m *Module) ResolveResult(ctx context.Context, ref api.ReleaseRef) (api.PrepareResult, error) {
	owned, err := m.resolveEnvelope(ctx, ref)
	if err != nil {
		return api.PrepareResult{}, err
	}
	return cloneResult(owned.result)
}

// Import validates and publishes an opaque seed. Persistence completes before
// the in-memory generation becomes visible.
func (m *Module) Import(ctx context.Context, seed Seed) (api.ReleaseRef, error) {
	if m == nil || m.store == nil {
		return api.ReleaseRef{}, errors.New("prepared release: module is not initialized")
	}
	if ctx == nil {
		return api.ReleaseRef{}, errors.New("prepared release: context is required")
	}
	owned, err := seed.clonePayload()
	if err != nil {
		return api.ReleaseRef{}, err
	}
	release := owned.result.Release
	normalized, err := normalizeSourcePath(release.Source.SourcePath)
	if err != nil || release.Generation == 0 {
		return api.ReleaseRef{}, internalerrors.ErrInvalidInput
	}
	key := canonicalSourceKey(normalized)
	if err := validateGeneration(release); err != nil {
		return api.ReleaseRef{}, err
	}

	releaseGate, err := m.gates.acquire(ctx, key)
	if err != nil {
		return api.ReleaseRef{}, fmt.Errorf("prepared release: wait to import source: %w", err)
	}
	defer releaseGate()
	current, found, err := m.loadCurrent(ctx, release.Source.SourcePath)
	if err != nil {
		return api.ReleaseRef{}, err
	}
	if err := validateSeedSource(ctx, owned); err != nil {
		return api.ReleaseRef{}, err
	}
	if found && current.Generation > release.Generation {
		return api.ReleaseRef{}, &StalePreparationError{
			SourcePath: key,
			Generation: release.Generation,
			Reason:     StaleReasonGeneration,
		}
	}
	if found && current.Generation == release.Generation && current.Compatibility != release.Compatibility {
		return api.ReleaseRef{}, &IncompatiblePreparationError{SourcePath: key, Reason: "generation compatibility differs"}
	}
	if !found || current.Generation != release.Generation {
		if err := m.store.CommitPreparedRelease(ctx, release); err != nil {
			return api.ReleaseRef{}, fmt.Errorf("prepared release: import generation: %w", err)
		}
	}
	m.publish(owned)
	return api.ReleaseRef{SourcePath: release.Source.SourcePath, Generation: release.Generation}, nil
}

// Purge removes persisted and published prepared state for sourcePath.
func (m *Module) Purge(ctx context.Context, sourcePath string) error {
	if m == nil || m.store == nil {
		return errors.New("prepared release: module is not initialized")
	}
	normalized, err := normalizeSourcePath(sourcePath)
	if err != nil {
		return err
	}
	key := canonicalSourceKey(normalized)
	releaseGate, err := m.gates.acquire(ctx, key)
	if err != nil {
		return fmt.Errorf("prepared release: wait to purge source: %w", err)
	}
	defer releaseGate()
	if err := m.store.PurgePreparedRelease(ctx, normalized); err != nil {
		return fmt.Errorf("prepared release: purge: %w", err)
	}
	m.mu.Lock()
	delete(m.envelopes, key)
	m.mu.Unlock()
	return nil
}

func (m *Module) loadCurrent(ctx context.Context, sourcePath string) (api.PreparedRelease, bool, error) {
	release, err := m.store.LoadPreparedRelease(ctx, sourcePath)
	if err == nil {
		cloned, cloneErr := release.Clone()
		if cloneErr != nil {
			return api.PreparedRelease{}, false, fmt.Errorf("prepared release: clone current release: %w", cloneErr)
		}
		return cloned, true, nil
	}
	if errors.Is(err, internalerrors.ErrNotFound) {
		return api.PreparedRelease{}, false, nil
	}
	return api.PreparedRelease{}, false, fmt.Errorf("prepared release: load current: %w", err)
}

func (m *Module) publish(owned envelope) {
	key := canonicalSourceKey(owned.result.Release.Source.SourcePath)
	cloned, err := cloneEnvelope(owned)
	if err != nil {
		panic(fmt.Sprintf("prepared release: clone validated generation for publication: %v", err))
	}
	m.mu.Lock()
	m.envelopes[key] = cloned
	m.mu.Unlock()
}

func (m *Module) hasPublishedGeneration(sourcePath string, generation api.PreparedGeneration) bool {
	key := canonicalSourceKey(sourcePath)
	m.mu.RLock()
	current, ok := m.envelopes[key]
	m.mu.RUnlock()
	return ok && current.result.Release.Generation == generation
}

func mergeFactInstructions(intent *externalidentity.ResolutionIntent, instructions api.ReleaseFactInstructions) {
	intent.ProviderOverrides = cloneExternalIDOverrides(instructions.Identity)
	if instructions.Category != nil {
		value := *instructions.Category
		intent.CategoryOverride = &value
	}
}

func normalizeAssessments(value api.ReleaseAssessments) api.ReleaseAssessments {
	if value.MediaInfoUniqueID == "" {
		value.MediaInfoUniqueID = api.UniqueIDStatusUnknown
	}
	if value.MediaInfoEncodeSettings == "" {
		value.MediaInfoEncodeSettings = api.EncodeSettingsStatusUnknown
	}
	if value.Naming.Status == "" {
		value.Naming.Status = api.NamingStatusUnknown
	}
	value.Naming.Missing = append([]api.NamingRequirement(nil), value.Naming.Missing...)
	return value
}

func validateGeneration(release api.PreparedRelease) error {
	if release.Generation == 0 || canonicalSourceKey(release.Source.SourcePath) == "" {
		return internalerrors.ErrInvalidInput
	}
	if release.Compatibility.ContractVersion != ContractVersion {
		return &IncompatiblePreparationError{SourcePath: release.Source.SourcePath, Reason: "unsupported prepared-release contract"}
	}
	if strings.TrimSpace(release.Compatibility.SourceFingerprint) == "" ||
		strings.TrimSpace(release.Compatibility.FactInstructionFingerprint) == "" ||
		strings.TrimSpace(release.Compatibility.PolicyFingerprint) == "" {
		return internalerrors.ErrInvalidInput
	}
	if release.Identity.Generation != release.Generation || release.ProviderMetadata.Generation != release.Generation ||
		canonicalSourceKey(release.Identity.SourcePath) != canonicalSourceKey(release.Source.SourcePath) ||
		canonicalSourceKey(release.ProviderMetadata.SourcePath) != canonicalSourceKey(release.Source.SourcePath) {
		return &IncompatiblePreparationError{SourcePath: release.Source.SourcePath, Reason: "generation components differ"}
	}
	if reason := providerIdentityMismatch(release); reason != "" {
		return &IncompatiblePreparationError{SourcePath: release.Source.SourcePath, Reason: reason}
	}
	return nil
}

func providerIdentityMismatch(release api.PreparedRelease) string {
	metadata := release.ProviderMetadata
	identity := release.Identity
	checks := []struct {
		provider api.IdentityProvider
		present  bool
		ownID    int
		identity int
	}{
		{
			provider: api.IdentityProviderTMDB,
			present:  metadata.TMDB != nil,
			ownID:    metadataIDTMDB(metadata.TMDB),
			identity: identity.TMDBID,
		},
		{
			provider: api.IdentityProviderIMDB,
			present:  metadata.IMDB != nil,
			ownID:    metadataIDIMDB(metadata.IMDB),
			identity: identity.IMDBID,
		},
		{
			provider: api.IdentityProviderTVDB,
			present:  metadata.TVDB != nil,
			ownID:    metadataIDTVDB(metadata.TVDB),
			identity: identity.TVDBID,
		},
		{
			provider: api.IdentityProviderTVmaze,
			present:  metadata.TVmaze != nil,
			ownID:    metadataIDTVmaze(metadata.TVmaze),
			identity: identity.TVmazeID,
		},
		{
			provider: api.IdentityProviderMAL,
			present:  metadata.AniList != nil,
			ownID:    metadataIDMAL(metadata.AniList),
			identity: identity.MALID,
		},
	}
	for _, check := range checks {
		if check.present && (check.ownID <= 0 || check.identity <= 0 || check.ownID != check.identity) {
			return fmt.Sprintf("%s provider metadata does not match canonical identity", check.provider)
		}
	}
	return ""
}

func metadataIDTMDB(value *api.TMDBMetadata) int {
	if value == nil {
		return 0
	}
	return value.TMDBID
}

func metadataIDIMDB(value *api.IMDBMetadata) int {
	if value == nil {
		return 0
	}
	return value.IMDBID
}

func metadataIDTVDB(value *api.TVDBMetadata) int {
	if value == nil {
		return 0
	}
	return value.TVDBID
}

func metadataIDTVmaze(value *api.TVmazeMetadata) int {
	if value == nil {
		return 0
	}
	return value.TVmazeID
}

func metadataIDMAL(value *api.AniListMetadata) int {
	if value == nil {
		return 0
	}
	return value.MALID
}

func cloneResult(result api.PrepareResult) (api.PrepareResult, error) {
	cloned, err := result.Clone()
	if err != nil {
		return api.PrepareResult{}, fmt.Errorf("prepared release: clone result: %w", err)
	}
	return cloned, nil
}

func cloneExternalIDOverrides(value api.ExternalIDOverrides) api.ExternalIDOverrides {
	return api.ExternalIDOverrides{
		TMDBID:   cloneInt(value.TMDBID),
		IMDBID:   cloneInt(value.IMDBID),
		TVDBID:   cloneInt(value.TVDBID),
		TVmazeID: cloneInt(value.TVmazeID),
		MALID:    cloneInt(value.MALID),
	}
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type envelope struct {
	result    api.PrepareResult
	resources preparationResources
}

type preparationResources struct {
	sourcePath            string
	playlist              api.PlaylistInstruction
	videoPath             string
	fileList              []string
	mediaInfoJSONPath     string
	mediaInfoTextPath     string
	dvdIFOPath            string
	dvdVOBPath            string
	dvdVOBMediaInfoJSON   string
	dvdVOBMediaInfoText   string
	sceneNFOPath          string
	descriptionTemplate   string
	selectedBDMVPlaylists []api.PlaylistInfo
	clientEvidence        preparationstate.ClientEvidenceSnapshot
}

func resourcesFromManifest(manifest api.SourceManifest, input api.PrepareInput) preparationResources {
	playlist := api.PlaylistInstruction{
		Set:      input.Instructions.Playlist.Set,
		Selected: append([]string(nil), input.Instructions.Playlist.Selected...),
		UseAll:   input.Instructions.Playlist.UseAll,
	}
	return preparationResources{sourcePath: manifest.SourcePath, playlist: playlist}
}

func envelopeFromPersisted(release api.PreparedRelease, input api.PrepareInput) envelope {
	return envelope{
		result:    api.PrepareResult{Release: release},
		resources: resourcesFromManifest(release.Source, input),
	}
}

func mergePreparationResources(base preparationResources, collected preparationResources) preparationResources {
	if strings.TrimSpace(collected.sourcePath) != "" {
		base.sourcePath = collected.sourcePath
	}
	base.videoPath = collected.videoPath
	base.fileList = append([]string(nil), collected.fileList...)
	base.mediaInfoJSONPath = collected.mediaInfoJSONPath
	base.mediaInfoTextPath = collected.mediaInfoTextPath
	base.dvdIFOPath = collected.dvdIFOPath
	base.dvdVOBPath = collected.dvdVOBPath
	base.dvdVOBMediaInfoJSON = collected.dvdVOBMediaInfoJSON
	base.dvdVOBMediaInfoText = collected.dvdVOBMediaInfoText
	base.sceneNFOPath = collected.sceneNFOPath
	base.descriptionTemplate = collected.descriptionTemplate
	base.selectedBDMVPlaylists = collected.selectedBDMVPlaylists
	base.clientEvidence = preparationstate.CloneClientEvidenceSnapshot(collected.clientEvidence)
	return base
}

func resourcesFromCollected(collected CollectedResources) preparationResources {
	return preparationResources{
		sourcePath:            collected.SourcePath,
		videoPath:             collected.VideoPath,
		fileList:              append([]string(nil), collected.FileList...),
		mediaInfoJSONPath:     collected.MediaInfoJSONPath,
		mediaInfoTextPath:     collected.MediaInfoTextPath,
		dvdIFOPath:            collected.DVDIFOPath,
		dvdVOBPath:            collected.DVDVOBPath,
		dvdVOBMediaInfoJSON:   collected.DVDVOBMediaInfoJSON,
		dvdVOBMediaInfoText:   collected.DVDVOBMediaInfoText,
		sceneNFOPath:          collected.SceneNFOPath,
		descriptionTemplate:   collected.DescriptionTemplate,
		selectedBDMVPlaylists: clonePreparedPlaylists(collected.SelectedBDMVPlaylists),
		clientEvidence:        preparationstate.CloneClientEvidenceSnapshot(collected.ClientEvidence),
	}
}

func cloneEnvelope(value envelope) (envelope, error) {
	clonedResult, err := value.result.Clone()
	if err != nil {
		return envelope{}, fmt.Errorf("prepared release: clone envelope: %w", err)
	}
	cloned := envelope{
		result: clonedResult,
		resources: preparationResources{
			sourcePath:          value.resources.sourcePath,
			videoPath:           value.resources.videoPath,
			fileList:            append([]string(nil), value.resources.fileList...),
			mediaInfoJSONPath:   value.resources.mediaInfoJSONPath,
			mediaInfoTextPath:   value.resources.mediaInfoTextPath,
			dvdIFOPath:          value.resources.dvdIFOPath,
			dvdVOBPath:          value.resources.dvdVOBPath,
			dvdVOBMediaInfoJSON: value.resources.dvdVOBMediaInfoJSON,
			dvdVOBMediaInfoText: value.resources.dvdVOBMediaInfoText,
			sceneNFOPath:        value.resources.sceneNFOPath,
			descriptionTemplate: value.resources.descriptionTemplate,
			playlist: api.PlaylistInstruction{
				Set:      value.resources.playlist.Set,
				Selected: append([]string(nil), value.resources.playlist.Selected...),
				UseAll:   value.resources.playlist.UseAll,
			},
			selectedBDMVPlaylists: clonePreparedPlaylists(value.resources.selectedBDMVPlaylists),
			clientEvidence:        preparationstate.CloneClientEvidenceSnapshot(value.resources.clientEvidence),
		},
	}
	return cloned, nil
}

func clonePreparedPlaylists(value []api.PlaylistInfo) []api.PlaylistInfo {
	cloned, err := cloneWithJSON(value)
	if err != nil {
		panic(fmt.Sprintf("prepared release: clone playlists: %v", err))
	}
	return cloned
}

func cloneWithJSON[T any](value T) (T, error) {
	var cloned T
	payload, err := json.Marshal(value)
	if err != nil {
		return cloned, fmt.Errorf("prepared release: clone value: marshal: %w", err)
	}
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return cloned, fmt.Errorf("prepared release: clone value: unmarshal: %w", err)
	}
	return cloned, nil
}

// Seed is an opaque prepared-generation transfer value. Its payload contains
// preparation resources but no workflow choices or outcomes.
type Seed struct {
	payload envelope
}

func (s Seed) clonePayload() (envelope, error) {
	if s.payload.result.Release.Generation == 0 {
		return envelope{}, internalerrors.ErrInvalidInput
	}
	return cloneEnvelope(s.payload)
}

// validateSeedSource rejects transferred generations whose current source
// fingerprint no longer matches the committed preparation resources.
func validateSeedSource(ctx context.Context, owned envelope) error {
	sourcePath := strings.TrimSpace(owned.resources.sourcePath)
	if sourcePath == "" {
		return &IncompatiblePreparationError{
			SourcePath: owned.result.Release.Source.SourcePath,
			Reason:     "seed has no source resources",
		}
	}
	input := api.PrepareInput{
		SourcePath: sourcePath,
		Instructions: api.ReleaseFactInstructions{
			Playlist: owned.resources.playlist,
		},
	}
	layout, err := sourcelayout.Resolve(ctx, sourcePath)
	if err != nil {
		return fmt.Errorf("prepared release: validate source layout: %w", err)
	}
	_, fingerprint, err := inspectSource(ctx, input, layout)
	if err != nil {
		return err
	}
	if fingerprint != owned.result.Release.Compatibility.SourceFingerprint {
		return &StalePreparationError{
			SourcePath: owned.result.Release.Source.SourcePath,
			Generation: owned.result.Release.Generation,
			Reason:     StaleReasonFingerprint,
		}
	}
	return nil
}

// StaleReason classifies why an exact prepared generation cannot be used.
type StaleReason string

const (
	// StaleReasonGeneration means the requested generation is not current/published.
	StaleReasonGeneration StaleReason = "generation"
	// StaleReasonFingerprint means source inventory no longer matches.
	StaleReasonFingerprint StaleReason = "fingerprint"
)

// StalePreparationError reports an exact-generation compatibility failure.
type StalePreparationError struct {
	SourcePath string
	Generation api.PreparedGeneration
	Reason     StaleReason
}

func (e *StalePreparationError) Error() string {
	if e == nil {
		return "stale prepared release"
	}
	return fmt.Sprintf("prepared release is stale: source=%s generation=%d reason=%s", e.SourcePath, e.Generation, e.Reason)
}

// IncompatiblePreparationError reports a prepared contract/lineage mismatch.
type IncompatiblePreparationError struct {
	SourcePath string
	Reason     string
}

func (e *IncompatiblePreparationError) Error() string {
	if e == nil {
		return "incompatible prepared release"
	}
	return fmt.Sprintf("prepared release is incompatible: source=%s reason=%s", e.SourcePath, e.Reason)
}
