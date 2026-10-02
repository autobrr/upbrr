// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path" //nolint:depguard // Extracts extensions from URL paths.
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

var descriptionURLPattern = regexp.MustCompile(`(?i)https?://[^\s\[\]<>]+`)

// rehostSourceOnlyDescriptionImages replaces image URLs that cannot travel
// between trackers, including image links inside intact comparison blocks.
func (s *Service) rehostSourceOnlyDescriptionImages(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	trackerCfg config.TrackerConfig,
	assets *DescriptionAssets,
	preloaded *preloadedDescriptionAssetData,
) error {
	if assets == nil {
		return nil
	}
	urls := sourceOnlyDescriptionImageURLs(assets.Description)
	reuseSourceURL := sourceOnlyImageReusePolicy(s.registry, tracker, meta, preloaded)
	urls = slices.DeleteFunc(urls, func(rawURL string) bool {
		return !imagehost.IsWsrvProxyURL(rawURL) && reuseSourceURL(rawURL)
	})
	menuIndices := make([]int, 0)
	if trackerUsesMenuImages(s.registry, tracker) {
		for index, menu := range assets.MenuImages {
			if strings.TrimSpace(menu.Path) != "" &&
				(sourceOnlyScreenshotImage(menu) || strings.TrimSpace(menu.ImgURL) == "" && strings.TrimSpace(menu.RawURL) == "") {
				menuIndices = append(menuIndices, index)
			}
		}
	}
	if len(urls) == 0 && len(menuIndices) == 0 {
		return nil
	}
	policy, err := resolveImageHostPolicyForMetadataWithRegistry(s.registry, tracker, s.cfg, trackerCfg, meta.ImageHostOverrides)
	if err != nil {
		return err
	}
	directReplacements := make(map[string]string)
	urls = slices.DeleteFunc(urls, func(rawURL string) bool {
		if !imagehost.IsWsrvProxyURL(rawURL) {
			return false
		}
		directURL := imagehost.DirectImageURL(rawURL)
		host := imagehost.ExtractHost(directURL)
		if directURL == "" || host == "" || hostInList(host, policy.failed) ||
			len(policy.allowed) > 0 && !hostAllowed(host, policy.allowed) ||
			!reusableSelectionMatchesPolicy(host, policy) ||
			(imagehost.IsSourceOnlyURL(directURL) && !reuseSourceURL(directURL)) {
			return reuseSourceURL(rawURL)
		}
		if owner := trackerForOwnedHost(s.registry, host); owner != "" && !strings.EqualFold(owner, tracker) {
			return reuseSourceURL(rawURL)
		}
		directReplacements[rawURL] = directURL
		return true
	})
	if len(urls) == 0 && len(menuIndices) == 0 {
		assets.Description = rewriteSourceOnlyDescriptionImageURLs(assets.Description, directReplacements)
		return nil
	}
	if s.images == nil || s.repo == nil || !meta.MediaBinding.Valid() || imageHostUploadSkipped(meta) && meta.ExactMedia == nil {
		return fmt.Errorf("trackers: %s description has %d images needing hosting and image hosting is unavailable", tracker, len(urls)+len(menuIndices))
	}
	if !policy.required {
		policy = sourceOnlyImageUploadPolicy(s.registry, s.cfg, tracker, policy)
	}
	hosts := uploadAttemptHosts(policy)
	if len(hosts) == 0 {
		return fmt.Errorf("trackers: %s description has %d images needing hosting but no image upload host is configured", tracker, len(urls)+len(menuIndices))
	}
	tmpRoot, err := dbsvc.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return fmt.Errorf("trackers: description image temp root: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return fmt.Errorf("trackers: description image release dir: %w", err)
	}
	imageDir := filepath.Join(tmpDir, "description-images")
	if err := os.MkdirAll(imageDir, 0o700); err != nil {
		return fmt.Errorf("trackers: description image dir: %w", err)
	}
	client := newDescriptionSlotImageHTTPClient()
	if slices.ContainsFunc(urls, func(rawURL string) bool {
		return isPTPDescriptionImageURL(imagehost.DirectImageURL(rawURL))
	}) {
		client = PTPDescriptionImageHTTPClient(ctx, client, s.cfg, s.logger)
	}
	images := make([]api.ScreenshotImage, 0, len(urls)+len(menuIndices))
	for index, rawURL := range urls {
		digest := sha256.Sum256([]byte(rawURL))
		name := buildDescriptionSlotImageName(rawURL, index)
		pathValue := filepath.Join(imageDir, fmt.Sprintf("source_%x_%s", digest[:6], name))
		if preferredURL := preferredFullSizeSourceURL(rawURL); preferredURL != "" {
			fullPath := filepath.Join(imageDir, fmt.Sprintf("full_%x_%s", digest[:6], name))
			if info, statErr := os.Stat(fullPath); statErr == nil && !info.IsDir() && info.Size() > 0 {
				images = append(images, api.ScreenshotImage{Index: index, Path: fullPath})
				continue
			}
			if err := downloadDescriptionSlotImage(ctx, client, preferredURL, fullPath); err == nil {
				images = append(images, api.ScreenshotImage{Index: index, Path: fullPath})
				continue
			} else if s.logger != nil {
				s.logger.Debugf(
					"trackers: full-size source image fallback tracker=%s image=%d reason=%s",
					tracker,
					index+1,
					descriptionSlotImageFailureReason(err),
				)
			}
		}
		if cached := cachedSourceDescriptionImagePath(tmpDir, rawURL, meta, preloaded); cached != "" {
			if uploadable := uploadableCachedSourceImagePath(cached, pathValue); uploadable != "" {
				images = append(images, api.ScreenshotImage{Index: index, Path: uploadable})
				continue
			}
		}
		if info, statErr := os.Stat(pathValue); statErr != nil || info.IsDir() || info.Size() == 0 {
			if err := downloadDescriptionSlotImage(ctx, client, rawURL, pathValue); err != nil {
				if s.logger != nil {
					s.logger.Warnf(
						"trackers: source-only description image download failed tracker=%s image=%d reason=%s",
						tracker, index+1, descriptionSlotImageFailureReason(err),
					)
				}
				return fmt.Errorf("trackers: %s description image %d could not be downloaded: %s", tracker, index+1, descriptionSlotImageFailureReason(err))
			}
		}
		images = append(images, api.ScreenshotImage{Index: index, Path: pathValue})
	}
	for _, menuIndex := range menuIndices {
		menu := assets.MenuImages[menuIndex]
		info, statErr := os.Stat(menu.Path)
		if statErr != nil || info.IsDir() || info.Size() == 0 {
			return fmt.Errorf("trackers: %s DVD menu image %d has no usable local file", tracker, menuIndex+1)
		}
		images = append(images, api.ScreenshotImage{Index: len(images), Path: menu.Path})
	}
	if s.logger != nil {
		s.logger.Debugf("trackers: source-only description rehost decision tracker=%s images=%d candidate_hosts=%d", tracker, len(images), len(hosts))
	}
	uploads, err := uploadedImagesFromSource(ctx, meta, s.repo, preloaded)
	if err != nil && !errorsIsNotFound(err) {
		return err
	}
	var lastErr error
	for _, host := range hosts {
		scope := usageScopeForHost(s.registry, host)
		byPath := make(map[string]api.UploadedImageLink, len(images))
		for _, upload := range uploads {
			if strings.EqualFold(upload.Host, host) && normalizeUsageScope(upload.UsageScope) == scope &&
				!sourceOnlyUploadedImage(upload) && strings.TrimSpace(upload.RawURL) != "" {
				byPath[upload.ImagePath] = upload
			}
		}
		missing := make([]api.ScreenshotImage, 0, len(images))
		for _, image := range images {
			if _, ok := byPath[image.Path]; !ok {
				missing = append(missing, image)
			}
		}
		var uploadedNow []api.UploadedImageLink
		if len(missing) > 0 {
			uploaded, uploadErr := s.images.Upload(ctx, imageHostingSubject(meta), host, scope, missing)
			if uploadErr != nil {
				cleanupUploadedImages(ctx, s.repo, meta.MediaBinding, uploaded, s.logger)
				lastErr = uploadErr
				if s.logger != nil {
					s.logger.Warnf(
						"trackers: source-only description image upload failed tracker=%s host=%s count=%d reason=%s",
						tracker,
						host,
						len(missing),
						safeTrackerMessage(uploadErr),
					)
				}
				continue
			}
			uploadedNow = uploaded
			for _, upload := range uploaded {
				if !sourceOnlyUploadedImage(upload) && strings.TrimSpace(upload.RawURL) != "" {
					byPath[upload.ImagePath] = upload
				}
			}
		}
		replacements := maps.Clone(directReplacements)
		complete := true
		for index, rawURL := range urls {
			image := images[index]
			upload, ok := byPath[image.Path]
			if !ok || strings.TrimSpace(upload.RawURL) == "" {
				lastErr = errors.New("image host returned incomplete description images")
				complete = false
				break
			}
			replacements[rawURL] = upload.RawURL
		}
		menuUploads := make(map[int]api.UploadedImageLink, len(menuIndices))
		if complete {
			for _, menuIndex := range menuIndices {
				upload, ok := byPath[assets.MenuImages[menuIndex].Path]
				if !ok || strings.TrimSpace(upload.RawURL) == "" {
					lastErr = errors.New("image host returned incomplete DVD menu images")
					complete = false
					break
				}
				menuUploads[menuIndex] = upload
			}
		}
		if !complete {
			cleanupUploadedImages(ctx, s.repo, meta.MediaBinding, uploadedNow, s.logger)
			continue
		}
		if preloaded != nil {
			preloaded.uploads = append(preloaded.uploads, uploadedNow...)
		}
		assets.Description = rewriteSourceOnlyDescriptionImageURLs(assets.Description, replacements)
		for menuIndex, upload := range menuUploads {
			assets.MenuImages[menuIndex].Host = upload.Host
			assets.MenuImages[menuIndex].ImgURL = upload.ImgURL
			assets.MenuImages[menuIndex].RawURL = upload.RawURL
			assets.MenuImages[menuIndex].WebURL = upload.WebURL
			assets.MenuImages[menuIndex].UploadedAt = upload.UploadedAt
		}
		if s.logger != nil {
			s.logger.Infof("trackers: source-only description images rehosted tracker=%s host=%s count=%d", tracker, host, len(images))
		}
		return nil
	}
	return fmt.Errorf("trackers: %s description images could not be rehosted: %w", tracker, lastErr)
}

func trackerUsesMenuImages(registry *Registry, tracker string) bool {
	if registry == nil {
		return false
	}
	definition, ok := registry.Lookup(tracker)
	if !ok {
		return false
	}
	consumer, ok := definition.(interface{ UsesMenuImages() bool })
	return ok && consumer.UsesMenuImages()
}

func sourceOnlyImageReusableOnTracker(registry *Registry, tracker string, rawURL string, records []api.TrackerMetadata) bool {
	if registry == nil {
		return false
	}
	definition, ok := registry.Lookup(tracker)
	if !ok {
		return false
	}
	provider, ok := definition.(interface {
		SourceOnlyImageReusable(string, []api.TrackerMetadata) bool
	})
	return ok && provider.SourceOnlyImageReusable(rawURL, records)
}

func sourceOnlyImageReusePolicy(registry *Registry, tracker string, meta api.UploadSubject, preloaded *preloadedDescriptionAssetData) func(string) bool {
	records := meta.TrackerData
	if preloaded != nil && len(preloaded.trackerRecords) > 0 {
		records = preloaded.trackerRecords
	}
	return func(rawURL string) bool {
		return sourceOnlyImageReusableOnTracker(registry, tracker, rawURL, records)
	}
}

func preferredFullSizeSourceURL(rawURL string) string {
	if !imagehost.IsWsrvProxyURL(rawURL) {
		return ""
	}
	return imagehost.DirectImageURL(rawURL)
}

func cachedSourceDescriptionImagePath(tmpDir string, rawURL string, meta api.UploadSubject, preloaded *preloadedDescriptionAssetData) string {
	records := meta.TrackerData
	if preloaded != nil && len(preloaded.trackerRecords) > 0 {
		records = preloaded.trackerRecords
	}
	for _, record := range records {
		dir := filepath.Join(tmpDir, sanitizePersistedTrackerArtifactName(strings.ToLower(record.Tracker)))
		for index, candidateURL := range record.ImageURLs {
			if candidateURL != rawURL {
				continue
			}
			for _, candidate := range localTrackerArtifactPaths(dir, candidateURL, index) {
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Size() > 0 {
					return candidate
				}
			}
		}
	}
	return ""
}

func uploadableCachedSourceImagePath(cached string, destination string) string {
	switch strings.ToLower(filepath.Ext(cached)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".avif":
		return cached
	}
	info, err := os.Stat(cached)
	if err != nil || info.Size() == 0 || info.Size() > descriptionSlotImageMaxBytes {
		return ""
	}
	payload, err := os.ReadFile(cached)
	if err != nil {
		return ""
	}
	ext := ""
	switch http.DetectContentType(payload) {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/webp":
		ext = ".webp"
	}
	if ext == "" && isAVIFImagePayload(payload) {
		ext = ".avif"
	}
	if ext == "" {
		return ""
	}
	pathValue := strings.TrimSuffix(destination, filepath.Ext(destination)) + ext
	// #nosec G703 -- destination is built from a sanitized filename under the release temp directory.
	if err := os.WriteFile(
		pathValue,
		payload,
		0o600,
	); err != nil {
		return ""
	}
	return pathValue
}

// sourceOnlyDescriptionImageURLs finds non-portable image links in image tags
// and bare image URLs while leaving ordinary text links alone.
func sourceOnlyDescriptionImageURLs(description string) []string {
	seen := make(map[string]struct{})
	urls := make([]string, 0)
	imageTags := make(map[string]struct{})
	for _, match := range slotImgPattern.FindAllStringSubmatch(description, -1) {
		imageTags[strings.TrimSpace(match[1])] = struct{}{}
	}
	for _, match := range slotURLImgPattern.FindAllStringSubmatch(description, -1) {
		if imageReferenceURL(match[1]) || imagehost.WsrvSourceURL(match[1]) != "" {
			imageTags[strings.TrimSpace(match[1])] = struct{}{}
		}
	}
	for _, candidate := range descriptionURLPattern.FindAllString(description, -1) {
		candidate = strings.TrimRight(candidate, ".,;:!)")
		_, inImageTag := imageTags[candidate]
		if !imagehost.IsSourceOnlyURL(candidate) || !inImageTag && !imageReferenceURL(candidate) {
			continue
		}
		if _, ok := seen[candidate]; !ok {
			seen[candidate] = struct{}{}
			urls = append(urls, candidate)
		}
	}
	return urls
}

func imageReferenceURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	if imageFilePathExtension(parsed.Path) {
		return true
	}
	if source := imagehost.WsrvSourceURL(rawURL); source != "" {
		sourceURL, err := url.Parse(source)
		return err == nil && imageFilePathExtension(sourceURL.Path)
	}
	return false
}

func imageFilePathExtension(pathValue string) bool {
	switch strings.ToLower(path.Ext(pathValue)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp":
		return true
	}
	return false
}

func rewriteSourceOnlyDescriptionImageURLs(description string, replacements map[string]string) string {
	keys := make([]string, 0, len(replacements))
	for original := range replacements {
		keys = append(keys, original)
	}
	slices.SortFunc(keys, func(left, right string) int {
		if len(left) != len(right) {
			return len(right) - len(left)
		}
		return strings.Compare(left, right)
	})
	for _, original := range keys {
		description = strings.ReplaceAll(description, original, replacements[original])
	}
	return description
}
