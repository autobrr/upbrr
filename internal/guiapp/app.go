// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"encoding/base64"
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
	"github.com/autobrr/upbrr/internal/core"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/internal/imagehostpolicy"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const previewTimeout = 30 * time.Minute
const bdinfoProgressEvent = "bdinfo:progress"
const metadataProgressEvent = "metadata:progress"

type App struct {
	runtimeCtx  *appRuntimeContext
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
}

func NewApp(configPath string, configProvided bool) (*App, error) {
	return NewAppWithContext(context.Background(), configPath, configProvided)
}

func NewAppWithContext(ctx context.Context, configPath string, configProvided bool) (*App, error) {
	if ctx == nil {
		return nil, errors.New("guiapp: context is required")
	}
	cfg, dbPath, err := configstore.Bootstrap(ctx, configPath, configProvided, true)
	if err != nil {
		return nil, err
	}

	logger, err := logging.New(cfg.Logging, cfg.MainSettings.DBPath)
	if err != nil {
		return nil, err
	}

	repo, err := db.OpenWithLoggerContext(ctx, dbPath, logger)
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	if err := repo.MigrateContext(ctx); err != nil {
		_ = repo.Close()
		_ = logger.Close()
		return nil, err
	}
	if err := repo.ClearUIState(ctx); err != nil {
		_ = repo.Close()
		_ = logger.Close()
		return nil, err
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
			return nil, err
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
	if a.core != nil {
		_ = a.core.Close()
	}
	if a.repo != nil {
		_ = a.repo.Close()
	}
	if a.logger != nil {
		_ = a.logger.Close()
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
	fallback := guishared.BrowseDirectoryFallback(a.cfg.MainSettings.DBPath)
	return guishared.BrowseDirectory(api.BrowseDirectoryRequest{Path: path, Mode: mode}, fallback)
}

func (a *App) ListUIStates() (api.UIStateList, error) {
	if a == nil || a.repo == nil {
		return api.UIStateList{}, errors.New("config repository not initialized")
	}
	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	states, err := a.repo.ListUIStates(ctx)
	if err != nil {
		return api.UIStateList{}, err
	}
	return api.UIStateList{States: states}, nil
}

func (a *App) GetUIState(id string) (api.UIStateRecord, error) {
	if a == nil || a.repo == nil {
		return api.UIStateRecord{}, errors.New("config repository not initialized")
	}
	if strings.TrimSpace(id) == "" {
		return api.UIStateRecord{}, errors.New("id is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return a.repo.LoadUIState(ctx, id)
}

func (a *App) SaveUIState(id string, label string, state api.UIState) error {
	if a == nil || a.repo == nil {
		return errors.New("config repository not initialized")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("id is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return a.repo.SaveUIState(ctx, id, label, state)
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

	return filesystem.DetectDiscType(ctx, path)
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
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return a.core.FetchMetadataPreview(progressCtx, req)
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
	if a.logger != nil {
		a.logger.Infof("gui: reset metadata started path=%s", trimmedPath)
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

	tmpRoot, err := db.Subdir(a.cfg.MainSettings.DBPath, "tmp")
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
	if a.logger != nil {
		a.logger.Debugf("gui: reset metadata collected artifacts path=%s files=%d", trimmedPath, len(artifactPaths))
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
	if a.logger != nil {
		a.logger.Infof("gui: reset metadata sqlite purge completed path=%s", trimmedPath)
	}

	removedFiles := 0
	for _, filePath := range artifactPaths {
		removed, err := removeIfWithinRoot(tmpRoot, filePath, false)
		if err != nil {
			if a.logger != nil {
				a.logger.Warnf("gui: reset metadata remove file failed %q: %v", filePath, err)
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
			if a.logger != nil {
				a.logger.Warnf("gui: reset metadata remove tmp dir failed %q: %v", dir, err)
			}
			continue
		}
		if removed {
			removedDirs++
		}
	}
	if a.logger != nil {
		a.logger.Infof("gui: reset metadata artifacts cleaned path=%s files_removed=%d dirs_removed=%d", trimmedPath, removedFiles, removedDirs)
	}

	req := api.Request{
		Paths:           []string{trimmedPath},
		Mode:            api.ModeGUI,
		Trackers:        slices.Clone(trackers),
		SourceLookupURL: strings.TrimSpace(sourceLookupURL),
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}
	preview, err := a.core.FetchMetadataPreview(progressCtx, req)
	if err != nil {
		return api.MetadataPreview{}, err
	}
	if a.logger != nil {
		a.logger.Infof("gui: reset metadata completed path=%s release=%s", trimmedPath, strings.TrimSpace(preview.ReleaseName))
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
	if !pathWithinRoot(absTmpRoot, absCandidate) {
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
	if absTarget == absRoot {
		return false, nil
	}
	if !pathWithinRoot(absRoot, absTarget) {
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

func (a *App) CheckDupes(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string) (api.DupeCheckSummary, error) {
	if a == nil || a.core == nil {
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
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return a.core.CheckDupes(ctx, req)
}

func (a *App) FetchPreparation(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string, ignoreDupesFor []string) (api.PreparationPreview, error) {
	if a == nil || a.core == nil {
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
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
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

	return a.core.FetchPreparationPreview(progressCtx, req)
}

func (a *App) FetchTrackerDryRun(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string, ignoreRuleFailures bool, ignoreDupesFor []string, questionnaireAnswers map[string]map[string]string, descriptionGroups []api.DescriptionBuilderGroup, debug bool, runLogLevel string) (api.TrackerDryRunPreview, error) {
	if err := a.requireCore(); err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	if strings.TrimSpace(path) == "" {
		return api.TrackerDryRunPreview{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	trimmedPath := strings.TrimSpace(path)
	runOpts, err := a.buildRunOptions(debug, runLogLevel)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	runCore, runLogger, err := a.buildRunCore(runOpts)
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
		IgnoreTrackerRuleFailures:   ignoreRuleFailures,
		Options:                     buildRunUploadOptions(a.cfg, runOpts),
		ExternalIDOverrides:         overrides,
		ReleaseNameOverrides:        nameOverrides,
		TrackerQuestionnaireAnswers: cloneQuestionnaireAnswers(questionnaireAnswers),
	}
	req.Options.DryRun = true
	if err := guishared.SeedRunCorePreparedMeta(ctx, a.core, runCore, req); err != nil {
		return api.TrackerDryRunPreview{}, err
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

	return runCore.FetchTrackerDryRunPreview(progressCtx, req)
}

func (a *App) FetchDescriptionBuilder(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string, ignoreDupesFor []string) (api.DescriptionBuilderPreview, error) {
	if a == nil || a.core == nil {
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
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return a.core.FetchDescriptionBuilderPreview(ctx, req)
}

func (a *App) RenderDescription(raw string) (string, error) {
	if a == nil || a.core == nil {
		return "", errors.New("app not initialized")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	return a.core.RenderDescription(ctx, raw)
}

func (a *App) SaveDescriptionOverride(path string, groupKey string, raw string, trackers []string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) (api.DescriptionBuilderGroup, error) {
	if a == nil || a.core == nil {
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

	return a.core.SaveDescriptionOverride(ctx, req, raw)
}

func (a *App) DiscoverPlaylists(path string) ([]api.PlaylistInfo, error) {
	if a == nil || a.core == nil {
		return nil, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	return a.core.DiscoverPlaylists(ctx, path)
}

func (a *App) SavePlaylistSelection(path string, playlists []string, useAll bool) error {
	if a == nil || a.core == nil {
		return errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	return a.core.SavePlaylistSelection(ctx, path, playlists, useAll)
}

func (a *App) LoadPlaylistSelection(path string) (api.PlaylistSelection, error) {
	if a == nil || a.core == nil {
		return api.PlaylistSelection{}, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return api.PlaylistSelection{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	return a.core.LoadPlaylistSelection(ctx, path)
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
	if a == nil || a.core == nil {
		return errors.New("app not initialized")
	}
	trimmedPath := strings.TrimSpace(sourcePath)
	if trimmedPath == "" {
		return errors.New("source path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	if err := a.core.DeleteHistoryRelease(ctx, trimmedPath); err != nil {
		return fmt.Errorf("delete history release: %w", err)
	}
	return nil
}

func (a *App) FetchScreenshotPlan(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) (api.ScreenshotPlan, error) {
	if a == nil || a.core == nil {
		return api.ScreenshotPlan{}, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return api.ScreenshotPlan{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths: []string{path},
		Mode:  api.ModeGUI,
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return a.core.FetchScreenshotPlan(ctx, req)
}

func (a *App) GenerateScreenshots(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, selections []api.ScreenshotSelection, purpose api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	if a == nil || a.core == nil {
		return api.ScreenshotResult{}, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return api.ScreenshotResult{}, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths: []string{path},
		Mode:  api.ModeGUI,
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return a.core.GenerateScreenshots(ctx, req, selections, purpose)
}

func (a *App) ListUploadCandidates(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) ([]api.ScreenshotImage, error) {
	if a == nil || a.core == nil {
		return nil, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths: []string{path},
		Mode:  api.ModeGUI,
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return a.core.ListUploadCandidates(ctx, req)
}

func (a *App) ListUploadedImages(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides) ([]api.UploadedImageLink, error) {
	if a == nil || a.core == nil {
		return nil, errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths: []string{path},
		Mode:  api.ModeGUI,
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return a.core.ListUploadedImages(ctx, req)
}

func (a *App) UploadImages(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, trackers []string, host string, images []api.ScreenshotImage) (api.UploadImagesResult, error) {
	if a == nil || a.core == nil {
		return api.UploadImagesResult{}, errors.New("app not initialized")
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
		Paths: []string{path},
		Mode:  api.ModeGUI,
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
		Trackers:             slices.Clone(trackers),
	}

	return a.core.UploadImages(ctx, req, host, images)
}

func (a *App) DeleteUploadedImage(path string, imagePath string, host string) error {
	if a == nil || a.core == nil {
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

	return a.core.DeleteUploadedImage(ctx, req, imagePath, host)
}

func (a *App) PreviewScreenshotFrame(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, timestampSeconds float64) (string, error) {
	if a == nil || a.core == nil {
		return "", errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths: []string{path},
		Mode:  api.ModeGUI,
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	preview, err := a.core.PreviewScreenshotFrame(ctx, req, timestampSeconds)
	if err != nil {
		return "", err
	}
	if len(preview.ImageBytes) == 0 {
		return "", errors.New("preview image is empty")
	}
	encoded := base64.StdEncoding.EncodeToString(preview.ImageBytes)
	return "data:image/png;base64," + encoded, nil
}

func (a *App) DeleteScreenshot(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, imagePath string) error {
	if a == nil || a.core == nil {
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
		Paths: []string{path},
		Mode:  api.ModeGUI,
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return a.core.DeleteScreenshot(ctx, req, imagePath)
}

func (a *App) DeleteTrackerImageURL(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, url string) error {
	if a == nil || a.core == nil {
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
		Paths: []string{path},
		Mode:  api.ModeGUI,
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return a.core.DeleteTrackerImageURL(ctx, req, url)
}

func (a *App) SaveFinalScreenshotSelections(path string, overrides api.ExternalIDOverrides, nameOverrides api.ReleaseNameOverrides, images []api.ScreenshotImage) error {
	if a == nil || a.core == nil {
		return errors.New("app not initialized")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}

	ctx := a.runtimeContext()
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req := api.Request{
		Paths: []string{path},
		Mode:  api.ModeGUI,
		Options: api.UploadOptions{
			Screens:    a.cfg.ScreenshotHandling.Screens,
			OnlyID:     a.cfg.Metadata.OnlyID,
			KeepImages: a.cfg.Metadata.KeepImages,
		},
		ExternalIDOverrides:  overrides,
		ReleaseNameOverrides: nameOverrides,
	}

	return a.core.SaveFinalScreenshotSelections(ctx, req, images)
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

func (a *App) GetConfig() (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}
	if a.repo == nil {
		return "", errors.New("config repository not initialized")
	}

	ctx := a.runtimeContext()

	cfg, err := config.LoadFromDatabase(ctx, a.repo)
	if err != nil {
		return "", err
	}
	if err := config.MergeMissingTrackerDefaults(cfg); err != nil {
		return "", err
	}
	if strings.TrimSpace(cfg.MainSettings.DBPath) == "" {
		cfg.MainSettings.DBPath = a.cfg.MainSettings.DBPath
	}
	if cfg.Trackers.Trackers == nil {
		cfg.Trackers.Trackers = map[string]config.TrackerConfig{}
	}
	if cfg.Trackers.DefaultTrackers == nil {
		cfg.Trackers.DefaultTrackers = config.CSVList{}
	}

	return config.ExportToJSON(cfg)
}

func (a *App) GetDefaultConfig() (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		return "", err
	}

	return config.ExportToJSON(cfg)
}

func (a *App) ListKnownTrackers() ([]string, error) {
	if a == nil {
		return nil, errors.New("app not initialized")
	}

	return trackers.KnownTrackers(), nil
}

func (a *App) GetImageHostPolicyMetadata() (imagehostpolicy.Metadata, error) {
	if a == nil {
		return imagehostpolicy.Metadata{}, errors.New("app not initialized")
	}

	return imagehostpolicy.PolicyMetadata(), nil
}

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
		return err
	}
	if err := config.MergeMissingTrackerDefaults(cfg); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.MainSettings.DBPath) == "" {
		cfg.MainSettings.DBPath = a.cfg.MainSettings.DBPath
	}
	if cfg.MainSettings.DBPath != a.cfg.MainSettings.DBPath {
		return errors.New("changing main_settings.db_path requires restart and is not supported in the GUI")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	ctx := a.runtimeContext()
	if err := config.SaveToDatabase(ctx, cfg, a.repo); err != nil {
		return err
	}

	return a.applyConfig(*cfg)
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

	allowPlaintext, err := a.allowUnencryptedExport()
	if err != nil {
		return "", err
	}

	if allowPlaintext {
		if err := config.ExportFromDatabaseToPlaintextYAML(ctx, trimmedPath, a.repo); err != nil {
			return "", err
		}
		return trimmedPath, nil
	}

	if err := config.ExportFromDatabaseToYAML(ctx, trimmedPath, a.repo); err != nil {
		return "", err
	}

	return trimmedPath, nil
}

func (a *App) allowUnencryptedExport() (bool, error) {
	if a == nil {
		return false, errors.New("app not initialized")
	}

	dbPath := strings.TrimSpace(a.cfg.MainSettings.DBPath)
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
	return false, err
}

type ImportResult struct {
	Message  string   `json:"message"`
	Warnings []string `json:"warnings"`
}

type WebAuthStatus struct {
	Path                    string `json:"path"`
	Exists                  bool   `json:"exists"`
	Usable                  bool   `json:"usable"`
	CanCreate               bool   `json:"canCreate"`
	Username                string `json:"username"`
	AllowUnencryptedExport  bool   `json:"allowUnencryptedExport"`
	BrowseRoot              string `json:"browseRoot"`
	AllowUnrestrictedBrowse bool   `json:"allowUnrestrictedBrowse"`
	EncryptionEnabled       bool   `json:"encryptionEnabled"`
	Message                 string `json:"message"`
}

func (a *App) GetWebAuthStatus() (WebAuthStatus, error) {
	if a == nil {
		return WebAuthStatus{}, errors.New("app not initialized")
	}

	dbPath := strings.TrimSpace(a.cfg.MainSettings.DBPath)
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
		} else if a.logger != nil {
			a.logger.Debugf(
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

	dbPath := strings.TrimSpace(a.cfg.MainSettings.DBPath)
	if dbPath == "" {
		return WebAuthStatus{}, errors.New("database path is not configured")
	}

	if err := authmaterial.BootstrapAuthFile(dbPath, username, password); err != nil {
		return WebAuthStatus{}, err
	}

	return a.GetWebAuthStatus()
}

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

	cfg, warnings, err := importer.ImportFromFile(path)
	if err != nil {
		return ImportResult{}, err
	}

	cfg.MainSettings.DBPath = a.cfg.MainSettings.DBPath

	if err := cfg.Validate(); err != nil {
		return ImportResult{}, fmt.Errorf("validate imported config: %w", err)
	}

	if err := config.SaveToDatabase(ctx, cfg, a.repo); err != nil {
		return ImportResult{}, err
	}

	config.ApplyEnvOverrides(cfg)
	if err := a.applyConfig(*cfg); err != nil {
		return ImportResult{}, err
	}

	message := "imported config from " + filepath.Base(path)
	if len(warnings) > 0 {
		message += fmt.Sprintf(" (%d warnings)", len(warnings))
	}
	return ImportResult{Message: message, Warnings: warnings}, nil
}

func (a *App) applyConfig(cfg config.Config) error {
	ctx := a.runtimeContext()
	rt, err := guishared.BuildRuntime(ctx, cfg, a.repo)
	if err != nil {
		return err
	}

	oldCore := a.core
	oldLogger := a.logger

	a.core = rt.Core
	a.coreInitErr = nil
	a.logger = rt.Logger
	a.cfg = cfg
	a.rebindLogStreams(oldLogger, rt.Logger)

	if oldCore != nil {
		_ = oldCore.Close()
	}
	if oldLogger != nil {
		_ = oldLogger.Close()
	}

	return nil
}

func (a *App) requireCore() error {
	if a == nil {
		return errors.New("app not initialized")
	}
	if a.core != nil {
		return nil
	}
	if a.coreInitErr != nil {
		return fmt.Errorf("core unavailable: %w", a.coreInitErr)
	}
	return errors.New("core not initialized")
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
