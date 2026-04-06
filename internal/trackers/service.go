// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
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

const defaultMaxConcurrentTrackerUploads = 4

type trackerUploadResult struct {
	tracker        string
	summary        api.UploadSummary
	err            error
	notImplemented bool
}

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

func (s *Service) Upload(ctx context.Context, meta api.PreparedMetadata) (api.UploadSummary, error) {
	select {
	case <-ctx.Done():
		return api.UploadSummary{}, ctx.Err()
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
				return api.UploadSummary{}, ctx.Err()
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

	var finalizePending func(string)
	if s.repo != nil {
		recordStatuses := make(map[string]string, len(trackers))
		var recordMu sync.Mutex
		updateRecordStatus := func(tracker string, status string) {
			trimmedTracker := strings.TrimSpace(tracker)
			trimmedStatus := strings.TrimSpace(status)
			if trimmedTracker == "" || trimmedStatus == "" {
				return
			}

			recordMu.Lock()
			current, ok := recordStatuses[trimmedTracker]
			if ok && current == trimmedStatus {
				recordMu.Unlock()
				return
			}
			recordMu.Unlock()

			if err := s.repo.UpdateLatestUploadRecordStatus(ctx, meta.SourcePath, trimmedTracker, trimmedStatus); err != nil {
				s.logger.Warnf("trackers: status update %s (%s): %v", trimmedTracker, trimmedStatus, err)
				return
			}

			recordMu.Lock()
			recordStatuses[trimmedTracker] = trimmedStatus
			recordMu.Unlock()
		}
		finalizePending = func(status string) {
			recordMu.Lock()
			pendingTrackers := make([]string, 0, len(recordStatuses))
			for tracker, currentStatus := range recordStatuses {
				if currentStatus == "pending" || currentStatus == "pending-internal" {
					pendingTrackers = append(pendingTrackers, tracker)
				}
			}
			recordMu.Unlock()

			for _, tracker := range pendingTrackers {
				updateRecordStatus(tracker, status)
			}
		}

		for _, tracker := range trackers {
			select {
			case <-ctx.Done():
				finalizePending("canceled")
				return api.UploadSummary{}, ctx.Err()
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
			recordStatuses[strings.TrimSpace(tracker)] = status
			recordMu.Unlock()
		}

		results, err := s.uploadTrackersConcurrently(ctx, meta, trackers)
		if err != nil {
			finalizePending("canceled")
			return api.UploadSummary{}, err
		}

		summary := api.UploadSummary{}
		notImplemented := make([]string, 0)
		failedTrackers := make([]string, 0)
		failedMessages := make([]string, 0)

		for _, result := range results {
			if result.notImplemented {
				notImplemented = append(notImplemented, result.tracker)
				updateRecordStatus(result.tracker, "failed")
				continue
			}
			if result.err != nil {
				failedTrackers = append(failedTrackers, result.tracker)
				failedMessages = append(failedMessages, fmt.Sprintf("%s: %v", result.tracker, result.err))
				updateRecordStatus(result.tracker, "failed")
				continue
			}

			updateRecordStatus(result.tracker, "uploaded")
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
			return summary, nil
		}
		if len(failedTrackers) > 0 {
			return summary, fmt.Errorf("trackers: %s", strings.Join(failedMessages, "; "))
		}
		if len(notImplemented) > 0 {
			return summary, fmt.Errorf("trackers: %s: %w", strings.Join(notImplemented, ","), internalerrors.ErrNotImplemented)
		}

		return summary, nil
	}

	results, err := s.uploadTrackersConcurrently(ctx, meta, trackers)
	if err != nil {
		return api.UploadSummary{}, err
	}

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
		return summary, nil
	}
	if len(failedTrackers) > 0 {
		return summary, fmt.Errorf("trackers: %s", strings.Join(failedMessages, "; "))
	}
	if len(notImplemented) > 0 {
		return summary, fmt.Errorf("trackers: %s: %w", strings.Join(notImplemented, ","), internalerrors.ErrNotImplemented)
	}

	return summary, nil
}

func (s *Service) uploadTrackersConcurrently(ctx context.Context, meta api.PreparedMetadata, trackers []string) ([]trackerUploadResult, error) {
	workerCount := s.maxConcurrentTrackerUploads(len(trackers))
	results := make([]trackerUploadResult, len(trackers))
	if workerCount <= 0 {
		return results, nil
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

			trackerCfg, _ := trackerConfigFor(s.cfg, tracker)
			trackerCfg = applyTrackerConfigOverrides(trackerCfg, meta.TrackerConfigOverrides)
			resolution, err := ensureDescriptionImageHost(ctx, tracker, meta, s.cfg, trackerCfg, s.repo, s.images, s.logger)
			if err != nil {
				s.logger.Warnf("trackers: description image host resolution failed for %s: %v", tracker, err)
				result.err = err
				results[idx] = result
				continue
			}
			assets, err := ResolveDescriptionAssets(ctx, tracker, meta, s.repo, s.logger)
			if err != nil {
				result.err = err
				results[idx] = result
				continue
			}
			assets.Screenshots = resolution.screenshots
			uploadSummary, err := definition.Upload(ctx, UploadRequest{
				Tracker:       tracker,
				Meta:          meta,
				TrackerConfig: trackerCfg,
				AppConfig:     s.cfg,
				Logger:        s.logger,
				Repo:          s.repo,
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

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go worker()
	}

	for idx := range trackers {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		case jobs <- idx:
		}
	}

	close(jobs)
	wg.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	return results, nil
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

func (s *Service) BuildPreparation(ctx context.Context, meta api.PreparedMetadata, trackersList []string) (api.PreparationPreview, error) {
	select {
	case <-ctx.Done():
		return api.PreparationPreview{}, ctx.Err()
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

	grouped := make(map[string]*api.PreparationDescription)
	order := make([]string, 0, len(resolved))
	placeholderCount := 0
	placeholder := func(groupKey, tracker string, note string) {
		key := strings.TrimSpace(groupKey)
		if key == "" {
			key = tracker
		}
		entry, exists := grouped[key]
		if !exists {
			entry = &api.PreparationDescription{
				Trackers:        []string{},
				Description:     "",
				DescriptionHTML: "",
			}
			grouped[key] = entry
			order = append(order, key)
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
			return api.PreparationPreview{}, ctx.Err()
		default:
		}

		definition, ok := s.registry.Lookup(tracker)
		if !ok {
			placeholder("", tracker, "not registered")
			continue
		}
		builder, ok := definition.(DescriptionBuilder)
		if !ok {
			placeholder("", tracker, "no description builder")
			continue
		}
		trackerCfg, _ := trackerConfigFor(s.cfg, tracker)
		trackerCfg = applyTrackerConfigOverrides(trackerCfg, meta.TrackerConfigOverrides)
		resolution, err := ensureDescriptionImageHostWithData(ctx, tracker, meta, s.cfg, trackerCfg, s.repo, s.images, s.logger, preloaded)
		if err != nil {
			s.logger.Warnf("trackers: preparation image host resolution failed for %s: %v", tracker, err)
			placeholder("", tracker, fmt.Sprintf("image host error: %v", err))
			continue
		}
		assets, err := resolveDescriptionAssets(ctx, tracker, meta, s.repo, s.logger, preloaded)
		if err != nil {
			s.logger.Warnf("trackers: preparation assets failed for %s: %v", tracker, err)
			assets = DescriptionAssets{}
		}
		assets.Screenshots = resolution.screenshots
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
			placeholder("", tracker, "build error")
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

		entry, exists := grouped[groupKey]
		if !exists {
			entry = &api.PreparationDescription{
				Trackers:        []string{},
				Description:     descriptionText,
				DescriptionHTML: description.Render(descriptionText),
				ImageHost:       resolution.feedback,
			}
			grouped[groupKey] = entry
			order = append(order, groupKey)
		} else if entry.Description != descriptionText {
			s.logger.Warnf("trackers: preparation group %s description mismatch (tracker=%s)", groupKey, tracker)
		}

		entry.Trackers = append(entry.Trackers, tracker)
		if entry.ImageHost.Status == "" {
			entry.ImageHost = resolution.feedback
		}
		if descriptionText == "" {
			placeholderCount++
		} else {
			s.logger.Infof("trackers: preparation built description for %s", tracker)
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

	return api.PreparationPreview{SourcePath: meta.SourcePath, Descriptions: results}, nil
}

func (s *Service) BuildUploadDryRun(ctx context.Context, meta api.PreparedMetadata, trackersList []string) ([]api.TrackerDryRunEntry, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
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
			return nil, ctx.Err()
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

		trackerCfg, _ := trackerConfigFor(s.cfg, tracker)
		trackerCfg = applyTrackerConfigOverrides(trackerCfg, meta.TrackerConfigOverrides)
		resolution, err := ensureDescriptionImageHostWithData(ctx, tracker, meta, s.cfg, trackerCfg, s.repo, s.images, s.logger, preloaded)
		if err != nil {
			s.logger.Warnf("trackers: dry-run image host resolution failed for %s: %v", tracker, err)
			entry.Status = "error"
			entry.Message = err.Error()
			results = append(results, entry)
			continue
		}
		assets, err := resolveDescriptionAssets(ctx, tracker, meta, s.repo, s.logger, preloaded)
		if err != nil {
			entry.Status = "error"
			entry.Message = err.Error()
			results = append(results, entry)
			continue
		}
		assets.Screenshots = resolution.screenshots
		preview, err := builder.BuildUploadDryRun(ctx, UploadRequest{
			Tracker:       tracker,
			Meta:          meta,
			TrackerConfig: trackerCfg,
			AppConfig:     s.cfg,
			Logger:        s.logger,
			Repo:          s.repo,
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
