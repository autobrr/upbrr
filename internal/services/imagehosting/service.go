// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

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
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type Service struct {
	cfg       config.Config
	logger    api.Logger
	repo      api.MetadataRepository
	client    *http.Client
	uploaders map[string]uploader
}

func NewService(cfg config.Config, logger api.Logger, repo api.MetadataRepository) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	client := httpclient.New(httpclient.UploadTimeout)
	service := &Service{cfg: cfg, logger: logger, repo: repo, client: client}
	service.uploaders = newUploaderRegistry(cfg, client)
	return service
}

func (s *Service) ListCandidates(ctx context.Context, meta api.PreparedMetadata) ([]api.ScreenshotImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
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
		return nil, err
	}

	// Then, get all previously uploaded images
	uploaded, err := s.repo.ListUploadedImagesByPath(ctx, meta.SourcePath)
	if err != nil {
		return nil, err
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
			Width:            shot.Width,
			Height:           shot.Height,
			SizeBytes:        info.Size(),
		}

		// If this image has been uploaded, include the upload info
		if uploadInfo, exists := uploadedByPath[pathValue]; exists {
			img.Host = uploadInfo.Host
			img.ImgURL = uploadInfo.ImgURL
			img.RawURL = uploadInfo.RawURL
			img.WebURL = uploadInfo.WebURL
			img.UploadedAt = uploadInfo.UploadedAt
			s.logger.Tracef("image hosting: found uploaded image %s (host: %s)", filepath.Base(pathValue), uploadInfo.Host)
		}

		images = append(images, img)
	}

	sort.Slice(images, func(i, j int) bool {
		return images[i].TimestampSeconds < images[j].TimestampSeconds
	})

	s.logger.Debugf("image hosting: returning %d candidate images (%d previously uploaded)", len(images), len(uploadedByPath))
	return images, nil
}

func (s *Service) Upload(ctx context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
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
	if owner := trackers.TrackerForOwnedImageHost(normalizedHost); owner != "" {
		expectedScope := trackers.TrackerImageUsageScope(owner)
		if !strings.EqualFold(normalizedScope, expectedScope) {
			return nil, fmt.Errorf("image hosting: host %q is scoped to tracker %s: expected scope %q, got %q", normalizedHost, owner, expectedScope, normalizedScope)
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
		return nil, err
	}

	s.logger.Infof("image hosting: uploading %d images to %s", len(images), normalizedHost)
	s.logger.Debugf("image hosting: source path %s", meta.SourcePath)

	unique := make([]imageCandidate, 0, len(images))
	seen := make(map[string]struct{}, len(images))
	for _, image := range images {
		pathValue := strings.TrimSpace(image.Path)
		if pathValue == "" {
			return nil, internalerrors.ErrInvalidInput
		}
		absPath, err := filepath.Abs(pathValue)
		if err != nil {
			return nil, err
		}
		if !isAllowedImageExt(absPath) {
			return nil, internalerrors.ErrInvalidInput
		}
		if _, exists := seen[absPath]; exists {
			s.logger.Tracef("image hosting: skipping duplicate %s", filepath.Base(absPath))
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
		unique = append(unique, imageCandidate{Path: absPath, SizeBytes: info.Size()})
		s.logger.Tracef("image hosting: queued %s (%.2f KB)", filepath.Base(absPath), float64(info.Size())/1024.0)
	}
	if len(unique) == 0 {
		return nil, internalerrors.ErrInvalidInput
	}

	s.logger.Debugf("image hosting: prepared %d unique images for upload", len(unique))

	if batch, ok := uploader.(batchUploader); ok {
		paths := make([]string, 0, len(unique))
		for _, candidate := range unique {
			paths = append(paths, candidate.Path)
		}
		s.logger.Debugf("image hosting: starting batch upload to %s", normalizedHost)
		uploadedResults, err := uploadBatch(ctx, batch, meta, paths)
		if err != nil {
			s.logger.Errorf("image hosting: batch upload failed: %v", err)
			return nil, err
		}
		if len(uploadedResults) != len(unique) {
			return nil, fmt.Errorf("image hosting: batch upload count mismatch for %s", normalizedHost)
		}
		results := make([]api.UploadedImageLink, len(unique))
		for idx, candidate := range unique {
			uploaded := uploadedResults[idx]
			fileName := filepath.Base(candidate.Path)
			if strings.TrimSpace(uploaded.RawURL) == "" {
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
			s.logger.Debugf("image hosting: persisting %d upload records to database", len(results))
			if err := s.repo.SaveUploadedImages(ctx, meta.SourcePath, normalizedHost, results); err != nil {
				s.logger.Errorf("image hosting: failed to save upload records: %v", err)
				return nil, err
			}
			summary, err := syncScreenshotSlotVariants(ctx, s.repo, meta.SourcePath, results)
			if err != nil {
				s.logger.Warnf("image hosting: failed to sync screenshot slot variants: %v", err)
			} else if summary.FallbackMatched > 0 {
				s.logger.Debugf("image hosting: applied ordered screenshot slot fallback host=%s matched=%d", normalizedHost, summary.FallbackMatched)
			}
		}
		return results, nil
	}

	limit := s.cfg.ScreenshotHandling.MaxConcurrentUploads
	if limit <= 0 {
		limit = 1
	}

	s.logger.Infof("image hosting: starting concurrent uploads (limit: %d)", limit)
	uploadStart := time.Now()

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		results  = make([]indexedUploadResult, 0, len(unique))
		failures = make([]string, 0)
	)
	sem := make(chan struct{}, limit)

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
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := ctx.Err(); err != nil {
				mu.Lock()
				failures = append(failures, err.Error())
				mu.Unlock()
				return
			}
			fileName := filepath.Base(candidate.Path)
			s.logger.Debugf("image hosting: uploading %s to %s (%.2f KB)", fileName, normalizedHost, float64(candidate.SizeBytes)/1024.0)

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
					s.logger.Debugf("imgbox: upload failed for %s after %v: %v", fileName, uploadDuration, err)
				} else {
					s.logger.Debugf("imgbox: upload succeeded for %s in %v", fileName, uploadDuration)
					s.logger.Tracef("imgbox: thumbnail URL: %s", uploaded.ImgURL)
					s.logger.Tracef("imgbox: original URL: %s", uploaded.RawURL)
					s.logger.Tracef("imgbox: image URL: %s", uploaded.WebURL)
				}
			}

			if err != nil {
				s.logger.Warnf("image hosting: upload failed for %s: %v", fileName, err)
				mu.Lock()
				failures = append(failures, fmt.Sprintf("image upload %s: %v", fileName, err))
				mu.Unlock()
				return
			}
			if strings.TrimSpace(uploaded.RawURL) == "" {
				s.logger.Warnf("image hosting: missing raw URL for %s", fileName)
				mu.Lock()
				failures = append(failures, fmt.Sprintf("image upload %s: missing raw url", fileName))
				mu.Unlock()
				return
			}
			if strings.TrimSpace(uploaded.ImgURL) == "" {
				s.logger.Tracef("image hosting: using raw URL as img URL for %s", fileName)
				uploaded.ImgURL = uploaded.RawURL
			}
			if strings.TrimSpace(uploaded.WebURL) == "" {
				s.logger.Tracef("image hosting: using raw URL as web URL for %s", fileName)
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
			s.logger.Debugf("image hosting: successfully uploaded %s in %v", fileName, uploadDuration)
		}()
	}

	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	totalDuration := time.Since(uploadStart)
	orderedResults := orderedUploadResults(results)
	if len(orderedResults) > 0 {
		s.logger.Infof("image hosting: completed %d uploads to %s in %v (avg: %v per image)", len(orderedResults), normalizedHost, totalDuration, totalDuration/time.Duration(len(orderedResults)))
	}

	if s.repo != nil && len(orderedResults) > 0 {
		s.logger.Debugf("image hosting: persisting %d upload records to database", len(orderedResults))
		if err := s.repo.SaveUploadedImages(ctx, meta.SourcePath, normalizedHost, orderedResults); err != nil {
			s.logger.Errorf("image hosting: failed to save upload records: %v", err)
			return nil, err
		}
		summary, err := syncScreenshotSlotVariants(ctx, s.repo, meta.SourcePath, orderedResults)
		if err != nil {
			s.logger.Warnf("image hosting: failed to sync screenshot slot variants: %v", err)
		} else if summary.FallbackMatched > 0 {
			s.logger.Debugf("image hosting: applied ordered screenshot slot fallback host=%s matched=%d", normalizedHost, summary.FallbackMatched)
		}
		s.logger.Debugf("image hosting: upload records persisted successfully")
	}

	if len(failures) > 0 {
		s.logger.Warnf("image hosting: upload batch completed with %d failures and %d successes", len(failures), len(orderedResults))
		return orderedResults, fmt.Errorf("image hosting: %d of %d uploads failed: %s", len(failures), len(unique), strings.Join(failures, "; "))
	}

	return orderedResults, nil
}

func uploadBatch(ctx context.Context, batch batchUploader, meta api.PreparedMetadata, imagePaths []string) ([]uploadResult, error) {
	if named, ok := batch.(namedBatchUploader); ok {
		return named.UploadBatchWithName(ctx, imagePaths, resolveGalleryName(meta))
	}
	return batch.UploadBatch(ctx, imagePaths)
}

func resolveGalleryName(meta api.PreparedMetadata) string {
	for _, candidate := range []string{
		strings.TrimSpace(meta.ReleaseName),
		strings.TrimSpace(meta.ReleaseNameNoTag),
		strings.TrimSpace(meta.Release.Title),
		strings.TrimSpace(meta.Filename),
		pathutil.Base(meta.SourcePath),
	} {
		if candidate != "" {
			return candidate
		}
	}
	return "upbrr"
}

type imageCandidate struct {
	Path      string
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

func syncScreenshotSlotVariants(ctx context.Context, repo api.MetadataRepository, sourcePath string, uploaded []api.UploadedImageLink) (trackers.SlotUploadAttachmentResult, error) {
	if repo == nil || len(uploaded) == 0 || strings.TrimSpace(sourcePath) == "" {
		return trackers.SlotUploadAttachmentResult{}, nil
	}
	slots, err := repo.ListScreenshotSlotsByPath(ctx, sourcePath)
	if err != nil || len(slots) == 0 {
		return trackers.SlotUploadAttachmentResult{}, err
	}
	summary := trackers.ApplyUploadedVariantsToSlots(slots, uploaded)
	if summary.FallbackMatched > 0 {
		if err := repo.ReplaceScreenshotSlots(ctx, sourcePath, slots); err != nil {
			return summary, err
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
	return summary, repo.UpsertScreenshotSlotVariants(ctx, sourcePath, variants)
}
