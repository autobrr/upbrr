// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package dvdmenus captures, persists, lists, and deletes managed DVD menu
// screenshots while preserving manual and normal screenshot categories.
package dvdmenus

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG metadata decoding for manual menu imports
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/dvdvideo/engine"
	"github.com/autobrr/upbrr/internal/dvdvideo/graph"
	"github.com/autobrr/upbrr/internal/dvdvideo/render"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	engineSchemaVersion       = 1
	defaultLanguage           = "en"
	requiredFFmpegMenuOptions = 5
)

var supportedFeatures = []string{
	"ifo_inventory",
	"vm_navigation",
	"nav_buttons",
	"spu_composition",
	"default_highlight",
}

type captureDirectoryFunc func(context.Context, string, render.Runner, string, engine.Options) (engine.Result, error)
type executableResolverFunc func() (string, error)

type executableIdentity struct {
	path    string
	size    int64
	modTime int64
}

type capabilityCache struct {
	identity   executableIdentity
	capability render.Capability
	valid      bool
}

// Service coordinates bounded DVD menu capture and category-aware persistence.
type Service struct {
	logger            api.Logger
	tmpRoot           string
	repo              repository
	runner            render.Runner
	resolveExecutable executableResolverFunc
	captureDirectory  captureDirectoryFunc
	removeFile        func(string) error
	capabilityMu      sync.Mutex
	capabilityCache   capabilityCache
}

type repository interface {
	api.ScreenshotLifecycleRepository
	LoadMediaAssetSnapshot(context.Context, string) (api.MediaAssetSnapshot, error)
}

// NewService returns a DVD menu service that shares the screenshot FFmpeg
// resolver and stores managed images beneath tmpRoot. A nil logger is replaced
// with [api.NopLogger].
func NewService(logger api.Logger, tmpRoot string, repo repository) *Service {
	return newService(logger, tmpRoot, repo, render.ExecRunner{}, screenshots.ResolveFFmpegExecutable, engine.CaptureDirectory)
}

func newService(
	logger api.Logger,
	tmpRoot string,
	repo repository,
	runner render.Runner,
	resolveExecutable executableResolverFunc,
	captureDirectory captureDirectoryFunc,
) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	if runner == nil {
		runner = render.ExecRunner{}
	}
	if resolveExecutable == nil {
		resolveExecutable = screenshots.ResolveFFmpegExecutable
	}
	if captureDirectory == nil {
		captureDirectory = engine.CaptureDirectory
	}
	return &Service{
		logger:            logger,
		tmpRoot:           tmpRoot,
		repo:              repo,
		runner:            runner,
		resolveExecutable: resolveExecutable,
		captureDirectory:  captureDirectory,
		removeFile:        os.Remove,
	}
}

// Capture renders and persists up to maxItems automatic menu images for one
// prepared DVD. It atomically replaces earlier automatic captures, preserves
// manual menus and normal screenshots, and reports partial coverage in result.
func (s *Service) Capture(ctx context.Context, meta api.DVDMenuSubject, maxItems int) (api.DVDMenuCaptureResult, error) {
	result := api.DVDMenuCaptureResult{SourcePath: meta.SourcePath, MaxItems: maxItems}
	if s != nil && s.logger != nil {
		s.logger.Debugf("DVD menus: capture requested disc_type=%s max_items=%d", strings.ToUpper(strings.TrimSpace(meta.DiscType)), maxItems)
	}
	api.ReportDVDMenuProgress(ctx, api.DVDMenuProgressUpdate{Phase: "preflight", Message: "Checking DVD menu capture support."})
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("DVD menus: capture canceled: %w", err)
	}
	if s == nil || s.repo == nil {
		return result, errors.New("DVD menus: screenshot lifecycle repository not configured")
	}
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return result, errors.New("DVD menus: source is not a DVD")
	}
	if strings.TrimSpace(meta.SourcePath) == "" || maxItems < 1 || maxItems > graph.MaxMenuItems {
		return result, internalerrors.ErrInvalidInput
	}

	videoTS, err := resolveVideoTSDirectory(meta.SourcePath)
	if err != nil {
		return result, err
	}
	s.logger.Debugf("DVD menus: source ready input=extracted_video_ts")
	executable, capability, info, err := s.resolveCapability(ctx)
	result.Engine = info
	if err != nil {
		s.logger.Warnf("DVD menus: capture failed stage=capability missing_options=%d", len(info.MissingFFmpegOptions))
		return result, err
	}

	s.logger.Infof(
		"DVD menus: capture started language=%s region=%d max_items=%d engine_version=%s ffmpeg_dvdvideo=%t",
		defaultLanguage,
		0,
		maxItems,
		info.EngineVersion,
		info.FFmpegDVDVideo,
	)
	lastProgress := engine.Progress{}
	hasProgress := false
	engineResult, err := s.captureDirectory(ctx, videoTS, s.runner, executable, engine.Options{
		Traversal:      graph.Options{Language: defaultLanguage, MaxItems: maxItems},
		ProcessTimeout: engine.DefaultProcessTimeout,
		Deinterlace:    true,
		Capability:     &capability,
		Logger:         s.logger,
		Progress: func(update engine.Progress) {
			if !hasProgress || update != lastProgress {
				s.logger.Debugf(
					"DVD menus: progress phase=%s inventoried=%d states=%d buttons=%d captured=%d warnings=%d",
					update.Phase,
					update.Inventoried,
					update.VisitedStates,
					update.VisitedButtons,
					update.Captured,
					update.Warnings,
				)
				lastProgress = update
				hasProgress = true
			}
			api.ReportDVDMenuProgress(ctx, api.DVDMenuProgressUpdate{
				Phase:           update.Phase,
				Message:         dvdMenuProgressMessage(update.Phase),
				DiscoveredMenus: update.Inventoried,
				VisitedStates:   update.VisitedStates,
				VisitedButtons:  update.VisitedButtons,
				CapturedCount:   update.Captured,
				WarningCount:    update.Warnings,
			})
		},
	})
	result = mapEngineResult(meta.SourcePath, maxItems, engineResult, info)
	if err != nil {
		s.logger.Warnf(
			"DVD menus: capture failed stage=engine captured=%d discovered=%d warnings=%d",
			len(engineResult.Captures),
			engineResult.Inventoried,
			len(engineResult.Warnings),
		)
		return result, fmt.Errorf("DVD menus: %w", err)
	}
	if len(engineResult.Captures) == 0 {
		s.logger.Warnf("DVD menus: capture failed stage=render reason=no_frames")
		return result, errors.New("DVD menus: no menu images captured")
	}
	s.logger.Debugf(
		"DVD menus: engine complete discovered=%d states=%d buttons=%d selected=%d captured=%d partial=%t truncated=%t warnings=%d",
		result.DiscoveredMenus,
		result.VisitedStates,
		result.VisitedButtons,
		engineResult.Selected,
		len(engineResult.Captures),
		result.Partial,
		result.Truncated,
		len(result.Warnings),
	)
	for _, warning := range result.Warnings {
		s.logger.Debugf("DVD menus: warning recorded code=%s detail=%q", warning.Code, dvdMenuWarningDetail(warning.Code))
	}
	s.logger.Debugf("DVD menus: persistence started images=%d", len(engineResult.Captures))
	api.ReportDVDMenuProgress(ctx, api.DVDMenuProgressUpdate{
		Phase:           "persisting",
		Message:         "Saving captured DVD menu images.",
		DiscoveredMenus: result.DiscoveredMenus,
		VisitedStates:   result.VisitedStates,
		VisitedButtons:  result.VisitedButtons,
		CapturedCount:   len(engineResult.Captures),
		WarningCount:    len(result.Warnings),
	})

	tmpDir, base, err := paths.ReleaseTempDirFor(s.tmpRoot, meta.SourcePath, api.ReleaseInfo{})
	if err != nil {
		return result, fmt.Errorf("DVD menus: %w", err)
	}
	images, records, selections, created, err := writeCaptureImages(ctx, tmpDir, base, meta.SourcePath, engineResult.Captures)
	if err != nil {
		removeCreatedFiles(created)
		s.logger.Warnf("DVD menus: persistence failed stage=write created=%d", len(created))
		return result, err
	}
	s.logger.Debugf("DVD menus: persistence files ready created=%d", len(created))
	replaced, err := s.repo.ReplaceDVDMenuScreenshots(ctx, meta.SourcePath, records, selections)
	if err != nil {
		removeCreatedFiles(created)
		s.logger.Warnf("DVD menus: persistence failed stage=database created=%d", len(created))
		return result, fmt.Errorf("DVD menus: persist capture: %w", err)
	}
	result.Images = images
	s.logger.Debugf("DVD menus: persistence complete stored=%d replaced=%d", len(images), len(replaced))

	cleanupFailed := 0
	for _, oldPath := range replaced {
		if !managedChildPath(tmpDir, oldPath) {
			cleanupFailed++
			continue
		}
		if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupFailed++
		}
	}
	if cleanupFailed > 0 {
		result.Partial = true
		result.Complete = false
		result.Warnings = appendWarning(result.Warnings, api.DVDMenuCaptureWarning{
			Code:    "cleanup_failed",
			Message: "Some replaced local DVD menu files could not be removed.",
		})
		s.logger.Warnf("DVD menus: replaced file cleanup incomplete count=%d", cleanupFailed)
	}
	if result.Partial || result.Truncated {
		s.logger.Warnf(
			"DVD menus: capture incomplete captured=%d discovered=%d states=%d buttons=%d partial=%t truncated=%t warnings=%d",
			len(result.Images),
			result.DiscoveredMenus,
			result.VisitedStates,
			result.VisitedButtons,
			result.Partial,
			result.Truncated,
			len(result.Warnings),
		)
	}
	s.logger.Infof(
		"DVD menus: capture complete captured=%d discovered=%d states=%d buttons=%d complete=%t partial=%t truncated=%t warnings=%d",
		len(result.Images),
		result.DiscoveredMenus,
		result.VisitedStates,
		result.VisitedButtons,
		result.Complete,
		result.Partial,
		result.Truncated,
		len(result.Warnings),
	)
	api.ReportDVDMenuProgress(ctx, api.DVDMenuProgressUpdate{
		Phase:           "complete",
		Message:         "DVD menu capture finished.",
		DiscoveredMenus: result.DiscoveredMenus,
		VisitedStates:   result.VisitedStates,
		VisitedButtons:  result.VisitedButtons,
		CapturedCount:   len(result.Images),
		WarningCount:    len(result.Warnings),
	})
	return result, nil
}

func dvdMenuProgressMessage(phase string) string {
	switch phase {
	case "discovering":
		return "Discovering reachable DVD menus."
	case "capturing":
		return "Rendering distinct DVD menu screens."
	case "preflight", "persisting", "complete":
		return ""
	default:
		return "Processing DVD menus."
	}
}

// List returns persisted manual and automatic menu images in final-selection
// order. Missing or directory-valued local image paths are omitted.
func (s *Service) List(ctx context.Context, meta api.DVDMenuSubject) ([]api.ScreenshotImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("DVD menus: list canceled: %w", err)
	}
	if s == nil || s.repo == nil {
		return nil, errors.New("DVD menus: repository not configured")
	}
	if strings.TrimSpace(meta.SourcePath) == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	snapshot, err := s.repo.LoadMediaAssetSnapshot(ctx, meta.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("DVD menus: load media assets: %w", err)
	}

	recordByPath := make(map[string]api.Screenshot, len(snapshot.Screenshots))
	for _, record := range snapshot.Screenshots {
		if record.Purpose == api.ScreenshotPurposeMenu {
			recordByPath[strings.TrimSpace(record.ImagePath)] = record
		}
	}
	uploadByPath := make(map[string]api.UploadedImageLink, len(snapshot.UploadedImages))
	for _, link := range snapshot.UploadedImages {
		uploadByPath[strings.TrimSpace(link.ImagePath)] = link
	}

	images := make([]api.ScreenshotImage, 0)
	for _, selection := range snapshot.FinalSelections {
		if !api.IsDiscMenuSelectionSource(selection.Source) {
			continue
		}
		pathValue := strings.TrimSpace(selection.ImagePath)
		if pathValue == "" {
			continue
		}
		imageValue, ok := screenshotImage(pathValue, len(images), recordByPath[pathValue], uploadByPath[pathValue])
		if ok {
			images = append(images, imageValue)
		}
	}
	return images, nil
}

// Delete removes one menu image beneath the prepared release's managed temp
// directory and atomically deletes its local DB references. Remote image-host
// assets are not deleted. If final removal of the staged file fails, Delete
// attempts to restore the original file and its local records before returning;
// compensation failures are joined with the removal error.
func (s *Service) Delete(ctx context.Context, meta api.DVDMenuSubject, imagePath string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("DVD menus: delete canceled: %w", err)
	}
	if s == nil || s.repo == nil {
		return errors.New("DVD menus: screenshot lifecycle repository not configured")
	}
	trimmedPath := strings.TrimSpace(imagePath)
	if strings.TrimSpace(meta.SourcePath) == "" || trimmedPath == "" {
		return internalerrors.ErrInvalidInput
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(s.tmpRoot, meta.SourcePath, api.ReleaseInfo{})
	if err != nil {
		return fmt.Errorf("DVD menus: %w", err)
	}
	if !managedChildPath(tmpDir, trimmedPath) {
		return errors.New("DVD menus: image is outside the managed release directory")
	}

	pendingPath := trimmedPath + fmt.Sprintf(".delete-%d", time.Now().UTC().UnixNano())
	renamed := false
	info, statErr := os.Stat(trimmedPath)
	switch {
	case statErr == nil && info.IsDir():
		return internalerrors.ErrInvalidInput
	case statErr == nil:
		if err := os.Rename(trimmedPath, pendingPath); err != nil {
			return fmt.Errorf("DVD menus: stage local delete: %w", err)
		}
		renamed = true
	case !errors.Is(statErr, os.ErrNotExist):
		return fmt.Errorf("DVD menus: inspect local image: %w", statErr)
	}

	deleted, err := s.repo.DeleteDiscMenuScreenshot(ctx, meta.SourcePath, trimmedPath)
	if err != nil {
		if renamed {
			if restoreErr := os.Rename(pendingPath, trimmedPath); restoreErr != nil {
				return fmt.Errorf("DVD menus: delete records and restore local image: %w", errors.Join(err, restoreErr))
			}
		}
		return fmt.Errorf("DVD menus: delete records: %w", err)
	}
	if renamed {
		removeFile := s.removeFile
		if removeFile == nil {
			removeFile = os.Remove
		}
		if removeErr := removeFile(pendingPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			if restoreErr := os.Rename(pendingPath, trimmedPath); restoreErr != nil {
				return fmt.Errorf("DVD menus: remove staged local image and restore file: %w", errors.Join(removeErr, restoreErr))
			}
			if restoreErr := s.repo.RestoreDiscMenuScreenshot(context.WithoutCancel(ctx), meta.SourcePath, deleted); restoreErr != nil {
				return fmt.Errorf("DVD menus: remove staged local image and restore records: %w", errors.Join(removeErr, restoreErr))
			}
			return fmt.Errorf("DVD menus: remove staged local image; deletion rolled back: %w", removeErr)
		}
	}
	if deleted.UploadedLinks > 0 {
		s.logger.Warnf("DVD menus: local image removed but remote assets may remain count=%d", deleted.UploadedLinks)
	}
	s.logger.Infof("DVD menus: image removed source=%s", deleted.Selection.Source)
	return nil
}

// Capability reports path-free engine metadata and FFmpeg dvdvideo support.
// Successful probes are cached until the resolved executable identity changes.
func (s *Service) Capability(ctx context.Context) (api.DVDMenuEngineInfo, error) {
	_, _, info, err := s.resolveCapability(ctx)
	return info, err
}

// resolveCapability resolves and probes FFmpeg, reusing a cached successful
// probe only while executable path, size, and modification time remain equal.
func (s *Service) resolveCapability(ctx context.Context) (string, render.Capability, api.DVDMenuEngineInfo, error) {
	info := baseEngineInfo()
	if s != nil && s.logger != nil {
		s.logger.Debugf("DVD menus: FFmpeg capability check started required_options=%d", requiredFFmpegMenuOptions)
	}
	if err := ctx.Err(); err != nil {
		return "", render.Capability{}, info, fmt.Errorf("DVD menus: capability canceled: %w", err)
	}
	if s == nil || s.resolveExecutable == nil || s.runner == nil {
		return "", render.Capability{}, info, errors.New("DVD menus: FFmpeg capability unavailable")
	}
	executable, err := s.resolveExecutable()
	if err != nil {
		s.logger.Debugf("DVD menus: FFmpeg capability unavailable stage=resolve")
		return "", render.Capability{}, info, fmt.Errorf("DVD menus: resolve FFmpeg: %w", err)
	}
	identity, err := inspectExecutable(executable)
	if err != nil {
		s.logger.Debugf("DVD menus: FFmpeg capability unavailable stage=inspect")
		return "", render.Capability{}, info, err
	}

	s.capabilityMu.Lock()
	if s.capabilityCache.valid && s.capabilityCache.identity == identity {
		capability := s.capabilityCache.capability
		s.capabilityMu.Unlock()
		s.logger.Debugf("DVD menus: FFmpeg capability cache hit dvdvideo=%t options=%d", capability.Available, len(capability.Options))
		return executable, capability, engineInfo(capability), nil
	}
	s.capabilityMu.Unlock()

	s.logger.Debugf("DVD menus: FFmpeg capability probe started")
	capability, err := render.Probe(ctx, s.runner, executable)
	if err != nil {
		info.MissingFFmpegOptions = missingFFmpegOptions(err)
		s.logger.Debugf("DVD menus: FFmpeg capability incompatible missing_options=%d", len(info.MissingFFmpegOptions))
		return "", render.Capability{}, info, fmt.Errorf("DVD menus: FFmpeg capability: %w", err)
	}
	s.capabilityMu.Lock()
	s.capabilityCache = capabilityCache{
		identity:   identity,
		capability: capability,
		valid:      true,
	}
	s.capabilityMu.Unlock()
	s.logger.Debugf("DVD menus: FFmpeg capability probe complete dvdvideo=%t options=%d", capability.Available, len(capability.Options))
	return executable, capability, engineInfo(capability), nil
}

func dvdMenuWarningDetail(code string) string {
	switch code {
	case "depth_limit":
		return "menu branch depth limit reached"
	case "state_limit":
		return "menu state limit reached"
	case "pre_command":
		return "menu pre-command failed"
	case "unsupported_pre_link":
		return "menu pre-command target was not resolved"
	case "live_state":
		return "menu NAV/SPU state could not be resolved"
	case "button_limit":
		return "menu button limit reached"
	case "button_command":
		return "menu button command failed"
	case "unsupported_button_link":
		return "menu button target was not resolved"
	case "post_command":
		return "menu post-command failed"
	case "unsupported_post_link":
		return "menu post-command target was not resolved"
	case "structural_only":
		return "visible menu was not reached through navigation"
	case "structural_state":
		return "structural menu candidate could not be classified"
	case "nav_scan_limit":
		return "menu NAV/SPU sector scan limit reached"
	case "frame_decode":
		return "inventory-selected menu frame could not be decoded"
	case "spu_unavailable":
		return "menu subpicture state was not available"
	case "highlight_unavailable":
		return "default menu highlight state was not available"
	case "black_frame":
		return "decoded menu frame was black"
	case "structural_discovery":
		return "some menu screens were found through structural inventory rather than reachable navigation"
	case "cleanup_failed":
		return "some replaced local DVD menu files could not be removed"
	}
	return "DVD menu coverage warning"
}

func inspectExecutable(executable string) (executableIdentity, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(executable))
	if err != nil {
		return executableIdentity{}, fmt.Errorf("DVD menus: resolve FFmpeg identity: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return executableIdentity{}, fmt.Errorf("DVD menus: inspect FFmpeg identity: %w", err)
	}
	if info.IsDir() {
		return executableIdentity{}, errors.New("DVD menus: FFmpeg executable is a directory")
	}
	return executableIdentity{
		path:    absPath,
		size:    info.Size(),
		modTime: info.ModTime().UnixNano(),
	}, nil
}

func baseEngineInfo() api.DVDMenuEngineInfo {
	return api.DVDMenuEngineInfo{
		EngineVersion:     engine.Version,
		SchemaVersion:     engineSchemaVersion,
		SupportedFeatures: append([]string(nil), supportedFeatures...),
	}
}

func engineInfo(capability render.Capability) api.DVDMenuEngineInfo {
	info := baseEngineInfo()
	info.FFmpegVersion = capability.Version
	info.FFmpegDVDVideo = capability.Available
	return info
}

func missingFFmpegOptions(err error) []string {
	message := err.Error()
	required := []string{"-menu", "-menu_lu", "-menu_vts", "-pgc", "-pg"}
	missing := make([]string, 0)
	_, reported, ok := strings.Cut(message, "missing ")
	if !ok {
		return missing
	}
	reportedOptions := make(map[string]struct{})
	for option := range strings.SplitSeq(reported, ",") {
		reportedOptions[strings.TrimSpace(option)] = struct{}{}
	}
	for _, option := range required {
		if _, ok := reportedOptions[option]; ok {
			missing = append(missing, option)
		}
	}
	return missing
}

func mapEngineResult(sourcePath string, maxItems int, source engine.Result, info api.DVDMenuEngineInfo) api.DVDMenuCaptureResult {
	result := api.DVDMenuCaptureResult{
		SourcePath:       sourcePath,
		SelectedLanguage: defaultLanguage,
		DiscoveredMenus:  source.Inventoried,
		VisitedStates:    source.VisitedStates,
		VisitedButtons:   source.VisitedButtons,
		MaxItems:         maxItems,
		Complete:         source.Complete,
		Partial:          source.Partial,
		Truncated:        source.Truncated,
		Engine:           info,
	}
	structural := false
	for _, capture := range source.Captures {
		if capture.Discovery == graph.DiscoveryStructural {
			structural = true
		}
	}
	for _, warning := range source.Warnings {
		result.Warnings = appendWarning(result.Warnings, api.DVDMenuCaptureWarning{Code: warning.Code, Message: warning.Message})
	}
	if structural {
		result.Warnings = appendWarning(result.Warnings, api.DVDMenuCaptureWarning{
			Code:    "structural_discovery",
			Message: "Some menu screens were found through structural inventory rather than reachable navigation.",
		})
	}
	return result
}

func appendWarning(warnings []api.DVDMenuCaptureWarning, warning api.DVDMenuCaptureWarning) []api.DVDMenuCaptureWarning {
	for _, existing := range warnings {
		if existing.Code == warning.Code {
			return warnings
		}
	}
	return append(warnings, warning)
}

// writeCaptureImages atomically publishes PNG captures and builds matching DB
// records/selections. The returned created paths remain caller-owned on error.
func writeCaptureImages(
	ctx context.Context,
	tmpDir string,
	base string,
	sourcePath string,
	captures []engine.Capture,
) ([]api.DVDMenuCaptureImage, []api.Screenshot, []api.ScreenshotFinalSelection, []string, error) {
	stamp := time.Now().UTC()
	images := make([]api.DVDMenuCaptureImage, 0, len(captures))
	records := make([]api.Screenshot, 0, len(captures))
	selections := make([]api.ScreenshotFinalSelection, 0, len(captures))
	created := make([]string, 0, len(captures))
	for index, capture := range captures {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, created, fmt.Errorf("DVD menus: write capture canceled: %w", err)
		}
		if capture.Image == nil || capture.Image.Bounds().Empty() {
			return nil, nil, nil, created, errors.New("DVD menus: capture image is empty")
		}
		finalPath := filepath.Join(tmpDir, fmt.Sprintf("%s-dvd-menu-%02d-%d.png", base, index+1, stamp.UnixNano()))
		partialPath := finalPath + ".partial"
		file, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, nil, nil, created, fmt.Errorf("DVD menus: create capture file: %w", err)
		}
		encodeErr := png.Encode(file, capture.Image)
		closeErr := file.Close()
		if encodeErr != nil {
			_ = os.Remove(partialPath)
			return nil, nil, nil, created, fmt.Errorf("DVD menus: encode capture: %w", encodeErr)
		}
		if closeErr != nil {
			_ = os.Remove(partialPath)
			return nil, nil, nil, created, fmt.Errorf("DVD menus: close capture: %w", closeErr)
		}
		if err := os.Rename(partialPath, finalPath); err != nil {
			_ = os.Remove(partialPath)
			return nil, nil, nil, created, fmt.Errorf("DVD menus: finalize capture: %w", err)
		}
		created = append(created, finalPath)
		stat, err := os.Stat(finalPath)
		if err != nil {
			return nil, nil, nil, created, fmt.Errorf("DVD menus: inspect capture: %w", err)
		}
		bounds := capture.Image.Bounds()
		discovery := api.DVDMenuDiscovery(capture.Discovery)
		images = append(images, api.DVDMenuCaptureImage{
			ScreenshotImage: api.ScreenshotImage{
				Index:     index,
				Path:      finalPath,
				Purpose:   api.ScreenshotPurposeMenu,
				Width:     bounds.Dx(),
				Height:    bounds.Dy(),
				SizeBytes: stat.Size(),
			},
			Discovery: discovery,
		})
		records = append(records, api.Screenshot{
			SourcePath:  sourcePath,
			ImagePath:   finalPath,
			FrameNumber: index,
			Width:       bounds.Dx(),
			Height:      bounds.Dy(),
			Purpose:     api.ScreenshotPurposeMenu,
			CapturedAt:  stamp,
		})
		selections = append(selections, api.ScreenshotFinalSelection{
			SourcePath: sourcePath,
			ImagePath:  finalPath,
			Order:      index,
			Source:     api.ScreenshotSelectionSourceDVDMenu,
			SelectedAt: stamp,
		})
	}
	return images, records, selections, created, nil
}

func removeCreatedFiles(paths []string) {
	for _, pathValue := range paths {
		_ = os.Remove(pathValue)
	}
}

func screenshotImage(pathValue string, index int, record api.Screenshot, upload api.UploadedImageLink) (api.ScreenshotImage, bool) {
	stat, err := os.Stat(pathValue)
	if err != nil || stat.IsDir() {
		return api.ScreenshotImage{}, false
	}
	result := api.ScreenshotImage{
		Index:            index,
		TimestampSeconds: record.Timestamp,
		Path:             pathValue,
		Purpose:          api.ScreenshotPurposeMenu,
		Width:            record.Width,
		Height:           record.Height,
		SizeBytes:        stat.Size(),
		Host:             upload.Host,
		ImgURL:           upload.ImgURL,
		RawURL:           upload.RawURL,
		WebURL:           upload.WebURL,
		UploadedAt:       upload.UploadedAt,
	}
	if result.Width <= 0 || result.Height <= 0 {
		file, openErr := os.Open(pathValue)
		if openErr == nil {
			config, _, decodeErr := image.DecodeConfig(file)
			_ = file.Close()
			if decodeErr == nil {
				result.Width = config.Width
				result.Height = config.Height
			}
		}
	}
	return result, true
}

// resolveVideoTSDirectory accepts an extracted DVD root or VIDEO_TS directory,
// resolves the source symlink, and rejects indirect VIDEO_TS symlinks.
func resolveVideoTSDirectory(sourcePath string) (string, error) {
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return "", internalerrors.ErrInvalidInput
	}
	resolved, err := filepath.EvalSymlinks(trimmed)
	if err != nil {
		return "", fmt.Errorf("DVD menus: resolve DVD source: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("DVD menus: inspect DVD source: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("DVD menus: DVD source is not a directory")
	}
	if strings.EqualFold(filepath.Base(resolved), "VIDEO_TS") {
		return resolved, nil
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return "", fmt.Errorf("DVD menus: read DVD source: %w", err)
	}
	for _, entry := range entries {
		if !strings.EqualFold(entry.Name(), "VIDEO_TS") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return "", errors.New("DVD menus: VIDEO_TS is not a direct directory")
		}
		return filepath.Join(resolved, entry.Name()), nil
	}
	return "", errors.New("DVD menus: VIDEO_TS directory not found")
}

// managedChildPath accepts descendants of root but never root itself.
func managedChildPath(root string, target string) bool {
	if pathutil.SamePath(root, target) {
		return false
	}
	return pathutil.IsWithinRoot(root, target)
}
