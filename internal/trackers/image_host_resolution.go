// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
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

var descriptionSlotImageBlockedIPRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

var descriptionSlotImageLookupIPAddrs = net.DefaultResolver.LookupIPAddr

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
	policy, err := resolveImageHostPolicyForMetadata(tracker, appCfg, trackerCfg, meta, meta.ImageHostOverrides)
	if err != nil {
		return descriptionImageHostResolution{}, err
	}
	selectionPolicy := reusableImageHostSelectionPolicy(policy, preferredHosts...)
	feedback := api.ImageHostFeedback{
		Status:       "reused",
		AllowedHosts: append([]string{}, policy.allowed...),
	}
	skipUpload := imageHostUploadSkipped(meta)
	if repo == nil || strings.TrimSpace(meta.SourcePath) == "" {
		return descriptionImageHostResolution{feedback: feedback}, nil
	}

	slots, err := screenshotSlotsForImageHostResolution(ctx, tracker, meta, repo, logger, preloaded, skipUpload)
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: image host resolution screenshot slots failed tracker=%s: %v", tracker, err)
		}
		slots = nil
	}
	var localTrackerImages []api.ScreenshotImage
	if !skipUpload {
		localTrackerImages = resolveLocalTrackerScreenshots(meta, appCfg, tracker, logger)
		if detachComparisonSourceImagesFromSlots(slots, localTrackerImages) {
			if err := repo.ReplaceScreenshotSlots(ctx, meta.SourcePath, slots); err != nil {
				return descriptionImageHostResolution{}, fmt.Errorf("trackers: %w", err)
			}
			syncSlotsToPreloaded(preloaded, slots)
		}
	}

	if !policy.required {
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

	if screenshots, host, usageScope, err := selectScreenshotsFromSlots(tracker, slots, selectionPolicy); err == nil && len(screenshots) > 0 && reusableSelectionMatchesPolicy(host, selectionPolicy) {
		feedback.SelectedHost = host
		feedback.Message = buildReuseMessage(tracker, host, usageScope, host != preferredHost(selectionPolicy))
		return descriptionImageHostResolution{screenshots: screenshots, feedback: feedback, usageScope: usageScope}, nil
	} else if err != nil && allRenderableSlotsHaveEligibleVariant(slots, tracker, selectionPolicy) {
		return descriptionImageHostResolution{}, err
	}

	if skipUpload {
		feedback.Status = "warning"
		feedback.Message = fmt.Sprintf("%s requires screenshots from %s, but automatic image-host uploads are disabled.", tracker, imageHostRequirementLabel(policy))
		return descriptionImageHostResolution{feedback: feedback}, nil
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
		if screenshots, host := resolveTrackerScreenshotsForAllowedHost(urls, selectionPolicy); len(screenshots) > 0 && reusableSelectionMatchesPolicy(host, selectionPolicy) {
			feedback.SelectedHost = host
			feedback.Message = buildReuseMessage(tracker, host, globalImageUsageScope, host != preferredHost(selectionPolicy))
			return descriptionImageHostResolution{screenshots: screenshots, feedback: feedback, usageScope: globalImageUsageScope}, nil
		}
		feedback.Status = "warning"
		feedback.Message = fmt.Sprintf("%s requires screenshots from %s, but no local screenshots are available to rehost.", tracker, imageHostRequirementLabel(policy))
		return descriptionImageHostResolution{feedback: feedback}, nil
	}

	for _, host := range reusableHostCandidates(selectionPolicy) {
		usageScope := usageScopeForHost(host)
		screenshots, err := reusableUploadedScreenshotsForHost(ctx, tracker, meta, repo, preloaded, host, usageScope, sourceImages)
		if err != nil {
			return descriptionImageHostResolution{}, err
		}
		if len(screenshots) > 0 {
			feedback.SelectedHost = host
			feedback.Message = buildReuseMessage(tracker, host, usageScope, host != preferredHost(selectionPolicy))
			return descriptionImageHostResolution{screenshots: screenshots, feedback: feedback, usageScope: usageScope}, nil
		}
	}

	if images == nil {
		fallbackPolicy := effectiveImageHostSelectionPolicy(policy, preferredHosts...)
		if screenshots, host, usageScope, err := selectScreenshotsFromSlots(tracker, slots, fallbackPolicy); err == nil && len(screenshots) > 0 {
			feedback.SelectedHost = host
			feedback.Message = buildReuseMessage(tracker, host, usageScope, host != preferredHost(selectionPolicy))
			return descriptionImageHostResolution{screenshots: screenshots, feedback: feedback, usageScope: usageScope}, nil
		}
		for _, host := range reusableHostCandidates(fallbackPolicy) {
			usageScope := usageScopeForHost(host)
			screenshots, err := reusableUploadedScreenshotsForHost(ctx, tracker, meta, repo, preloaded, host, usageScope, sourceImages)
			if err != nil {
				return descriptionImageHostResolution{}, err
			}
			if len(screenshots) > 0 {
				feedback.SelectedHost = host
				feedback.Message = buildReuseMessage(tracker, host, usageScope, host != preferredHost(selectionPolicy))
				return descriptionImageHostResolution{screenshots: screenshots, feedback: feedback, usageScope: usageScope}, nil
			}
		}
		feedback.Status = "warning"
		feedback.Message = fmt.Sprintf("%s requires screenshots from %s, but image hosting is unavailable.", tracker, imageHostRequirementLabel(policy))
		return descriptionImageHostResolution{feedback: feedback}, nil
	}

	var lastErr error
	for _, host := range uploadAttemptHosts(policy, preferredHosts...) {
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
		screenshots, _, _, err := selectScreenshotsFromSlots(tracker, candidateSlots, selectionPolicy)
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
	attemptHosts := strings.Join(uploadAttemptHosts(policy, preferredHosts...), ", ")
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

func imageHostRequirementLabel(policy imageHostPolicy) string {
	if len(policy.allowed) > 0 {
		return strings.Join(policy.allowed, ", ")
	}
	if len(policy.preferred) > 0 {
		return strings.Join(policy.preferred, ", ")
	}
	return "a configured image host"
}

func imageHostUploadSkipped(meta api.PreparedMetadata) bool {
	return meta.ImageHostOverrides.SkipUpload != nil && *meta.ImageHostOverrides.SkipUpload
}

func screenshotSlotsForImageHostResolution(
	ctx context.Context,
	tracker string,
	meta api.PreparedMetadata,
	repo api.MetadataRepository,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
	skipUpload bool,
) ([]api.ScreenshotSlot, error) {
	if !skipUpload {
		return screenshotSlotsFromSource(ctx, tracker, meta, repo, logger, preloaded)
	}
	return screenshotSlotsFromSourceWithoutPersist(ctx, tracker, meta, repo, logger, preloaded)
}

func screenshotSlotsFromSourceWithoutPersist(
	ctx context.Context,
	tracker string,
	meta api.PreparedMetadata,
	repo api.MetadataRepository,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
) ([]api.ScreenshotSlot, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: load screenshot slots canceled: %w", err)
	}
	if repo == nil || strings.TrimSpace(meta.SourcePath) == "" {
		return nil, nil
	}
	if preloaded != nil && preloaded.screenshotSlotsLoaded {
		return cloneScreenshotSlots(preloaded.screenshotSlots), nil
	}

	slots, err := repo.ListScreenshotSlotsByPath(ctx, meta.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if len(slots) > 0 {
		if !meta.Options.KeepImages {
			slots, err = filterStoredSlotsForSelectedImages(ctx, meta, repo, slots, preloaded)
			if err != nil {
				return nil, err
			}
		} else {
			slots, err = appendStoredSelectionSlots(ctx, meta, repo, slots, preloaded)
			if err != nil {
				return nil, err
			}
		}
		if len(slots) == 0 {
			return nil, nil
		}
		return cloneScreenshotSlots(slots), nil
	}

	slots, err = synthesizeScreenshotSlots(ctx, tracker, meta, repo, logger, preloaded)
	if err != nil {
		return nil, err
	}
	return cloneScreenshotSlots(slots), nil
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

func effectiveImageHostSelectionPolicy(policy imageHostPolicy, preferredHosts ...string) imageHostPolicy {
	host := firstPreferredDescriptionImageHost(preferredHosts)
	if host == "" || !hostAllowed(host, policy.allowed) {
		return policy
	}
	effective := policy
	effective.preferred = prependHost(host, effective.preferred)
	return effective
}

func reusableImageHostSelectionPolicy(policy imageHostPolicy, preferredHosts ...string) imageHostPolicy {
	effective := effectiveImageHostSelectionPolicy(policy, preferredHosts...)
	if !effective.required || effective.fallbackOK || len(effective.preferred) <= 1 {
		return effective
	}
	effective.preferred = append([]string(nil), effective.preferred[:1]...)
	return effective
}

func reusableSelectionMatchesPolicy(host string, policy imageHostPolicy) bool {
	normalizedHost := strings.ToLower(strings.TrimSpace(host))
	if normalizedHost == "" {
		return false
	}
	if !policy.required || policy.fallbackOK {
		return true
	}
	preferred := preferredHost(policy)
	return preferred == "" || strings.EqualFold(normalizedHost, preferred)
}

func reusableHostCandidates(policy imageHostPolicy) []string {
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

func uploadAttemptHosts(policy imageHostPolicy, preferredHosts ...string) []string {
	selectionPolicy := effectiveImageHostSelectionPolicy(policy, preferredHosts...)
	candidates := reusableHostCandidates(selectionPolicy)
	if len(candidates) == 0 {
		return nil
	}
	if policy.fallbackOK || firstPreferredDescriptionImageHost(preferredHosts) != "" {
		return candidates
	}
	return candidates[:1]
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

	client := newDescriptionSlotImageHTTPClient()
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
	if err := validateDescriptionSlotImageURL(ctx, strings.TrimSpace(rawURL)); err != nil {
		return err
	}
	client = descriptionSlotImageHTTPClient(client)
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
	if contentType != "" && !isDescriptionSlotImageContentType(contentType) {
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
	detectedContentType := strings.ToLower(http.DetectContentType(payload))
	if !isDescriptionSlotImageContentType(detectedContentType) {
		return fmt.Errorf("invalid image payload content-type %q", detectedContentType)
	}
	if err := os.WriteFile(outPath, payload, 0o600); err != nil {
		return fmt.Errorf("write image: %w", err)
	}
	return nil
}

var newDescriptionSlotImageHTTPClient = func() *http.Client {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return descriptionSlotImageHTTPClient(&http.Client{Timeout: descriptionSlotImageTimeout})
	}
	transport := defaultTransport.Clone()
	dialer := &net.Dialer{Timeout: descriptionSlotImageTimeout}
	transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("parse address: %w", err)
		}
		addrs, err := resolveDescriptionSlotImagePublicAddrs(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, addr := range addrs {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	return descriptionSlotImageHTTPClient(&http.Client{
		Timeout:   descriptionSlotImageTimeout,
		Transport: transport,
	})
}

func descriptionSlotImageHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: descriptionSlotImageTimeout}
	}
	cloned := *client
	if cloned.Timeout == 0 {
		cloned.Timeout = descriptionSlotImageTimeout
	}
	checkRedirect := cloned.CheckRedirect
	cloned.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateDescriptionSlotImageURL(req.Context(), req.URL.String()); err != nil {
			return err
		}
		if checkRedirect != nil {
			return checkRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &cloned
}

func validateDescriptionSlotImageURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return errors.New("missing host")
	}
	_, err = resolveDescriptionSlotImagePublicAddrs(ctx, host)
	return err
}

func resolveDescriptionSlotImagePublicAddrs(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return nil, errors.New("missing host")
	}
	lowerHost := strings.ToLower(host)
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") || strings.Contains(lowerHost, "%") {
		return nil, fmt.Errorf("blocked private image host %q", host)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if !isDescriptionSlotPublicIP(addr) {
			return nil, fmt.Errorf("blocked private image address %q", addr)
		}
		return []netip.Addr{addr}, nil
	}
	resolved, err := descriptionSlotImageLookupIPAddrs(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve host %q: %w", host, err)
	}
	addrs := make([]netip.Addr, 0, len(resolved))
	for _, item := range resolved {
		addr, ok := netip.AddrFromSlice(item.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if !isDescriptionSlotPublicIP(addr) {
			return nil, fmt.Errorf("blocked private image address %q", addr)
		}
		addrs = append(addrs, addr)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("host %q resolved no public addresses", host)
	}
	return addrs, nil
}

func isDescriptionSlotPublicIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	for _, blocked := range descriptionSlotImageBlockedIPRanges {
		if blocked.Contains(addr) {
			return false
		}
	}
	return true
}

func isDescriptionSlotImageContentType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "image/")
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
