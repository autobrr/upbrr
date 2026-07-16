// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package imagehosting selects, uploads, and persists release images.
package imagehosting

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/httpclient"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Service coordinates configured image-host uploaders and image persistence.
type Service struct {
	cfg       config.Config
	logger    api.Logger
	repo      repository
	client    *http.Client
	uploaders map[string]uploader
	registry  *trackers.Registry
}

type repository interface {
	ListFinalSelections(context.Context, string) ([]api.ScreenshotFinalSelection, error)
	ListScreenshotsByPath(context.Context, string) ([]api.Screenshot, error)
	ListUploadedImagesByPath(context.Context, string) ([]api.UploadedImageLink, error)
	SaveUploadedImages(context.Context, string, string, []api.UploadedImageLink) error
	ListScreenshotSlotsByPath(context.Context, string) ([]api.ScreenshotSlot, error)
	ReplaceScreenshotSlots(context.Context, string, []api.ScreenshotSlot) error
	UpsertScreenshotSlotVariants(context.Context, string, []api.ScreenshotSlotVariant) error
}

// NewServiceWithRegistry returns an image-hosting service with tracker-owned host scope metadata.
func NewServiceWithRegistry(cfg config.Config, logger api.Logger, repo repository, registry *trackers.Registry) *Service {
	if registry == nil {
		panic("image hosting: tracker registry is nil")
	}
	if logger == nil {
		logger = api.NopLogger{}
	}
	client := httpclient.New(httpclient.UploadTimeout)
	service := &Service{
		cfg:      cfg,
		logger:   logger,
		repo:     repo,
		client:   client,
		registry: registry,
	}
	service.uploaders = newUploaderRegistry(cfg, client, registry)
	return service
}

// ListCandidates returns existing local and uploaded screenshot selections for
// image hosting, deduplicated by local image path. Disc-menu selections retain
// [api.ScreenshotPurposeMenu]; all other candidates use final purpose.
func (s *Service) ListCandidates(ctx context.Context, meta api.ImageHostingSubject) ([]api.ScreenshotImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("image hosting: list candidates canceled: %w", err)
	}
	if s == nil || s.repo == nil {
		return nil, errors.New("image hosting: repository not configured")
	}
	if strings.TrimSpace(meta.SourcePath) == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	// First, get all screenshots from the database
	screens, err := s.repo.ListScreenshotsByPath(ctx, meta.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("image hosting: %w", err)
	}

	// Then, get all previously uploaded images
	uploaded, err := s.repo.ListUploadedImagesByPath(ctx, meta.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("image hosting: %w", err)
	}
	selections, err := s.repo.ListFinalSelections(ctx, meta.SourcePath)
	if err != nil {
		s.logger.Debugf("image hosting: final selections unavailable: %v", err)
		selections = nil
	}
	selectionSourceByPath := make(map[string]string, len(selections))
	for _, selection := range selections {
		selectionSourceByPath[strings.TrimSpace(selection.ImagePath)] = strings.TrimSpace(selection.Source)
	}

	// Build a map of uploaded images by path for quick lookup
	uploadedByPath := make(map[string]api.UploadedImageLink, len(uploaded))
	for _, upload := range uploaded {
		uploadedByPath[upload.ImagePath] = upload
	}

	// Build result list, merging upload info where available
	images := make([]api.ScreenshotImage, 0, len(screens))
	for _, shot := range screens {
		pathValue := strings.TrimSpace(shot.ImagePath)
		if pathValue == "" || !isAllowedImageExt(pathValue) {
			continue
		}
		info, statErr := os.Stat(pathValue)
		if statErr != nil || info.IsDir() {
			continue
		}

		img := api.ScreenshotImage{
			Path:             pathValue,
			TimestampSeconds: shot.Timestamp,
			Purpose:          shot.Purpose,
			Width:            shot.Width,
			Height:           shot.Height,
			SizeBytes:        info.Size(),
		}
		if api.IsDiscMenuSelectionSource(selectionSourceByPath[pathValue]) {
			img.Purpose = api.ScreenshotPurposeMenu
		} else if img.Purpose == "" {
			img.Purpose = api.ScreenshotPurposeFinal
		}

		// If this image has been uploaded, include the upload info
		if uploadInfo, exists := uploadedByPath[pathValue]; exists {
			img.Host = uploadInfo.Host
			img.ImgURL = uploadInfo.ImgURL
			img.RawURL = uploadInfo.RawURL
			img.WebURL = uploadInfo.WebURL
			img.UploadedAt = uploadInfo.UploadedAt
			s.logger.Tracef(
				"image hosting: found uploaded image file=%s host=%s tracker=%s",
				filepath.Base(pathValue),
				uploadInfo.Host,
				s.imageHostLogTracker(uploadInfo.Host),
			)
		}

		images = append(images, img)
	}

	// Also include final selections (like menu images that didn't go through screenshot generation)
	seenPaths := make(map[string]struct{}, len(images))
	for _, img := range images {
		seenPaths[img.Path] = struct{}{}
	}
	for _, sel := range selections {
		pathValue := strings.TrimSpace(sel.ImagePath)
		if pathValue == "" || !isAllowedImageExt(pathValue) {
			continue
		}
		if _, exists := seenPaths[pathValue]; exists {
			continue
		}
		info, statErr := os.Stat(pathValue)
		if statErr != nil || info.IsDir() {
			continue
		}

		img := api.ScreenshotImage{
			Path:             pathValue,
			TimestampSeconds: 0, // Fallback since it wasn't a generated frame
			Purpose:          api.ScreenshotPurposeFinal,
			Width:            0,
			Height:           0,
			SizeBytes:        info.Size(),
		}
		if api.IsDiscMenuSelectionSource(sel.Source) {
			img.Purpose = api.ScreenshotPurposeMenu
		}

		if uploadInfo, exists := uploadedByPath[pathValue]; exists {
			img.Host = uploadInfo.Host
			img.ImgURL = uploadInfo.ImgURL
			img.RawURL = uploadInfo.RawURL
			img.WebURL = uploadInfo.WebURL
			img.UploadedAt = uploadInfo.UploadedAt
			s.logger.Tracef(
				"image hosting: found uploaded final selection file=%s host=%s tracker=%s",
				filepath.Base(pathValue),
				uploadInfo.Host,
				s.imageHostLogTracker(uploadInfo.Host),
			)
		}

		images = append(images, img)
		seenPaths[pathValue] = struct{}{}
	}

	for _, upload := range uploaded {
		pathValue := strings.TrimSpace(upload.ImagePath)
		if pathValue == "" || !isAllowedImageExt(pathValue) {
			continue
		}
		if _, exists := seenPaths[pathValue]; exists {
			continue
		}
		info, statErr := os.Stat(pathValue)
		if statErr != nil || info.IsDir() {
			continue
		}
		purpose := api.ScreenshotPurposeFinal
		if api.IsDiscMenuSelectionSource(selectionSourceByPath[pathValue]) {
			purpose = api.ScreenshotPurposeMenu
		}
		images = append(images, api.ScreenshotImage{
			Path:       pathValue,
			Purpose:    purpose,
			SizeBytes:  info.Size(),
			Host:       upload.Host,
			ImgURL:     upload.ImgURL,
			RawURL:     upload.RawURL,
			WebURL:     upload.WebURL,
			UploadedAt: upload.UploadedAt,
		})
		seenPaths[pathValue] = struct{}{}
		s.logger.Tracef(
			"image hosting: found uploaded-only image file=%s host=%s tracker=%s",
			filepath.Base(pathValue),
			upload.Host,
			s.imageHostLogTracker(upload.Host),
		)
	}

	sort.Slice(images, func(i, j int) bool {
		return images[i].TimestampSeconds < images[j].TimestampSeconds
	})

	s.logger.Debugf("image hosting: returning candidate images count=%d uploaded=%d", len(images), len(uploadedByPath))
	return images, nil
}

// Upload validates and deduplicates local image paths, uploads them to one host,
// and persists successful links when a repository is configured. Tracker-owned
// hosts require their matching usage scope; HDB batches keep normal and
// disc-menu images in separate, non-overlapping galleries.
func (s *Service) Upload(
	ctx context.Context,
	meta api.ImageHostingSubject,
	host string,
	usageScope string,
	images []api.ScreenshotImage,
) ([]api.UploadedImageLink, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("image hosting: upload canceled: %w", err)
	}
	if s == nil {
		return nil, errors.New("image hosting: service not configured")
	}
	normalizedHost := strings.ToLower(strings.TrimSpace(host))
	if normalizedHost == "" {
		return nil, internalerrors.ErrInvalidInput
	}
	normalizedScope := strings.TrimSpace(usageScope)
	if normalizedScope == "" {
		normalizedScope = "global"
	}
	if owner := trackers.TrackerForOwnedImageHost(s.registry, normalizedHost); owner != "" {
		expectedScope := trackers.TrackerImageUsageScope(owner)
		if !strings.EqualFold(normalizedScope, expectedScope) {
			return nil, fmt.Errorf(
				"image hosting: host %q is scoped to tracker %s: expected scope %q, got %q",
				normalizedHost,
				owner,
				expectedScope,
				normalizedScope,
			)
		}
	}
	if len(images) == 0 {
		return nil, internalerrors.ErrInvalidInput
	}
	uploader := s.uploaders[normalizedHost]
	if uploader == nil {
		return nil, fmt.Errorf("image hosting: unsupported host %q", normalizedHost)
	}
	if strings.TrimSpace(meta.SourcePath) == "" {
		return nil, internalerrors.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("image hosting: upload canceled: %w", err)
	}
	logTracker := s.imageHostLogTracker(normalizedHost)

	s.logger.Infof("image hosting: uploading images count=%d host=%s tracker=%s", len(images), normalizedHost, logTracker)
	s.logger.Debugf("image hosting: upload source path=%s host=%s tracker=%s", meta.SourcePath, normalizedHost, logTracker)

	unique := make([]imageCandidate, 0, len(images))
	seen := make(map[string]struct{}, len(images))
	for _, image := range images {
		pathValue := strings.TrimSpace(image.Path)
		if pathValue == "" {
			return nil, internalerrors.ErrInvalidInput
		}
		absPath, err := filepath.Abs(pathValue)
		if err != nil {
			return nil, fmt.Errorf("image hosting: resolve image path: %w", err)
		}
		if !isAllowedImageExt(absPath) {
			return nil, internalerrors.ErrInvalidInput
		}
		if _, exists := seen[absPath]; exists {
			s.logger.Tracef("image hosting: skipping duplicate file=%s host=%s tracker=%s", filepath.Base(absPath), normalizedHost, logTracker)
			continue
		}
		info, err := os.Stat(absPath)
		if err != nil {
			return nil, fmt.Errorf("image hosting: stat image path: %w", err)
		}
		if info.IsDir() {
			return nil, internalerrors.ErrInvalidInput
		}
		seen[absPath] = struct{}{}
		unique = append(unique, imageCandidate{
			Path:      absPath,
			Purpose:   image.Purpose,
			SizeBytes: info.Size(),
		})
		s.logger.Tracef(
			"image hosting: queued file=%s host=%s tracker=%s size_kb=%.2f",
			filepath.Base(absPath),
			normalizedHost,
			logTracker,
			float64(info.Size())/1024.0,
		)
	}
	if len(unique) == 0 {
		return nil, internalerrors.ErrInvalidInput
	}
	progressTarget, ok := api.ImageUploadProgressTargetFromContext(ctx)
	if !ok {
		progressTarget = api.ImageUploadProgressTarget{
			AttemptID:  normalizedHost + "|" + normalizedScope,
			Host:       normalizedHost,
			UsageScope: normalizedScope,
			Total:      len(unique),
		}
	}
	if progressTarget.Total <= 0 {
		progressTarget.Total = progressTarget.Reused + len(unique)
	}
	emitUploadProgress(ctx, progressTarget, api.ImageUploadProgressRunning, progressTarget.Reused, 0, 0, "Uploading images.")

	s.logger.Tracef("image hosting: prepared unique images count=%d host=%s tracker=%s", len(unique), normalizedHost, logTracker)

	if batch, ok := uploader.(batchUploader); ok {
		if normalizedHost == "hdb" {
			normalCount, menuCount := imagePurposeCounts(unique)
			s.logger.Infof("image hosting: HDB gallery upload plan host=%s tracker=%s normal=%d menu=%d", normalizedHost, logTracker, normalCount, menuCount)
		}
		s.logger.Debugf("image hosting: starting batch upload host=%s tracker=%s", normalizedHost, logTracker)
		uploadedResults, err := uploadBatch(ctx, batch, meta, normalizedHost, unique)
		if err != nil {
			s.logger.Errorf("image hosting: batch upload failed host=%s tracker=%s err=%s", normalizedHost, logTracker, redaction.RedactValue(err.Error(), nil))
			emitUploadProgress(
				ctx,
				progressTarget,
				api.ImageUploadProgressFailed,
				progressTarget.Total,
				0,
				len(unique),
				"Host batch upload failed.",
			)
			return nil, err
		}
		if len(uploadedResults) != len(unique) {
			emitUploadProgress(
				ctx,
				progressTarget,
				api.ImageUploadProgressFailed,
				progressTarget.Total,
				0,
				len(unique),
				"Host returned an incomplete batch.",
			)
			return nil, fmt.Errorf("image hosting: batch upload count mismatch for %s", normalizedHost)
		}
		results := make([]api.UploadedImageLink, len(unique))
		for idx, candidate := range unique {
			uploaded := uploadedResults[idx]
			fileName := filepath.Base(candidate.Path)
			if strings.TrimSpace(uploaded.RawURL) == "" {
				emitUploadProgress(
					ctx,
					progressTarget,
					api.ImageUploadProgressFailed,
					progressTarget.Total,
					idx,
					len(unique)-idx,
					"Host returned an invalid batch result.",
				)
				return nil, fmt.Errorf("image upload %s: missing raw url", fileName)
			}
			if strings.TrimSpace(uploaded.ImgURL) == "" {
				uploaded.ImgURL = uploaded.RawURL
			}
			if strings.TrimSpace(uploaded.WebURL) == "" {
				uploaded.WebURL = uploaded.RawURL
			}
			results[idx] = api.UploadedImageLink{
				SourcePath: meta.SourcePath,
				ImagePath:  candidate.Path,
				Host:       normalizedHost,
				UsageScope: normalizedScope,
				ImgURL:     strings.TrimSpace(uploaded.ImgURL),
				RawURL:     strings.TrimSpace(uploaded.RawURL),
				WebURL:     strings.TrimSpace(uploaded.WebURL),
				SizeBytes:  candidate.SizeBytes,
				UploadedAt: time.Now().UTC(),
			}
		}
		if s.repo != nil {
			s.logger.Tracef("image hosting: persisting upload records count=%d host=%s tracker=%s", len(results), normalizedHost, logTracker)
			if err := s.repo.SaveUploadedImages(ctx, meta.SourcePath, normalizedHost, results); err != nil {
				s.logger.Errorf(
					"image hosting: failed to save upload records host=%s tracker=%s err=%s",
					normalizedHost,
					logTracker,
					redaction.RedactValue(err.Error(), nil),
				)
				emitUploadProgress(
					ctx,
					progressTarget,
					api.ImageUploadProgressFailed,
					progressTarget.Total,
					0,
					len(unique),
					"Uploaded images could not be saved.",
				)
				return nil, fmt.Errorf("image hosting: %w", err)
			}
			summary, err := syncScreenshotSlotVariants(ctx, s.repo, meta.SourcePath, results)
			if err != nil {
				s.logger.Warnf(
					"image hosting: failed to sync screenshot slot variants host=%s tracker=%s err=%s",
					normalizedHost,
					logTracker,
					redaction.RedactValue(err.Error(), nil),
				)
			} else if summary.FallbackMatched > 0 {
				s.logger.Debugf(
					"image hosting: applied ordered screenshot slot fallback host=%s tracker=%s matched=%d",
					normalizedHost,
					logTracker,
					summary.FallbackMatched,
				)
			}
		}
		s.logger.Infof("image hosting: completed batch upload count=%d host=%s tracker=%s", len(results), normalizedHost, logTracker)
		emitUploadProgress(
			ctx,
			progressTarget,
			api.ImageUploadProgressCompleted,
			progressTarget.Total,
			len(results),
			0,
			"Host upload complete.",
		)
		return results, nil
	}

	limit := s.cfg.ScreenshotHandling.MaxConcurrentUploads
	if limit <= 0 {
		limit = 1
	}

	s.logger.Infof("image hosting: starting concurrent uploads limit=%d host=%s tracker=%s", limit, normalizedHost, logTracker)
	uploadStart := time.Now()

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		progressMu sync.Mutex
		results    = make([]indexedUploadResult, 0, len(unique))
		failures   = make([]string, 0)
		completed  = progressTarget.Reused
		succeeded  int
		failed     int
	)
	sem := make(chan struct{}, limit)
	recordProgress := func(success bool) {
		progressMu.Lock()
		defer progressMu.Unlock()
		completed++
		if success {
			succeeded++
		} else {
			failed++
		}
		emitUploadProgress(
			ctx,
			progressTarget,
			api.ImageUploadProgressRunning,
			completed,
			succeeded,
			failed,
			"Uploading images.",
		)
	}

dispatchLoop:
	for idx, candidate := range unique {
		if err := ctx.Err(); err != nil {
			break
		}
		select {
		case <-ctx.Done():
			break dispatchLoop
		case sem <- struct{}{}:
		}
		if err := ctx.Err(); err != nil {
			<-sem
			break
		}
		wg.Go(func() {
			defer func() { <-sem }()
			if err := ctx.Err(); err != nil {
				mu.Lock()
				failures = append(failures, err.Error())
				mu.Unlock()
				recordProgress(false)
				return
			}
			fileName := filepath.Base(candidate.Path)
			s.logger.Debugf("image hosting: uploading image file=%s host=%s tracker=%s size_kb=%.2f", fileName, normalizedHost, logTracker, float64(candidate.SizeBytes)/1024.0)

			// Add detailed debug logging for imgbox
			if normalizedHost == "imgbox" {
				s.logger.Debugf("imgbox: starting upload for %s", fileName)
				s.logger.Tracef("imgbox: file path: %s", candidate.Path)
				s.logger.Tracef("imgbox: file size: %d bytes", candidate.SizeBytes)
			}

			uploadStartTime := time.Now()
			uploaded, err := uploader.Upload(ctx, candidate.Path)
			uploadDuration := time.Since(uploadStartTime)

			if normalizedHost == "imgbox" {
				if err != nil {
					s.logger.Debugf("imgbox: upload failed file=%s duration=%v err=%s", fileName, uploadDuration, redaction.RedactValue(err.Error(), nil))
				} else {
					s.logger.Debugf("imgbox: upload succeeded file=%s duration=%v", fileName, uploadDuration)
					s.logger.Tracef("imgbox: thumbnail URL: %s", uploaded.ImgURL)
					s.logger.Tracef("imgbox: original URL: %s", uploaded.RawURL)
					s.logger.Tracef("imgbox: image URL: %s", uploaded.WebURL)
				}
			}

			if err != nil {
				s.logger.Warnf("image hosting: upload failed file=%s host=%s tracker=%s err=%s", fileName, normalizedHost, logTracker, redaction.RedactValue(err.Error(), nil))
				mu.Lock()
				failures = append(failures, fmt.Sprintf("image upload %s: %v", fileName, err))
				mu.Unlock()
				recordProgress(false)
				return
			}
			if strings.TrimSpace(uploaded.RawURL) == "" {
				s.logger.Warnf("image hosting: missing raw URL file=%s host=%s tracker=%s", fileName, normalizedHost, logTracker)
				mu.Lock()
				failures = append(failures, fmt.Sprintf("image upload %s: missing raw url", fileName))
				mu.Unlock()
				recordProgress(false)
				return
			}
			if strings.TrimSpace(uploaded.ImgURL) == "" {
				s.logger.Tracef("image hosting: using raw URL as img URL file=%s host=%s tracker=%s", fileName, normalizedHost, logTracker)
				uploaded.ImgURL = uploaded.RawURL
			}
			if strings.TrimSpace(uploaded.WebURL) == "" {
				s.logger.Tracef("image hosting: using raw URL as web URL file=%s host=%s tracker=%s", fileName, normalizedHost, logTracker)
				uploaded.WebURL = uploaded.RawURL
			}
			mu.Lock()
			results = append(results, indexedUploadResult{
				index: idx,
				link: api.UploadedImageLink{
					SourcePath: meta.SourcePath,
					ImagePath:  candidate.Path,
					Host:       normalizedHost,
					UsageScope: normalizedScope,
					ImgURL:     strings.TrimSpace(uploaded.ImgURL),
					RawURL:     strings.TrimSpace(uploaded.RawURL),
					WebURL:     strings.TrimSpace(uploaded.WebURL),
					SizeBytes:  candidate.SizeBytes,
					UploadedAt: time.Now().UTC(),
				},
			})
			mu.Unlock()
			recordProgress(true)
			s.logger.Debugf("image hosting: successfully uploaded file=%s host=%s tracker=%s duration=%v", fileName, normalizedHost, logTracker, uploadDuration)
		})
	}

	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("image hosting: upload canceled: %w", err)
	}

	totalDuration := time.Since(uploadStart)
	orderedResults := orderedUploadResults(results)
	if len(orderedResults) > 0 {
		s.logger.Infof(
			"image hosting: completed uploads count=%d host=%s tracker=%s duration=%v avg=%v",
			len(orderedResults),
			normalizedHost,
			logTracker,
			totalDuration,
			totalDuration/time.Duration(len(orderedResults)),
		)
	}

	if s.repo != nil && len(orderedResults) > 0 {
		s.logger.Tracef("image hosting: persisting upload records count=%d host=%s tracker=%s", len(orderedResults), normalizedHost, logTracker)
		if err := s.repo.SaveUploadedImages(ctx, meta.SourcePath, normalizedHost, orderedResults); err != nil {
			s.logger.Errorf(
				"image hosting: failed to save upload records host=%s tracker=%s err=%s",
				normalizedHost,
				logTracker,
				redaction.RedactValue(err.Error(), nil),
			)
			emitUploadProgress(
				ctx,
				progressTarget,
				api.ImageUploadProgressFailed,
				completed,
				succeeded,
				failed,
				"Uploaded images could not be saved.",
			)
			return nil, fmt.Errorf("image hosting: %w", err)
		}
		summary, err := syncScreenshotSlotVariants(ctx, s.repo, meta.SourcePath, orderedResults)
		if err != nil {
			s.logger.Warnf(
				"image hosting: failed to sync screenshot slot variants host=%s tracker=%s err=%s",
				normalizedHost,
				logTracker,
				redaction.RedactValue(err.Error(), nil),
			)
		} else if summary.FallbackMatched > 0 {
			s.logger.Debugf(
				"image hosting: applied ordered screenshot slot fallback host=%s tracker=%s matched=%d",
				normalizedHost,
				logTracker,
				summary.FallbackMatched,
			)
		}
		s.logger.Debugf("image hosting: upload records persisted successfully host=%s tracker=%s", normalizedHost, logTracker)
	}

	if len(failures) > 0 {
		s.logger.Warnf(
			"image hosting: upload batch completed host=%s tracker=%s failures=%d successes=%d",
			normalizedHost,
			logTracker,
			len(failures),
			len(orderedResults),
		)
		emitUploadProgress(
			ctx,
			progressTarget,
			api.ImageUploadProgressFailed,
			completed,
			succeeded,
			failed,
			fmt.Sprintf("%d of %d host uploads failed.", failed, len(unique)),
		)
		return orderedResults, fmt.Errorf("image hosting: %d of %d uploads failed: %s", len(failures), len(unique), strings.Join(failures, "; "))
	}

	emitUploadProgress(
		ctx,
		progressTarget,
		api.ImageUploadProgressCompleted,
		completed,
		succeeded,
		0,
		"Host upload complete.",
	)
	return orderedResults, nil
}

func emitUploadProgress(
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
		Trackers:   append([]string(nil), target.Trackers...),
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

// uploadBatch dispatches candidates through the uploader's supported batch
// contract. Named HDB batches retain image purpose so gallery partitioning can
// preserve input-to-result ordering.
func uploadBatch(ctx context.Context, batch batchUploader, meta api.ImageHostingSubject, host string, images []imageCandidate) ([]uploadResult, error) {
	imagePaths := make([]string, 0, len(images))
	for _, image := range images {
		imagePaths = append(imagePaths, image.Path)
	}
	if named, ok := batch.(namedBatchUploader); ok {
		if strings.EqualFold(strings.TrimSpace(host), "hdb") {
			return uploadHDBBatchByPurpose(ctx, named, meta, images)
		}
		results, err := named.UploadBatchWithName(ctx, imagePaths, resolveGalleryName(meta))
		if err != nil {
			return nil, fmt.Errorf("image hosting: upload named batch: %w", err)
		}
		return results, nil
	}
	results, err := batch.UploadBatch(ctx, imagePaths)
	if err != nil {
		return nil, fmt.Errorf("image hosting: upload batch: %w", err)
	}
	return results, nil
}

// uploadHDBBatchByPurpose uploads normal images to the release gallery and menu
// images to a suffixed disc-menu gallery. Each candidate is uploaded once, and
// results are restored to candidate order across both batches.
func uploadHDBBatchByPurpose(ctx context.Context, batch namedBatchUploader, meta api.ImageHostingSubject, images []imageCandidate) ([]uploadResult, error) {
	type indexedImage struct {
		index int
		path  string
	}
	normal := make([]indexedImage, 0, len(images))
	menus := make([]indexedImage, 0, len(images))
	for idx, image := range images {
		item := indexedImage{index: idx, path: image.Path}
		if image.Purpose == api.ScreenshotPurposeMenu {
			menus = append(menus, item)
			continue
		}
		normal = append(normal, item)
	}

	results := make([]uploadResult, len(images))
	uploadGroup := func(group []indexedImage, galleryName string) error {
		if len(group) == 0 {
			return nil
		}
		paths := make([]string, 0, len(group))
		for _, image := range group {
			paths = append(paths, image.path)
		}
		uploaded, err := batch.UploadBatchWithName(ctx, paths, galleryName)
		if err != nil {
			return fmt.Errorf("image hosting: upload named batch: %w", err)
		}
		if len(uploaded) != len(group) {
			return fmt.Errorf("image hosting: HDB batch returned %d images for %d uploads", len(uploaded), len(group))
		}
		for idx, image := range group {
			results[image.index] = uploaded[idx]
		}
		return nil
	}

	galleryName := resolveGalleryName(meta)
	if err := uploadGroup(normal, galleryName); err != nil {
		return nil, err
	}
	if err := uploadGroup(menus, galleryName+" Disc Menus"); err != nil {
		return nil, err
	}
	return results, nil
}

func resolveGalleryName(meta api.ImageHostingSubject) string {
	for _, candidate := range []string{strings.TrimSpace(meta.GalleryName), pathutil.Base(meta.SourcePath)} {
		if candidate != "" {
			return candidate
		}
	}
	return "upbrr"
}

// imageHostLogTracker returns the tracker that owns a private image host, or
// "shared" for hosts usable outside one tracker. Logging both host and tracker
// keeps tracker-owned rehosting identifiable in operator output.
func (s *Service) imageHostLogTracker(host string) string {
	if tracker := trackers.TrackerForOwnedImageHost(s.registry, host); tracker != "" {
		return tracker
	}
	return "shared"
}

// imagePurposeCounts summarizes the HDB gallery partition without exposing
// local image paths in operator-visible progress logs.
func imagePurposeCounts(images []imageCandidate) (normal int, menus int) {
	for _, image := range images {
		if image.Purpose == api.ScreenshotPurposeMenu {
			menus++
			continue
		}
		normal++
	}
	return normal, menus
}

type imageCandidate struct {
	Path      string
	Purpose   api.ScreenshotPurpose
	SizeBytes int64
}

type indexedUploadResult struct {
	index int
	link  api.UploadedImageLink
}

func orderedUploadResults(results []indexedUploadResult) []api.UploadedImageLink {
	if len(results) == 0 {
		return nil
	}
	sort.Slice(results, func(i, j int) bool { return results[i].index < results[j].index })
	ordered := make([]api.UploadedImageLink, 0, len(results))
	for _, result := range results {
		ordered = append(ordered, result.link)
	}
	return ordered
}

func isAllowedImageExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".webp":
		return true
	default:
		return false
	}
}

func syncScreenshotSlotVariants(
	ctx context.Context,
	repo repository,
	sourcePath string,
	uploaded []api.UploadedImageLink,
) (trackers.SlotUploadAttachmentResult, error) {
	if repo == nil || len(uploaded) == 0 || strings.TrimSpace(sourcePath) == "" {
		return trackers.SlotUploadAttachmentResult{}, nil
	}
	slots, err := repo.ListScreenshotSlotsByPath(ctx, sourcePath)
	if err != nil {
		return trackers.SlotUploadAttachmentResult{}, fmt.Errorf("image hosting: %w", err)
	}
	if len(slots) == 0 {
		return trackers.SlotUploadAttachmentResult{}, nil
	}
	summary := trackers.ApplyUploadedVariantsToSlots(slots, uploaded)
	if summary.FallbackMatched > 0 {
		if err := repo.ReplaceScreenshotSlots(ctx, sourcePath, slots); err != nil {
			return summary, fmt.Errorf("image hosting: %w", err)
		}
	}
	slotByPath := make(map[string]int, len(slots))
	for _, slot := range slots {
		if pathValue := strings.TrimSpace(slot.ImagePath); pathValue != "" {
			slotByPath[pathValue] = slot.SlotOrder
		}
	}
	variants := make([]api.ScreenshotSlotVariant, 0, len(uploaded))
	for _, image := range uploaded {
		slotOrder, ok := slotByPath[strings.TrimSpace(image.ImagePath)]
		if !ok {
			continue
		}
		variants = append(variants, api.ScreenshotSlotVariant{
			SourcePath: sourcePath,
			SlotOrder:  slotOrder,
			Host:       strings.TrimSpace(image.Host),
			UsageScope: strings.TrimSpace(image.UsageScope),
			ImagePath:  strings.TrimSpace(image.ImagePath),
			ImgURL:     strings.TrimSpace(image.ImgURL),
			RawURL:     strings.TrimSpace(image.RawURL),
			WebURL:     strings.TrimSpace(image.WebURL),
			UploadedAt: image.UploadedAt,
		})
	}
	if err := repo.UpsertScreenshotSlotVariants(ctx, sourcePath, variants); err != nil {
		return summary, fmt.Errorf("image hosting: upsert screenshot slot variants: %w", err)
	}
	return summary, nil
}
