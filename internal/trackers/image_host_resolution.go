// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path" //nolint:depguard // Extracts URL path components from image host URLs.
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/paths"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/imagehost"
	"github.com/autobrr/upbrr/pkg/api"
)

type descriptionImageHostResolution struct {
	screenshots []api.ScreenshotImage
	feedback    api.ImageHostFeedback
	usageScope  string
	blocking    bool
}

const (
	descriptionSlotImageTimeout  = 30 * time.Second
	descriptionSlotImageMaxBytes = 25 * 1024 * 1024
)

func ensureDescriptionImageHost(
	ctx context.Context,
	tracker string,
	meta api.PreparedMetadata,
	appCfg config.Config,
	trackerCfg config.TrackerConfig,
	repo api.MetadataRepository,
	images api.ImageHostingService,
	logger api.Logger,
) (descriptionImageHostResolution, error) {
	return ensureDescriptionImageHostWithData(ctx, tracker, meta, appCfg, trackerCfg, repo, images, logger, nil)
}

func ensureDescriptionImageHostWithData(
	ctx context.Context,
	tracker string,
	meta api.PreparedMetadata,
	appCfg config.Config,
	trackerCfg config.TrackerConfig,
	repo api.MetadataRepository,
	images api.ImageHostingService,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
	preferredHosts ...string,
) (descriptionImageHostResolution, error) {
	policy, err := resolveImageHostPolicy(tracker, trackerCfg, meta.ImageHostOverrides)
	if err != nil {
		return descriptionImageHostResolution{}, err
	}
	feedback := api.ImageHostFeedback{
		Status:       "reused",
		AllowedHosts: append([]string{}, policy.allowed...),
	}
	if repo == nil || strings.TrimSpace(meta.SourcePath) == "" {
		return descriptionImageHostResolution{feedback: feedback}, nil
	}

	slots, err := screenshotSlotsFromSource(ctx, tracker, meta, repo, logger, preloaded)
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: image host resolution screenshot slots failed tracker=%s: %v", tracker, err)
		}
		slots = nil
	}
	localTrackerImages := resolveLocalTrackerScreenshots(meta, appCfg, tracker, logger)
	if detachComparisonSourceImagesFromSlots(slots, localTrackerImages) {
		if err := repo.ReplaceScreenshotSlots(ctx, meta.SourcePath, slots); err != nil {
			return descriptionImageHostResolution{}, fmt.Errorf("trackers: %w", err)
		}
		syncSlotsToPreloaded(preloaded, slots)
	}

	if len(policy.allowed) == 0 {
		selectionPolicy := imageHostPolicy{}
		if host := firstPreferredDescriptionImageHost(preferredHosts); host != "" {
			selectionPolicy.preferred = []string{host}
		}
		screenshots, host, usageScope, err := selectScreenshotsFromSlots(tracker, slots, selectionPolicy)
		if err != nil {
			return descriptionImageHostResolution{}, err
		}
		if len(screenshots) > 0 {
			feedback.SelectedHost = host
			feedback.Message = buildReuseMessage(tracker, host, usageScope, false)
			return descriptionImageHostResolution{screenshots: screenshots, feedback: feedback, usageScope: usageScope}, nil
		}
		urls := resolveTrackerImageURLs(ctx, tracker, meta, repo, logger, preloaded)
		screenshots = resolveTrackerScreenshots(urls)
		if len(screenshots) > 0 {
			feedback.SelectedHost = strings.ToLower(strings.TrimSpace(screenshots[0].Host))
			feedback.Message = buildReuseMessage(tracker, feedback.SelectedHost, globalImageUsageScope, false)
		}
		return descriptionImageHostResolution{screenshots: screenshots, feedback: feedback, usageScope: globalImageUsageScope}, nil
	}

	if screenshots, host, usageScope, err := selectScreenshotsFromSlots(tracker, slots, policy); err == nil && len(screenshots) > 0 {
		feedback.SelectedHost = host
		feedback.Message = buildReuseMessage(tracker, host, usageScope, host != preferredHost(policy))
		return descriptionImageHostResolution{screenshots: screenshots, feedback: feedback, usageScope: usageScope}, nil
	} else if err != nil && allRenderableSlotsHaveEligibleVariant(slots, tracker, policy) {
		return descriptionImageHostResolution{}, err
	}

	sourceImages := slotSourceImagesForRehost(slots)
	if len(sourceImages) == 0 {
		sourceImages = localTrackerImages
		if len(sourceImages) > 0 && !slotsContainComparison(slots) && alignRenderableSlotsToSourceImages(slots, sourceImages) {
			if err := repo.ReplaceScreenshotSlots(ctx, meta.SourcePath, slots); err != nil {
				return descriptionImageHostResolution{}, fmt.Errorf("trackers: %w", err)
			}
			syncSlotsToPreloaded(preloaded, slots)
		}
	}
	if len(slots) > 0 {
		changed := attachMatchingSourceImagesToSlots(slots, localTrackerImages)
		if appendSourceImageSlots(&slots, meta.SourcePath, localTrackerImages) {
			changed = true
		}
		if materialized, materializeChanged := materializeDescriptionSlotImages(ctx, meta, appCfg, tracker, slots, logger); len(materialized) > 0 || materializeChanged {
			changed = changed || materializeChanged
		}
		if changed {
			if err := repo.ReplaceScreenshotSlots(ctx, meta.SourcePath, slots); err != nil {
				return descriptionImageHostResolution{}, fmt.Errorf("trackers: %w", err)
			}
			syncSlotsToPreloaded(preloaded, slots)
		}
		sourceImages = slotSourceImagesForRehost(slots)
	}
	if len(sourceImages) == 0 {
		urls := resolveTrackerImageURLs(ctx, tracker, meta, repo, logger, preloaded)
		if screenshots, host := resolveTrackerScreenshotsForAllowedHost(urls, policy); len(screenshots) > 0 {
			feedback.SelectedHost = host
			feedback.Message = buildReuseMessage(tracker, host, globalImageUsageScope, host != preferredHost(policy))
			return descriptionImageHostResolution{screenshots: screenshots, feedback: feedback, usageScope: globalImageUsageScope}, nil
		}
		feedback.Status = "warning"
		feedback.Message = fmt.Sprintf("%s requires screenshots from %s, but no local screenshots are available to rehost.", tracker, strings.Join(policy.allowed, ", "))
		return descriptionImageHostResolution{feedback: feedback}, nil
	}

	for _, host := range uploadAttemptHosts(policy) {
		usageScope := usageScopeForHost(host)
		screenshots, err := reusableUploadedScreenshotsForHost(ctx, tracker, meta, repo, preloaded, host, usageScope, sourceImages)
		if err != nil {
			return descriptionImageHostResolution{}, err
		}
		if len(screenshots) > 0 {
			feedback.SelectedHost = host
			feedback.Message = buildReuseMessage(tracker, host, usageScope, host != preferredHost(policy))
			return descriptionImageHostResolution{screenshots: screenshots, feedback: feedback, usageScope: usageScope}, nil
		}
	}

	if images == nil {
		feedback.Status = "warning"
		feedback.Message = fmt.Sprintf("%s requires screenshots from %s, but image hosting is unavailable.", tracker, strings.Join(policy.allowed, ", "))
		return descriptionImageHostResolution{feedback: feedback}, nil
	}
	if meta.ImageHostOverrides.SkipUpload != nil && *meta.ImageHostOverrides.SkipUpload {
		feedback.Status = "warning"
		feedback.Message = fmt.Sprintf("%s requires screenshots from %s, but automatic image-host uploads are disabled.", tracker, strings.Join(policy.allowed, ", "))
		return descriptionImageHostResolution{feedback: feedback}, nil
	}

	var lastErr error
	for _, host := range uploadAttemptHosts(policy) {
		usageScope := usageScopeForHost(host)
		uploaded, err := images.Upload(ctx, meta, host, usageScope, sourceImages)
		if err != nil {
			lastErr = err
			if len(uploaded) > 0 {
				cleanupUploadedImages(ctx, repo, meta.SourcePath, uploaded, logger)
			}
			feedback.Warnings = append(feedback.Warnings, api.ImageHostWarning{
				Host:    host,
				Message: err.Error(),
			})
			if logger != nil {
				logger.Warnf("trackers: image host upload failed tracker=%s host=%s: %v", tracker, host, err)
			}
			continue
		}
		candidateSlots := cloneScreenshotSlots(slots)
		summary := applyUploadedVariantsToSlots(candidateSlots, uploaded)
		if summary.FallbackMatched > 0 && logger != nil {
			logger.Debugf("trackers: image host resolution applied ordered slot fallback tracker=%s host=%s matched=%d", tracker, host, summary.FallbackMatched)
		}
		screenshots, _, _, err := selectScreenshotsFromSlots(tracker, candidateSlots, policy)
		if err != nil {
			cleanupUploadedImages(ctx, repo, meta.SourcePath, uploaded, logger)
			return descriptionImageHostResolution{}, err
		}
		if len(screenshots) == 0 {
			cleanupUploadedImages(ctx, repo, meta.SourcePath, uploaded, logger)
			message := "upload did not produce usable screenshots"
			feedback.Warnings = append(feedback.Warnings, api.ImageHostWarning{
				Host:    host,
				Message: message,
			})
			if logger != nil {
				logger.Warnf("trackers: image host upload produced no usable screenshots tracker=%s host=%s", tracker, host)
			}
			continue
		}
		if err := upsertScreenshotVariantsFromUploads(ctx, repo, meta.SourcePath, slots, uploaded); err != nil {
			cleanupUploadedImages(ctx, repo, meta.SourcePath, uploaded, logger)
			return descriptionImageHostResolution{}, err
		}
		applyUploadedVariantsToSlots(slots, uploaded)
		syncSlotVariantsToPreloaded(preloaded, uploaded)
		feedback.Status = "reuploaded"
		feedback.SelectedHost = host
		feedback.Reuploaded = true
		if usageScope == globalImageUsageScope {
			feedback.Message = uploadSuccessMessage(tracker, host, "", feedback.Warnings)
		} else {
			feedback.Message = uploadSuccessMessage(tracker, host, usageScope, feedback.Warnings)
		}
		return descriptionImageHostResolution{screenshots: screenshots, feedback: feedback, usageScope: usageScope}, nil
	}

	feedback.Status = "warning"
	attemptHosts := strings.Join(uploadAttemptHosts(policy), ", ")
	if attemptHosts == "" {
		attemptHosts = "none"
	}
	if lastErr != nil {
		feedback.Message = fmt.Sprintf("%s could not upload screenshots to an allowed upload host (%s): %v", tracker, attemptHosts, lastErr)
	} else {
		feedback.Message = fmt.Sprintf("%s could not find an allowed screenshot host to upload to (%s).", tracker, attemptHosts)
	}
	return descriptionImageHostResolution{feedback: feedback, blocking: true}, nil
}

func firstPreferredDescriptionImageHost(hosts []string) string {
	for _, host := range hosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized != "" {
			return normalized
		}
	}
	return ""
}

func reusableUploadedScreenshotsForHost(
	ctx context.Context,
	tracker string,
	meta api.PreparedMetadata,
	repo api.MetadataRepository,
	preloaded *preloadedDescriptionAssetData,
	host string,
	usageScope string,
	sourceImages []api.ScreenshotImage,
) ([]api.ScreenshotImage, error) {
	if repo == nil || len(sourceImages) == 0 {
		return nil, nil
	}
	uploads, err := uploadedImagesFromSource(ctx, meta, repo, preloaded)
	if err != nil {
		if errorsIsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	byPath := make(map[string]api.UploadedImageLink, len(uploads))
	for _, upload := range uploads {
		if !strings.EqualFold(strings.TrimSpace(upload.Host), strings.TrimSpace(host)) {
			continue
		}
		if !strings.EqualFold(normalizeUsageScope(upload.UsageScope), normalizeUsageScope(usageScope)) {
			continue
		}
		if !uploadEligibleForTracker(upload.UsageScope, tracker) {
			continue
		}
		pathValue := strings.TrimSpace(upload.ImagePath)
		if pathValue == "" || strings.TrimSpace(upload.ImgURL) == "" {
			continue
		}
		byPath[pathValue] = upload
	}
	if len(byPath) == 0 {
		return nil, nil
	}
	screenshots := make([]api.ScreenshotImage, 0, len(sourceImages))
	for _, image := range sourceImages {
		pathValue := strings.TrimSpace(image.Path)
		if pathValue == "" {
			return nil, nil
		}
		upload, ok := byPath[pathValue]
		if !ok {
			return nil, nil
		}
		screenshots = append(screenshots, api.ScreenshotImage{
			Index:  image.Index,
			Path:   pathValue,
			Host:   strings.TrimSpace(upload.Host),
			ImgURL: strings.TrimSpace(upload.ImgURL),
			RawURL: strings.TrimSpace(upload.RawURL),
			WebURL: strings.TrimSpace(upload.WebURL),
		})
	}
	return screenshots, nil
}

func cleanupUploadedImages(ctx context.Context, repo api.MetadataRepository, sourcePath string, uploaded []api.UploadedImageLink, logger api.Logger) {
	if repo == nil || len(uploaded) == 0 || strings.TrimSpace(sourcePath) == "" {
		return
	}
	seen := make(map[string]struct{}, len(uploaded))
	for _, image := range uploaded {
		pathValue := strings.TrimSpace(image.ImagePath)
		hostValue := strings.TrimSpace(image.Host)
		if pathValue == "" || hostValue == "" {
			continue
		}
		key := hostValue + "\x00" + pathValue
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := repo.DeleteUploadedImage(ctx, sourcePath, pathValue, hostValue); err != nil && logger != nil {
			logger.Warnf("trackers: failed to roll back uploaded image tracker=%s host=%s path=%s: %v", strings.TrimSpace(sourcePath), hostValue, pathValue, err)
		}
	}
}

func preferredHost(policy imageHostPolicy) string {
	if len(policy.preferred) == 0 {
		return ""
	}
	return policy.preferred[0]
}

func buildReuseMessage(tracker string, host string, usageScope string, fallback bool) string {
	if strings.TrimSpace(host) == "" {
		return ""
	}
	if normalizeUsageScope(usageScope) == trackerImageUsageScope(tracker) {
		return fmt.Sprintf("Using tracker-scoped %s screenshots for %s.", host, tracker)
	}
	if fallback {
		return fmt.Sprintf("Using allowed fallback host %s for %s.", host, tracker)
	}
	return fmt.Sprintf("Using %s screenshots for %s.", host, tracker)
}

func uploadSuccessMessage(tracker string, host string, usageScope string, warnings []api.ImageHostWarning) string {
	failedHosts := failedImageHostNames(warnings)
	if normalizeUsageScope(usageScope) == trackerImageUsageScope(tracker) {
		if len(failedHosts) > 0 {
			return fmt.Sprintf("Uploaded tracker-scoped screenshots to %s for %s after %s failed.", host, tracker, strings.Join(failedHosts, ", "))
		}
		return fmt.Sprintf("Uploaded tracker-scoped screenshots to %s for %s.", host, tracker)
	}
	if len(failedHosts) > 0 {
		return fmt.Sprintf("Uploaded screenshots to fallback host %s for %s after %s failed.", host, tracker, strings.Join(failedHosts, ", "))
	}
	return fmt.Sprintf("Uploaded screenshots to %s for %s image-host requirements.", host, tracker)
}

func failedImageHostNames(warnings []api.ImageHostWarning) []string {
	if len(warnings) == 0 {
		return nil
	}
	hosts := make([]string, 0, len(warnings))
	seen := make(map[string]struct{}, len(warnings))
	for _, warning := range warnings {
		host := strings.ToLower(strings.TrimSpace(warning.Host))
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	return hosts
}

func uploadAttemptHosts(policy imageHostPolicy) []string {
	if len(policy.preferred) == 0 {
		return nil
	}
	allowedUploads := make(map[string]struct{}, len(policy.uploadHosts))
	for _, host := range policy.uploadHosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized != "" {
			allowedUploads[normalized] = struct{}{}
		}
	}
	out := make([]string, 0, len(policy.preferred))
	seen := make(map[string]struct{}, len(policy.preferred))
	for _, host := range policy.preferred {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized == "" {
			continue
		}
		if _, ok := allowedUploads[normalized]; !ok {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func resolveTrackerScreenshotsForAllowedHost(urls []string, policy imageHostPolicy) ([]api.ScreenshotImage, string) {
	if len(urls) == 0 {
		return nil, ""
	}
	for _, host := range policy.preferred {
		filtered := make([]api.ScreenshotImage, 0, len(urls))
		for _, rawURL := range urls {
			trimmed := strings.TrimSpace(rawURL)
			if trimmed == "" {
				continue
			}
			if strings.ToLower(strings.TrimSpace(imagehost.ExtractHost(trimmed))) != host {
				continue
			}
			filtered = append(filtered, api.ScreenshotImage{
				Index:  freshScreenshotImageIndex(filtered),
				Host:   host,
				ImgURL: trimmed,
				RawURL: trimmed,
				WebURL: trimmed,
			})
		}
		if len(filtered) > 0 {
			return filtered, host
		}
	}
	return nil, ""
}

func resolveLocalTrackerScreenshots(meta api.PreparedMetadata, appCfg config.Config, tracker string, logger api.Logger) []api.ScreenshotImage {
	if strings.TrimSpace(meta.SourcePath) == "" {
		return nil
	}
	tmpRoot, err := dbsvc.Subdir(appCfg.MainSettings.DBPath, "tmp")
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: local tracker screenshots tmp dir failed tracker=%s: %v", tracker, err)
		}
		return nil
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: local tracker screenshots release dir failed tracker=%s: %v", tracker, err)
		}
		return nil
	}

	results := make([]api.ScreenshotImage, 0)
	for _, record := range prioritizedTrackerRecords(meta, tracker) {
		trackerDir := sanitizeTrackerArtifactName(strings.ToLower(strings.TrimSpace(record.Tracker)))
		if trackerDir == "" {
			trackerDir = "tracker"
		}
		for index, rawURL := range record.ImageURLs {
			trimmed := strings.TrimSpace(rawURL)
			if trimmed == "" {
				continue
			}
			fileName := buildTrackerArtifactImageName(trimmed, index)
			if fileName == "" {
				continue
			}
			fullPath := filepath.Join(tmpDir, trackerDir, fileName)
			info, err := os.Stat(fullPath)
			if err != nil || info.IsDir() {
				continue
			}
			results = append(results, api.ScreenshotImage{
				Index: freshScreenshotImageIndex(results),
				Path:  fullPath,
				Host:  imagehost.ExtractHost(trimmed),
			})
		}
		if len(results) > 0 {
			return results
		}
	}
	return nil
}

func slotsContainComparison(slots []api.ScreenshotSlot) bool {
	for _, slot := range slots {
		if slot.SectionKind == screenshotSectionComparison {
			return true
		}
	}
	return false
}

func materializeDescriptionSlotImages(
	ctx context.Context,
	meta api.PreparedMetadata,
	appCfg config.Config,
	tracker string,
	slots []api.ScreenshotSlot,
	logger api.Logger,
) ([]api.ScreenshotImage, bool) {
	if len(slots) == 0 || strings.TrimSpace(meta.SourcePath) == "" {
		return nil, false
	}
	tmpRoot, err := dbsvc.Subdir(appCfg.MainSettings.DBPath, "tmp")
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: description slot image tmp dir failed tracker=%s: %v", tracker, err)
		}
		return nil, false
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: description slot image release dir failed tracker=%s: %v", tracker, err)
		}
		return nil, false
	}
	artifactDir := filepath.Join(tmpDir, "description-images")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		if logger != nil {
			logger.Warnf("trackers: description slot image dir failed tracker=%s: %v", tracker, err)
		}
		return nil, false
	}

	client := &http.Client{Timeout: descriptionSlotImageTimeout}
	results := make([]api.ScreenshotImage, 0)
	changed := false
	for idx := range slots {
		if !slots[idx].RenderInScreenshots {
			continue
		}
		if pathValue := strings.TrimSpace(slots[idx].ImagePath); pathValue != "" {
			results = append(results, api.ScreenshotImage{Index: preservedScreenshotImageIndex(slots[idx].SlotOrder), Path: pathValue})
			continue
		}
		originalURL := strings.TrimSpace(slots[idx].OriginalURL)
		if originalURL == "" {
			continue
		}
		outPath := filepath.Join(artifactDir, buildDescriptionSlotImageName(originalURL, slots[idx].SlotOrder))
		if info, err := os.Stat(outPath); err == nil && !info.IsDir() && info.Size() > 0 {
			slots[idx].ImagePath = outPath
			if strings.TrimSpace(slots[idx].OriginalKey) == "" {
				slots[idx].OriginalKey = outPath
			}
			results = append(results, api.ScreenshotImage{Index: preservedScreenshotImageIndex(slots[idx].SlotOrder), Path: outPath})
			changed = true
			continue
		}
		if err := downloadDescriptionSlotImage(ctx, client, originalURL, outPath); err != nil {
			if logger != nil {
				logger.Warnf("trackers: description slot image download failed tracker=%s url=%s: %v", tracker, originalURL, err)
			}
			if slots[idx].SectionKind != screenshotSectionComparison {
				slots[idx].RenderInScreenshots = false
				changed = true
			}
			continue
		}
		slots[idx].ImagePath = outPath
		if strings.TrimSpace(slots[idx].OriginalKey) == "" {
			slots[idx].OriginalKey = outPath
		}
		results = append(results, api.ScreenshotImage{Index: preservedScreenshotImageIndex(slots[idx].SlotOrder), Path: outPath})
		changed = true
	}
	return results, changed
}

func buildDescriptionSlotImageName(rawURL string, slotOrder int) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	base := ""
	if err == nil {
		base = path.Base(parsed.Path)
	}
	if base == "" || base == "." || base == "/" {
		base = "image"
	}
	base = sanitizeTrackerArtifactName(base)
	ext := path.Ext(base)
	if ext == "" {
		return fmt.Sprintf("slot_%03d_%s.png", slotOrder+1, base)
	}
	return fmt.Sprintf("slot_%03d_%s", slotOrder+1, base)
}

func downloadDescriptionSlotImage(ctx context.Context, client *http.Client, rawURL string, outPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "image") {
		return fmt.Errorf("invalid content-type %q", contentType)
	}
	if resp.ContentLength > descriptionSlotImageMaxBytes {
		return fmt.Errorf("image exceeds max size (%d bytes)", resp.ContentLength)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, descriptionSlotImageMaxBytes+1))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if len(payload) == 0 {
		return errors.New("empty image")
	}
	if len(payload) > descriptionSlotImageMaxBytes {
		return fmt.Errorf("image exceeds max size (%d bytes)", len(payload))
	}
	if err := os.WriteFile(outPath, payload, 0o600); err != nil {
		return fmt.Errorf("write image: %w", err)
	}
	return nil
}

func prioritizedTrackerRecords(meta api.PreparedMetadata, tracker string) []api.TrackerMetadata {
	if len(meta.TrackerData) == 0 {
		return nil
	}
	needle := strings.ToUpper(strings.TrimSpace(tracker))
	preferred := make([]api.TrackerMetadata, 0, len(meta.TrackerData))
	fallback := make([]api.TrackerMetadata, 0, len(meta.TrackerData))
	for _, record := range meta.TrackerData {
		if strings.ToUpper(strings.TrimSpace(record.Tracker)) == needle {
			preferred = append(preferred, record)
			continue
		}
		fallback = append(fallback, record)
	}
	return append(preferred, fallback...)
}

func sanitizeTrackerArtifactName(value string) string {
	replacer := strings.NewReplacer("<", "_", ">", "_", ":", "_", "\"", "_", "/", "_", "\\", "_", "|", "_", "?", "_", "*", "_")
	return strings.TrimSpace(replacer.Replace(value))
}

func buildTrackerArtifactImageName(rawURL string, index int) string {
	parsed, err := url.Parse(rawURL)
	base := ""
	if err == nil {
		base = path.Base(parsed.Path)
	}
	if base == "" || base == "." || base == "/" {
		base = "image"
	}
	base = sanitizeTrackerArtifactName(base)
	if !strings.Contains(base, ".") {
		return fmt.Sprintf("%s_%02d", base, index+1)
	}
	parts := strings.Split(base, ".")
	ext := parts[len(parts)-1]
	return fmt.Sprintf("%s_%02d.%s", strings.TrimSuffix(base, "."+ext), index+1, ext)
}

func hostAllowed(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	needle := strings.ToLower(strings.TrimSpace(host))
	for _, item := range allowed {
		if needle == strings.ToLower(strings.TrimSpace(item)) {
			return true
		}
	}
	return false
}
