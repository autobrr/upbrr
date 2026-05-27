// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/services/imagehost"
	"github.com/autobrr/upbrr/pkg/api"
)

type DescriptionAssets struct {
	Description string
	Screenshots []api.ScreenshotImage
	Slots       []api.ScreenshotSlot
	Override    bool
}

var embeddedNFOBlockPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)\[(?:center|align=center)\]\s*\[spoiler=.*? nfo:\]\[code\].*?\[/code\]\[/spoiler\]\s*\[/(?:center|align)\]`),
	regexp.MustCompile(`(?is)\[hide=(?:Scene|FraMeSToR) NFO:\]\[pre\].*?\[/pre\]\[/hide\]`),
}

var descriptionSpacingPattern = regexp.MustCompile(`\n{3,}`)
var defaultSignaturePattern = regexp.MustCompile(`(?is)\[(?:right|align=right)\]\s*\[url=https://github\.com/(?:Audionut|autobrr)/upbrr\].*?\[/url\]\s*\[/(?:right|align)\]`)

type preloadedDescriptionAssetData struct {
	descriptionOverrides  map[string]api.DescriptionOverride
	groupDescriptions     map[string]string
	trackerDescriptions   map[string]string
	ambiguousTrackers     map[string]struct{}
	trackerRecords        []api.TrackerMetadata
	selections            []api.ScreenshotFinalSelection
	uploads               []api.UploadedImageLink
	screenshotSlots       []api.ScreenshotSlot
	screenshotSlotsLoaded bool
}

func clonePreloadedDescriptionAssetData(preloaded *preloadedDescriptionAssetData) *preloadedDescriptionAssetData {
	if preloaded == nil {
		return nil
	}
	return &preloadedDescriptionAssetData{
		descriptionOverrides:  cloneDescriptionOverrides(preloaded.descriptionOverrides),
		groupDescriptions:     cloneStringMap(preloaded.groupDescriptions),
		trackerDescriptions:   cloneStringMap(preloaded.trackerDescriptions),
		ambiguousTrackers:     cloneStringSet(preloaded.ambiguousTrackers),
		trackerRecords:        cloneTrackerMetadata(preloaded.trackerRecords),
		selections:            append([]api.ScreenshotFinalSelection(nil), preloaded.selections...),
		uploads:               append([]api.UploadedImageLink(nil), preloaded.uploads...),
		screenshotSlots:       cloneScreenshotSlots(preloaded.screenshotSlots),
		screenshotSlotsLoaded: preloaded.screenshotSlotsLoaded,
	}
}

func cloneDescriptionOverrides(values map[string]api.DescriptionOverride) map[string]api.DescriptionOverride {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]api.DescriptionOverride, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneStringSet(values map[string]struct{}) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]struct{}, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneTrackerMetadata(records []api.TrackerMetadata) []api.TrackerMetadata {
	if len(records) == 0 {
		return nil
	}
	cloned := make([]api.TrackerMetadata, len(records))
	for idx := range records {
		cloned[idx] = records[idx]
		cloned[idx].ImageURLs = append([]string(nil), records[idx].ImageURLs...)
	}
	return cloned
}

func DescriptionOverrideGroupForTracker(tracker string) string {
	normalized := strings.ToUpper(strings.TrimSpace(tracker))
	switch {
	case normalized == "":
		return ""
	case IsUnit3DTracker(normalized):
		if normalized == "ACM" {
			return "acm"
		}
		return "unit3d"
	default:
		return strings.ToLower(normalized)
	}
}

func normalizeDescriptionOverrideGroupKey(groupKey string) string {
	return strings.ToLower(strings.TrimSpace(groupKey))
}

func ResolveDescriptionAssets(ctx context.Context, tracker string, meta api.PreparedMetadata, repo api.MetadataRepository, logger api.Logger) (DescriptionAssets, error) {
	return resolveDescriptionAssets(ctx, tracker, meta, repo, logger, nil)
}

func LogDescriptionAssetResolutionFailure(logger api.Logger, tracker string, err error) {
	if err == nil || logger == nil {
		return
	}
	logger.Warnf(
		"trackers: %s description assets unavailable, continuing with empty assets: %v",
		strings.ToUpper(strings.TrimSpace(tracker)),
		err,
	)
}

func resolveDescriptionAssets(ctx context.Context, tracker string, meta api.PreparedMetadata, repo api.MetadataRepository, logger api.Logger, preloaded *preloadedDescriptionAssetData) (DescriptionAssets, error) {
	if err := ctx.Err(); err != nil {
		return DescriptionAssets{}, fmt.Errorf("trackers: resolve description assets canceled: %w", err)
	}
	if repo == nil || strings.TrimSpace(meta.SourcePath) == "" {
		description := meta.DescriptionOverride
		if canonical := descriptionGroupFromPreparedMeta(meta, tracker, preloaded); strings.TrimSpace(canonical) != "" {
			description = canonical
		}
		description = sanitizeTrackerDescription(tracker, description)
		return DescriptionAssets{Description: description, Override: strings.TrimSpace(description) != ""}, nil
	}
	if logger != nil {
		logger.Tracef("trackers: description assets start tracker=%s source=%s", strings.TrimSpace(tracker), meta.SourcePath)
	}

	description, overridden := resolveTrackerDescription(ctx, tracker, meta, repo, logger, preloaded)
	slots, screenshots, err := resolveDescriptionScreenshots(ctx, tracker, meta, repo, logger, preloaded)
	if err != nil {
		if logger != nil {
			logger.Warnf("trackers: description assets screenshots degraded for %s: %v", strings.TrimSpace(tracker), err)
		}
		slots = nil
		screenshots = nil
	}
	if logger != nil {
		logger.Tracef("trackers: description assets resolved desc_len=%d screenshots=%d", len(strings.TrimSpace(description)), len(screenshots))
	}
	return DescriptionAssets{Description: sanitizeTrackerDescription(tracker, description), Screenshots: screenshots, Slots: slots, Override: overridden}, nil
}

func resolveTrackerDescription(ctx context.Context, tracker string, meta api.PreparedMetadata, repo api.MetadataRepository, logger api.Logger, preloaded *preloadedDescriptionAssetData) (string, bool) {
	if err := ctx.Err(); err != nil {
		return "", false
	}
	if canonical := descriptionGroupFromPreparedMeta(meta, tracker, preloaded); strings.TrimSpace(canonical) != "" {
		if logger != nil {
			logger.Tracef("trackers: canonical group description applied source=%s tracker=%s len=%d", meta.SourcePath, strings.TrimSpace(tracker), len(strings.TrimSpace(canonical)))
		}
		return canonical, true
	}
	if trimmed := strings.TrimSpace(meta.DescriptionOverride); trimmed != "" {
		if logger != nil {
			logger.Tracef("trackers: request description override applied source=%s len=%d", meta.SourcePath, len(trimmed))
		}
		return meta.DescriptionOverride, true
	}
	if repo != nil && strings.TrimSpace(meta.SourcePath) != "" {
		for _, groupKey := range descriptionOverrideLookupKeys(meta.DescriptionGroups, tracker) {
			override, err := descriptionOverrideFromSource(ctx, meta, repo, groupKey, preloaded)
			if err == nil {
				trimmed := strings.TrimSpace(override.Description)
				if trimmed != "" {
					if logger != nil {
						logger.Tracef("trackers: description override applied source=%s group=%s len=%d", meta.SourcePath, strings.TrimSpace(groupKey), len(trimmed))
					}
					return override.Description, true
				}
				continue
			}
			if !errors.Is(err, internalerrors.ErrNotFound) {
				if logger != nil {
					logger.Debugf("trackers: description override lookup failed group=%s: %v", strings.TrimSpace(groupKey), err)
				}
				break
			}
		}
	}
	records, err := trackerMetadataFromSource(ctx, meta, repo, preloaded)
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: description assets failed to load tracker metadata: %v", err)
		}
		records = nil
	}
	combined := mergeTrackerMetadata(records, meta.TrackerData)
	if filtered := filterTrackerMetadataByName(combined, tracker); len(filtered) > 0 {
		combined = filtered
	}
	result := combineDescriptions(combined)
	if logger != nil {
		logger.Tracef("trackers: description assets description sources db=%d meta=%d combined=%d desc_len=%d", len(records), len(meta.TrackerData), len(combined), len(strings.TrimSpace(result)))
	}
	return result, false
}

func descriptionGroupFromPreparedMeta(meta api.PreparedMetadata, tracker string, preloaded *preloadedDescriptionAssetData) string {
	if len(meta.DescriptionGroups) == 0 {
		return ""
	}

	groupDescriptions, trackerDescriptions, ambiguousTrackers := preparedDescriptionGroupLookups(meta.DescriptionGroups, preloaded)
	if len(groupDescriptions) == 0 && len(trackerDescriptions) == 0 && len(ambiguousTrackers) == 0 {
		return ""
	}

	for _, groupKey := range descriptionOverrideLookupKeys(meta.DescriptionGroups, tracker) {
		if description, ok := groupDescriptions[strings.ToUpper(strings.TrimSpace(groupKey))]; ok {
			return description
		}
	}

	normalizedTracker := strings.ToUpper(strings.TrimSpace(tracker))
	if normalizedTracker == "" {
		return ""
	}
	if _, ambiguous := ambiguousTrackers[normalizedTracker]; ambiguous {
		return ""
	}
	if description, ok := trackerDescriptions[normalizedTracker]; ok {
		return description
	}
	return ""
}

func descriptionOverrideLookupKeys(groups []api.DescriptionBuilderGroup, tracker string) []string {
	keys := matchingPreparationDescriptionGroupKeys(groups, tracker)
	canonical := strings.TrimSpace(DescriptionOverrideGroupForTracker(tracker))
	if canonical == "" {
		return keys
	}
	return appendUniqueDescriptionGroupKey(keys, canonical)
}

func matchingPreparationDescriptionGroupKeys(groups []api.DescriptionBuilderGroup, tracker string) []string {
	if len(groups) == 0 {
		return nil
	}

	normalizedTracker := strings.ToUpper(strings.TrimSpace(tracker))
	if normalizedTracker == "" {
		return nil
	}

	canonicalGroup := strings.ToLower(strings.TrimSpace(DescriptionOverrideGroupForTracker(tracker)))
	if canonicalGroup == "" {
		return nil
	}

	type candidate struct {
		key   string
		score int
		order int
	}

	candidates := make([]candidate, 0, len(groups))
	for idx, group := range groups {
		key := strings.TrimSpace(group.GroupKey)
		if key == "" {
			continue
		}
		if !descriptionGroupMatchesTracker(group, canonicalGroup) {
			continue
		}
		_, host, usageScope := parsePreparationDescriptionGroupKey(key)
		score := 0
		if usageScope == trackerImageUsageScope(normalizedTracker) {
			score += 4
		} else if usageScope == globalImageUsageScope {
			score += 2
		}
		if host == strings.ToLower(normalizedTracker) {
			score++
		}
		candidates = append(candidates, candidate{key: key, score: score, order: idx})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].order < candidates[j].order
	})

	keys := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		keys = appendUniqueDescriptionGroupKey(keys, candidate.key)
	}
	return keys
}

func descriptionGroupMatchesTracker(group api.DescriptionBuilderGroup, canonicalGroup string) bool {
	baseGroup, _, _ := parsePreparationDescriptionGroupKey(group.GroupKey)
	return strings.EqualFold(strings.TrimSpace(baseGroup), canonicalGroup)
}

func parsePreparationDescriptionGroupKey(groupKey string) (string, string, string) {
	trimmed := strings.TrimSpace(groupKey)
	if trimmed == "" {
		return "", "", globalImageUsageScope
	}
	parts := strings.SplitN(trimmed, "|", 3)
	baseGroup := strings.ToLower(strings.TrimSpace(parts[0]))
	if len(parts) == 1 {
		return baseGroup, "", globalImageUsageScope
	}
	host := strings.ToLower(strings.TrimSpace(parts[1]))
	usageScope := globalImageUsageScope
	if len(parts) == 3 {
		usageScope = normalizeUsageScope(parts[2])
	}
	return baseGroup, host, usageScope
}

func appendUniqueDescriptionGroupKey(keys []string, groupKey string) []string {
	trimmed := strings.TrimSpace(groupKey)
	if trimmed == "" {
		return keys
	}
	for _, existing := range keys {
		if strings.EqualFold(strings.TrimSpace(existing), trimmed) {
			return keys
		}
	}
	return append(keys, trimmed)
}

func preparedDescriptionGroupLookups(groups []api.DescriptionBuilderGroup, preloaded *preloadedDescriptionAssetData) (map[string]string, map[string]string, map[string]struct{}) {
	if preloaded != nil && (preloaded.groupDescriptions != nil || preloaded.trackerDescriptions != nil || preloaded.ambiguousTrackers != nil) {
		return preloaded.groupDescriptions, preloaded.trackerDescriptions, preloaded.ambiguousTrackers
	}

	groupDescriptions := make(map[string]string, len(groups))
	trackerDescriptions := make(map[string]string)
	ambiguousTrackers := make(map[string]struct{})
	for _, group := range groups {
		normalizedGroupKey := strings.TrimSpace(group.GroupKey)
		if normalizedGroupKey != "" {
			groupDescriptions[strings.ToUpper(normalizedGroupKey)] = group.RawDescription
		}
		for _, candidate := range group.Trackers {
			normalizedTracker := strings.ToUpper(strings.TrimSpace(candidate))
			if normalizedTracker == "" {
				continue
			}
			if _, ambiguous := ambiguousTrackers[normalizedTracker]; ambiguous {
				continue
			}
			if existing, ok := trackerDescriptions[normalizedTracker]; ok && !strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(group.RawDescription)) {
				delete(trackerDescriptions, normalizedTracker)
				ambiguousTrackers[normalizedTracker] = struct{}{}
				continue
			}
			trackerDescriptions[normalizedTracker] = group.RawDescription
		}
	}

	if preloaded != nil {
		preloaded.groupDescriptions = groupDescriptions
		preloaded.trackerDescriptions = trackerDescriptions
		preloaded.ambiguousTrackers = ambiguousTrackers
	}

	return groupDescriptions, trackerDescriptions, ambiguousTrackers
}

func mergeTrackerMetadata(primary []api.TrackerMetadata, fallback []api.TrackerMetadata) []api.TrackerMetadata {
	if len(primary) == 0 && len(fallback) == 0 {
		return nil
	}
	combined := make([]api.TrackerMetadata, 0, len(primary)+len(fallback))
	combined = append(combined, primary...)
	combined = append(combined, fallback...)
	return combined
}

func resolveDescriptionScreenshots(ctx context.Context, tracker string, meta api.PreparedMetadata, repo api.MetadataRepository, logger api.Logger, preloaded *preloadedDescriptionAssetData) ([]api.ScreenshotSlot, []api.ScreenshotImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("trackers: resolve description screenshots canceled: %w", err)
	}
	slots, err := screenshotSlotsFromSource(ctx, tracker, meta, repo, logger, preloaded)
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: description assets failed to load screenshot slots: %v", err)
		}
		slots = nil
	}
	images, _, _, err := selectScreenshotsFromSlots(tracker, slots, imageHostPolicy{})
	if err != nil {
		if logger != nil {
			logger.Warnf("trackers: description assets slot screenshot resolution failed tracker=%s: %v", strings.TrimSpace(tracker), err)
		}
		images = nil
	}
	if len(images) > 0 {
		if logger != nil {
			logger.Tracef("trackers: description assets screenshots source=slots slots=%d resolved=%d", len(slots), len(images))
		}
		return slots, images, nil
	}

	urls := resolveTrackerImageURLs(ctx, tracker, meta, repo, logger, preloaded)
	if logger != nil {
		logger.Tracef("trackers: description assets screenshots source=tracker_urls tracker=%s urls=%d", strings.TrimSpace(tracker), len(urls))
	}
	return nil, resolveTrackerScreenshots(urls), nil
}

func preloadDescriptionAssetData(ctx context.Context, meta api.PreparedMetadata, repo api.MetadataRepository) (*preloadedDescriptionAssetData, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: preload description assets canceled: %w", err)
	}
	if repo == nil || strings.TrimSpace(meta.SourcePath) == "" {
		return nil, nil
	}

	preloaded := &preloadedDescriptionAssetData{
		descriptionOverrides: make(map[string]api.DescriptionOverride),
	}
	preloaded.groupDescriptions, preloaded.trackerDescriptions, preloaded.ambiguousTrackers = preparedDescriptionGroupLookups(meta.DescriptionGroups, nil)

	overrides, err := repo.ListDescriptionOverridesByPath(ctx, meta.SourcePath)
	switch {
	case err == nil:
		for _, override := range overrides {
			normalizedGroupKey := normalizeDescriptionOverrideGroupKey(override.GroupKey)
			if normalizedGroupKey == "" {
				continue
			}
			preloaded.descriptionOverrides[normalizedGroupKey] = override
		}
	case errors.Is(err, internalerrors.ErrNotFound):
	default:
		return nil, fmt.Errorf("trackers: %w", err)
	}

	trackerRecords, err := repo.ListTrackerMetadataByPath(ctx, meta.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	preloaded.trackerRecords = trackerRecords

	selections, err := repo.ListFinalSelections(ctx, meta.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	preloaded.selections = selections

	uploads, err := repo.ListUploadedImagesByPath(ctx, meta.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	preloaded.uploads = uploads

	slots, err := screenshotSlotsFromSource(ctx, "", meta, repo, nil, preloaded)
	if err != nil {
		return nil, err
	}
	preloaded.screenshotSlots = slots
	preloaded.screenshotSlotsLoaded = true

	return preloaded, nil
}

func descriptionOverrideFromSource(ctx context.Context, meta api.PreparedMetadata, repo api.MetadataRepository, groupKey string, preloaded *preloadedDescriptionAssetData) (api.DescriptionOverride, error) {
	if err := ctx.Err(); err != nil {
		return api.DescriptionOverride{}, fmt.Errorf("trackers: load description override canceled: %w", err)
	}
	normalizedGroupKey := normalizeDescriptionOverrideGroupKey(groupKey)
	if preloaded != nil {
		if override, ok := preloaded.descriptionOverrides[normalizedGroupKey]; ok {
			return override, nil
		}
		return api.DescriptionOverride{}, internalerrors.ErrNotFound
	}
	override, err := repo.GetDescriptionOverride(ctx, meta.SourcePath, normalizedGroupKey)
	if err == nil {
		return override, nil
	}
	return api.DescriptionOverride{}, fmt.Errorf("trackers: %w", err)
}

func trackerMetadataFromSource(ctx context.Context, meta api.PreparedMetadata, repo api.MetadataRepository, preloaded *preloadedDescriptionAssetData) ([]api.TrackerMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: load tracker metadata canceled: %w", err)
	}
	if preloaded != nil {
		return preloaded.trackerRecords, nil
	}
	return wrapTrackerResult(repo.ListTrackerMetadataByPath(ctx, meta.SourcePath))
}

func finalSelectionsFromSource(ctx context.Context, meta api.PreparedMetadata, repo api.MetadataRepository, preloaded *preloadedDescriptionAssetData) ([]api.ScreenshotFinalSelection, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: load final selections canceled: %w", err)
	}
	if preloaded != nil {
		return preloaded.selections, nil
	}
	return wrapTrackerResult(repo.ListFinalSelections(ctx, meta.SourcePath))
}

func uploadedImagesFromSource(ctx context.Context, meta api.PreparedMetadata, repo api.MetadataRepository, preloaded *preloadedDescriptionAssetData) ([]api.UploadedImageLink, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: load uploaded images canceled: %w", err)
	}
	if preloaded != nil {
		return preloaded.uploads, nil
	}
	return wrapTrackerResult(repo.ListUploadedImagesByPath(ctx, meta.SourcePath))
}

func resolveTrackerImageURLs(ctx context.Context, tracker string, meta api.PreparedMetadata, repo api.MetadataRepository, logger api.Logger, preloaded *preloadedDescriptionAssetData) []string {
	if err := ctx.Err(); err != nil {
		return nil
	}
	trackerKey := strings.TrimSpace(tracker)
	records, err := trackerMetadataFromSource(ctx, meta, repo, preloaded)
	if err == nil {
		if len(records) > 0 {
			if trackerKey != "" {
				filtered := filterTrackerMetadataByName(records, trackerKey)
				if len(filtered) > 0 {
					if logger != nil {
						logger.Tracef("trackers: description assets tracker urls source=db tracker=%s records=%d filtered=%d", trackerKey, len(records), len(filtered))
					}
					return collectImageURLs(filtered)
				}
			}
			if logger != nil {
				logger.Tracef("trackers: description assets tracker urls source=db tracker=%s records=%d", trackerKey, len(records))
			}
			return collectImageURLs(records)
		}
	} else if logger != nil {
		logger.Debugf("trackers: description assets failed to load tracker image urls: %v", err)
	}
	if trackerKey != "" {
		filtered := filterTrackerMetadataByName(meta.TrackerData, trackerKey)
		if len(filtered) > 0 {
			if logger != nil {
				logger.Tracef("trackers: description assets tracker urls source=meta tracker=%s records=%d filtered=%d", trackerKey, len(meta.TrackerData), len(filtered))
			}
			return collectImageURLs(filtered)
		}
	}
	if logger != nil {
		logger.Tracef("trackers: description assets tracker urls source=meta tracker=%s records=%d", trackerKey, len(meta.TrackerData))
	}
	return collectImageURLs(meta.TrackerData)
}

func filterTrackerMetadataByName(records []api.TrackerMetadata, tracker string) []api.TrackerMetadata {
	if len(records) == 0 || strings.TrimSpace(tracker) == "" {
		return nil
	}
	needle := strings.ToUpper(strings.TrimSpace(tracker))
	filtered := make([]api.TrackerMetadata, 0, len(records))
	for _, record := range records {
		if strings.ToUpper(strings.TrimSpace(record.Tracker)) != needle {
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func resolveTrackerScreenshots(urls []string) []api.ScreenshotImage {
	if len(urls) == 0 {
		return nil
	}
	hostCounts := make(map[string]int)
	for _, rawURL := range urls {
		trimmed := strings.TrimSpace(rawURL)
		if trimmed == "" {
			continue
		}
		if isTMDBImageURL(trimmed) {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(imagehost.ExtractHost(trimmed)))
		if host == "" {
			continue
		}
		hostCounts[host]++
	}
	selectedHost := pickMostCommonHost(hostCounts)
	if selectedHost == "" {
		return nil
	}

	results := make([]api.ScreenshotImage, 0, len(urls))
	for _, rawURL := range urls {
		trimmed := strings.TrimSpace(rawURL)
		if trimmed == "" {
			continue
		}
		if isTMDBImageURL(trimmed) {
			continue
		}
		host := strings.TrimSpace(imagehost.ExtractHost(trimmed))
		normalizedHost := strings.ToLower(host)
		if selectedHost != "" && normalizedHost != selectedHost {
			continue
		}
		results = append(results, api.ScreenshotImage{
			Index:  freshScreenshotImageIndex(results),
			Host:   host,
			ImgURL: trimmed,
			RawURL: trimmed,
			WebURL: trimmed,
		})
	}
	return results
}

func pickMostCommonHost(counts map[string]int) string {
	best := ""
	bestCount := 0
	for host, count := range counts {
		if count > bestCount || (count == bestCount && (best == "" || host < best)) {
			best = host
			bestCount = count
		}
	}
	return best
}

func collectImageURLs(records []api.TrackerMetadata) []string {
	if len(records) == 0 {
		return nil
	}
	ordered := make([]api.TrackerMetadata, 0, len(records))
	ordered = append(ordered, records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := ordered[i]
		right := ordered[j]
		if !left.UpdatedAt.IsZero() || !right.UpdatedAt.IsZero() {
			if left.UpdatedAt.After(right.UpdatedAt) {
				return true
			}
			if left.UpdatedAt.Before(right.UpdatedAt) {
				return false
			}
		}
		return strings.ToUpper(left.Tracker) < strings.ToUpper(right.Tracker)
	})
	urls := make([]string, 0)
	seen := make(map[string]struct{})
	for _, record := range ordered {
		for _, url := range record.ImageURLs {
			trimmed := strings.TrimSpace(url)
			if trimmed == "" {
				continue
			}
			if isTMDBImageURL(trimmed) {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			urls = append(urls, trimmed)
		}
	}
	return urls
}

func isTMDBImageURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(lower, "tmdb.org")
}

func combineDescriptions(records []api.TrackerMetadata) string {
	if len(records) == 0 {
		return ""
	}
	ordered := make([]api.TrackerMetadata, 0, len(records))
	ordered = append(ordered, records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := ordered[i]
		right := ordered[j]
		if !left.UpdatedAt.IsZero() || !right.UpdatedAt.IsZero() {
			if left.UpdatedAt.After(right.UpdatedAt) {
				return true
			}
			if left.UpdatedAt.Before(right.UpdatedAt) {
				return false
			}
		}
		return strings.ToUpper(left.Tracker) < strings.ToUpper(right.Tracker)
	})
	seen := make(map[string]struct{})
	parts := make([]string, 0, len(ordered))
	for _, record := range ordered {
		trimmed := strings.TrimSpace(record.Description)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, "\n\n")
}

func stripEmbeddedNFOBlocks(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	cleaned := trimmed
	for _, pattern := range embeddedNFOBlockPatterns {
		cleaned = pattern.ReplaceAllString(cleaned, "")
	}
	cleaned = descriptionSpacingPattern.ReplaceAllString(cleaned, "\n\n")
	return strings.TrimSpace(cleaned)
}

func sanitizeTrackerDescription(tracker string, value string) string {
	cleaned := stripEmbeddedNFOBlocks(value)
	switch strings.ToUpper(strings.TrimSpace(tracker)) {
	case "ANT", "NBL":
		cleaned = defaultSignaturePattern.ReplaceAllString(cleaned, "")
		cleaned = descriptionSpacingPattern.ReplaceAllString(cleaned, "\n\n")
		return strings.TrimSpace(cleaned)
	default:
		return cleaned
	}
}
