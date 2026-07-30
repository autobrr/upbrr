// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/logging"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/preparedrelease"
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

type menuImageContent struct {
	contentType string
	bytes       []byte
}

// importAcceptedMenuImageContents persists browser/API-uploaded image bytes in
// managed release storage and returns private image identities for workflow use.
func (m *mediaModule) importAcceptedMenuImageContents(
	ctx context.Context,
	input api.MediaPlanInput,
	contents []menuImageContent,
) (api.DVDMenuSubject, []api.ScreenshotImage, error) {
	if len(contents) == 0 {
		return api.DVDMenuSubject{}, nil, nil
	}
	if m.preparedFacts == nil || m.repo == nil {
		return api.DVDMenuSubject{}, nil, errors.New("core: menu image import is unavailable")
	}
	subject, err := m.preparedFacts.ResolveDVDMenuSubject(ctx, input)
	if err != nil {
		return api.DVDMenuSubject{}, nil, fmt.Errorf("core: resolve staged menu image subject: %w", err)
	}
	tmpRoot, err := db.Subdir(m.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return api.DVDMenuSubject{}, nil, fmt.Errorf("core: resolve tmp root: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, subject.SourcePath, api.ReleaseInfo{})
	if err != nil {
		return api.DVDMenuSubject{}, nil, fmt.Errorf("core: create release tmp dir: %w", err)
	}
	now := time.Now().UTC()
	images := make([]api.ScreenshotImage, 0, len(contents))
	records := make([]api.Screenshot, 0, len(contents))
	selections := make([]api.ScreenshotFinalSelection, 0, len(contents))
	created := make([]string, 0, len(contents))
	seen := make(map[string]struct{}, len(contents))
	for _, content := range contents {
		if err := ctx.Err(); err != nil {
			removeMenuImportFiles(created)
			return api.DVDMenuSubject{}, nil, fmt.Errorf("core: stage menu image canceled: %w", err)
		}
		extension, extensionErr := stagedMenuImageExtension(content.contentType)
		if extensionErr != nil || len(content.bytes) == 0 {
			removeMenuImportFiles(created)
			return api.DVDMenuSubject{}, nil, internalerrors.ErrInvalidInput
		}
		destPath, wasCreated, writeErr := writeManagedMenuImage(tmpDir, content.bytes, extension)
		if writeErr != nil {
			removeMenuImportFiles(created)
			return api.DVDMenuSubject{}, nil, writeErr
		}
		if _, duplicate := seen[destPath]; duplicate {
			continue
		}
		seen[destPath] = struct{}{}
		if wasCreated {
			created = append(created, destPath)
		}
		image := api.ScreenshotImage{
			Index:     len(images),
			Path:      destPath,
			Purpose:   api.ScreenshotPurposeMenu,
			SizeBytes: int64(len(content.bytes)),
		}
		images = append(images, image)
		records = append(records, api.Screenshot{
			SourcePath: subject.SourcePath,
			ImagePath:  destPath,
			Purpose:    api.ScreenshotPurposeMenu,
			CapturedAt: now,
		})
		selections = append(selections, api.ScreenshotFinalSelection{
			SourcePath: subject.SourcePath,
			ImagePath:  destPath,
			Order:      len(selections),
			Source:     api.ScreenshotSelectionSourceMenu,
			SelectedAt: now,
		})
	}
	if err := m.repo.AppendManualMenuScreenshots(ctx, subject.SourcePath, records, selections); err != nil {
		removeMenuImportFiles(created)
		return api.DVDMenuSubject{}, nil, fmt.Errorf("core: save staged menu selections: %w", err)
	}
	return subject, images, nil
}

func stagedMenuImageExtension(contentType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])) {
	case "image/png":
		return ".png", nil
	case "image/jpeg":
		return ".jpg", nil
	case "image/webp":
		return ".webp", nil
	default:
		return "", internalerrors.ErrInvalidInput
	}
}

func writeManagedMenuImage(tmpDir string, data []byte, extension string) (string, bool, error) {
	sum := sha256.Sum256(data)
	destPath := filepath.Join(tmpDir, fmt.Sprintf("manual-dvd-menu-%x%s", sum[:8], extension))
	if info, err := os.Stat(destPath); err == nil {
		if info.IsDir() {
			return "", false, internalerrors.ErrInvalidInput
		}
		return destPath, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("core: inspect managed menu image: %w", err)
	}
	staged, err := os.CreateTemp(tmpDir, ".manual-dvd-menu-*.partial")
	if err != nil {
		return "", false, fmt.Errorf("core: stage menu image: %w", err)
	}
	stagedPath := staged.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(stagedPath)
		}
	}()
	if _, err := staged.Write(data); err != nil {
		_ = staged.Close()
		return "", false, fmt.Errorf("core: write staged menu image: %w", err)
	}
	if err := staged.Close(); err != nil {
		return "", false, fmt.Errorf("core: close staged menu image: %w", err)
	}
	if err := os.Rename(stagedPath, destPath); err != nil {
		if info, statErr := os.Stat(destPath); statErr == nil && !info.IsDir() {
			return destPath, false, nil
		}
		return "", false, fmt.Errorf("core: finalize staged menu image: %w", err)
	}
	cleanup = false
	return destPath, true, nil
}

func removeMenuImportFiles(paths []string) {
	for _, pathValue := range paths {
		_ = os.Remove(pathValue)
	}
}

// uploadAcceptedImages uploads selected images to the requested global host
// and additional hosts required by eligible trackers. Host uploads run
// concurrently, tracker-owned hosts use tracker-scoped records, and recoverable
// host failures are returned in [api.UploadImagesResult].
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
	subject, err := m.preparedFacts.ResolveUploadSubject(ctx, api.UploadSubjectInput{
		Release:  input.Release,
		Trackers: append([]string(nil), input.Trackers...),
	})
	if err != nil {
		return api.UploadImagesResult{}, fmt.Errorf("core: resolve image upload subject: %w", err)
	}
	targets, err := m.resolveImageUploadTargets(input.Trackers, subject, input.Host, input.ExcludedHosts)
	if err != nil {
		return api.UploadImagesResult{}, err
	}
	return m.uploadImagesToTargetsWithFallback(ctx, subject, input.Host, input.ExcludedHosts, targets, images)
}

func (m *mediaModule) resolveImageUploadTargets(
	trackerNames []string,
	subject api.UploadSubject,
	host string,
	excludedHosts []string,
) ([]trackers.ImageUploadTarget, error) {
	normalizedHost := strings.ToLower(strings.TrimSpace(host))

	// The workflow already supplied its exact downstream-eligible projection set.
	// Do not reapply legacy prepared-subject removals or client matches here.
	resolvedTrackers := trackers.ResolveExplicitTrackersWithRegistry(trackerNames, m.logger, m.registry)
	targets, err := trackers.NeededImageUploadTargetsForMetadataExcludingWithRegistry(
		m.registry,
		m.cfg,
		resolvedTrackers,
		normalizedHost,
		excludedHosts,
		subject,
	)
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}
	if len(targets) == 0 {
		if normalizedHost == "" {
			return nil, nil
		}
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

func (m *mediaModule) resolveFallbackImageUploadTargets(
	host string,
	trackerNames []string,
	excludedHosts []string,
	meta api.UploadSubject,
) ([]trackers.ImageUploadTarget, error) {
	normalizedHost := strings.ToLower(strings.TrimSpace(host))
	if len(trackerNames) == 0 {
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
	excludedHosts []string,
	targets []trackers.ImageUploadTarget,
	images []api.ScreenshotImage,
) (api.UploadImagesResult, error) {
	type scheduledAttempt struct {
		target   trackers.ImageUploadTarget
		fallback bool
		links    []api.UploadedImageLink
		err      error
		done     bool
	}
	type completedAttempt struct {
		index int
		links []api.UploadedImageLink
		err   error
	}

	attempts := make([]*scheduledAttempt, 0, len(targets))
	attemptByTarget := make(map[string]*scheduledAttempt, len(targets))
	failedHosts := make(map[string]struct{}, len(targets)+len(excludedHosts))
	for _, excludedHost := range excludedHosts {
		if normalized := strings.ToLower(strings.TrimSpace(excludedHost)); normalized != "" {
			failedHosts[normalized] = struct{}{}
		}
	}
	coveredTrackers := make(map[string]struct{})
	results := make(chan completedAttempt)
	pending := 0

	targetKey := func(target trackers.ImageUploadTarget) string {
		return strings.ToLower(strings.TrimSpace(target.Host)) + "\x00" + normalizeImageUploadUsageScope(target.UsageScope)
	}
	markCovered := func(target trackers.ImageUploadTarget) {
		for _, tracker := range target.Trackers {
			name := strings.ToUpper(strings.TrimSpace(tracker))
			if name != "" {
				coveredTrackers[name] = struct{}{}
			}
		}
	}
	uncoveredTrackers := func(trackersList []string) []string {
		result := make([]string, 0, len(trackersList))
		for _, tracker := range trackersList {
			name := strings.ToUpper(strings.TrimSpace(tracker))
			if name == "" {
				continue
			}
			if _, covered := coveredTrackers[name]; !covered {
				result = appendUniqueNormalizedTracker(result, name)
			}
		}
		return result
	}
	launch := func(target trackers.ImageUploadTarget, fallback bool) bool {
		target = normalizeImageUploadTarget(target)
		if target.Host == "" || len(uncoveredTrackers(target.Trackers)) == 0 {
			return false
		}
		key := targetKey(target)
		if existing := attemptByTarget[key]; existing != nil {
			for _, tracker := range target.Trackers {
				existing.target.Trackers = appendUniqueNormalizedTracker(existing.target.Trackers, tracker)
			}
			if existing.done && existing.err == nil {
				markCovered(existing.target)
			}
			return false
		}
		attempt := &scheduledAttempt{target: target, fallback: fallback}
		index := len(attempts)
		attempts = append(attempts, attempt)
		attemptByTarget[key] = attempt
		pending++
		go func() {
			links, err := m.uploadImagesToTarget(ctx, meta, target, images, fallback)
			results <- completedAttempt{
				index: index,
				links: links,
				err:   err,
			}
		}()
		return true
	}

	for _, target := range targets {
		launch(target, false)
	}
	var schedulerErr error
	for pending > 0 {
		completed := <-results
		pending--
		attempt := attempts[completed.index]
		attempt.links = completed.links
		attempt.err = completed.err
		attempt.done = true
		if completed.err == nil {
			markCovered(attempt.target)
		}
		if completed.err == nil || schedulerErr != nil {
			continue
		}
		failedHost := strings.ToLower(strings.TrimSpace(attempt.target.Host))
		if failedHost != "" {
			failedHosts[failedHost] = struct{}{}
		}
		blockedTrackers := uncoveredTrackers(attempt.target.Trackers)
		if len(blockedTrackers) == 0 {
			continue
		}
		fallbackTargets, err := m.resolveFallbackImageUploadTargets(host, blockedTrackers, sortedMapKeys(failedHosts), meta)
		if err != nil {
			schedulerErr = err
			continue
		}
		startedTargets := make([]trackers.ImageUploadTarget, 0, len(fallbackTargets))
		for _, fallbackTarget := range fallbackTargets {
			if launch(fallbackTarget, true) {
				startedTargets = append(startedTargets, fallbackTarget)
			}
		}
		if len(startedTargets) > 0 {
			m.logger.Infof(
				"core: starting image upload fallback failed_hosts=%s fallback_hosts=%s trackers=%v",
				strings.Join(sortedMapKeys(failedHosts), ","),
				strings.Join(uploadTargetHosts(startedTargets), ","),
				uploadTargetTrackers(startedTargets),
			)
		}
	}
	if schedulerErr != nil {
		return api.UploadImagesResult{}, schedulerErr
	}

	allLinks := make([]api.UploadedImageLink, 0, len(images)*len(attempts))
	failures := make([]api.UploadImageHostFailure, 0)
	attemptResults := make([]api.UploadImageHostAttemptResult, 0, len(attempts))
	for _, attempt := range attempts {
		allLinks = append(allLinks, attempt.links...)
		attemptResult := api.UploadImageHostAttemptResult{
			Host:       attempt.target.Host,
			UsageScope: attempt.target.UsageScope,
			Trackers:   append([]string(nil), attempt.target.Trackers...),
			Fallback:   attempt.fallback,
			Links:      append([]api.UploadedImageLink(nil), attempt.links...),
		}
		if attempt.err == nil {
			attemptResults = append(attemptResults, attemptResult)
			continue
		}
		attemptFailure := api.UploadImageHostFailure{
			Host:       attempt.target.Host,
			UsageScope: attempt.target.UsageScope,
			Trackers:   append([]string(nil), attempt.target.Trackers...),
			Message:    logging.SanitizeMessage(uploadFailureMessage(attempt.err)),
		}
		attemptResult.Failure = &attemptFailure
		attemptResults = append(attemptResults, attemptResult)
		remainingTrackers := uncoveredTrackers(attempt.target.Trackers)
		if len(attempt.target.Trackers) > 0 && len(remainingTrackers) == 0 {
			continue
		}
		failures = append(failures, api.UploadImageHostFailure{
			Host:       attempt.target.Host,
			UsageScope: attempt.target.UsageScope,
			Trackers:   remainingTrackers,
			Message:    logging.SanitizeMessage(uploadFailureMessage(attempt.err)),
		})
	}
	return api.UploadImagesResult{
		Links:       allLinks,
		Failures:    failures,
		FailedHosts: sortedMapKeys(failedHosts),
		Attempts:    attemptResults,
	}, nil
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
