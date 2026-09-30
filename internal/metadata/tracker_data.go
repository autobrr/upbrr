// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	trackerscatalog "github.com/autobrr/upbrr/internal/trackers"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	defaultTrackerCooldown = 15 * time.Second
	trackerLookupWorkers   = 4
)

// collectTrackerEvidence reuses matching stored tracker IDs for fresh sources,
// looks up missing IDs while respecting tracker cooldowns, then persists
// accepted lookup records and timestamps. Explicit IDs or a
// preferred tracker force configured priority order; otherwise at most four
// lookups race and the first result with IDs wins. Lookup failures are soft,
// while cancellation and persistence failures discard the result.
func (s *Service) collectTrackerEvidence(ctx context.Context, meta preparationstate.State) (preparationstate.State, error) {
	select {
	case <-ctx.Done():
		return preparationstate.State{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	if s.repo == nil {
		return preparationstate.State{}, internalerrors.ErrInvalidInput
	}
	if strings.TrimSpace(meta.SourcePath) == "" {
		return preparationstate.State{}, internalerrors.ErrInvalidInput
	}
	candidates := normalizeTrackers(resolveTrackerCandidates(meta))
	if len(candidates) == 0 {
		return meta, nil
	}

	trackers := candidates
	if s.logger != nil {
		configured, missing := configuredTrackers(s.cfg, s.registry)
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
	trackers = filterConfiguredTrackers(s.cfg, trackers, s.logger, s.registry)
	trackers = orderTrackersByPriority(trackers, s.registry)
	trackers = reorderTrackersForMetadataNeeds(trackers, api.UploadOptions{OnlyID: meta.Policy.OnlyID, KeepImages: meta.Policy.KeepImages}, s.registry)
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
	var cached api.TrackerMetadata
	cachedIndex := -1
	refreshCachedAssets := false
	if meta.StoredDataFresh {
		stored, err := s.repo.ListTrackerMetadataByPath(ctx, meta.SourcePath)
		if err != nil {
			return preparationstate.State{}, fmt.Errorf("metadata: load stored tracker metadata: %w", err)
		}
		if !meta.Policy.OnlyID && meta.Policy.KeepImages {
			for index := range stored {
				record := &stored[index]
				if len(record.ImageURLs) == 0 || !trackerscatalog.LegacyImageAssetsNeedProvenance(s.registry, record.Tracker) {
					continue
				}
				_, provenanceErr := s.repo.GetTrackerTimestamp(ctx, trackerscatalog.TrackerAssetProvenanceKey(*record))
				if provenanceErr == nil {
					continue
				}
				if !errors.Is(provenanceErr, internalerrors.ErrNotFound) {
					return preparationstate.State{}, fmt.Errorf("metadata: load tracker asset provenance: %w", provenanceErr)
				}
				if s.logger != nil {
					s.logger.Tracef("metadata: clearing legacy tracker image provenance tracker=%s count=%d", record.Tracker, len(record.ImageURLs))
				}
				record.ImageURLs = nil
				record.ImagePreviews = nil
				if err := s.repo.SaveTrackerMetadata(ctx, *record); err != nil {
					return preparationstate.State{}, fmt.Errorf("metadata: clear unverified tracker image urls: %w", err)
				}
			}
		}
		for index, tracker := range trackers {
			for _, record := range stored {
				if !strings.EqualFold(record.Tracker, tracker) || !hasTrackerMetadataIDs(record) {
					continue
				}
				currentID := trackerIDFor(meta, tracker)
				if currentID != "" {
					if currentID != strings.TrimSpace(record.TrackerID) {
						continue
					}
				} else if meta.InfoHash == "" || record.InfoHash == "" {
					continue
				}
				if meta.InfoHash != "" && record.InfoHash != "" && !strings.EqualFold(meta.InfoHash, record.InfoHash) {
					continue
				}
				cached = record
				cachedIndex = index
				break
			}
			if cachedIndex >= 0 {
				break
			}
		}
		if cachedIndex >= 0 {
			needsDescription := !meta.Policy.OnlyID && strings.TrimSpace(cached.Description) == ""
			needsImages := meta.Policy.KeepImages && (len(cached.ImageURLs) == 0 || slices.ContainsFunc(cached.ImageURLs, func(value string) bool {
				return strings.TrimSpace(value) == ""
			}))
			if needsImages {
				_, deletionErr := s.repo.GetTrackerTimestamp(ctx, trackerscatalog.TrackerImageDeletionKey(cached))
				if deletionErr == nil {
					needsImages = false
				} else if !errors.Is(deletionErr, internalerrors.ErrNotFound) {
					return preparationstate.State{}, fmt.Errorf("metadata: load tracker image deletion: %w", deletionErr)
				}
			}
			needsAssets := needsDescription || needsImages
			refreshCachedAssets = needsAssets
			strictPriority := shouldUseStrictPriorityLookup(meta, trackers, s.cfg.Trackers.PreferredTracker)
			if !needsAssets && (cachedIndex == 0 || !strictPriority) {
				meta.TrackerData = append(meta.TrackerData, cached)
				if s.logger != nil {
					s.logger.Debugf("metadata: reusing matching stored tracker IDs tracker=%s for %s", cached.Tracker, meta.SourcePath)
				}
				return meta, nil
			}
			if needsAssets && s.logger != nil {
				s.logger.Debugf(
					"metadata: refreshing stored tracker assets tracker=%s description_missing=%t images_missing=%t",
					cached.Tracker, needsDescription, needsImages,
				)
			}
			if strictPriority {
				limit := cachedIndex
				if needsAssets {
					limit++
				}
				trackers = trackers[:limit]
			} else {
				trackers = trackers[cachedIndex : cachedIndex+1]
			}
		} else if s.logger != nil {
			s.logger.Debugf("metadata: stored source snapshot has no matching tracker IDs; checking trackers for %s", meta.SourcePath)
		}
	}

	now := time.Now().UTC()
	meta.TrackerData = append([]api.TrackerMetadata{}, meta.TrackerData...)
	eligible := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		select {
		case <-ctx.Done():
			return preparationstate.State{}, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}
		assetRefresh := refreshCachedAssets && strings.EqualFold(tracker, cached.Tracker)
		timestampKey := tracker
		if assetRefresh {
			timestampKey = trackerAssetTimestampKey(tracker)
		}
		if s.isTrackerCoolingDown(ctx, tracker, timestampKey, now) {
			continue
		}
		if assetRefresh && s.logger != nil {
			s.logger.Debugf("metadata: tracker asset refresh eligible tracker=%s", tracker)
		}
		eligible = append(eligible, tracker)
	}
	if len(eligible) == 0 {
		if cachedIndex >= 0 {
			meta.TrackerData = append(meta.TrackerData, cached)
		}
		return meta, nil
	}
	if cachedIndex >= 0 {
		result, err := s.enrichTrackerDataPriority(ctx, meta, eligible, now, &cached)
		if err != nil {
			return preparationstate.State{}, err
		}
		if slices.ContainsFunc(result.TrackerData, hasTrackerMetadataIDs) {
			return result, nil
		}
		result.TrackerData = append(result.TrackerData, cached)
		return result, nil
	}

	if !shouldUseStrictPriorityLookup(meta, eligible, s.cfg.Trackers.PreferredTracker) {
		return s.enrichTrackerDataConcurrent(ctx, meta, eligible, now)
	}

	return s.enrichTrackerDataPriority(ctx, meta, eligible, now, nil)
}

func shouldUseStrictPriorityLookup(meta preparationstate.State, eligible []string, preferred string) bool {
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

// enrichTrackerDataPriority checks eligible trackers in order, retains assets
// only from the first tracker with a description or images, and stops once IDs resolve.
// Successful records and their freshness timestamps are persisted.
func (s *Service) enrichTrackerDataPriority(
	ctx context.Context,
	meta preparationstate.State,
	eligible []string,
	now time.Time,
	cached *api.TrackerMetadata,
) (preparationstate.State, error) {
	assetSourceTracker := ""
	for _, tracker := range eligible {
		select {
		case <-ctx.Done():
			return preparationstate.State{}, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		lookupMeta := meta
		if cached != nil && strings.EqualFold(cached.Tracker, tracker) && trackerIDFor(meta, tracker) == "" && cached.TrackerID != "" {
			lookupMeta.TrackerIDs = cloneTrackerIDs(meta.TrackerIDs)
			if lookupMeta.TrackerIDs == nil {
				lookupMeta.TrackerIDs = make(map[string]string)
			}
			lookupMeta.TrackerIDs[strings.ToLower(tracker)] = cached.TrackerID
		}
		record, persistable, hasIDs, err := s.lookupTrackerData(ctx, lookupMeta, tracker, now)
		freshAssets := trackerRecordHasDescriptionAssets(record)
		if cached != nil && strings.EqualFold(cached.Tracker, tracker) && ctx.Err() == nil {
			if stampErr := s.repo.SaveTrackerTimestamp(ctx, db.TrackerTimestamp{Tracker: trackerAssetTimestampKey(tracker), UpdatedAt: now}); stampErr != nil {
				return preparationstate.State{}, fmt.Errorf("metadata: save tracker asset timestamp: %w", stampErr)
			}
		}
		if err != nil {
			if s.logger != nil {
				s.logger.Warnf("metadata: tracker lookup failed tracker=%s: %s", tracker, redaction.RedactValue(err.Error(), nil))
			}
			continue
		}
		if !persistable {
			continue
		}
		if cached != nil && strings.EqualFold(cached.Tracker, tracker) {
			freshDescription := strings.TrimSpace(record.Description) != ""
			if !hasIDs && !trackerRecordHasDescriptionAssets(record) {
				continue
			}
			if record.TrackerID == "" {
				record.TrackerID = cached.TrackerID
			}
			if record.InfoHash == "" {
				record.InfoHash = cached.InfoHash
			}
			record.Matched = record.Matched || cached.Matched
			record.TMDBID = firstNonZero(record.TMDBID, cached.TMDBID)
			record.IMDBID = firstNonZero(record.IMDBID, cached.IMDBID)
			record.TVDBID = firstNonZero(record.TVDBID, cached.TVDBID)
			record.MALID = firstNonZero(record.MALID, cached.MALID)
			if record.Category == api.CategoryUnknown {
				record.Category = cached.Category
			}
			if record.Description == "" {
				record.Description = cached.Description
			}
			if !freshDescription {
				previews := make(map[string]string, len(cached.ImagePreviews)+len(record.ImagePreviews))
				maps.Copy(previews, cached.ImagePreviews)
				maps.Copy(previews, record.ImagePreviews)
				record.ImagePreviews = previews
			}
			if len(record.ImageURLs) == 0 && !freshDescription {
				record.ImageURLs = append([]string(nil), cached.ImageURLs...)
			}
			if record.Filename == "" {
				record.Filename = cached.Filename
			}
			hasIDs = hasTrackerMetadataIDs(record)
		}

		if trackerRecordHasDescriptionAssets(record) {
			if assetSourceTracker == "" {
				assetSourceTracker = strings.ToUpper(strings.TrimSpace(tracker))
				if s.logger != nil {
					s.logger.Debugf("metadata: description/image source tracker selected: %s", assetSourceTracker)
				}
			} else if !strings.EqualFold(assetSourceTracker, tracker) {
				if s.logger != nil {
					s.logger.Debugf(
						"metadata: ignoring description/images from %s (source=%s)",
						strings.ToUpper(strings.TrimSpace(tracker)),
						assetSourceTracker,
					)
				}
				record.Description = ""
				record.ImageURLs = nil
				record.ImagePreviews = nil
			}
		}

		if err := s.repo.SaveTrackerMetadata(ctx, record); err != nil {
			return preparationstate.State{}, fmt.Errorf("metadata: save tracker metadata: %w", err)
		}
		if err := s.repo.SaveTrackerTimestamp(ctx, db.TrackerTimestamp{Tracker: tracker, UpdatedAt: now}); err != nil {
			return preparationstate.State{}, fmt.Errorf("metadata: save tracker timestamp: %w", err)
		}
		if !meta.Policy.OnlyID && meta.Policy.KeepImages && freshAssets && trackerRecordHasDescriptionAssets(record) &&
			trackerscatalog.LegacyImageAssetsNeedProvenance(s.registry, tracker) {
			if err := s.repo.SaveTrackerTimestamp(ctx, db.TrackerTimestamp{
				Tracker: trackerscatalog.TrackerAssetProvenanceKey(record), UpdatedAt: now,
			}); err != nil {
				return preparationstate.State{}, fmt.Errorf("metadata: save tracker asset provenance: %w", err)
			}
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
	meta preparationstate.State,
	eligible []string,
	now time.Time,
) (preparationstate.State, error) {
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
				record, persistable, hasIDs, err := s.lookupTrackerData(lookupCtx, lookupMeta, tracker, now)
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
					s.logger.Debugf(
						"metadata: ignoring description/images from %s (source=%s)",
						strings.ToUpper(strings.TrimSpace(outcome.tracker)),
						assetSourceTracker,
					)
				}
				outcome.record.Description = ""
				outcome.record.ImageURLs = nil
				outcome.record.ImagePreviews = nil
			}
		}

		if err := s.repo.SaveTrackerMetadata(ctx, outcome.record); err != nil {
			workers.Wait()
			return preparationstate.State{}, fmt.Errorf("metadata: save tracker metadata: %w", err)
		}
		if err := s.repo.SaveTrackerTimestamp(ctx, db.TrackerTimestamp{Tracker: outcome.tracker, UpdatedAt: now}); err != nil {
			workers.Wait()
			return preparationstate.State{}, fmt.Errorf("metadata: save tracker timestamp: %w", err)
		}
		if !meta.Policy.OnlyID && meta.Policy.KeepImages && trackerRecordHasDescriptionAssets(outcome.record) &&
			trackerscatalog.LegacyImageAssetsNeedProvenance(s.registry, outcome.tracker) {
			if err := s.repo.SaveTrackerTimestamp(ctx, db.TrackerTimestamp{
				Tracker: trackerscatalog.TrackerAssetProvenanceKey(outcome.record), UpdatedAt: now,
			}); err != nil {
				workers.Wait()
				return preparationstate.State{}, fmt.Errorf("metadata: save tracker asset provenance: %w", err)
			}
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
	meta preparationstate.State,
	tracker string,
	now time.Time,
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
		trackerLookupSubject(meta),
		trackerLookupFileName(meta, s.cfg.Metadata.SkipTrackerFilenameLookup),
		meta.Policy.OnlyID,
		meta.Policy.KeepImages,
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
		downloadedImages := s.persistTrackerArtifacts(
			ctx,
			meta,
			tracker,
			trackerdata.Result{Description: result.Description, Validated: result.Images},
			meta.Policy.KeepImages,
		)
		record.ImageURLs, record.ImagePreviews = trackerImageURLsFromResult(result, downloadedImages, meta.Policy.KeepImages)
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

func trackerLookupSubject(meta preparationstate.State) api.UploadSubject {
	identity := meta.Identity
	if identity.Category == "" || identity.Category == api.CanonicalCategoryUnknown {
		identity.Category, _ = api.NormalizeCanonicalCategory(meta.MediaInfoCategory)
	}
	return api.UploadSubject{
		SourcePath:            meta.SourcePath,
		Paths:                 append([]string(nil), meta.Paths...),
		DiscType:              meta.DiscType,
		VideoPath:             meta.VideoPath,
		FileList:              append([]string(nil), meta.FileList...),
		SourceSize:            meta.SourceSize,
		MediaInfoJSONPath:     meta.MediaInfoJSONPath,
		MediaInfoTextPath:     meta.MediaInfoTextPath,
		DVDVOBMediaInfoText:   meta.DVDVOBMediaInfoText,
		Scene:                 meta.Scene,
		SceneName:             meta.SceneName,
		SceneNFOPath:          meta.SceneNFOPath,
		SceneRenamed:          meta.SceneRenamed,
		SceneRenamedReason:    meta.SceneRenamedReason,
		Trackers:              append([]string(nil), meta.EvidenceTrackers...),
		MatchedTrackers:       append([]string(nil), meta.MatchedEvidenceTrackers...),
		Tag:                   meta.Tag,
		Release:               meta.Release,
		DescriptionTemplate:   meta.DescriptionTemplate,
		PersonalRelease:       meta.PersonalRelease,
		InfoHash:              meta.InfoHash,
		TrackerIDs:            cloneTrackerIDs(meta.TrackerIDs),
		TrackerData:           append([]api.TrackerMetadata(nil), meta.TrackerData...),
		ArrReleaseGroup:       meta.ArrReleaseGroup,
		ReleaseNameOverrides:  meta.ReleaseNameOverrides,
		SeasonInt:             meta.SeasonInt,
		EpisodeInt:            meta.EpisodeInt,
		SeasonStr:             meta.SeasonStr,
		EpisodeStr:            meta.EpisodeStr,
		TVDBAiredDate:         meta.TVDBAiredDate,
		TVDBAirsTime:          meta.TVDBAirsTime,
		TVDBAirsTimezone:      meta.TVDBAirsTimezone,
		TVPack:                meta.TVPack,
		DailyEpisodeDate:      meta.DailyEpisodeDate,
		Anime:                 meta.Anime,
		EpisodeTitle:          meta.EpisodeTitle,
		EpisodeOverview:       meta.EpisodeOverview,
		SelectedBDMVPlaylists: append([]api.PlaylistInfo(nil), meta.SelectedBDMVPlaylists...),
		Identity:              identity,
		ProviderMetadata:      meta.ProviderMetadata,
		AudioLanguages:        append([]string(nil), meta.AudioLanguages...),
		SubtitleLanguages:     append([]string(nil), meta.SubtitleLanguages...),
		Container:             meta.Container,
		Audio:                 meta.Audio,
		Channels:              meta.Channels,
		HasCommentary:         meta.HasCommentary,
		Is3D:                  meta.Is3D,
		Source:                meta.Source,
		Type:                  meta.Type,
		UHD:                   meta.UHD,
		HDR:                   meta.HDR,
		Distributor:           meta.Distributor,
		Region:                meta.Region,
		VideoCodec:            meta.VideoCodec,
		VideoEncode:           meta.VideoEncode,
		HasEncodeSettings:     meta.HasEncodeSettings,
		BitDepth:              meta.BitDepth,
		Edition:               meta.Edition,
		Repack:                meta.Repack,
		WebDV:                 meta.WebDV,
		Assessments:           meta.ReleaseAssessments(),
		StreamOptimized:       meta.StreamOptimized,
		Service:               meta.Service,
		ServiceLongName:       meta.ServiceLongName,
		Filename:              meta.Filename,
		ReleaseName:           meta.ReleaseName,
		ReleaseNameNoTag:      meta.ReleaseNameNoTag,
		ReleaseNameClean:      meta.ReleaseNameClean,
		GeneratedName:         meta.GeneratedName.Clone(),
	}
}

func trackerImageURLsFromResult(result trackerdata.Result, downloadedImages []string, keepImages bool) ([]string, map[string]string) {
	previews := make(map[string]string)
	for _, image := range result.Images {
		full := strings.TrimSpace(image.RawURL)
		preview := strings.TrimSpace(image.ImgURL)
		parsed, err := url.Parse(preview)
		if full != "" && preview != "" && preview != full && err == nil &&
			(parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" {
			previews[full] = preview
		}
	}
	if !keepImages || len(downloadedImages) == 0 {
		return nil, previews
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
		return nil, previews
	}
	return urls, previews
}

func trackerRecordHasPathedData(record api.TrackerMetadata) bool {
	return record.TrackerID != "" || record.TorrentURL != "" || record.InfoHash != "" || record.Matched
}

func trackerRecordHasDescriptionAssets(record api.TrackerMetadata) bool {
	return strings.TrimSpace(record.Description) != "" || len(record.ImageURLs) > 0
}

func trackerAssetTimestampKey(tracker string) string {
	return tracker + ":assets"
}

func (s *Service) isTrackerCoolingDown(ctx context.Context, tracker string, timestampKey string, now time.Time) bool {
	last, err := s.repo.GetTrackerTimestamp(ctx, timestampKey)
	if err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			return false
		}
		if s.logger != nil {
			s.logger.Warnf("metadata: tracker timestamp lookup failed for %s: %v", tracker, err)
		}
		return false
	}
	cooldown := trackerCooldown(s.registry, tracker)
	if cooldown <= 0 {
		return false
	}
	if now.Sub(last) < cooldown {
		if s.logger != nil {
			s.logger.Debugf("metadata: tracker %s cooldown active timestamp_key=%s", tracker, timestampKey)
		}
		return true
	}
	return false
}

func trackerCooldown(registry *trackerscatalog.Registry, tracker string) time.Duration {
	if policy, ok := registry.LookupDataPolicy(tracker); ok {
		return policy.Cooldown
	}
	return defaultTrackerCooldown
}

// resolveTrackerCandidates orders trackers with current matched evidence first,
// then appends remaining tracker-ID keys deterministically.
func resolveTrackerCandidates(meta preparationstate.State) []string {
	if len(meta.TrackerIDs) > 0 {
		result := make([]string, 0, len(meta.TrackerIDs))
		seen := make(map[string]struct{}, len(meta.TrackerIDs))
		appendKnown := func(values []string) {
			for _, value := range values {
				key := strings.ToLower(strings.TrimSpace(value))
				if _, exists := meta.TrackerIDs[key]; !exists {
					continue
				}
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				result = append(result, value)
			}
		}
		appendKnown(meta.MatchedEvidenceTrackers)
		appendKnown(meta.EvidenceTrackers)
		remaining := make([]string, 0, len(meta.TrackerIDs)-len(result))
		for key := range meta.TrackerIDs {
			if _, exists := seen[key]; !exists {
				remaining = append(remaining, key)
			}
		}
		sort.Strings(remaining)
		result = append(result, remaining...)
		return result
	}
	if len(meta.MatchedEvidenceTrackers) > 0 {
		return meta.MatchedEvidenceTrackers
	}
	if len(meta.EvidenceTrackers) > 0 {
		return meta.EvidenceTrackers
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

func orderTrackersByPriority(trackers []string, registry *trackerscatalog.Registry) []string {
	if len(trackers) == 0 {
		return trackers
	}
	trackerPriority := registry.Priority()
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

func reorderTrackersForMetadataNeeds(trackerNames []string, opts api.UploadOptions, registry *trackerscatalog.Registry) []string {
	if len(trackerNames) == 0 {
		return trackerNames
	}
	if opts.OnlyID || !opts.KeepImages {
		return trackerNames
	}

	preferred := make([]string, 0, len(trackerNames))
	deferred := make([]string, 0, len(trackerNames))
	for _, tracker := range trackerNames {
		policy, ok := registry.LookupDataPolicy(tracker)
		if ok && policy.DeferWhenCollectingImages {
			deferred = append(deferred, tracker)
			continue
		}
		preferred = append(preferred, tracker)
	}
	if len(deferred) == 0 {
		return trackerNames
	}
	return append(preferred, deferred...)
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

func configuredTrackers(cfg config.Config, registry *trackerscatalog.Registry) ([]string, []string) {
	configured := make([]string, 0)
	missing := make([]string, 0)
	for name, entry := range cfg.Trackers.Trackers {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		ready, owned := registry.DataLookupConfigured(upper, cfg)
		if !owned {
			ready = customTrackerLookupConfigured(entry)
		}
		if !ready {
			missing = append(missing, upper)
			continue
		}
		configured = append(configured, upper)
	}
	for _, name := range registry.Names() {
		if ready, owned := registry.DataLookupConfigured(name, cfg); owned && ready {
			configured = append(configured, name)
		}
	}
	configured = uniqueSorted(configured)
	missing = uniqueSorted(missing)
	return configured, missing
}

func filterConfiguredTrackers(cfg config.Config, trackers []string, logger api.Logger, registry *trackerscatalog.Registry) []string {
	if len(trackers) == 0 {
		return trackers
	}

	filtered := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		configured, owned := registry.DataLookupConfigured(tracker, cfg)
		if owned {
			if configured {
				filtered = append(filtered, tracker)
			} else if logger != nil {
				logger.Debugf("metadata: tracker %s missing lookup credentials", tracker)
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
		configured = customTrackerLookupConfigured(entry)
		if !configured {
			if logger != nil {
				logger.Debugf("metadata: tracker %s missing lookup credentials", tracker)
			}
			continue
		}
		filtered = append(filtered, tracker)
	}
	return filtered
}

func customTrackerLookupConfigured(entry config.TrackerConfig) bool {
	return strings.TrimSpace(entry.APIKey) != "" ||
		strings.TrimSpace(entry.AnnounceURL) != "" ||
		(strings.TrimSpace(entry.Username) != "" && strings.TrimSpace(entry.Passkey) != "") ||
		(strings.TrimSpace(entry.PTPAPIUser) != "" && strings.TrimSpace(entry.PTPAPIKey) != "")
}

func applyTrackerDataResult(record *api.TrackerMetadata, result trackerdata.Result) {
	if record == nil {
		return
	}
	record.TrackerID = metautil.FirstNonEmptyTrimmed(result.TrackerID, record.TrackerID)
	record.TorrentURL = strings.TrimSpace(result.TorrentURL)
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

func trackerIDFor(meta preparationstate.State, tracker string) string {
	if len(meta.TrackerIDs) == 0 {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(tracker))
	if key == "" {
		return ""
	}
	return strings.TrimSpace(meta.TrackerIDs[key])
}

func trackerMatched(meta preparationstate.State, tracker string) bool {
	if !meta.FoundTrackerMatch || len(meta.MatchedEvidenceTrackers) == 0 {
		return false
	}
	target := strings.ToUpper(strings.TrimSpace(tracker))
	if target == "" {
		return false
	}
	for _, value := range meta.MatchedEvidenceTrackers {
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

func logPathedTrackerDetails(meta preparationstate.State, logger api.Logger) {
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

func logClientSearchIDs(meta preparationstate.State, logger api.Logger) {
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

func searchFileName(meta preparationstate.State) string {
	base := strings.TrimSpace(meta.SourcePath)
	if base == "" {
		return ""
	}
	return pathutil.Base(base)
}

func trackerLookupFileName(meta preparationstate.State, skipFilenameLookup bool) string {
	if skipFilenameLookup {
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

func hasTrackerMetadataIDs(record api.TrackerMetadata) bool {
	return record.TMDBID != 0 || record.IMDBID != 0 || record.TVDBID != 0 || record.MALID != 0
}
