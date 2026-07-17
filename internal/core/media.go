// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/logging"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type mediaRepository interface {
	api.ScreenshotLifecycleRepository
	ListTrackerMetadataByPath(ctx context.Context, path string) ([]api.TrackerMetadata, error)
	SaveTrackerMetadata(ctx context.Context, metadata api.TrackerMetadata) error
	ListUploadedImagesByPath(ctx context.Context, path string) ([]api.UploadedImageLink, error)
	DeleteUploadedImage(ctx context.Context, path string, imagePath string, host string) error
}

type mediaFilesystemService interface {
	ValidatePaths(ctx context.Context, paths []string) ([]string, error)
}

type mediaScreenshotService interface {
	Plan(ctx context.Context, subject api.ScreenshotSubject, count int) (api.ScreenshotPlan, error)
	Capture(
		ctx context.Context,
		subject api.ScreenshotSubject,
		selections []api.ScreenshotSelection,
		purpose api.ScreenshotPurpose,
	) (api.ScreenshotResult, error)
	PreviewFrame(ctx context.Context, subject api.ScreenshotSubject, timestampSeconds float64) (api.ScreenshotPreview, error)
	Delete(ctx context.Context, subject api.ScreenshotSubject, imagePath string) error
	SaveFinalSelections(ctx context.Context, subject api.ScreenshotSubject, images []api.ScreenshotImage) error
}

type mediaDVDMenuService interface {
	Capture(ctx context.Context, subject api.DVDMenuSubject, maxItems int) (api.DVDMenuCaptureResult, error)
	List(ctx context.Context, subject api.DVDMenuSubject) ([]api.ScreenshotImage, error)
	Delete(ctx context.Context, subject api.DVDMenuSubject, imagePath string) error
	Capability(ctx context.Context) (api.DVDMenuEngineInfo, error)
}

type mediaImageHostingService interface {
	ListCandidates(ctx context.Context, subject api.ImageHostingSubject) ([]api.ScreenshotImage, error)
	Upload(
		ctx context.Context,
		subject api.ImageHostingSubject,
		host string,
		usageScope string,
		images []api.ScreenshotImage,
	) ([]api.UploadedImageLink, error)
}

// mediaModule owns screenshot, disc-menu, and image-host workflow policy. It
// borrows its services, repository, and prepared-release state from Core.
type mediaModule struct {
	cfg           config.Config
	logger        api.Logger
	filesystem    mediaFilesystemService
	screenshots   mediaScreenshotService
	dvdMenus      mediaDVDMenuService
	images        mediaImageHostingService
	repo          mediaRepository
	registry      *trackers.Registry
	preparedFacts *preparedrelease.Module
}

func newMediaModule(
	cfg config.Config,
	logger api.Logger,
	services api.ServiceSet,
	repo mediaRepository,
	registry *trackers.Registry,
	preparedFacts *preparedrelease.Module,
) *mediaModule {
	return &mediaModule{
		cfg:           cfg,
		logger:        logger,
		filesystem:    services.Filesystem,
		screenshots:   services.Screenshots,
		dvdMenus:      services.DVDMenus,
		images:        services.Images,
		repo:          repo,
		registry:      registry,
		preparedFacts: preparedFacts,
	}
}

func imageHostingSubject(meta api.UploadSubject) api.ImageHostingSubject {
	galleryName := ""
	for _, candidate := range []string{
		meta.ReleaseName,
		meta.ReleaseNameNoTag,
		meta.Release.Title,
		meta.Filename,
		filepath.Base(meta.SourcePath),
	} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			galleryName = trimmed
			break
		}
	}
	return api.ImageHostingSubject{SourcePath: meta.SourcePath, GalleryName: galleryName}
}

func (m *mediaModule) dvdMenuCapability(ctx context.Context) (api.DVDMenuEngineInfo, error) {
	if m == nil || m.dvdMenus == nil {
		return api.DVDMenuEngineInfo{}, errors.New("core: DVD menu service not configured")
	}
	return wrapCoreResult(m.dvdMenus.Capability(ctx))
}

func (m *mediaModule) captureAcceptedDVDMenus(ctx context.Context, input api.MediaPlanInput) (api.DVDMenuCaptureResult, error) {
	if m.dvdMenus == nil {
		return api.DVDMenuCaptureResult{}, errors.New("core: DVD menu service not configured")
	}
	if m.preparedFacts == nil {
		return api.DVDMenuCaptureResult{}, errors.New("core: canonical preparation is not configured")
	}
	subject, err := m.preparedFacts.ResolveDVDMenuSubject(ctx, input)
	if err != nil {
		return api.DVDMenuCaptureResult{}, fmt.Errorf("core: resolve DVD menu capture subject: %w", err)
	}
	return wrapCoreResult(m.dvdMenus.Capture(ctx, subject, m.cfg.ScreenshotHandling.ResolvedMaxMenuItems()))
}

func (m *mediaModule) listAcceptedDVDMenuScreenshots(ctx context.Context, input api.MediaPlanInput) ([]api.ScreenshotImage, error) {
	if m.dvdMenus == nil {
		return nil, errors.New("core: DVD menu service not configured")
	}
	if m.preparedFacts == nil {
		return nil, errors.New("core: canonical preparation is not configured")
	}
	subject, err := m.preparedFacts.ResolveDVDMenuSubject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("core: resolve DVD menu list subject: %w", err)
	}
	return wrapCoreResult(m.dvdMenus.List(ctx, subject))
}

func (m *mediaModule) deleteAcceptedDVDMenuScreenshot(ctx context.Context, input api.MediaPlanInput, imagePath string) error {
	if m.dvdMenus == nil {
		return errors.New("core: DVD menu service not configured")
	}
	if m.preparedFacts == nil {
		return errors.New("core: canonical preparation is not configured")
	}
	if strings.TrimSpace(imagePath) == "" {
		return internalerrors.ErrInvalidInput
	}
	subject, err := m.preparedFacts.ResolveDVDMenuSubject(ctx, input)
	if err != nil {
		return fmt.Errorf("core: resolve DVD menu deletion subject: %w", err)
	}
	return wrapCoreError(m.dvdMenus.Delete(ctx, subject, imagePath))
}

func (m *mediaModule) fetchAcceptedScreenshotPlan(ctx context.Context, input api.MediaPlanInput) (api.ScreenshotPlan, error) {
	if m.screenshots == nil {
		return api.ScreenshotPlan{}, errors.New("core: screenshots service not configured")
	}
	if m.preparedFacts == nil {
		return api.ScreenshotPlan{}, errors.New("core: canonical preparation is not configured")
	}
	if input.Count <= 0 {
		input.Count = m.cfg.ScreenshotHandling.Screens
	}
	subject, err := m.preparedFacts.ResolveScreenshotSubject(ctx, input)
	if err != nil {
		return api.ScreenshotPlan{}, fmt.Errorf("core: resolve screenshot plan subject: %w", err)
	}
	return wrapCoreResult(m.screenshots.Plan(ctx, subject, input.Count))
}

func (m *mediaModule) generateAcceptedScreenshots(
	ctx context.Context,
	input api.MediaPlanInput,
	selections []api.ScreenshotSelection,
) (api.ScreenshotResult, error) {
	if m.screenshots == nil {
		return api.ScreenshotResult{}, errors.New("core: screenshots service not configured")
	}
	if m.preparedFacts == nil {
		return api.ScreenshotResult{}, errors.New("core: canonical preparation is not configured")
	}
	if len(selections) == 0 {
		return api.ScreenshotResult{}, internalerrors.ErrInvalidInput
	}
	subject, err := m.preparedFacts.ResolveScreenshotSubject(ctx, input)
	if err != nil {
		return api.ScreenshotResult{}, fmt.Errorf("core: resolve screenshot capture subject: %w", err)
	}
	return wrapCoreResult(m.screenshots.Capture(ctx, subject, selections, input.Purpose))
}

func (m *mediaModule) previewAcceptedScreenshotFrame(
	ctx context.Context,
	input api.MediaPlanInput,
	timestampSeconds float64,
) (api.ScreenshotPreview, error) {
	if m.screenshots == nil {
		return api.ScreenshotPreview{}, errors.New("core: screenshots service not configured")
	}
	if m.preparedFacts == nil {
		return api.ScreenshotPreview{}, errors.New("core: canonical preparation is not configured")
	}
	subject, err := m.preparedFacts.ResolveScreenshotSubject(ctx, input)
	if err != nil {
		return api.ScreenshotPreview{}, fmt.Errorf("core: resolve screenshot preview subject: %w", err)
	}
	return wrapCoreResult(m.screenshots.PreviewFrame(ctx, subject, timestampSeconds))
}

func (m *mediaModule) deleteAcceptedScreenshot(ctx context.Context, input api.MediaPlanInput, imagePath string) error {
	if m.screenshots == nil {
		return errors.New("core: screenshots service not configured")
	}
	if m.preparedFacts == nil {
		return errors.New("core: canonical preparation is not configured")
	}
	if strings.TrimSpace(imagePath) == "" {
		return internalerrors.ErrInvalidInput
	}
	subject, err := m.preparedFacts.ResolveScreenshotSubject(ctx, input)
	if err != nil {
		return fmt.Errorf("core: resolve screenshot deletion subject: %w", err)
	}
	return wrapCoreError(m.screenshots.Delete(ctx, subject, imagePath))
}

func (m *mediaModule) deleteAcceptedTrackerImageURL(ctx context.Context, input api.ImageHostingInput, rawURL string) error {
	if m.repo == nil {
		return errors.New("core: repository not configured")
	}
	if m.preparedFacts == nil {
		return errors.New("core: canonical preparation is not configured")
	}
	trimmedURL := strings.TrimSpace(rawURL)
	if trimmedURL == "" {
		return internalerrors.ErrInvalidInput
	}
	subject, err := m.preparedFacts.ResolveImageHostingSubject(ctx, input)
	if err != nil {
		return fmt.Errorf("core: resolve tracker image deletion subject: %w", err)
	}
	return m.deleteTrackerImageURLForSource(ctx, subject.SourcePath, trimmedURL)
}

func (m *mediaModule) deleteTrackerImageURLForSource(ctx context.Context, sourcePath string, trimmedURL string) error {
	records, err := m.repo.ListTrackerMetadataByPath(ctx, sourcePath)
	if err != nil {
		return fmt.Errorf("core: tracker metadata lookup: %w", err)
	}

	for _, record := range records {
		if len(record.ImageURLs) == 0 {
			continue
		}
		filtered := make([]string, 0, len(record.ImageURLs))
		removed := false
		for _, value := range record.ImageURLs {
			if strings.TrimSpace(value) == trimmedURL {
				removed = true
				continue
			}
			filtered = append(filtered, value)
		}
		if !removed {
			continue
		}
		record.ImageURLs = filtered
		if strings.TrimSpace(record.SourcePath) == "" {
			record.SourcePath = sourcePath
		}
		if err := m.repo.SaveTrackerMetadata(ctx, record); err != nil {
			return fmt.Errorf("core: save tracker metadata: %w", err)
		}
	}

	return nil
}

func (m *mediaModule) saveAcceptedFinalScreenshotSelections(ctx context.Context, input api.MediaPlanInput, images []api.ScreenshotImage) error {
	if m.screenshots == nil {
		return errors.New("core: screenshots service not configured")
	}
	if m.preparedFacts == nil {
		return errors.New("core: canonical preparation is not configured")
	}
	subject, err := m.preparedFacts.ResolveScreenshotSubject(ctx, input)
	if err != nil {
		return fmt.Errorf("core: resolve final screenshot selection subject: %w", err)
	}
	return wrapCoreError(m.screenshots.SaveFinalSelections(ctx, subject, images))
}

// ImportMenuImages copies supported image files from host filesystem paths into
// one prepared release's managed temp directory. Content-addressed names dedupe
// repeated imports, and DB records/selections are appended atomically.

func (m *mediaModule) importAcceptedMenuImages(ctx context.Context, input api.MediaPlanInput, importPaths []string) error {
	if len(importPaths) == 0 {
		return nil
	}
	if m.preparedFacts == nil {
		return errors.New("core: canonical preparation is not configured")
	}
	subject, err := m.preparedFacts.ResolveDVDMenuSubject(ctx, input)
	if err != nil {
		return fmt.Errorf("core: resolve menu image import subject: %w", err)
	}
	return m.importMenuImagesForSource(ctx, subject.SourcePath, importPaths)
}

func (m *mediaModule) importMenuImagesForSource(ctx context.Context, sourcePath string, importPaths []string) error {
	var expandedPaths []string
	for _, p := range importPaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("stat menu path %s: %w", p, err)
		}
		if info.IsDir() {
			entries, err := os.ReadDir(p)
			if err != nil {
				return fmt.Errorf("read menu dir %s: %w", p, err)
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					ext := strings.ToLower(filepath.Ext(entry.Name()))
					if isMenuImageExtension(ext) {
						expandedPaths = append(expandedPaths, filepath.Join(p, entry.Name()))
					}
				}
			}
		} else {
			ext := strings.ToLower(filepath.Ext(p))
			if isMenuImageExtension(ext) {
				expandedPaths = append(expandedPaths, p)
			}
		}
	}

	tmpRoot, err := db.Subdir(m.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return fmt.Errorf("core: resolve tmp root: %w", err)
	}

	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, sourcePath, api.ReleaseInfo{})
	if err != nil {
		return fmt.Errorf("core: create release tmp dir: %w", err)
	}

	if len(expandedPaths) == 0 {
		return nil
	}

	now := time.Now().UTC()
	records := make([]api.Screenshot, 0, len(expandedPaths))
	selections := make([]api.ScreenshotFinalSelection, 0, len(expandedPaths))
	created := make([]string, 0, len(expandedPaths))
	seen := make(map[string]struct{}, len(expandedPaths))
	for _, sourceImage := range expandedPaths {
		destPath, wasCreated, err := copyManagedMenuImage(tmpDir, sourceImage)
		if err != nil {
			removeMenuImportFiles(created)
			return err
		}
		if _, exists := seen[destPath]; exists {
			continue
		}
		seen[destPath] = struct{}{}
		if wasCreated {
			created = append(created, destPath)
		}
		records = append(records, api.Screenshot{
			SourcePath: sourcePath,
			ImagePath:  destPath,
			Purpose:    api.ScreenshotPurposeMenu,
			CapturedAt: now,
		})
		selections = append(selections, api.ScreenshotFinalSelection{
			SourcePath: sourcePath,
			ImagePath:  destPath,
			Order:      len(selections),
			Source:     api.ScreenshotSelectionSourceMenu,
			SelectedAt: now,
		})
	}
	if err := m.repo.AppendManualMenuScreenshots(ctx, sourcePath, records, selections); err != nil {
		removeMenuImportFiles(created)
		return fmt.Errorf("core: save menu selections: %w", err)
	}
	return nil
}

func isMenuImageExtension(extension string) bool {
	switch strings.ToLower(strings.TrimSpace(extension)) {
	case ".png", ".jpg", ".jpeg", ".webp":
		return true
	default:
		return false
	}
}

// copyManagedMenuImage stages one import and assigns a content-addressed managed
// name. The boolean result reports whether this call created the destination.
func copyManagedMenuImage(tmpDir string, sourcePath string) (string, bool, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", false, fmt.Errorf("core: open menu image: %w", err)
	}
	defer source.Close()

	staged, err := os.CreateTemp(tmpDir, ".manual-dvd-menu-*.partial")
	if err != nil {
		return "", false, fmt.Errorf("core: stage menu image: %w", err)
	}
	stagedPath := staged.Name()
	cleanupStaged := true
	defer func() {
		if cleanupStaged {
			_ = os.Remove(stagedPath)
		}
	}()

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(staged, hash), source); err != nil {
		_ = staged.Close()
		return "", false, fmt.Errorf("core: copy menu image: %w", err)
	}
	if err := staged.Close(); err != nil {
		return "", false, fmt.Errorf("core: close staged menu image: %w", err)
	}
	extension := strings.ToLower(filepath.Ext(sourcePath))
	destPath := filepath.Join(tmpDir, fmt.Sprintf("manual-dvd-menu-%x%s", hash.Sum(nil)[:8], extension))
	if info, err := os.Stat(destPath); err == nil {
		if info.IsDir() {
			return "", false, internalerrors.ErrInvalidInput
		}
		return destPath, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("core: inspect managed menu image: %w", err)
	}
	if err := os.Rename(stagedPath, destPath); err != nil {
		if info, statErr := os.Stat(destPath); statErr == nil && !info.IsDir() {
			return destPath, false, nil
		}
		return "", false, fmt.Errorf("core: finalize menu image: %w", err)
	}
	cleanupStaged = false
	return destPath, true, nil
}

func removeMenuImportFiles(paths []string) {
	for _, pathValue := range paths {
		_ = os.Remove(pathValue)
	}
}

// ListUploadCandidates returns persisted normal and disc-menu images eligible
// for image-host upload for one prepared release.

func (m *mediaModule) listAcceptedUploadCandidates(ctx context.Context, input api.ImageHostingInput) ([]api.ScreenshotImage, error) {
	if m.images == nil {
		return nil, errors.New("core: image hosting service not configured")
	}
	if m.preparedFacts == nil {
		return nil, errors.New("core: canonical preparation is not configured")
	}
	subject, err := m.preparedFacts.ResolveImageHostingSubject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("core: resolve image upload candidate subject: %w", err)
	}
	return wrapCoreResult(m.images.ListCandidates(ctx, subject))
}

func (m *mediaModule) listAcceptedUploadedImages(ctx context.Context, input api.ImageHostingInput) ([]api.UploadedImageLink, error) {
	if m.repo == nil {
		return nil, errors.New("core: repository not configured")
	}
	if m.preparedFacts == nil {
		return nil, errors.New("core: canonical preparation is not configured")
	}
	subject, err := m.preparedFacts.ResolveImageHostingSubject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("core: resolve uploaded image list subject: %w", err)
	}
	return wrapCoreResult(m.repo.ListUploadedImagesByPath(ctx, subject.SourcePath))
}

// UploadImages uploads one source's selected images to the requested global
// host and any additional hosts required by its eligible trackers. Host uploads
// run concurrently, tracker-owned hosts use tracker-scoped records, and
// recoverable host failures are returned in [api.UploadImagesResult].

func (m *mediaModule) uploadAcceptedImages(
	ctx context.Context,
	input api.ImageHostingInput,
	images []api.ScreenshotImage,
) (api.UploadImagesResult, error) {
	if m.images == nil {
		return api.UploadImagesResult{}, errors.New("core: image hosting service not configured")
	}
	if m.preparedFacts == nil {
		return api.UploadImagesResult{}, errors.New("core: canonical preparation is not configured")
	}
	if len(images) == 0 {
		return api.UploadImagesResult{}, internalerrors.ErrInvalidInput
	}
	subject, err := m.preparedFacts.ResolveUploadSubject(ctx, api.UploadReviewInput{
		Release:  input.Release,
		Trackers: append([]string(nil), input.Trackers...),
	})
	if err != nil {
		return api.UploadImagesResult{}, fmt.Errorf("core: resolve image upload subject: %w", err)
	}
	targets, err := m.resolveImageUploadTargets(input.Trackers, subject, input.Host)
	if err != nil {
		return api.UploadImagesResult{}, err
	}
	return m.uploadImagesToTargetsWithFallback(ctx, subject, input.Host, targets, images)
}

func (m *mediaModule) resolveImageUploadTargets(trackerNames []string, subject api.UploadSubject, host string) ([]trackers.ImageUploadTarget, error) {
	normalizedHost := strings.ToLower(strings.TrimSpace(host))
	if normalizedHost == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	trackerCfg := m.cfg
	trackerCfg.Trackers.DefaultTrackers = nil
	resolvedTrackers := trackers.ResolveTrackersWithRegistry(trackerCfg, trackerNames, subject.TrackersRemove, m.logger, m.registry)
	resolvedTrackers = m.filterImageUploadTrackers(resolvedTrackers, subject)
	targets, err := trackers.NeededImageUploadTargetsForMetadataWithRegistry(m.registry, m.cfg, resolvedTrackers, normalizedHost, subject)
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("core: image host %q is tracker-scoped but no active tracker can use it", normalizedHost)
	}
	normalized := make([]trackers.ImageUploadTarget, 0, len(targets))
	for _, target := range targets {
		target = normalizeImageUploadTarget(target)
		if target.Host == "" {
			continue
		}
		normalized = append(normalized, target)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf(
			"core: image host %q resolved image upload targets were filtered out after tracker eligibility and normalization",
			normalizedHost,
		)
	}
	return normalized, nil
}

// filterImageUploadTrackers returns canonical tracker names that can receive
// tracker-scoped image uploads, excluding unconditional policy blocks and existing matches.
func (m *mediaModule) filterImageUploadTrackers(trackerNames []string, meta api.UploadSubject) []string {
	filtered := make([]string, 0, len(trackerNames))
	for _, tracker := range trackerNames {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		blockedReasons := blockedReasonsForTracker(meta.BlockedTrackers, name)
		existingMatch := matchedTrackerForUpload(meta.MatchedTrackers, name)
		if len(blockedReasons) > 0 || existingMatch {
			if m.logger != nil {
				m.logger.Debugf(
					"core: excluding blocked image upload tracker tracker=%s blocked_reasons=%v rule_failures=%d existing_match=%t",
					name,
					blockedReasons,
					len(ruleFailuresForTracker(meta.TrackerRuleFailures, name)),
					existingMatch,
				)
			}
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func matchedTrackerForUpload(matchedTrackers []string, tracker string) bool {
	if len(matchedTrackers) == 0 {
		return false
	}
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if name == "" {
		return false
	}
	for _, matched := range matchedTrackers {
		if strings.EqualFold(strings.TrimSpace(matched), name) {
			return true
		}
	}
	return false
}

func blockedReasonsForTracker(blocked map[string][]api.TrackerBlockReason, tracker string) []api.TrackerBlockReason {
	if len(blocked) == 0 {
		return nil
	}
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if reasons, ok := blocked[name]; ok {
		return reasons
	}
	for key, reasons := range blocked {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return reasons
		}
	}
	return nil
}

func ruleFailuresForTracker(failures map[string][]api.RuleFailure, tracker string) []api.RuleFailure {
	if len(failures) == 0 {
		return nil
	}
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if trackerFailures, ok := failures[name]; ok {
		return trackerFailures
	}
	for key, trackerFailures := range failures {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return trackerFailures
		}
	}
	return nil
}

func (m *mediaModule) resolveFallbackImageUploadTargets(
	host string,
	trackerNames []string,
	excludedHosts []string,
	meta api.UploadSubject,
) ([]trackers.ImageUploadTarget, error) {
	normalizedHost := strings.ToLower(strings.TrimSpace(host))
	if normalizedHost == "" || len(trackerNames) == 0 {
		return nil, nil
	}
	targets, err := trackers.NeededImageUploadTargetsForMetadataExcludingWithRegistry(m.registry, m.cfg, trackerNames, normalizedHost, excludedHosts, meta)
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}
	normalized := make([]trackers.ImageUploadTarget, 0, len(targets))
	for _, target := range targets {
		target = normalizeImageUploadTarget(target)
		if target.Host == "" {
			continue
		}
		normalized = append(normalized, target)
	}
	return normalized, nil
}

func (m *mediaModule) uploadImagesToTargetsWithFallback(
	ctx context.Context,
	meta api.UploadSubject,
	host string,
	targets []trackers.ImageUploadTarget,
	images []api.ScreenshotImage,
) (api.UploadImagesResult, error) {
	allLinks := make([]api.UploadedImageLink, 0, len(images)*len(targets))
	failedHosts := make(map[string]struct{}, len(targets))
	currentTargets := targets
	fallbackAttempt := false
	var failures []api.UploadImageHostFailure

	for len(currentTargets) > 0 {
		result := m.uploadImagesToTargets(ctx, meta, currentTargets, images, fallbackAttempt)
		allLinks = append(allLinks, result.Links...)
		if len(result.Failures) == 0 {
			return api.UploadImagesResult{Links: allLinks}, nil
		}

		failures = result.Failures
		for _, failure := range result.Failures {
			host := strings.ToLower(strings.TrimSpace(failure.Host))
			if host != "" {
				failedHosts[host] = struct{}{}
			}
		}

		blockedTrackers := uploadFailureTrackers(failures)
		fallbackTargets, err := m.resolveFallbackImageUploadTargets(host, blockedTrackers, sortedMapKeys(failedHosts), meta)
		if err != nil {
			return api.UploadImagesResult{}, err
		}

		var recoveredTrackers []string
		nextTargets := make([]trackers.ImageUploadTarget, 0, len(fallbackTargets))
		for _, target := range fallbackTargets {
			if uploadedLinksCoverTarget(allLinks, target, len(images)) {
				recoveredTrackers = append(recoveredTrackers, target.Trackers...)
				continue
			}
			nextTargets = append(nextTargets, target)
		}
		failures = filterUploadFailuresForRecoveredTrackers(failures, recoveredTrackers)
		if len(nextTargets) == 0 {
			if len(failures) == 0 {
				return api.UploadImagesResult{Links: allLinks}, nil
			}
			return api.UploadImagesResult{Links: allLinks, Failures: failures}, nil
		}

		m.logger.Warnf(
			"core: retrying image uploads after host failures failed_hosts=%s fallback_hosts=%s trackers=%v",
			strings.Join(sortedMapKeys(failedHosts), ","),
			strings.Join(uploadTargetHosts(nextTargets), ","),
			uploadTargetTrackers(nextTargets),
		)
		currentTargets = nextTargets
		fallbackAttempt = true
	}

	return api.UploadImagesResult{Links: allLinks, Failures: failures}, nil
}

func uploadFailureTrackers(failures []api.UploadImageHostFailure) []string {
	trackersList := make([]string, 0)
	for _, failure := range failures {
		for _, tracker := range failure.Trackers {
			trackersList = appendUniqueNormalizedTracker(trackersList, tracker)
		}
	}
	return trackersList
}

func filterUploadFailuresForRecoveredTrackers(failures []api.UploadImageHostFailure, recoveredTrackers []string) []api.UploadImageHostFailure {
	if len(failures) == 0 || len(recoveredTrackers) == 0 {
		return failures
	}
	recovered := make(map[string]struct{}, len(recoveredTrackers))
	for _, tracker := range recoveredTrackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name != "" {
			recovered[name] = struct{}{}
		}
	}

	filtered := make([]api.UploadImageHostFailure, 0, len(failures))
	for _, failure := range failures {
		remainingTrackers := make([]string, 0, len(failure.Trackers))
		for _, tracker := range failure.Trackers {
			name := strings.ToUpper(strings.TrimSpace(tracker))
			if name == "" {
				continue
			}
			if _, ok := recovered[name]; ok {
				continue
			}
			remainingTrackers = appendUniqueNormalizedTracker(remainingTrackers, name)
		}
		if len(failure.Trackers) > 0 && len(remainingTrackers) == 0 {
			continue
		}
		failure.Trackers = remainingTrackers
		filtered = append(filtered, failure)
	}
	return filtered
}

func uploadedLinksCoverTarget(links []api.UploadedImageLink, target trackers.ImageUploadTarget, expectedImages int) bool {
	if expectedImages == 0 {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(target.Host))
	scope := normalizeImageUploadUsageScope(target.UsageScope)
	seenPaths := make(map[string]struct{}, expectedImages)
	for _, link := range links {
		if !strings.EqualFold(strings.TrimSpace(link.Host), host) {
			continue
		}
		if normalizeImageUploadUsageScope(link.UsageScope) != scope {
			continue
		}
		path := normalizedUploadImagePath(link.ImagePath)
		if path == "" {
			continue
		}
		seenPaths[path] = struct{}{}
	}
	return len(seenPaths) >= expectedImages
}

func uploadTargetHosts(targets []trackers.ImageUploadTarget) []string {
	hosts := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		host := strings.ToLower(strings.TrimSpace(target.Host))
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	slices.Sort(hosts)
	return hosts
}

func uploadTargetTrackers(targets []trackers.ImageUploadTarget) []string {
	trackersList := make([]string, 0)
	for _, target := range targets {
		for _, tracker := range target.Trackers {
			trackersList = appendUniqueNormalizedTracker(trackersList, tracker)
		}
	}
	slices.Sort(trackersList)
	return trackersList
}

func sortedMapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func (m *mediaModule) uploadImagesToTargets(
	ctx context.Context,
	meta api.UploadSubject,
	targets []trackers.ImageUploadTarget,
	images []api.ScreenshotImage,
	fallback bool,
) api.UploadImagesResult {
	m.logger.Infof(
		"core: image upload round started hosts=%d host_names=%s fallback=%t images=%d",
		len(targets),
		strings.Join(uploadTargetHosts(targets), ","),
		fallback,
		len(images),
	)
	type uploadResult struct {
		index  int
		target trackers.ImageUploadTarget
		links  []api.UploadedImageLink
		err    error
	}

	resultCh := make(chan uploadResult, len(targets))
	var wg sync.WaitGroup
	for idx, target := range targets {
		wg.Add(1)
		go func(idx int, target trackers.ImageUploadTarget) {
			defer wg.Done()
			uploaded, err := m.uploadImagesToTarget(ctx, meta, target, images, fallback)
			resultCh <- uploadResult{
				index:  idx,
				target: normalizeImageUploadTarget(target),
				links:  uploaded,
				err:    err,
			}
		}(idx, target)
	}
	wg.Wait()
	close(resultCh)

	ordered := make([]uploadResult, len(targets))
	for result := range resultCh {
		ordered[result.index] = result
	}

	results := make([]api.UploadedImageLink, 0, len(images)*len(targets))
	failures := make([]api.UploadImageHostFailure, 0)
	failureMessages := make([]string, 0)
	for _, result := range ordered {
		results = append(results, result.links...)
		if result.err != nil {
			failure := api.UploadImageHostFailure{
				Host:       result.target.Host,
				UsageScope: result.target.UsageScope,
				Trackers:   slices.Clone(result.target.Trackers),
				Message:    logging.SanitizeMessage(uploadFailureMessage(result.err)),
			}
			failures = append(failures, failure)
			failureMessages = append(failureMessages, fmt.Sprintf(
				"host=%s trackers=%v err=%s",
				result.target.Host,
				result.target.Trackers,
				redaction.RedactValue(failure.Message, nil),
			))
		}
	}
	if len(failures) == 0 {
		return api.UploadImagesResult{Links: results}
	}
	result := api.UploadImagesResult{Links: results, Failures: failures}
	if len(results) > 0 {
		m.logger.Warnf(
			"core: image uploads completed with %d host failures and %d successful links: %s",
			len(failures),
			len(results),
			strings.Join(failureMessages, "; "),
		)
		return result
	}
	m.logger.Warnf("core: image uploads failed for all hosts: %s", strings.Join(failureMessages, "; "))
	return result
}

func normalizeImageUploadTarget(target trackers.ImageUploadTarget) trackers.ImageUploadTarget {
	target.Host = strings.ToLower(strings.TrimSpace(target.Host))
	target.UsageScope = normalizeImageUploadUsageScope(target.UsageScope)
	trackersList := make([]string, 0, len(target.Trackers))
	for _, tracker := range target.Trackers {
		trackersList = appendUniqueNormalizedTracker(trackersList, tracker)
	}
	target.Trackers = trackersList
	return target
}

func appendUniqueNormalizedTracker(trackersList []string, tracker string) []string {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if name == "" {
		return trackersList
	}
	if slices.Contains(trackersList, name) {
		return trackersList
	}
	return append(trackersList, name)
}

func (m *mediaModule) uploadImagesToTarget(
	ctx context.Context,
	meta api.UploadSubject,
	target trackers.ImageUploadTarget,
	images []api.ScreenshotImage,
	fallback bool,
) ([]api.UploadedImageLink, error) {
	target.Host = strings.ToLower(strings.TrimSpace(target.Host))
	target.UsageScope = normalizeImageUploadUsageScope(target.UsageScope)
	progressTarget := api.ImageUploadProgressTarget{
		AttemptID:  target.Host + "|" + target.UsageScope,
		Host:       target.Host,
		UsageScope: target.UsageScope,
		Trackers:   slices.Clone(target.Trackers),
		Fallback:   fallback,
		Total:      len(images),
	}
	progressCtx := api.WithImageUploadProgressTarget(ctx, progressTarget)
	emitCoreImageUploadProgress(progressCtx, progressTarget, api.ImageUploadProgressRunning, 0, 0, 0, "Preparing host upload.")
	if m.repo == nil {
		m.logger.Tracef(
			"core: uploading images host=%s tracker=%s scope=%s trackers=%v count=%d",
			target.Host,
			m.imageHostOwnerLogValue(target.Host),
			target.UsageScope,
			target.Trackers,
			len(images),
		)
		uploaded, err := m.images.Upload(progressCtx, imageHostingSubject(meta), target.Host, target.UsageScope, images)
		emitCoreImageUploadResult(progressCtx, progressTarget, len(uploaded), err)
		return wrapCoreResult(uploaded, err)
	}

	existing, err := m.repo.ListUploadedImagesByPath(ctx, meta.SourcePath)
	if err != nil {
		emitCoreImageUploadProgress(progressCtx, progressTarget, api.ImageUploadProgressFailed, 0, 0, 0, "Existing uploads could not be checked.")
		return nil, fmt.Errorf("core: %w", err)
	}
	existingByPath := uploadedImagesByPathForTarget(existing, target)
	results := make([]api.UploadedImageLink, 0, len(images))
	missing := make([]api.ScreenshotImage, 0, len(images))
	for _, image := range images {
		key := normalizedUploadImagePath(image.Path)
		if key == "" {
			missing = append(missing, image)
			continue
		}
		if link, ok := existingByPath[key]; ok {
			results = append(results, link)
			continue
		}
		missing = append(missing, image)
	}
	progressTarget.Reused = len(results)
	progressCtx = api.WithImageUploadProgressTarget(ctx, progressTarget)
	if len(missing) == 0 {
		m.logger.Tracef(
			"core: reusing uploaded images host=%s tracker=%s scope=%s trackers=%v count=%d",
			target.Host,
			m.imageHostOwnerLogValue(target.Host),
			target.UsageScope,
			target.Trackers,
			len(results),
		)
		emitCoreImageUploadProgress(
			progressCtx,
			progressTarget,
			api.ImageUploadProgressCompleted,
			len(results),
			0,
			0,
			"Reused existing host uploads.",
		)
		return results, nil
	}
	emitCoreImageUploadProgress(
		progressCtx,
		progressTarget,
		api.ImageUploadProgressRunning,
		len(results),
		0,
		0,
		fmt.Sprintf("Uploading %d images; %d already ready.", len(missing), len(results)),
	)

	m.logger.Debugf(
		"core: uploading missing images host=%s tracker=%s scope=%s trackers=%v missing=%d reused=%d",
		target.Host,
		m.imageHostOwnerLogValue(target.Host),
		target.UsageScope,
		target.Trackers,
		len(missing),
		len(results),
	)
	uploaded, err := m.images.Upload(progressCtx, imageHostingSubject(meta), target.Host, target.UsageScope, missing)
	results = append(results, uploaded...)
	emitCoreImageUploadResult(progressCtx, progressTarget, len(uploaded), err)
	if err != nil {
		return results, fmt.Errorf("core: %w", err)
	}
	return results, nil
}

func emitCoreImageUploadResult(
	ctx context.Context,
	target api.ImageUploadProgressTarget,
	uploaded int,
	err error,
) {
	if err == nil {
		emitCoreImageUploadProgress(
			ctx,
			target,
			api.ImageUploadProgressCompleted,
			target.Total,
			uploaded,
			0,
			"Host upload complete.",
		)
		return
	}
	failed := max(0, target.Total-target.Reused-uploaded)
	emitCoreImageUploadProgress(
		ctx,
		target,
		api.ImageUploadProgressFailed,
		target.Reused+uploaded+failed,
		uploaded,
		failed,
		fmt.Sprintf("%d of %d host uploads failed.", failed, target.Total-target.Reused),
	)
}

func emitCoreImageUploadProgress(
	ctx context.Context,
	target api.ImageUploadProgressTarget,
	status api.ImageUploadProgressStatus,
	completed int,
	succeeded int,
	failed int,
	message string,
) {
	total := max(0, target.Total)
	api.EmitImageUploadProgress(ctx, api.ImageUploadProgressUpdate{
		AttemptID:  target.AttemptID,
		Host:       target.Host,
		UsageScope: target.UsageScope,
		Trackers:   slices.Clone(target.Trackers),
		Fallback:   target.Fallback,
		Completed:  max(0, min(completed, total)),
		Total:      total,
		Succeeded:  max(0, succeeded),
		Failed:     max(0, failed),
		Reused:     max(0, min(target.Reused, total)),
		Status:     status,
		Message:    strings.TrimSpace(message),
	})
}

func uploadFailureMessage(err error) string {
	if err == nil {
		return ""
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		return unwrapped.Error()
	}
	return err.Error()
}

func uploadedImagesByPathForTarget(images []api.UploadedImageLink, target trackers.ImageUploadTarget) map[string]api.UploadedImageLink {
	matches := make(map[string]api.UploadedImageLink, len(images))
	for _, image := range images {
		if !strings.EqualFold(strings.TrimSpace(image.Host), target.Host) {
			continue
		}
		if !strings.EqualFold(normalizeImageUploadUsageScope(image.UsageScope), normalizeImageUploadUsageScope(target.UsageScope)) {
			continue
		}
		key := normalizedUploadImagePath(image.ImagePath)
		if key == "" {
			continue
		}
		matches[key] = image
	}
	return matches
}

func normalizedUploadImagePath(pathValue string) string {
	trimmed := strings.TrimSpace(pathValue)
	if trimmed == "" {
		return ""
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(absPath)
}

func normalizeImageUploadUsageScope(scope string) string {
	trimmed := strings.TrimSpace(scope)
	if trimmed == "" || strings.EqualFold(trimmed, "global") {
		return "global"
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "tracker:") {
		tracker := strings.ToUpper(strings.TrimSpace(trimmed[len("tracker:"):]))
		if tracker == "" {
			return "global"
		}
		return "tracker:" + tracker
	}
	return trimmed
}

// imageHostOwnerLogValue identifies tracker-owned hosts in core upload logs.
func (m *mediaModule) imageHostOwnerLogValue(host string) string {
	if tracker := trackers.TrackerForOwnedImageHost(m.registry, host); tracker != "" {
		return tracker
	}
	return "shared"
}

func (m *mediaModule) deleteAcceptedUploadedImage(
	ctx context.Context,
	input api.ImageHostingInput,
	imagePath string,
	host string,
) error {
	if m.repo == nil {
		return errors.New("core: repository not configured")
	}
	if m.preparedFacts == nil {
		return errors.New("core: canonical preparation is not configured")
	}
	if strings.TrimSpace(imagePath) == "" || strings.TrimSpace(host) == "" {
		return internalerrors.ErrInvalidInput
	}
	subject, err := m.preparedFacts.ResolveImageHostingSubject(ctx, input)
	if err != nil {
		return fmt.Errorf("core: resolve uploaded image deletion subject: %w", err)
	}
	m.logger.Debugf("core: deleting uploaded image path=%s host=%s tracker=%s", imagePath, host, m.imageHostOwnerLogValue(host))
	return wrapCoreError(m.repo.DeleteUploadedImage(ctx, subject.SourcePath, imagePath, host))
}
