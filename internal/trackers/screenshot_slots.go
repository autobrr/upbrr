// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path" //nolint:depguard // Manipulates URL/torrent-style slash paths, not local filesystem paths.
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/services/imagehost"
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
	slotWrapperPattern    = regexp.MustCompile(`(?is)\[(?:center|align=[^\]]+)\]([\s\S]*?)\[/(?:center|align)\]`)
	slotComparisonPattern = regexp.MustCompile(`(?is)\[comparison=([^\]]+)\]([\s\S]*?)\[/comparison\]`)
	slotComparisonURL     = regexp.MustCompile(`(?i)https?://[^\s\]]+\.(?:png|jpe?g|gif|webp)`)
	slotURLImgPattern     = regexp.MustCompile(`(?is)\[url=(https?://[^\]]+)\]\s*\[img[^\]]*\](.*?)\[/img\]\s*\[/url\]`)
	slotImgPattern        = regexp.MustCompile(`(?is)\[img[^\]]*\](.*?)\[/img\]`)
	posterLikeSlotHosts   = map[string]struct{}{"image.tmdb.org": {}, "themoviedb.org": {}, "www.themoviedb.org": {}}
)

type parsedDescriptionSlot struct {
	start int
	slot  api.ScreenshotSlot
}

func screenshotSlotsFromSource(
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
	if len(slots) == 0 {
		return nil, nil
	}
	if err := repo.ReplaceScreenshotSlots(ctx, meta.SourcePath, slots); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	return cloneScreenshotSlots(slots), nil
}

func filterStoredSlotsForSelectedImages(
	ctx context.Context,
	meta api.PreparedMetadata,
	repo api.MetadataRepository,
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
	meta api.PreparedMetadata,
	repo api.MetadataRepository,
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
	meta api.PreparedMetadata,
	repo api.MetadataRepository,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
) ([]api.ScreenshotSlot, error) {
	description, _, _ := resolveTrackerDescription(ctx, tracker, meta, repo, logger, preloaded)
	selections, err := finalSelectionsFromSource(ctx, meta, repo, preloaded)
	if err != nil && !errorsIsNotFound(err) {
		return nil, err
	}
	sort.Slice(selections, func(i, j int) bool { return selections[i].Order < selections[j].Order })

	trackerRecords, err := trackerMetadataFromSource(ctx, meta, repo, preloaded)
	if err != nil && !errorsIsNotFound(err) {
		return nil, err
	}
	uploads, err := uploadedImagesFromSource(ctx, meta, repo, preloaded)
	if err != nil && !errorsIsNotFound(err) {
		return nil, err
	}

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

	urls := collectImageURLs(trackerRecords)
	if len(urls) == 0 {
		urls = collectImageURLs(meta.TrackerData)
	}
	slots = buildTrackerURLSlots(meta.SourcePath, urls)
	applyUploadedVariantsToSlots(slots, uploads)
	return normalizeSlotOrders(slots), nil
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
			if _, ok := sourceByKey[screenshotURLMatchKey(slots[idx].OriginalURL)]; ok {
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
			key := screenshotURLMatchKey(slots[idx].OriginalURL)
			source, ok := sourceByKey[key]
			if !ok {
				slots[idx].RenderInScreenshots = false
				changed = true
				continue
			}
			matched = true
			pathValue := strings.TrimSpace(source.Path)
			if pathValue != "" && strings.TrimSpace(slots[idx].ImagePath) == "" {
				slots[idx].ImagePath = pathValue
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
		source, ok := sourceByKey[screenshotURLMatchKey(slots[idx].OriginalURL)]
		if !ok {
			continue
		}
		pathValue := strings.TrimSpace(source.Path)
		if pathValue == "" {
			continue
		}
		slots[idx].ImagePath = pathValue
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
		if strings.TrimSpace(slots[idx].OriginalKey) == "" {
			slots[idx].OriginalKey = pathValue
		}
		changed = true
	}
	return changed
}

func screenshotURLMatchKey(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return screenshotBaseMatchKey(path.Base(parsed.Path))
}

func screenshotSourceMatchKey(pathValue string) string {
	return screenshotBaseMatchKey(filepath.Base(strings.TrimSpace(pathValue)))
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
	base = regexp.MustCompile(`_\d+$`).ReplaceAllString(base, "")
	return base
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
			slots[idx].Variants[variantIdx].SlotOrder = idx
			if strings.TrimSpace(slots[idx].Variants[variantIdx].UsageScope) == "" {
				slots[idx].Variants[variantIdx].UsageScope = globalImageUsageScope
			}
		}
	}
	return slots
}

type SlotUploadAttachmentResult struct {
	MatchedUploads   int
	FallbackMatched  int
	UnmatchedUploads int
}

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
		uploadKey := strings.ToLower(strings.TrimSpace(upload.Host)) + "\x00" + normalizeUsageScope(upload.UsageScope) + "\x00" + strings.TrimSpace(upload.ImagePath)
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
			if strings.TrimSpace(slot.OriginalKey) == "" {
				slot.OriginalKey = slot.ImagePath
			}
		}
		slot.Variants = upsertVariant(slot.Variants, api.ScreenshotSlotVariant{
			SourcePath: slot.SourcePath,
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

	if len(policy.allowed) == 0 || hostAllowed(slot.OriginalHost, policy.allowed) {
		originalURL := strings.TrimSpace(slot.OriginalURL)
		if originalURL != "" {
			host := strings.TrimSpace(slot.OriginalHost)
			if host == "" {
				host = strings.TrimSpace(imagehost.ExtractHost(originalURL))
			}
			return api.ScreenshotImage{
				Path:   strings.TrimSpace(slot.ImagePath),
				Host:   host,
				ImgURL: originalURL,
				RawURL: originalURL,
				WebURL: originalURL,
			}, host, globalImageUsageScope, true
		}
	}

	return api.ScreenshotImage{}, "", "", false
}

func selectVariantForSlot(slot api.ScreenshotSlot, tracker string, policy imageHostPolicy) (api.ScreenshotImage, string, string, bool) {
	preferredScopes := []string{trackerImageUsageScope(tracker), globalImageUsageScope}

	for _, scope := range preferredScopes {
		candidates := make([]api.ScreenshotSlotVariant, 0)
		for _, variant := range slot.Variants {
			if normalizeUsageScope(variant.UsageScope) != scope {
				continue
			}
			host := strings.ToLower(strings.TrimSpace(variant.Host))
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
			if !uploadEligibleForTracker(variant.UsageScope, tracker) {
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

func upsertScreenshotVariantsFromUploads(ctx context.Context, repo api.MetadataRepository, sourcePath string, slots []api.ScreenshotSlot, uploads []api.UploadedImageLink) error {
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
				SourcePath: sourcePath,
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
	return wrapTrackerError(repo.UpsertScreenshotSlotVariants(ctx, sourcePath, variants))
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
			Index: preservedScreenshotImageIndex(slot.SlotOrder),
			Path:  pathValue,
		})
	}
	return results
}

func errorsIsNotFound(err error) bool {
	return errors.Is(err, internalerrors.ErrNotFound)
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
