// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackerdata"
	trackerscatalog "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	defaultTrackerCooldown = 15 * time.Second
	ptpTrackerCooldown     = 60 * time.Second
	trackerLookupWorkers   = 4
)

func (s *Service) EnrichTrackerData(ctx context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	select {
	case <-ctx.Done():
		return api.PreparedMetadata{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	if s.repo == nil {
		return api.PreparedMetadata{}, internalerrors.ErrInvalidInput
	}
	if strings.TrimSpace(meta.SourcePath) == "" {
		return api.PreparedMetadata{}, internalerrors.ErrInvalidInput
	}
	if meta.StoredDataFresh {
		if s.logger != nil {
			s.logger.Debugf("metadata: skipping tracker lookup, stored metadata snapshot is fresh for %s", meta.SourcePath)
		}
		return meta, nil
	}

	candidates := resolveTrackerCandidates(meta)
	if len(candidates) == 0 {
		return meta, nil
	}

	trackers := normalizeTrackers(candidates)
	if s.logger != nil {
		configured, missing := configuredTrackers(s.cfg)
		s.logger.Debugf("metadata: tracker candidates %v", trackers)
		if len(configured) > 0 {
			s.logger.Debugf("metadata: trackers configured %v", configured)
		}
		if len(missing) > 0 {
			s.logger.Tracef("metadata: trackers missing api_key/announce_url %v", missing)
		}
	}
	if s.logger != nil {
		logPathedTrackerDetails(meta, s.logger)
		logClientSearchIDs(meta, s.logger)
	}
	trackers = filterConfiguredTrackers(s.cfg, trackers, s.logger)
	trackers = orderTrackersByPriority(trackers)
	trackers = reorderTrackersForMetadataNeeds(trackers, meta.Options)
	trackers = applyPreferredTracker(trackers, s.cfg.Trackers.PreferredTracker)
	if len(trackers) == 0 {
		if s.logger != nil {
			s.logger.Debugf("metadata: no configured trackers matched pathed search")
		}
		return meta, nil
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: using trackers %v", trackers)
	}

	now := time.Now().UTC()
	meta.TrackerData = append([]api.TrackerMetadata{}, meta.TrackerData...)
	unit3dClient := trackerdata.NewClient(s.cfg, s.logger, nil)
	eligible := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		select {
		case <-ctx.Done():
			return api.PreparedMetadata{}, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}
		if s.isTrackerCoolingDown(ctx, tracker, now) {
			continue
		}
		eligible = append(eligible, tracker)
	}
	if len(eligible) == 0 {
		return meta, nil
	}

	if !shouldUseStrictPriorityLookup(meta, eligible, s.cfg.Trackers.PreferredTracker) {
		return s.enrichTrackerDataConcurrent(ctx, meta, eligible, now, unit3dClient)
	}

	return s.enrichTrackerDataPriority(ctx, meta, eligible, now, unit3dClient)
}

func shouldUseStrictPriorityLookup(meta api.PreparedMetadata, eligible []string, preferred string) bool {
	if len(meta.TrackerIDs) > 0 {
		return true
	}
	preferred = strings.TrimSpace(preferred)
	if preferred == "" {
		return false
	}
	for _, tracker := range eligible {
		if strings.EqualFold(strings.TrimSpace(tracker), preferred) {
			return true
		}
	}
	return false
}

func (s *Service) enrichTrackerDataPriority(
	ctx context.Context,
	meta api.PreparedMetadata,
	eligible []string,
	now time.Time,
	unit3dClient *trackerdata.Client,
) (api.PreparedMetadata, error) {
	assetSourceTracker := ""
	for _, tracker := range eligible {
		select {
		case <-ctx.Done():
			return api.PreparedMetadata{}, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		record, persistable, hasIDs, err := s.lookupTrackerData(ctx, meta, tracker, now, unit3dClient)
		if err != nil {
			if s.logger != nil {
				s.logger.Warnf("metadata: tracker lookup failed tracker=%s: %s", tracker, redaction.RedactValue(err.Error(), nil))
			}
			continue
		}
		if !persistable {
			continue
		}

		if trackerRecordHasDescriptionAssets(record) {
			if assetSourceTracker == "" {
				assetSourceTracker = strings.ToUpper(strings.TrimSpace(tracker))
				if s.logger != nil {
					s.logger.Debugf("metadata: description/image source tracker selected: %s", assetSourceTracker)
				}
			} else if !strings.EqualFold(assetSourceTracker, tracker) {
				if s.logger != nil {
					s.logger.Debugf("metadata: ignoring description/images from %s (source=%s)", strings.ToUpper(strings.TrimSpace(tracker)), assetSourceTracker)
				}
				record.Description = ""
				record.ImageURLs = nil
			}
		}

		if err := s.repo.SaveTrackerMetadata(ctx, record); err != nil {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: save tracker metadata: %w", err)
		}
		if err := s.repo.SaveTrackerTimestamp(ctx, db.TrackerTimestamp{Tracker: tracker, UpdatedAt: now}); err != nil {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: save tracker timestamp: %w", err)
		}
		meta.TrackerData = append(meta.TrackerData, record)
		if hasIDs {
			if s.logger != nil {
				s.logger.Debugf("metadata: tracker lookup resolved ids via %s; stopping by priority order", tracker)
			}
			break
		}
	}

	return meta, nil
}

func (s *Service) enrichTrackerDataConcurrent(
	ctx context.Context,
	meta api.PreparedMetadata,
	eligible []string,
	now time.Time,
	unit3dClient *trackerdata.Client,
) (api.PreparedMetadata, error) {
	lookupCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan string, len(eligible))
	results := make(chan trackerLookupOutcome, len(eligible))

	workerCount := min(trackerLookupWorkers, len(eligible))
	if workerCount <= 0 {
		workerCount = 1
	}

	lookupMeta := meta
	var workers sync.WaitGroup
	for idx := 0; idx < workerCount; idx++ {
		workers.Go(func() {
			for tracker := range jobs {
				record, persistable, hasIDs, err := s.lookupTrackerData(lookupCtx, lookupMeta, tracker, now, unit3dClient)
				results <- trackerLookupOutcome{
					tracker:     tracker,
					record:      record,
					persistable: persistable,
					hasIDs:      hasIDs,
					err:         err,
				}
			}
		})
	}

	for _, tracker := range eligible {
		jobs <- tracker
	}
	close(jobs)

	winnerResolved := false
	assetSourceTracker := ""
	for range eligible {
		outcome := <-results
		if outcome.err != nil {
			if s.logger != nil {
				s.logger.Warnf("metadata: tracker lookup failed tracker=%s: %s", outcome.tracker, redaction.RedactValue(outcome.err.Error(), nil))
			}
			continue
		}
		if !outcome.persistable {
			continue
		}
		if winnerResolved {
			continue
		}

		if trackerRecordHasDescriptionAssets(outcome.record) {
			if assetSourceTracker == "" {
				assetSourceTracker = strings.ToUpper(strings.TrimSpace(outcome.tracker))
				if s.logger != nil {
					s.logger.Debugf("metadata: description/image source tracker selected: %s", assetSourceTracker)
				}
			} else if !strings.EqualFold(assetSourceTracker, outcome.tracker) {
				if s.logger != nil {
					s.logger.Debugf("metadata: ignoring description/images from %s (source=%s)", strings.ToUpper(strings.TrimSpace(outcome.tracker)), assetSourceTracker)
				}
				outcome.record.Description = ""
				outcome.record.ImageURLs = nil
			}
		}

		if err := s.repo.SaveTrackerMetadata(ctx, outcome.record); err != nil {
			workers.Wait()
			return api.PreparedMetadata{}, fmt.Errorf("metadata: save tracker metadata: %w", err)
		}
		if err := s.repo.SaveTrackerTimestamp(ctx, db.TrackerTimestamp{Tracker: outcome.tracker, UpdatedAt: now}); err != nil {
			workers.Wait()
			return api.PreparedMetadata{}, fmt.Errorf("metadata: save tracker timestamp: %w", err)
		}
		meta.TrackerData = append(meta.TrackerData, outcome.record)
		if outcome.hasIDs {
			winnerResolved = true
			cancel()
			if s.logger != nil {
				s.logger.Debugf("metadata: tracker lookup resolved ids via %s; stopping at fastest winner", outcome.tracker)
			}
		}
	}

	workers.Wait()

	return meta, nil
}

type trackerLookupOutcome struct {
	tracker     string
	record      api.TrackerMetadata
	persistable bool
	hasIDs      bool
	err         error
}

func (s *Service) lookupTrackerData(
	ctx context.Context,
	meta api.PreparedMetadata,
	tracker string,
	now time.Time,
	unit3dClient *trackerdata.Client,
) (api.TrackerMetadata, bool, bool, error) {
	select {
	case <-ctx.Done():
		return api.TrackerMetadata{}, false, false, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	record := api.TrackerMetadata{
		SourcePath: meta.SourcePath,
		Tracker:    tracker,
		TrackerID:  trackerIDFor(meta, tracker),
		InfoHash:   meta.InfoHash,
		Matched:    trackerMatched(meta, tracker),
		UpdatedAt:  now,
	}

	if trackerdata.IsUnit3DTracker(tracker) {
		fileName := trackerLookupFileName(meta, record.TrackerID, s.cfg.Metadata.SkipTrackerFilenameLookup)
		if s.logger != nil {
			s.logger.Tracef("metadata: unit3d lookup start tracker=%s id=%q file=%q", tracker, record.TrackerID, fileName)
		}
		result, err := unit3dClient.TorrentInfo(
			ctx,
			tracker,
			record.TrackerID,
			fileName,
			meta.Options.OnlyID,
			meta.Options.KeepImages,
		)
		if err != nil {
			return api.TrackerMetadata{}, false, false, fmt.Errorf("metadata: %w", err)
		}
		if !hasUnit3DData(result) {
			if s.logger != nil {
				s.logger.Debugf("metadata: unit3d lookup empty tracker=%s id=%q", tracker, record.TrackerID)
			}
			if !trackerRecordHasPathedData(record) {
				if s.logger != nil {
					s.logger.Debugf("metadata: tracker %s has no pathed data, skipping", tracker)
				}
				return api.TrackerMetadata{}, false, false, nil
			}
			return record, true, false, nil
		}

		downloadedImages := s.persistUnit3DArtifacts(ctx, meta, tracker, result, meta.Options.KeepImages)
		if s.logger != nil {
			s.logger.Debugf("metadata: unit3d downloaded %d of %d images", len(downloadedImages), len(result.Validated))
		}
		record.TMDBID = result.TMDBID
		record.IMDBID = result.IMDBID
		record.TVDBID = result.TVDBID
		record.MALID = result.MALID
		record.Category = normalizeUnit3DCategory(result.Category)
		record.InfoHash = metautil.FirstNonEmptyTrimmed(record.InfoHash, result.InfoHash)
		record.Description = result.Description
		record.ImageURLs = trackerImageURLsFromResult(result, downloadedImages, meta.Options.KeepImages)
		record.Filename = result.FileName
		record.Matched = true
		if s.logger != nil {
			s.logger.Debugf(
				"metadata: unit3d lookup applied tracker=%s tmdb=%d imdb=%d tvdb=%d mal=%d desc=%t images=%d infohash=%t file=%t",
				tracker,
				record.TMDBID,
				record.IMDBID,
				record.TVDBID,
				record.MALID,
				record.Description != "",
				len(record.ImageURLs),
				record.InfoHash != "",
				record.Filename != "",
			)
		}

		if !trackerRecordHasPathedData(record) {
			if s.logger != nil {
				s.logger.Debugf("metadata: tracker %s has no pathed data, skipping", tracker)
			}
			return api.TrackerMetadata{}, false, false, nil
		}
		return record, true, hasTrackerMetadataIDs(record), nil
	}

	if s.tracker == nil {
		if !trackerRecordHasPathedData(record) {
			if s.logger != nil {
				s.logger.Debugf("metadata: tracker %s has no pathed data, skipping", tracker)
			}
			return api.TrackerMetadata{}, false, false, nil
		}
		return record, true, false, nil
	}

	result, err := s.tracker.Lookup(
		ctx,
		tracker,
		record.TrackerID,
		meta,
		trackerLookupFileName(meta, record.TrackerID, s.cfg.Metadata.SkipTrackerFilenameLookup),
		meta.Options.OnlyID,
		meta.Options.KeepImages,
	)
	if err != nil {
		return api.TrackerMetadata{}, false, false, fmt.Errorf("metadata: %w", err)
	}
	if !result.HasData() {
		if s.logger != nil {
			s.logger.Debugf("metadata: tracker lookup empty tracker=%s id=%q", tracker, record.TrackerID)
		}
		if !trackerRecordHasPathedData(record) {
			if s.logger != nil {
				s.logger.Debugf("metadata: tracker %s has no pathed data, skipping", tracker)
			}
			return api.TrackerMetadata{}, false, false, nil
		}
		return record, true, false, nil
	}

	applyTrackerDataResult(&record, result)
	if strings.TrimSpace(result.Description) != "" || len(result.Images) > 0 {
		downloadedImages := s.persistUnit3DArtifacts(
			ctx,
			meta,
			tracker,
			trackerdata.Result{Description: result.Description, Validated: result.Images},
			meta.Options.KeepImages,
		)
		record.ImageURLs = trackerImageURLsFromResult(result, downloadedImages, meta.Options.KeepImages)
	}
	if s.logger != nil {
		s.logger.Debugf(
			"metadata: tracker lookup applied tracker=%s tmdb=%d imdb=%d tvdb=%d desc=%t images=%d infohash=%t file=%t",
			tracker,
			record.TMDBID,
			record.IMDBID,
			record.TVDBID,
			record.Description != "",
			len(record.ImageURLs),
			record.InfoHash != "",
			record.Filename != "",
		)
	}

	if !trackerRecordHasPathedData(record) {
		if s.logger != nil {
			s.logger.Debugf("metadata: tracker %s has no pathed data, skipping", tracker)
		}
		return api.TrackerMetadata{}, false, false, nil
	}
	return record, true, hasTrackerMetadataIDs(record), nil
}

func trackerImageURLsFromResult(_ trackerdata.Result, downloadedImages []string, keepImages bool) []string {
	if !keepImages || len(downloadedImages) == 0 {
		return nil
	}
	urls := make([]string, len(downloadedImages))
	hasUsable := false
	for idx, imageURL := range downloadedImages {
		trimmed := strings.TrimSpace(imageURL)
		if trimmed == "" {
			continue
		}
		urls[idx] = trimmed
		hasUsable = true
	}
	if !hasUsable {
		return nil
	}
	return urls
}

func trackerRecordHasPathedData(record api.TrackerMetadata) bool {
	return record.TrackerID != "" || record.InfoHash != "" || record.Matched
}

func trackerRecordHasDescriptionAssets(record api.TrackerMetadata) bool {
	return strings.TrimSpace(record.Description) != "" || len(record.ImageURLs) > 0
}

func (s *Service) isTrackerCoolingDown(ctx context.Context, tracker string, now time.Time) bool {
	last, err := s.repo.GetTrackerTimestamp(ctx, tracker)
	if err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			return false
		}
		if s.logger != nil {
			s.logger.Warnf("metadata: tracker timestamp lookup failed for %s: %v", tracker, err)
		}
		return false
	}
	cooldown := trackerCooldown(tracker)
	if cooldown <= 0 {
		return false
	}
	if now.Sub(last) < cooldown {
		if s.logger != nil {
			s.logger.Debugf("metadata: tracker %s cooldown active", tracker)
		}
		return true
	}
	return false
}

func trackerCooldown(tracker string) time.Duration {
	if strings.EqualFold(tracker, "PTP") {
		return ptpTrackerCooldown
	}
	return defaultTrackerCooldown
}

func resolveTrackerCandidates(meta api.PreparedMetadata) []string {
	if len(meta.TrackerIDs) > 0 {
		result := make([]string, 0, len(meta.TrackerIDs))
		for key := range meta.TrackerIDs {
			result = append(result, key)
		}
		return result
	}
	if len(meta.MatchedTrackers) > 0 {
		return meta.MatchedTrackers
	}
	if len(meta.Trackers) > 0 {
		return meta.Trackers
	}
	return nil
}

func normalizeTrackers(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		if _, ok := seen[upper]; ok {
			continue
		}
		seen[upper] = struct{}{}
		out = append(out, upper)
	}
	return out
}

func orderTrackersByPriority(trackers []string) []string {
	if len(trackers) == 0 {
		return trackers
	}
	trackerPriority := trackerscatalog.TrackerPriority()
	priority := make(map[string]int, len(trackerPriority))
	for idx, value := range trackerPriority {
		priority[strings.ToUpper(value)] = idx
	}

	sort.SliceStable(trackers, func(i, j int) bool {
		left := strings.ToUpper(strings.TrimSpace(trackers[i]))
		right := strings.ToUpper(strings.TrimSpace(trackers[j]))
		leftIdx, leftOK := priority[left]
		rightIdx, rightOK := priority[right]
		if leftOK && rightOK {
			return leftIdx < rightIdx
		}
		if leftOK {
			return true
		}
		if rightOK {
			return false
		}
		return false
	})

	return trackers
}

func reorderTrackersForMetadataNeeds(trackers []string, opts api.UploadOptions) []string {
	if len(trackers) == 0 {
		return trackers
	}
	if opts.OnlyID || !opts.KeepImages {
		return trackers
	}

	nonBTN := make([]string, 0, len(trackers))
	btnCount := 0
	for _, tracker := range trackers {
		if strings.EqualFold(strings.TrimSpace(tracker), "BTN") {
			btnCount++
			continue
		}
		nonBTN = append(nonBTN, tracker)
	}
	if btnCount == 0 {
		return trackers
	}
	for idx := 0; idx < btnCount; idx++ {
		nonBTN = append(nonBTN, "BTN")
	}
	return nonBTN
}

func applyPreferredTracker(trackers []string, preferred string) []string {
	if len(trackers) == 0 {
		return trackers
	}
	preferred = strings.ToUpper(strings.TrimSpace(preferred))
	if preferred == "" {
		return trackers
	}

	preferredIndex := -1
	for idx, tracker := range trackers {
		if strings.EqualFold(strings.TrimSpace(tracker), preferred) {
			preferredIndex = idx
			break
		}
	}
	if preferredIndex <= 0 {
		return trackers
	}

	selected := trackers[preferredIndex]
	copy(trackers[1:preferredIndex+1], trackers[0:preferredIndex])
	trackers[0] = selected
	return trackers
}

func configuredTrackers(cfg config.Config) ([]string, []string) {
	configured := make([]string, 0)
	missing := make([]string, 0)
	for name, entry := range cfg.Trackers.Trackers {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		if !trackerLookupConfigured(upper, entry) {
			missing = append(missing, upper)
			continue
		}
		configured = append(configured, upper)
	}
	if len(config.ResolveBTNAPIToken(cfg)) >= minTrackerTokenLen {
		configured = append(configured, "BTN")
	}
	configured = uniqueSorted(configured)
	missing = uniqueSorted(missing)
	return configured, missing
}

func filterConfiguredTrackers(cfg config.Config, trackers []string, logger api.Logger) []string {
	if len(trackers) == 0 {
		return trackers
	}

	filtered := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		if strings.EqualFold(strings.TrimSpace(tracker), "BTN") {
			if len(config.ResolveBTNAPIToken(cfg)) >= minTrackerTokenLen {
				filtered = append(filtered, tracker)
			} else if logger != nil {
				logger.Debugf("metadata: tracker %s missing BTN api token", tracker)
			}
			continue
		}
		entry, ok := trackerConfigFor(cfg, tracker)
		if !ok {
			if logger != nil {
				logger.Debugf("metadata: tracker %s not configured", tracker)
			}
			continue
		}
		if !trackerLookupConfigured(tracker, entry) {
			if logger != nil {
				logger.Debugf("metadata: tracker %s missing lookup credentials", tracker)
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(tracker), "ANT") && strings.TrimSpace(entry.APIKey) == "" {
			if logger != nil {
				logger.Debugf("metadata: tracker %s missing api_key", tracker)
			}
			continue
		}
		filtered = append(filtered, tracker)
	}
	return filtered
}

const minTrackerTokenLen = 25

func trackerLookupConfigured(tracker string, entry config.TrackerConfig) bool {
	switch strings.ToUpper(strings.TrimSpace(tracker)) {
	case "BHD":
		return len(strings.TrimSpace(entry.APIKey)) >= minTrackerTokenLen &&
			len(strings.TrimSpace(entry.BhdRSSKey)) >= minTrackerTokenLen
	case "PTP":
		return strings.TrimSpace(entry.PTPAPIUser) != "" && strings.TrimSpace(entry.PTPAPIKey) != ""
	case "HDB":
		return strings.TrimSpace(entry.Username) != "" && strings.TrimSpace(entry.Passkey) != ""
	case "ANT":
		return strings.TrimSpace(entry.APIKey) != ""
	default:
		return strings.TrimSpace(entry.APIKey) != "" || strings.TrimSpace(entry.AnnounceURL) != ""
	}
}

func applyTrackerDataResult(record *api.TrackerMetadata, result trackerdata.Result) {
	if record == nil {
		return
	}
	record.TrackerID = metautil.FirstNonEmptyTrimmed(result.TrackerID, record.TrackerID)
	record.InfoHash = metautil.FirstNonEmptyTrimmed(record.InfoHash, result.InfoHash)
	record.TMDBID = result.TMDBID
	record.IMDBID = result.IMDBID
	record.TVDBID = result.TVDBID
	record.MALID = result.MALID
	record.Category = normalizeUnit3DCategory(result.Category)
	record.Description = strings.TrimSpace(result.Description)
	record.Filename = strings.TrimSpace(result.FileName)
	record.Matched = result.HasData()
}

func trackerConfigFor(cfg config.Config, tracker string) (config.TrackerConfig, bool) {
	if cfg.Trackers.Trackers == nil {
		return config.TrackerConfig{}, false
	}
	key := strings.TrimSpace(tracker)
	if key == "" {
		return config.TrackerConfig{}, false
	}
	if value, ok := cfg.Trackers.Trackers[key]; ok {
		return value, true
	}
	lower := strings.ToLower(key)
	upper := strings.ToUpper(key)
	if value, ok := cfg.Trackers.Trackers[lower]; ok {
		return value, true
	}
	if value, ok := cfg.Trackers.Trackers[upper]; ok {
		return value, true
	}
	for name, value := range cfg.Trackers.Trackers {
		if strings.EqualFold(name, key) {
			return value, true
		}
	}
	return config.TrackerConfig{}, false
}

func trackerIDFor(meta api.PreparedMetadata, tracker string) string {
	if len(meta.TrackerIDs) == 0 {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(tracker))
	if key == "" {
		return ""
	}
	return strings.TrimSpace(meta.TrackerIDs[key])
}

func trackerMatched(meta api.PreparedMetadata, tracker string) bool {
	if !meta.FoundTrackerMatch || len(meta.MatchedTrackers) == 0 {
		return false
	}
	target := strings.ToUpper(strings.TrimSpace(tracker))
	if target == "" {
		return false
	}
	for _, value := range meta.MatchedTrackers {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	sort.Strings(result)
	return result
}

func logPathedTrackerDetails(meta api.PreparedMetadata, logger api.Logger) {
	if logger == nil {
		return
	}
	if len(meta.TorrentComments) == 0 {
		return
	}
	logger.Tracef("metadata: pathed torrent tracker details for %s", meta.SourcePath)
	for _, match := range meta.TorrentComments {
		if len(match.TrackerURLsRaw) == 0 && len(match.TrackerURLs) == 0 && strings.TrimSpace(match.Tracker) == "" {
			continue
		}
		name := strings.TrimSpace(match.Name)
		if name == "" {
			name = strings.TrimSpace(match.Hash)
		}
		urls := redactStrings(match.TrackerURLsRaw)
		comment := redaction.RedactValue(strings.TrimSpace(match.Comment), nil)
		logger.Tracef(
			"metadata: torrent %s trackers=%v extracted_ids=%v comment=%q",
			name,
			urls,
			match.TrackerURLs,
			comment,
		)
	}
}

func logClientSearchIDs(meta api.PreparedMetadata, logger api.Logger) {
	if logger == nil {
		return
	}
	if len(meta.TrackerIDs) == 0 {
		return
	}
	keys := make([]string, 0, len(meta.TrackerIDs))
	for key := range meta.TrackerIDs {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(meta.TrackerIDs[key])
		if value == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", strings.ToUpper(key), redaction.RedactValue(value, nil)))
	}
	if len(parts) == 0 {
		return
	}
	logger.Debugf("metadata: pathed tracker ids for %s: %s", meta.SourcePath, strings.Join(parts, ", "))
}

func redactStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	redacted := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		redacted = append(redacted, redaction.RedactValue(trimmed, nil))
	}
	return redacted
}

func searchFileName(meta api.PreparedMetadata) string {
	base := strings.TrimSpace(meta.SourcePath)
	if base == "" {
		return ""
	}
	return pathutil.Base(base)
}

func trackerLookupFileName(meta api.PreparedMetadata, trackerID string, skipFilenameLookup bool) string {
	if skipFilenameLookup && strings.TrimSpace(trackerID) == "" {
		return ""
	}
	return searchFileName(meta)
}

func normalizeUnit3DCategory(category string) api.Category {
	normalized := api.NormalizeCategory(category)
	if normalized.IsValid() {
		return normalized.Canonical()
	}
	return api.CategoryUnknown
}

func hasUnit3DData(result trackerdata.Result) bool {
	if result.HasIDs() {
		return true
	}
	if strings.TrimSpace(result.Description) != "" {
		return true
	}
	if len(result.Images) > 0 {
		return true
	}
	if strings.TrimSpace(result.InfoHash) != "" {
		return true
	}
	if strings.TrimSpace(result.FileName) != "" {
		return true
	}
	if strings.TrimSpace(result.Category) != "" {
		return true
	}
	return false
}

func hasTrackerMetadataIDs(record api.TrackerMetadata) bool {
	return record.TMDBID != 0 || record.IMDBID != 0 || record.TVDBID != 0 || record.MALID != 0
}
