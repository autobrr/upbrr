// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path" //nolint:depguard // Builds URL paths, not local filesystem paths.
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/description"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/imagehosting"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/dvdmenus"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/internal/torrentclient"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

// Core composes the upload, prepared-release, duplicate-check, media,
// description, and history capabilities over one dependency snapshot. It owns
// the repository only when construction opened that repository internally.
// Operation contexts are per call and are not retained by Core.
type Core struct {
	logger    api.Logger
	repoOwner api.RepositoryOwner
	ownsRepo  bool

	history       *historyModule
	preparedFacts *preparedrelease.Module
	workflow      *releaseworkflow.Module
	media         *mediaModule
}

func workflowPrivateVaultRoot(dbPath string) string {
	dbPath = filepath.Clean(strings.TrimSpace(dbPath))
	sum := sha256.Sum256([]byte(dbPath))
	return filepath.Join(filepath.Dir(dbPath), "workflow-private", hex.EncodeToString(sum[:16]))
}

func applySkipAutoTorrentDefault(input api.PrepareInput, configured bool) api.PrepareInput {
	input.Search.Skip = input.Search.Skip || configured
	return input
}

// NewWithContext constructs a Core and applies ctx to initialization work such
// as opening and migrating an internally created repository. The context is not
// retained after construction.
func NewWithContext(ctx context.Context, deps api.CoreDependencies) (*Core, error) {
	if ctx == nil {
		return nil, errors.New("core: context is required")
	}
	return newCore(ctx, deps)
}

func newCore(ctx context.Context, deps api.CoreDependencies) (*Core, error) {
	return newCoreWithHooks(ctx, deps, coreConstructionHooks{})
}

type coreConstructionHooks struct {
	closeRepository func(api.RepositoryOwner) error
}

type metadataRepositoryView struct {
	api.ReleaseStateRepository
	api.ReleaseSelectionRepository
	api.TrackerStateRepository
}

type trackerRepositoryView struct {
	api.ReleaseSelectionRepository
	api.UploadLedgerRepository
	api.TrackerStateRepository
	api.MediaAssetRepository
}

type mediaRepositoryView struct {
	api.TrackerStateRepository
	api.MediaAssetRepository
}

// newCoreWithHooks constructs the runtime graph and closes only repositories it
// opened itself when construction fails. Hooks expose that cleanup to tests.
func newCoreWithHooks(ctx context.Context, deps api.CoreDependencies, hooks coreConstructionHooks) (*Core, error) {
	if ctx == nil {
		return nil, errors.New("core: context is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = api.NopLogger{}
	}
	logger.Infof("core: initializing")

	var cfg config.Config
	switch typed := deps.Config.(type) {
	case nil:
		return nil, errors.New("core: config is required")
	case config.Config:
		cfg = typed
	case *config.Config:
		if typed == nil {
			return nil, errors.New("core: config is required")
		}
		cfg = *typed
	default:
		return nil, fmt.Errorf("core: unsupported config type %T", deps.Config)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}

	repositories := deps.Repository
	repoOwner := deps.RepositoryOwner
	ownsRepo := false
	constructionSucceeded := false
	defer func() {
		if constructionSucceeded || !ownsRepo || repoOwner == nil {
			return
		}
		if hooks.closeRepository != nil {
			_ = hooks.closeRepository(repoOwner)
			return
		}
		_ = repoOwner.Close()
	}()
	if repositories.IsZero() {
		logger.Debugf("core: opening repository")
		sqliteRepo, err := db.OpenWithLoggerContext(ctx, cfg.MainSettings.DBPath, logger)
		if err != nil {
			return nil, fmt.Errorf("core: %w", err)
		}
		repositories = sqliteRepo.RepositoryCapabilities()
		repoOwner = sqliteRepo
		ownsRepo = true
		if err := sqliteRepo.MigrateContext(ctx); err != nil {
			return nil, fmt.Errorf("core: %w", err)
		}
	}
	if err := repositories.Validate(); err != nil {
		return nil, fmt.Errorf("core: repository capabilities: %w", err)
	}
	if sqliteRepo, ok := repoOwner.(*db.SQLiteRepository); ok && !deps.SkipCookieMigration {
		if err := migrateLegacyCookies(ctx, sqliteRepo.RawDB(), cfg.MainSettings.DBPath, logger); err != nil {
			logger.Warnf("core: cookie migration failed: %v (continuing)", err)
		}
	}

	services := deps.Services
	if err := maybeApplyE2EServices(ctx, &services, cfg, repositories, logger); err != nil {
		return nil, err
	}
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		return nil, fmt.Errorf("core: tracker registry: %w", err)
	}
	if services.Clients == nil {
		services.Clients = torrentclient.NewServiceWithRegistry(cfg, logger, registry)
	}
	clientDiscovery := clientdiscovery.New(services.Clients, logger)
	if services.Metadata == nil {
		if _, err := metadata.EnsureDefaultTagOverrides(cfg.MainSettings.DBPath); err != nil {
			return nil, fmt.Errorf("core: default tag overrides: %w", err)
		}
		bdinfoService := bdinfo.New(logger)

		services.Metadata = metadata.NewService(
			metadataRepositoryView{
				ReleaseStateRepository:     repositories.ReleaseState(),
				ReleaseSelectionRepository: repositories.Selections(),
				TrackerStateRepository:     repositories.Trackers(),
			},
			metadata.WithTagsPathFromDB(cfg.MainSettings.DBPath),
			metadata.WithLogger(logger),
			metadata.WithSRRDBPaths(cfg.MainSettings.DBPath),
			metadata.WithConfig(cfg),
			metadata.WithBDInfoService(bdinfoService),
			metadata.WithTrackerRegistry(registry),
			metadata.WithClientDiscovery(clientDiscovery),
		)
	}
	if services.Torrents == nil {
		tmpDir, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
		if err != nil {
			return nil, fmt.Errorf("core: tmp dir: %w", err)
		}
		services.Torrents = torrent.NewServiceWithRegistry(logger, tmpDir, registry)
	}
	if services.Screenshots == nil {
		tmpDir, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
		if err != nil {
			return nil, fmt.Errorf("core: tmp dir: %w", err)
		}
		services.Screenshots = screenshots.NewServiceWithRepo(
			cfg,
			logger,
			tmpDir,
			nil,
			mediaRepositoryView{TrackerStateRepository: repositories.Trackers(), MediaAssetRepository: repositories.Media()},
		)
	}
	if services.DVDMenus == nil {
		tmpDir, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
		if err != nil {
			return nil, fmt.Errorf("core: tmp dir: %w", err)
		}
		services.DVDMenus = dvdmenus.NewService(logger, tmpDir, repositories.Media())
	}
	if services.Images == nil {
		services.Images = imagehosting.NewServiceWithRegistry(cfg, logger, repositories.Media(), registry)
	}
	if services.Trackers == nil {
		services.Trackers = trackers.NewServiceWithRegistryAndImages(
			cfg,
			logger,
			trackerRepositoryView{
				ReleaseSelectionRepository: repositories.Selections(),
				UploadLedgerRepository:     repositories.Uploads(),
				TrackerStateRepository:     repositories.Trackers(),
				MediaAssetRepository:       repositories.Media(),
			},
			registry,
			services.Images,
		)
	}
	if services.Filesystem == nil {
		services.Filesystem = filesystem.NewValidatorWithLogger(logger)
	}
	if services.Dupes == nil {
		services.Dupes = dupechecking.NewServiceWithRegistry(cfg, logger, registry)
	}
	if services.TrackerAuth == nil {
		services.TrackerAuth = trackerauth.NewServiceWithRegistryAndLogger(cfg, registry, logger)
	}
	logger.Infof("core: initialized services")
	evidencePipeline, ok := services.Metadata.(preparedrelease.EvidencePipeline)
	if !ok {
		return nil, errors.New("core: metadata service does not implement canonical evidence collection")
	}
	collector, err := preparedrelease.NewEvidenceCollector(evidencePipeline)
	if err != nil {
		return nil, fmt.Errorf("core: canonical preparation collector: %w", err)
	}
	identityResolver, err := externalidentity.NewWithCandidateSource(repositories.ReleaseState(), collector)
	if err != nil {
		return nil, fmt.Errorf("core: canonical identity resolver: %w", err)
	}
	preparedFacts, err := preparedrelease.New(repositories.Prepared(), identityResolver, collector)
	if err != nil {
		return nil, fmt.Errorf("core: canonical preparation: %w", err)
	}
	trackerWorkflowProjector, err := trackers.NewWorkflowProjector(registry, cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("core: tracker workflow projector: %w", err)
	}
	workflowRepository, err := releaseworkflow.NewPersistentRepository(repositories.Workflows())
	if err != nil {
		return nil, fmt.Errorf("core: release workflow repository: %w", err)
	}
	workflowPreparer := releaseworkflow.ReleasePreparerFunc{
		PrepareFunc: func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			return preparedFacts.Prepare(ctx, applySkipAutoTorrentDefault(input, cfg.Metadata.SkipAutoTorrent))
		},
		DisplayFunc: func(ctx context.Context, ref api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
			display, displayErr := preparedFacts.ResolveDisplay(ctx, ref)
			if displayErr != nil {
				return api.PreparedReleaseDisplay{}, fmt.Errorf("workflow resolve prepared release display: %w", displayErr)
			}
			records, loadErr := repositories.Trackers().ListTrackerMetadataByPath(ctx, ref.SourcePath)
			if loadErr != nil && !errors.Is(loadErr, internalerrors.ErrNotFound) {
				return api.PreparedReleaseDisplay{}, fmt.Errorf("workflow display tracker data: %w", loadErr)
			}
			display.TrackerData = buildTrackerPreview(records, cfg)
			return display, nil
		},
		SubjectFunc:   preparedFacts.ResolveUploadSubject,
		DuplicateFunc: preparedFacts.ResolveDuplicateSubject,
	}
	workflowMedia := newMediaModule(
		cfg,
		logger,
		services,
		mediaRepositoryView{TrackerStateRepository: repositories.Trackers(), MediaAssetRepository: repositories.Media()},
		registry,
		preparedFacts,
	)
	workflowMediaArtifacts := workflowMediaBuilder{
		config:      cfg,
		resolver:    preparedFacts,
		screenshots: services.Screenshots,
		dvdMenus:    services.DVDMenus,
		media:       workflowMedia,
	}
	var workflowPrivateResources releaseworkflow.PrivateResourceStore = releaseworkflow.NewMemoryPrivateResourceStore()
	if sqliteRepo, ok := repoOwner.(*db.SQLiteRepository); ok && strings.TrimSpace(sqliteRepo.DBPath()) != "" {
		vault, vaultErr := releaseworkflow.NewPrivateArtifactVault(
			workflowPrivateVaultRoot(sqliteRepo.DBPath()),
			workflowPrivateResourceCodecs(workflowMediaArtifacts)...,
		)
		if vaultErr != nil {
			return nil, fmt.Errorf("core: release workflow private artifact vault: %w", vaultErr)
		}
		workflowPrivateResources = vault
	}
	e2eOptions := e2eReleaseWorkflowOptions()
	workflowOptions := make([]releaseworkflow.Option, 0, 9+len(e2eOptions))
	workflowOptions = append(workflowOptions,
		releaseworkflow.WithTrackerProjectionBuilder(trackerWorkflowProjector),
		releaseworkflow.WithTrackerPreflightBuilder(workflowPreflightBuilder{
			auth:     services.TrackerAuth,
			config:   cfg,
			registry: registry,
			logger:   logger,
			banned:   trackers.NewBannedGroupCheckerWithRegistry(cfg.MainSettings.DBPath, registry),
		}),
		releaseworkflow.WithDupeAssessmentBuilder(workflowDupeBuilder{service: services.Dupes, logger: logger}),
		releaseworkflow.WithMediaArtifactBuilder(workflowMediaArtifacts),
		releaseworkflow.WithDescriptionBuilder(workflowDescriptionBuilder{
			resolver: preparedFacts,
			trackers: services.Trackers,
		}),
		releaseworkflow.WithUploadPlanBuilder(newWorkflowUploadPlanBuilder(cfg, preparedFacts, services.Trackers, services.Torrents, services.Clients)),
		releaseworkflow.WithOperationErrorClassifier(classifyOperationError),
		releaseworkflow.WithLogger(logger),
	)
	workflowOptions = append(workflowOptions, e2eOptions...)
	workflow, err := releaseworkflow.New(
		workflowRepository,
		workflowPrivateResources,
		workflowPreparer,
		workflowOptions...,
	)
	if err != nil {
		return nil, fmt.Errorf("core: release workflow: %w", err)
	}

	core := &Core{
		logger:        logger,
		repoOwner:     repoOwner,
		ownsRepo:      ownsRepo,
		preparedFacts: preparedFacts,
		workflow:      workflow,
	}
	core.history = newHistoryModule(repositories.History(), cfg.MainSettings.DBPath, logger)
	core.history.preparedFacts = core.preparedFacts
	core.media = workflowMedia
	constructionSucceeded = true
	return core, nil
}

// ContinueReleaseWorkflow reconciles typed desired state through the central planner.
func (c *Core) ContinueReleaseWorkflow(
	ctx context.Context,
	ownerID string,
	request api.ContinueReleaseWorkflowRequest,
) (releaseworkflow.CommandResult, error) {
	result, err := c.workflow.Continue(ctx, ownerID, request)
	return result, classifyOperationError(api.OperationKindUnknown, err)
}

// StartReleaseWorkflowUpload starts or idempotently replays one owner-scoped
// durable composite upload.
func (c *Core) StartReleaseWorkflowUpload(
	ctx context.Context,
	ownerID string,
	request api.CreateReleaseWorkflowUploadRequest,
) (releaseworkflow.CommandResult, error) {
	result, err := c.workflow.StartUpload(ctx, ownerID, request)
	return result, classifyOperationError(api.OperationKindUploadExecute, err)
}

// SubmitReleaseWorkflowUploadFeedback applies one exact required-action response
// and starts the resumed composite operation.
func (c *Core) SubmitReleaseWorkflowUploadFeedback(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	feedback api.ReleaseWorkflowUploadFeedback,
) (releaseworkflow.CommandResult, error) {
	result, err := c.workflow.SubmitUploadFeedback(ctx, ownerID, workflowID, feedback)
	return result, classifyOperationError(api.OperationKindUploadExecute, err)
}

// ExecuteReleaseWorkflow applies one owner-scoped typed workflow command.
func (c *Core) ExecuteReleaseWorkflow(
	ctx context.Context,
	ownerID string,
	command releaseworkflow.Command,
) (releaseworkflow.CommandResult, error) {
	result, err := c.workflow.Execute(ctx, ownerID, command)
	return result, classifyOperationError(releaseWorkflowOperation(command), err)
}

// StartReleaseWorkflow durably accepts one long-running workflow command.
func (c *Core) StartReleaseWorkflow(
	ctx context.Context,
	ownerID string,
	command releaseworkflow.Command,
) (api.WorkflowOperationStatus, error) {
	operation, err := c.workflow.Start(ctx, ownerID, command)
	if err != nil {
		return api.WorkflowOperationStatus{}, classifyOperationError(releaseWorkflowOperation(command), err)
	}
	return operation, nil
}

func releaseWorkflowOperation(command releaseworkflow.Command) api.OperationKind {
	switch command.(type) {
	case releaseworkflow.CreateWorkflowCommand,
		releaseworkflow.ReplaceFactInstructionsCommand,
		releaseworkflow.PrepareReleaseCommand:
		return api.OperationKindPreparation
	case releaseworkflow.CheckDuplicatesCommand,
		releaseworkflow.DecideDuplicatesCommand:
		return api.OperationKindDuplicateCheck
	case releaseworkflow.CaptureMediaCommand,
		releaseworkflow.SetMediaSelectionCommand,
		releaseworkflow.DeleteMediaArtifactsCommand,
		releaseworkflow.ReorderMediaArtifactsCommand,
		releaseworkflow.AttachMediaArtifactsCommand,
		releaseworkflow.RemoveHostedImagesCommand:
		return api.OperationKindMedia
	case releaseworkflow.UploadMediaImagesCommand:
		return api.OperationKindImageHosting
	case releaseworkflow.GenerateDescriptionsCommand:
		return api.OperationKindDescription
	case releaseworkflow.ExecuteUploadsCommand, releaseworkflow.RetryFailedUploadsCommand:
		return api.OperationKindUploadExecute
	case releaseworkflow.RetryClientInjectionsCommand:
		return api.OperationKindClientInjection
	case releaseworkflow.ApproveTrackersCommand,
		releaseworkflow.CancelWorkflowCommand:
		return api.OperationKindUnknown
	default:
		return api.OperationKindUploadDryRun
	}
}

// OpenReleaseWorkflowMediaArtifact returns one owner-scoped opaque workflow
// media resource without exposing its retained filesystem path.
func (c *Core) OpenReleaseWorkflowMediaArtifact(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	media api.MediaArtifactSetRef,
	artifactID api.PublicResourceID,
) (releaseworkflow.MediaArtifactContent, error) {
	content, err := c.workflow.MediaArtifact(ctx, ownerID, workflowID, media, artifactID)
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, classifyOperationError(api.OperationKindMedia, err)
	}
	return content, nil
}

// ReleaseWorkflowMediaPlan returns the safe media plan for the workflow's
// current exact release and tracker projections.
func (c *Core) ReleaseWorkflowMediaPlan(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) (api.MediaPlan, error) {
	plan, err := c.workflow.MediaPlan(ctx, ownerID, workflowID)
	if err != nil {
		return api.MediaPlan{}, classifyOperationError(api.OperationKindMedia, err)
	}
	return plan, nil
}

// PreviewReleaseWorkflowFrame creates one owner-scoped non-authoritative frame preview.
func (c *Core) PreviewReleaseWorkflowFrame(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	expectedRevision api.WorkflowRevision,
	timestampSeconds float64,
) (api.FramePreview, error) {
	preview, err := c.workflow.PreviewFrame(ctx, ownerID, workflowID, expectedRevision, timestampSeconds)
	if err != nil {
		return api.FramePreview{}, classifyOperationError(api.OperationKindMedia, err)
	}
	return preview, nil
}

// OpenReleaseWorkflowPreview returns one owner-scoped opaque transient preview.
func (c *Core) OpenReleaseWorkflowPreview(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	previewID api.PublicResourceID,
) (releaseworkflow.MediaArtifactContent, error) {
	content, err := c.workflow.PreviewArtifact(ctx, ownerID, workflowID, previewID)
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, classifyOperationError(api.OperationKindMedia, err)
	}
	return content, nil
}

// StageReleaseWorkflowMediaResource retains private image bytes for a later
// exact workflow attachment command.
func (c *Core) StageReleaseWorkflowMediaResource(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	expectedRevision api.WorkflowRevision,
	content releaseworkflow.StagedMediaContent,
) (api.WorkflowResourceRef, error) {
	resource, err := c.workflow.StageMediaResource(ctx, ownerID, workflowID, expectedRevision, content)
	if err != nil {
		return api.WorkflowResourceRef{}, classifyOperationError(api.OperationKindMedia, err)
	}
	return resource, nil
}

// CurrentReleaseWorkflow returns the current aggregate and immutable stage projections.
func (c *Core) CurrentReleaseWorkflow(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
) (releaseworkflow.CommandResult, error) {
	result, err := c.workflow.Current(ctx, ownerID, workflowID)
	if err != nil {
		return releaseworkflow.CommandResult{}, fmt.Errorf("core: current release workflow: %w", err)
	}
	return result, nil
}

// ReleaseWorkflowOperation returns one pollable workflow operation.
func (c *Core) ReleaseWorkflowOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	operation, err := c.workflow.Operation(ctx, ownerID, workflowID, operationID)
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("core: release workflow operation: %w", err)
	}
	return operation, nil
}

// ReleaseWorkflowOperationEvents returns retained operation events after one workflow-global cursor.
func (c *Core) ReleaseWorkflowOperationEvents(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	after uint64,
	limit int,
) ([]api.WorkflowEvent, error) {
	events, err := c.workflow.OperationEvents(ctx, ownerID, workflowID, operationID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("core: release workflow operation events: %w", err)
	}
	return events, nil
}

// CancelReleaseWorkflowOperation requests cancellation of one active operation.
func (c *Core) CancelReleaseWorkflowOperation(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	operation, err := c.workflow.CancelOperation(ctx, ownerID, workflowID, operationID)
	if err != nil {
		return api.WorkflowOperationStatus{}, fmt.Errorf("core: cancel release workflow operation: %w", err)
	}
	return operation, nil
}

// DiscoverPlaylists scans the local source for Blu-ray playlists and returns
// them by descending score. Durations are seconds and item sizes are bytes.
func (c *Core) DiscoverPlaylists(ctx context.Context, sourcePath string) ([]api.PlaylistInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("core: discover playlists canceled: %w", err)
	}
	if strings.TrimSpace(sourcePath) == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	c.logger.Debugf("core: discovering playlists in %q", sourcePath)
	layout, err := sourcelayout.Resolve(ctx, sourcePath)
	if err != nil {
		return nil, classifyOperationError(api.OperationKindPreparation, fmt.Errorf("core: resolve playlist source: %w", err))
	}
	if strings.TrimSpace(layout.BDMVRoot) == "" {
		return nil, classifyOperationError(api.OperationKindPreparation, &api.InvalidPlaylistSelectionError{
			SourcePath: layout.SourcePath,
			Reason:     "source is not a Blu-ray disc",
		})
	}

	playlists, err := filesystem.DiscoverPlaylists(ctx, layout.BDMVRoot)
	if err != nil {
		c.logger.Warnf("core: discover playlists failed: %v", err)
		return nil, classifyOperationError(api.OperationKindPreparation, fmt.Errorf("core: discover playlists: %w", err))
	}

	// Convert filesystem types to API types.
	var result []api.PlaylistInfo
	for _, p := range playlists {
		var items []api.PlaylistItem
		for _, item := range p.Items {
			items = append(items, api.PlaylistItem{
				File: item.File,
				Size: item.Size,
			})
		}
		result = append(result, api.PlaylistInfo{
			File:     p.File,
			Duration: p.Duration,
			Items:    items,
			Score:    p.Score,
			Edition:  p.Edition,
		})
	}

	c.logger.Infof("core: discovered %d playlists", len(result))
	return result, nil
}

// ListHistory returns stored releases with their latest display status.
func (c *Core) ListHistory(ctx context.Context) ([]api.HistoryEntry, error) {
	return c.history.List(ctx)
}

// GetHistoryOverview assembles persisted metadata, overrides, media, tracker
// state, and upload history for one source path.
func (c *Core) GetHistoryOverview(ctx context.Context, sourcePath string) (api.HistoryOverview, error) {
	return c.history.Overview(ctx, sourcePath)
}

// DeleteHistoryRelease purges a stored source and related stored child paths,
// removing only artifacts validated beneath the configured tmp, cache, or nfo
// roots.
func (c *Core) DeleteHistoryRelease(ctx context.Context, sourcePath string) error {
	return c.history.Delete(ctx, sourcePath)
}

// DeleteAllHistoryReleases deletes stored releases sequentially. On cancellation
// or failure, the returned count includes releases deleted before the error.
func (c *Core) DeleteAllHistoryReleases(ctx context.Context) (int, error) {
	return c.history.DeleteAll(ctx)
}

// Close closes the repository only when this Core opened and owns it.
func (c *Core) Close() error {
	if c == nil {
		return nil
	}
	if c.repoOwner == nil || !c.ownsRepo {
		return nil
	}
	return wrapCoreError(c.repoOwner.Close())
}

// RenderDescription renders raw BBCode after rejecting a pre-canceled context.
func (c *Core) RenderDescription(ctx context.Context, raw string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", classifyOperationError(api.OperationKindDescription, fmt.Errorf("core: render description canceled: %w", err))
	}
	return description.Render(raw), nil
}

func buildTrackerPreview(records []api.TrackerMetadata, cfg config.Config) []api.TrackerPreview {
	if len(records) == 0 {
		return nil
	}
	chooseBest := func(existing api.TrackerMetadata, candidate api.TrackerMetadata) api.TrackerMetadata {
		existingTime := existing.UpdatedAt
		candidateTime := candidate.UpdatedAt
		if !candidateTime.IsZero() && (existingTime.IsZero() || candidateTime.After(existingTime)) {
			return candidate
		}
		if existingTime.IsZero() && !candidateTime.IsZero() {
			return candidate
		}
		if len(candidate.ImageURLs) > len(existing.ImageURLs) {
			return candidate
		}
		if len(candidate.Description) > len(existing.Description) {
			return candidate
		}
		if candidate.Matched && !existing.Matched {
			return candidate
		}
		return existing
	}
	byTracker := make(map[string]api.TrackerMetadata, len(records))
	orderedKeys := make([]string, 0, len(records))
	for _, record := range records {
		key := strings.ToUpper(strings.TrimSpace(record.Tracker))
		if key == "" {
			key = fmt.Sprintf("unknown-%d", len(orderedKeys))
		}
		if existing, ok := byTracker[key]; ok {
			byTracker[key] = chooseBest(existing, record)
			continue
		}
		byTracker[key] = record
		orderedKeys = append(orderedKeys, key)
	}

	result := make([]api.TrackerPreview, 0, len(byTracker))
	for _, key := range orderedKeys {
		record := byTracker[key]
		preview := api.TrackerPreview{
			Tracker:         record.Tracker,
			TrackerID:       record.TrackerID,
			TorrentURL:      trackerTorrentURL(cfg, record.Tracker, record.TrackerID),
			InfoHash:        record.InfoHash,
			TMDBID:          record.TMDBID,
			IMDBID:          record.IMDBID,
			TVDBID:          record.TVDBID,
			MALID:           record.MALID,
			Category:        string(record.Category),
			Description:     record.Description,
			DescriptionHTML: description.Render(record.Description),
			ImageURLs:       append([]string{}, record.ImageURLs...),
			Filename:        record.Filename,
			Matched:         record.Matched,
		}
		if !record.UpdatedAt.IsZero() {
			preview.UpdatedAt = record.UpdatedAt.UTC().Format(time.RFC3339)
		}
		result = append(result, preview)
	}
	return result
}

func trackerTorrentURL(cfg config.Config, tracker string, trackerID string) string {
	if strings.TrimSpace(tracker) == "" || strings.TrimSpace(trackerID) == "" {
		return ""
	}
	base := trackerBaseURL(cfg, tracker)
	if base == "" {
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	parsed.Path = path.Join("/", "torrents", trackerID)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func trackerBaseURL(cfg config.Config, tracker string) string {
	if strings.TrimSpace(tracker) == "" {
		return ""
	}
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(name, tracker) {
			return baseFromAnnounce(entry.AnnounceURL)
		}
	}
	return ""
}

func baseFromAnnounce(announce string) string {
	trimmed := strings.TrimSpace(announce)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	parsed.Path = "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// migrateLegacyCookies performs automatic migration of cookies from file-based storage
// to the encrypted database. This is called during core initialization if needed.
func migrateLegacyCookies(ctx context.Context, sqliteDB *sql.DB, dbPath string, logger api.Logger) error {
	if sqliteDB == nil {
		return errors.New("database connection is required for cookie migration")
	}

	if err := cookies.SyncCookieEncryptionWithAuth(ctx, sqliteDB, dbPath); err != nil {
		if errors.Is(err, cookies.ErrAuthHelperUnavailable) {
			logger.Debugf("core: cookie encryption sync skipped: web auth helper unavailable")
		} else {
			return fmt.Errorf("cookies encryption sync: %w", err)
		}
	}

	cookiesDir, err := db.CookiePath(dbPath, "")
	if err != nil {
		logger.Debugf("core: failed to resolve cookies directory: %v", err)
		return nil // Non-fatal: directory path resolution failed
	}

	if err := cookies.EnsureCookieMigration(ctx, sqliteDB, dbPath, cookiesDir, logger); err != nil {
		if errors.Is(err, cookies.ErrAuthHelperUnavailable) {
			logger.Debugf("core: cookie migration skipped: web auth helper unavailable")
			return nil
		}
		return fmt.Errorf("cookies migration: %w", err)
	}

	return nil
}
