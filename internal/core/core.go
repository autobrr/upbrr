// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path" //nolint:depguard // Builds URL paths, not local filesystem paths.
	"path/filepath"
	"slices"
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
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/redaction"
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
	logger     api.Logger
	repoOwner  api.RepositoryOwner
	selections api.ReleaseSelectionRepository
	ownsRepo   bool

	history       *historyModule
	preparedFacts *preparedrelease.Module
	description   *descriptionModule
	media         *mediaModule
	upload        *uploadModule
	dupe          *dupeModule
}

// New constructs a Core using a background context for initialization. Call
// [Core.Close] when finished; it closes only an internally opened repository.
func New(deps api.CoreDependencies) (*Core, error) {
	return newCore(context.Background(), deps)
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

	core := &Core{
		logger:        logger,
		repoOwner:     repoOwner,
		selections:    repositories.Selections(),
		ownsRepo:      ownsRepo,
		preparedFacts: preparedFacts,
	}
	core.history = newHistoryModule(repositories.History(), cfg.MainSettings.DBPath, logger)
	core.history.preparedFacts = core.preparedFacts
	core.description = newDescriptionModule(cfg, logger, services, repositories.Selections(), registry, core.preparedFacts)
	core.media = newMediaModule(
		cfg,
		logger,
		services,
		mediaRepositoryView{TrackerStateRepository: repositories.Trackers(), MediaAssetRepository: repositories.Media()},
		registry,
		core.preparedFacts,
	)
	core.upload = newUploadModule(
		cfg,
		logger,
		services,
		repositories.ReleaseState(),
		repositories.Trackers(),
		registry,
		core.preparedFacts,
		core.description.resolveOverrideRequest,
		core.description.resolveSubjectGroups,
		core.ImportAcceptedMenuImages,
		clientDiscovery,
	)
	core.dupe = newDupeModule(cfg, logger, services, registry, core.preparedFacts, clientDiscovery)
	constructionSucceeded = true
	return core, nil
}

// RunUploadPrepared uploads from cached prepared metadata for each requested path.
// Explicit tracker selections that resolve empty return without tracker upload
// side effects, while omitted tracker selections retain configured default behavior.
// If a later path fails or the context is canceled after earlier uploads complete,
// the returned result preserves the uploaded count accumulated before the error.
func (c *Core) RunUploadPrepared(ctx context.Context, req api.Request) (result api.Result, err error) {
	result, err = c.upload.runPrepared(ctx, req)
	return result, classifyOperationError(api.OperationKindUploadExecute, err)
}

// RunAcceptedUpload executes one reviewed upload input against state imported
// and validated for its exact prepared generation.
func (c *Core) RunAcceptedUpload(ctx context.Context, plan api.UploadExecutionPlan) (api.Result, error) {
	result, err := c.upload.runAccepted(ctx, plan)
	return classifyOperationResult(api.OperationKindUploadExecute, result, err)
}

func emitPreparedUploadProgress(ctx context.Context, req api.Request, sourcePath string, tracker string, task string, status string, message string) {
	normalizedTracker := strings.TrimSpace(tracker)
	if normalizedTracker == "" && len(req.Trackers) == 1 {
		normalizedTracker = firstRequestedTracker(req.Trackers)
	}
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath: sourcePath,
		Tracker:    normalizedTracker,
		Task:       task,
		Status:     status,
		Message:    message,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	})
}

func firstRequestedTracker(trackers []string) string {
	for _, tracker := range trackers {
		name := strings.TrimSpace(tracker)
		if name != "" {
			return name
		}
	}
	return ""
}

// CheckDupes checks one validated source against the resolved tracker set and
// stores the resulting duplicate state with its prepared release. WebUI requests
// require a compatible metadata-preview cache entry.
func (c *Core) CheckDupes(ctx context.Context, req api.Request) (api.DupeCheckSummary, error) {
	summary, err := c.dupe.check(ctx, req)
	return summary, classifyOperationError(api.OperationKindDuplicateCheck, err)
}

// CheckAcceptedDupes executes duplicate checking against state imported and
// validated for one exact accepted prepared generation.
func (c *Core) CheckAcceptedDupes(ctx context.Context, input api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
	summary, err := c.dupe.checkAccepted(ctx, input)
	return summary, classifyOperationError(api.OperationKindDuplicateCheck, err)
}

// FetchScreenshotPlan builds a capture plan for one validated source. WebUI
// requests require a compatible metadata-preview cache entry.
func (c *Core) FetchScreenshotPlan(ctx context.Context, req api.Request) (api.ScreenshotPlan, error) {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return api.ScreenshotPlan{}, err
	}
	return c.FetchAcceptedScreenshotPlan(ctx, api.MediaPlanInput{
		Release: ref,
		Count:   req.Options.Screens,
		Options: req.ScreenshotOverrides,
	})
}

// FetchAcceptedScreenshotPlan plans screenshots for one exact prepared generation.
func (c *Core) FetchAcceptedScreenshotPlan(ctx context.Context, input api.MediaPlanInput) (api.ScreenshotPlan, error) {
	result, err := c.media.fetchAcceptedScreenshotPlan(ctx, input)
	return classifyOperationResult(api.OperationKindMedia, result, err)
}

// GenerateScreenshots captures the selected frames for one validated source.
// WebUI requests require a compatible metadata-preview cache entry.
func (c *Core) GenerateScreenshots(
	ctx context.Context,
	req api.Request,
	selections []api.ScreenshotSelection,
	purpose api.ScreenshotPurpose,
) (api.ScreenshotResult, error) {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return api.ScreenshotResult{}, err
	}
	return c.GenerateAcceptedScreenshots(ctx, api.MediaPlanInput{
		Release: ref,
		Count:   req.Options.Screens,
		Purpose: purpose,
		Options: req.ScreenshotOverrides,
	}, selections)
}

// GenerateAcceptedScreenshots captures selections for one exact prepared generation.
func (c *Core) GenerateAcceptedScreenshots(
	ctx context.Context,
	input api.MediaPlanInput,
	selections []api.ScreenshotSelection,
) (api.ScreenshotResult, error) {
	result, err := c.media.generateAcceptedScreenshots(ctx, input, selections)
	return classifyOperationResult(api.OperationKindMedia, result, err)
}

// PreviewScreenshotFrame renders one frame at timestampSeconds for one
// validated source without adding it to the final screenshot selection.
func (c *Core) PreviewScreenshotFrame(ctx context.Context, req api.Request, timestampSeconds float64) (api.ScreenshotPreview, error) {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return api.ScreenshotPreview{}, err
	}
	return c.PreviewAcceptedScreenshotFrame(ctx, api.MediaPlanInput{
		Release: ref,
		Count:   req.Options.Screens,
		Options: req.ScreenshotOverrides,
	}, timestampSeconds)
}

// PreviewAcceptedScreenshotFrame renders one frame for an exact prepared generation.
func (c *Core) PreviewAcceptedScreenshotFrame(
	ctx context.Context,
	input api.MediaPlanInput,
	timestampSeconds float64,
) (api.ScreenshotPreview, error) {
	result, err := c.media.previewAcceptedScreenshotFrame(ctx, input, timestampSeconds)
	return classifyOperationResult(api.OperationKindMedia, result, err)
}

// DeleteScreenshot removes a managed local screenshot and attempts cleanup of
// its persisted records. The default screenshot service rejects paths outside
// the release's managed temporary directory.
func (c *Core) DeleteScreenshot(ctx context.Context, req api.Request, imagePath string) error {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return err
	}
	return c.DeleteAcceptedScreenshot(ctx, api.MediaPlanInput{
		Release: ref,
		Count:   req.Options.Screens,
		Options: req.ScreenshotOverrides,
	}, imagePath)
}

// DeleteAcceptedScreenshot deletes one managed image for an exact prepared generation.
func (c *Core) DeleteAcceptedScreenshot(ctx context.Context, input api.MediaPlanInput, imagePath string) error {
	return classifyOperationError(api.OperationKindMedia, c.media.deleteAcceptedScreenshot(ctx, input, imagePath))
}

// DeleteTrackerImageURL removes url from persisted tracker metadata for one
// validated source; it does not delete the remote image.
func (c *Core) DeleteTrackerImageURL(ctx context.Context, req api.Request, url string) error {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return err
	}
	return c.DeleteAcceptedTrackerImageURL(ctx, api.ImageHostingInput{Release: ref}, url)
}

// DeleteAcceptedTrackerImageURL removes one tracker image from an exact generation.
func (c *Core) DeleteAcceptedTrackerImageURL(ctx context.Context, input api.ImageHostingInput, url string) error {
	return classifyOperationError(api.OperationKindImageHosting, c.media.deleteAcceptedTrackerImageURL(ctx, input, url))
}

// SaveFinalScreenshotSelections validates and persists the non-menu final
// screenshot selection for one source. Menu images in images are ignored.
func (c *Core) SaveFinalScreenshotSelections(ctx context.Context, req api.Request, images []api.ScreenshotImage) error {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return err
	}
	return c.SaveAcceptedFinalScreenshotSelections(ctx, api.MediaPlanInput{
		Release: ref,
		Count:   req.Options.Screens,
		Options: req.ScreenshotOverrides,
	}, images)
}

// SaveAcceptedFinalScreenshotSelections persists selections for an exact prepared generation.
func (c *Core) SaveAcceptedFinalScreenshotSelections(ctx context.Context, input api.MediaPlanInput, images []api.ScreenshotImage) error {
	return classifyOperationError(api.OperationKindMedia, c.media.saveAcceptedFinalScreenshotSelections(ctx, input, images))
}

// ImportMenuImages copies supported host-filesystem images into one release's
// managed temporary directory and appends them to its final selection.
func (c *Core) ImportMenuImages(ctx context.Context, req api.Request, importPaths []string) error {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return err
	}
	return c.ImportAcceptedMenuImages(ctx, api.MediaPlanInput{Release: ref}, importPaths)
}

// ImportAcceptedMenuImages imports menu images for one exact prepared generation.
func (c *Core) ImportAcceptedMenuImages(ctx context.Context, input api.MediaPlanInput, importPaths []string) error {
	return classifyOperationError(api.OperationKindMedia, c.media.importAcceptedMenuImages(ctx, input, importPaths))
}

// ListUploadCandidates returns persisted normal and disc-menu images eligible
// for image-host upload for one validated source.
func (c *Core) ListUploadCandidates(ctx context.Context, req api.Request) ([]api.ScreenshotImage, error) {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return nil, err
	}
	return c.ListAcceptedUploadCandidates(ctx, api.ImageHostingInput{Release: ref})
}

// ListAcceptedUploadCandidates returns image-host candidates for an exact generation.
func (c *Core) ListAcceptedUploadCandidates(ctx context.Context, input api.ImageHostingInput) ([]api.ScreenshotImage, error) {
	result, err := c.media.listAcceptedUploadCandidates(ctx, input)
	return classifyOperationResult(api.OperationKindImageHosting, result, err)
}

// ListUploadedImages returns persisted image-host links for one validated
// source.
func (c *Core) ListUploadedImages(ctx context.Context, req api.Request) ([]api.UploadedImageLink, error) {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return nil, err
	}
	return c.ListAcceptedUploadedImages(ctx, api.ImageHostingInput{Release: ref})
}

// ListAcceptedUploadedImages returns persisted links for one exact generation.
func (c *Core) ListAcceptedUploadedImages(ctx context.Context, input api.ImageHostingInput) ([]api.UploadedImageLink, error) {
	result, err := c.media.listAcceptedUploadedImages(ctx, input)
	return classifyOperationResult(api.OperationKindImageHosting, result, err)
}

// UploadImages uploads selected images to the requested host and any additional
// hosts required by eligible trackers. Per-host failures are returned in the
// result alongside successful links.
func (c *Core) UploadImages(
	ctx context.Context,
	req api.Request,
	host string,
	images []api.ScreenshotImage,
) (api.UploadImagesResult, error) {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return api.UploadImagesResult{}, err
	}
	return c.UploadAcceptedImages(ctx, api.ImageHostingInput{
		Release:  ref,
		Trackers: append([]string(nil), req.Trackers...),
		Host:     host,
	}, images)
}

// UploadAcceptedImages uploads selected images for an exact prepared generation.
func (c *Core) UploadAcceptedImages(
	ctx context.Context,
	input api.ImageHostingInput,
	images []api.ScreenshotImage,
) (api.UploadImagesResult, error) {
	result, err := c.media.uploadAcceptedImages(ctx, input, images)
	return classifyOperationResult(api.OperationKindImageHosting, result, err)
}

// DeleteUploadedImage removes one persisted image-host link. It does not delete
// either the local image or the remote asset.
func (c *Core) DeleteUploadedImage(ctx context.Context, req api.Request, imagePath string, host string) error {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentMedia)
	if err != nil {
		return err
	}
	return c.DeleteAcceptedUploadedImage(ctx, api.ImageHostingInput{Release: ref}, imagePath, host)
}

// DeleteAcceptedUploadedImage removes one persisted link for an exact generation.
func (c *Core) DeleteAcceptedUploadedImage(ctx context.Context, input api.ImageHostingInput, imagePath string, host string) error {
	return classifyOperationError(api.OperationKindImageHosting, c.media.deleteAcceptedUploadedImage(ctx, input, imagePath, host))
}

// FetchMetadataPreview prepares and enriches metadata for one validated source,
// emits metadata progress, and stores an isolated prepared-release snapshot.
func (c *Core) FetchMetadataPreview(ctx context.Context, req api.Request) (preview api.MetadataPreview, err error) {
	defer func() { err = classifyOperationError(api.OperationKindPreparation, err) }()
	input, err := api.MapPreparationRequest(req, api.PreparationIntentPreview)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("core: map metadata preparation request: %w", err)
	}
	prepared, err := c.preparedFacts.Prepare(ctx, input)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("core: prepare metadata preview: %w", err)
	}
	preview, err = c.FetchAcceptedMetadataPreview(ctx, api.ReleaseRef{
		SourcePath: prepared.Release.Source.SourcePath,
		Generation: prepared.Release.Generation,
	})
	preview.ReleaseNameOverrides = req.ReleaseNameOverrides
	return preview, err
}

// PrepareRelease returns one immutable canonical prepared-release generation.
func (c *Core) PrepareRelease(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
	result, err := c.preparedFacts.Prepare(ctx, input)
	if err != nil {
		return api.PrepareResult{}, classifyOperationError(api.OperationKindPreparation, fmt.Errorf("core: prepare release: %w", err))
	}
	return result, nil
}

// ExportReleaseSeed snapshots one exact canonical prepared generation.
func (c *Core) ExportReleaseSeed(ctx context.Context, ref api.ReleaseRef) (preparedrelease.Seed, error) {
	seed, err := c.preparedFacts.Export(ctx, ref)
	if err != nil {
		return preparedrelease.Seed{}, fmt.Errorf("core: export release seed: %w", err)
	}
	return seed, nil
}

// ImportReleaseSeed validates and installs one exact canonical prepared generation.
func (c *Core) ImportReleaseSeed(ctx context.Context, seed preparedrelease.Seed) (api.ReleaseRef, error) {
	ref, err := c.preparedFacts.Import(ctx, seed)
	if err != nil {
		return api.ReleaseRef{}, fmt.Errorf("core: import release seed: %w", err)
	}
	return ref, nil
}

// FetchPreparationPreview builds tracker preparation data for one validated
// source. WebUI requests reuse a compatible prepared-release cache entry.
func (c *Core) FetchPreparationPreview(ctx context.Context, req api.Request) (preview api.PreparationPreview, err error) {
	defer func() { err = classifyOperationError(api.OperationKindDescription, err) }()
	input, err := api.MapPreparationRequest(req, api.PreparationIntentDescription)
	if err != nil {
		return api.PreparationPreview{}, fmt.Errorf("core: map description preparation request: %w", err)
	}
	prepared, err := c.preparedFacts.Prepare(ctx, input)
	if err != nil {
		return api.PreparationPreview{}, fmt.Errorf("core: prepare description preview: %w", err)
	}
	return c.FetchAcceptedPreparationPreview(ctx, api.DescriptionInput{
		Release:  api.ReleaseRef{SourcePath: prepared.Release.Source.SourcePath, Generation: prepared.Release.Generation},
		Trackers: append([]string(nil), req.Trackers...),
		Options:  req.Options,
	})
}

// FetchTrackerDryRunPreview builds per-tracker dry-run upload entries from cached
// prepared metadata, creating torrents and cache state only after selected trackers
// resolve. Dry-run/debug processing still evaluates tracker prerequisites such
// as banned-group matches, rule failures, and block state, but reports them
// without suppressing payload generation or performing the tracker upload.
func (c *Core) FetchTrackerDryRunPreview(ctx context.Context, req api.Request) (api.TrackerDryRunPreview, error) {
	req = c.upload.expandEntrypointTrackerDefaults(req)
	resolvedReq, err := c.description.resolveOverrideRequest(ctx, req)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	input, err := api.MapPreparationRequest(resolvedReq, api.PreparationIntentDryRun)
	if err != nil {
		return api.TrackerDryRunPreview{}, fmt.Errorf("core: map dry-run preparation request: %w", err)
	}
	prepared, err := c.preparedFacts.Prepare(ctx, input)
	if err != nil {
		return api.TrackerDryRunPreview{}, fmt.Errorf("core: prepare tracker dry run: %w", err)
	}
	return c.FetchAcceptedTrackerDryRun(ctx, trackerDryRunInputFromRequest(resolvedReq, api.ReleaseRef{
		SourcePath: prepared.Release.Source.SourcePath,
		Generation: prepared.Release.Generation,
	}))
}

// FetchAcceptedTrackerDryRun builds a dry-run preview from one exact prepared
// generation and typed operation input.
func (c *Core) FetchAcceptedTrackerDryRun(ctx context.Context, input api.TrackerDryRunInput) (api.TrackerDryRunPreview, error) {
	preview, err := c.upload.fetchAcceptedTrackerDryRun(ctx, input)
	return preview, classifyOperationError(api.OperationKindDryRun, err)
}

func sanitizeTrackerDryRunEntries(entries []api.TrackerDryRunEntry) []api.TrackerDryRunEntry {
	sanitized := make([]api.TrackerDryRunEntry, len(entries))
	for index, entry := range entries {
		entry.Message = logging.SanitizeMessage(entry.Message)
		entry.BannedReason = logging.SanitizeMessage(entry.BannedReason)
		entry.BannedCheckError = logging.SanitizeMessage(entry.BannedCheckError)
		entry.Endpoint = logging.SanitizeMessage(entry.Endpoint)
		entry.Payload = redactDryRunPayload(entry.Payload)
		entry.Files = sanitizeTrackerDryRunFiles(entry.Files)
		entry.DebugSections = sanitizeTrackerDryRunDebugSections(entry.DebugSections)
		entry.ImageHost = sanitizeDryRunImageHostFeedback(entry.ImageHost)
		sanitized[index] = entry
	}
	return sanitized
}

func redactDryRunPayload(payload map[string]string) map[string]string {
	if payload == nil {
		return nil
	}
	redacted := make(map[string]string, len(payload))
	for key, value := range payload {
		wrapped := map[string]any{key: value}
		result, ok := redaction.RedactPrivateInfo(wrapped, nil).(map[string]any)
		if !ok {
			redacted[key] = "[REDACTED]"
			continue
		}
		redactedValue, ok := result[key].(string)
		if !ok {
			redacted[key] = "[REDACTED]"
			continue
		}
		redacted[key] = logging.SanitizeMessage(redactedValue)
	}
	return redacted
}

func sanitizeTrackerDryRunFiles(files []api.TrackerDryRunFile) []api.TrackerDryRunFile {
	sanitized := slices.Clone(files)
	for index := range sanitized {
		sanitized[index].Path = logging.SanitizeMessage(sanitized[index].Path)
	}
	return sanitized
}

func sanitizeTrackerDryRunDebugSections(sections []api.TrackerDryRunDebugSection) []api.TrackerDryRunDebugSection {
	sanitized := make([]api.TrackerDryRunDebugSection, len(sections))
	for index, section := range sections {
		section.Endpoint = logging.SanitizeMessage(section.Endpoint)
		section.Payload = redactDryRunPayload(section.Payload)
		section.Files = sanitizeTrackerDryRunFiles(section.Files)
		sanitized[index] = section
	}
	return sanitized
}

func sanitizeDryRunImageHostFeedback(feedback api.ImageHostFeedback) api.ImageHostFeedback {
	feedback.Message = logging.SanitizeMessage(feedback.Message)
	feedback.Warnings = slices.Clone(feedback.Warnings)
	for index := range feedback.Warnings {
		feedback.Warnings[index].Message = logging.SanitizeMessage(feedback.Warnings[index].Message)
	}
	return feedback
}

// trackerDryRunTorrentPath returns the present torrent file path advertised by
// a dry-run payload, ignoring other upload file fields.
func trackerDryRunTorrentPath(entry api.TrackerDryRunEntry) string {
	for _, file := range entry.Files {
		if strings.EqualFold(strings.TrimSpace(file.Field), "torrent") && file.Present {
			return strings.TrimSpace(file.Path)
		}
	}
	return ""
}

// annotateDryRunReleaseNames records whether each tracker-specific dry-run
// upload name differs from the prepared release name.

func annotateDryRunSubjectReleaseNames(subject api.UploadSubject, entries []api.TrackerDryRunEntry) {
	annotateDryRunNames(subject.ReleaseName, subject.ReleaseNameNoTag, subject.Filename, entries)
}

func annotateDryRunNames(releaseName string, nameWithoutTag string, filename string, entries []api.TrackerDryRunEntry) {
	original := strings.TrimSpace(releaseName)
	if original == "" {
		original = strings.TrimSpace(nameWithoutTag)
	}
	if original == "" {
		original = strings.TrimSpace(filename)
	}
	for idx := range entries {
		uploadName := strings.TrimSpace(entries[idx].ReleaseName)
		if uploadName == "" {
			uploadName = original
		}
		entries[idx].OriginalReleaseName = original
		entries[idx].UploadReleaseName = uploadName
		entries[idx].ReleaseNameChanged = original != "" && uploadName != "" && uploadName != original
		if entries[idx].ReleaseNameChanged && strings.TrimSpace(entries[idx].ReleaseNameChangeReason) == "" {
			entries[idx].ReleaseNameChangeReason = "tracker naming rules"
		}
	}
}

// FetchDescriptionBuilderPreview builds editable description groups for one
// validated source and its resolved tracker set.
func (c *Core) FetchDescriptionBuilderPreview(ctx context.Context, req api.Request) (api.DescriptionBuilderPreview, error) {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentDescription)
	if err != nil {
		return api.DescriptionBuilderPreview{}, err
	}
	return c.FetchAcceptedDescriptionBuilderPreview(ctx, api.DescriptionInput{
		Release:           ref,
		Trackers:          append([]string(nil), req.Trackers...),
		Groups:            api.CloneDescriptionBuilderGroups(req.DescriptionGroups),
		ImageHost:         req.ImageHostOverrides,
		QuestionnaireData: cloneOperationQuestionnaireAnswers(req.TrackerQuestionnaireAnswers),
		Options:           req.Options,
	})
}

// FetchAcceptedDescriptionBuilderPreview builds groups for one exact generation.
func (c *Core) FetchAcceptedDescriptionBuilderPreview(ctx context.Context, input api.DescriptionInput) (api.DescriptionBuilderPreview, error) {
	result, err := c.description.fetchAcceptedPreview(ctx, input)
	return classifyOperationResult(api.OperationKindDescription, result, err)
}

// FetchDescriptionBuilderGroupPreview builds one editable description group
// selected by the request's group or first tracker.
func (c *Core) FetchDescriptionBuilderGroupPreview(ctx context.Context, req api.Request) (api.DescriptionBuilderGroup, error) {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentDescription)
	if err != nil {
		return api.DescriptionBuilderGroup{}, err
	}
	return c.FetchAcceptedDescriptionBuilderGroupPreview(ctx, api.DescriptionInput{
		Release:           ref,
		Trackers:          append([]string(nil), req.Trackers...),
		GroupKey:          req.DescriptionOverrideGroup,
		Groups:            api.CloneDescriptionBuilderGroups(req.DescriptionGroups),
		ImageHost:         req.ImageHostOverrides,
		QuestionnaireData: cloneOperationQuestionnaireAnswers(req.TrackerQuestionnaireAnswers),
		Options:           req.Options,
	})
}

// FetchAcceptedDescriptionBuilderGroupPreview builds one exact-generation group.
func (c *Core) FetchAcceptedDescriptionBuilderGroupPreview(ctx context.Context, input api.DescriptionInput) (api.DescriptionBuilderGroup, error) {
	result, err := c.description.fetchAcceptedGroupPreview(ctx, input)
	return classifyOperationResult(api.OperationKindDescription, result, err)
}

// SelectBlurayCandidate publishes a new prepared generation containing the
// selected Blu-ray candidate as a release-fact instruction.
func (c *Core) SelectBlurayCandidate(ctx context.Context, sourcePath string, releaseID string) (api.MetadataPreview, error) {
	trimmedPath := strings.TrimSpace(sourcePath)
	trimmedID := strings.TrimSpace(releaseID)
	if trimmedPath == "" || trimmedID == "" {
		return api.MetadataPreview{}, classifyOperationError(api.OperationKindPreparation, internalerrors.ErrInvalidInput)
	}
	prepared, err := c.preparedFacts.Prepare(ctx, api.PrepareInput{
		SourcePath: trimmedPath,
		Intent:     api.PreparationIntentPreview,
		Instructions: api.ReleaseFactInstructions{
			BlurayReleaseID: trimmedID,
		},
		Force: true,
	})
	if err != nil {
		return api.MetadataPreview{}, classifyOperationError(api.OperationKindPreparation, fmt.Errorf("core: select Blu-ray candidate: %w", err))
	}
	return c.FetchAcceptedMetadataPreview(ctx, api.ReleaseRef{SourcePath: prepared.Release.Source.SourcePath, Generation: prepared.Release.Generation})
}

// explicitTrackerSelectionResolvedEmpty reports whether a non-empty requested
// tracker set was fully removed by tracker resolution.
func explicitTrackerSelectionResolvedEmpty(requested []string, resolved []string) bool {
	return len(requested) > 0 && len(resolved) == 0
}

// resolveTrackersPreservingExplicitEmpty resolves requested trackers while
// preserving the difference between an omitted selection and an explicit
// selection that was fully removed.
//
// includeDefaults adds configured defaults to non-empty explicit selections.
// fallbackDefaultsWhenExplicitEmpty controls whether a removed explicit
// selection may fall back to defaults instead of returning explicitEmpty.
func resolveTrackersPreservingExplicitEmpty(
	cfg config.Config,
	requested []string,
	remove []string,
	logger api.Logger,
	registry *trackers.Registry,
	includeDefaults bool,
	fallbackDefaultsWhenExplicitEmpty bool,
) ([]string, bool) {
	resolved := trackers.ResolveTrackersWithRegistry(cfg, requested, remove, logger, registry)
	if explicitTrackerSelectionResolvedEmpty(requested, resolved) {
		if includeDefaults && fallbackDefaultsWhenExplicitEmpty {
			defaults := trackers.ResolveTrackersWithRegistry(cfg, nil, remove, logger, registry)
			if len(defaults) > 0 {
				return defaults, false
			}
		}
		return nil, true
	}
	if includeDefaults && len(requested) > 0 {
		return trackers.ResolveTrackersWithDefaultsAndRegistry(cfg, requested, remove, logger, registry), false
	}
	return resolved, false
}

// deepCopyAniListMetadata clones rich anime metadata for prepared-metadata
// snapshots so cached WebUI/core state cannot alias mutable slice fields.

// DetectDiscType classifies one preparation source through the canonical source-layout resolver.
func (c *Core) DetectDiscType(ctx context.Context, sourcePath string) (string, error) {
	if ctx == nil {
		return "", internalerrors.ErrInvalidInput
	}
	layout, err := sourcelayout.Resolve(ctx, sourcePath)
	if err != nil {
		return "", classifyOperationError(api.OperationKindPreparation, fmt.Errorf("core: resolve source layout: %w", err))
	}
	return layout.DiscType, nil
}

// DiscoverPlaylists scans the local source path for Blu-ray playlists and
// returns their ordered items, durations, and selection scores.
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

// SavePlaylistSelection persists selected playlist names, or the use-all flag,
// under the source path's normalized slash-delimited database key.
func (c *Core) SavePlaylistSelection(ctx context.Context, sourcePath string, playlists []string, useAll bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("core: save playlist selection canceled: %w", err)
	}
	if strings.TrimSpace(sourcePath) == "" {
		return internalerrors.ErrInvalidInput
	}
	if c.selections == nil {
		return errors.New("core: repository not initialized")
	}

	// Normalize path to ensure consistent storage and retrieval
	normalizedPath := filepath.ToSlash(filepath.Clean(sourcePath))
	c.logger.Debugf("core: saving playlist selection for %q (normalized: %q): %d playlists, useAll=%v", sourcePath, normalizedPath, len(playlists), useAll)

	if err := c.selections.SavePlaylistSelection(ctx, normalizedPath, playlists, useAll); err != nil {
		c.logger.Warnf("core: save playlist selection failed: %v", err)
		return fmt.Errorf("core: %w", err)
	}

	c.logger.Infof("core: playlist selection saved for %q", normalizedPath)
	return nil
}

// LoadPlaylistSelection returns the selection stored under the source path's
// normalized slash-delimited database key. It returns
// [internalerrors.ErrNotFound] when absent.
func (c *Core) LoadPlaylistSelection(ctx context.Context, sourcePath string) (api.PlaylistSelection, error) {
	if err := ctx.Err(); err != nil {
		return api.PlaylistSelection{}, fmt.Errorf("core: load playlist selection canceled: %w", err)
	}
	if strings.TrimSpace(sourcePath) == "" {
		return api.PlaylistSelection{}, internalerrors.ErrInvalidInput
	}
	if c.selections == nil {
		return api.PlaylistSelection{}, errors.New("core: repository not initialized")
	}

	normalizedPath := filepath.ToSlash(filepath.Clean(sourcePath))
	c.logger.Debugf("core: loading playlist selection source=%q normalized=%q", sourcePath, normalizedPath)

	selection, err := c.selections.GetPlaylistSelection(ctx, normalizedPath)
	if err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			c.logger.Debugf("core: playlist selection decision=not_found source=%q", sourcePath)
			return api.PlaylistSelection{}, internalerrors.ErrNotFound
		}
		c.logger.Warnf("core: load playlist selection failed: %v", err)
		return api.PlaylistSelection{}, fmt.Errorf("core: %w", err)
	}

	c.logger.Debugf(
		"core: playlist selection decision=loaded source=%q playlists=%d use_all=%v",
		sourcePath,
		len(selection.SelectedPlaylists),
		selection.UseAll,
	)
	return selection, nil
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
	result, err := c.description.render(ctx, raw)
	return classifyOperationResult(api.OperationKindDescription, result, err)
}

// SaveDescriptionOverride stores a trimmed override for one validated source
// and group. Blank raw content deletes the existing override.
func (c *Core) SaveDescriptionOverride(ctx context.Context, req api.Request, raw string) (api.DescriptionBuilderGroup, error) {
	ref, err := c.prepareRequestRef(ctx, req, api.PreparationIntentDescription)
	if err != nil {
		return api.DescriptionBuilderGroup{}, err
	}
	return c.SaveAcceptedDescriptionOverride(ctx, api.DescriptionInput{
		Release:  ref,
		Trackers: append([]string(nil), req.Trackers...),
		GroupKey: req.DescriptionOverrideGroup,
		Options:  req.Options,
	}, raw)
}

// SaveAcceptedDescriptionOverride persists an override scoped to one exact generation.
func (c *Core) SaveAcceptedDescriptionOverride(
	ctx context.Context,
	input api.DescriptionInput,
	raw string,
) (api.DescriptionBuilderGroup, error) {
	result, err := c.description.saveAcceptedOverride(ctx, input, raw)
	return classifyOperationResult(api.OperationKindDescription, result, err)
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

// dupeCheckDryRunProcessingMeta clears previous duplicate-match suppression
// before the dupe service runs for dry-run/debug. Tracker rule failures stay in
// place so rule-failed trackers remain skipped by the dupe check.

// trackerDryRunProcessingMeta clears suppressive duplicate/block state for
// dry-run/debug artifact generation while leaving normal upload processing
// unchanged. Rule failures stay terminal in every mode; dry-run/debug should
// only bypass duplicate-hit state so operators can inspect what would have been
// built for trackers that passed rules.

func normalizeExecutionRequest(req api.Request) api.Request {
	if req.Execution.SiteCheck {
		req.Options.DryRun = true
	}
	if strings.TrimSpace(req.Execution.SiteUploadTracker) != "" {
		req.Trackers = []string{strings.ToUpper(strings.TrimSpace(req.Execution.SiteUploadTracker))}
	}
	return req
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
