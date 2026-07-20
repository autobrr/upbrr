// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	"github.com/autobrr/upbrr/pkg/api"
)

// DescriptionAssets contains persisted description text and images selected for one tracker.
type DescriptionAssets struct {
	// Description is the persisted or generated BBCode selected for the tracker.
	Description string
	// Screenshots contains non-menu images selected for the final description.
	Screenshots []api.ScreenshotImage
	// MenuImages contains selected disc-menu images.
	MenuImages []api.ScreenshotImage
	// Slots preserves screenshot ordering and uploaded variants.
	Slots []api.ScreenshotSlot
	// Override reports that Description came from a saved user override.
	Override bool
	// Final reports that screenshot selection has been finalized.
	Final bool
}

// PreparedDescriptionAssets returns a defensive copy of module-resolved image
// and slot slices. Nil input returns zero assets; it currently returns no error.
func PreparedDescriptionAssets(prepared *DescriptionAssets) (DescriptionAssets, error) {
	if prepared == nil {
		return DescriptionAssets{}, nil
	}
	assets := *prepared
	assets.Screenshots = append([]api.ScreenshotImage(nil), prepared.Screenshots...)
	assets.MenuImages = append([]api.ScreenshotImage(nil), prepared.MenuImages...)
	assets.Slots = cloneScreenshotSlots(prepared.Slots)
	return assets, nil
}

var embeddedNFOBlockPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)\[(?:center|align=center)\]\s*\[spoiler=.*? nfo:\]\[code\].*?\[/code\]\[/spoiler\]\s*\[/(?:center|align)\]`),
	regexp.MustCompile(`(?is)\[hide=(?:Scene|FraMeSToR) NFO:\]\[pre\].*?\[/pre\]\[/hide\]`),
}

var descriptionSpacingPattern = regexp.MustCompile(`\n{3,}`)

var defaultSignaturePattern = regexp.MustCompile(
	`(?is)\[(?:right|align=right)\]\s*\[url=https://github\.com/(?:Audionut|autobrr)/upbrr\].*?\[/url\]\s*\[/(?:right|align)\]`,
)

var unit3DBotSignaturePattern = regexp.MustCompile(
	`(?is)\[(?:center|right|align=right)\]\s*(?:\[img=\d+\]https://blutopia\.xyz/favicon\.ico\[/img\]\s*)?(?:\[b\]\s*Uploaded\s+Using\s+\[url=https://github\.com/HDInnovations/UNIT3D\]UNIT3D\[/url\]\s+Auto\s+Uploader\s*\[/b\]|Uploaded\s+Using\s+\[url=https://github\.com/HDInnovations/UNIT3D\]UNIT3D\[/url\]\s+Auto\s+Uploader)(?:\s*\[img=\d+\]https://blutopia\.xyz/favicon\.ico\[/img\])?\s*\[/(?:center|right|align)\]`,
)

var knownBotSignaturePattern = regexp.MustCompile(
	`(?is)(?:\[center\]\s*\[url=https://github\.com/z-ink/uploadrr\]\[img=\d+\]https://i\.ibb\.co/2NVWb0c/uploadrr\.webp\[/img\]\[/url\]\s*\[/center\])|(?:\[center\]\s*\[url=https://github\.com/edge20200/Only-Uploader\]Powered\s+by\s+Only-Uploader\[/url\]\s*\[/center\])|(?:\[center\]\s*\[url=/torrents\?perPage=\d+&name=[^\]]*\]\s*\[/url\]\s*\[/center\])|(?:\[center\]\s*Find\s+our\s+uploads\s+\[url=https?://[^\]]*/torrents\?name=[^\]]+\].*?here.*?\[/url\]\s*\[/center\])|(?:\[center\]\s*(?:\[b\]\s*(?:\[size=\d+\])?brush(?:\[/size\])?\s*\[/b\]\s*)?This is an internal release which was first released exclusively on Aither\.\s*Cheers to all the Aither(?:\s+users)?\s*\[/center\])|(?:\[(?:center|right|align=right)\]\s*(?:\[url=[^\]]+\]\s*)?(?:\[size=[^\]]+\]\s*)?Created by(?:\s+[^[]*?)?\s*Upload Assistant(?:\s+v?\d+(?:\.\d+)*)?(?:\s*\[/size\])?(?:\s*\[/url\])?\s*\[/(?:center|right|align)\])|(?:\[(?:center|right|align=right)\]\s*Uploaded\s+with\s+(?:\[color=[^\]]+\]\s*)?\x{2764}(?:\s*\[/color\])?\s+using\s+GG-BOT\s+Upload\s+Assistant\s*\[/(?:center|right|align)\])`,
)
var knownBotImagePattern = regexp.MustCompile(`(?is)\[img(?:=[^\]]*)?\]\s*https://files\.catbox\.moe/5izwmx\.svg\s*\[/img\]`)
var emptyCenterPattern = regexp.MustCompile(`(?is)\[center\]\s*\[/center\]`)

type preloadedDescriptionAssetData struct {
	registry              *Registry
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

func preloadedRegistry(preloaded *preloadedDescriptionAssetData) *Registry {
	if preloaded == nil {
		return nil
	}
	return preloaded.registry
}

func firstRegistry(registries []*Registry) *Registry {
	if len(registries) == 0 {
		return nil
	}
	return registries[0]
}

func clonePreloadedDescriptionAssetData(preloaded *preloadedDescriptionAssetData) *preloadedDescriptionAssetData {
	if preloaded == nil {
		return nil
	}
	return &preloadedDescriptionAssetData{
		registry:              preloaded.registry,
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
	maps.Copy(cloned, values)
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	maps.Copy(cloned, values)
	return cloned
}

func cloneStringSet(values map[string]struct{}) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]struct{}, len(values))
	maps.Copy(cloned, values)
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

// DescriptionOverrideGroupForTrackerWithRegistry returns the registered group,
// falls back to "unit3d" for Unit3D definitions, and otherwise uses the
// lower-case tracker name. Unregistered trackers return empty.
func DescriptionOverrideGroupForTrackerWithRegistry(tracker string, registry *Registry) string {
	normalized := strings.ToUpper(strings.TrimSpace(tracker))
	if descriptor, ok := registry.LookupDescriptor(normalized); ok {
		if group := strings.ToLower(strings.TrimSpace(descriptor.DescriptionGroup)); group != "" {
			return group
		}
		if descriptor.Family == FamilyUnit3D {
			return "unit3d"
		}
		return strings.ToLower(normalized)
	}
	return ""
}

func normalizeDescriptionOverrideGroupKey(groupKey string) string {
	return strings.ToLower(strings.TrimSpace(groupKey))
}

// ResolveDescriptionAssets returns sanitized tracker description text and
// ordered persisted screenshot assets. With no repository or source path it
// resolves prepared/request description text only; required screenshot-slot
// failures abort the result while optional description lookups fall back.
func ResolveDescriptionAssets(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	logger api.Logger,
	registries ...*Registry,
) (DescriptionAssets, error) {
	return resolveDescriptionAssets(ctx, tracker, meta, repo, logger, nil, registries...)
}

// LogDescriptionAssetResolutionFailure records a redacted tracker asset-resolution failure.
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

func resolveDescriptionAssets(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
	registries ...*Registry,
) (DescriptionAssets, error) {
	registry := firstRegistry(registries)
	if registry == nil {
		registry = preloadedRegistry(preloaded)
	}
	if err := ctx.Err(); err != nil {
		return DescriptionAssets{}, fmt.Errorf("trackers: resolve description assets canceled: %w", err)
	}
	if repo == nil || strings.TrimSpace(meta.SourcePath) == "" {
		description := meta.DescriptionOverride
		final := false
		if canonical := descriptionGroupFromPreparedMeta(meta, tracker, preloaded, registry); strings.TrimSpace(canonical) != "" {
			description = canonical
			final = true
		}
		description = sanitizeTrackerDescription(tracker, description)
		hasDescription := strings.TrimSpace(description) != ""
		return DescriptionAssets{
			Description: description,
			Override:    hasDescription,
			Final:       final && hasDescription,
		}, nil
	}
	if logger != nil {
		logger.Tracef("trackers: description assets start tracker=%s source=%s", strings.TrimSpace(tracker), meta.SourcePath)
	}

	description, overridden, final := resolveTrackerDescription(ctx, tracker, meta, repo, logger, preloaded, registry)
	slots, screenshots, err := resolveDescriptionScreenshots(ctx, tracker, meta, repo, logger, preloaded, registry)
	if err != nil {
		return DescriptionAssets{}, fmt.Errorf("trackers: resolve required description screenshots: %w", err)
	}
	if logger != nil {
		logger.Tracef("trackers: description assets resolved desc_len=%d screenshots=%d", len(strings.TrimSpace(description)), len(screenshots))
	}

	menuImages, normalScreenshots := splitDescriptionScreenshots(ctx, meta, repo, preloaded, screenshots)

	description = sanitizeTrackerDescription(tracker, description)
	hasDescription := strings.TrimSpace(description) != ""
	return DescriptionAssets{
		Description: description,
		Screenshots: normalScreenshots,
		MenuImages:  menuImages,
		Slots:       slots,
		Override:    overridden && hasDescription,
		Final:       final && hasDescription,
	}, nil
}

func applyResolvedDescriptionScreenshots(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	preloaded *preloadedDescriptionAssetData,
	assets *DescriptionAssets,
	screenshots []api.ScreenshotImage,
) {
	if assets == nil {
		return
	}
	assets.Description = rewriteDescriptionSlotURLs(assets.Description, assets.Slots, screenshots, assets.Final)
	if assets.Final {
		assets.MenuImages = nil
		assets.Screenshots = nil
		return
	}
	assets.MenuImages, assets.Screenshots = splitResolvedDescriptionScreenshots(ctx, meta, repo, preloaded, assets.Slots, screenshots)
}

func splitResolvedDescriptionScreenshots(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	preloaded *preloadedDescriptionAssetData,
	slots []api.ScreenshotSlot,
	screenshots []api.ScreenshotImage,
) ([]api.ScreenshotImage, []api.ScreenshotImage) {
	if len(screenshots) == 0 {
		return nil, nil
	}
	if len(slots) == 0 {
		return splitDescriptionScreenshots(ctx, meta, repo, preloaded, screenshots)
	}
	renderable := renderableSlots(slots)
	filtered := make([]api.ScreenshotImage, 0, len(screenshots))
	for idx, screenshot := range screenshots {
		if idx < len(renderable) && renderable[idx].SectionKind == screenshotSectionComparison {
			continue
		}
		filtered = append(filtered, screenshot)
	}
	return splitDescriptionScreenshots(ctx, meta, repo, preloaded, filtered)
}

func splitDescriptionScreenshots(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	preloaded *preloadedDescriptionAssetData,
	screenshots []api.ScreenshotImage,
) ([]api.ScreenshotImage, []api.ScreenshotImage) {
	if len(screenshots) == 0 {
		return nil, nil
	}

	var selections []api.ScreenshotFinalSelection
	if repo != nil || preloaded != nil {
		selections, _ = finalSelectionsFromSource(ctx, meta, repo, preloaded)
	}
	menuPaths := make(map[string]struct{})
	for _, sel := range selections {
		if api.IsDiscMenuSelectionSource(sel.Source) && strings.TrimSpace(sel.ImagePath) != "" {
			menuPaths[strings.TrimSpace(sel.ImagePath)] = struct{}{}
		}
	}

	menuImages := make([]api.ScreenshotImage, 0)
	visitedMenuURLs := make(map[string]struct{})
	for _, shot := range screenshots {
		if !screenshotMatchesMenuPath(shot, menuPaths) {
			continue
		}
		menuImages = append(menuImages, shot)
		if u := screenshotURLKey(shot); u != "" {
			visitedMenuURLs[u] = struct{}{}
		}
	}

	normalScreenshots := make([]api.ScreenshotImage, 0, len(screenshots)-len(menuImages))
	for _, shot := range screenshots {
		if screenshotMatchesMenuPath(shot, menuPaths) {
			continue
		}
		if u := screenshotURLKey(shot); u != "" {
			if _, ok := visitedMenuURLs[u]; ok {
				continue
			}
		}
		normalScreenshots = append(normalScreenshots, shot)
	}
	return menuImages, normalScreenshots
}

func screenshotMatchesMenuPath(shot api.ScreenshotImage, menuPaths map[string]struct{}) bool {
	if len(menuPaths) == 0 {
		return false
	}
	path := strings.TrimSpace(shot.Path)
	if path == "" {
		return false
	}
	_, ok := menuPaths[path]
	return ok
}

func screenshotURLKey(shot api.ScreenshotImage) string {
	if u := strings.TrimSpace(shot.RawURL); u != "" {
		return u
	}
	if u := strings.TrimSpace(shot.ImgURL); u != "" {
		return u
	}
	return strings.TrimSpace(shot.WebURL)
}

func rewriteDescriptionSlotURLs(description string, slots []api.ScreenshotSlot, screenshots []api.ScreenshotImage, preserveNonRenderable bool) string {
	if strings.TrimSpace(description) == "" || len(slots) == 0 {
		return description
	}
	renderable := renderableSlots(slots)
	shotIdx := 0
	result := description
	for _, slot := range slots {
		originalURL := strings.TrimSpace(slot.OriginalURL)
		if originalURL == "" {
			continue
		}
		if !slot.RenderInScreenshots {
			if !preserveNonRenderable {
				result = strings.ReplaceAll(result, originalURL, "")
			}
			continue
		}
		if shotIdx >= len(renderable) || shotIdx >= len(screenshots) {
			continue
		}
		if renderable[shotIdx].SlotOrder != slot.SlotOrder {
			continue
		}
		replacement := screenshotURLKey(screenshots[shotIdx])
		shotIdx++
		if replacement == "" {
			continue
		}
		result = strings.ReplaceAll(result, originalURL, replacement)
	}
	return strings.TrimSpace(descriptionSpacingPattern.ReplaceAllString(result, "\n\n"))
}

func resolveTrackerDescription(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
	registry *Registry,
) (string, bool, bool) {
	if err := ctx.Err(); err != nil {
		return "", false, false
	}
	if canonical := descriptionGroupFromPreparedMeta(meta, tracker, preloaded, registry); strings.TrimSpace(canonical) != "" {
		if logger != nil {
			logger.Tracef(
				"trackers: canonical group description applied source=%s tracker=%s len=%d",
				meta.SourcePath,
				strings.TrimSpace(tracker),
				len(strings.TrimSpace(canonical)),
			)
		}
		return canonical, true, true
	}
	if trimmed := strings.TrimSpace(meta.DescriptionOverride); trimmed != "" {
		if logger != nil {
			logger.Tracef("trackers: request description override applied source=%s len=%d", meta.SourcePath, len(trimmed))
		}
		return meta.DescriptionOverride, true, false
	}
	if repo != nil && strings.TrimSpace(meta.SourcePath) != "" {
		for _, groupKey := range descriptionOverrideLookupKeys(meta.DescriptionGroups, tracker, registry) {
			override, err := descriptionOverrideFromSource(ctx, meta, repo, groupKey, preloaded)
			if err == nil {
				trimmed := strings.TrimSpace(override.Description)
				if trimmed != "" {
					if logger != nil {
						logger.Tracef(
							"trackers: description override applied source=%s group=%s len=%d",
							meta.SourcePath,
							strings.TrimSpace(groupKey),
							len(trimmed),
						)
					}
					return override.Description, true, false
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
	result := combineDescriptions(tracker, combined)
	if logger != nil {
		logger.Tracef(
			"trackers: description assets description sources db=%d meta=%d combined=%d desc_len=%d",
			len(records),
			len(meta.TrackerData),
			len(combined),
			len(strings.TrimSpace(result)),
		)
	}
	return result, false, false
}

func descriptionGroupFromPreparedMeta(meta api.UploadSubject, tracker string, preloaded *preloadedDescriptionAssetData, registry *Registry) string {
	if len(meta.DescriptionGroups) == 0 {
		return ""
	}

	groupDescriptions, trackerDescriptions, ambiguousTrackers := preparedDescriptionGroupLookups(meta.DescriptionGroups, preloaded)
	if len(groupDescriptions) == 0 && len(trackerDescriptions) == 0 && len(ambiguousTrackers) == 0 {
		return ""
	}

	for _, groupKey := range descriptionOverrideLookupKeys(meta.DescriptionGroups, tracker, registry) {
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

func descriptionOverrideLookupKeys(groups []api.DescriptionBuilderGroup, tracker string, registries ...*Registry) []string {
	keys := matchingPreparationDescriptionGroupKeys(groups, tracker, registries...)
	canonical := strings.TrimSpace(DescriptionOverrideGroupForTrackerWithRegistry(tracker, firstRegistry(registries)))
	if canonical == "" {
		return keys
	}
	return appendUniqueDescriptionGroupKey(keys, canonical)
}

func matchingPreparationDescriptionGroupKeys(groups []api.DescriptionBuilderGroup, tracker string, registries ...*Registry) []string {
	if len(groups) == 0 {
		return nil
	}

	normalizedTracker := strings.ToUpper(strings.TrimSpace(tracker))
	if normalizedTracker == "" {
		return nil
	}

	canonicalGroup := strings.ToLower(strings.TrimSpace(DescriptionOverrideGroupForTrackerWithRegistry(tracker, firstRegistry(registries))))
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
		if !descriptionGroupMatchesTracker(group, canonicalGroup, normalizedTracker) {
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
		candidates = append(candidates, candidate{
			key:   key,
			score: score,
			order: idx,
		})
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

func descriptionGroupMatchesTracker(group api.DescriptionBuilderGroup, canonicalGroup string, tracker string) bool {
	baseGroup, _, _ := parsePreparationDescriptionGroupKey(group.GroupKey)
	if !strings.EqualFold(strings.TrimSpace(baseGroup), canonicalGroup) {
		return false
	}
	if len(group.Trackers) == 0 {
		return true
	}
	normalizedTracker := strings.ToUpper(strings.TrimSpace(tracker))
	for _, candidate := range group.Trackers {
		if strings.ToUpper(strings.TrimSpace(candidate)) == normalizedTracker {
			return true
		}
	}
	return false
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

func preparedDescriptionGroupLookups(
	groups []api.DescriptionBuilderGroup,
	preloaded *preloadedDescriptionAssetData,
) (map[string]string, map[string]string, map[string]struct{}) {
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
			if existing, ok := trackerDescriptions[normalizedTracker]; ok &&
				!strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(group.RawDescription)) {
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

func resolveDescriptionScreenshots(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
	registry *Registry,
) ([]api.ScreenshotSlot, []api.ScreenshotImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("trackers: resolve description screenshots canceled: %w", err)
	}
	slots, err := screenshotSlotsFromSource(ctx, tracker, meta, repo, logger, preloaded, registry)
	if err != nil {
		return nil, nil, fmt.Errorf("trackers: load screenshot slots: %w", err)
	}
	images, _, _, err := selectScreenshotsFromSlots(tracker, slots, imageHostPolicy{})
	if err != nil {
		return nil, nil, fmt.Errorf("trackers: resolve screenshot slots for %s: %w", strings.TrimSpace(tracker), err)
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

func preloadDescriptionAssetData(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	registry *Registry,
) (*preloadedDescriptionAssetData, error) {
	preloaded, err := preloadUploadAssetData(ctx, meta, repo, registry)
	if err != nil {
		return nil, err
	}
	if err := preloadDescriptionFields(ctx, meta, repo, preloaded); err != nil {
		return nil, err
	}
	return preloaded, nil
}

func preloadScreenshotAssetData(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	registry *Registry,
) (*preloadedDescriptionAssetData, error) {
	return preloadUploadAssetData(ctx, meta, repo, registry)
}

func preloadUploadAssetData(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	registry *Registry,
) (*preloadedDescriptionAssetData, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: preload upload assets canceled: %w", err)
	}
	if repo == nil || strings.TrimSpace(meta.SourcePath) == "" {
		return nil, nil
	}

	preloaded := &preloadedDescriptionAssetData{registry: registry}

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

	slots, err := screenshotSlotsFromSource(ctx, "", meta, repo, nil, preloaded, registry)
	if err != nil {
		return nil, err
	}
	preloaded.screenshotSlots = slots
	preloaded.screenshotSlotsLoaded = true

	return preloaded, nil
}

func preloadDescriptionFields(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	preloaded *preloadedDescriptionAssetData,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("trackers: preload description fields canceled: %w", err)
	}
	if preloaded == nil || repo == nil || strings.TrimSpace(meta.SourcePath) == "" {
		return nil
	}

	preloaded.descriptionOverrides = make(map[string]api.DescriptionOverride)
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
		return fmt.Errorf("trackers: list description overrides: %w", err)
	}
	return nil
}

func descriptionOverrideFromSource(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	groupKey string,
	preloaded *preloadedDescriptionAssetData,
) (api.DescriptionOverride, error) {
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

func trackerMetadataFromSource(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	preloaded *preloadedDescriptionAssetData,
) ([]api.TrackerMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: load tracker metadata canceled: %w", err)
	}
	if preloaded != nil {
		return preloaded.trackerRecords, nil
	}
	return wrapTrackerResult(repo.ListTrackerMetadataByPath(ctx, meta.SourcePath))
}

func finalSelectionsFromSource(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	preloaded *preloadedDescriptionAssetData,
) ([]api.ScreenshotFinalSelection, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: load final selections canceled: %w", err)
	}
	if preloaded != nil {
		return preloaded.selections, nil
	}
	return wrapTrackerResult(repo.ListFinalSelections(ctx, meta.SourcePath))
}

func uploadedImagesFromSource(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	preloaded *preloadedDescriptionAssetData,
) ([]api.UploadedImageLink, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("trackers: load uploaded images canceled: %w", err)
	}
	if preloaded != nil {
		return preloaded.uploads, nil
	}
	return wrapTrackerResult(repo.ListUploadedImagesByPath(ctx, meta.SourcePath))
}

func resolveTrackerImageURLs(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
) []string {
	if err := ctx.Err(); err != nil {
		return nil
	}
	trackerKey := strings.TrimSpace(tracker)
	if !meta.Options.KeepImages {
		if logger != nil {
			logger.Tracef("trackers: description assets tracker urls skipped keep_images=false tracker=%s", trackerKey)
		}
		return nil
	}
	records, err := trackerMetadataFromSource(ctx, meta, repo, preloaded)
	if err == nil {
		if len(records) > 0 {
			if trackerKey != "" {
				filtered := filterTrackerMetadataByName(records, trackerKey)
				if len(filtered) > 0 {
					if logger != nil {
						logger.Tracef(
							"trackers: description assets tracker urls source=db tracker=%s records=%d filtered=%d",
							trackerKey,
							len(records),
							len(filtered),
						)
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
				logger.Tracef(
					"trackers: description assets tracker urls source=meta tracker=%s records=%d filtered=%d",
					trackerKey,
					len(meta.TrackerData),
					len(filtered),
				)
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

func combineDescriptions(tracker string, records []api.TrackerMetadata) string {
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
		recordTracker := strings.TrimSpace(record.Tracker)
		if recordTracker == "" {
			recordTracker = tracker
		}
		trimmed := sanitizeTrackerDescription(recordTracker, record.Description)
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

func sanitizeTrackerDescription(_ string, value string) string {
	cleaned := stripEmbeddedNFOBlocks(value)
	cleaned = unit3DBotSignaturePattern.ReplaceAllString(cleaned, "")
	cleaned = knownBotSignaturePattern.ReplaceAllString(cleaned, "")
	cleaned = knownBotImagePattern.ReplaceAllString(cleaned, "")
	cleaned = defaultSignaturePattern.ReplaceAllString(cleaned, "")
	cleaned = emptyCenterPattern.ReplaceAllString(cleaned, "")
	cleaned = descriptionSpacingPattern.ReplaceAllString(cleaned, "\n\n")
	return strings.TrimSpace(cleaned)
}

// StripDefaultDescriptionSignature removes the generic uploader signature.
func StripDefaultDescriptionSignature(value string) string {
	cleaned := defaultSignaturePattern.ReplaceAllString(value, "")
	cleaned = descriptionSpacingPattern.ReplaceAllString(cleaned, "\n\n")
	return strings.TrimSpace(cleaned)
}
