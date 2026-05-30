// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/base64"
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
	"github.com/autobrr/upbrr/internal/guiapp"
	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/internal/imagehostpolicy"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const previewTimeout = 30 * time.Minute

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
	RunLogLevel string
}

func NewBackend(cfg config.Config, hub *eventHub) (*Backend, error) {
	return NewBackendWithContext(context.Background(), cfg, hub)
}

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
	if err := repo.ClearUIState(ctx); err != nil {
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
	if err := b.requireCore(); err != nil {
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
		Options:         b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
		ConfirmBDMVRescan:    confirmBDMVRescan,
	}

	return wrapWebResult(b.currentCore().FetchMetadataPreview(progressCtx, req))
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

func (b *Backend) FetchTrackerDryRun(sessionID string, path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackersList []string, ignoreDupesFor []string, questionnaireAnswers map[string]map[string]string, descriptionGroups []api.DescriptionBuilderGroup, debug bool, runLogLevel string) (api.TrackerDryRunPreview, error) {
	if err := b.requireCore(); err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	runOpts, err := b.buildRunOptions(debug, runLogLevel)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	runCore, runLogger, err := b.buildRunCore(runOpts)
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
		Options:                     buildRunUploadOptions(b.currentConfig(), runOpts),
		ExternalIDOverrides:         overrides,
		ReleaseNameOverrides:        nameOverrides,
		TrackerQuestionnaireAnswers: cloneQuestionnaireAnswers(questionnaireAnswers),
	}
	req.Options.DryRun = true
	if err := guishared.SeedRunCorePreparedMeta(ctx, b.currentCore(), runCore, req); err != nil {
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

func (b *Backend) ListUIStates() (api.UIStateList, error) {
	if b == nil || b.repo == nil {
		return api.UIStateList{}, errors.New("config repository not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	states, err := b.repo.ListUIStates(ctx)
	if err != nil {
		return api.UIStateList{}, fmt.Errorf("web: %w", err)
	}
	return api.UIStateList{States: states}, nil
}

func (b *Backend) GetUIState(id string) (api.UIStateRecord, error) {
	if b == nil || b.repo == nil {
		return api.UIStateRecord{}, errors.New("config repository not initialized")
	}
	if strings.TrimSpace(id) == "" {
		return api.UIStateRecord{}, errors.New("id is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(b.repo.LoadUIState(ctx, id))
}

func (b *Backend) SaveUIState(id string, label string, state api.UIState) error {
	if b == nil || b.repo == nil {
		return errors.New("config repository not initialized")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("id is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebError(b.repo.SaveUIState(ctx, id, label, state))
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
	if err := b.requireCore(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(b.currentCore().ListUploadCandidates(ctx, api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}))
}

func (b *Backend) ListUploadedImages(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) ([]api.UploadedImageLink, error) {
	if err := b.requireCore(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(b.currentCore().ListUploadedImages(ctx, api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: b.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}))
}

func (b *Backend) UploadImages(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackersList []string, host string, images []api.ScreenshotImage) (api.UploadImagesResult, error) {
	if err := b.requireCore(); err != nil {
		return api.UploadImagesResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(b.currentCore().UploadImages(ctx, api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: b.baseUploadOptions(),

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

func (b *Backend) GetConfig() (string, error) {
	cfg, err := b.exportableConfig()
	if err != nil {
		return "", err
	}
	return wrapWebResult(config.ExportToJSON(cfg))
}

func (b *Backend) ExportConfig() (string, error) {
	cfg, err := b.exportableConfig()
	if err != nil {
		return "", err
	}

	allowPlaintext, err := b.allowUnencryptedExport()
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

func (b *Backend) exportableConfig() (*config.Config, error) {
	if b.repo == nil {
		return nil, errors.New("config repository not initialized")
	}
	cfg, err := config.LoadFromDatabase(context.Background(), b.repo)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	if strings.TrimSpace(cfg.MainSettings.DBPath) == "" {
		cfg.MainSettings.DBPath = b.currentConfig().MainSettings.DBPath
	}
	if cfg.Trackers.Trackers == nil {
		cfg.Trackers.Trackers = map[string]config.TrackerConfig{}
	}
	if cfg.Trackers.DefaultTrackers == nil {
		cfg.Trackers.DefaultTrackers = config.CSVList{}
	}
	return cfg, nil
}

func (b *Backend) allowUnencryptedExport() (bool, error) {
	material, err := authmaterial.LoadFromDBPath(b.currentConfig().MainSettings.DBPath)
	if err == nil {
		return material.AllowUnencryptedExport, nil
	}
	if errors.Is(err, authmaterial.ErrUnavailable) {
		return false, nil
	}
	return false, fmt.Errorf("web: %w", err)
}

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
	if cfg.MainSettings.DBPath != currentCfg.MainSettings.DBPath {
		return errors.New("changing main_settings.db_path requires restart")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("web: %w", err)
	}
	runtimeCfg := *cfg
	config.ApplyEnvOverrides(&runtimeCfg)
	runtimeCfg.MainSettings.DBPath = currentCfg.MainSettings.DBPath
	if err := runtimeCfg.Validate(); err != nil {
		return fmt.Errorf("web: %w", err)
	}
	if err := config.SaveToDatabase(context.Background(), cfg, b.repo); err != nil {
		return fmt.Errorf("web: %w", err)
	}
	return b.applyConfig(runtimeCfg)
}

const configImportMaxBytes = importer.MaxFileBytes

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

	if err := config.SaveToDatabase(context.Background(), cfg, b.repo); err != nil {
		return "", nil, fmt.Errorf("web: %w", err)
	}

	if err := b.applyConfig(runtimeCfg); err != nil {
		return "", nil, err
	}

	result := "imported config"
	if len(warnings) > 0 {
		result += fmt.Sprintf(" (%d warnings)", len(warnings))
	}
	return result, warnings, nil
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

func (b *Backend) StartLogStream(sessionID string) (string, error) {
	logger := b.currentLogger()
	if logger == nil {
		return "", errors.New("logger not initialized")
	}
	streamID, err := randomString(12)
	if err != nil {
		return "", err
	}
	session := &backendLogStream{
		id:        streamID,
		sessionID: sessionID,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	subID, ch := logger.Subscribe(0)
	session.logger = logger
	session.subID = subID

	b.streamMu.Lock()
	b.streams[streamID] = session
	b.streamMu.Unlock()

	b.streamWG.Add(1)
	go func() {
		defer b.streamWG.Done()
		defer close(session.done)
		for {
			select {
			case entry, ok := <-ch:
				if !ok {
					return
				}
				b.hub.Emit(sessionID, "log:stream:"+streamID, entry)
			case <-session.stop:
				session.logger.Unsubscribe(session.subID)
				return
			}
		}
	}()

	return streamID, nil
}

func (b *Backend) StopLogStream(streamID string) error {
	b.streamMu.Lock()
	session := b.streams[streamID]
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
		_ = b.StopLogStream(streamID)
	}
}

func (b *Backend) buildRunOptions(debug bool, runLogLevel string) (runOptions, error) {
	if strings.TrimSpace(runLogLevel) == "" {
		return runOptions{Debug: debug}, nil
	}
	normalized, err := api.ParseLogLevel(runLogLevel)
	if err != nil {
		return runOptions{}, fmt.Errorf("web: %w", err)
	}
	return runOptions{Debug: debug, RunLogLevel: normalized}, nil
}

func (b *Backend) buildRunCore(opts runOptions) (api.Core, *logging.Logger, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return nil, nil, err
	}
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
	rt, err := guishared.BuildRuntime(context.Background(), cfg, b.repo)
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}
	oldCore, oldLogger := b.replaceRuntime(cfg, rt.Core, rt.Logger)
	if oldCore != nil {
		_ = oldCore.Close()
	}
	if oldLogger != nil {
		_ = oldLogger.Close()
	}
	return nil
}

func (b *Backend) isPathWithinManagedDirs(candidate string) bool {
	tmpDir, err := db.Subdir(b.currentConfig().MainSettings.DBPath, "tmp")
	if err == nil && pathWithinRoot(tmpDir, candidate) {
		return true
	}
	logPath, err := logging.LogPath(b.currentConfig().MainSettings.DBPath)
	if err == nil && pathWithinRoot(filepath.Dir(logPath), candidate) {
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
	if !pathWithinRoot(absTmpRoot, absCandidate) {
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
	if absTarget == absRoot || !pathWithinRoot(absRoot, absTarget) {
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

func pathWithinRoot(root string, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)
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
