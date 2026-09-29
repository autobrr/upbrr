// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"crypto/sha256"
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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	"github.com/autobrr/upbrr/internal/logging"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type descriptionImageHostResolution struct {
	screenshots []api.ScreenshotImage
	feedback    api.ImageHostFeedback
	usageScope  string
	blocking    bool
}

// exactMediaForTrackerHost keeps only screenshots on a reusable host accepted
// by this tracker. The prepared workflow may contain other trackers' images.
func exactMediaForTrackerHost(
	tracker string,
	meta api.UploadSubject,
	appCfg config.Config,
	trackerCfg config.TrackerConfig,
	registry *Registry,
	preferredHosts ...string,
) (*api.ExactMediaAssets, error) {
	exact := meta.ExactMedia.Clone()
	if exact != nil {
		exact.ScreenshotUploads = slices.DeleteFunc(exact.ScreenshotUploads, sourceOnlyUploadedImage)
		exact.DVDMenuUploads = slices.DeleteFunc(exact.DVDMenuUploads, sourceOnlyUploadedImage)
	}
	if exact == nil || len(exact.Screenshots) == 0 || len(exact.ScreenshotUploads) == 0 {
		return exact, nil
	}
	policy, err := resolveImageHostPolicyForMetadataWithRegistry(registry, tracker, appCfg, trackerCfg, meta.ImageHostOverrides)
	if err != nil {
		return nil, err
	}
	if !policy.required {
		foreignPaths := make(map[string]struct{})
		compatiblePaths := make(map[string]struct{})
		uploads := make([]api.UploadedImageLink, 0, len(exact.ScreenshotUploads))
		for _, upload := range exact.ScreenshotUploads {
			pathValue := strings.TrimSpace(upload.ImagePath)
			host := strings.ToLower(strings.TrimSpace(upload.Host))
			scope := normalizeUsageScope(upload.UsageScope)
			if owner := trackerForOwnedHost(registry, host); owner != "" && !strings.EqualFold(owner, tracker) ||
				scope != globalImageUsageScope && scope != trackerImageUsageScope(tracker) {
				foreignPaths[pathValue] = struct{}{}
				continue
			}
			uploads = append(uploads, upload)
			compatiblePaths[pathValue] = struct{}{}
		}
		if len(foreignPaths) > 0 {
			images := make([]api.ScreenshotImage, 0, len(exact.Screenshots))
			for _, image := range exact.Screenshots {
				pathValue := strings.TrimSpace(image.Path)
				if _, foreign := foreignPaths[pathValue]; foreign {
					if _, compatible := compatiblePaths[pathValue]; !compatible {
						continue
					}
				}
				images = append(images, image)
			}
			exact.Screenshots = images
		}
		exact.ScreenshotUploads = uploads
		return exact, nil
	}
	selectionPolicy := reusableImageHostSelectionPolicy(policy, preferredHosts...)
	availablePaths := make(map[string]struct{}, len(exact.Screenshots))
	for _, image := range exact.Screenshots {
		availablePaths[strings.TrimSpace(image.Path)] = struct{}{}
	}
	pathsByHost := make(map[string]map[string]struct{})
	orderedHosts := make([]string, 0)
	for _, upload := range exact.ScreenshotUploads {
		host := strings.ToLower(strings.TrimSpace(upload.Host))
		pathValue := strings.TrimSpace(upload.ImagePath)
		if _, selected := availablePaths[pathValue]; !selected || pathValue == "" || host == "" ||
			hostInList(host, selectionPolicy.failed) ||
			len(selectionPolicy.allowed) > 0 && !hostAllowed(host, selectionPolicy.allowed) ||
			!reusableSelectionMatchesPolicy(host, selectionPolicy) {
			continue
		}
		if owner := trackerForOwnedHost(registry, host); owner != "" && !strings.EqualFold(owner, tracker) {
			continue
		}
		scope := normalizeUsageScope(upload.UsageScope)
		if scope != globalImageUsageScope && scope != trackerImageUsageScope(tracker) {
			continue
		}
		if pathsByHost[host] == nil {
			pathsByHost[host] = make(map[string]struct{})
			orderedHosts = append(orderedHosts, host)
		}
		pathsByHost[host][pathValue] = struct{}{}
	}
	selectedHost := ""
	for _, host := range orderedHosts {
		if selectedHost == "" || len(pathsByHost[host]) > len(pathsByHost[selectedHost]) {
			selectedHost = host
		}
	}
	if selectedHost == "" {
		exact.ScreenshotUploads = nil
		return exact, nil
	}
	selected := pathsByHost[selectedHost]
	uploadedPaths := make(map[string]struct{}, len(exact.ScreenshotUploads))
	for _, upload := range exact.ScreenshotUploads {
		uploadedPaths[strings.TrimSpace(upload.ImagePath)] = struct{}{}
	}
	filteredImages := make([]api.ScreenshotImage, 0, len(selected))
	for _, image := range exact.Screenshots {
		pathValue := strings.TrimSpace(image.Path)
		_, hosted := selected[pathValue]
		_, hasUpload := uploadedPaths[pathValue]
		if hosted || !hasUpload {
			filteredImages = append(filteredImages, image)
		}
	}
	filteredUploads := make([]api.UploadedImageLink, 0, len(selected))
	for _, upload := range exact.ScreenshotUploads {
		if strings.EqualFold(strings.TrimSpace(upload.Host), selectedHost) {
			if _, ok := selected[strings.TrimSpace(upload.ImagePath)]; ok {
				filteredUploads = append(filteredUploads, upload)
			}
		}
	}
	exact.Screenshots = filteredImages
	exact.ScreenshotUploads = filteredUploads
	return exact, nil
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

func ensureDescriptionImageHostWithRegistry(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	appCfg config.Config,
	trackerCfg config.TrackerConfig,
	repo UploadPersistence,
	images api.ImageHostingService,
	registry *Registry,
) (descriptionImageHostResolution, error) {
	return ensureDescriptionImageHostWithDataAndRegistry(ctx, tracker, meta, appCfg, trackerCfg, repo, images, api.NopLogger{}, registry, nil)
}

func ensureDescriptionImageHostWithDataAndRegistry(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	appCfg config.Config,
	trackerCfg config.TrackerConfig,
	repo UploadPersistence,
	images api.ImageHostingService,
	logger api.Logger,
	registry *Registry,
	preloaded *preloadedDescriptionAssetData,
	preferredHosts ...string,
) (descriptionImageHostResolution, error) {
	policy, err := resolveImageHostPolicyForMetadataWithRegistry(registry, tracker, appCfg, trackerCfg, meta.ImageHostOverrides)
	if err != nil {
		return descriptionImageHostResolution{}, err
	}
	policy.sourceOnlyAllowed = sourceOnlyImageReusePolicy(registry, tracker, meta, preloaded)
	selectionPolicy := reusableImageHostSelectionPolicy(policy, preferredHosts...)
	feedback := api.ImageHostFeedback{
		Status:       "reused",
		AllowedHosts: append([]string{}, policy.allowed...),
	}
	skipUpload := imageHostUploadSkipped(meta)
	if repo == nil || strings.TrimSpace(meta.SourcePath) == "" {
		return descriptionImageHostResolution{feedback: feedback}, nil
	}
	if meta.MediaBinding != (api.PreparedMediaBinding{}) && !meta.MediaBinding.Valid() {
		return descriptionImageHostResolution{}, internalerrors.ErrInvalidInput
	}

	slots, err := screenshotSlotsForImageHostResolution(ctx, tracker, meta, repo, logger, preloaded, registry, skipUpload)
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: image host resolution screenshot slots failed tracker=%s: %v", tracker, err)
		}
		slots = nil
	}
	records := meta.TrackerData
	if preloaded != nil && len(preloaded.trackerRecords) > 0 {
		records = preloaded.trackerRecords
	}
	if attachNativeSourceURLsToSlots(slots, records, policy) {
		syncSlotsToPreloaded(preloaded, slots)
	}
	if meta.ExactMedia != nil {
		for index := range slots {
			if strings.TrimSpace(slots[index].ImagePath) != "" && len(slots[index].Variants) == 0 {
				slots[index].RenderInScreenshots = true
			}
		}
	}
	sourceOnlySlots := false
	needsLocalRehost := false
	for _, slot := range renderableSlots(slots) {
		if imagehost.IsSourceOnlyURL(imagehost.DirectImageURL(slot.OriginalURL)) {
			sourceOnlySlots = true
		}
		if strings.TrimSpace(slot.ImagePath) != "" && len(slot.Variants) == 0 {
			needsLocalRehost = true
		}
	}
	if (sourceOnlySlots || needsLocalRehost) && meta.ExactMedia != nil {
		skipUpload = false
	}
	persistSlots := screenshotSlotsSourceWide(ctx, tracker, meta, repo, preloaded, registry)
	var localTrackerImages []api.ScreenshotImage
	if !skipUpload {
		localTrackerImages = resolveLocalTrackerScreenshots(ctx, meta, appCfg, tracker, repo, registry, logger)
		if detachComparisonSourceImagesFromSlots(slots, localTrackerImages) {
			if persistSlots {
				if err := repo.ReplaceScreenshotSlots(ctx, meta.MediaBinding, slots); err != nil {
					return descriptionImageHostResolution{}, fmt.Errorf("trackers: %w", err)
				}
			}
			syncSlotsToPreloaded(preloaded, slots)
		}
	}

	if !policy.required {
		selectionPolicy = optionalImageHostSelectionPolicy(policy, preferredHosts...)
		preferredHost := preferredHost(selectionPolicy)
		screenshots, host, usageScope, err := selectScreenshotsFromSlots(tracker, slots, selectionPolicy)
		if err == nil && len(screenshots) > 0 {
			feedback.SelectedHost = host
			feedback.Message = buildReuseMessage(tracker, host, usageScope, false)
			return descriptionImageHostResolution{
				screenshots: screenshots,
				feedback:    feedback,
				usageScope:  usageScope,
			}, nil
		}
		urls := resolveTrackerImageURLs(ctx, tracker, meta, repo, logger, preloaded, registry)
		screenshots = resolveTrackerScreenshotsWithPolicy(urls, selectionPolicy)
		if len(screenshots) > 0 {
			feedback.SelectedHost = strings.ToLower(strings.TrimSpace(screenshots[0].Host))
			feedback.Message = buildReuseMessage(tracker, feedback.SelectedHost, globalImageUsageScope, false)
		}
		if !sourceOnlySlots && !needsLocalRehost && (len(screenshots) > 0 || preferredHost == "") {
			return descriptionImageHostResolution{
				screenshots: screenshots,
				feedback:    feedback,
				usageScope:  globalImageUsageScope,
			}, nil
		}
		if sourceOnlySlots || needsLocalRehost {
			policy = sourceOnlyImageUploadPolicy(registry, appCfg, tracker, policy, preferredHosts...)
		} else {
			policy = optionalImageHostUploadPolicy(policy, preferredHosts...)
		}
		selectionPolicy = reusableImageHostSelectionPolicy(policy, preferredHosts...)
	}

	if screenshots, host, usageScope, err := selectScreenshotsFromSlots(
		tracker,
		slots,
		selectionPolicy,
	); err == nil && len(screenshots) > 0 &&
		reusableSelectionMatchesPolicy(host, selectionPolicy) {
		feedback.SelectedHost = host
		feedback.Message = buildReuseMessage(tracker, host, usageScope, host != preferredHost(selectionPolicy))
		return descriptionImageHostResolution{
			screenshots: screenshots,
			feedback:    feedback,
			usageScope:  usageScope,
		}, nil
	} else if err != nil &&
		allRenderableSlotsHaveEligibleVariant(slots, tracker, selectionPolicy) {
		return descriptionImageHostResolution{}, err
	}

	if skipUpload {
		feedback.Status = "warning"
		feedback.Message = fmt.Sprintf(
			"%s requires screenshots from %s, but automatic image-host uploads are disabled.",
			tracker,
			imageHostRequirementLabel(policy),
		)
		return descriptionImageHostResolution{feedback: feedback, blocking: sourceOnlySlots || needsLocalRehost}, nil
	}

	sourceImages := slotSourceImagesForRehost(slots)
	if len(sourceImages) == 0 {
		sourceImages = localTrackerImages
		if len(sourceImages) > 0 && !slotsContainComparison(slots) && alignRenderableSlotsToSourceImages(slots, sourceImages) {
			if persistSlots {
				if err := repo.ReplaceScreenshotSlots(ctx, meta.MediaBinding, slots); err != nil {
					return descriptionImageHostResolution{}, fmt.Errorf("trackers: %w", err)
				}
			}
			syncSlotsToPreloaded(preloaded, slots)
		}
	}
	if len(slots) > 0 {
		changed := attachMatchingSourceImagesToSlots(slots, localTrackerImages)
		if appendSourceImageSlots(&slots, meta.SourcePath, localTrackerImages) {
			changed = true
		}
		if materialized, materializeChanged := materializeDescriptionSlotImages(
			ctx,
			meta,
			appCfg,
			tracker,
			slots,
			logger,
		); len(materialized) > 0 ||
			materializeChanged {
			changed = changed || materializeChanged
		}
		if changed {
			if persistSlots {
				if err := repo.ReplaceScreenshotSlots(ctx, meta.MediaBinding, slots); err != nil {
					return descriptionImageHostResolution{}, fmt.Errorf("trackers: %w", err)
				}
			}
			syncSlotsToPreloaded(preloaded, slots)
		}
		sourceImages = slotSourceImagesForRehost(slots)
	}
	if len(sourceImages) == 0 {
		urls := resolveTrackerImageURLs(ctx, tracker, meta, repo, logger, preloaded, registry)
		if screenshots, host := resolveTrackerScreenshotsForAllowedHost(
			urls,
			selectionPolicy,
		); len(screenshots) > 0 &&
			reusableSelectionMatchesPolicy(host, selectionPolicy) {
			feedback.SelectedHost = host
			feedback.Message = buildReuseMessage(tracker, host, globalImageUsageScope, host != preferredHost(selectionPolicy))
			return descriptionImageHostResolution{
				screenshots: screenshots,
				feedback:    feedback,
				usageScope:  globalImageUsageScope,
			}, nil
		}
		feedback.Status = "warning"
		feedback.Message = fmt.Sprintf(
			"%s requires screenshots from %s, but no local screenshots are available to rehost.",
			tracker,
			imageHostRequirementLabel(policy),
		)
		return descriptionImageHostResolution{feedback: feedback}, nil
	}

	for _, host := range reusableHostCandidates(selectionPolicy) {
		usageScope := usageScopeForHost(registry, host)
		screenshots, err := reusableUploadedScreenshotsForHost(ctx, tracker, meta, repo, preloaded, host, usageScope, sourceImages)
		if err != nil {
			return descriptionImageHostResolution{}, err
		}
		if len(screenshots) > 0 {
			feedback.SelectedHost = host
			feedback.Message = buildReuseMessage(tracker, host, usageScope, host != preferredHost(selectionPolicy))
			return descriptionImageHostResolution{
				screenshots: screenshots,
				feedback:    feedback,
				usageScope:  usageScope,
			}, nil
		}
	}

	if images == nil {
		fallbackPolicy := effectiveImageHostSelectionPolicy(policy, preferredHosts...)
		if screenshots, host, usageScope, err := selectScreenshotsFromSlots(tracker, slots, fallbackPolicy); err == nil && len(screenshots) > 0 {
			feedback.SelectedHost = host
			feedback.Message = buildReuseMessage(tracker, host, usageScope, host != preferredHost(selectionPolicy))
			return descriptionImageHostResolution{
				screenshots: screenshots,
				feedback:    feedback,
				usageScope:  usageScope,
			}, nil
		}
		for _, host := range reusableHostCandidates(fallbackPolicy) {
			usageScope := usageScopeForHost(registry, host)
			screenshots, err := reusableUploadedScreenshotsForHost(ctx, tracker, meta, repo, preloaded, host, usageScope, sourceImages)
			if err != nil {
				return descriptionImageHostResolution{}, err
			}
			if len(screenshots) > 0 {
				feedback.SelectedHost = host
				feedback.Message = buildReuseMessage(tracker, host, usageScope, host != preferredHost(selectionPolicy))
				return descriptionImageHostResolution{
					screenshots: screenshots,
					feedback:    feedback,
					usageScope:  usageScope,
				}, nil
			}
		}
		feedback.Status = "warning"
		feedback.Message = fmt.Sprintf("%s requires screenshots from %s, but image hosting is unavailable.", tracker, imageHostRequirementLabel(policy))
		return descriptionImageHostResolution{feedback: feedback}, nil
	}

	var lastErr error
	for _, host := range uploadAttemptHosts(policy, preferredHosts...) {
		usageScope := usageScopeForHost(registry, host)
		uploaded, err := images.Upload(ctx, imageHostingSubject(meta), host, usageScope, sourceImages)
		if err != nil {
			lastErr = err
			if len(uploaded) > 0 {
				cleanupUploadedImages(ctx, repo, meta.MediaBinding, uploaded, logger)
			}
			feedback.Warnings = append(feedback.Warnings, api.ImageHostWarning{
				Host:    host,
				Message: logging.SanitizeMessage(err.Error()),
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
			cleanupUploadedImages(ctx, repo, meta.MediaBinding, uploaded, logger)
			lastErr = err
			feedback.Warnings = append(feedback.Warnings, api.ImageHostWarning{
				Host:    host,
				Message: logging.SanitizeMessage(err.Error()),
			})
			if logger != nil {
				logger.Warnf("trackers: image host upload produced unusable screenshots tracker=%s host=%s: %v", tracker, host, err)
			}
			continue
		}
		if len(screenshots) == 0 {
			cleanupUploadedImages(ctx, repo, meta.MediaBinding, uploaded, logger)
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
		if persistSlots {
			if err := upsertScreenshotVariantsFromUploads(ctx, repo, meta.MediaBinding, slots, uploaded); err != nil {
				cleanupUploadedImages(ctx, repo, meta.MediaBinding, uploaded, logger)
				return descriptionImageHostResolution{}, err
			}
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
		return descriptionImageHostResolution{
			screenshots: screenshots,
			feedback:    feedback,
			usageScope:  usageScope,
		}, nil
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

func imageHostingSubject(meta api.UploadSubject) api.ImageHostingSubject {
	return api.NewImageHostingSubject(meta)
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

// optionalImageHostSelectionPolicy keeps preferred host ordering for optional
// image-host reuse without restricting the set of acceptable screenshot hosts.
func optionalImageHostSelectionPolicy(policy imageHostPolicy, preferredHosts ...string) imageHostPolicy {
	return imageHostPolicy{
		preferred:         optionalImageHostPreferredHosts(policy, preferredHosts...),
		failed:            append([]string(nil), policy.failed...),
		sourceOnlyAllowed: policy.sourceOnlyAllowed,
	}
}

// optionalImageHostUploadPolicy promotes optional preferences into an upload
// policy once reuse cannot satisfy the requested preferred host.
func optionalImageHostUploadPolicy(policy imageHostPolicy, preferredHosts ...string) imageHostPolicy {
	hosts := optionalImageHostPreferredHosts(policy, preferredHosts...)
	for _, host := range policy.uploadHosts {
		if supportedUploadImageHost(host) {
			hosts = appendUniqueHost(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return policy
	}
	return applyFailedImageHosts(newPreferredImageHostPolicy(hosts[0], hosts[1:]...), policy.failed)
}

// sourceOnlyImageUploadPolicy adds configured, tracker-compatible upload hosts
// before applying optional preferences to images whose original URL cannot be reused.
func sourceOnlyImageUploadPolicy(registry *Registry, appCfg config.Config, tracker string, policy imageHostPolicy, preferredHosts ...string) imageHostPolicy {
	for _, host := range imageUploadCandidatesForTracker(registry, appCfg, tracker, configuredImageUploadHosts(registry, appCfg)) {
		if !imageHostUsableForPolicy(registry, tracker, host, policy) {
			continue
		}
		policy.uploadHosts = appendUniqueHost(policy.uploadHosts, host)
		policy.preferred = appendUniqueHost(policy.preferred, host)
	}
	return optionalImageHostUploadPolicy(policy, preferredHosts...)
}

// optionalImageHostPreferredHosts returns policy preferences with an explicit
// preferred host first, preserving existing fallback order and deduping hosts.
func optionalImageHostPreferredHosts(policy imageHostPolicy, preferredHosts ...string) []string {
	hosts := append([]string(nil), policy.preferred...)
	if host := firstPreferredDescriptionImageHost(preferredHosts); host != "" {
		if !hostInList(host, policy.failed) {
			hosts = prependHost(host, hosts)
		}
	}
	return hosts
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

func imageHostUploadSkipped(meta api.UploadSubject) bool {
	return meta.ImageHostOverrides.SkipUpload != nil && *meta.ImageHostOverrides.SkipUpload
}

func screenshotSlotsForImageHostResolution(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
	registry *Registry,
	skipUpload bool,
) ([]api.ScreenshotSlot, error) {
	if !skipUpload {
		return screenshotSlotsFromSource(ctx, tracker, meta, repo, logger, preloaded, registry)
	}
	return screenshotSlotsFromSourceWithoutPersist(ctx, tracker, meta, repo, logger, preloaded, registry)
}

func screenshotSlotsFromSourceWithoutPersist(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
	registry *Registry,
) ([]api.ScreenshotSlot, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: load screenshot slots canceled: %w", err)
	}
	if repo == nil || strings.TrimSpace(meta.SourcePath) == "" {
		return nil, nil
	}
	if preloaded != nil && preloaded.screenshotSlotsLoaded {
		return reconcileStoredComparisonSlots(ctx, tracker, meta, repo, logger, preloaded, registry,
			cloneScreenshotSlots(preloaded.screenshotSlots), false)
	}

	slots, err := repo.ListScreenshotSlotsByPath(ctx, meta.MediaBinding)
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
		return reconcileStoredComparisonSlots(ctx, tracker, meta, repo, logger, preloaded, registry,
			cloneScreenshotSlots(slots), false)
	}

	slots, err = synthesizeScreenshotSlots(ctx, tracker, meta, repo, logger, preloaded, registry)
	if err != nil {
		return nil, err
	}
	return cloneScreenshotSlots(slots), nil
}

func reusableUploadedScreenshotsForHost(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
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
		if sourceOnlyUploadedImage(upload) {
			continue
		}
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

func cleanupUploadedImages(
	ctx context.Context,
	repo UploadPersistence,
	binding api.PreparedMediaBinding,
	uploaded []api.UploadedImageLink,
	logger api.Logger,
) {
	if repo == nil || len(uploaded) == 0 || !binding.Valid() {
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
		if err := repo.DeleteUploadedImage(ctx, binding, pathValue, hostValue); err != nil && logger != nil {
			logger.Warnf(
				"trackers: failed to roll back uploaded image source_path=%s host=%s path=%s: %v",
				strings.TrimSpace(binding.SourcePath),
				hostValue,
				pathValue,
				err,
			)
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
	if host == "" || hostInList(host, policy.failed) || !hostAllowed(host, policy.allowed) {
		return policy
	}
	effective := policy
	effective.preferred = prependHost(host, effective.preferred)
	return effective
}

func reusableImageHostSelectionPolicy(policy imageHostPolicy, preferredHosts ...string) imageHostPolicy {
	effective := effectiveImageHostSelectionPolicy(policy, preferredHosts...)
	if !effective.required || effective.fallbackOK || len(effective.allowed) > 0 || len(effective.preferred) <= 1 {
		return effective
	}
	preferred := []string{effective.preferred[0]}
	for _, host := range effective.preferred[1:] {
		if hostAllowed(host, effective.allowed) && !supportedUploadImageHost(host) {
			preferred = append(preferred, host)
		}
	}
	effective.preferred = preferred
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
	if len(policy.allowed) > 0 && hostAllowed(normalizedHost, policy.allowed) {
		return true
	}
	preferred := preferredHost(policy)
	return preferred == "" || strings.EqualFold(normalizedHost, preferred)
}

func reusableHostCandidates(policy imageHostPolicy) []string {
	allowedUploads := make(map[string]struct{}, len(policy.uploadHosts))
	for _, host := range policy.uploadHosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized != "" && !hostInList(normalized, policy.failed) {
			allowedUploads[normalized] = struct{}{}
		}
	}
	out := make([]string, 0, len(policy.preferred))
	seen := make(map[string]struct{}, len(policy.preferred))
	for _, host := range policy.preferred {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized == "" || hostInList(normalized, policy.failed) {
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
			directURL := imagehost.DirectImageURL(trimmed)
			if directURL == "" || imagehost.IsSourceOnlyURL(directURL) {
				continue
			}
			if strings.ToLower(strings.TrimSpace(imagehost.ExtractHost(directURL))) != host {
				continue
			}
			filtered = append(filtered, api.ScreenshotImage{
				Index:  freshScreenshotImageIndex(filtered),
				Host:   host,
				ImgURL: directURL,
				RawURL: directURL,
				WebURL: directURL,
			})
		}
		if len(filtered) > 0 {
			return filtered, host
		}
	}
	return nil, ""
}

func resolveLocalTrackerScreenshots(
	ctx context.Context,
	meta api.UploadSubject,
	appCfg config.Config,
	tracker string,
	repo UploadPersistence,
	registry *Registry,
	logger api.Logger,
) []api.ScreenshotImage {
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
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: local tracker screenshots release dir failed tracker=%s: %v", tracker, err)
		}
		return nil
	}

	results := make([]api.ScreenshotImage, 0)
	for _, record := range FilterUnverifiedTrackerImages(ctx, repo, registry, prioritizedTrackerRecords(meta, tracker), logger) {
		trackerDir := sanitizePersistedTrackerArtifactName(strings.ToLower(strings.TrimSpace(record.Tracker)))
		if trackerDir == "" {
			trackerDir = "tracker"
		}
		allowedURLs := make(map[string]struct{})
		for _, rawURL := range ComparisonSafeTrackerImageURLs(record, registry) {
			allowedURLs[strings.TrimSpace(rawURL)] = struct{}{}
		}
		for index, rawURL := range record.ImageURLs {
			trimmed := strings.TrimSpace(rawURL)
			if trimmed == "" {
				continue
			}
			if _, allowed := allowedURLs[trimmed]; !allowed {
				continue
			}
			for _, fullPath := range localTrackerArtifactPaths(filepath.Join(tmpDir, trackerDir), trimmed, index) {
				info, err := os.Stat(fullPath)
				if err != nil || info.IsDir() || info.Size() == 0 {
					continue
				}
				results = append(results, api.ScreenshotImage{
					Index: freshScreenshotImageIndex(results),
					Path:  fullPath,
					Host:  imagehost.ExtractHost(trimmed),
				})
				break
			}
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
	meta api.UploadSubject,
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
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
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
	if slices.ContainsFunc(slots, func(slot api.ScreenshotSlot) bool {
		return slot.RenderInScreenshots && strings.TrimSpace(slot.ImagePath) == "" && isPTPDescriptionImageURL(imagehost.DirectImageURL(slot.OriginalURL))
	}) {
		client = PTPDescriptionImageHTTPClient(ctx, client, appCfg, logger)
	}
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
				logger.Warnf("trackers: description slot image download failed tracker=%s slot=%d reason=%s",
					tracker, slots[idx].SlotOrder+1, descriptionSlotImageFailureReason(err))
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

func descriptionSlotImageFailureReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "request_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "request_canceled"
	}
	message := err.Error()
	if after, ok := strings.CutPrefix(message, "status "); ok {
		if status, parseErr := strconv.Atoi(after); parseErr == nil && status >= 100 && status <= 599 {
			return fmt.Sprintf("http_status_%d", status)
		}
	}
	for _, failure := range []struct{ prefix, reason string }{
		{"parse url:", "invalid_url"},
		{"unsupported scheme", "invalid_or_nonpublic_url"},
		{"missing host", "invalid_or_nonpublic_url"},
		{"blocked private", "invalid_or_nonpublic_url"},
		{"host ", "invalid_or_nonpublic_url"},
		{"resolve host", "dns_lookup_failed"},
		{"build request:", "invalid_request"},
		{"invalid content-type", "non_image_content_type"},
		{"invalid image payload content-type", "non_image_payload"},
		{"image exceeds max size", "image_too_large"},
		{"read body:", "body_read_failed"},
		{"write image:", "local_write_failed"},
		{"empty image", "empty_image"},
	} {
		if strings.HasPrefix(message, failure.prefix) {
			return failure.reason
		}
	}
	return "request_failed"
}

func buildDescriptionSlotImageName(rawURL string, slotOrder int) string {
	normalizedURL := bbcode.NormalizeImageRawURL(strings.TrimSpace(rawURL))
	parsed, err := url.Parse(normalizedURL)
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
		ext = ".png"
	}
	stem := strings.TrimSuffix(base, path.Ext(base))
	digest := sha256.Sum256([]byte(normalizedURL))
	return fmt.Sprintf("%s_%03d_%x%s", stem, slotOrder+1, digest[:6], ext)
}

func downloadDescriptionSlotImage(ctx context.Context, client *http.Client, rawURL string, outPath string) error {
	rawURL = imagehost.DirectImageURL(rawURL)
	if rawURL == "" {
		return errors.New("invalid image proxy source")
	}
	rawURL = PTPDescriptionImageDownloadURL(rawURL)
	if err := validateDescriptionSlotImageURL(ctx, rawURL); err != nil {
		return err
	}
	client = descriptionSlotImageHTTPClient(client)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
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
	avifPayload := isAVIFImagePayload(payload)
	headerAllowed := contentType == "" || isDescriptionSlotImageContentType(contentType)
	if avifPayload && strings.HasPrefix(contentType, "application/octet-stream") {
		headerAllowed = true
	}
	if !headerAllowed {
		return fmt.Errorf("invalid content-type %q", contentType)
	}
	if !isDescriptionSlotImageContentType(detectedContentType) && !avifPayload {
		return fmt.Errorf("invalid image payload content-type %q", detectedContentType)
	}
	if err := os.WriteFile(outPath, payload, 0o600); err != nil {
		return fmt.Errorf("write image: %w", err)
	}
	return nil
}

func isPTPDescriptionImageURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "passthepopcorn.me" || strings.HasSuffix(host, ".passthepopcorn.me")
}

// PTPDescriptionImageDownloadURL upgrades legacy PTP image links to HTTPS while
// retaining their original URL for cache and metadata identity at the caller.
func PTPDescriptionImageDownloadURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if isPTPDescriptionImageURL(rawURL) {
		parsed, err := url.Parse(rawURL)
		if err == nil && parsed.Scheme == "http" {
			parsed.Scheme = "https"
			return parsed.String()
		}
	}
	return rawURL
}

// PTPDescriptionImageHTTPClient refreshes PTP's image cookie with the saved web
// session and sends those cookies only to HTTPS requests on PTP's domain.
func PTPDescriptionImageHTTPClient(ctx context.Context, client *http.Client, cfg config.Config, logger api.Logger) *http.Client {
	storedCookies, err := cookiepkg.LoadTrackerCookieMap(ctx, cfg.MainSettings.DBPath, "PTP")
	if err != nil && !errors.Is(err, cookiepkg.ErrTrackerCookiesNotFound) {
		if logger != nil {
			logger.Warnf("trackers: PTP description image session unavailable reason=cookie_load_failed")
		}
		storedCookies = nil
	}
	if len(storedCookies) == 0 {
		if logger != nil {
			logger.Warnf("trackers: PTP description image session unavailable reason=no_session_cookies")
		}
		return client
	}
	imageCookie, reason := refreshPTPDescriptionImageCookie(ctx, client, storedCookies)
	if imageCookie != "" {
		storedCookies["img"] = imageCookie
	} else if logger != nil {
		logger.Warnf("trackers: PTP description image cookie unavailable reason=%s", reason)
	}
	if logger != nil {
		logger.Debugf(
			"trackers: PTP description image web session configured session_cookies=%d image_cookie=%t",
			len(storedCookies),
			storedCookies["img"] != "",
		)
	}
	cloned := *client
	base := cloned.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	cloned.Transport = &ptpDescriptionImageTransport{
		base:    base,
		cookies: cookiepkg.CookieMapToHTTPCookies(storedCookies, "passthepopcorn.me"),
	}
	return &cloned
}

func refreshPTPDescriptionImageCookie(ctx context.Context, client *http.Client, storedCookies map[string]string) (string, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://passthepopcorn.me/upload.php", nil)
	if err != nil {
		return "", "invalid_request"
	}
	req.Header.Set("User-Agent", "upbrr")
	for _, cookie := range cookiepkg.CookieMapToHTTPCookies(storedCookies, "passthepopcorn.me") {
		req.AddCookie(cookie)
	}
	webClient := *client
	webClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := webClient.Do(req)
	if err != nil {
		return "", "request_failed"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Sprintf("http_status_%d", resp.StatusCode)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "img" && cookie.Value != "" {
			return cookie.Value, ""
		}
	}
	return "", "missing_img_cookie"
}

type ptpDescriptionImageTransport struct {
	base    http.RoundTripper
	cookies []*http.Cookie
}

// RoundTrip adds saved web cookies to HTTPS requests on PTP's domain and
// subdomains at the default TLS port, then delegates to the base transport.
func (t *ptpDescriptionImageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" && isPTPDescriptionImageURL(req.URL.String()) && (req.URL.Port() == "" || req.URL.Port() == "443") {
		authenticated := req.Clone(req.Context())
		for _, cookie := range t.cookies {
			authenticated.AddCookie(cookie)
		}
		req = authenticated
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("description image request: %w", err)
	}
	return resp, nil
}

func isAVIFImagePayload(payload []byte) bool {
	return len(payload) >= 12 && string(payload[4:8]) == "ftyp" &&
		(string(payload[8:12]) == "avif" || string(payload[8:12]) == "avis")
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
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() ||
		addr.IsUnspecified() {
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

func prioritizedTrackerRecords(meta api.UploadSubject, tracker string) []api.TrackerMetadata {
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

func sanitizePersistedTrackerArtifactName(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-' || r == '_' || r == '.':
			return r
		default:
			return '_'
		}
	}, strings.TrimSpace(value))
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
	base = sanitizePersistedTrackerArtifactName(base)
	digest := sha256.Sum256([]byte(rawURL))
	suffix := fmt.Sprintf("_%02d_%x", index+1, digest[:6])
	if !strings.Contains(base, ".") {
		return base + suffix
	}
	parts := strings.Split(base, ".")
	ext := parts[len(parts)-1]
	return strings.TrimSuffix(base, "."+ext) + suffix + "." + ext
}

// localTrackerArtifactPaths looks up saved images by direct source identity
// for wsrv proxies, leaving older proxy-keyed thumbnails ineligible.
func localTrackerArtifactPaths(dir string, rawURL string, index int) []string {
	if imagehost.IsWsrvProxyURL(rawURL) {
		rawURL = imagehost.DirectImageURL(rawURL)
		if rawURL == "" {
			return nil
		}
	}
	exactName := buildTrackerArtifactImageName(rawURL, index)
	paths := []string{filepath.Join(dir, exactName)}
	legacy := legacyTrackerArtifactImageName(rawURL, 0)
	ext := path.Ext(legacy)
	stem := strings.TrimSuffix(strings.TrimSuffix(legacy, ext), "_01")
	digest := sha256.Sum256([]byte(rawURL))
	prefix := stem + "_"
	suffix := fmt.Sprintf("_%x%s", digest[:6], ext)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if name == exactName || entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
				continue
			}
			indexText := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
			if value, err := strconv.Atoi(indexText); err == nil && value > 0 {
				paths = append(paths, filepath.Join(dir, name))
			}
		}
	}
	return append(paths, filepath.Join(dir, legacyTrackerArtifactImageName(rawURL, index)))
}

func legacyTrackerArtifactImageName(rawURL string, index int) string {
	parsed, err := url.Parse(rawURL)
	base := ""
	if err == nil {
		base = path.Base(parsed.Path)
	}
	if base == "" || base == "." || base == "/" {
		base = "image"
	}
	base = sanitizePersistedTrackerArtifactName(base)
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
