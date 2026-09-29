// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"path" //nolint:depguard // Manipulates URL/torrent-style slash paths, not local filesystem paths.
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/bbcode/comparison"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	screenshotSlotSourceDescription = "description"
	screenshotSlotSourceSelection   = "final_selection"
	screenshotSlotSourceTracker     = "tracker_metadata"
	screenshotPurposeMenu           = api.ScreenshotSelectionSourceMenu

	screenshotSectionWrapped    = "wrapped"
	screenshotSectionComparison = "comparison"
	screenshotSectionInline     = "inline"

	mixedSlotResolutionValue = "mixed"
)

var (
	slotArtifactSuffixPattern = regexp.MustCompile(`_\d+$`)
	slotHashedArtifactPattern = regexp.MustCompile(`^(.*)_\d+_([0-9a-f]{12})$`)
	slotWrapperPattern        = regexp.MustCompile(`(?is)\[(?:center|align=[^\]]+)\]([\s\S]*?)\[/(?:center|align)\]`)
	slotComparisonPattern     = regexp.MustCompile(`(?is)\[comparison=([^\]]+)\]([\s\S]*?)\[/comparison\]`)
	slotComparisonURL         = regexp.MustCompile(`(?i)https?://[^\s\[\]]+\.(?:png|jpe?g|gif|webp)(?:\?[^\s\[\]]*)?`)
	slotURLImgPattern         = regexp.MustCompile(`(?is)\[url=(https?://[^\]]+)\]\s*\[img[^\]]*\](.*?)\[/img\]\s*\[/url\]`)
	slotImgPattern            = regexp.MustCompile(`(?is)\[img[^\]]*\](.*?)\[/img\]`)
	posterLikeSlotHosts       = map[string]struct{}{
		"image.tmdb.org":     {},
		"themoviedb.org":     {},
		"www.themoviedb.org": {},
	}
)

type parsedDescriptionSlot struct {
	start int
	slot  api.ScreenshotSlot
}

// screenshotSlotsFromSource loads or rebuilds the source's slots and returns
// tracker-specific views without replacing shared stored slots unless the
// selected media or description and image list belongs to that source.
func screenshotSlotsFromSource(
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
	if preloaded != nil && preloaded.screenshotSlotsLoaded {
		if preloaded.exactMedia != nil {
			return cloneScreenshotSlots(preloaded.screenshotSlots), nil
		}
		if len(preloaded.screenshotSlots) == 0 && strings.TrimSpace(tracker) != "" {
			slots, err := synthesizeScreenshotSlots(ctx, tracker, meta, repo, logger, preloaded, registry)
			if err != nil {
				return nil, err
			}
			if len(slots) > 0 && screenshotSlotsSourceWide(ctx, tracker, meta, repo, preloaded, registry) {
				if err := repo.ReplaceScreenshotSlots(ctx, meta.MediaBinding, slots); err != nil {
					return nil, fmt.Errorf("trackers: %w", err)
				}
			}
			syncSlotsToPreloaded(preloaded, slots)
			return cloneScreenshotSlots(slots), nil
		}
		return reconcileStoredComparisonSlots(ctx, tracker, meta, repo, logger, preloaded, registry,
			cloneScreenshotSlots(preloaded.screenshotSlots), screenshotSlotsSourceWide(ctx, tracker, meta, repo, preloaded, registry))
	}
	if repo == nil || strings.TrimSpace(meta.SourcePath) == "" {
		return nil, nil
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
		if strings.TrimSpace(tracker) == "" {
			return cloneScreenshotSlots(slots), nil
		}
		return reconcileStoredComparisonSlots(ctx, tracker, meta, repo, logger, preloaded, registry,
			cloneScreenshotSlots(slots), screenshotSlotsSourceWide(ctx, tracker, meta, repo, preloaded, registry))
	}
	if strings.TrimSpace(tracker) == "" {
		return nil, nil
	}

	slots, err = synthesizeScreenshotSlots(ctx, tracker, meta, repo, logger, preloaded, registry)
	if err != nil {
		return nil, err
	}
	if len(slots) == 0 {
		return nil, nil
	}
	if screenshotSlotsSourceWide(ctx, tracker, meta, repo, preloaded, registry) {
		if err := repo.ReplaceScreenshotSlots(ctx, meta.MediaBinding, slots); err != nil {
			return nil, fmt.Errorf("trackers: %w", err)
		}
	}
	if preloaded != nil {
		syncSlotsToPreloaded(preloaded, slots)
	}
	return cloneScreenshotSlots(slots), nil
}

// screenshotSlotsSourceWide permits replacing the shared slot row only for
// exact selected media or a description and image list owned by this source.
func screenshotSlotsSourceWide(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	preloaded *preloadedDescriptionAssetData,
	registry *Registry,
) bool {
	if repo == nil || strings.TrimSpace(tracker) == "" {
		return false
	}
	if meta.ExactMedia != nil {
		return true
	}
	trackerDescription, _, _ := resolveTrackerDescription(ctx, tracker, meta, repo, nil, preloaded, registry)
	sourceDescription, _, _ := resolveTrackerDescription(ctx, "", meta, repo, nil, preloaded, registry)
	if trackerDescription != sourceDescription {
		if !soleDescriptionScopeForTracker(ctx, tracker, meta, repo, preloaded, registry) {
			return false
		}
		records, err := trackerMetadataFromSource(ctx, meta, repo, preloaded, registry)
		if err != nil {
			return false
		}
		for _, record := range mergeTrackerMetadata(records, meta.TrackerData) {
			if strings.TrimSpace(record.Tracker) != "" && !strings.EqualFold(record.Tracker, tracker) &&
				(strings.TrimSpace(record.Description) != "" || len(record.ImageURLs) > 0) {
				return false
			}
		}
		if preloaded != nil && (len(preloaded.descriptionOverrides) > 1 || len(preloaded.groupDescriptions) > 1 ||
			len(preloaded.trackerDescriptions) > 1) {
			return false
		}
	}
	return slices.Equal(
		resolveTrackerImageURLs(ctx, tracker, meta, repo, nil, preloaded, registry),
		resolveTrackerImageURLs(ctx, "", meta, repo, nil, preloaded, registry),
	)
}

func soleDescriptionScopeForTracker(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	preloaded *preloadedDescriptionAssetData,
	registry *Registry,
) bool {
	if len(meta.DescriptionGroups) > 0 {
		return len(meta.DescriptionGroups) == 1 && len(meta.DescriptionGroups[0].Trackers) == 1 &&
			strings.EqualFold(strings.TrimSpace(meta.DescriptionGroups[0].Trackers[0]), strings.TrimSpace(tracker))
	}
	var overrides []api.DescriptionOverride
	if preloaded != nil {
		for _, override := range preloaded.descriptionOverrides {
			overrides = append(overrides, override)
		}
	} else {
		var err error
		overrides, err = repo.ListDescriptionOverridesByPath(ctx, meta.SourcePath)
		if err != nil {
			return false
		}
	}
	if len(overrides) != 1 {
		return false
	}
	for _, key := range descriptionOverrideLookupKeys(meta.DescriptionGroups, tracker, registry) {
		if strings.EqualFold(strings.TrimSpace(overrides[0].GroupKey), strings.TrimSpace(key)) {
			return true
		}
	}
	return false
}

// reconcileStoredComparisonSlots rebuilds stale description slots while
// preserving selected local assets. Legacy images shared with comparison blocks
// lose unselected variants when their provenance cannot be verified.
func reconcileStoredComparisonSlots(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
	registry *Registry,
	slots []api.ScreenshotSlot,
	persist bool,
) ([]api.ScreenshotSlot, error) {
	description, _, _ := resolveTrackerDescription(ctx, tracker, meta, repo, logger, preloaded, registry)
	sharedURLs := sharedComparisonImageURLs(description)
	migrationURLs := make(map[string]struct{})
	checkedURLs := make(map[string]struct{})
	for _, slot := range slots {
		if slot.SourceKind != screenshotSlotSourceDescription {
			continue
		}
		urlKey := bbcode.NormalizeImageRawURL(slot.OriginalURL)
		if _, shared := sharedURLs[urlKey]; !shared {
			continue
		}
		if _, checked := checkedURLs[urlKey]; !checked {
			checkedURLs[urlKey] = struct{}{}
			if repo != nil {
				if _, err := repo.GetTrackerTimestamp(ctx, comparisonSlotProvenanceKey(meta.SourcePath, tracker, urlKey)); err == nil {
					continue
				} else if !errors.Is(err, internalerrors.ErrNotFound) {
					return nil, fmt.Errorf("trackers: load comparison slot provenance: %w", err)
				}
			}
			migrationURLs[urlKey] = struct{}{}
		}
	}
	migrateShared := len(migrationURLs) > 0
	selectedSharedURLs := make(map[string]struct{})
	if migrateShared {
		selections, err := finalSelectionsFromSource(ctx, meta, repo, preloaded)
		if err != nil && !errorsIsNotFound(err) {
			return nil, err
		}
		selectedPaths := make(map[string]struct{}, len(selections))
		for _, selection := range selections {
			selectedPaths[strings.TrimSpace(selection.ImagePath)] = struct{}{}
		}
		for _, slot := range slots {
			urlKey := bbcode.NormalizeImageRawURL(slot.OriginalURL)
			if _, migrate := migrationURLs[urlKey]; !migrate {
				continue
			}
			if _, selected := selectedPaths[strings.TrimSpace(slot.ImagePath)]; selected && strings.TrimSpace(slot.ImagePath) != "" {
				selectedSharedURLs[urlKey] = struct{}{}
			}
		}
	}
	trackerRecords, err := trackerMetadataFromSource(ctx, meta, repo, preloaded, registry)
	if err != nil && !errorsIsNotFound(err) {
		return nil, err
	}
	if len(trackerRecords) == 0 {
		trackerRecords = FilterUnverifiedTrackerImages(ctx, repo, registry, meta.TrackerData, logger)
	}
	filteredSlots := make([]api.ScreenshotSlot, 0, len(slots))
	for _, slot := range slots {
		if !trackerArtifactPathAllowed(slot.ImagePath, trackerRecords, registry) {
			continue
		}
		filteredSlots = append(filteredSlots, slot)
	}
	changed := len(filteredSlots) != len(slots)
	slots = filteredSlots
	if !changed && !migrateShared && (strings.TrimSpace(description) == "" || storedDescriptionSlotsMatchCurrent(slots, description)) &&
		storedTrackerURLSlotsMatchCurrent(ctx, tracker, meta, repo, logger, preloaded, registry, slots) {
		return slots, nil
	}
	rebuilt, err := synthesizeScreenshotSlots(ctx, tracker, meta, repo, logger, preloaded, registry)
	if err != nil {
		return nil, err
	}
	preserveStoredDescriptionAssets(rebuilt, slots)
	if migrateShared {
		for index := range rebuilt {
			if rebuilt[index].SourceKind != screenshotSlotSourceDescription {
				continue
			}
			urlKey := bbcode.NormalizeImageRawURL(rebuilt[index].OriginalURL)
			if _, migrate := migrationURLs[urlKey]; !migrate {
				continue
			}
			if _, selected := selectedSharedURLs[urlKey]; selected {
				continue
			}
			rebuilt[index].ImagePath = ""
			rebuilt[index].DiscID = ""
			rebuilt[index].Variants = nil
			rebuilt[index].OriginalKey = rebuilt[index].OriginalURL
		}
	}
	rebuilt = normalizeSlotOrders(rebuilt)
	if persist && repo != nil {
		if err := repo.ReplaceScreenshotSlots(ctx, meta.MediaBinding, rebuilt); err != nil {
			return nil, fmt.Errorf("trackers: %w", err)
		}
		if migrateShared {
			for urlKey := range migrationURLs {
				if err := repo.SaveTrackerTimestamp(ctx, api.TrackerTimestamp{
					Tracker: comparisonSlotProvenanceKey(meta.SourcePath, tracker, urlKey), UpdatedAt: time.Now().UTC(),
				}); err != nil {
					return nil, fmt.Errorf("trackers: save comparison slot provenance: %w", err)
				}
			}
		}
	}
	if preloaded != nil {
		syncSlotsToPreloaded(preloaded, rebuilt)
	}
	return cloneScreenshotSlots(rebuilt), nil
}

func sharedComparisonImageURLs(description string) map[string]struct{} {
	blocks := comparison.BlockRanges(description)
	if len(blocks) == 0 {
		return nil
	}
	comparisonURLs := make(map[string]struct{})
	for _, block := range blocks {
		for _, rawURL := range slotComparisonURL.FindAllString(description[block[0]:block[1]], -1) {
			comparisonURLs[bbcode.NormalizeImageRawURL(rawURL)] = struct{}{}
		}
	}
	shared := make(map[string]struct{})
	for _, slot := range parseDescriptionImageSlots("", comparison.RemoveComparisonBlocks(description)) {
		key := bbcode.NormalizeImageRawURL(slot.OriginalURL)
		if _, found := comparisonURLs[key]; found {
			shared[key] = struct{}{}
		}
	}
	return shared
}

func comparisonSlotProvenanceKey(sourcePath string, tracker string, imageURL string) string {
	record := api.TrackerMetadata{
		SourcePath:  sourcePath,
		Tracker:     tracker,
		Description: imageURL,
	}
	return TrackerAssetProvenanceKey(record) + ":comparison_slots_v2"
}

func storedDescriptionSlotsMatchCurrent(slots []api.ScreenshotSlot, description string) bool {
	current := parseDescriptionImageSlots("", comparison.RemoveComparisonBlocks(description))
	index := 0
	for _, slot := range slots {
		if slot.SourceKind != screenshotSlotSourceDescription {
			continue
		}
		if index >= len(current) || slot.OriginalURL != current[index].OriginalURL ||
			slot.SectionKind != current[index].SectionKind {
			return false
		}
		index++
	}
	return index == len(current)
}

func storedTrackerURLSlotsMatchCurrent(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
	registry *Registry,
	slots []api.ScreenshotSlot,
) bool {
	hasTrackerURL := false
	for _, slot := range slots {
		if slot.SourceKind == screenshotSlotSourceTracker && strings.TrimSpace(slot.OriginalURL) != "" {
			hasTrackerURL = true
			break
		}
	}
	if !hasTrackerURL {
		return true
	}
	allowed := make(map[string]struct{})
	for _, rawURL := range resolveTrackerImageURLs(ctx, tracker, meta, repo, logger, preloaded, registry) {
		allowed[strings.TrimSpace(rawURL)] = struct{}{}
	}
	for _, slot := range slots {
		if slot.SourceKind != screenshotSlotSourceTracker || strings.TrimSpace(slot.OriginalURL) == "" {
			continue
		}
		if _, found := allowed[strings.TrimSpace(slot.OriginalURL)]; !found {
			return false
		}
		delete(allowed, strings.TrimSpace(slot.OriginalURL))
	}
	return len(allowed) == 0
}

func preserveStoredDescriptionAssets(rebuilt []api.ScreenshotSlot, stored []api.ScreenshotSlot) {
	for index := range rebuilt {
		if rebuilt[index].SourceKind != screenshotSlotSourceDescription {
			continue
		}
		for _, previous := range stored {
			if previous.SourceKind != screenshotSlotSourceDescription || previous.OriginalURL != rebuilt[index].OriginalURL ||
				previous.SectionKind != rebuilt[index].SectionKind {
				continue
			}
			if rebuilt[index].ImagePath == "" {
				rebuilt[index].ImagePath = previous.ImagePath
				rebuilt[index].DiscID = previous.DiscID
			}
			for _, variant := range previous.Variants {
				if variant.ImagePath != "" && variant.ImagePath != rebuilt[index].ImagePath {
					continue
				}
				found := false
				for _, current := range rebuilt[index].Variants {
					if strings.EqualFold(current.Host, variant.Host) &&
						normalizeUsageScope(current.UsageScope) == normalizeUsageScope(variant.UsageScope) {
						found = true
						break
					}
				}
				if !found {
					rebuilt[index].Variants = append(rebuilt[index].Variants, variant)
				}
			}
			break
		}
	}
}

func filterStoredSlotsForSelectedImages(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	slots []api.ScreenshotSlot,
	preloaded *preloadedDescriptionAssetData,
) ([]api.ScreenshotSlot, error) {
	selections, err := finalSelectionsFromSource(ctx, meta, repo, preloaded)
	if err != nil && !errorsIsNotFound(err) {
		return nil, err
	}
	selectedPaths := make(map[string]struct{}, len(selections))
	for _, selection := range selections {
		pathValue := strings.TrimSpace(selection.ImagePath)
		if pathValue == "" {
			continue
		}
		selectedPaths[pathValue] = struct{}{}
	}
	filtered := make([]api.ScreenshotSlot, 0, len(slots))
	for _, slot := range slots {
		imagePath := strings.TrimSpace(slot.ImagePath)
		if strings.EqualFold(strings.TrimSpace(slot.SourceKind), screenshotSlotSourceSelection) || selectedPathExists(selectedPaths, imagePath) {
			slot.Variants = nil
			filtered = append(filtered, slot)
		}
	}
	appendSelectionOnlySlots(&filtered, selections)
	uploads, err := uploadedImagesFromSource(ctx, meta, repo, preloaded)
	if err != nil && !errorsIsNotFound(err) {
		return nil, err
	}
	applyUploadedVariantsToSlots(filtered, uploads)
	return normalizeSlotOrders(filtered), nil
}

func appendStoredSelectionSlots(
	ctx context.Context,
	meta api.UploadSubject,
	repo UploadPersistence,
	slots []api.ScreenshotSlot,
	preloaded *preloadedDescriptionAssetData,
) ([]api.ScreenshotSlot, error) {
	selections, err := finalSelectionsFromSource(ctx, meta, repo, preloaded)
	if err != nil && !errorsIsNotFound(err) {
		return nil, err
	}
	appendSelectionOnlySlots(&slots, selections)
	uploads, err := uploadedImagesFromSource(ctx, meta, repo, preloaded)
	if err != nil && !errorsIsNotFound(err) {
		return nil, err
	}
	applyUploadedVariantsToSlots(slots, uploads)
	return normalizeSlotOrders(slots), nil
}

func selectedPathExists(selectedPaths map[string]struct{}, imagePath string) bool {
	if strings.TrimSpace(imagePath) == "" {
		return false
	}
	_, ok := selectedPaths[imagePath]
	return ok
}

func synthesizeScreenshotSlots(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
	registry *Registry,
) ([]api.ScreenshotSlot, error) {
	description, _, _ := resolveTrackerDescription(ctx, tracker, meta, repo, logger, preloaded, registry)
	selections, err := finalSelectionsFromSource(ctx, meta, repo, preloaded)
	if err != nil && !errorsIsNotFound(err) {
		return nil, err
	}

	trackerRecords, err := trackerMetadataFromSource(ctx, meta, repo, preloaded, registry)
	if err != nil && !errorsIsNotFound(err) {
		return nil, err
	}
	if len(trackerRecords) == 0 {
		trackerRecords = FilterUnverifiedTrackerImages(ctx, repo, registry, meta.TrackerData, logger)
	}
	selections = filterTrackerArtifactSelections(selections, trackerRecords, registry)
	sort.Slice(selections, func(i, j int) bool { return selections[i].Order < selections[j].Order })
	uploads, err := uploadedImagesFromSource(ctx, meta, repo, preloaded)
	if err != nil && !errorsIsNotFound(err) {
		return nil, err
	}

	description = comparison.RemoveComparisonBlocks(description)
	slots := parseDescriptionImageSlots(meta.SourcePath, description)
	if len(slots) > 0 {
		attachSelectionPathsToSlots(slots, selections)
		limitRenderableSlotsToSelections(slots, selections)
		appendSelectionOnlySlots(&slots, selections)
		applyUploadedVariantsToSlots(slots, uploads)
		return normalizeSlotOrders(slots), nil
	}

	if len(selections) > 0 {
		slots = buildSelectionSlots(meta.SourcePath, selections)
		applyUploadedVariantsToSlots(slots, uploads)
		return normalizeSlotOrders(slots), nil
	}

	if !meta.Options.KeepImages {
		if logger != nil {
			logger.Tracef("trackers: screenshot slots tracker urls skipped keep_images=false tracker=%s", strings.TrimSpace(tracker))
		}
		return nil, nil
	}

	urls := collectImageURLs(trackerRecords, registry)
	if len(urls) == 0 {
		urls = collectImageURLs(FilterUnverifiedTrackerImages(ctx, repo, registry, meta.TrackerData, logger), registry)
	}
	slots = buildTrackerURLSlots(meta.SourcePath, urls)
	applyUploadedVariantsToSlots(slots, uploads)
	return normalizeSlotOrders(slots), nil
}

func filterTrackerArtifactSelections(
	selections []api.ScreenshotFinalSelection,
	records []api.TrackerMetadata,
	registry *Registry,
) []api.ScreenshotFinalSelection {
	filtered := make([]api.ScreenshotFinalSelection, 0, len(selections))
	for _, selection := range selections {
		if trackerArtifactPathAllowed(selection.ImagePath, records, registry) {
			filtered = append(filtered, selection)
		}
	}
	return filtered
}

// trackerArtifactPathAllowed requires selected files from provenance-gated
// trackers to match a comparison-safe saved image URL.
func trackerArtifactPathAllowed(imagePath string, records []api.TrackerMetadata, registry *Registry) bool {
	if strings.TrimSpace(imagePath) == "" {
		return true
	}
	for _, record := range records {
		if !LegacyImageAssetsNeedProvenance(registry, record.Tracker) {
			continue
		}
		trackerDir := sanitizePersistedTrackerArtifactName(strings.ToLower(strings.TrimSpace(record.Tracker)))
		if !strings.EqualFold(filepath.Base(filepath.Dir(imagePath)), trackerDir) {
			continue
		}
		allowedURLs := ComparisonSafeTrackerImageURLs(record, registry)
		for index, rawURL := range record.ImageURLs {
			if !slices.Contains(allowedURLs, rawURL) {
				continue
			}
			for _, candidate := range localTrackerArtifactPaths(filepath.Dir(imagePath), rawURL, index) {
				if pathutil.SamePath(candidate, imagePath) {
					return true
				}
			}
		}
		return false
	}
	return true
}

func parseDescriptionImageSlots(sourcePath string, description string) []api.ScreenshotSlot {
	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		return nil
	}

	covered := make([][2]int, 0)
	parsed := make([]parsedDescriptionSlot, 0)

	for _, match := range slotComparisonPattern.FindAllStringSubmatchIndex(trimmed, -1) {
		if len(match) < 6 {
			continue
		}
		blockStart, blockEnd := match[0], match[1]
		covered = append(covered, [2]int{blockStart, blockEnd})
		body := trimmed[match[4]:match[5]]
		urls := slotComparisonURL.FindAllStringIndex(body, -1)
		for _, urlMatch := range urls {
			rawURL := strings.TrimSpace(body[urlMatch[0]:urlMatch[1]])
			parsed = append(parsed, parsedDescriptionSlot{
				start: blockStart + urlMatch[0],
				slot:  newDescriptionSlot(sourcePath, rawURL, rawURL, screenshotSectionComparison, true),
			})
		}
	}

	for _, match := range slotWrapperPattern.FindAllStringSubmatchIndex(trimmed, -1) {
		if len(match) < 4 {
			continue
		}
		blockStart, blockEnd := match[0], match[1]
		if rangeCovered(blockStart, blockEnd, covered) {
			continue
		}
		covered = append(covered, [2]int{blockStart, blockEnd})
		body := trimmed[match[2]:match[3]]
		images := parseImageMatchesInSegment(sourcePath, body, blockStart+match[2], screenshotSectionWrapped)
		renderInScreenshots := !isPosterLikeSlotBlock(images)
		for idx := range images {
			images[idx].slot.RenderInScreenshots = renderInScreenshots
		}
		parsed = append(parsed, images...)
	}

	inline := parseImageMatchesInSegment(sourcePath, trimmed, 0, screenshotSectionInline)
	for _, image := range inline {
		if rangeCovered(image.start, image.start+1, covered) {
			continue
		}
		image.slot.RenderInScreenshots = false
		parsed = append(parsed, image)
	}

	sort.SliceStable(parsed, func(i, j int) bool { return parsed[i].start < parsed[j].start })

	slots := make([]api.ScreenshotSlot, 0, len(parsed))
	seen := make(map[string]struct{}, len(parsed))
	for _, item := range parsed {
		key := strings.TrimSpace(item.slot.OriginalURL)
		if key == "" {
			key = strings.TrimSpace(item.slot.OriginalKey)
		}
		if key == "" {
			continue
		}
		if item.slot.SectionKind == screenshotSectionComparison {
			slots = append(slots, item.slot)
			continue
		}
		identity := fmt.Sprintf("%s|%s|%s", item.slot.SectionKind, key, item.slot.OriginalHost)
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		slots = append(slots, item.slot)
	}
	return normalizeSlotOrders(slots)
}

func parseImageMatchesInSegment(sourcePath string, value string, offset int, sectionKind string) []parsedDescriptionSlot {
	results := make([]parsedDescriptionSlot, 0)
	covered := make([][2]int, 0)

	for _, match := range slotURLImgPattern.FindAllStringSubmatchIndex(value, -1) {
		if len(match) < 6 {
			continue
		}
		covered = append(covered, [2]int{match[0], match[1]})
		imgURL := strings.TrimSpace(value[match[4]:match[5]])
		if imgURL == "" {
			continue
		}
		results = append(results, parsedDescriptionSlot{
			start: offset + match[0],
			slot:  newDescriptionSlot(sourcePath, imgURL, imgURL, sectionKind, true),
		})
	}

	for _, match := range slotImgPattern.FindAllStringSubmatchIndex(value, -1) {
		if len(match) < 4 {
			continue
		}
		if rangeCovered(match[0], match[1], covered) {
			continue
		}
		imgURL := strings.TrimSpace(value[match[2]:match[3]])
		if imgURL == "" {
			continue
		}
		results = append(results, parsedDescriptionSlot{
			start: offset + match[0],
			slot:  newDescriptionSlot(sourcePath, imgURL, imgURL, sectionKind, true),
		})
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].start < results[j].start })
	return results
}

func newDescriptionSlot(sourcePath string, originalURL string, rawURL string, sectionKind string, fromDescription bool) api.ScreenshotSlot {
	normalizedOriginal := strings.TrimSpace(originalURL)
	host := strings.TrimSpace(imagehost.ExtractHost(rawURL))
	return api.ScreenshotSlot{
		SourcePath:          sourcePath,
		SourceKind:          screenshotSlotSourceDescription,
		OriginalKey:         normalizedOriginal,
		OriginalURL:         normalizedOriginal,
		OriginalHost:        host,
		FromDescription:     fromDescription,
		SectionKind:         sectionKind,
		RenderInScreenshots: true,
	}
}

func isPosterLikeSlotBlock(images []parsedDescriptionSlot) bool {
	if len(images) != 1 {
		return false
	}
	rawURL := strings.TrimSpace(images[0].slot.OriginalURL)
	if rawURL == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	_, ok := posterLikeSlotHosts[strings.ToLower(strings.TrimSpace(parsed.Hostname()))]
	return ok
}

func rangeCovered(start int, end int, covered [][2]int) bool {
	for _, item := range covered {
		if start >= item[0] && end <= item[1] {
			return true
		}
	}
	return false
}

func attachSelectionPathsToSlots(slots []api.ScreenshotSlot, selections []api.ScreenshotFinalSelection) {
	for idx := range slots {
		if idx >= len(selections) {
			break
		}
		if strings.TrimSpace(slots[idx].ImagePath) != "" {
			continue
		}
		slots[idx].ImagePath = strings.TrimSpace(selections[idx].ImagePath)
		slots[idx].DiscID = selections[idx].DiscID
		if strings.TrimSpace(slots[idx].OriginalKey) == "" {
			slots[idx].OriginalKey = slots[idx].ImagePath
		}
	}
}

func limitRenderableSlotsToSelections(slots []api.ScreenshotSlot, selections []api.ScreenshotFinalSelection) {
	if len(selections) == 0 {
		return
	}
	for idx := range slots {
		if strings.TrimSpace(slots[idx].ImagePath) != "" {
			continue
		}
		slots[idx].RenderInScreenshots = false
	}
}

func alignRenderableSlotsToSourceImages(slots []api.ScreenshotSlot, sourceImages []api.ScreenshotImage) bool {
	if len(slots) == 0 || len(sourceImages) == 0 {
		return false
	}
	sourceByKey := make(map[string]api.ScreenshotImage, len(sourceImages))
	for _, source := range sourceImages {
		if key := screenshotSourceMatchKey(source.Path); key != "" {
			sourceByKey[key] = source
		}
	}
	if len(sourceByKey) > 0 {
		hasMatch := false
		for idx := range slots {
			if !slots[idx].RenderInScreenshots {
				continue
			}
			if _, ok := sourceImageForURL(sourceByKey, slots[idx].OriginalURL); ok {
				hasMatch = true
				break
			}
		}
		if !hasMatch {
			return alignRenderableSlotsToSourceImagesByOrder(slots, sourceImages)
		}
		matched := false
		changed := false
		for idx := range slots {
			if !slots[idx].RenderInScreenshots {
				continue
			}
			source, ok := sourceImageForURL(sourceByKey, slots[idx].OriginalURL)
			if !ok {
				slots[idx].RenderInScreenshots = false
				changed = true
				continue
			}
			matched = true
			pathValue := strings.TrimSpace(source.Path)
			if pathValue != "" && strings.TrimSpace(slots[idx].ImagePath) == "" {
				slots[idx].ImagePath = pathValue
				slots[idx].DiscID = source.DiscID
				if strings.TrimSpace(slots[idx].OriginalKey) == "" {
					slots[idx].OriginalKey = pathValue
				}
				changed = true
			}
		}
		if matched {
			return changed
		}
	}

	return alignRenderableSlotsToSourceImagesByOrder(slots, sourceImages)
}

func attachMatchingSourceImagesToSlots(slots []api.ScreenshotSlot, sourceImages []api.ScreenshotImage) bool {
	if len(slots) == 0 || len(sourceImages) == 0 {
		return false
	}
	sourceByKey := make(map[string]api.ScreenshotImage, len(sourceImages))
	for _, source := range sourceImages {
		if key := screenshotSourceMatchKey(source.Path); key != "" {
			sourceByKey[key] = source
		}
	}
	if len(sourceByKey) == 0 {
		return false
	}
	changed := false
	for idx := range slots {
		if slots[idx].SectionKind == screenshotSectionComparison {
			continue
		}
		if !slots[idx].RenderInScreenshots || strings.TrimSpace(slots[idx].ImagePath) != "" {
			continue
		}
		source, ok := sourceImageForURL(sourceByKey, slots[idx].OriginalURL)
		if !ok {
			continue
		}
		pathValue := strings.TrimSpace(source.Path)
		if pathValue == "" {
			continue
		}
		slots[idx].ImagePath = pathValue
		slots[idx].DiscID = source.DiscID
		if strings.TrimSpace(slots[idx].OriginalKey) == "" {
			slots[idx].OriginalKey = pathValue
		}
		changed = true
	}
	return changed
}

func detachComparisonSourceImagesFromSlots(slots []api.ScreenshotSlot, sourceImages []api.ScreenshotImage) bool {
	if len(slots) == 0 || len(sourceImages) == 0 {
		return false
	}
	sourcePaths := make(map[string]struct{}, len(sourceImages))
	for _, source := range sourceImages {
		pathValue := strings.TrimSpace(source.Path)
		if pathValue == "" {
			continue
		}
		sourcePaths[pathValue] = struct{}{}
	}
	if len(sourcePaths) == 0 {
		return false
	}
	changed := false
	for idx := range slots {
		if slots[idx].SectionKind != screenshotSectionComparison {
			continue
		}
		if _, ok := sourcePaths[strings.TrimSpace(slots[idx].ImagePath)]; ok {
			slots[idx].ImagePath = ""
			if strings.TrimSpace(slots[idx].OriginalURL) != "" {
				slots[idx].OriginalKey = strings.TrimSpace(slots[idx].OriginalURL)
			}
			changed = true
		}
		if len(slots[idx].Variants) == 0 {
			continue
		}
		filtered := slots[idx].Variants[:0]
		for _, variant := range slots[idx].Variants {
			if _, ok := sourcePaths[strings.TrimSpace(variant.ImagePath)]; ok {
				changed = true
				continue
			}
			filtered = append(filtered, variant)
		}
		slots[idx].Variants = filtered
	}
	return changed
}

func appendSourceImageSlots(slots *[]api.ScreenshotSlot, sourcePath string, sourceImages []api.ScreenshotImage) bool {
	if len(sourceImages) == 0 {
		return false
	}
	existingNormalPaths := make(map[string]struct{}, len(*slots))
	for _, slot := range *slots {
		if slot.SectionKind == screenshotSectionComparison {
			continue
		}
		pathValue := strings.TrimSpace(slot.ImagePath)
		if pathValue == "" {
			continue
		}
		existingNormalPaths[pathValue] = struct{}{}
	}
	changed := false
	for _, image := range sourceImages {
		pathValue := strings.TrimSpace(image.Path)
		if pathValue == "" {
			continue
		}
		if _, exists := existingNormalPaths[pathValue]; exists {
			continue
		}
		*slots = append(*slots, api.ScreenshotSlot{
			SourcePath:          sourcePath,
			DiscID:              image.DiscID,
			SourceKind:          screenshotSlotSourceTracker,
			OriginalKey:         pathValue,
			ImagePath:           pathValue,
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		})
		existingNormalPaths[pathValue] = struct{}{}
		changed = true
	}
	if changed {
		*slots = normalizeSlotOrders(*slots)
	}
	return changed
}

func alignRenderableSlotsToSourceImagesByOrder(slots []api.ScreenshotSlot, sourceImages []api.ScreenshotImage) bool {
	changed := false
	sourceIdx := 0
	for idx := range slots {
		if !slots[idx].RenderInScreenshots {
			continue
		}
		if sourceIdx >= len(sourceImages) {
			slots[idx].RenderInScreenshots = false
			changed = true
			continue
		}
		pathValue := strings.TrimSpace(sourceImages[sourceIdx].Path)
		sourceIdx++
		if pathValue == "" || strings.TrimSpace(slots[idx].ImagePath) != "" {
			continue
		}
		slots[idx].ImagePath = pathValue
		slots[idx].DiscID = sourceImages[sourceIdx-1].DiscID
		if strings.TrimSpace(slots[idx].OriginalKey) == "" {
			slots[idx].OriginalKey = pathValue
		}
		changed = true
	}
	return changed
}

func screenshotURLMatchKey(rawURL string) string {
	trimmed := bbcode.NormalizeImageRawURL(strings.TrimSpace(rawURL))
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	filename := strings.ToLower(path.Base(parsed.Path))
	base := sanitizePersistedTrackerArtifactName(strings.TrimSuffix(filename, filepath.Ext(filename)))
	if base == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(trimmed))
	return fmt.Sprintf("%s|%x", base, digest[:6])
}

func screenshotSourceMatchKey(pathValue string) string {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(pathValue)))
	ext := path.Ext(base)
	if match := slotHashedArtifactPattern.FindStringSubmatch(strings.TrimSuffix(base, ext)); len(match) == 3 {
		return match[1] + "|" + match[2]
	}
	return screenshotBaseMatchKey(base)
}

func sourceImageForURL(sourceByKey map[string]api.ScreenshotImage, rawURL string) (api.ScreenshotImage, bool) {
	key := screenshotURLMatchKey(rawURL)
	if key == "" {
		return api.ScreenshotImage{}, false
	}
	if source, ok := sourceByKey[key]; ok {
		return source, true
	}
	base, _, _ := strings.Cut(key, "|")
	source, ok := sourceByKey[base]
	return source, ok
}

func screenshotBaseMatchKey(base string) string {
	base = strings.ToLower(strings.TrimSpace(base))
	if base == "" || base == "." || base == "/" {
		return ""
	}
	ext := path.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	base = slotArtifactSuffixPattern.ReplaceAllString(base, "")
	return sanitizePersistedTrackerArtifactName(base)
}

func appendSelectionOnlySlots(slots *[]api.ScreenshotSlot, selections []api.ScreenshotFinalSelection) {
	existingPaths := make(map[string]struct{}, len(*slots))
	for _, slot := range *slots {
		if pathValue := strings.TrimSpace(slot.ImagePath); pathValue != "" {
			existingPaths[pathValue] = struct{}{}
		}
	}
	for _, selection := range selections {
		pathValue := strings.TrimSpace(selection.ImagePath)
		if pathValue == "" {
			continue
		}
		if _, ok := existingPaths[pathValue]; ok {
			continue
		}
		*slots = append(*slots, api.ScreenshotSlot{
			SourcePath:          selection.SourcePath,
			DiscID:              selection.DiscID,
			SourceKind:          screenshotSlotSourceSelection,
			OriginalKey:         pathValue,
			ImagePath:           pathValue,
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		})
	}
}

func buildSelectionSlots(sourcePath string, selections []api.ScreenshotFinalSelection) []api.ScreenshotSlot {
	slots := make([]api.ScreenshotSlot, 0, len(selections))
	for _, selection := range selections {
		pathValue := strings.TrimSpace(selection.ImagePath)
		if pathValue == "" {
			continue
		}
		slots = append(slots, api.ScreenshotSlot{
			SourcePath:          sourcePath,
			DiscID:              selection.DiscID,
			SourceKind:          screenshotSlotSourceSelection,
			OriginalKey:         pathValue,
			ImagePath:           pathValue,
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		})
	}
	return normalizeSlotOrders(slots)
}

func buildTrackerURLSlots(sourcePath string, urls []string) []api.ScreenshotSlot {
	slots := make([]api.ScreenshotSlot, 0, len(urls))
	for _, rawURL := range urls {
		trimmed := strings.TrimSpace(rawURL)
		if trimmed == "" {
			continue
		}
		slots = append(slots, api.ScreenshotSlot{
			SourcePath:          sourcePath,
			SourceKind:          screenshotSlotSourceTracker,
			OriginalKey:         trimmed,
			OriginalURL:         trimmed,
			OriginalHost:        strings.TrimSpace(imagehost.ExtractHost(trimmed)),
			SectionKind:         screenshotSectionWrapped,
			RenderInScreenshots: true,
		})
	}
	return normalizeSlotOrders(slots)
}

func normalizeSlotOrders(slots []api.ScreenshotSlot) []api.ScreenshotSlot {
	for idx := range slots {
		slots[idx].SlotOrder = idx
		if strings.TrimSpace(slots[idx].OriginalKey) == "" {
			if strings.TrimSpace(slots[idx].ImagePath) != "" {
				slots[idx].OriginalKey = strings.TrimSpace(slots[idx].ImagePath)
			} else {
				slots[idx].OriginalKey = strings.TrimSpace(slots[idx].OriginalURL)
			}
		}
		for variantIdx := range slots[idx].Variants {
			slots[idx].Variants[variantIdx].SourcePath = slots[idx].SourcePath
			slots[idx].Variants[variantIdx].DiscID = slots[idx].DiscID
			slots[idx].Variants[variantIdx].SlotOrder = idx
			if strings.TrimSpace(slots[idx].Variants[variantIdx].UsageScope) == "" {
				slots[idx].Variants[variantIdx].UsageScope = globalImageUsageScope
			}
		}
	}
	return slots
}

// SlotUploadAttachmentResult contains updated screenshot slots and unmatched uploads.
type SlotUploadAttachmentResult struct {
	MatchedUploads   int
	FallbackMatched  int
	UnmatchedUploads int
}

// ApplyUploadedVariantsToSlots mutates slots by upserting host/scope variants.
// Uploads match by local path, then known URL; unmatched uploads use ordered
// fallback only when their count exactly matches remaining renderable pathless
// slots. The result reports upload counts, not variant insertions.
func ApplyUploadedVariantsToSlots(slots []api.ScreenshotSlot, uploads []api.UploadedImageLink) SlotUploadAttachmentResult {
	if len(slots) == 0 || len(uploads) == 0 {
		return SlotUploadAttachmentResult{}
	}
	slotsByPath := make(map[string][]*api.ScreenshotSlot, len(slots))
	slotByURL := make(map[string]*api.ScreenshotSlot, len(slots))
	slotIndexByPointer := make(map[*api.ScreenshotSlot]int, len(slots))
	for idx := range slots {
		slotIndexByPointer[&slots[idx]] = idx
		if pathValue := strings.TrimSpace(slots[idx].ImagePath); pathValue != "" {
			slotsByPath[pathValue] = append(slotsByPath[pathValue], &slots[idx])
		}
		if originalURL := strings.TrimSpace(slots[idx].OriginalURL); originalURL != "" {
			slotByURL[originalURL] = &slots[idx]
		}
	}
	directlyMatchedSlots := make(map[int]struct{}, len(slots))
	unmatchedUploads := make([]api.UploadedImageLink, 0)
	result := SlotUploadAttachmentResult{}
	seenUploads := make(map[string]struct{}, len(uploads))
	for _, upload := range uploads {
		if upload.Purpose == api.ScreenshotPurposeAudioAnalysis || sourceOnlyUploadedImage(upload) {
			continue
		}
		uploadKey := strings.ToLower(
			strings.TrimSpace(upload.Host),
		) + "\x00" + normalizeUsageScope(
			upload.UsageScope,
		) + "\x00" + strings.TrimSpace(
			upload.ImagePath,
		)
		if _, exists := seenUploads[uploadKey]; exists {
			continue
		}
		seenUploads[uploadKey] = struct{}{}
		matchedSlots := make([]*api.ScreenshotSlot, 0)
		if pathValue := strings.TrimSpace(upload.ImagePath); pathValue != "" {
			matchedSlots = append(matchedSlots, slotsByPath[pathValue]...)
		}
		if len(matchedSlots) == 0 {
			for _, candidate := range []string{strings.TrimSpace(upload.RawURL), strings.TrimSpace(upload.ImgURL), strings.TrimSpace(upload.WebURL)} {
				if candidate == "" {
					continue
				}
				if slot := slotByURL[candidate]; slot != nil {
					matchedSlots = append(matchedSlots, slot)
					break
				}
			}
		}
		if len(matchedSlots) == 0 {
			unmatchedUploads = append(unmatchedUploads, upload)
			continue
		}
		for _, slot := range matchedSlots {
			directlyMatchedSlots[slotIndexByPointer[slot]] = struct{}{}
			slot.Variants = upsertVariant(slot.Variants, api.ScreenshotSlotVariant{
				SourcePath: slot.SourcePath,
				DiscID:     firstNonEmptyScreenshotValue(upload.DiscID, slot.DiscID),
				SlotOrder:  slot.SlotOrder,
				Host:       strings.TrimSpace(upload.Host),
				UsageScope: normalizeUsageScope(upload.UsageScope),
				ImagePath:  strings.TrimSpace(upload.ImagePath),
				ImgURL:     strings.TrimSpace(upload.ImgURL),
				RawURL:     strings.TrimSpace(upload.RawURL),
				WebURL:     strings.TrimSpace(upload.WebURL),
				UploadedAt: upload.UploadedAt,
			})
		}
		result.MatchedUploads++
	}

	if len(unmatchedUploads) == 0 {
		return result
	}

	fallbackSlotIndexes := make([]int, 0, len(unmatchedUploads))
	for idx, slot := range renderableSlots(slots) {
		_ = idx
		slotIndex := slot.SlotOrder
		if slotIndex < 0 || slotIndex >= len(slots) {
			continue
		}
		if strings.TrimSpace(slots[slotIndex].ImagePath) != "" {
			continue
		}
		if _, ok := directlyMatchedSlots[slotIndex]; ok {
			continue
		}
		fallbackSlotIndexes = append(fallbackSlotIndexes, slotIndex)
	}
	if len(unmatchedUploads) != len(fallbackSlotIndexes) {
		result.UnmatchedUploads = len(unmatchedUploads)
		return result
	}

	for idx, upload := range unmatchedUploads {
		slot := &slots[fallbackSlotIndexes[idx]]
		if strings.TrimSpace(slot.ImagePath) == "" {
			slot.ImagePath = strings.TrimSpace(upload.ImagePath)
			slot.DiscID = upload.DiscID
			if strings.TrimSpace(slot.OriginalKey) == "" {
				slot.OriginalKey = slot.ImagePath
			}
		}
		slot.Variants = upsertVariant(slot.Variants, api.ScreenshotSlotVariant{
			SourcePath: slot.SourcePath,
			DiscID:     firstNonEmptyScreenshotValue(upload.DiscID, slot.DiscID),
			SlotOrder:  slot.SlotOrder,
			Host:       strings.TrimSpace(upload.Host),
			UsageScope: normalizeUsageScope(upload.UsageScope),
			ImagePath:  strings.TrimSpace(upload.ImagePath),
			ImgURL:     strings.TrimSpace(upload.ImgURL),
			RawURL:     strings.TrimSpace(upload.RawURL),
			WebURL:     strings.TrimSpace(upload.WebURL),
			UploadedAt: upload.UploadedAt,
		})
		result.MatchedUploads++
		result.FallbackMatched++
	}
	return result
}

func applyUploadedVariantsToSlots(slots []api.ScreenshotSlot, uploads []api.UploadedImageLink) SlotUploadAttachmentResult {
	return ApplyUploadedVariantsToSlots(slots, uploads)
}

func upsertVariant(variants []api.ScreenshotSlotVariant, variant api.ScreenshotSlotVariant) []api.ScreenshotSlotVariant {
	for idx := range variants {
		if strings.EqualFold(strings.TrimSpace(variants[idx].Host), strings.TrimSpace(variant.Host)) &&
			normalizeUsageScope(variants[idx].UsageScope) == normalizeUsageScope(variant.UsageScope) {
			variants[idx] = variant
			return variants
		}
	}
	return append(variants, variant)
}

func cloneScreenshotSlots(slots []api.ScreenshotSlot) []api.ScreenshotSlot {
	if len(slots) == 0 {
		return nil
	}
	cloned := make([]api.ScreenshotSlot, len(slots))
	for idx := range slots {
		cloned[idx] = slots[idx]
		if len(slots[idx].Variants) > 0 {
			cloned[idx].Variants = append([]api.ScreenshotSlotVariant(nil), slots[idx].Variants...)
		}
	}
	return cloned
}

func selectScreenshotsFromSlots(tracker string, slots []api.ScreenshotSlot, policy imageHostPolicy) ([]api.ScreenshotImage, string, string, error) {
	renderable := renderableSlots(slots)
	if len(renderable) == 0 {
		return nil, "", "", nil
	}

	results := make([]api.ScreenshotImage, 0, len(renderable))
	resolvedHosts := make([]string, 0, len(renderable))
	resolvedScopes := make([]string, 0, len(renderable))
	for _, slot := range renderable {
		image, host, scope, ok := selectSlotImageForTracker(slot, tracker, policy)
		if !ok {
			if len(policy.allowed) > 0 {
				return nil, "", "", fmt.Errorf("missing eligible screenshot variant for slot %d (%s)", slot.SlotOrder, slotIdentity(slot))
			}
			return nil, "", "", fmt.Errorf("missing screenshot variant for slot %d (%s)", slot.SlotOrder, slotIdentity(slot))
		}
		image.Index = len(results)
		results = append(results, image)
		if strings.TrimSpace(host) != "" {
			resolvedHosts = append(resolvedHosts, strings.ToLower(strings.TrimSpace(host)))
		}
		if strings.TrimSpace(scope) != "" {
			resolvedScopes = append(resolvedScopes, normalizeUsageScope(scope))
		}
	}
	if len(results) == 0 {
		return nil, "", "", nil
	}
	return results, collapseResolvedValue(resolvedHosts), collapseResolvedValue(resolvedScopes), nil
}

func renderableSlots(slots []api.ScreenshotSlot) []api.ScreenshotSlot {
	results := make([]api.ScreenshotSlot, 0, len(slots))
	for _, slot := range slots {
		if !slot.RenderInScreenshots {
			continue
		}
		results = append(results, slot)
	}
	return results
}

func selectSlotImageForTracker(slot api.ScreenshotSlot, tracker string, policy imageHostPolicy) (api.ScreenshotImage, string, string, bool) {
	if image, host, scope, ok := selectVariantForSlot(slot, tracker, policy); ok {
		return image, host, scope, true
	}

	directURL := imagehost.DirectImageURL(slot.OriginalURL)
	host := strings.TrimSpace(imagehost.ExtractHost(directURL))
	if directURL != "" && host != "" && reusableSourceImageURL(directURL, policy) && !hostInList(host, policy.failed) &&
		(len(policy.allowed) == 0 || hostAllowed(host, policy.allowed)) {
		return api.ScreenshotImage{
			DiscID: slot.DiscID,
			Path:   strings.TrimSpace(slot.ImagePath),
			Host:   host,
			ImgURL: directURL,
			RawURL: directURL,
			WebURL: directURL,
		}, host, globalImageUsageScope, true
	}
	rawURL := strings.TrimSpace(slot.OriginalURL)
	if imagehost.IsWsrvProxyURL(rawURL) && policy.sourceOnlyAllowed != nil && policy.sourceOnlyAllowed(rawURL) {
		proxyURL, _ := url.Parse(rawURL)
		host = strings.ToLower(proxyURL.Hostname())
		if host != "" && !hostInList(host, policy.failed) && (len(policy.allowed) == 0 || hostAllowed(host, policy.allowed)) {
			return api.ScreenshotImage{
				DiscID: slot.DiscID,
				Path:   strings.TrimSpace(slot.ImagePath),
				Host:   host,
				ImgURL: rawURL,
				RawURL: rawURL,
				WebURL: rawURL,
			}, host, globalImageUsageScope, true
		}
	}

	return api.ScreenshotImage{}, "", "", false
}

func reusableSourceImageURL(rawURL string, policy imageHostPolicy) bool {
	if imagehost.IsWsrvProxyURL(rawURL) {
		return false
	}
	return !imagehost.IsSourceOnlyURL(rawURL) || policy.sourceOnlyAllowed != nil && policy.sourceOnlyAllowed(rawURL)
}

// attachNativeSourceURLsToSlots restores a saved source URL for selected
// local tracker artifacts when the destination permits source-only reuse.
func attachNativeSourceURLsToSlots(slots []api.ScreenshotSlot, records []api.TrackerMetadata, policy imageHostPolicy) bool {
	if policy.sourceOnlyAllowed == nil {
		return false
	}
	changed := false
	for index := range slots {
		slot := &slots[index]
		if slot.OriginalURL != "" || slot.ImagePath == "" {
			continue
		}
		for _, record := range records {
			trackerDir := sanitizePersistedTrackerArtifactName(strings.ToLower(strings.TrimSpace(record.Tracker)))
			if trackerDir == "" || !strings.EqualFold(filepath.Base(filepath.Dir(slot.ImagePath)), trackerDir) {
				continue
			}
			for urlIndex, rawURL := range record.ImageURLs {
				if !imagehost.IsSourceOnlyURL(rawURL) || !policy.sourceOnlyAllowed(rawURL) {
					continue
				}
				for _, candidate := range localTrackerArtifactPaths(filepath.Dir(slot.ImagePath), rawURL, urlIndex) {
					if pathutil.SamePath(candidate, slot.ImagePath) {
						slot.OriginalURL = rawURL
						slot.OriginalHost = imagehost.ExtractHost(rawURL)
						changed = true
						break
					}
				}
				if slot.OriginalURL != "" {
					break
				}
			}
			if slot.OriginalURL != "" {
				break
			}
		}
	}
	return changed
}

func selectVariantForSlot(slot api.ScreenshotSlot, tracker string, policy imageHostPolicy) (api.ScreenshotImage, string, string, bool) {
	preferredScopes := []string{trackerImageUsageScope(tracker), globalImageUsageScope}

	for _, scope := range preferredScopes {
		candidates := make([]api.ScreenshotSlotVariant, 0)
		for _, variant := range slot.Variants {
			if !reusableSourceImageURL(variant.RawURL, policy) || !reusableSourceImageURL(variant.ImgURL, policy) ||
				!reusableSourceImageURL(variant.WebURL, policy) {
				continue
			}
			if normalizeUsageScope(variant.UsageScope) != scope {
				continue
			}
			host := strings.ToLower(strings.TrimSpace(variant.Host))
			if hostInList(host, policy.failed) {
				continue
			}
			if len(policy.allowed) > 0 && !hostAllowed(host, policy.allowed) {
				continue
			}
			candidates = append(candidates, variant)
		}
		if len(candidates) == 0 {
			continue
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			left := strings.ToLower(strings.TrimSpace(candidates[i].Host))
			right := strings.ToLower(strings.TrimSpace(candidates[j].Host))
			leftPreferred := preferredHostOrder(left, policy.preferred)
			rightPreferred := preferredHostOrder(right, policy.preferred)
			if leftPreferred != rightPreferred {
				return leftPreferred < rightPreferred
			}
			if !candidates[i].UploadedAt.Equal(candidates[j].UploadedAt) {
				return candidates[i].UploadedAt.After(candidates[j].UploadedAt)
			}
			return left < right
		})
		chosen := candidates[0]
		return api.ScreenshotImage{
			DiscID:     firstNonEmptyScreenshotValue(chosen.DiscID, slot.DiscID),
			Path:       strings.TrimSpace(chosen.ImagePath),
			Host:       strings.TrimSpace(chosen.Host),
			ImgURL:     strings.TrimSpace(chosen.ImgURL),
			RawURL:     strings.TrimSpace(chosen.RawURL),
			WebURL:     strings.TrimSpace(chosen.WebURL),
			UploadedAt: chosen.UploadedAt,
		}, chosen.Host, chosen.UsageScope, true
	}
	return api.ScreenshotImage{}, "", "", false
}

func allRenderableSlotsHaveEligibleVariant(slots []api.ScreenshotSlot, tracker string, policy imageHostPolicy) bool {
	renderable := renderableSlots(slots)
	if len(renderable) == 0 {
		return false
	}
	for _, slot := range renderable {
		found := false
		for _, variant := range slot.Variants {
			if !reusableSourceImageURL(variant.RawURL, policy) || !reusableSourceImageURL(variant.ImgURL, policy) ||
				!reusableSourceImageURL(variant.WebURL, policy) {
				continue
			}
			if !uploadEligibleForTracker(variant.UsageScope, tracker) {
				continue
			}
			if hostInList(variant.Host, policy.failed) {
				continue
			}
			if len(policy.allowed) > 0 && !hostAllowed(variant.Host, policy.allowed) {
				continue
			}
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func preferredHostOrder(host string, preferred []string) int {
	for idx, value := range preferred {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(host)) {
			return idx
		}
	}
	return len(preferred) + 1
}

func collapseResolvedValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	first := strings.TrimSpace(values[0])
	for _, value := range values[1:] {
		if strings.TrimSpace(value) != first {
			return mixedSlotResolutionValue
		}
	}
	return first
}

func slotIdentity(slot api.ScreenshotSlot) string {
	for _, candidate := range []string{
		strings.TrimSpace(slot.ImagePath),
		strings.TrimSpace(slot.OriginalURL),
		strings.TrimSpace(slot.OriginalKey),
	} {
		if candidate != "" {
			return candidate
		}
	}
	return "unknown"
}

func upsertScreenshotVariantsFromUploads(
	ctx context.Context,
	repo UploadPersistence,
	binding api.PreparedMediaBinding,
	slots []api.ScreenshotSlot,
	uploads []api.UploadedImageLink,
) error {
	if repo == nil || len(slots) == 0 || len(uploads) == 0 {
		return nil
	}
	slotsByPath := make(map[string][]int, len(slots))
	for idx := range slots {
		if pathValue := strings.TrimSpace(slots[idx].ImagePath); pathValue != "" {
			slotsByPath[pathValue] = append(slotsByPath[pathValue], slots[idx].SlotOrder)
		}
	}
	variants := make([]api.ScreenshotSlotVariant, 0, len(uploads))
	for _, upload := range uploads {
		slotOrders := slotsByPath[strings.TrimSpace(upload.ImagePath)]
		if len(slotOrders) == 0 {
			continue
		}
		for _, slotOrder := range slotOrders {
			variants = append(variants, api.ScreenshotSlotVariant{
				DiscID:     upload.DiscID,
				SlotOrder:  slotOrder,
				Host:       strings.TrimSpace(upload.Host),
				UsageScope: normalizeUsageScope(upload.UsageScope),
				ImagePath:  strings.TrimSpace(upload.ImagePath),
				ImgURL:     strings.TrimSpace(upload.ImgURL),
				RawURL:     strings.TrimSpace(upload.RawURL),
				WebURL:     strings.TrimSpace(upload.WebURL),
				UploadedAt: upload.UploadedAt,
			})
		}
	}
	return wrapTrackerError(repo.UpsertScreenshotSlotVariants(ctx, binding, variants))
}

func slotSourceImagesForRehost(slots []api.ScreenshotSlot) []api.ScreenshotImage {
	renderable := renderableSlots(slots)
	results := make([]api.ScreenshotImage, 0, len(renderable))
	for _, slot := range renderable {
		pathValue := strings.TrimSpace(slot.ImagePath)
		if pathValue == "" {
			continue
		}
		results = append(results, api.ScreenshotImage{
			DiscID: slot.DiscID,
			Index:  preservedScreenshotImageIndex(slot.SlotOrder),
			Path:   pathValue,
		})
	}
	return results
}

func errorsIsNotFound(err error) bool {
	return errors.Is(err, internalerrors.ErrNotFound)
}

func firstNonEmptyScreenshotValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func syncSlotVariantsToPreloaded(preloaded *preloadedDescriptionAssetData, uploads []api.UploadedImageLink) {
	if preloaded == nil || len(preloaded.screenshotSlots) == 0 || len(uploads) == 0 {
		return
	}
	applyUploadedVariantsToSlots(preloaded.screenshotSlots, uploads)
}

func syncSlotsToPreloaded(preloaded *preloadedDescriptionAssetData, slots []api.ScreenshotSlot) {
	if preloaded == nil {
		return
	}
	preloaded.screenshotSlots = cloneScreenshotSlots(slots)
	preloaded.screenshotSlotsLoaded = true
}
