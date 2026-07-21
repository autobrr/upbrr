// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/config/importer"
	"github.com/autobrr/upbrr/internal/core"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	imagehostpolicy "github.com/autobrr/upbrr/internal/imagehosting/policy"
	"github.com/autobrr/upbrr/internal/logging"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	sharedjobs "github.com/autobrr/upbrr/internal/webserver/jobs"
	"github.com/autobrr/upbrr/pkg/api"
)

const previewTimeout = 30 * time.Minute

// metadataPreparationRequest is the transport-neutral command shared by prepare
// and reset routes for one browser-selected source.
type metadataPreparationRequest struct {
	// CorrelationID binds emitted progress to the initiating browser command.
	CorrelationID string
	// Path is the selected host filesystem source path.
	Path string
	// SourceLookupURL optionally supplies tracker or provider lookup evidence.
	SourceLookupURL string
	// Overrides contains explicit external identity selections.
	Overrides api.ExternalIDOverrides
	// NameOverrides contains explicit release-name selections.
	NameOverrides api.ReleaseNameOverrides
	// Playlist carries direct Blu-ray playlist selection state.
	Playlist api.PlaylistInstruction
	// ConfirmBDMVRescan permits replacement of a partial cached Blu-ray analysis.
	ConfirmBDMVRescan bool
}

// blurayCandidateSelectionRequest selects one candidate within a correlated preparation attempt.
type blurayCandidateSelectionRequest struct {
	// CorrelationID binds emitted progress to the initiating browser command.
	CorrelationID string
	// Path is the selected host filesystem source path.
	Path string
	// ReleaseID identifies the candidate returned by the current preview.
	ReleaseID string
}

func newTrackerAuthService(cfg config.Config, logger api.Logger) *trackerauth.Service {
	return trackerauth.NewServiceWithRegistryAndLogger(cfg, trackerimpl.MustNewRegistry(), logger)
}

// Backend owns the embedded web API runtime and request-scoped background jobs.
type Backend struct {
	runtimeMu         sync.RWMutex
	cfg               config.Config
	runtimeGeneration uint64
	capabilities      CoreCapabilities
	coreOwner         LifecycleOwner
	coreInitErr       error
	logger            *logging.Logger
	repo              *db.SQLiteRepository
	hub               *eventHub

	streamMu sync.Mutex
	streams  map[string]*backendLogStream
	streamWG sync.WaitGroup

	// jobEngine owns session-scoped duplicate-check and tracker-upload jobs.
	jobEngine  *sharedjobs.Engine
	jobOwnerMu sync.Mutex
	jobOwners  map[string]*sharedjobs.OwnerHandle
	reviewMu   sync.Mutex
	reviews    *UploadReviewRegistry

	activationInitMu sync.Mutex
	activator        *RuntimeActivator
}

type backendLogStream struct {
	id        string
	sessionID string
	logger    *logging.Logger
	subID     int
	stop      chan struct{}
	done      chan struct{}
}

type runOptions struct {
	NoSeed      bool
	RunLogLevel string
}

// logExclusions stores muted log patterns for the WebUI.
type logExclusions struct {
	Patterns []string `json:"patterns"`
}

// NewBackend constructs a Backend using a background context.
func NewBackend(cfg config.Config, hub *eventHub) (*Backend, error) {
	return NewBackendWithContext(context.Background(), cfg, hub)
}

// NewBackendWithContext opens the shared repository, creates the logger, and
// starts the core service when cfg validates. Invalid config keeps settings
// routes usable while core-backed routes report the initialization error.
func NewBackendWithContext(ctx context.Context, cfg config.Config, hub *eventHub) (*Backend, error) {
	if ctx == nil {
		return nil, errors.New("webserver: context is required")
	}
	logger, err := logging.New(cfg.Logging, cfg.MainSettings.DBPath)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}

	repo, err := db.OpenWithLoggerContext(ctx, cfg.MainSettings.DBPath, logger)
	if err != nil {
		_ = logger.Close()
		return nil, fmt.Errorf("web: %w", err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		_ = repo.Close()
		_ = logger.Close()
		return nil, fmt.Errorf("web: %w", err)
	}

	var capabilities CoreCapabilities
	var coreOwner LifecycleOwner
	var coreInitErr error
	if err := cfg.Validate(); err != nil {
		coreInitErr = err
		logger.Warnf("web: config invalid, core disabled until settings are fixed: %v", err)
	} else {
		coreSvc, coreErr := core.NewWithContext(ctx, api.CoreDependencies{
			Config: cfg,
			Logger: logger,
			Services: api.ServiceSet{
				Filesystem: filesystem.NewValidator(),
			},
			Repository:      repo.RepositoryCapabilities(),
			RepositoryOwner: repo,
		})
		if coreErr != nil {
			_ = repo.Close()
			_ = logger.Close()
			return nil, fmt.Errorf("web: %w", coreErr)
		}
		capabilities, coreOwner = BindCoreCapabilities(coreSvc)
	}

	backend := &Backend{
		cfg:               cfg,
		runtimeGeneration: AllocateRuntimeGenerationID(),
		capabilities:      capabilities,
		coreOwner:         coreOwner,
		coreInitErr:       coreInitErr,
		logger:            logger,
		repo:              repo,
		hub:               hub,
		streams:           make(map[string]*backendLogStream),
		jobOwners:         make(map[string]*sharedjobs.OwnerHandle),
	}
	if _, err := backend.runtimeActivator(); err != nil {
		if coreOwner != nil {
			_ = coreOwner.Close()
		}
		_ = repo.Close()
		_ = logger.Close()
		return nil, fmt.Errorf("web: %w", err)
	}
	backend.jobEngine = sharedjobs.New(webJobSink{hub: hub, logger: logger}, sharedjobs.Config{
		UploadProgress: sharedjobs.UploadProgressPolicy{
			MinInterval:      trackerUploadSnapshotThrottle,
			HashRateDeltaMiB: trackerUploadHashRateEmitDeltaMiB,
		},
	})
	return backend, nil
}

// Close stops active background work and releases runtime, repository, and log resources.
func (b *Backend) Close() error {
	b.stopAllLogStreams()
	if b.jobEngine != nil {
		b.jobEngine.Close()
	}
	rt := b.runtimeSnapshot()
	if rt.coreOwner != nil {
		_ = rt.coreOwner.Close()
	}
	if b.repo != nil {
		_ = b.repo.Close()
	}
	if rt.logger != nil {
		_ = rt.logger.Close()
	}
	return nil
}

// DetectDiscType classifies the selected host filesystem release path.
func (b *Backend) DetectDiscType(ctx context.Context, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	layout, err := sourcelayout.Resolve(ctx, path)
	if err != nil {
		return wrapWebResult("", err)
	}
	return layout.DiscType, nil
}

// withPreparationProgress installs the sole WebUI progress boundary. It
// requires correlation, sanitizes display text, timestamps updates, and emits
// them only to the initiating session.
func (b *Backend) withPreparationProgress(ctx context.Context, sessionID string, correlationID string) (context.Context, error) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return nil, errors.New("preparation correlation ID is required")
	}
	progressCtx := api.WithPreparationProgressReporter(ctx, func(update api.PreparationProgressUpdate) {
		update.CorrelationID = correlationID
		update.Label = logging.SanitizeMessage(strings.TrimSpace(update.Label))
		update.Message = logging.SanitizeMessage(strings.TrimSpace(update.Message))
		update.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
		b.hub.Emit(sessionID, "preparation:progress", update)
	})
	progressCtx = bdinfo.WithProgressReporter(progressCtx, func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		api.EmitPreparationProgress(
			progressCtx,
			api.NewPreparationProgressUpdate(api.PreparationPhaseBDInfo, api.PreparationProgressRunning, line),
		)
	})
	return progressCtx, nil
}

// withImageUploadProgress installs the WebUI image-host progress boundary. It
// correlates, sanitizes, timestamps, and scopes advisory updates to one session.
func (b *Backend) withImageUploadProgress(ctx context.Context, sessionID string, correlationID string) (context.Context, error) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return nil, errors.New("image upload correlation ID is required")
	}
	return api.WithImageUploadProgressReporter(ctx, func(update api.ImageUploadProgressUpdate) {
		update.CorrelationID = correlationID
		update.AttemptID = logging.SanitizeMessage(strings.TrimSpace(update.AttemptID))
		update.Host = logging.SanitizeMessage(strings.TrimSpace(update.Host))
		update.UsageScope = logging.SanitizeMessage(strings.TrimSpace(update.UsageScope))
		for index := range update.Trackers {
			update.Trackers[index] = logging.SanitizeMessage(strings.TrimSpace(update.Trackers[index]))
		}
		update.Message = logging.SanitizeMessage(strings.TrimSpace(update.Message))
		update.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
		b.hub.Emit(sessionID, "image-upload:progress", update)
	}), nil
}

// FetchMetadata prepares and caches a metadata preview for the selected release
// path while emitting correlation-scoped progress to sessionID.
func (b *Backend) FetchMetadata(
	ctx context.Context,
	sessionID string,
	command metadataPreparationRequest,
) (api.MetadataPreview, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.MetadataPreview{}, err
	}
	metadataCore, err := rt.metadataCore()
	if err != nil {
		return api.MetadataPreview{}, err
	}
	trimmedPath := strings.TrimSpace(command.Path)
	if trimmedPath == "" {
		return api.MetadataPreview{}, errors.New("path is required")
	}

	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	progressCtx, err := b.withPreparationProgress(ctx, sessionID, command.CorrelationID)
	if err != nil {
		return api.MetadataPreview{}, err
	}

	req := api.Request{
		SourcePath:      trimmedPath,
		SourceLookupURL: strings.TrimSpace(command.SourceLookupURL),
		Options:         rt.baseUploadOptions(),

		ExternalIDOverrides:  command.Overrides,
		ReleaseNameOverrides: command.NameOverrides,
		PlaylistInstruction:  command.Playlist,
		ConfirmBDMVRescan:    command.ConfirmBDMVRescan,
	}

	preparationCore, err := rt.releasePreparationCore()
	if err != nil {
		return api.MetadataPreview{}, err
	}
	_, ref, err := PrepareGeneration(progressCtx, preparationCore, req, api.PreparationIntentPreview)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("web: %w", err)
	}
	preview, err := metadataCore.FetchAcceptedMetadataPreview(progressCtx, ref)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("web: %w", err)
	}
	preview.ReleaseNameOverrides = command.NameOverrides
	return preview, nil
}

// PrepareRelease creates or reuses one canonical prepared-release generation.
func (b *Backend) PrepareRelease(input api.PrepareInput) (api.PrepareResult, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.PrepareResult{}, err
	}
	preparationCore, err := rt.releasePreparationCore()
	if err != nil {
		return api.PrepareResult{}, err
	}
	if strings.TrimSpace(input.SourcePath) == "" {
		return api.PrepareResult{}, errors.New("path is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(preparationCore.PrepareRelease(ctx, input))
}

// SelectBlurayCandidate updates the prepared release with the chosen Blu-ray
// metadata candidate and emits correlation-scoped progress to sessionID.
func (b *Backend) SelectBlurayCandidate(
	ctx context.Context,
	sessionID string,
	command blurayCandidateSelectionRequest,
) (api.MetadataPreview, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.MetadataPreview{}, err
	}
	selectionCore, err := rt.selectionCore()
	if err != nil {
		return api.MetadataPreview{}, err
	}
	if strings.TrimSpace(command.Path) == "" {
		return api.MetadataPreview{}, errors.New("path is required")
	}
	if strings.TrimSpace(command.ReleaseID) == "" {
		return api.MetadataPreview{}, errors.New("release ID is required")
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	progressCtx, err := b.withPreparationProgress(ctx, sessionID, command.CorrelationID)
	if err != nil {
		return api.MetadataPreview{}, err
	}
	finish := api.BeginPreparationProgress(progressCtx, api.PreparationPhaseCandidateSelection, "Applying Blu-ray candidate selection.")
	preview, err := selectionCore.SelectBlurayCandidate(progressCtx, command.Path, command.ReleaseID)
	finish(err)
	return wrapWebResult(preview, err)
}

// ResetMetadata removes persisted source artifacts, then rebuilds prepared
// metadata using the supplied overrides and correlation-scoped progress.
func (b *Backend) ResetMetadata(
	ctx context.Context,
	sessionID string,
	command metadataPreparationRequest,
) (api.MetadataPreview, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.MetadataPreview{}, err
	}
	metadataCore, err := rt.metadataCore()
	if err != nil {
		return api.MetadataPreview{}, err
	}
	if b.repo == nil {
		return api.MetadataPreview{}, errors.New("config repository not initialized")
	}
	trimmedPath := strings.TrimSpace(command.Path)
	if trimmedPath == "" {
		return api.MetadataPreview{}, errors.New("path is required")
	}

	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	progressCtx, err := b.withPreparationProgress(ctx, sessionID, command.CorrelationID)
	if err != nil {
		return api.MetadataPreview{}, err
	}
	resetFinish := api.BeginPreparationProgress(progressCtx, api.PreparationPhaseResetCleanup, "Removing previous metadata artifacts.")

	tmpRoot, err := db.Subdir(rt.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		resetFinish(err)
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: resolve tmp dir: %w", err)
	}

	artifactPaths := make([]string, 0)
	shots, err := b.repo.ListScreenshotsByPath(ctx, trimmedPath)
	if err != nil {
		resetFinish(err)
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: list screenshots: %w", err)
	}
	for _, shot := range shots {
		artifactPaths = append(artifactPaths, shot.ImagePath)
	}
	uploaded, err := b.repo.ListUploadedImagesByPath(ctx, trimmedPath)
	if err != nil {
		resetFinish(err)
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: list uploaded images: %w", err)
	}
	for _, image := range uploaded {
		artifactPaths = append(artifactPaths, image.ImagePath)
	}
	finals, err := b.repo.ListFinalSelections(ctx, trimmedPath)
	if err != nil {
		resetFinish(err)
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: list final selections: %w", err)
	}
	for _, image := range finals {
		artifactPaths = append(artifactPaths, image.ImagePath)
	}
	artifactPaths = slices.Compact(artifactPaths)

	tmpDirs := make(map[string]struct{})
	fallbackBase := paths.ReleaseTempBaseFor(trimmedPath, api.ReleaseInfo{})
	tmpDirs[filepath.Join(tmpRoot, fallbackBase)] = struct{}{}
	stored, err := b.repo.GetByPath(ctx, trimmedPath)
	if err == nil {
		releaseBase := paths.ReleaseTempBaseFor(trimmedPath, api.ReleaseInfo{
			Title:    stored.Title,
			Alt:      stored.Alt,
			Year:     stored.Year,
			Category: string(stored.Category),
			Source:   stored.Source,
			Type:     stored.Type,
			Group:    stored.Group,
		})
		tmpDirs[filepath.Join(tmpRoot, releaseBase)] = struct{}{}
	}
	for _, filePath := range artifactPaths {
		contentRoot, ok := resolveContentTmpRoot(tmpRoot, filePath)
		if ok {
			tmpDirs[contentRoot] = struct{}{}
		}
	}
	if err := b.repo.PurgeContentData(ctx, trimmedPath); err != nil {
		resetFinish(err)
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: purge sqlite: %w", err)
	}
	for _, filePath := range artifactPaths {
		_ = removeIfWithinRoot(tmpRoot, filePath, false)
	}
	for dir := range tmpDirs {
		_ = removeIfWithinRoot(tmpRoot, dir, true)
	}
	resetFinish(nil)

	req := api.Request{
		SourcePath:      trimmedPath,
		SourceLookupURL: strings.TrimSpace(command.SourceLookupURL),
		Options:         rt.baseUploadOptions(),

		ExternalIDOverrides:  command.Overrides,
		ReleaseNameOverrides: command.NameOverrides,
		PlaylistInstruction:  command.Playlist,
		ConfirmBDMVRescan:    command.ConfirmBDMVRescan,
	}
	preparationCore, err := rt.releasePreparationCore()
	if err != nil {
		return api.MetadataPreview{}, err
	}
	_, ref, err := PrepareGeneration(progressCtx, preparationCore, req, api.PreparationIntentPreview)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("web: %w", err)
	}
	preview, err := metadataCore.FetchAcceptedMetadataPreview(progressCtx, ref)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("web: %w", err)
	}
	preview.ReleaseNameOverrides = command.NameOverrides
	return preview, nil
}

// FetchPreparation returns tracker review data prepared from the current release snapshot.
func (b *Backend) FetchPreparation(
	sessionID string,
	path string,
	overrides api.ExternalIDOverrides,
	nameOverrides api.ReleaseNameOverrides,
	trackersList []string,
	ignoreDupesFor []string,
) (api.PreparationPreview, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.PreparationPreview{}, err
	}
	preparationCore, err := rt.preparationCore()
	if err != nil {
		return api.PreparationPreview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	req := api.Request{
		SourcePath:           strings.TrimSpace(path),
		Trackers:             append([]string{}, trackersList...),
		IgnoreDupesFor:       normalizeTrackerList(ignoreDupesFor),
		Options:              rt.baseUploadOptions(),
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}
	progressCtx := bdinfo.WithProgressReporter(ctx, func(line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		b.hub.Emit(sessionID, "bdinfo:progress", map[string]string{
			"path": strings.TrimSpace(path),
			"line": line,
		})
	})
	ref, err := prepareWebReleaseRef(progressCtx, rt, req, api.PreparationIntentDescription)
	if err != nil {
		return api.PreparationPreview{}, err
	}
	return wrapWebResult(preparationCore.FetchAcceptedPreparationPreview(progressCtx, api.DescriptionInput{
		Release:  ref,
		Trackers: append([]string(nil), trackersList...),
		Options:  req.Options,
	}))
}

// FetchTrackerDryRun runs prepared payload previews for selected trackers.
func (b *Backend) FetchTrackerDryRun(
	ctx context.Context,
	sessionID string,
	dupeJobID string,
	release api.ReleaseRef,
	trackersList []string,
	ignoreDupesFor []string,
	questionnaireAnswers map[string]map[string]string,
	descriptionGroups []api.DescriptionBuilderGroup,
	noSeed bool,
	runLogLevel string,
) (api.TrackerDryRunPreview, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	if !rt.capabilities.PreparedDryRunReady() {
		return api.TrackerDryRunPreview{}, ErrPreparedDryRunUnavailable
	}
	runOpts, err := b.buildRunOptions(noSeed, runLogLevel)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	release, err = normalizeExactRelease(release, api.OperationKindDryRun)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	b.logDebugf("web: tracker dry-run request path=%s no_seed=%t run_log_level=%s", release.SourcePath, noSeed, runOpts.RunLogLevel)
	resolvedTrackers := normalizeTrackerList(trackersList)
	duplicateEvidence, err := b.acceptedDryRunDuplicateEvidence(sessionID, dupeJobID, release, rt.generationID, resolvedTrackers)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	req := api.Request{
		DescriptionGroups:           api.CloneDescriptionBuilderGroups(descriptionGroups),
		Trackers:                    append([]string{}, resolvedTrackers...),
		IgnoreDupesFor:              normalizeTrackerList(ignoreDupesFor),
		Options:                     buildRunUploadOptions(rt.cfg, runOpts),
		TrackerQuestionnaireAnswers: cloneQuestionnaireAnswers(questionnaireAnswers),
	}
	progressCtx := api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
		b.hub.Emit(sessionID, trackerUploadProgressEvent, update)
	})
	if rt.logger != nil {
		operationLogger, scopeErr := logging.NewOperationLogger(rt.logger, runOpts.RunLogLevel)
		if scopeErr != nil {
			return api.TrackerDryRunPreview{}, fmt.Errorf("web: scoped dry-run logger: %w", scopeErr)
		}
		progressCtx = logging.WithOperationLogger(progressCtx, operationLogger)
	}
	progressCtx = bdinfo.WithProgressReporter(progressCtx, func(line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		b.hub.Emit(sessionID, "bdinfo:progress", map[string]string{
			"path": release.SourcePath,
			"line": line,
		})
	})
	return wrapWebResult(rt.capabilities.DryRun.RunAcceptedTrackerDryRun(progressCtx, api.TrackerDryRunPlan{
		Input: api.TrackerDryRunInput{
			Release:                release,
			Trackers:               append([]string(nil), req.Trackers...),
			IgnoreDupesFor:         append([]string(nil), req.IgnoreDupesFor...),
			QuestionnaireAnswers:   cloneQuestionnaireAnswers(req.TrackerQuestionnaireAnswers),
			DescriptionGroups:      api.CloneDescriptionBuilderGroups(req.DescriptionGroups),
			TrackerConfigOverrides: req.TrackerConfigOverrides,
			TrackerSiteOverrides:   req.TrackerSiteOverrides,
			ImageHostOverrides:     req.ImageHostOverrides,
			TorrentOverrides:       req.TorrentOverrides,
			Options:                req.Options,
		},
		Duplicate: duplicateEvidence,
	}))
}

// acceptedDryRunDuplicateEvidence returns duplicate evidence only when the
// retained job belongs to the session and matches the exact release, runtime
// generation, and ordered tracker selection used by the dry run.
func (b *Backend) acceptedDryRunDuplicateEvidence(
	sessionID string,
	jobID string,
	release api.ReleaseRef,
	runtimeGeneration uint64,
	trackers []string,
) (api.AcceptedDuplicateEvidence, error) {
	missing := func(message string, cause error) (api.AcceptedDuplicateEvidence, error) {
		return api.AcceptedDuplicateEvidence{}, api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureMissingPrerequisite,
			Operation: api.OperationKindDryRun,
			Message:   message,
			Recovery:  api.OperationRecoveryCompletePrerequisite,
		}, cause)
	}
	if strings.TrimSpace(jobID) == "" || b == nil || b.jobEngine == nil {
		return missing("Run duplicate checking before starting a dry run.", errors.New("duplicate job is required"))
	}
	snapshot, err := b.jobEngine.DupeSnapshot(b.lookupJobOwner(sessionID), strings.TrimSpace(jobID))
	if err != nil {
		return missing("Duplicate-check results are unavailable. Run duplicate checking again.", err)
	}
	status := strings.ToLower(strings.TrimSpace(snapshot.Status))
	if status != sharedjobs.StatusCompleted && status != sharedjobs.StatusCompletedWithErrors {
		return missing("Duplicate checking must finish before starting a dry run.", fmt.Errorf("duplicate job status is %s", status))
	}
	if snapshot.Release != release {
		return api.AcceptedDuplicateEvidence{}, api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureStaleGeneration,
			Operation: api.OperationKindDryRun,
			Message:   "Duplicate-check results are stale. Run duplicate checking again.",
			Recovery:  api.OperationRecoveryCompletePrerequisite,
		}, errors.New("duplicate job release does not match dry-run release"))
	}
	if snapshot.RuntimeGeneration != runtimeGeneration {
		return api.AcceptedDuplicateEvidence{}, api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureStaleGeneration,
			Operation: api.OperationKindDryRun,
			Message:   "Runtime settings changed. Run duplicate checking again.",
			Recovery:  api.OperationRecoveryCompletePrerequisite,
		}, errors.New("duplicate job runtime generation is stale"))
	}
	requested := normalizeTrackerList(snapshot.RequestedTrackers)
	if !slices.Equal(requested, trackers) {
		return missing(
			"Duplicate-check results do not match the selected trackers. Run duplicate checking again.",
			errors.New("duplicate job tracker selection does not match dry-run selection"),
		)
	}
	return api.NewAcceptedDuplicateEvidence(release, requested, snapshot.Summary), nil
}

// FetchDescriptionBuilder returns editable tracker description groups for the prepared release.
func (b *Backend) FetchDescriptionBuilder(
	release api.ReleaseRef,
	trackersList []string,
) (api.DescriptionBuilderPreview, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.DescriptionBuilderPreview{}, err
	}
	descriptionCore, err := rt.descriptionCore()
	if err != nil {
		return api.DescriptionBuilderPreview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindDescription)
	if err != nil {
		return api.DescriptionBuilderPreview{}, err
	}
	return wrapWebResult(descriptionCore.FetchAcceptedDescriptionBuilderPreview(ctx, api.DescriptionInput{
		Release:  ref,
		Trackers: append([]string(nil), trackersList...),
		Options:  rt.baseUploadOptions(),
	}))
}

// RenderDescription converts tracker markup into sanitized preview HTML.
func (b *Backend) RenderDescription(raw string) (string, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return "", err
	}
	descriptionCore, err := rt.descriptionCore()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(descriptionCore.RenderDescription(ctx, raw))
}

// SaveDescriptionOverride publishes edited description groups to the prepared release.
func (b *Backend) SaveDescriptionOverride(
	release api.ReleaseRef,
	groupKey string,
	raw string,
	trackers []string,
) (api.DescriptionBuilderGroup, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.DescriptionBuilderGroup{}, err
	}
	descriptionCore, err := rt.descriptionCore()
	if err != nil {
		return api.DescriptionBuilderGroup{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindDescription)
	if err != nil {
		return api.DescriptionBuilderGroup{}, err
	}
	return wrapWebResult(descriptionCore.SaveAcceptedDescriptionOverride(ctx, api.DescriptionInput{
		Release:  ref,
		Trackers: append([]string(nil), trackers...),
		GroupKey: strings.TrimSpace(groupKey),
		Options:  rt.baseUploadOptions(),
	}, raw))
}

// DiscoverPlaylists returns Blu-ray playlists available under the selected release path.
func (b *Backend) DiscoverPlaylists(ctx context.Context, path string) ([]api.PlaylistInfo, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return nil, err
	}
	playlistCore, err := rt.playlistCore()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	return wrapWebResult(playlistCore.DiscoverPlaylists(ctx, path))
}

// BrowseDirectory lists a host filesystem directory for the requested frontend browse mode.
func (b *Backend) BrowseDirectory(path string, mode string) (api.BrowseDirectoryResponse, error) {
	if b == nil {
		return api.BrowseDirectoryResponse{}, errors.New("backend not initialized")
	}
	fallback := BrowseDirectoryFallback(b.currentConfig().MainSettings.DBPath)
	return wrapWebResult(BrowseDirectory(api.BrowseDirectoryRequest{Path: path, Mode: mode}, fallback))
}

// BrowseDirectoryWithinRoot lists a directory only when it remains within the authorized host root.
func (b *Backend) BrowseDirectoryWithinRoot(path string, mode string, root string) (api.BrowseDirectoryResponse, error) {
	if b == nil {
		return api.BrowseDirectoryResponse{}, errors.New("backend not initialized")
	}
	fallback := BrowseDirectoryFallback(b.currentConfig().MainSettings.DBPath)
	return wrapWebResult(BrowseDirectoryWithinRoot(api.BrowseDirectoryRequest{Path: path, Mode: mode}, fallback, root))
}

// BrowseDirectoryWithinRoots lists a directory only when it remains within an authorized host root.
func (b *Backend) BrowseDirectoryWithinRoots(path string, mode string, roots []string) (api.BrowseDirectoryResponse, error) {
	if b == nil {
		return api.BrowseDirectoryResponse{}, errors.New("backend not initialized")
	}
	fallback := BrowseDirectoryFallback(b.currentConfig().MainSettings.DBPath)
	return wrapWebResult(BrowseDirectoryWithinRoots(api.BrowseDirectoryRequest{Path: path, Mode: mode}, fallback, roots))
}

// FetchScreenshotPlan returns existing images and capture selections for a prepared release.
func (b *Backend) FetchScreenshotPlan(release api.ReleaseRef) (api.ScreenshotPlan, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.ScreenshotPlan{}, err
	}
	screenshotCore, err := rt.screenshotCore()
	if err != nil {
		return api.ScreenshotPlan{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return api.ScreenshotPlan{}, err
	}
	return wrapWebResult(screenshotCore.FetchAcceptedScreenshotPlan(ctx, api.MediaPlanInput{
		Release: ref,
		Count:   rt.baseUploadOptions().Screens,
	}))
}

// GenerateScreenshots captures selected frames and persists the resulting managed images.
func (b *Backend) GenerateScreenshots(
	release api.ReleaseRef,
	selections []api.ScreenshotSelection,
	purpose api.ScreenshotPurpose,
) (api.ScreenshotResult, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.ScreenshotResult{}, err
	}
	screenshotCore, err := rt.screenshotCore()
	if err != nil {
		return api.ScreenshotResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return api.ScreenshotResult{}, err
	}
	return wrapWebResult(screenshotCore.GenerateAcceptedScreenshots(ctx, api.MediaPlanInput{
		Release: ref,
		Count:   rt.baseUploadOptions().Screens,
		Purpose: purpose,
	}, selections))
}

// PreviewScreenshotFrame returns an encoded preview for the requested timestamp in seconds.
func (b *Backend) PreviewScreenshotFrame(
	release api.ReleaseRef,
	timestampSeconds float64,
) (string, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return "", err
	}
	screenshotCore, err := rt.screenshotCore()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return "", err
	}
	preview, err := screenshotCore.PreviewAcceptedScreenshotFrame(ctx, api.MediaPlanInput{
		Release: ref,
		Count:   rt.baseUploadOptions().Screens,
	}, timestampSeconds)
	if err != nil {
		return "", fmt.Errorf("web: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(preview.ImageBytes), nil
}

// DeleteScreenshot removes one managed screenshot and its prepared-release reference.
func (b *Backend) DeleteScreenshot(release api.ReleaseRef, imagePath string) error {
	rt, err := b.requireRuntime()
	if err != nil {
		return err
	}
	screenshotCore, err := rt.screenshotCore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return err
	}
	return wrapWebError(screenshotCore.DeleteAcceptedScreenshot(ctx, api.MediaPlanInput{
		Release: ref,
		Count:   rt.baseUploadOptions().Screens,
	}, imagePath))
}

// DeleteTrackerImageURL removes one tracker image URL from prepared release metadata.
func (b *Backend) DeleteTrackerImageURL(release api.ReleaseRef, imageURL string) error {
	rt, err := b.requireRuntime()
	if err != nil {
		return err
	}
	screenshotCore, err := rt.screenshotCore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return err
	}
	return wrapWebError(screenshotCore.DeleteAcceptedTrackerImageURL(ctx, api.ImageHostingInput{Release: ref}, imageURL))
}

// SaveFinalScreenshotSelections persists the ordered image set selected for tracker preparation.
func (b *Backend) SaveFinalScreenshotSelections(
	release api.ReleaseRef,
	images []api.ScreenshotImage,
) error {
	rt, err := b.requireRuntime()
	if err != nil {
		return err
	}
	screenshotCore, err := rt.screenshotCore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return err
	}
	return wrapWebError(screenshotCore.SaveAcceptedFinalScreenshotSelections(ctx, api.MediaPlanInput{
		Release: ref,
		Count:   rt.baseUploadOptions().Screens,
	}, images))
}

// ImportMenuImages copies selected host images into managed DVD menu storage.
func (b *Backend) ImportMenuImages(release api.ReleaseRef, paths []string) error {
	rt, err := b.requireRuntime()
	if err != nil {
		return err
	}
	screenshotCore, err := rt.screenshotCore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return err
	}
	return wrapWebError(screenshotCore.ImportAcceptedMenuImages(ctx, api.MediaPlanInput{Release: ref}, paths))
}

// ReadScreenshotImage returns a managed screenshot encoded for frontend display.
func (b *Backend) ReadScreenshotImage(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("path is required")
	}
	if !b.isPathWithinManagedDirs(trimmed) {
		return "", errors.New("path outside managed directories")
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return "", fmt.Errorf("read preview image: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(payload), nil
}

// ListUploadCandidates returns managed screenshots eligible for image-host upload.
func (b *Backend) ListUploadCandidates(release api.ReleaseRef) ([]api.ScreenshotImage, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return nil, err
	}
	hostedImageCore, err := rt.hostedImageCore()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return nil, err
	}
	return wrapWebResult(hostedImageCore.ListAcceptedUploadCandidates(ctx, api.ImageHostingInput{Release: ref}))
}

// ListUploadedImages returns persisted image-host links for the prepared release.
func (b *Backend) ListUploadedImages(release api.ReleaseRef) ([]api.UploadedImageLink, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return nil, err
	}
	hostedImageCore, err := rt.hostedImageCore()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return nil, err
	}
	return wrapWebResult(hostedImageCore.ListAcceptedUploadedImages(ctx, api.ImageHostingInput{Release: ref}))
}

// UploadImages sends selected managed images to the requested configured host and persists returned links.
func (b *Backend) UploadImages(
	ctx context.Context,
	sessionID string,
	correlationID string,
	release api.ReleaseRef,
	trackersList []string,
	host string,
	images []api.ScreenshotImage,
) (api.UploadImagesResult, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.UploadImagesResult{}, err
	}
	hostedImageCore, err := rt.hostedImageCore()
	if err != nil {
		return api.UploadImagesResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	progressCtx, err := b.withImageUploadProgress(ctx, sessionID, correlationID)
	if err != nil {
		return api.UploadImagesResult{}, err
	}
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return api.UploadImagesResult{}, err
	}
	return wrapWebResult(hostedImageCore.UploadAcceptedImages(progressCtx, api.ImageHostingInput{
		Release:  ref,
		Trackers: append([]string(nil), trackersList...),
		Host:     host,
	}, images))
}

// DeleteUploadedImage removes one persisted image-host link from release state.
func (b *Backend) DeleteUploadedImage(release api.ReleaseRef, imagePath string, host string) error {
	rt, err := b.requireRuntime()
	if err != nil {
		return err
	}
	hostedImageCore, err := rt.hostedImageCore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	ref, err := normalizeExactRelease(release, api.OperationKindMedia)
	if err != nil {
		return err
	}
	return wrapWebError(hostedImageCore.DeleteAcceptedUploadedImage(ctx, api.ImageHostingInput{Release: ref}, imagePath, host))
}

// normalizeExactRelease trims and validates the source path while preserving
// the caller's exact prepared generation.
func normalizeExactRelease(release api.ReleaseRef, operation api.OperationKind) (api.ReleaseRef, error) {
	release.SourcePath = strings.TrimSpace(release.SourcePath)
	if release.SourcePath == "" || release.Generation == 0 {
		return api.ReleaseRef{}, api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureInvalidSource,
			Operation: operation,
			Message:   "An exact prepared release is required.",
			Recovery:  api.OperationRecoveryRefreshRelease,
		}, errors.New("exact release reference is required"))
	}
	return release, nil
}

// prepareWebReleaseRef creates or reuses canonical preparation and returns only
// the exact reference needed by the requested browser operation.
func prepareWebReleaseRef(
	ctx context.Context,
	rt backendRuntimeSnapshot,
	request api.Request,
	intent api.PreparationIntent,
) (api.ReleaseRef, error) {
	preparer, err := rt.releasePreparationCore()
	if err != nil {
		return api.ReleaseRef{}, err
	}
	_, ref, err := PrepareGeneration(ctx, preparer, request, intent)
	if err != nil {
		return api.ReleaseRef{}, fmt.Errorf("web: %w", err)
	}
	return ref, nil
}

// GetConfig returns the current exportable config as JSON with encrypted
// secret fields for browser settings consumers.
func (b *Backend) GetConfig() (string, error) {
	cfg, _, err := b.exportableConfig()
	if err != nil {
		return "", err
	}
	return wrapWebResult(config.ExportToJSON(cfg))
}

// GetApplicationInfo returns build/runtime metadata plus a bounded, path-free
// DVD menu capability probe for embedded-web diagnostics.
func (b *Backend) GetApplicationInfo() (api.ApplicationInfo, error) {
	rt := b.runtimeSnapshot()
	return CurrentApplicationInfo(context.Background(), rt.capabilities.DiagnosticProbe), nil
}

// ExportConfig returns the exportable config, using plaintext secrets only
// when auth material for the exported snapshot's DB path explicitly allows
// unencrypted export.
func (b *Backend) ExportConfig() (string, error) {
	cfg, authDBPath, err := b.exportableConfig()
	if err != nil {
		return "", err
	}

	allowPlaintext, err := b.allowUnencryptedExport(authDBPath)
	if err != nil {
		return "", err
	}
	if allowPlaintext {
		return wrapWebResult(config.ExportToPlaintextJSON(cfg))
	}

	return wrapWebResult(config.ExportToJSON(cfg))
}

// GetDefaultConfig returns the built-in configuration serialized for the settings editor.
func (b *Backend) GetDefaultConfig() (string, error) {
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		return "", fmt.Errorf("web: %w", err)
	}
	return wrapWebResult(config.ExportToJSON(cfg))
}

// ListTrackerAuthCapabilities returns browser-visible tracker auth support from
// the current runtime config.
func (b *Backend) ListTrackerAuthCapabilities() ([]api.TrackerAuthCapability, error) {
	if b == nil {
		return nil, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Capabilities(context.Background()))
}

// GetTrackerAuthStatus reports local auth state for tracker from the current
// runtime config and persisted cookie/auth state.
func (b *Backend) GetTrackerAuthStatus(tracker string) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Status(context.Background(), tracker))
}

// ImportTrackerAuthCookieContent imports browser-supplied cookie content with
// the request context and the shared raw content size limit.
func (b *Backend) ImportTrackerAuthCookieContent(ctx context.Context, tracker string, fileName string, content string) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).ImportCookies(ctx, tracker, fileName, content))
}

// TestTrackerAuth validates tracker auth with ctx so canceled web requests stop
// remote validation and persistence work.
func (b *Backend) TestTrackerAuth(ctx context.Context, tracker string) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Validate(ctx, tracker))
}

// LoginTrackerAuth attempts credential-based tracker auth with ctx and returns
// status for missing credentials, unsupported login, or 2FA.
func (b *Backend) LoginTrackerAuth(ctx context.Context, tracker string, req api.TrackerAuthLoginRequest) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Login(ctx, tracker, req))
}

// SubmitTrackerAuth2FA completes an active manual 2FA challenge with ctx and
// returns the refreshed tracker auth status.
func (b *Backend) SubmitTrackerAuth2FA(ctx context.Context, challengeID string, code string) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Submit2FA(ctx, challengeID, code))
}

// DeleteTrackerAuth removes stored tracker cookies and tracker-specific auth
// state with ctx, then returns the refreshed local status.
func (b *Backend) DeleteTrackerAuth(ctx context.Context, tracker string) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Delete(ctx, tracker))
}

// exportableConfig returns the normalized config snapshot and the DB path that
// must authorize plaintext export for that exact snapshot. Fresh installs with
// no persisted config export the current runtime config without saving it.
func (b *Backend) exportableConfig() (*config.Config, string, error) {
	if b.repo == nil {
		return nil, "", errors.New("config repository not initialized")
	}
	rt := b.runtimeSnapshot()
	cfg, err := config.LoadFromDatabase(context.Background(), b.repo)
	if err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			// Fresh web installs can run from embedded defaults before any config
			// rows exist, so export the runtime config until the user saves setup.
			cfg, normalizeErr := normalizeExportableConfig(&rt.cfg, rt.cfg.MainSettings.DBPath)
			if normalizeErr != nil {
				return nil, "", normalizeErr
			}
			return cfg, cfg.MainSettings.DBPath, nil
		}
		return nil, "", fmt.Errorf("web: %w", err)
	}
	cfg, err = normalizeExportableConfig(cfg, rt.cfg.MainSettings.DBPath)
	if err != nil {
		return nil, "", err
	}
	return cfg, cfg.MainSettings.DBPath, nil
}

// normalizeExportableConfig returns a cloned config with tracker defaults,
// legacy nils, and missing DB path values filled so browser consumers receive
// stable JSON shapes without mutating the loaded runtime or database config.
func normalizeExportableConfig(cfg *config.Config, dbPath string) (*config.Config, error) {
	normalized, err := cloneConfigForExport(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := config.MergeMissingTrackerDefaults(normalized); err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	if strings.TrimSpace(normalized.MainSettings.DBPath) == "" {
		normalized.MainSettings.DBPath = dbPath
	}
	if normalized.Trackers.Trackers == nil {
		normalized.Trackers.Trackers = map[string]config.TrackerConfig{}
	}
	if normalized.Trackers.DefaultTrackers == nil {
		normalized.Trackers.DefaultTrackers = config.CSVList{}
	}
	return normalized, nil
}

// cloneConfigForExport deep-copies config through JSON so export
// normalization cannot mutate the source snapshot.
func cloneConfigForExport(cfg *config.Config) (*config.Config, error) {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("web: clone config for export: marshal: %w", err)
	}
	var cloned config.Config
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, fmt.Errorf("web: clone config for export: unmarshal: %w", err)
	}
	return &cloned, nil
}

// allowUnencryptedExport reports whether the auth material for dbPath permits
// plaintext config export. Missing auth material denies plaintext export;
// malformed material is returned as an error.
func (b *Backend) allowUnencryptedExport(dbPath string) (bool, error) {
	material, err := authmaterial.LoadFromDBPath(dbPath)
	if err == nil {
		return material.AllowUnencryptedExport, nil
	}
	if errors.Is(err, authmaterial.ErrUnavailable) {
		return false, nil
	}
	return false, fmt.Errorf("web: %w", err)
}

// SaveConfig decodes encrypted browser settings and delegates the complete
// config/runtime transition to the shared runtime activator.
func (b *Backend) SaveConfig(payload string) error {
	if b.repo == nil {
		return errors.New("config repository not initialized")
	}
	cfg, err := config.ImportFromJSONEncrypted(payload)
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}
	activator, err := b.runtimeActivator()
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}
	if err := activator.Activate(context.Background(), *cfg); err != nil {
		return fmt.Errorf("web: %w", err)
	}
	return nil
}

const configImportMaxBytes = importer.MaxFileBytes

// ImportConfig imports browser-uploaded config content, validates the saved and
// env-applied runtime forms, builds the replacement runtime, migrates shared
// cookies, then persists the non-env config before installing the runtime.
// Runtime build, migration, or save failures leave the persisted config and
// active runtime unchanged.
func (b *Backend) ImportConfig(fileName, fileContent string) (string, []string, error) {
	if b.repo == nil {
		return "", nil, errors.New("config repository not initialized")
	}
	if strings.TrimSpace(fileName) == "" {
		return "", nil, errors.New("file name is required")
	}
	if strings.TrimSpace(fileContent) == "" {
		return "", nil, errors.New("file content is required")
	}

	cfg, warnings, err := importer.ImportFromContent(fileName, []byte(fileContent))
	if err != nil {
		return "", nil, fmt.Errorf("web: %w", err)
	}

	activator, err := b.runtimeActivator()
	if err != nil {
		return "", nil, fmt.Errorf("web: %w", err)
	}
	if err := activator.Activate(context.Background(), *cfg); err != nil {
		return "", nil, fmt.Errorf("web: %w", err)
	}

	result := "imported config"
	if len(warnings) > 0 {
		result += fmt.Sprintf(" (%d warnings)", len(warnings))
	}
	return result, warnings, nil
}

// ListTrackerCatalog returns ordered tracker identity, config schemas, and local
// configured state without exposing current credential values.
func (b *Backend) ListTrackerCatalog() (api.TrackerCatalog, error) {
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		return api.TrackerCatalog{}, fmt.Errorf("webserver: tracker registry: %w", err)
	}
	schemas, err := config.OrderedTrackerSchemas()
	if err != nil {
		return api.TrackerCatalog{}, fmt.Errorf("webserver: tracker config catalog: %w", err)
	}

	cfg := b.currentConfig()
	entries := make([]api.TrackerCatalogEntry, 0, len(schemas))
	seen := make(map[string]struct{}, len(schemas))
	for _, schema := range schemas {
		descriptor, ok := registry.LookupDescriptor(schema.Name)
		if !ok {
			return api.TrackerCatalog{}, fmt.Errorf("webserver: tracker config catalog entry %s has no implementation", schema.Name)
		}
		fields := make([]api.TrackerCatalogField, len(schema.Fields))
		for index, field := range schema.Fields {
			fields[index] = api.TrackerCatalogField{
				Key:        field.JSONKey,
				YAMLKey:    field.YAMLKey,
				Default:    field.Default,
				Activation: field.Activation,
			}
		}
		trackerCfg, _ := trackerConfigByName(cfg.Trackers.Trackers, schema.Name)
		entries = append(entries, api.TrackerCatalogEntry{
			Name:              schema.Name,
			Family:            string(descriptor.Family),
			BaseURL:           descriptor.BaseURL,
			UploadContentMode: string(descriptor.UploadContentMode),
			Fields:            fields,
			Configured:        config.TrackerConfigured(trackerCfg, schema),
		})
		seen[schema.Name] = struct{}{}
	}
	for _, name := range registry.Names() {
		if _, ok := seen[name]; !ok {
			return api.TrackerCatalog{}, fmt.Errorf("webserver: tracker implementation %s has no config catalog entry", name)
		}
	}

	unsupported := make([]string, 0)
	for name := range cfg.Trackers.Trackers {
		normalized := strings.ToUpper(strings.TrimSpace(name))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; !ok {
			unsupported = append(unsupported, name)
		}
	}
	slices.SortFunc(unsupported, func(left, right string) int {
		return strings.Compare(strings.ToUpper(left), strings.ToUpper(right))
	})
	return api.TrackerCatalog{Entries: entries, Unsupported: unsupported}, nil
}

func trackerConfigByName(entries map[string]config.TrackerConfig, name string) (config.TrackerConfig, bool) {
	if cfg, ok := entries[name]; ok {
		return cfg, true
	}
	for entryName, cfg := range entries {
		if strings.EqualFold(strings.TrimSpace(entryName), strings.TrimSpace(name)) {
			return cfg, true
		}
	}
	return config.TrackerConfig{}, false
}

// GetImageHostPolicyMetadata returns image-host policy metadata consumed by settings and upload UI.
func (b *Backend) GetImageHostPolicyMetadata() (imagehostpolicy.Metadata, error) {
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		return imagehostpolicy.Metadata{}, fmt.Errorf("webserver: tracker registry: %w", err)
	}
	metadata := imagehostpolicy.Metadata{
		UploadHosts:        imagehostpolicy.KnownUploadHosts(),
		TrackerUploadHosts: make(map[string][]string),
		OwnedHosts:         make(map[string]string),
	}
	for _, tracker := range registry.Names() {
		policy, ok := registry.LookupImageHostPolicy(tracker)
		if !ok {
			continue
		}
		hosts := append([]string(nil), policy.AllowedHosts...)
		if host := strings.ToLower(strings.TrimSpace(policy.ConditionalHost)); host != "" {
			hosts = append(hosts, host)
		}
		uploadHosts := make([]string, 0, len(hosts))
		for _, host := range hosts {
			normalized := strings.ToLower(strings.TrimSpace(host))
			if imagehostpolicy.IsUploadHost(normalized) && !slices.Contains(uploadHosts, normalized) {
				uploadHosts = append(uploadHosts, normalized)
			}
		}
		if len(uploadHosts) > 0 {
			metadata.TrackerUploadHosts[tracker] = uploadHosts
		}
		for _, host := range policy.OwnedHosts {
			metadata.OwnedHosts[strings.ToLower(strings.TrimSpace(host))] = tracker
		}
	}
	return metadata, nil
}

// ListHistory returns persisted release history in repository-defined order.
func (b *Backend) ListHistory() ([]api.HistoryEntry, error) {
	rt := b.runtimeSnapshot()
	history, err := rt.historyCore()
	if err != nil {
		return nil, err
	}
	return wrapWebResult(history.ListHistory(context.Background()))
}

// GetHistoryOverview returns persisted upload and asset detail for one source path.
func (b *Backend) GetHistoryOverview(sourcePath string) (api.HistoryOverview, error) {
	rt := b.runtimeSnapshot()
	history, err := rt.historyCore()
	if err != nil {
		return api.HistoryOverview{}, err
	}
	return wrapWebResult(history.GetHistoryOverview(context.Background(), sourcePath))
}

// DeleteHistoryRelease purges persisted history and managed artifacts for one source path.
func (b *Backend) DeleteHistoryRelease(sourcePath string) error {
	rt, err := b.requireRuntime()
	if err != nil {
		return err
	}
	historyCore, err := rt.historyCore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebError(historyCore.DeleteHistoryRelease(ctx, strings.TrimSpace(sourcePath)))
}

// GetLogPath returns the host filesystem path of the active application log.
func (b *Backend) GetLogPath() (string, error) {
	return wrapWebResult(logging.LogPath(b.currentConfig().MainSettings.DBPath))
}

// GetRecentLogs returns up to limit sanitized recent log entries.
func (b *Backend) GetRecentLogs(limit int) ([]logging.Entry, error) {
	logger := b.currentLogger()
	if logger == nil {
		return nil, errors.New("logger not initialized")
	}
	return logger.Recent(limit), nil
}

// GetLogExclusions returns persisted frontend log-filter patterns.
func (b *Backend) GetLogExclusions() ([]string, error) {
	if b.repo == nil {
		return nil, errors.New("config repository not initialized")
	}
	var exclusions logExclusions
	err := config.LoadSectionFromDatabase(context.Background(), "log_exclusions", &exclusions, b.repo)
	if err != nil {
		if errorsIsNotFound(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("web: %w", err)
	}
	return normalizePatterns(exclusions.Patterns), nil
}

// UpdateLogExclusions validates and persists frontend log-filter patterns.
func (b *Backend) UpdateLogExclusions(patterns []string) error {
	if b.repo == nil {
		return errors.New("config repository not initialized")
	}
	return wrapWebError(config.SaveSectionToDatabase(context.Background(), "log_exclusions", logExclusions{
		Patterns: normalizePatterns(patterns),
	}, b.repo))
}

// StartLogStream subscribes the browser session to live log events. Active
// streams are rebound when settings replace the runtime logger. If no logger or
// event hub is installed, it returns an error without registering a stream.
func (b *Backend) StartLogStream(sessionID string) (string, error) {
	streamID, err := randomString(12)
	if err != nil {
		return "", err
	}
	if b == nil {
		return "", errors.New("logger not initialized")
	}
	if b.hub == nil {
		return "", errors.New("event hub not initialized")
	}

	b.runtimeMu.RLock()
	logger := b.logger
	if logger == nil {
		b.runtimeMu.RUnlock()
		return "", errors.New("logger not initialized")
	}
	session := &backendLogStream{
		id:        streamID,
		sessionID: sessionID,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	b.streamMu.Lock()
	b.streams[streamID] = session
	b.startLogStreamWorker(session, logger)
	b.streamMu.Unlock()
	b.runtimeMu.RUnlock()

	return streamID, nil
}

// startLogStreamWorker subscribes session to logger and forwards entries to
// the browser event hub until the stream is stopped.
func (b *Backend) startLogStreamWorker(session *backendLogStream, logger *logging.Logger) {
	subID, ch := logger.Subscribe(0)
	stop := session.stop
	done := session.done
	session.logger = logger
	session.subID = subID

	b.streamWG.Go(func() {
		defer close(done)
		for {
			select {
			case entry, ok := <-ch:
				if !ok {
					return
				}
				b.hub.Emit(session.sessionID, "log:stream:"+session.id, entry)
			case <-stop:
				logger.Unsubscribe(subID)
				return
			}
		}
	})
}

// rebindLogStreams moves streams attached to oldLogger onto newLogger without
// changing their browser-visible stream IDs.
func (b *Backend) rebindLogStreams(oldLogger *logging.Logger, newLogger *logging.Logger) {
	if b == nil || oldLogger == nil || newLogger == nil || oldLogger == newLogger {
		return
	}

	type stoppedStream struct {
		session *backendLogStream
		done    <-chan struct{}
	}

	b.streamMu.Lock()
	stopped := make([]stoppedStream, 0, len(b.streams))
	for _, session := range b.streams {
		if session == nil || session.logger != oldLogger {
			continue
		}
		stopped = append(stopped, stoppedStream{
			session: session,
			done:    session.done,
		})
		select {
		case <-session.stop:
		default:
			close(session.stop)
		}
	}
	b.streamMu.Unlock()

	for _, stream := range stopped {
		if stream.done != nil {
			<-stream.done
		}
	}

	b.streamMu.Lock()
	for _, stream := range stopped {
		session := stream.session
		if session == nil || b.streams[session.id] != session {
			continue
		}
		session.stop = make(chan struct{})
		session.done = make(chan struct{})
		b.startLogStreamWorker(session, newLogger)
	}
	b.streamMu.Unlock()
}

// StopLogStream stops streamID only when it belongs to sessionID.
// Unknown streams and streams owned by other sessions are treated as no-ops.
func (b *Backend) StopLogStream(sessionID string, streamID string) error {
	trimmedSessionID := strings.TrimSpace(sessionID)
	b.streamMu.Lock()
	session := b.streams[streamID]
	if session != nil && strings.TrimSpace(session.sessionID) != trimmedSessionID {
		session = nil
	}
	if session != nil {
		delete(b.streams, streamID)
		select {
		case <-session.stop:
		default:
			close(session.stop)
		}
	}
	b.streamMu.Unlock()
	if session != nil {
		<-session.done
	}
	return nil
}

// StopSessionLogStreams closes all active log streams owned by sessionID.
func (b *Backend) StopSessionLogStreams(sessionID string) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return
	}

	b.streamMu.Lock()
	streamIDs := make([]string, 0)
	for id, stream := range b.streams {
		if stream != nil && stream.sessionID == trimmedSessionID {
			streamIDs = append(streamIDs, id)
		}
	}
	b.streamMu.Unlock()

	for _, streamID := range streamIDs {
		_ = b.StopLogStream(trimmedSessionID, streamID)
	}
}

func (b *Backend) buildRunOptions(noSeed bool, runLogLevel string) (runOptions, error) {
	if strings.TrimSpace(runLogLevel) == "" {
		return runOptions{NoSeed: noSeed}, nil
	}
	normalized, err := api.ParseLogLevel(runLogLevel)
	if err != nil {
		return runOptions{}, fmt.Errorf("web: %w", err)
	}
	return runOptions{
		NoSeed:      noSeed,
		RunLogLevel: normalized,
	}, nil
}

// buildRunCoreFromSnapshot creates a per-run core and logger from the same
// runtime snapshot used to build upload options. The transient core skips
// startup-only legacy cookie migration while sharing the backend repository.
// The capability bundle borrows the core; callers must close the returned
// owner and logger separately unless they transfer both to a job.
func (b *Backend) buildRunCoreFromSnapshot(
	ctx context.Context,
	rt backendRuntimeSnapshot,
	opts runOptions,
) (CoreCapabilities, LifecycleOwner, *logging.Logger, error) {
	if err := ctx.Err(); err != nil {
		return CoreCapabilities{}, nil, nil, fmt.Errorf("web: build run core canceled: %w", err)
	}
	effectiveLogLevel := logging.ResolveEffectiveLevel(rt.cfg.Logging.Level, opts.RunLogLevel, false)
	logger, err := logging.NewWithLevel(rt.cfg.Logging, rt.cfg.MainSettings.DBPath, effectiveLogLevel)
	if err != nil {
		return CoreCapabilities{}, nil, nil, fmt.Errorf("web: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = logger.Close()
		return CoreCapabilities{}, nil, nil, fmt.Errorf("web: build run core canceled: %w", err)
	}
	coreSvc, err := core.NewWithContext(ctx, api.CoreDependencies{
		Config: rt.cfg,
		Logger: logger,
		Services: api.ServiceSet{
			Filesystem: filesystem.NewValidator(),
		},
		Repository:          b.repo.RepositoryCapabilities(),
		RepositoryOwner:     b.repo,
		SkipCookieMigration: true,
	})
	if err != nil {
		_ = logger.Close()
		return CoreCapabilities{}, nil, nil, fmt.Errorf("web: %w", err)
	}
	capabilities, owner := BindCoreCapabilities(coreSvc)
	return capabilities, owner, logger, nil
}

func buildRunUploadOptions(cfg config.Config, opts runOptions) api.UploadOptions {
	options := buildBaseMetadataOptions(cfg)
	options.NoSeed = opts.NoSeed
	options.RunLogLevel = opts.RunLogLevel
	return options
}

func buildBaseMetadataOptions(cfg config.Config) api.UploadOptions {
	return api.UploadOptions{
		Screens:         cfg.ScreenshotHandling.Screens,
		SkipAutoTorrent: cfg.Metadata.SkipAutoTorrent,
		OnlyID:          cfg.Metadata.OnlyID,
		KeepImages:      cfg.Metadata.KeepImages,
	}
}

func (b *Backend) isPathWithinManagedDirs(candidate string) bool {
	tmpDir, err := db.Subdir(b.currentConfig().MainSettings.DBPath, "tmp")
	if err == nil && pathutil.IsWithinRoot(tmpDir, candidate) {
		return true
	}
	logPath, err := logging.LogPath(b.currentConfig().MainSettings.DBPath)
	if err == nil && pathutil.IsWithinRoot(filepath.Dir(logPath), candidate) {
		return true
	}
	return false
}

func resolveContentTmpRoot(tmpRoot string, candidate string) (string, bool) {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return "", false
	}
	absCandidate, err := filepath.Abs(trimmed)
	if err != nil {
		return "", false
	}
	absTmpRoot, err := filepath.Abs(strings.TrimSpace(tmpRoot))
	if err != nil {
		return "", false
	}
	if !pathutil.IsWithinRoot(absTmpRoot, absCandidate) {
		return "", false
	}
	rel, err := filepath.Rel(absTmpRoot, absCandidate)
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" || parts[0] == "." {
		return "", false
	}
	return filepath.Join(absTmpRoot, parts[0]), true
}

func removeIfWithinRoot(root string, target string, recursive bool) error {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return nil
	}
	absRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return fmt.Errorf("cleanup path: resolve root path: %w", err)
	}
	absTarget, err := filepath.Abs(trimmed)
	if err != nil {
		return fmt.Errorf("cleanup path: resolve target path: %w", err)
	}
	if pathutil.SamePath(absRoot, absTarget) || !pathutil.IsWithinRoot(absRoot, absTarget) {
		return nil
	}
	if recursive {
		if _, err := os.Stat(absTarget); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("cleanup path: stat target: %w", err)
		}
		if err := os.RemoveAll(absTarget); err != nil {
			return fmt.Errorf("cleanup path: remove target tree: %w", err)
		}
		return nil
	}
	if err := os.Remove(absTarget); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cleanup path: remove target: %w", err)
	}
	return nil
}

func errorsIsNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}
