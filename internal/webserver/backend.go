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
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/core"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/guiapp"
	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/internal/imagehostpolicy"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const previewTimeout = 30 * time.Minute

// Backend owns the embedded web API runtime and request-scoped background jobs.
type Backend struct {
	runtimeMu   sync.RWMutex
	cfg         config.Config
	core        api.Core
	coreInitErr error
	logger      *logging.Logger
	repo        *db.SQLiteRepository
	hub         *eventHub

	streamMu sync.Mutex
	streams  map[string]*backendLogStream
	streamWG sync.WaitGroup

	dupeMu sync.Mutex
	dupes  map[string]*dupeCheckJob
	dupeWG sync.WaitGroup

	uploadMu sync.Mutex
	uploads  map[string]*trackerUploadJob
	uploadWG sync.WaitGroup
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
	Debug       bool
	NoSeed      bool
	RunLogLevel string
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

	var coreSvc api.Core
	var coreInitErr error
	if err := cfg.Validate(); err != nil {
		coreInitErr = err
		logger.Warnf("web: config invalid, core disabled until settings are fixed: %v", err)
	} else {
		coreSvc, err = core.NewWithContext(ctx, api.CoreDependencies{
			Config: cfg,
			Logger: logger,
			Services: api.ServiceSet{
				Filesystem: filesystem.NewValidator(),
			},
			Repository: repo,
		})
		if err != nil {
			_ = repo.Close()
			_ = logger.Close()
			return nil, fmt.Errorf("web: %w", err)
		}
	}

	return &Backend{
		cfg:         cfg,
		core:        coreSvc,
		coreInitErr: coreInitErr,
		logger:      logger,
		repo:        repo,
		hub:         hub,
		streams:     make(map[string]*backendLogStream),
		dupes:       make(map[string]*dupeCheckJob),
		uploads:     make(map[string]*trackerUploadJob),
	}, nil
}

// Close stops active background work and releases runtime, repository, and log resources.
func (b *Backend) Close() error {
	b.stopAllLogStreams()
	b.stopAllDupeJobs()
	b.stopAllUploadJobs()
	rt := b.runtimeSnapshot()
	if rt.core != nil {
		_ = rt.core.Close()
	}
	if b.repo != nil {
		_ = b.repo.Close()
	}
	if rt.logger != nil {
		_ = rt.logger.Close()
	}
	return nil
}

func (b *Backend) requireCore() error {
	_, err := b.requireRuntime()
	return err
}

func (b *Backend) requireHistoryRepo() error {
	if b == nil || b.repo == nil {
		return errors.New("history repository not initialized")
	}
	return nil
}

func (b *Backend) DetectDiscType(ctx context.Context, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	return wrapWebResult(filesystem.DetectDiscType(ctx, path))
}

func (b *Backend) FetchMetadata(sessionID string, path string, sourceLookupURL string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackersList []string, confirmBDMVRescan bool) (api.MetadataPreview, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.MetadataPreview{}, err
	}
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return api.MetadataPreview{}, errors.New("path is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	progressCtx := api.WithMetadataProgressReporter(ctx, func(update api.MetadataProgressUpdate) {
		if strings.TrimSpace(update.Path) == "" {
			update.Path = trimmedPath
		}
		if strings.TrimSpace(update.Timestamp) == "" {
			update.Timestamp = time.Now().UTC().Format(time.RFC3339)
		}
		b.hub.Emit(sessionID, "metadata:progress", update)
	})

	req := api.Request{
		Paths:           []string{trimmedPath},
		Mode:            api.ModeGUI,
		Trackers:        append([]string{}, trackersList...),
		SourceLookupURL: strings.TrimSpace(sourceLookupURL),
		Options:         rt.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
		ConfirmBDMVRescan:    confirmBDMVRescan,
	}

	return wrapWebResult(rt.core.FetchMetadataPreview(progressCtx, req))
}

type blurayCandidateSelector interface {
	SelectBlurayCandidate(ctx context.Context, sourcePath string, releaseID string) (api.MetadataPreview, error)
}

func (b *Backend) SelectBlurayCandidate(path string, releaseID string) (api.MetadataPreview, error) {
	if strings.TrimSpace(path) == "" {
		return api.MetadataPreview{}, errors.New("path is required")
	}
	if strings.TrimSpace(releaseID) == "" {
		return api.MetadataPreview{}, errors.New("release ID is required")
	}
	if err := b.requireCore(); err != nil {
		return api.MetadataPreview{}, err
	}
	selector, ok := b.currentCore().(blurayCandidateSelector)
	if !ok {
		return api.MetadataPreview{}, errors.New("blu-ray candidate selection is unavailable in this build")
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(selector.SelectBlurayCandidate(ctx, path, releaseID))
}

func (b *Backend) ResetMetadata(sessionID string, path string, sourceLookupURL string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackersList []string, confirmBDMVRescan bool) (api.MetadataPreview, error) {
	if err := b.requireCore(); err != nil {
		return api.MetadataPreview{}, err
	}
	if b.repo == nil {
		return api.MetadataPreview{}, errors.New("config repository not initialized")
	}
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return api.MetadataPreview{}, errors.New("path is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	progressCtx := api.WithMetadataProgressReporter(ctx, func(update api.MetadataProgressUpdate) {
		if strings.TrimSpace(update.Path) == "" {
			update.Path = trimmedPath
		}
		if strings.TrimSpace(update.Timestamp) == "" {
			update.Timestamp = time.Now().UTC().Format(time.RFC3339)
		}
		b.hub.Emit(sessionID, "metadata:progress", update)
	})

	tmpRoot, err := db.Subdir(b.currentConfig().MainSettings.DBPath, "tmp")
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: resolve tmp dir: %w", err)
	}

	artifactPaths := make([]string, 0)
	shots, err := b.repo.ListScreenshotsByPath(ctx, trimmedPath)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: list screenshots: %w", err)
	}
	for _, shot := range shots {
		artifactPaths = append(artifactPaths, shot.ImagePath)
	}
	uploaded, err := b.repo.ListUploadedImagesByPath(ctx, trimmedPath)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: list uploaded images: %w", err)
	}
	for _, image := range uploaded {
		artifactPaths = append(artifactPaths, image.ImagePath)
	}
	finals, err := b.repo.ListFinalSelections(ctx, trimmedPath)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: list final selections: %w", err)
	}
	for _, image := range finals {
		artifactPaths = append(artifactPaths, image.ImagePath)
	}
	artifactPaths = slices.Compact(artifactPaths)

	tmpDirs := make(map[string]struct{})
	fallbackBase := paths.ReleaseTempBase(api.PreparedMetadata{}, trimmedPath)
	tmpDirs[filepath.Join(tmpRoot, fallbackBase)] = struct{}{}
	stored, err := b.repo.GetByPath(ctx, trimmedPath)
	if err == nil {
		releaseBase := paths.ReleaseTempBase(api.PreparedMetadata{
			Release: api.ReleaseInfo{
				Title:    stored.Title,
				Alt:      stored.Alt,
				Year:     stored.Year,
				Category: string(stored.Category),
				Source:   stored.Source,
				Type:     stored.Type,
				Group:    stored.Group,
			},
		}, trimmedPath)
		tmpDirs[filepath.Join(tmpRoot, releaseBase)] = struct{}{}
	}
	for _, filePath := range artifactPaths {
		contentRoot, ok := resolveContentTmpRoot(tmpRoot, filePath)
		if ok {
			tmpDirs[contentRoot] = struct{}{}
		}
	}
	if err := b.repo.PurgeContentData(ctx, trimmedPath); err != nil {
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: purge sqlite: %w", err)
	}
	for _, filePath := range artifactPaths {
		_ = removeIfWithinRoot(tmpRoot, filePath, false)
	}
	for dir := range tmpDirs {
		_ = removeIfWithinRoot(tmpRoot, dir, true)
	}

	req := api.Request{
		Paths:           []string{trimmedPath},
		Mode:            api.ModeGUI,
		Trackers:        append([]string{}, trackersList...),
		SourceLookupURL: strings.TrimSpace(sourceLookupURL),
		Options:         b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
		ConfirmBDMVRescan:    confirmBDMVRescan,
	}
	return wrapWebResult(b.currentCore().FetchMetadataPreview(progressCtx, req))
}

func (b *Backend) CheckDupes(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackersList []string) (api.DupeCheckSummary, error) {
	if err := b.requireCore(); err != nil {
		return api.DupeCheckSummary{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	req := api.Request{
		Paths:    []string{strings.TrimSpace(path)},
		Mode:     api.ModeGUI,
		Trackers: append([]string{}, trackersList...),
		Options:  b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}
	return wrapWebResult(b.currentCore().CheckDupes(ctx, req))
}

func (b *Backend) FetchPreparation(sessionID string, path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackersList []string, ignoreDupesFor []string) (api.PreparationPreview, error) {
	if err := b.requireCore(); err != nil {
		return api.PreparationPreview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	req := api.Request{
		Paths:          []string{strings.TrimSpace(path)},
		Mode:           api.ModeGUI,
		Trackers:       append([]string{}, trackersList...),
		IgnoreDupesFor: normalizeTrackerList(ignoreDupesFor),
		Options:        b.baseUploadOptions(),

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
	return wrapWebResult(b.currentCore().FetchPreparationPreview(progressCtx, req))
}

func (b *Backend) FetchTrackerDryRun(sessionID string, path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackersList []string, ignoreDupesFor []string, questionnaireAnswers map[string]map[string]string, descriptionGroups []api.DescriptionBuilderGroup, debug bool, noSeed bool, runLogLevel string) (api.TrackerDryRunPreview, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	runOpts, err := b.buildRunOptions(debug, noSeed, runLogLevel)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	b.logDebugf("web: tracker dry-run request path=%s debug=%t no_seed=%t run_log_level=%s", strings.TrimSpace(path), debug, noSeed, runOpts.RunLogLevel)
	runCore, runLogger, err := b.buildRunCoreFromSnapshot(rt, runOpts)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	defer func() {
		_ = runCore.Close()
		_ = runLogger.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	req := api.Request{
		Paths:                       []string{strings.TrimSpace(path)},
		Mode:                        api.ModeGUI,
		DescriptionGroups:           api.CloneDescriptionBuilderGroups(descriptionGroups),
		Trackers:                    append([]string{}, trackersList...),
		IgnoreDupesFor:              normalizeTrackerList(ignoreDupesFor),
		IgnoreTrackerRuleFailures:   false,
		Options:                     buildRunUploadOptions(rt.cfg, runOpts),
		ExternalIDOverrides:         overrides,
		ReleaseNameOverrides:        nameOverrides,
		TrackerQuestionnaireAnswers: cloneQuestionnaireAnswers(questionnaireAnswers),
	}
	req.Options.DryRun = true
	if err := guishared.SeedRunCorePreparedMeta(ctx, rt.core, runCore, req); err != nil {
		return api.TrackerDryRunPreview{}, fmt.Errorf("web: %w", err)
	}
	progressCtx := api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
		b.hub.Emit(sessionID, trackerUploadProgressEvent, update)
	})
	progressCtx = bdinfo.WithProgressReporter(progressCtx, func(line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		b.hub.Emit(sessionID, "bdinfo:progress", map[string]string{
			"path": strings.TrimSpace(path),
			"line": line,
		})
	})
	return wrapWebResult(runCore.FetchTrackerDryRunPreview(progressCtx, req))
}

func (b *Backend) FetchDescriptionBuilder(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackersList []string, ignoreDupesFor []string) (api.DescriptionBuilderPreview, error) {
	if err := b.requireCore(); err != nil {
		return api.DescriptionBuilderPreview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	req := api.Request{
		Paths:          []string{strings.TrimSpace(path)},
		Mode:           api.ModeGUI,
		Trackers:       append([]string{}, trackersList...),
		IgnoreDupesFor: normalizeTrackerList(ignoreDupesFor),
		Options:        b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}
	return wrapWebResult(b.currentCore().FetchDescriptionBuilderPreview(ctx, req))
}

func (b *Backend) RenderDescription(raw string) (string, error) {
	if err := b.requireCore(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(b.currentCore().RenderDescription(ctx, raw))
}

func (b *Backend) SaveDescriptionOverride(path string, groupKey string, raw string, trackers []string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) (api.DescriptionBuilderGroup, error) {
	if err := b.requireCore(); err != nil {
		return api.DescriptionBuilderGroup{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(b.currentCore().SaveDescriptionOverride(ctx, api.Request{
		Paths:                    []string{strings.TrimSpace(path)},
		Mode:                     api.ModeGUI,
		DescriptionOverrideGroup: strings.TrimSpace(groupKey),
		Trackers:                 append([]string{}, trackers...),
		ExternalIDOverrides:      overrides,
		ReleaseNameOverrides:     nameOverrides,
	}, raw))
}

func (b *Backend) DiscoverPlaylists(path string) ([]api.PlaylistInfo, error) {
	if err := b.requireCore(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(b.currentCore().DiscoverPlaylists(ctx, path))
}

func (b *Backend) SavePlaylistSelection(path string, playlists []string, useAll bool) error {
	if err := b.requireCore(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebError(b.currentCore().SavePlaylistSelection(ctx, path, playlists, useAll))
}

func (b *Backend) LoadPlaylistSelection(path string) (api.PlaylistSelection, error) {
	if err := b.requireCore(); err != nil {
		return api.PlaylistSelection{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(b.currentCore().LoadPlaylistSelection(ctx, path))
}

func (b *Backend) BrowseDirectory(path string, mode string) (api.BrowseDirectoryResponse, error) {
	if b == nil {
		return api.BrowseDirectoryResponse{}, errors.New("backend not initialized")
	}
	fallback := guishared.BrowseDirectoryFallback(b.currentConfig().MainSettings.DBPath)
	return wrapWebResult(guishared.BrowseDirectory(api.BrowseDirectoryRequest{Path: path, Mode: mode}, fallback))
}

func (b *Backend) BrowseDirectoryWithinRoot(path string, mode string, root string) (api.BrowseDirectoryResponse, error) {
	if b == nil {
		return api.BrowseDirectoryResponse{}, errors.New("backend not initialized")
	}
	fallback := guishared.BrowseDirectoryFallback(b.currentConfig().MainSettings.DBPath)
	return wrapWebResult(guishared.BrowseDirectoryWithinRoot(api.BrowseDirectoryRequest{Path: path, Mode: mode}, fallback, root))
}

func (b *Backend) BrowseDirectoryWithinRoots(path string, mode string, roots []string) (api.BrowseDirectoryResponse, error) {
	if b == nil {
		return api.BrowseDirectoryResponse{}, errors.New("backend not initialized")
	}
	fallback := guishared.BrowseDirectoryFallback(b.currentConfig().MainSettings.DBPath)
	return wrapWebResult(guishared.BrowseDirectoryWithinRoots(api.BrowseDirectoryRequest{Path: path, Mode: mode}, fallback, roots))
}

func (b *Backend) FetchScreenshotPlan(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) (api.ScreenshotPlan, error) {
	if err := b.requireCore(); err != nil {
		return api.ScreenshotPlan{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}
	return wrapWebResult(b.currentCore().FetchScreenshotPlan(ctx, req))
}

func (b *Backend) GenerateScreenshots(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, selections []api.ScreenshotSelection, purpose api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	if err := b.requireCore(); err != nil {
		return api.ScreenshotResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}
	return wrapWebResult(b.currentCore().GenerateScreenshots(ctx, req, selections, purpose))
}

func (b *Backend) PreviewScreenshotFrame(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, timestampSeconds float64) (string, error) {
	if err := b.requireCore(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}
	preview, err := b.currentCore().PreviewScreenshotFrame(ctx, req, timestampSeconds)
	if err != nil {
		return "", fmt.Errorf("web: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(preview.ImageBytes), nil
}

func (b *Backend) DeleteScreenshot(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, imagePath string) error {
	if err := b.requireCore(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebError(b.currentCore().DeleteScreenshot(ctx, api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}, imagePath))
}

func (b *Backend) DeleteTrackerImageURL(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, imageURL string) error {
	if err := b.requireCore(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebError(b.currentCore().DeleteTrackerImageURL(ctx, api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}, imageURL))
}

func (b *Backend) SaveFinalScreenshotSelections(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, images []api.ScreenshotImage) error {
	if err := b.requireCore(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebError(b.currentCore().SaveFinalScreenshotSelections(ctx, api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}, images))
}

func (b *Backend) ImportMenuImages(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, paths []string) error {
	if err := b.requireCore(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebError(b.currentCore().ImportMenuImages(ctx, api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}, paths))
}

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

func (b *Backend) ListUploadCandidates(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) ([]api.ScreenshotImage, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(rt.core.ListUploadCandidates(ctx, api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: rt.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}))
}

func (b *Backend) ListUploadedImages(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) ([]api.UploadedImageLink, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(rt.core.ListUploadedImages(ctx, api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: rt.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}))
}

func (b *Backend) UploadImages(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackersList []string, host string, images []api.ScreenshotImage) (api.UploadImagesResult, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return api.UploadImagesResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(rt.core.UploadImages(ctx, api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: rt.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
		Trackers:             append([]string{}, trackersList...),
	}, host, images))
}

func (b *Backend) DeleteUploadedImage(path string, imagePath string, host string) error {
	if err := b.requireCore(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebError(b.currentCore().DeleteUploadedImage(ctx, api.Request{
		Paths: []string{path},
		Mode:  api.ModeGUI,
	}, imagePath, host))
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

func (b *Backend) GetApplicationInfo() (api.ApplicationInfo, error) {
	return api.CurrentApplicationInfo(), nil
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

func (b *Backend) GetDefaultConfig() (string, error) {
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		return "", fmt.Errorf("web: %w", err)
	}
	return wrapWebResult(config.ExportToJSON(cfg))
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
	if err := config.MergeMissingTrackerDefaults(normalized); err != nil {
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

// SaveConfig validates encrypted browser settings, builds the replacement
// runtime, then syncs cookie encryption metadata and saves the non-env config.
// Runtime build or save failures leave the persisted config and active runtime
// unchanged; env overrides apply only to the installed runtime config.
func (b *Backend) SaveConfig(payload string) error {
	if b.repo == nil {
		return errors.New("config repository not initialized")
	}
	cfg, err := config.ImportFromJSONEncrypted(payload)
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}
	if err := config.MergeMissingTrackerDefaults(cfg); err != nil {
		return fmt.Errorf("web: %w", err)
	}
	currentCfg := b.currentConfig()
	if strings.TrimSpace(cfg.MainSettings.DBPath) == "" {
		cfg.MainSettings.DBPath = currentCfg.MainSettings.DBPath
	}
	if !pathutil.SamePath(cfg.MainSettings.DBPath, currentCfg.MainSettings.DBPath) {
		return errors.New("changing main_settings.db_path requires restart")
	}
	cfg.MainSettings.DBPath = currentCfg.MainSettings.DBPath
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("web: %w", err)
	}
	runtimeCfg := *cfg
	config.ApplyEnvOverrides(&runtimeCfg)
	runtimeCfg.MainSettings.DBPath = currentCfg.MainSettings.DBPath
	if err := runtimeCfg.Validate(); err != nil {
		return fmt.Errorf("web: %w", err)
	}
	return b.saveAndApplyConfig(context.Background(), cfg, runtimeCfg, cfg.MainSettings.DBPath)
}

const configImportMaxBytes = importer.MaxFileBytes

// ImportConfig imports browser-uploaded config content, validates the saved and
// env-applied runtime forms, builds the replacement runtime, then syncs cookie
// encryption metadata and saves the non-env config. Runtime build or save
// failures leave the persisted config and active runtime unchanged.
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

	currentCfg := b.currentConfig()
	cfg.MainSettings.DBPath = currentCfg.MainSettings.DBPath

	if err := cfg.Validate(); err != nil {
		return "", nil, fmt.Errorf("validate imported config: %w", err)
	}
	runtimeCfg := *cfg
	config.ApplyEnvOverrides(&runtimeCfg)
	runtimeCfg.MainSettings.DBPath = currentCfg.MainSettings.DBPath
	if err := runtimeCfg.Validate(); err != nil {
		return "", nil, fmt.Errorf("validate imported config: %w", err)
	}

	if err := b.saveAndApplyConfig(context.Background(), cfg, runtimeCfg, cfg.MainSettings.DBPath); err != nil {
		return "", nil, err
	}

	result := "imported config"
	if len(warnings) > 0 {
		result += fmt.Sprintf(" (%d warnings)", len(warnings))
	}
	return result, warnings, nil
}

// saveAndApplyConfig builds the replacement runtime before any repository
// writes, then persists cfg and installs the runtime as one ordered transition.
// If persistence fails, the built runtime is closed and the active runtime is
// left untouched.
func (b *Backend) saveAndApplyConfig(ctx context.Context, cfg *config.Config, runtimeCfg config.Config, dbPath string) error {
	if err := validateCookieAuthMaterial(dbPath); err != nil {
		if !errors.Is(err, cookies.ErrAuthHelperUnavailable) {
			return fmt.Errorf("web: validate cookie auth before config save: %w", err)
		}
	}

	rt, err := b.buildConfigRuntime(ctx, runtimeCfg)
	if err != nil {
		return err
	}
	if err := b.saveConfigToRepository(ctx, cfg, dbPath); err != nil {
		closeBuiltRuntime(rt)
		return err
	}
	b.installConfigRuntime(runtimeCfg, rt)
	return nil
}

// saveConfigToRepository enforces the shared pre-save cookie encryption sync
// before persisting browser config changes through the already-open repository.
func (b *Backend) saveConfigToRepository(ctx context.Context, cfg *config.Config, dbPath string) error {
	if err := configstore.SaveToRepository(ctx, cfg, b.repo, dbPath); err != nil {
		return fmt.Errorf("web: %w", err)
	}
	return nil
}

// validateCookieAuthMaterial returns ErrAuthHelperUnavailable only when auth
// material is absent; malformed material remains an error so callers can abort
// before cookie encryption metadata is initialized.
func validateCookieAuthMaterial(dbPath string) error {
	material, err := authmaterial.LoadFromDBPath(dbPath)
	if err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			return cookies.ErrAuthHelperUnavailable
		}
		return fmt.Errorf("load auth helper: %w", err)
	}
	if _, _, err := material.PrimaryHelper(); err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			return cookies.ErrAuthHelperUnavailable
		}
		return fmt.Errorf("derive auth helper: %w", err)
	}
	return nil
}

// buildConfigRuntime wraps shared runtime construction with web-specific error
// context.
func (b *Backend) buildConfigRuntime(ctx context.Context, cfg config.Config) (guishared.Runtime, error) {
	rt, err := guishared.BuildRuntime(ctx, cfg, b.repo)
	if err != nil {
		return guishared.Runtime{}, fmt.Errorf("web: %w", err)
	}
	return rt, nil
}

// installConfigRuntime swaps in a newly built runtime, rebinds event/log
// streams, and closes the previous core and logger after replacement.
func (b *Backend) installConfigRuntime(cfg config.Config, rt guishared.Runtime) {
	oldCore, oldLogger := b.replaceRuntime(cfg, rt.Core, rt.Logger)
	if b.hub != nil {
		b.hub.SetLogger(rt.Logger)
	}
	b.rebindLogStreams(oldLogger, rt.Logger)
	if oldCore != nil {
		_ = oldCore.Close()
	}
	if oldLogger != nil {
		_ = oldLogger.Close()
	}
}

// closeBuiltRuntime closes a runtime that was built but not installed.
func closeBuiltRuntime(rt guishared.Runtime) {
	if rt.Core != nil {
		_ = rt.Core.Close()
	}
	if rt.Logger != nil {
		_ = rt.Logger.Close()
	}
}

func (b *Backend) ListKnownTrackers() ([]string, error) {
	return trackers.KnownTrackers(), nil
}

func (b *Backend) GetImageHostPolicyMetadata() (imagehostpolicy.Metadata, error) {
	return imagehostpolicy.PolicyMetadata(), nil
}

func (b *Backend) ListHistory() ([]api.HistoryEntry, error) {
	if err := b.requireHistoryRepo(); err != nil {
		return nil, err
	}
	entries, err := b.repo.ListHistoryEntries(context.Background())
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	result := make([]api.HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		entryCopy := entry
		entryCopy.LatestUploadStatus = historyStatusLabel(entry.LatestUploadStatus, entry.RuleFailureCount)
		result = append(result, entryCopy)
	}
	return result, nil
}

func (b *Backend) GetHistoryOverview(sourcePath string) (api.HistoryOverview, error) {
	return historyOverviewFromRepo(b.repo, sourcePath)
}

func (b *Backend) DeleteHistoryRelease(sourcePath string) error {
	if err := b.requireCore(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebError(b.currentCore().DeleteHistoryRelease(ctx, strings.TrimSpace(sourcePath)))
}

func (b *Backend) GetLogPath() (string, error) {
	return wrapWebResult(logging.LogPath(b.currentConfig().MainSettings.DBPath))
}

func (b *Backend) GetRecentLogs(limit int) ([]logging.Entry, error) {
	logger := b.currentLogger()
	if logger == nil {
		return nil, errors.New("logger not initialized")
	}
	return logger.Recent(limit), nil
}

func (b *Backend) GetLogExclusions() ([]string, error) {
	if b.repo == nil {
		return nil, errors.New("config repository not initialized")
	}
	var exclusions guiapp.LogExclusions
	err := config.LoadSectionFromDatabase(context.Background(), "log_exclusions", &exclusions, b.repo)
	if err != nil {
		if errorsIsNotFound(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("web: %w", err)
	}
	return normalizePatterns(exclusions.Patterns), nil
}

func (b *Backend) UpdateLogExclusions(patterns []string) error {
	if b.repo == nil {
		return errors.New("config repository not initialized")
	}
	return wrapWebError(config.SaveSectionToDatabase(context.Background(), "log_exclusions", guiapp.LogExclusions{
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

func (b *Backend) buildRunOptions(debug bool, noSeed bool, runLogLevel string) (runOptions, error) {
	if strings.TrimSpace(runLogLevel) == "" {
		return runOptions{Debug: debug, NoSeed: noSeed}, nil
	}
	normalized, err := api.ParseLogLevel(runLogLevel)
	if err != nil {
		return runOptions{}, fmt.Errorf("web: %w", err)
	}
	return runOptions{Debug: debug, NoSeed: noSeed, RunLogLevel: normalized}, nil
}

// buildRunCoreFromSnapshot creates a per-run core and logger from the same
// runtime snapshot used to build upload options.
func (b *Backend) buildRunCoreFromSnapshot(rt backendRuntimeSnapshot, opts runOptions) (api.Core, *logging.Logger, error) {
	effectiveLogLevel := logging.ResolveEffectiveLevel(rt.cfg.Logging.Level, opts.RunLogLevel, opts.Debug)
	logger, err := logging.NewWithLevel(rt.cfg.Logging, rt.cfg.MainSettings.DBPath, effectiveLogLevel)
	if err != nil {
		return nil, nil, fmt.Errorf("web: %w", err)
	}
	coreSvc, err := core.New(api.CoreDependencies{
		Config: rt.cfg,
		Logger: logger,
		Services: api.ServiceSet{
			Filesystem: filesystem.NewValidator(),
		},
		Repository: b.repo,
	})
	if err != nil {
		_ = logger.Close()
		return nil, nil, fmt.Errorf("web: %w", err)
	}
	return coreSvc, logger, nil
}

func buildRunUploadOptions(cfg config.Config, opts runOptions) api.UploadOptions {
	options := buildBaseMetadataOptions(cfg)
	options.Debug = opts.Debug
	options.DryRun = opts.Debug
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

func (b *Backend) applyConfig(cfg config.Config) error {
	rt, err := b.buildConfigRuntime(context.Background(), cfg)
	if err != nil {
		return err
	}
	b.installConfigRuntime(cfg, rt)
	return nil
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

func preferredHistoryDescriptionOverride(overrides []api.DescriptionOverride) api.DescriptionOverride {
	if len(overrides) == 0 {
		return api.DescriptionOverride{}
	}
	for _, override := range overrides {
		if strings.TrimSpace(override.GroupKey) == "" {
			return override
		}
	}
	for _, override := range overrides {
		if strings.TrimSpace(override.Description) != "" {
			return override
		}
	}
	return overrides[0]
}

func historyOverviewFromRepo(repo *db.SQLiteRepository, sourcePath string) (api.HistoryOverview, error) {
	if repo == nil {
		return api.HistoryOverview{}, errors.New("history repository not initialized")
	}
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return api.HistoryOverview{}, internalerrors.ErrInvalidInput
	}
	ctx := context.Background()
	metadata, err := repo.GetByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("web: %w", err)
	}
	overview := api.HistoryOverview{
		SourcePath:        metadata.Path,
		ReleaseTitle:      metadata.Title,
		ReleaseSource:     metadata.Source,
		ReleaseResolution: metadata.Resolution,
		MetadataUpdatedAt: metadata.UpdatedAt,
		Metadata:          metadata,
	}
	if externalIDs, err := repo.GetExternalIDs(ctx, trimmed); err == nil {
		overview.ExternalIDs = externalIDs
	}
	if externalMetadata, err := repo.GetExternalMetadata(ctx, trimmed); err == nil {
		overview.ExternalMetadata = externalMetadata
	}
	if releaseOverrides, err := repo.GetReleaseNameOverrides(ctx, trimmed); err == nil {
		overview.ReleaseNameOverrides = releaseOverrides
	}
	if descriptionOverrides, err := repo.ListDescriptionOverridesByPath(ctx, trimmed); err == nil {
		overview.DescriptionOverrides = append([]api.DescriptionOverride(nil), descriptionOverrides...)
		overview.DescriptionOverride = preferredHistoryDescriptionOverride(descriptionOverrides)
	}
	if playlistSelection, err := repo.GetPlaylistSelection(ctx, trimmed); err == nil {
		overview.PlaylistSelection = playlistSelection
	}
	trackerMetadata, err := repo.ListTrackerMetadataByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("web: %w", err)
	}
	overview.TrackerMetadata = trackerMetadata
	ruleFailures, err := repo.ListTrackerRuleFailuresByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("web: %w", err)
	}
	overview.TrackerRuleFailures = ruleFailures
	screenshots, err := repo.ListScreenshotsByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("web: %w", err)
	}
	overview.Screenshots = screenshots
	finalSelections, err := repo.ListFinalSelections(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("web: %w", err)
	}
	overview.FinalSelections = finalSelections
	uploadedImages, err := repo.ListUploadedImagesByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("web: %w", err)
	}
	overview.UploadedImages = uploadedImages
	uploadHistory, err := repo.ListUploadHistoryByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("web: %w", err)
	}
	overview.UploadHistory = uploadHistory
	if len(uploadHistory) > 0 {
		overview.LatestUploadStatus = uploadHistory[0].Status
		overview.LatestUploadAt = uploadHistory[0].CreatedAt
	}
	overview.StatusLabel = historyStatusLabel(overview.LatestUploadStatus, len(ruleFailures))
	return overview, nil
}

func historyStatusLabel(rawStatus string, ruleFailureCount int) string {
	status := strings.TrimSpace(strings.ToLower(rawStatus))
	switch status {
	case "pending":
		return "Pending"
	case "pending-internal":
		return "Pending Internal"
	case "uploaded", "success", "completed":
		return "Uploaded"
	case "failed", "error":
		return "Failed"
	}
	if status != "" {
		normalized := strings.ReplaceAll(status, "-", " ")
		words := strings.Fields(normalized)
		for idx, word := range words {
			words[idx] = strings.ToUpper(word[:1]) + word[1:]
		}
		return strings.Join(words, " ")
	}
	if ruleFailureCount > 0 {
		return "Rule Issues"
	}
	return "Stored"
}
