// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/config/importer"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/core"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/internal/imagehostpolicy"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/trackericon"
	"github.com/autobrr/upbrr/internal/trackerauth"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const previewTimeout = 30 * time.Minute
const bdinfoProgressEvent = "bdinfo:progress"
const metadataProgressEvent = "metadata:progress"

// App owns the Wails-bound backend state for the desktop GUI.
type App struct {
	runtimeCtx  *appRuntimeContext
	runtimeMu   sync.RWMutex
	cfg         config.Config
	core        api.Core
	coreInitErr error
	logger      *logging.Logger
	repo        *db.SQLiteRepository
	streamMu    sync.Mutex
	streams     map[string]*logStreamSession
	dupeMu      sync.Mutex
	dupes       map[string]*dupeCheckJob
	uploadMu    sync.Mutex
	uploads     map[string]*trackerUploadJob
	uploadWG    sync.WaitGroup
}

type blurayCandidateSelector interface {
	SelectBlurayCandidate(ctx context.Context, sourcePath string, releaseID string) (api.MetadataPreview, error)
}

// NewApp creates a Wails backend using the default background context.
func NewApp(configPath string, configProvided bool) (*App, error) {
	return NewAppWithContext(context.Background(), configPath, configProvided)
}

// NewAppWithContext bootstraps config, logging, repository access, and the core
// service used by Wails calls. Invalid runtime config leaves upload features
// disabled until settings are saved, while config/settings calls remain usable.
func NewAppWithContext(ctx context.Context, configPath string, configProvided bool) (*App, error) {
	if ctx == nil {
		return nil, errors.New("guiapp: context is required")
	}
	cfg, dbPath, err := configstore.Bootstrap(ctx, configPath, configProvided, true)
	if err != nil {
		return nil, fmt.Errorf("gui: %w", err)
	}

	logger, err := logging.New(cfg.Logging, cfg.MainSettings.DBPath)
	if err != nil {
		return nil, fmt.Errorf("gui: %w", err)
	}

	repo, err := db.OpenWithLoggerContext(ctx, dbPath, logger)
	if err != nil {
		_ = logger.Close()
		return nil, fmt.Errorf("gui: %w", err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		_ = repo.Close()
		_ = logger.Close()
		return nil, fmt.Errorf("gui: %w", err)
	}

	var coreSvc api.Core
	var coreInitErr error
	if err := cfg.Validate(); err != nil {
		coreInitErr = err
		logger.Warnf("gui: config invalid, core disabled until settings are fixed: %v", err)
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
			return nil, fmt.Errorf("gui: %w", err)
		}
	}

	return &App{
		runtimeCtx:  newAppRuntimeContext(ctx),
		cfg:         cfg,
		core:        coreSvc,
		coreInitErr: coreInitErr,
		logger:      logger,
		repo:        repo,
		streams:     make(map[string]*logStreamSession),
		dupes:       make(map[string]*dupeCheckJob),
		uploads:     make(map[string]*trackerUploadJob),
	}, nil
}

func (a *App) startup(ctx context.Context) {
	a.runtimeCtx.Store(ctx)
}

func (a *App) shutdown(_ context.Context) {
	a.stopAllLogStreams()
	a.stopAllDupeJobs()
	a.stopAllUploadJobs()
	rt := a.runtimeSnapshot()
	if rt.core != nil {
		_ = rt.core.Close()
	}
	if a.repo != nil {
		_ = a.repo.Close()
	}
	if rt.logger != nil {
		_ = rt.logger.Close()
	}
}

func (a *App) BrowseFile() (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}
	ctx, err := a.readyRuntimeContext()
	if err != nil {
		return "", err
	}

	selection, err := runtime.OpenFileDialog(ctx, runtime.OpenDialogOptions{
		Title:   "Select a file",
		Filters: []runtime.FileFilter{videoFileDialogFilter()},
	})
	if err != nil {
		return "", fmt.Errorf("gui: open file dialog: %w", err)
	}
	return selection, nil
}

func videoFileDialogFilter() runtime.FileFilter {
	extensions := filesystem.SupportedVideoExtensions()
	patterns := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		patterns = append(patterns, "*"+ext)
	}
	pattern := strings.Join(patterns, ";")
	return runtime.FileFilter{
		DisplayName: "Video files (" + pattern + ")",
		Pattern:     pattern,
	}
}

func (a *App) BrowseFiles() ([]string, error) {
	return a.BrowseImageFiles()
}

func (a *App) BrowseImageFiles() ([]string, error) {
	if a == nil {
		return nil, errors.New("app not initialized")
	}
	ctx, err := a.readyRuntimeContext()
	if err != nil {
		return nil, err
	}

	selection, err := runtime.OpenMultipleFilesDialog(ctx, runtime.OpenDialogOptions{
		Title: "Select images",
		Filters: []runtime.FileFilter{
			{DisplayName: "Image files", Pattern: "*.png;*.jpg;*.jpeg;*.webp"},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("gui: open files dialog: %w", err)
	}
	return selection, nil
}

func (a *App) BrowseFolder() (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}
	ctx, err := a.readyRuntimeContext()
	if err != nil {
		return "", err
	}

	selection, err := runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{
		Title: "Select a folder",
	})
	if err != nil {
		return "", fmt.Errorf("gui: open directory dialog: %w", err)
	}
	return selection, nil
}

func (a *App) BrowsePath() (string, error) {
	selection, err := a.BrowseFile()
	if err != nil {
		return "", err
	}
	if selection != "" {
		return selection, nil
	}

	return a.BrowseFolder()
}

func (a *App) BrowseDirectory(path string, mode string) (api.BrowseDirectoryResponse, error) {
	if a == nil {
		return api.BrowseDirectoryResponse{}, errors.New("app not initialized")
	}
	fallback := guishared.BrowseDirectoryFallback(a.currentConfig().MainSettings.DBPath)
	return wrapGUIResult(guishared.BrowseDirectory(api.BrowseDirectoryRequest{Path: path, Mode: mode}, fallback))
}

func validateExternalURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("url is required")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if parsed == nil || !parsed.IsAbs() {
		return "", errors.New("url must be absolute")
	}

	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", errors.New("unsupported url scheme")
	}

	if strings.TrimSpace(parsed.Host) == "" {
		return "", errors.New("url host is required")
	}

	return parsed.String(), nil
}

func (a *App) OpenExternalURL(rawURL string) error {
	if a == nil {
		return errors.New("app not initialized")
	}
	ctx, err := a.readyRuntimeContext()
	if err != nil {
		return err
	}

	validatedURL, err := validateExternalURL(rawURL)
	if err != nil {
		return fmt.Errorf("open external url: %w", err)
	}

	runtime.BrowserOpenURL(ctx, validatedURL)
	return nil
}

func (a *App) DetectDiscType(path string) (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	return wrapGUIResult(filesystem.DetectDiscType(ctx, path))
}

func (a *App) FetchMetadata(path string, sourceLookupURL string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string) (api.MetadataPreview, error) {
	if err := a.requireCore(); err != nil {
		return api.MetadataPreview{}, err
	}
	if strings.TrimSpace(path) == "" {
		return api.MetadataPreview{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	trimmedPath := strings.TrimSpace(path)
	progressCtx := api.WithMetadataProgressReporter(ctx, func(update api.MetadataProgressUpdate) {
		if strings.TrimSpace(update.Path) == "" {
			update.Path = trimmedPath
		}
		if strings.TrimSpace(update.Timestamp) == "" {
			update.Timestamp = time.Now().UTC().Format(time.RFC3339)
		}
		runtime.EventsEmit(ctx, metadataProgressEvent, update)
	})

	req := api.Request{
		Paths:           []string{trimmedPath},
		Mode:            api.ModeGUI,
		Trackers:        slices.Clone(trackers),
		SourceLookupURL: strings.TrimSpace(sourceLookupURL),
		Options:         a.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return wrapGUIResult(a.currentCore().FetchMetadataPreview(progressCtx, req))
}

func (a *App) SelectBlurayCandidate(path string, releaseID string) (api.MetadataPreview, error) {
	if err := a.requireCore(); err != nil {
		return api.MetadataPreview{}, err
	}
	if strings.TrimSpace(path) == "" {
		return api.MetadataPreview{}, errors.New("path is required")
	}
	if strings.TrimSpace(releaseID) == "" {
		return api.MetadataPreview{}, errors.New("release ID is required")
	}
	selector, ok := a.currentCore().(blurayCandidateSelector)
	if !ok {
		return api.MetadataPreview{}, errors.New("blu-ray candidate selection is unavailable in this build")
	}
	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	return wrapGUIResult(selector.SelectBlurayCandidate(ctx, path, releaseID))
}

func (a *App) ResetMetadata(path string, sourceLookupURL string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string) (api.MetadataPreview, error) {
	if err := a.requireCore(); err != nil {
		return api.MetadataPreview{}, err
	}
	if a.repo == nil {
		return api.MetadataPreview{}, errors.New("config repository not initialized")
	}
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return api.MetadataPreview{}, errors.New("path is required")
	}
	logger := a.currentLogger()
	if logger != nil {
		logger.Infof("gui: reset metadata started path=%s", trimmedPath)
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	progressCtx := api.WithMetadataProgressReporter(ctx, func(update api.MetadataProgressUpdate) {
		if strings.TrimSpace(update.Path) == "" {
			update.Path = trimmedPath
		}
		if strings.TrimSpace(update.Timestamp) == "" {
			update.Timestamp = time.Now().UTC().Format(time.RFC3339)
		}
		runtime.EventsEmit(ctx, metadataProgressEvent, update)
	})

	tmpRoot, err := db.Subdir(a.currentConfig().MainSettings.DBPath, "tmp")
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: resolve tmp dir: %w", err)
	}

	artifactPaths := make([]string, 0)
	shots, err := a.repo.ListScreenshotsByPath(ctx, trimmedPath)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: list screenshots: %w", err)
	}
	for _, shot := range shots {
		artifactPaths = append(artifactPaths, shot.ImagePath)
	}

	uploaded, err := a.repo.ListUploadedImagesByPath(ctx, trimmedPath)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: list uploaded images: %w", err)
	}
	for _, image := range uploaded {
		artifactPaths = append(artifactPaths, image.ImagePath)
	}

	finals, err := a.repo.ListFinalSelections(ctx, trimmedPath)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: list final selections: %w", err)
	}
	for _, image := range finals {
		artifactPaths = append(artifactPaths, image.ImagePath)
	}
	artifactPaths = slices.Compact(artifactPaths)
	if logger != nil {
		logger.Debugf("gui: reset metadata collected artifacts path=%s files=%d", trimmedPath, len(artifactPaths))
	}

	tmpDirs := make(map[string]struct{})
	fallbackBase := paths.ReleaseTempBase(api.PreparedMetadata{}, trimmedPath)
	tmpDirs[filepath.Join(tmpRoot, fallbackBase)] = struct{}{}

	stored, err := a.repo.GetByPath(ctx, trimmedPath)
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
		if !ok {
			continue
		}
		tmpDirs[contentRoot] = struct{}{}
	}

	if err := a.repo.PurgeContentData(ctx, trimmedPath); err != nil {
		return api.MetadataPreview{}, fmt.Errorf("reset metadata: purge sqlite: %w", err)
	}
	if logger != nil {
		logger.Infof("gui: reset metadata sqlite purge completed path=%s", trimmedPath)
	}

	removedFiles := 0
	for _, filePath := range artifactPaths {
		removed, err := removeIfWithinRoot(tmpRoot, filePath, false)
		if err != nil {
			if logger != nil {
				logger.Warnf("gui: reset metadata remove file failed %q: %v", filePath, err)
			}
			continue
		}
		if removed {
			removedFiles++
		}
	}
	removedDirs := 0
	for dir := range tmpDirs {
		removed, err := removeIfWithinRoot(tmpRoot, dir, true)
		if err != nil {
			if logger != nil {
				logger.Warnf("gui: reset metadata remove tmp dir failed %q: %v", dir, err)
			}
			continue
		}
		if removed {
			removedDirs++
		}
	}
	if logger != nil {
		logger.Infof("gui: reset metadata artifacts cleaned path=%s files_removed=%d dirs_removed=%d", trimmedPath, removedFiles, removedDirs)
	}

	req := api.Request{
		Paths:           []string{trimmedPath},
		Mode:            api.ModeGUI,
		Trackers:        slices.Clone(trackers),
		SourceLookupURL: strings.TrimSpace(sourceLookupURL),
		Options:         a.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}
	preview, err := a.currentCore().FetchMetadataPreview(progressCtx, req)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("gui: %w", err)
	}
	if logger != nil {
		logger.Infof("gui: reset metadata completed path=%s release=%s", trimmedPath, strings.TrimSpace(preview.ReleaseName))
	}
	return preview, nil
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
	if len(parts) < 1 || strings.TrimSpace(parts[0]) == "" || parts[0] == "." {
		return "", false
	}
	return filepath.Join(absTmpRoot, parts[0]), true
}

func removeIfWithinRoot(root string, target string, recursive bool) (bool, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return false, nil
	}
	absRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return false, fmt.Errorf("cleanup path: resolve root path: %w", err)
	}
	absTarget, err := filepath.Abs(trimmed)
	if err != nil {
		return false, fmt.Errorf("cleanup path: resolve target path: %w", err)
	}
	if pathutil.SamePath(absRoot, absTarget) {
		return false, nil
	}
	if !pathutil.IsWithinRoot(absRoot, absTarget) {
		return false, nil
	}
	if recursive {
		if _, err := os.Stat(absTarget); err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, fmt.Errorf("cleanup path: stat target: %w", err)
		}
		if err := os.RemoveAll(absTarget); err != nil {
			return false, fmt.Errorf("cleanup path: remove target tree: %w", err)
		}
		return true, nil
	}
	if err := os.Remove(absTarget); err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("cleanup path: remove target: %w", err)
	}
	if _, err := os.Stat(absTarget); err == nil {
		return false, nil
	}
	return true, nil
}

func (a *App) CheckDupes(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string) (api.DupeCheckSummary, error) {
	if a == nil || a.currentCore() == nil {
		return api.DupeCheckSummary{}, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return api.DupeCheckSummary{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	trimmedPath := strings.TrimSpace(path)

	req := api.Request{
		Paths:    []string{trimmedPath},
		Mode:     api.ModeGUI,
		Trackers: slices.Clone(trackers),
		Options:  a.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return wrapGUIResult(a.currentCore().CheckDupes(ctx, req))
}

func (a *App) FetchPreparation(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string, ignoreDupesFor []string) (api.PreparationPreview, error) {
	if a == nil || a.currentCore() == nil {
		return api.PreparationPreview{}, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return api.PreparationPreview{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	trimmedPath := strings.TrimSpace(path)

	req := api.Request{
		Paths:          []string{trimmedPath},
		Mode:           api.ModeGUI,
		Trackers:       slices.Clone(trackers),
		IgnoreDupesFor: normalizeTrackerList(ignoreDupesFor),
		Options:        a.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	progressCtx := bdinfo.WithProgressReporter(ctx, func(line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		runtime.EventsEmit(ctx, bdinfoProgressEvent, map[string]string{
			"path": trimmedPath,
			"line": line,
		})
	})

	return wrapGUIResult(a.currentCore().FetchPreparationPreview(progressCtx, req))
}

func (a *App) FetchTrackerDryRun(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string, ignoreDupesFor []string, questionnaireAnswers map[string]map[string]string, descriptionGroups []api.DescriptionBuilderGroup, debug bool, noSeed bool, runLogLevel string) (api.TrackerDryRunPreview, error) {
	rt, err := a.requireRuntime()
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	if strings.TrimSpace(path) == "" {
		return api.TrackerDryRunPreview{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return api.TrackerDryRunPreview{}, fmt.Errorf("gui: tracker dry-run preview canceled: %w", err)
	}
	trimmedPath := strings.TrimSpace(path)
	runOpts, err := a.buildRunOptions(debug, noSeed, runLogLevel)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	if logger := a.currentLogger(); logger != nil {
		logger.Debugf("gui: tracker dry-run request path=%s debug=%t no_seed=%t run_log_level=%s", trimmedPath, debug, noSeed, runOpts.RunLogLevel)
	}
	runCore, runLogger, err := a.buildRunCoreFromSnapshot(rt, runOpts)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	defer func() {
		_ = runCore.Close()
		_ = runLogger.Close()
	}()

	req := api.Request{
		Paths:                       []string{trimmedPath},
		Mode:                        api.ModeGUI,
		DescriptionGroups:           api.CloneDescriptionBuilderGroups(descriptionGroups),
		Trackers:                    slices.Clone(trackers),
		IgnoreDupesFor:              normalizeTrackerList(ignoreDupesFor),
		IgnoreTrackerRuleFailures:   false,
		Options:                     buildRunUploadOptions(rt.cfg, runOpts),
		ExternalIDOverrides:         overrides,
		ReleaseNameOverrides:        nameOverrides,
		TrackerQuestionnaireAnswers: cloneQuestionnaireAnswers(questionnaireAnswers),
	}
	req.Options.DryRun = true
	if err := guishared.SeedRunCorePreparedMeta(ctx, rt.core, runCore, req); err != nil {
		return api.TrackerDryRunPreview{}, fmt.Errorf("gui: %w", err)
	}

	progressCtx := api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
		runtime.EventsEmit(ctx, trackerUploadProgressEvent, update)
	})
	progressCtx = bdinfo.WithProgressReporter(progressCtx, func(line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		runtime.EventsEmit(ctx, bdinfoProgressEvent, map[string]string{
			"path": trimmedPath,
			"line": line,
		})
	})

	return wrapGUIResult(runCore.FetchTrackerDryRunPreview(progressCtx, req))
}

func (a *App) FetchDescriptionBuilder(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string, ignoreDupesFor []string) (api.DescriptionBuilderPreview, error) {
	if a == nil || a.currentCore() == nil {
		return api.DescriptionBuilderPreview{}, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return api.DescriptionBuilderPreview{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:          []string{path},
		Mode:           api.ModeGUI,
		Trackers:       slices.Clone(trackers),
		IgnoreDupesFor: normalizeTrackerList(ignoreDupesFor),
		Options:        a.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return wrapGUIResult(a.currentCore().FetchDescriptionBuilderPreview(ctx, req))
}

func (a *App) RenderDescription(raw string) (string, error) {
	if a == nil || a.currentCore() == nil {
		return "", errors.New("app not initialized")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	return wrapGUIResult(a.currentCore().RenderDescription(ctx, raw))
}

func (a *App) SaveDescriptionOverride(path string, groupKey string, raw string, trackers []string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) (api.DescriptionBuilderGroup, error) {
	if a == nil || a.currentCore() == nil {
		return api.DescriptionBuilderGroup{}, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return api.DescriptionBuilderGroup{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:                    []string{path},
		Mode:                     api.ModeGUI,
		DescriptionOverrideGroup: strings.TrimSpace(groupKey),
	}

	req.Trackers = slices.Clone(trackers)
	req.ExternalIDOverrides = overrides
	req.ReleaseNameOverrides = nameOverrides

	return wrapGUIResult(a.currentCore().SaveDescriptionOverride(ctx, req, raw))
}

func (a *App) DiscoverPlaylists(path string) ([]api.PlaylistInfo, error) {
	if a == nil || a.currentCore() == nil {
		return nil, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	return wrapGUIResult(a.currentCore().DiscoverPlaylists(ctx, path))
}

func (a *App) SavePlaylistSelection(path string, playlists []string, useAll bool) error {
	if a == nil || a.currentCore() == nil {
		return errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	return wrapGUIError(a.currentCore().SavePlaylistSelection(ctx, path, playlists, useAll))
}

func (a *App) LoadPlaylistSelection(path string) (api.PlaylistSelection, error) {
	if a == nil || a.currentCore() == nil {
		return api.PlaylistSelection{}, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return api.PlaylistSelection{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	return wrapGUIResult(a.currentCore().LoadPlaylistSelection(ctx, path))
}

func (a *App) ListHistory() ([]api.HistoryEntry, error) {
	if err := a.requireHistoryRepo(); err != nil {
		return nil, err
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	return a.listHistoryFromRepo(ctx)
}

func (a *App) GetHistoryOverview(sourcePath string) (api.HistoryOverview, error) {
	if err := a.requireHistoryRepo(); err != nil {
		return api.HistoryOverview{}, err
	}
	if strings.TrimSpace(sourcePath) == "" {
		return api.HistoryOverview{}, errors.New("source path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	return a.getHistoryOverviewFromRepo(ctx, sourcePath)
}

func (a *App) DeleteHistoryRelease(sourcePath string) error {
	if a == nil || a.currentCore() == nil {
		return errors.New("app not initialized")
	}
	trimmedPath := strings.TrimSpace(sourcePath)
	if trimmedPath == "" {
		return errors.New("source path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	if err := a.currentCore().DeleteHistoryRelease(ctx, trimmedPath); err != nil {
		return fmt.Errorf("delete history release: %w", err)
	}
	return nil
}

func (a *App) FetchScreenshotPlan(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) (api.ScreenshotPlan, error) {
	rt, err := a.requireRuntime()
	if err != nil {
		return api.ScreenshotPlan{}, err
	}
	if strings.TrimSpace(path) == "" {
		return api.ScreenshotPlan{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: rt.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return wrapGUIResult(rt.core.FetchScreenshotPlan(ctx, req))
}

func (a *App) GenerateScreenshots(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, selections []api.ScreenshotSelection, purpose api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	rt, err := a.requireRuntime()
	if err != nil {
		return api.ScreenshotResult{}, err
	}
	if strings.TrimSpace(path) == "" {
		return api.ScreenshotResult{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: rt.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return wrapGUIResult(rt.core.GenerateScreenshots(ctx, req, selections, purpose))
}

func (a *App) ListUploadCandidates(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) ([]api.ScreenshotImage, error) {
	rt, err := a.requireRuntime()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: rt.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return wrapGUIResult(rt.core.ListUploadCandidates(ctx, req))
}

func (a *App) ListUploadedImages(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) ([]api.UploadedImageLink, error) {
	rt, err := a.requireRuntime()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: rt.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return wrapGUIResult(rt.core.ListUploadedImages(ctx, req))
}

func (a *App) UploadImages(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string, host string, images []api.ScreenshotImage) (api.UploadImagesResult, error) {
	rt, err := a.requireRuntime()
	if err != nil {
		return api.UploadImagesResult{}, err
	}
	if strings.TrimSpace(path) == "" {
		return api.UploadImagesResult{}, errors.New("path is required")
	}
	if strings.TrimSpace(host) == "" {
		return api.UploadImagesResult{}, errors.New("host is required")
	}
	if len(images) == 0 {
		return api.UploadImagesResult{}, errors.New("no images selected")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: rt.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
		Trackers:             slices.Clone(trackers),
	}

	return wrapGUIResult(rt.core.UploadImages(ctx, req, host, images))
}

func (a *App) DeleteUploadedImage(path string, imagePath string, host string) error {
	if a == nil || a.currentCore() == nil {
		return errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	if strings.TrimSpace(imagePath) == "" {
		return errors.New("image path is required")
	}
	if strings.TrimSpace(host) == "" {
		return errors.New("host is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths: []string{path},
		Mode:  api.ModeGUI,
	}

	return wrapGUIError(a.currentCore().DeleteUploadedImage(ctx, req, imagePath, host))
}

func (a *App) PreviewScreenshotFrame(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, timestampSeconds float64) (string, error) {
	if a == nil || a.currentCore() == nil {
		return "", errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: a.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	preview, err := a.currentCore().PreviewScreenshotFrame(ctx, req, timestampSeconds)
	if err != nil {
		return "", fmt.Errorf("gui: %w", err)
	}
	if len(preview.ImageBytes) == 0 {
		return "", errors.New("preview image is empty")
	}
	encoded := base64.StdEncoding.EncodeToString(preview.ImageBytes)
	return "data:image/png;base64," + encoded, nil
}

func (a *App) DeleteScreenshot(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, imagePath string) error {
	if a == nil || a.currentCore() == nil {
		return errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	if strings.TrimSpace(imagePath) == "" {
		return errors.New("image path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: a.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return wrapGUIError(a.currentCore().DeleteScreenshot(ctx, req, imagePath))
}

func (a *App) DeleteTrackerImageURL(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, url string) error {
	if a == nil || a.currentCore() == nil {
		return errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	if strings.TrimSpace(url) == "" {
		return errors.New("tracker image URL is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: a.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return wrapGUIError(a.currentCore().DeleteTrackerImageURL(ctx, req, url))
}

func (a *App) SaveFinalScreenshotSelections(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, images []api.ScreenshotImage) error {
	if a == nil || a.currentCore() == nil {
		return errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: a.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return wrapGUIError(a.currentCore().SaveFinalScreenshotSelections(ctx, req, images))
}

func (a *App) ImportMenuImages(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, paths []string) error {
	if a == nil || a.currentCore() == nil {
		return errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths:   []string{path},
		Mode:    api.ModeGUI,
		Options: a.baseUploadOptions(),

		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return wrapGUIError(a.currentCore().ImportMenuImages(ctx, req, paths))
}

func (a *App) ReadScreenshotImage(path string) (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("path is required")
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return "", fmt.Errorf("read preview image: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	return "data:image/png;base64," + encoded, nil
}

// GetConfig returns the GUI settings payload as encrypted JSON. If no config
// rows exist yet, it exports the current runtime config without persisting it.
func (a *App) GetConfig() (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}
	if a.repo == nil {
		return "", errors.New("config repository not initialized")
	}

	ctx := a.runtimeContext()
	rt := a.runtimeSnapshot()

	cfg, err := config.LoadFromDatabase(ctx, a.repo)
	if err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			cfg = &rt.cfg
		} else {
			return "", fmt.Errorf("gui: %w", err)
		}
	}
	normalized, err := normalizeGUIConfigForExport(cfg, rt.cfg.MainSettings.DBPath)
	if err != nil {
		return "", err
	}

	return wrapGUIResult(config.ExportToJSON(normalized))
}

func (a *App) GetApplicationInfo() (api.ApplicationInfo, error) {
	if a == nil {
		return api.ApplicationInfo{}, errors.New("app not initialized")
	}
	return api.CurrentApplicationInfo(), nil
}

func (a *App) GetDefaultConfig() (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		return "", fmt.Errorf("gui: %w", err)
	}

	return wrapGUIResult(config.ExportToJSON(cfg))
}

func (a *App) ListKnownTrackers() ([]string, error) {
	if a == nil {
		return nil, errors.New("app not initialized")
	}

	return trackers.KnownTrackers(), nil
}

// ListTrackerAuthCapabilities returns tracker auth support metadata through the Wails bridge.
func (a *App) ListTrackerAuthCapabilities() ([]api.TrackerAuthCapability, error) {
	if a == nil {
		return nil, errors.New("app not initialized")
	}
	return wrapGUIResult(trackerauth.NewService(a.currentConfig()).Capabilities(context.Background()))
}

// GetTrackerAuthStatus returns the current local auth status for one tracker.
func (a *App) GetTrackerAuthStatus(tracker string) (api.TrackerAuthStatus, error) {
	if a == nil {
		return api.TrackerAuthStatus{}, errors.New("app not initialized")
	}
	return wrapGUIResult(trackerauth.NewService(a.currentConfig()).Status(context.Background(), tracker))
}

// ImportTrackerAuthCookies opens a native file picker, imports selected cookie content, and returns updated tracker auth status.
func (a *App) ImportTrackerAuthCookies(tracker string) (api.TrackerAuthStatus, error) {
	if a == nil {
		return api.TrackerAuthStatus{}, errors.New("app not initialized")
	}
	ctx, err := a.readyRuntimeContext()
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	selection, err := runtime.OpenFileDialog(ctx, runtime.OpenDialogOptions{
		Title: "Import tracker cookies",
		Filters: []runtime.FileFilter{
			{DisplayName: "Cookie files (*.txt;*.json)", Pattern: "*.txt;*.json"},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
	if err != nil {
		return api.TrackerAuthStatus{}, fmt.Errorf("gui: open tracker cookie dialog: %w", err)
	}
	if strings.TrimSpace(selection) == "" {
		return wrapGUIResult(trackerauth.NewService(a.currentConfig()).Status(context.Background(), tracker))
	}
	payload, err := os.ReadFile(selection)
	if err != nil {
		return api.TrackerAuthStatus{}, fmt.Errorf("gui: read tracker cookie file: %w", err)
	}
	return wrapGUIResult(trackerauth.NewService(a.currentConfig()).ImportCookies(context.Background(), tracker, filepath.Base(selection), string(payload)))
}

// ImportTrackerAuthCookieContent imports caller-supplied cookie content for one tracker and returns updated auth status.
func (a *App) ImportTrackerAuthCookieContent(tracker string, fileName string, content string) (api.TrackerAuthStatus, error) {
	if a == nil {
		return api.TrackerAuthStatus{}, errors.New("app not initialized")
	}
	return wrapGUIResult(trackerauth.NewService(a.currentConfig()).ImportCookies(context.Background(), tracker, fileName, content))
}

// TestTrackerAuth validates tracker auth remotely when supported and otherwise returns local status with an unsupported message.
func (a *App) TestTrackerAuth(tracker string) (api.TrackerAuthStatus, error) {
	if a == nil {
		return api.TrackerAuthStatus{}, errors.New("app not initialized")
	}
	return wrapGUIResult(trackerauth.NewService(a.currentConfig()).Validate(context.Background(), tracker))
}

// LoginTrackerAuth attempts credential-based tracker auth and returns status for missing credentials, unsupported login, or 2FA.
func (a *App) LoginTrackerAuth(tracker string, req api.TrackerAuthLoginRequest) (api.TrackerAuthStatus, error) {
	if a == nil {
		return api.TrackerAuthStatus{}, errors.New("app not initialized")
	}
	return wrapGUIResult(trackerauth.NewService(a.currentConfig()).Login(context.Background(), tracker, req))
}

// SubmitTrackerAuth2FA submits a manual 2FA code for an active tracker auth challenge.
func (a *App) SubmitTrackerAuth2FA(challengeID string, code string) (api.TrackerAuthStatus, error) {
	if a == nil {
		return api.TrackerAuthStatus{}, errors.New("app not initialized")
	}
	return wrapGUIResult(trackerauth.NewService(a.currentConfig()).Submit2FA(context.Background(), challengeID, code))
}

// DeleteTrackerAuth removes stored auth material for one tracker and returns refreshed local status.
func (a *App) DeleteTrackerAuth(tracker string) (api.TrackerAuthStatus, error) {
	if a == nil {
		return api.TrackerAuthStatus{}, errors.New("app not initialized")
	}
	return wrapGUIResult(trackerauth.NewService(a.currentConfig()).Delete(context.Background(), tracker))
}

func (a *App) GetImageHostPolicyMetadata() (imagehostpolicy.Metadata, error) {
	if a == nil {
		return imagehostpolicy.Metadata{}, errors.New("app not initialized")
	}

	return imagehostpolicy.PolicyMetadata(), nil
}

// SaveConfig validates encrypted GUI settings, builds the replacement runtime,
// then syncs cookie encryption metadata and saves the non-env config. Runtime
// build or save failures leave the persisted config and active runtime unchanged;
// env overrides apply only to the installed runtime config.
func (a *App) SaveConfig(payload string) error {
	if a == nil {
		return errors.New("app not initialized")
	}
	if a.repo == nil {
		return errors.New("config repository not initialized")
	}
	if strings.TrimSpace(payload) == "" {
		return errors.New("config payload is required")
	}

	cfg, err := config.ImportFromJSONEncrypted(payload)
	if err != nil {
		return fmt.Errorf("gui: %w", err)
	}
	if _, err := config.MergeMissingTrackerDefaults(cfg); err != nil {
		return fmt.Errorf("gui: %w", err)
	}
	currentCfg := a.currentConfig()
	if strings.TrimSpace(cfg.MainSettings.DBPath) == "" {
		cfg.MainSettings.DBPath = currentCfg.MainSettings.DBPath
	}
	if !pathutil.SamePath(cfg.MainSettings.DBPath, currentCfg.MainSettings.DBPath) {
		return errors.New("changing main_settings.db_path requires restart and is not supported in the GUI")
	}
	cfg.MainSettings.DBPath = currentCfg.MainSettings.DBPath
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("gui: %w", err)
	}
	runtimeCfg := *cfg
	config.ApplyEnvOverrides(&runtimeCfg)
	runtimeCfg.MainSettings.DBPath = currentCfg.MainSettings.DBPath
	if err := runtimeCfg.Validate(); err != nil {
		return fmt.Errorf("gui: %w", err)
	}

	ctx := a.runtimeContext()
	return a.saveAndApplyConfig(ctx, cfg, runtimeCfg, cfg.MainSettings.DBPath)
}

// normalizeGUIConfigForExport clones cfg and fills tracker defaults, legacy
// nils, and a missing DB path without mutating runtime or persisted config.
func normalizeGUIConfigForExport(cfg *config.Config, dbPath string) (*config.Config, error) {
	normalized, err := cloneGUIConfigForExport(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := config.MergeMissingTrackerDefaults(normalized); err != nil {
		return nil, fmt.Errorf("gui: %w", err)
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

// cloneGUIConfigForExport deep-copies config through JSON so export
// normalization cannot mutate the source snapshot.
func cloneGUIConfigForExport(cfg *config.Config) (*config.Config, error) {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("gui: clone config for export: marshal: %w", err)
	}
	var cloned config.Config
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, fmt.Errorf("gui: clone config for export: unmarshal: %w", err)
	}
	return &cloned, nil
}

// isDialogCancelledErr reports whether err is the result of the user closing a
// native Wails file dialog. On Windows, cancelling SaveFileDialog/OpenFileDialog
// surfaces as a non-nil error containing "shellItem is nil" rather than an
// empty path; treat that case the same as a normal cancel.
func isDialogCancelledErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "shellItem is nil")
}

// ExportConfig opens a native save dialog and writes the GUI config as YAML.
// It returns the written host path, or an empty string when the dialog is
// canceled or yields a blank path.
func (a *App) ExportConfig() (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}
	if a.repo == nil {
		return "", errors.New("config repository not initialized")
	}
	ctx, err := a.readyRuntimeContext()
	if err != nil {
		return "", err
	}

	path, err := runtime.SaveFileDialog(ctx, runtime.SaveDialogOptions{
		Title:           "Export configuration",
		DefaultFilename: "config-export.yaml",
		Filters: []runtime.FileFilter{
			{DisplayName: "YAML files", Pattern: "*.yaml;*.yml"},
		},
	})
	if isDialogCancelledErr(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("gui: save config dialog: %w", err)
	}

	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", nil
	}
	if ext := strings.ToLower(filepath.Ext(trimmedPath)); ext == "" {
		trimmedPath += ".yaml"
	}

	return a.exportConfigToPath(ctx, trimmedPath)
}

// exportConfigToPath writes the persisted config to trimmedPath, adding a YAML
// extension when needed. Fresh installs with no config rows export the current
// runtime config without persisting it.
func (a *App) exportConfigToPath(ctx context.Context, trimmedPath string) (string, error) {
	trimmedPath = strings.TrimSpace(trimmedPath)
	if trimmedPath == "" {
		return "", nil
	}
	if ext := strings.ToLower(filepath.Ext(trimmedPath)); ext == "" {
		trimmedPath += ".yaml"
	}

	cfg, authDBPath, err := a.exportableConfig(ctx)
	if err != nil {
		return "", err
	}
	allowPlaintext, err := a.allowUnencryptedExport(authDBPath)
	if err != nil {
		return "", err
	}

	if allowPlaintext {
		if err := config.ExportToPlaintextYAML(cfg, trimmedPath); err != nil {
			return "", fmt.Errorf("gui: %w", err)
		}
		return trimmedPath, nil
	}

	if err := config.ExportToYAML(cfg, trimmedPath); err != nil {
		return "", fmt.Errorf("gui: %w", err)
	}

	return trimmedPath, nil
}

// exportableConfig returns the normalized GUI export snapshot and the DB path
// embedded in that snapshot. Plaintext export authorization must use that same
// DB path so runtime config changes cannot authorize a different persisted
// config boundary.
func (a *App) exportableConfig(ctx context.Context) (*config.Config, string, error) {
	rt := a.runtimeSnapshot()
	cfg, err := config.LoadFromDatabase(ctx, a.repo)
	if err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			cfg = &rt.cfg
		} else {
			return nil, "", fmt.Errorf("gui: %w", err)
		}
	}
	normalized, err := normalizeGUIConfigForExport(cfg, rt.cfg.MainSettings.DBPath)
	if err != nil {
		return nil, "", err
	}
	return normalized, normalized.MainSettings.DBPath, nil
}

// allowUnencryptedExport reports whether the supplied DB auth material
// permits plaintext config export. Missing auth material denies plaintext
// export; malformed material is returned as an error.
func (a *App) allowUnencryptedExport(dbPath string) (bool, error) {
	if a == nil {
		return false, errors.New("app not initialized")
	}

	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return false, nil
	}

	material, err := authmaterial.LoadFromDBPath(dbPath)
	if err == nil {
		return material.AllowUnencryptedExport, nil
	}
	if errors.Is(err, authmaterial.ErrUnavailable) {
		return false, nil
	}
	return false, fmt.Errorf("gui: %w", err)
}

// ImportResult is returned to the GUI after an interactive config import.
type ImportResult struct {
	// Message is a user-displayable import summary.
	Message string `json:"message"`
	// Warnings contains non-fatal parser/import warnings.
	Warnings []string `json:"warnings"`
}

// WebAuthStatus reports whether the current database has usable web auth
// material for config secret encryption.
type WebAuthStatus struct {
	// Path is the host filesystem path to the web auth material file.
	Path string `json:"path"`
	// Exists reports whether web auth material was found on disk.
	Exists bool `json:"exists"`
	// Usable reports whether the auth material can decrypt/encrypt config secrets.
	Usable bool `json:"usable"`
	// CanCreate reports whether the GUI may create new auth material at Path.
	CanCreate bool `json:"canCreate"`
	// Username is populated only when usable auth material is loaded.
	Username string `json:"username"`
	// AllowUnencryptedExport mirrors the loaded auth policy.
	AllowUnencryptedExport bool `json:"allowUnencryptedExport"`
	// BrowseRoot is the host filesystem root enforced for browser file browsing.
	BrowseRoot string `json:"browseRoot"`
	// AllowUnrestrictedBrowse reports whether browser file browsing may ignore BrowseRoot.
	AllowUnrestrictedBrowse bool `json:"allowUnrestrictedBrowse"`
	// EncryptionEnabled reports whether config secret encryption is active.
	EncryptionEnabled bool `json:"encryptionEnabled"`
	// Message is a user-displayable status summary.
	Message string `json:"message"`
}

func (a *App) GetWebAuthStatus() (WebAuthStatus, error) {
	if a == nil {
		return WebAuthStatus{}, errors.New("app not initialized")
	}

	dbPath := strings.TrimSpace(a.currentConfig().MainSettings.DBPath)
	if dbPath == "" {
		return WebAuthStatus{
			CanCreate: false,
			Message:   "Database path is not configured.",
		}, nil
	}

	authPath := authmaterial.AuthFilePath(dbPath)
	status := WebAuthStatus{
		Path:      authPath,
		CanCreate: true,
		Message:   "No web auth file found. Secrets will continue to be stored in plaintext until one is created.",
	}

	if _, err := os.Stat(authPath); err == nil {
		status.Exists = true
		status.CanCreate = false
	} else if err != nil && !os.IsNotExist(err) {
		return WebAuthStatus{}, fmt.Errorf("web auth status: stat auth file: %w", err)
	}

	material, err := authmaterial.LoadFromDBPath(dbPath)
	if err == nil {
		status.Exists = true
		status.Usable = true
		status.CanCreate = false
		status.Username = material.Username
		status.AllowUnencryptedExport = material.AllowUnencryptedExport
		if record, loadErr := authmaterial.LoadRecordFromDBPath(dbPath); loadErr == nil {
			status.BrowseRoot = record.BrowseRoot
			status.AllowUnrestrictedBrowse = record.AllowUnrestrictedBrowse
		} else if logger := a.currentLogger(); logger != nil {
			logger.Debugf(
				"gui: web auth browse policy record unavailable db_path=%s error=%s",
				redaction.RedactValue(dbPath, nil),
				redaction.RedactValue(loadErr.Error(), nil),
			)
		}
		status.EncryptionEnabled = true
		status.Message = "Secret encryption is enabled for this installation."
		return status, nil
	}
	if errors.Is(err, authmaterial.ErrUnavailable) {
		if status.Exists {
			status.Message = "web-auth.json exists but is not usable for secret encryption."
		}
		return status, nil
	}

	return WebAuthStatus{}, fmt.Errorf("web auth status: %w", err)
}

func (a *App) CreateWebAuth(username string, password string) (WebAuthStatus, error) {
	if a == nil {
		return WebAuthStatus{}, errors.New("app not initialized")
	}

	dbPath := strings.TrimSpace(a.currentConfig().MainSettings.DBPath)
	if dbPath == "" {
		return WebAuthStatus{}, errors.New("database path is not configured")
	}

	if err := authmaterial.BootstrapAuthFile(dbPath, username, password); err != nil {
		return WebAuthStatus{}, fmt.Errorf("gui: %w", err)
	}

	return a.GetWebAuthStatus()
}

// ImportConfig opens a native file picker, imports the selected config, builds
// the replacement runtime, then syncs cookie encryption metadata and saves the
// non-env config. Runtime build or save failures leave the persisted config and
// active runtime unchanged; env overrides apply only to the installed runtime.
func (a *App) ImportConfig() (ImportResult, error) {
	if a == nil {
		return ImportResult{}, errors.New("app not initialized")
	}
	if a.repo == nil {
		return ImportResult{}, errors.New("config repository not initialized")
	}
	ctx, err := a.readyRuntimeContext()
	if err != nil {
		return ImportResult{}, err
	}

	path, err := runtime.OpenFileDialog(ctx, runtime.OpenDialogOptions{
		Title: "Import configuration",
		Filters: []runtime.FileFilter{
			{DisplayName: "Config files", Pattern: "*.py;*.yaml;*.yml;*.json"},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
	if isDialogCancelledErr(err) {
		return ImportResult{}, nil
	}
	if err != nil {
		return ImportResult{}, fmt.Errorf("gui: import config dialog: %w", err)
	}
	if strings.TrimSpace(path) == "" {
		return ImportResult{}, nil
	}

	return a.importConfigFromPath(ctx, path)
}

// importConfigFromPath imports one selected config file, validates both
// persisted and env-applied runtime forms, then atomically saves and applies it.
func (a *App) importConfigFromPath(ctx context.Context, path string) (ImportResult, error) {
	cfg, warnings, err := importer.ImportFromFile(path)
	if err != nil {
		return ImportResult{}, fmt.Errorf("gui: %w", err)
	}

	currentCfg := a.currentConfig()
	cfg.MainSettings.DBPath = currentCfg.MainSettings.DBPath

	if err := cfg.Validate(); err != nil {
		return ImportResult{}, fmt.Errorf("validate imported config: %w", err)
	}
	runtimeCfg := *cfg
	config.ApplyEnvOverrides(&runtimeCfg)
	runtimeCfg.MainSettings.DBPath = currentCfg.MainSettings.DBPath
	if err := runtimeCfg.Validate(); err != nil {
		return ImportResult{}, fmt.Errorf("validate imported config: %w", err)
	}

	if err := a.saveAndApplyConfig(ctx, cfg, runtimeCfg, cfg.MainSettings.DBPath); err != nil {
		return ImportResult{}, err
	}

	message := "imported config from " + filepath.Base(path)
	if len(warnings) > 0 {
		message += fmt.Sprintf(" (%d warnings)", len(warnings))
	}
	return ImportResult{Message: message, Warnings: warnings}, nil
}

// saveConfigToRepository enforces the shared pre-save cookie encryption sync
// before persisting GUI config changes through the already-open repository.
func (a *App) saveConfigToRepository(ctx context.Context, cfg *config.Config, dbPath string) error {
	if err := configstore.SaveToRepository(ctx, cfg, a.repo, dbPath); err != nil {
		return fmt.Errorf("gui: %w", err)
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

// saveAndApplyConfig builds the replacement runtime before any repository
// writes, then persists cfg and installs the runtime as one ordered transition.
// If persistence fails, the built runtime is closed and the active runtime is
// left untouched.
func (a *App) saveAndApplyConfig(ctx context.Context, storedCfg *config.Config, runtimeCfg config.Config, dbPath string) error {
	if err := validateCookieAuthMaterial(dbPath); err != nil {
		if !errors.Is(err, cookies.ErrAuthHelperUnavailable) {
			return fmt.Errorf("gui: validate cookie auth before config save: %w", err)
		}
	}

	rt, err := guishared.BuildRuntime(ctx, runtimeCfg, a.repo)
	if err != nil {
		return fmt.Errorf("gui: %w", err)
	}

	applied := false
	defer func() {
		if applied {
			return
		}
		if rt.Core != nil {
			_ = rt.Core.Close()
		}
		if rt.Logger != nil {
			_ = rt.Logger.Close()
		}
	}()

	if err := a.saveConfigToRepository(ctx, storedCfg, dbPath); err != nil {
		return err
	}

	oldCore, oldLogger := a.replaceRuntime(runtimeCfg, rt.Core, rt.Logger)
	applied = true
	a.rebindLogStreams(ctx, oldLogger, rt.Logger)

	if oldCore != nil {
		_ = oldCore.Close()
	}
	if oldLogger != nil {
		_ = oldLogger.Close()
	}

	return nil
}

// applyConfig builds and installs a runtime from cfg without writing cfg to the
// repository; it is used for startup and explicit runtime refresh paths.
func (a *App) applyConfig(ctx context.Context, cfg config.Config) error {
	rt, err := guishared.BuildRuntime(ctx, cfg, a.repo)
	if err != nil {
		return fmt.Errorf("gui: %w", err)
	}

	oldCore, oldLogger := a.replaceRuntime(cfg, rt.Core, rt.Logger)
	a.rebindLogStreams(ctx, oldLogger, rt.Logger)

	if oldCore != nil {
		_ = oldCore.Close()
	}
	if oldLogger != nil {
		_ = oldLogger.Close()
	}

	return nil
}

func (a *App) requireCore() error {
	_, err := a.requireRuntime()
	return err
}

func (a *App) requireHistoryRepo() error {
	if a == nil {
		return errors.New("app not initialized")
	}
	if a.repo == nil {
		return errors.New("history repository not initialized")
	}
	return nil
}

func (a *App) GetTrackerIcon(trackerNameOrDomain string, customURL string) (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}
	ctx := a.runtimeContext()
	cfg := a.currentConfig()

	domain, resolvedURL := config.ResolveTrackerDomain(&cfg, trackerNameOrDomain)
	urlToUse := customURL
	if urlToUse == "" {
		urlToUse = resolvedURL
	}

	res, err := trackericon.GetTrackerIcon(ctx, cfg.MainSettings.DBPath, domain, urlToUse)
	if err != nil {
		return "", fmt.Errorf("gui: %w", err)
	}
	return res, nil
}
