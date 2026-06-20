// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

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
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/description"
	"github.com/autobrr/upbrr/pkg/api"
)

type Service struct {
	cfg      config.Config
	logger   api.Logger
	repo     db.MetadataRepository
	images   api.ImageHostingService
	banned   *BannedGroupChecker
	registry *Registry
}

const (
	defaultMaxConcurrentTrackerUploads = 4
	uploadRecordFinalizationTimeout    = 5 * time.Second
)

type trackerUploadResult struct {
	tracker        string
	summary        api.UploadSummary
	err            error
	notImplemented bool
}

type imageHostPreflight map[string]descriptionImageHostResolution

func NewService(cfg config.Config, logger api.Logger, repo db.MetadataRepository) *Service {
	return NewServiceWithRegistryAndImages(cfg, logger, repo, nil, nil)
}

func NewServiceWithRegistry(cfg config.Config, logger api.Logger, repo db.MetadataRepository, registry *Registry) *Service {
	return NewServiceWithRegistryAndImages(cfg, logger, repo, registry, nil)
}

func NewServiceWithRegistryAndImages(cfg config.Config, logger api.Logger, repo db.MetadataRepository, registry *Registry, images api.ImageHostingService) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Service{cfg: cfg, logger: logger, repo: repo, images: images, banned: NewBannedGroupChecker(cfg.MainSettings.DBPath), registry: registry}
}

// Upload submits prepared metadata to the resolved tracker set.
// The returned summary includes tracker uploads that completed before a later
// failure or cancellation, and pending upload records are finalized with a
// cleanup context when the caller's context has already been canceled.
// Successful tracker results are treated as terminal for finalization even when
// persisting that terminal status logs a warning.
func (s *Service) Upload(ctx context.Context, meta api.PreparedMetadata) (api.UploadSummary, error) {
	select {
	case <-ctx.Done():
		return api.UploadSummary{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	trackers := resolveTrackers(s.cfg, meta.Trackers, meta.TrackersRemove)
	trackers = filterKnownTrackers(trackers, s.logger)
	trackers = filterTrackersByRuleFailures(trackers, meta.TrackerRuleFailures, meta.IgnoreTrackerRuleFailures, s.logger)
	trackers = filterTrackersByBlocks(trackers, meta.BlockedTrackers, s.logger)
	s.logger.Debugf("trackers: resolved %d trackers", len(trackers))
	if len(trackers) == 0 {
		s.logger.Infof("trackers: no trackers configured, skipping upload")
		return api.UploadSummary{Uploaded: 0}, nil
	}

	if meta.Tag != "" {
		group := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-")))
		if strings.Contains(group, "taoe") {
			group = "taoe"
		}
		for _, tracker := range trackers {
			select {
			case <-ctx.Done():
				return api.UploadSummary{}, fmt.Errorf("context canceled: %w", ctx.Err())
			default:
			}
			banned, err := s.banned.IsBanned(tracker, group)
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %s banned groups: %w", tracker, err)
			}
			if banned {
				return api.UploadSummary{}, fmt.Errorf("trackers: %s group %s: %w", tracker, group, internalerrors.ErrBannedGroup)
			}
		}
	}

	if s.registry == nil {
		if s.repo != nil {
			for _, tracker := range trackers {
				s.logger.Debugf("trackers: creating pending record for %s", tracker)
				status := "pending"
				if IsInternalGroup(s.cfg, tracker, meta) {
					status = "pending-internal"
					if s.logger != nil {
						s.logger.Infof("trackers: %s marked internal for %s", tracker, meta.Tag)
					}
				}
				err := s.repo.CreateUploadRecord(ctx, db.UploadRecord{
					Tracker:    tracker,
					Status:     status,
					CreatedAt:  time.Now().UTC(),
					SourcePath: meta.SourcePath,
				})
				if err != nil {
					return api.UploadSummary{}, fmt.Errorf("trackers: record %s: %w", tracker, err)
				}
				if err := s.repo.UpdateLatestUploadRecordStatus(ctx, meta.SourcePath, tracker, "failed"); err != nil {
					s.logger.Warnf("trackers: status update %s (failed): %v", tracker, err)
				}
			}
		}
		return api.UploadSummary{}, fmt.Errorf("trackers: %s: %w", strings.Join(trackers, ","), internalerrors.ErrNotImplemented)
	}

	var finalizePending func(context.Context, string) []string
	if s.repo != nil {
		type recordStatusState struct {
			status    string
			persisted bool
		}

		recordStatuses := make(map[string]recordStatusState, len(trackers))
		var recordMu sync.Mutex
		updateRecordStatus := func(updateCtx context.Context, tracker string, status string) error {
			trimmedTracker := strings.TrimSpace(tracker)
			trimmedStatus := strings.TrimSpace(status)
			if trimmedTracker == "" || trimmedStatus == "" {
				return nil
			}

			recordMu.Lock()
			current, ok := recordStatuses[trimmedTracker]
			if ok && current.status == trimmedStatus && current.persisted {
				recordMu.Unlock()
				return nil
			}
			recordMu.Unlock()

			if err := s.repo.UpdateLatestUploadRecordStatus(updateCtx, meta.SourcePath, trimmedTracker, trimmedStatus); err != nil {
				s.logger.Warnf("trackers: status update %s (%s): %v", trimmedTracker, trimmedStatus, err)
				recordMu.Lock()
				recordStatuses[trimmedTracker] = recordStatusState{status: trimmedStatus}
				recordMu.Unlock()
				return fmt.Errorf("trackers: status update %s (%s): %w", trimmedTracker, trimmedStatus, err)
			}
			recordMu.Lock()
			recordStatuses[trimmedTracker] = recordStatusState{status: trimmedStatus, persisted: true}
			recordMu.Unlock()
			return nil
		}
		finalizePending = func(updateCtx context.Context, status string) []string {
			recordMu.Lock()
			targetStatuses := make(map[string]string, len(recordStatuses))
			for tracker, current := range recordStatuses {
				switch {
				case current.status == "pending" || current.status == "pending-internal":
					targetStatuses[tracker] = status
				case !current.persisted:
					targetStatuses[tracker] = current.status
				}
			}
			recordMu.Unlock()

			failures := make([]string, 0)
			for tracker, targetStatus := range targetStatuses {
				if err := updateRecordStatus(updateCtx, tracker, targetStatus); err != nil {
					failures = append(failures, fmt.Sprintf("%s (%s): %v", tracker, targetStatus, err))
				}
			}
			return failures
		}

		for _, tracker := range trackers {
			select {
			case <-ctx.Done():
				cleanupCtx, cleanupCancel := uploadRecordCleanupContext(ctx)
				finalizePending(cleanupCtx, "canceled")
				cleanupCancel()
				return api.UploadSummary{}, fmt.Errorf("context canceled: %w", ctx.Err())
			default:
			}
			s.logger.Debugf("trackers: creating pending record for %s", tracker)
			status := "pending"
			if IsInternalGroup(s.cfg, tracker, meta) {
				status = "pending-internal"
				if s.logger != nil {
					s.logger.Infof("trackers: %s marked internal for %s", tracker, meta.Tag)
				}
			}
			err := s.repo.CreateUploadRecord(ctx, db.UploadRecord{
				Tracker:    tracker,
				Status:     status,
				CreatedAt:  time.Now().UTC(),
				SourcePath: meta.SourcePath,
			})
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: record %s: %w", tracker, err)
			}
			recordMu.Lock()
			recordStatuses[strings.TrimSpace(tracker)] = recordStatusState{status: status, persisted: true}
			recordMu.Unlock()
		}

		preflight := s.preflightDescriptionImageHosts(ctx, meta, trackers)
		results, err := s.uploadTrackersConcurrently(ctx, meta, trackers, preflight)
		statusCtx, statusCancel := uploadRecordCleanupContext(ctx)
		defer statusCancel()

		summary := api.UploadSummary{}
		notImplemented := make([]string, 0)
		failedTrackers := make([]string, 0)
		failedMessages := make([]string, 0)
		retryStatusUpdates := false

		for _, result := range results {
			if result.notImplemented {
				notImplemented = append(notImplemented, result.tracker)
				if updateRecordStatus(statusCtx, result.tracker, "failed") != nil {
					retryStatusUpdates = true
				}
				continue
			}
			if result.err != nil {
				if err != nil && isContextCancellation(result.err) {
					continue
				}
				failedTrackers = append(failedTrackers, result.tracker)
				failedMessages = append(failedMessages, fmt.Sprintf("%s: %v", result.tracker, result.err))
				if updateRecordStatus(statusCtx, result.tracker, "failed") != nil {
					retryStatusUpdates = true
				}
				continue
			}

			if updateRecordStatus(statusCtx, result.tracker, "uploaded") != nil {
				retryStatusUpdates = true
			}
			summary.Uploaded += result.summary.Uploaded
			if len(result.summary.UploadedTorrents) > 0 {
				summary.UploadedTorrents = append(summary.UploadedTorrents, result.summary.UploadedTorrents...)
			}
		}

		statusFailures := make([]string, 0)
		if err != nil {
			statusFailures = append(statusFailures, finalizePending(statusCtx, "canceled")...)
		} else if retryStatusUpdates {
			statusFailures = append(statusFailures, finalizePending(statusCtx, "failed")...)
		}
		if len(failedMessages) > 0 {
			s.logger.Warnf("trackers: upload failures: %s", strings.Join(failedMessages, "; "))
		}
		if len(notImplemented) > 0 {
			s.logger.Warnf("trackers: not implemented: %s", strings.Join(notImplemented, ","))
		}

		if summary.Uploaded > 0 {
			if err != nil {
				return summary, err
			}
			if len(failedTrackers) > 0 {
				return summary, fmt.Errorf("trackers: %s", strings.Join(failedMessages, "; "))
			}
			if len(statusFailures) > 0 {
				return summary, fmt.Errorf("trackers: status updates: %s", strings.Join(statusFailures, "; "))
			}
			return summary, err
		}
		if err != nil {
			return summary, err
		}
		if len(failedTrackers) > 0 {
			return summary, fmt.Errorf("trackers: %s", strings.Join(failedMessages, "; "))
		}
		if len(notImplemented) > 0 {
			return summary, fmt.Errorf("trackers: %s: %w", strings.Join(notImplemented, ","), internalerrors.ErrNotImplemented)
		}
		if len(statusFailures) > 0 {
			return summary, fmt.Errorf("trackers: status updates: %s", strings.Join(statusFailures, "; "))
		}

		return summary, nil
	}

	preflight := s.preflightDescriptionImageHosts(ctx, meta, trackers)
	results, err := s.uploadTrackersConcurrently(ctx, meta, trackers, preflight)

	summary := api.UploadSummary{}
	notImplemented := make([]string, 0)
	failedTrackers := make([]string, 0)
	failedMessages := make([]string, 0)
	for _, result := range results {
		if result.notImplemented {
			notImplemented = append(notImplemented, result.tracker)
			continue
		}
		if result.err != nil {
			if err != nil && isContextCancellation(result.err) {
				continue
			}
			failedTrackers = append(failedTrackers, result.tracker)
			failedMessages = append(failedMessages, fmt.Sprintf("%s: %v", result.tracker, result.err))
			continue
		}
		summary.Uploaded += result.summary.Uploaded
		if len(result.summary.UploadedTorrents) > 0 {
			summary.UploadedTorrents = append(summary.UploadedTorrents, result.summary.UploadedTorrents...)
		}
	}

	if len(failedMessages) > 0 {
		s.logger.Warnf("trackers: upload failures: %s", strings.Join(failedMessages, "; "))
	}
	if len(notImplemented) > 0 {
		s.logger.Warnf("trackers: not implemented: %s", strings.Join(notImplemented, ","))
	}

	if summary.Uploaded > 0 {
		if err != nil {
			return summary, err
		}
		if len(failedTrackers) > 0 {
			return summary, fmt.Errorf("trackers: %s", strings.Join(failedMessages, "; "))
		}
		return summary, err
	}
	if err != nil {
		return summary, err
	}
	if len(failedTrackers) > 0 {
		return summary, fmt.Errorf("trackers: %s", strings.Join(failedMessages, "; "))
	}
	if len(notImplemented) > 0 {
		return summary, fmt.Errorf("trackers: %s: %w", strings.Join(notImplemented, ","), internalerrors.ErrNotImplemented)
	}

	return summary, nil
}

func (s *Service) uploadTrackersConcurrently(ctx context.Context, meta api.PreparedMetadata, trackers []string, preflight imageHostPreflight) ([]trackerUploadResult, error) {
	workerCount := s.maxConcurrentTrackerUploads(len(trackers))
	results := make([]trackerUploadResult, len(trackers))
	if workerCount <= 0 {
		return results, nil
	}

	trackerConfigs := make([]config.TrackerConfig, len(trackers))
	trackerMetas := make([]api.PreparedMetadata, len(trackers))
	prepErrors := make([]error, len(trackers))
	for idx, tracker := range trackers {
		trackerCfg := trackerConfigFor(s.cfg, tracker)
		trackerCfg = applyTrackerConfigOverrides(trackerCfg, meta.TrackerConfigOverrides)
		trackerConfigs[idx] = trackerCfg
		if _, ok := s.registry.Lookup(tracker); !ok {
			continue
		}
		trackerMeta, err := PrepareTrackerUploadTorrent(meta, s.cfg.MainSettings.DBPath, tracker, trackerCfg)
		if err != nil {
			prepErrors[idx] = fmt.Errorf("trackers: %s upload torrent artifact: %w", tracker, err)
			continue
		}
		trackerMetas[idx] = trackerMeta
	}

	jobs := make(chan int)
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for idx := range jobs {
			tracker := trackers[idx]
			result := trackerUploadResult{tracker: tracker}

			if ctx.Err() != nil {
				result.err = ctx.Err()
				results[idx] = result
				continue
			}

			definition, ok := s.registry.Lookup(tracker)
			if !ok {
				result.notImplemented = true
				results[idx] = result
				continue
			}

			trackerCfg := trackerConfigs[idx]
			trackerMeta := trackerMetas[idx]
			if prepErrors[idx] != nil {
				result.err = prepErrors[idx]
				results[idx] = result
				continue
			}
			resolution, ok := preflight[strings.ToUpper(strings.TrimSpace(tracker))]
			if !ok {
				var err error
				resolution, err = ensureDescriptionImageHost(ctx, tracker, trackerMeta, s.cfg, trackerCfg, s.repo, s.images, s.logger)
				if err != nil {
					s.logger.Warnf("trackers: description image host resolution failed for %s: %v", tracker, err)
					result.err = err
					results[idx] = result
					continue
				}
			}
			if resolution.blocking {
				message := strings.TrimSpace(resolution.feedback.Message)
				if message == "" {
					message = "image-host requirements could not be met"
				}
				s.logger.Warnf("trackers: skipping %s due to image host failure: %s", tracker, message)
				result.err = errors.New(message)
				results[idx] = result
				continue
			}
			assets, err := ResolveDescriptionAssets(ctx, tracker, trackerMeta, s.repo, s.logger)
			if err != nil {
				result.err = err
				results[idx] = result
				continue
			}
			applyResolvedDescriptionScreenshots(ctx, trackerMeta, s.repo, nil, &assets, resolution.screenshots)
			uploadSummary, err := definition.Upload(ctx, UploadRequest{
				Tracker:       tracker,
				Meta:          trackerMeta,
				TrackerConfig: trackerCfg,
				AppConfig:     s.cfg,
				Logger:        s.logger,
				Repo:          s.repo,
				Images:        s.images,
				Assets:        &assets,
			})
			if err != nil {
				if errors.Is(err, internalerrors.ErrNotImplemented) {
					result.notImplemented = true
					results[idx] = result
					continue
				}
				result.err = err
				results[idx] = result
				continue
			}

			result.summary = uploadSummary
			results[idx] = result
		}
	}

	for range workerCount {
		wg.Add(1)
		go worker()
	}

	for idx := range trackers {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			for canceledIdx := idx; canceledIdx < len(trackers); canceledIdx++ {
				if results[canceledIdx].tracker == "" {
					results[canceledIdx] = trackerUploadResult{tracker: trackers[canceledIdx], err: ctx.Err()}
				}
			}
			return results, fmt.Errorf("context canceled: %w", ctx.Err())
		case jobs <- idx:
		}
	}

	close(jobs)
	wg.Wait()

	if ctx.Err() != nil {
		return results, fmt.Errorf("context canceled: %w", ctx.Err())
	}

	return results, nil
}

func isContextCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// uploadRecordCleanupContext keeps terminal upload record updates bounded while
// allowing them to outlive the caller's canceled context.
func uploadRecordCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), uploadRecordFinalizationTimeout)
}

func (s *Service) maxConcurrentTrackerUploads(total int) int {
	if total <= 0 {
		return 0
	}
	limit := s.cfg.PostUpload.MaxConcurrentTrackers
	if limit <= 0 {
		limit = defaultMaxConcurrentTrackerUploads
	}
	if limit > total {
		return total
	}
	return limit
}

func (s *Service) preflightDescriptionImageHosts(ctx context.Context, meta api.PreparedMetadata, trackers []string) imageHostPreflight {
	return s.preflightDescriptionImageHostsWithPreferences(ctx, meta, trackers, nil, nil, true)
}

func (s *Service) preflightDescriptionImageHostsWithPreferences(
	ctx context.Context,
	meta api.PreparedMetadata,
	trackers []string,
	preferredImageHosts map[string]string,
	preloaded *preloadedDescriptionAssetData,
	resolveAll bool,
) imageHostPreflight {
	if len(trackers) == 0 || s.registry == nil {
		return nil
	}

	type preflightEntry struct {
		tracker    string
		trackerCfg config.TrackerConfig
		targetKey  string
	}

	entries := make([]preflightEntry, 0, len(trackers))
	representatives := make(map[string]preflightEntry)
	for _, tracker := range trackers {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if _, ok := s.registry.Lookup(tracker); !ok {
			continue
		}
		trackerCfg := trackerConfigFor(s.cfg, tracker)
		trackerCfg = applyTrackerConfigOverrides(trackerCfg, meta.TrackerConfigOverrides)
		targetKey := ""
		policy, err := resolveImageHostPolicyForMetadata(tracker, s.cfg, trackerCfg, meta, meta.ImageHostOverrides)
		if err != nil {
			s.logger.Warnf("trackers: image host preflight failed for %s: %v", tracker, err)
			continue
		}
		if host := preferredHost(effectiveImageHostSelectionPolicy(policy, preferredImageHosts[strings.ToUpper(strings.TrimSpace(tracker))])); host != "" {
			targetKey = strings.ToLower(strings.TrimSpace(host)) + "\x00" + normalizeUsageScope(usageScopeForHost(host))
		} else if host := preferredImageHosts[strings.ToUpper(strings.TrimSpace(tracker))]; strings.TrimSpace(host) != "" {
			targetKey = strings.ToLower(strings.TrimSpace(host)) + "\x00" + normalizeUsageScope(usageScopeForHost(host))
		}
		entry := preflightEntry{
			tracker:    tracker,
			trackerCfg: trackerCfg,
			targetKey:  targetKey,
		}
		entries = append(entries, entry)
		if targetKey != "" {
			if _, ok := representatives[targetKey]; !ok {
				representatives[targetKey] = entry
			}
		}
	}

	if preloaded == nil && (len(representatives) > 0 || resolveAll) {
		var err error
		preloaded, err = preloadDescriptionAssetData(ctx, meta, s.repo)
		if err != nil {
			s.logger.Warnf("trackers: image host preflight preload failed for %s: %v", meta.SourcePath, err)
			preloaded = nil
		}
	}

	resolutions := make(imageHostPreflight, len(entries))
	var (
		mu         sync.Mutex
		wg         sync.WaitGroup
		reuploaded bool
	)
	for _, entry := range representatives {
		preloadedCopy := clonePreloadedDescriptionAssetData(preloaded)
		wg.Add(1)
		go func(entry preflightEntry, preloadedCopy *preloadedDescriptionAssetData) {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}
			resolution, err := ensureDescriptionImageHostWithData(
				ctx,
				entry.tracker,
				meta,
				s.cfg,
				entry.trackerCfg,
				s.repo,
				s.images,
				s.logger,
				preloadedCopy,
				preferredImageHosts[strings.ToUpper(strings.TrimSpace(entry.tracker))],
			)
			if err != nil {
				s.logger.Warnf("trackers: image host preflight failed for %s: %v", entry.tracker, err)
				return
			}
			mu.Lock()
			resolutions[strings.ToUpper(strings.TrimSpace(entry.tracker))] = resolution
			if resolution.feedback.Reuploaded {
				reuploaded = true
			}
			mu.Unlock()
		}(entry, preloadedCopy)
	}
	wg.Wait()

	if !resolveAll {
		return resolutions
	}

	if len(representatives) > 0 && reuploaded {
		refreshed, err := preloadDescriptionAssetData(ctx, meta, s.repo)
		if err != nil {
			s.logger.Warnf("trackers: image host preflight reload failed for %s: %v", meta.SourcePath, err)
		} else {
			preloaded = refreshed
		}
	}

	for _, entry := range entries {
		key := strings.ToUpper(strings.TrimSpace(entry.tracker))
		if _, ok := resolutions[key]; ok {
			continue
		}
		resolution, err := ensureDescriptionImageHostWithData(
			ctx,
			entry.tracker,
			meta,
			s.cfg,
			entry.trackerCfg,
			s.repo,
			s.images,
			s.logger,
			preloaded,
			preferredImageHosts[strings.ToUpper(strings.TrimSpace(entry.tracker))],
		)
		if err != nil {
			s.logger.Warnf("trackers: image host preflight failed for %s: %v", entry.tracker, err)
			continue
		}
		resolutions[key] = resolution
	}
	return resolutions
}

func (s *Service) BuildPreparation(ctx context.Context, meta api.PreparedMetadata, trackersList []string) (api.PreparationPreview, error) {
	select {
	case <-ctx.Done():
		return api.PreparationPreview{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	resolved := trackersList
	if len(resolved) == 0 {
		resolved = ResolveTrackersWithDefaults(s.cfg, meta.Trackers, meta.TrackersRemove, s.logger)
	}
	resolved = filterTrackersByRuleFailures(resolved, meta.TrackerRuleFailures, meta.IgnoreTrackerRuleFailures, s.logger)
	resolved = filterTrackersByBlocks(resolved, meta.BlockedTrackers, s.logger)
	if len(resolved) == 0 {
		return api.PreparationPreview{}, errors.New("trackers: no trackers configured")
	}
	if s.registry == nil {
		return api.PreparationPreview{}, errors.New("trackers: registry not configured")
	}

	s.logger.Debugf("trackers: building preparation for %d trackers", len(resolved))

	preloaded, err := preloadDescriptionAssetData(ctx, meta, s.repo)
	if err != nil {
		s.logger.Warnf("trackers: preparation preload failed for %s: %v", meta.SourcePath, err)
		preloaded = nil
	}
	preferredImageHosts := preparationImageHostPreferences(s.cfg, meta, resolved, s.logger)
	preflight := s.preflightDescriptionImageHostsWithPreferences(ctx, meta, resolved, preferredImageHosts, preloaded, false)
	preflightUploaded := false
	for _, resolution := range preflight {
		if resolution.feedback.Reuploaded {
			preflightUploaded = true
			break
		}
	}
	if preflightUploaded {
		refreshed, reloadErr := preloadDescriptionAssetData(ctx, meta, s.repo)
		if reloadErr != nil {
			s.logger.Warnf("trackers: preparation preload reload failed for %s: %v", meta.SourcePath, reloadErr)
		} else {
			preloaded = refreshed
		}
	}

	grouped := make(map[string]*api.PreparationDescription)
	order := make([]string, 0, len(resolved))
	placeholderCount := 0
	placeholder := func(groupKey, tracker string, note string, imageHost api.ImageHostFeedback) {
		key := strings.TrimSpace(groupKey)
		if key == "" {
			key = tracker
		}
		entry, exists := grouped[key]
		if !exists {
			entry = &api.PreparationDescription{
				GroupKey:           key,
				Trackers:           []string{},
				RawDescription:     "",
				RawDescriptionHTML: "",
				Description:        "",
				DescriptionHTML:    "",
				ImageHost:          imageHost,
			}
			grouped[key] = entry
			order = append(order, key)
		} else {
			entry.ImageHost = mergePreparationImageHostFeedback(entry.ImageHost, imageHost)
		}
		entry.Trackers = append(entry.Trackers, tracker)
		placeholderCount++
		if note != "" {
			s.logger.Warnf("trackers: preparation placeholder for %s (%s)", tracker, note)
		} else {
			s.logger.Warnf("trackers: preparation placeholder for %s", tracker)
		}
	}

	for _, tracker := range resolved {
		select {
		case <-ctx.Done():
			return api.PreparationPreview{}, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		definition, ok := s.registry.Lookup(tracker)
		if !ok {
			placeholder("", tracker, "not registered", api.ImageHostFeedback{})
			continue
		}
		builder, ok := definition.(DescriptionBuilder)
		if !ok {
			placeholder("", tracker, "no description builder", api.ImageHostFeedback{})
			continue
		}
		trackerCfg := trackerConfigFor(s.cfg, tracker)
		trackerCfg = applyTrackerConfigOverrides(trackerCfg, meta.TrackerConfigOverrides)
		key := strings.ToUpper(strings.TrimSpace(tracker))
		resolution, ok := preflight[key]
		if !ok {
			var err error
			resolution, err = ensureDescriptionImageHostWithData(
				ctx,
				tracker,
				meta,
				s.cfg,
				trackerCfg,
				s.repo,
				s.images,
				s.logger,
				preloaded,
				preferredImageHosts[key],
			)
			if err != nil {
				s.logger.Warnf("trackers: preparation image host resolution failed for %s: %v", tracker, err)
				placeholder("", tracker, fmt.Sprintf("image host error: %v", err), api.ImageHostFeedback{})
				continue
			}
		}
		if resolution.blocking {
			feedback := blockedPreparationImageHostFeedback(resolution.feedback)
			placeholder(preparationBlockedImageHostGroupKey(tracker), tracker, "image host blocked", feedback)
			continue
		}
		assets, err := resolveDescriptionAssets(ctx, tracker, meta, s.repo, s.logger, preloaded)
		if err != nil {
			s.logger.Warnf("trackers: preparation assets failed for %s: %v", tracker, err)
			assets = DescriptionAssets{}
		}
		applyResolvedDescriptionScreenshots(ctx, meta, s.repo, preloaded, &assets, resolution.screenshots)
		result, err := builder.BuildDescription(ctx, DescriptionRequest{
			Tracker:       tracker,
			Meta:          meta,
			TrackerConfig: trackerCfg,
			AppConfig:     s.cfg,
			Logger:        s.logger,
			Repo:          s.repo,
			Assets:        &assets,
		})
		if err != nil {
			s.logger.Errorf("trackers: preparation failed for %s: %v", tracker, err)
			placeholder("", tracker, "build error", api.ImageHostFeedback{})
			continue
		}

		descriptionText := strings.TrimSpace(result.Description)
		if descriptionText == "" {
			s.logger.Infof("trackers: preparation empty description for %s", tracker)
		}
		groupKey := strings.TrimSpace(result.Group)
		if groupKey == "" {
			groupKey = tracker
		}
		groupKey = preparationGroupKey(groupKey, resolution.feedback.SelectedHost, resolution.usageScope)

		descriptionHTML := description.Render(descriptionText)
		candidate := api.PreparationDescription{
			RawDescription:     descriptionText,
			RawDescriptionHTML: descriptionHTML,
			Description:        descriptionText,
			DescriptionHTML:    descriptionHTML,
			HasOverride:        assets.Override,
			ImageHost:          resolution.feedback,
		}
		entry := preparationDescriptionGroup(grouped, &order, groupKey, candidate)

		entry.Trackers = append(entry.Trackers, tracker)
		entry.ImageHost = mergePreparationImageHostFeedback(entry.ImageHost, resolution.feedback)
		if descriptionText == "" {
			placeholderCount++
		} else {
			s.logger.Debugf("trackers: preparation built description for %s", tracker)
		}
	}

	if placeholderCount > 0 {
		s.logger.Infof("trackers: preparation placeholders created for %d trackers", placeholderCount)
	}

	results := make([]api.PreparationDescription, 0, len(order))
	for _, key := range order {
		entry := grouped[key]
		if entry == nil {
			continue
		}
		results = append(results, *entry)
	}

	results = stabilizePreparationDescriptionGroupKeys(results)

	return api.PreparationPreview{SourcePath: meta.SourcePath, Descriptions: results}, nil
}

func preparationImageHostPreferences(appCfg config.Config, meta api.PreparedMetadata, trackers []string, logger api.Logger) map[string]string {
	if meta.ImageHostOverrides.PreferredHost != nil {
		return nil
	}
	targets, err := NeededImageUploadTargetsForMetadata(appCfg, trackers, "", meta)
	if err != nil {
		if logger != nil {
			logger.Warnf("trackers: preparation image host target resolution failed: %v", err)
		}
		return nil
	}
	if len(targets) == 0 {
		return nil
	}
	preferred := make(map[string]string, len(trackers))
	for _, target := range targets {
		host := strings.ToLower(strings.TrimSpace(target.Host))
		if host == "" {
			continue
		}
		for _, tracker := range target.Trackers {
			name := strings.ToUpper(strings.TrimSpace(tracker))
			if name == "" {
				continue
			}
			if _, exists := preferred[name]; !exists {
				preferred[name] = host
			}
		}
	}
	return preferred
}

func blockedPreparationImageHostFeedback(feedback api.ImageHostFeedback) api.ImageHostFeedback {
	feedback.Status = "blocked"
	if strings.TrimSpace(feedback.Message) == "" {
		feedback.Message = "image-host requirements could not be met"
	}
	if len(feedback.Warnings) == 0 {
		feedback.Warnings = []api.ImageHostWarning{{Message: feedback.Message}}
	}
	return feedback
}

func preparationBlockedImageHostGroupKey(tracker string) string {
	name := strings.ToLower(strings.TrimSpace(tracker))
	if name == "" {
		name = "tracker"
	}
	return name + "|blocked|image-host"
}

func (s *Service) BuildUploadDryRun(ctx context.Context, meta api.PreparedMetadata, trackersList []string) ([]api.TrackerDryRunEntry, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	resolved := trackersList
	if len(resolved) == 0 {
		resolved = ResolveTrackersWithDefaults(s.cfg, meta.Trackers, meta.TrackersRemove, s.logger)
	}
	resolved = filterKnownTrackers(resolved, s.logger)
	resolved = filterTrackersByRuleFailures(resolved, meta.TrackerRuleFailures, meta.IgnoreTrackerRuleFailures, s.logger)
	resolved = filterTrackersByBlocks(resolved, meta.BlockedTrackers, s.logger)
	if len(resolved) == 0 {
		return nil, errors.New("trackers: no trackers configured")
	}
	if s.registry == nil {
		return nil, errors.New("trackers: registry not configured")
	}

	s.logger.Debugf("trackers: building upload dry-run for %d trackers", len(resolved))

	preloaded, err := preloadDescriptionAssetData(ctx, meta, s.repo)
	if err != nil {
		s.logger.Warnf("trackers: dry-run preload failed for %s: %v", meta.SourcePath, err)
		preloaded = nil
	}

	results := make([]api.TrackerDryRunEntry, 0, len(resolved))
	for _, tracker := range resolved {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		entry := api.TrackerDryRunEntry{Tracker: tracker}
		definition, ok := s.registry.Lookup(tracker)
		if !ok {
			entry.Status = "not_supported"
			entry.Message = "tracker not registered"
			results = append(results, entry)
			continue
		}
		builder, ok := definition.(UploadDryRunBuilder)
		if !ok {
			entry.Status = "not_supported"
			entry.Message = "dry-run payload preview not implemented"
			results = append(results, entry)
			continue
		}

		trackerCfg := trackerConfigFor(s.cfg, tracker)
		trackerCfg = applyTrackerConfigOverrides(trackerCfg, meta.TrackerConfigOverrides)
		trackerMeta, err := PrepareTrackerUploadTorrent(meta, s.cfg.MainSettings.DBPath, tracker, trackerCfg)
		if err != nil {
			entry.Status = "error"
			entry.Message = err.Error()
			results = append(results, entry)
			continue
		}
		resolution, err := ensureDescriptionImageHostWithData(ctx, tracker, trackerMeta, s.cfg, trackerCfg, s.repo, s.images, s.logger, preloaded)
		if err != nil {
			s.logger.Warnf("trackers: dry-run image host resolution failed for %s: %v", tracker, err)
			entry.Status = "error"
			entry.Message = err.Error()
			results = append(results, entry)
			continue
		}
		if resolution.blocking {
			entry.Status = "blocked"
			entry.Message = resolution.feedback.Message
			entry.ImageHost = resolution.feedback
			results = append(results, entry)
			continue
		}
		assets, err := resolveDescriptionAssets(ctx, tracker, trackerMeta, s.repo, s.logger, preloaded)
		if err != nil {
			entry.Status = "error"
			entry.Message = err.Error()
			results = append(results, entry)
			continue
		}
		applyResolvedDescriptionScreenshots(ctx, trackerMeta, s.repo, preloaded, &assets, resolution.screenshots)
		preview, err := builder.BuildUploadDryRun(ctx, UploadRequest{
			Tracker:       tracker,
			Meta:          trackerMeta,
			TrackerConfig: trackerCfg,
			AppConfig:     s.cfg,
			Logger:        s.logger,
			Repo:          s.repo,
			Images:        s.images,
			Assets:        &assets,
		})
		if err != nil {
			entry.Status = "error"
			entry.Message = err.Error()
			results = append(results, entry)
			continue
		}
		if strings.TrimSpace(preview.Tracker) == "" {
			preview.Tracker = tracker
		}
		if strings.TrimSpace(preview.Status) == "" {
			preview.Status = "ready"
		}
		preview.ImageHost = resolution.feedback
		results = append(results, preview)
	}

	return results, nil
}

func filterTrackersByRuleFailures(trackers []string, failures map[string][]api.RuleFailure, ignore bool, logger api.Logger) []string {
	if ignore || len(trackers) == 0 || len(failures) == 0 {
		return trackers
	}

	filtered := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		trackerFailures, ok := failures[name]
		if ok && len(trackerFailures) > 0 {
			if logger != nil {
				for _, failure := range trackerFailures {
					logger.Warnf("trackers: skipping %s due to rule failure %s (%s)", name, failure.Rule, failure.Reason)
				}
			}
			continue
		}
		filtered = append(filtered, tracker)
	}

	return filtered
}

func filterTrackersByBlocks(trackers []string, blocked map[string][]api.TrackerBlockReason, logger api.Logger) []string {
	if len(trackers) == 0 || len(blocked) == 0 {
		return trackers
	}

	filtered := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}

		reasons := blocked[name]
		if len(reasons) == 0 {
			filtered = append(filtered, tracker)
			continue
		}

		if logger != nil {
			logger.Warnf("trackers: skipping %s due to blocked state (%s)", name, formatTrackerBlockReasons(reasons))
		}
	}

	return filtered
}

func formatTrackerBlockReasons(reasons []api.TrackerBlockReason) string {
	if len(reasons) == 0 {
		return "blocked"
	}

	labels := make([]string, 0, len(reasons))
	seen := make(map[api.TrackerBlockReason]struct{}, len(reasons))
	for _, reason := range reasons {
		if _, ok := seen[reason]; ok {
			continue
		}
		seen[reason] = struct{}{}
		label := strings.TrimSpace(string(reason))
		if label != "" {
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		return "blocked"
	}
	return strings.Join(labels, ", ")
}

func applyTrackerConfigOverrides(cfg config.TrackerConfig, overrides api.TrackerConfigOverrides) config.TrackerConfig {
	if overrides.Anon != nil {
		cfg.Anon = *overrides.Anon
	}
	if overrides.Draft != nil {
		cfg.Draft = *overrides.Draft
	}
	if overrides.ModQ != nil {
		cfg.ModQ = *overrides.ModQ
	}
	if overrides.Channel != nil {
		cfg.Channel = strings.TrimSpace(*overrides.Channel)
	}
	return cfg
}

func filterKnownTrackers(trackers []string, logger api.Logger) []string {
	if len(trackers) == 0 {
		return trackers
	}

	filtered := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		upper := strings.ToUpper(strings.TrimSpace(tracker))
		if upper == "" {
			continue
		}
		if !IsKnownTracker(upper) {
			if logger != nil {
				logger.Infof("trackers: unknown tracker %q, skipping", tracker)
			}
			continue
		}
		filtered = append(filtered, upper)
	}

	return filtered
}

func resolveTrackers(cfg config.Config, override []string, remove []string) []string {
	trackers := []string{}
	if len(override) > 0 {
		trackers = normalizeTrackers(override)
	} else if len(cfg.Trackers.DefaultTrackers) > 0 {
		trackers = normalizeTrackers([]string(cfg.Trackers.DefaultTrackers))
	}

	if len(trackers) == 0 || len(remove) == 0 {
		return trackers
	}

	removeSet := make(map[string]struct{}, len(remove))
	for _, value := range normalizeTrackers(remove) {
		removeSet[value] = struct{}{}
	}

	filtered := trackers[:0]
	for _, tracker := range trackers {
		if _, found := removeSet[tracker]; !found {
			filtered = append(filtered, tracker)
		}
	}

	return filtered
}

func resolveTrackersWithDefaults(cfg config.Config, override []string, remove []string) []string {
	trackers := normalizeTrackers([]string(cfg.Trackers.DefaultTrackers))
	if len(override) > 0 {
		trackers = mergeTrackers(trackers, normalizeTrackers(override))
	}

	if len(trackers) == 0 || len(remove) == 0 {
		return trackers
	}

	removeSet := make(map[string]struct{}, len(remove))
	for _, value := range normalizeTrackers(remove) {
		removeSet[value] = struct{}{}
	}

	filtered := trackers[:0]
	for _, tracker := range trackers {
		if _, found := removeSet[tracker]; !found {
			filtered = append(filtered, tracker)
		}
	}

	return filtered
}

func mergeTrackers(base []string, extra []string) []string {
	if len(extra) == 0 {
		return base
	}

	seen := make(map[string]struct{}, len(base)+len(extra))
	merged := make([]string, 0, len(base)+len(extra))
	for _, tracker := range base {
		if tracker == "" {
			continue
		}
		if _, exists := seen[tracker]; exists {
			continue
		}
		seen[tracker] = struct{}{}
		merged = append(merged, tracker)
	}
	for _, tracker := range extra {
		if tracker == "" {
			continue
		}
		if _, exists := seen[tracker]; exists {
			continue
		}
		seen[tracker] = struct{}{}
		merged = append(merged, tracker)
	}
	return merged
}

func preparationGroupKey(group string, host string, usageScope string) string {
	trimmedGroup := strings.TrimSpace(group)
	trimmedHost := strings.ToLower(strings.TrimSpace(host))
	trimmedScope := normalizeUsageScope(usageScope)
	if trimmedHost == "" && trimmedScope == globalImageUsageScope {
		return trimmedGroup
	}
	return trimmedGroup + "|" + trimmedHost + "|" + trimmedScope
}

func preparationDescriptionGroup(grouped map[string]*api.PreparationDescription, order *[]string, groupKey string, candidate api.PreparationDescription) *api.PreparationDescription {
	baseKey := strings.TrimSpace(groupKey)
	if baseKey == "" {
		baseKey = "description"
	}
	if entry := matchingUnit3DPreparationDescriptionGroup(grouped, *order, baseKey, candidate); entry != nil {
		return entry
	}

	if entry, ok := grouped[baseKey]; ok && preparationDescriptionsMatch(*entry, candidate) {
		return entry
	}
	if _, ok := grouped[baseKey]; !ok {
		return addPreparationDescriptionGroup(grouped, order, baseKey, candidate)
	}

	for variant := 2; ; variant++ {
		key := preparationVariantGroupKey(baseKey, variant)
		if entry, ok := grouped[key]; ok && preparationDescriptionsMatch(*entry, candidate) {
			return entry
		}
		if _, ok := grouped[key]; !ok {
			return addPreparationDescriptionGroup(grouped, order, key, candidate)
		}
	}
}

func matchingUnit3DPreparationDescriptionGroup(grouped map[string]*api.PreparationDescription, order []string, groupKey string, candidate api.PreparationDescription) *api.PreparationDescription {
	if preparationDescriptionBaseGroup(groupKey) != "unit3d" {
		return nil
	}
	for _, key := range order {
		if preparationDescriptionBaseGroup(key) != "unit3d" {
			continue
		}
		entry := grouped[key]
		if entry == nil || !preparationDescriptionsMatch(*entry, candidate) {
			continue
		}
		return entry
	}
	return nil
}

func preparationDescriptionBaseGroup(groupKey string) string {
	baseGroup, _, _ := parsePreparationDescriptionGroupKey(groupKey)
	if baseGroup == "" {
		baseGroup = strings.ToLower(strings.TrimSpace(groupKey))
	}
	return baseGroup
}

func addPreparationDescriptionGroup(grouped map[string]*api.PreparationDescription, order *[]string, groupKey string, candidate api.PreparationDescription) *api.PreparationDescription {
	entry := candidate
	entry.GroupKey = groupKey
	entry.Trackers = []string{}
	grouped[groupKey] = &entry
	*order = append(*order, groupKey)
	return &entry
}

func preparationDescriptionsMatch(left api.PreparationDescription, right api.PreparationDescription) bool {
	return left.RawDescription == right.RawDescription &&
		left.Description == right.Description &&
		left.HasOverride == right.HasOverride
}

func mergePreparationImageHostFeedback(left api.ImageHostFeedback, right api.ImageHostFeedback) api.ImageHostFeedback {
	if left.Status == "" {
		return right
	}
	if right.Status == "" {
		return left
	}

	merged := left
	if imageHostStatusRank(right.Status) > imageHostStatusRank(merged.Status) {
		merged.Status = right.Status
	}
	if strings.TrimSpace(merged.SelectedHost) == "" {
		merged.SelectedHost = strings.TrimSpace(right.SelectedHost)
	} else if strings.TrimSpace(right.SelectedHost) != "" && !strings.EqualFold(strings.TrimSpace(merged.SelectedHost), strings.TrimSpace(right.SelectedHost)) {
		merged.SelectedHost = ""
	}
	merged.AllowedHosts = appendUniqueStrings(merged.AllowedHosts, right.AllowedHosts)
	merged.Warnings = appendUniqueImageHostWarnings(merged.Warnings, right.Warnings)
	merged.Reuploaded = merged.Reuploaded || right.Reuploaded

	leftMessage := strings.TrimSpace(merged.Message)
	rightMessage := strings.TrimSpace(right.Message)
	switch {
	case leftMessage == "":
		merged.Message = rightMessage
	case rightMessage == "" || leftMessage == rightMessage:
		merged.Message = leftMessage
	default:
		merged.Message = "Multiple image-host states apply to this description group."
	}

	return merged
}

func imageHostStatusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "blocked":
		return 4
	case "warning":
		return 3
	case "reuploaded":
		return 2
	case "reused":
		return 1
	default:
		return 0
	}
}

func appendUniqueStrings(values []string, additions []string) []string {
	if len(additions) == 0 {
		return values
	}
	seen := make(map[string]struct{}, len(values)+len(additions))
	out := make([]string, 0, len(values)+len(additions))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	for _, value := range additions {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func appendUniqueImageHostWarnings(values []api.ImageHostWarning, additions []api.ImageHostWarning) []api.ImageHostWarning {
	if len(additions) == 0 {
		return values
	}
	seen := make(map[string]struct{}, len(values)+len(additions))
	out := make([]api.ImageHostWarning, 0, len(values)+len(additions))
	for _, value := range values {
		host := strings.TrimSpace(value.Host)
		message := strings.TrimSpace(value.Message)
		if host == "" && message == "" {
			continue
		}
		key := strings.ToLower(host) + "\x00" + message
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, api.ImageHostWarning{Host: host, Message: message})
	}
	for _, value := range additions {
		host := strings.TrimSpace(value.Host)
		message := strings.TrimSpace(value.Message)
		if host == "" && message == "" {
			continue
		}
		key := strings.ToLower(host) + "\x00" + message
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, api.ImageHostWarning{Host: host, Message: message})
	}
	return out
}

func preparationVariantGroupKey(groupKey string, variant int) string {
	trimmed := strings.TrimSpace(groupKey)
	if variant <= 1 {
		return trimmed
	}
	parts := strings.SplitN(trimmed, "|", 3)
	if len(parts) == 3 {
		host := strings.TrimSpace(parts[1])
		if host == "" {
			host = "variant"
		}
		return fmt.Sprintf("%s|%s#%d|%s", strings.TrimSpace(parts[0]), host, variant, strings.TrimSpace(parts[2]))
	}
	return fmt.Sprintf("%s|variant:%d|%s", trimmed, variant, globalImageUsageScope)
}

func stabilizePreparationDescriptionGroupKeys(descriptions []api.PreparationDescription) []api.PreparationDescription {
	if len(descriptions) < 2 {
		return descriptions
	}

	countByBase := make(map[string]int, len(descriptions))
	for _, entry := range descriptions {
		baseGroup, _, _ := parsePreparationDescriptionGroupKey(entry.GroupKey)
		if baseGroup == "" {
			baseGroup = strings.ToLower(strings.TrimSpace(entry.GroupKey))
		}
		if baseGroup == "" {
			continue
		}
		countByBase[baseGroup]++
	}

	for idx := range descriptions {
		baseGroup, _, _ := parsePreparationDescriptionGroupKey(descriptions[idx].GroupKey)
		if baseGroup == "" {
			baseGroup = strings.ToLower(strings.TrimSpace(descriptions[idx].GroupKey))
		}
		if baseGroup == "" || countByBase[baseGroup] <= 1 {
			continue
		}
		descriptions[idx].GroupKey = stablePreparationDescriptionGroupKey(baseGroup, descriptions[idx].Trackers)
	}
	return descriptions
}

func stablePreparationDescriptionGroupKey(baseGroup string, trackers []string) string {
	base := strings.TrimSpace(baseGroup)
	normalized := normalizedPreparationGroupTrackers(trackers)
	if len(normalized) == 0 {
		return preparationGroupKey(base, "variant", globalImageUsageScope)
	}
	if len(normalized) == 1 {
		return preparationGroupKey(base, strings.ToLower(normalized[0]), trackerImageUsageScope(normalized[0]))
	}
	return preparationGroupKey(base, strings.ToLower(strings.Join(normalized, "+")), globalImageUsageScope)
}

func normalizedPreparationGroupTrackers(trackers []string) []string {
	if len(trackers) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(trackers))
	normalized := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		value := strings.ToUpper(strings.TrimSpace(tracker))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
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
		if _, exists := seen[upper]; exists {
			continue
		}
		seen[upper] = struct{}{}
		out = append(out, upper)
	}
	return out
}
