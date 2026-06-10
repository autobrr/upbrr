// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func (c *Core) BuildUploadReview(ctx context.Context, req api.Request) (api.UploadReview, error) {
	req = normalizeExecutionRequest(req)
	resolvedReq, err := c.resolveDescriptionOverrideRequest(ctx, req)
	if err != nil {
		return api.UploadReview{}, err
	}
	req = resolvedReq

	if len(req.Paths) == 0 {
		return api.UploadReview{}, internalerrors.ErrInvalidInput
	}
	if c.services.Filesystem == nil {
		return api.UploadReview{}, errors.New("core: filesystem service not configured")
	}
	if c.services.Dupes == nil {
		return api.UploadReview{}, errors.New("core: dupe service not configured")
	}
	if c.services.Torrents == nil {
		return api.UploadReview{}, errors.New("core: torrent service not configured")
	}
	if c.services.Trackers == nil {
		return api.UploadReview{}, errors.New("core: tracker service not configured")
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.UploadReview{}, fmt.Errorf("core: %w", err)
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return api.UploadReview{}, internalerrors.ErrInvalidInput
	}

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return api.UploadReview{}, err
	}

	singleReq := req
	singleReq.Paths = []string{uniquePaths[0]}
	singleReq.Options = options
	singleReq.ExternalIDOverrides = mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, uniquePaths[0]))

	signature := overrideSignature(singleReq.ExternalIDOverrides, singleReq.ReleaseNameOverrides, singleReq.MetadataOverrides, singleReq.TrackerConfigOverrides, singleReq.TrackerSiteOverrides, singleReq.ClientOverrides, singleReq.TorrentOverrides, singleReq.ImageHostOverrides, singleReq.ScreenshotOverrides)
	var (
		meta api.PreparedMetadata
		ok   bool
	)
	if req.Mode == api.ModeGUI {
		meta, ok, err = c.resolveGUICachedPreparedMeta(ctx, singleReq, uniquePaths[0])
		if err != nil {
			return api.UploadReview{}, err
		}
	} else {
		meta, ok = c.getDupeCache(uniquePaths[0], signature)
		if ok {
			meta, err = c.applyRequestToCachedPreparedMeta(ctx, meta, singleReq)
			if err != nil {
				return api.UploadReview{}, err
			}
		}
	}
	if !ok {
		return api.UploadReview{}, fmt.Errorf("core: upload review requires prepared metadata for %s", uniquePaths[0])
	}

	resolvedTrackers := trackers.ResolveTrackersWithDefaults(c.cfg, singleReq.Trackers, meta.TrackersRemove, c.logger)

	dupeResults := make(map[string]api.DupeCheckResult)
	if singleReq.SkipDupeCheck {
		meta.BlockedTrackers = removeTrackerBlockReason(cloneBlockedTrackers(meta.BlockedTrackers), api.TrackerBlockReasonDupe)
		for _, tracker := range resolvedTrackers {
			name := strings.ToUpper(strings.TrimSpace(tracker))
			if name == "" {
				continue
			}
			dupeResults[name] = api.DupeCheckResult{
				Tracker:    name,
				Status:     "skipped",
				Skipped:    true,
				SkipReason: "dupe check skipped",
			}
		}
	} else {
		summary, err := c.services.Dupes.Check(ctx, meta, resolvedTrackers)
		if err != nil {
			return api.UploadReview{}, fmt.Errorf("core: %w", err)
		}
		summary = appendPathedDupeResults(summary, meta.MatchedTrackers)
		applyDupeSummaryToPreparedMeta(&meta, summary)
		meta.BlockedTrackers = removeTrackerBlockReasonForTrackers(meta.BlockedTrackers, api.TrackerBlockReasonDupe, singleReq.IgnoreDupesFor)
		dupeResults = mapDupeResults(summary.Results)
	}

	dryRunMeta := meta
	dryRunMeta.IgnoreTrackerRuleFailures = true
	torrent, err := c.services.Torrents.Create(ctx, dryRunMeta)
	if err != nil {
		return api.UploadReview{}, fmt.Errorf("core: %w", err)
	}
	dryRunMeta.TorrentPath = torrent.Path

	dryRunEntries, err := c.services.Trackers.BuildUploadDryRun(ctx, dryRunMeta, resolvedTrackers)
	if err != nil {
		return api.UploadReview{}, fmt.Errorf("core: %w", err)
	}
	annotateDryRunReleaseNames(dryRunMeta, dryRunEntries)
	dryRunByTracker := make(map[string]api.TrackerDryRunEntry, len(dryRunEntries))
	for _, entry := range dryRunEntries {
		name := strings.ToUpper(strings.TrimSpace(entry.Tracker))
		if name == "" {
			continue
		}
		dryRunByTracker[name] = entry
	}

	bannedChecker := trackers.NewBannedGroupChecker(c.cfg.MainSettings.DBPath)
	group := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-")))
	if strings.Contains(group, "taoe") {
		group = "taoe"
	}

	reviews := make([]api.TrackerReview, 0, len(resolvedTrackers))
	for _, tracker := range resolvedTrackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}

		review := api.TrackerReview{
			Tracker:      name,
			RuleFailures: cloneRuleFailures(meta.TrackerRuleFailures[name]),
			DupeCheck:    dupeResults[name],
			DryRun:       dryRunByTracker[name],
		}
		if review.DryRun.Tracker == "" {
			review.DryRun.Tracker = name
		}
		if reasons := meta.BlockedTrackers[name]; len(reasons) > 0 {
			review.DryRun.Status = "blocked"
			review.DryRun.Message = "tracker blocked: " + formatBlockedReasons(reasons)
		}
		review.Questionnaire = review.DryRun.Questionnaire

		if group != "" {
			banned, err := bannedChecker.IsBanned(name, group)
			if err != nil {
				return api.UploadReview{}, fmt.Errorf("core: upload review banned group check %s: %w", name, err)
			}
			if banned {
				review.Banned = true
				review.BannedReason = fmt.Sprintf("group %s is banned on %s", group, name)
			}
		}

		reviews = append(reviews, review)
	}

	return api.UploadReview{
		SourcePath: meta.SourcePath,
		Trackers:   reviews,
	}, nil
}

func formatBlockedReasons(reasons []api.TrackerBlockReason) string {
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

func applyRequestToPreparedMeta(meta api.PreparedMetadata, req api.Request, cfg config.Config, logger api.Logger) api.PreparedMetadata {
	return applyRequestToPreparedMetaWithDerivedFields(meta, req, cfg, logger, true)
}

func applyRequestToPreparedMetaBeforeRefresh(meta api.PreparedMetadata, req api.Request, cfg config.Config, logger api.Logger) api.PreparedMetadata {
	return applyRequestToPreparedMetaWithDerivedFields(meta, req, cfg, logger, false)
}

func applyRequestToPreparedMetaWithDerivedFields(meta api.PreparedMetadata, req api.Request, cfg config.Config, logger api.Logger, rebuildDerivedFields bool) api.PreparedMetadata {
	meta = deepCopyPreparedMetadata(meta)
	existingTrackerIDs := cloneStringMap(meta.TrackerIDs)
	existingTrackersRemove := append([]string{}, meta.TrackersRemove...)
	existingMatchedTrackers := append([]string{}, meta.MatchedTrackers...)
	meta.Mode = req.Mode
	meta.Options = req.Options
	meta.Paths = append([]string{}, req.Paths...)
	if req.DescriptionGroups != nil {
		meta.DescriptionGroups = api.CloneDescriptionBuilderGroups(req.DescriptionGroups)
	}
	meta.Trackers = append([]string{}, req.Trackers...)
	meta.TrackersRemove = mergeTrackerRemovals(req.TrackersRemove, existingTrackersRemove)
	meta.TrackersRemove = mergeTrackerRemovals(meta.TrackersRemove, existingMatchedTrackers)
	meta.TrackerIDs = mergeTrackerIDOverrides(existingTrackerIDs, req.TrackerIDOverrides)
	meta.DescriptionOverride = strings.TrimSpace(req.DescriptionOverrideRaw)
	meta.MetadataOverrides = req.MetadataOverrides
	meta.TrackerConfigOverrides = req.TrackerConfigOverrides
	meta.TrackerSiteOverrides = req.TrackerSiteOverrides
	meta.ClientOverrides = req.ClientOverrides
	meta.ImageHostOverrides = req.ImageHostOverrides
	meta.ScreenshotOverrides = req.ScreenshotOverrides
	meta.TorrentOverrides = req.TorrentOverrides
	meta.BlockedTrackers = cloneBlockedTrackers(meta.BlockedTrackers)
	meta.IgnoreTrackerRuleFailures = req.IgnoreTrackerRuleFailures
	meta.ExternalIDOverrides = req.ExternalIDOverrides
	meta.ReleaseNameOverrides = req.ReleaseNameOverrides
	meta.TrackerQuestionnaireAnswers = cloneTrackerQuestionnaireAnswers(req.TrackerQuestionnaireAnswers)
	applyMetadataOverridesToPreparedMeta(&meta)
	if rebuildDerivedFields {
		metadata.ApplyRequestScopedAudioPolicy(&meta, cfg, logger)
		metadata.RebuildReleaseName(&meta, logger)
	}
	applyTorrentOverridesToPreparedMeta(&meta)
	meta.TrackerRuleFailures = filterTrackerRuleFailures(meta.TrackerRuleFailures, req.IgnoreTrackerRuleFailuresFor)
	if req.SkipDupeCheck {
		meta.BlockedTrackers = removeTrackerBlockReason(meta.BlockedTrackers, api.TrackerBlockReasonDupe)
	} else if len(req.IgnoreDupesFor) > 0 {
		meta.BlockedTrackers = removeTrackerBlockReasonForTrackers(meta.BlockedTrackers, api.TrackerBlockReasonDupe, req.IgnoreDupesFor)
	}
	return meta
}

func mergeTrackerIDOverrides(existing map[string]string, overrides map[string]string) map[string]string {
	normalizedExisting := make(map[string]string, len(existing))
	for key, value := range existing {
		trimmedKey := strings.ToLower(strings.TrimSpace(key))
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		normalizedExisting[trimmedKey] = trimmedValue
	}

	merged := cloneStringMap(normalizedExisting)
	if merged == nil {
		merged = make(map[string]string)
	}
	for key, value := range overrides {
		trimmedKey := strings.ToLower(strings.TrimSpace(key))
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		merged[trimmedKey] = trimmedValue
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func (c *Core) applyRequestToCachedPreparedMeta(ctx context.Context, meta api.PreparedMetadata, req api.Request) (api.PreparedMetadata, error) {
	if c.services.Metadata == nil {
		meta = applyRequestToPreparedMeta(meta, req, c.cfg, c.logger)
		return meta, nil
	}
	meta = applyRequestToPreparedMetaBeforeRefresh(meta, req, c.cfg, c.logger)
	refreshed, err := c.services.Metadata.RefreshPreparedMetadata(ctx, meta)
	if err != nil {
		return api.PreparedMetadata{}, fmt.Errorf("core: %w", err)
	}
	refreshed.TrackerRuleFailures = filterTrackerRuleFailures(refreshed.TrackerRuleFailures, req.IgnoreTrackerRuleFailuresFor)
	return refreshed, nil
}

func applyMetadataOverridesToPreparedMeta(meta *api.PreparedMetadata) {
	if meta == nil {
		return
	}

	overrides := meta.MetadataOverrides
	if overrides.Distributor != nil {
		meta.Distributor = strings.TrimSpace(*overrides.Distributor)
	}
	applyOriginalLanguageOverrideToPreparedMeta(meta, overrides.OriginalLanguage)
	if overrides.PersonalRelease != nil {
		meta.PersonalRelease = *overrides.PersonalRelease
	}
	if overrides.Commentary != nil {
		meta.HasCommentary = *overrides.Commentary
	}
	if overrides.WebDV != nil {
		meta.WebDV = *overrides.WebDV
	}
	if overrides.StreamOptimized != nil {
		if *overrides.StreamOptimized {
			meta.StreamOptimized = 1
		} else {
			meta.StreamOptimized = 0
		}
	}
	if overrides.Anime != nil {
		meta.Anime = *overrides.Anime
	}
}

func applyOriginalLanguageOverrideToPreparedMeta(meta *api.PreparedMetadata, language *string) {
	if meta == nil || language == nil {
		return
	}

	trimmed := strings.TrimSpace(*language)
	if trimmed == "" {
		return
	}
	if meta.ExternalMetadata.TMDB != nil {
		meta.ExternalMetadata.TMDB.OriginalLanguage = trimmed
	}
	if meta.ExternalMetadata.IMDB != nil {
		meta.ExternalMetadata.IMDB.OriginalLanguage = trimmed
	}
	if meta.ExternalMetadata.TVDB != nil {
		meta.ExternalMetadata.TVDB.OriginalLanguage = trimmed
	}
	if meta.ExternalMetadata.TVmaze != nil {
		meta.ExternalMetadata.TVmaze.Language = trimmed
	}
}

func applyTorrentOverridesToPreparedMeta(meta *api.PreparedMetadata) {
	if meta == nil {
		return
	}

	if meta.TorrentOverrides.InfoHash != nil {
		meta.InfoHash = strings.ToLower(strings.TrimSpace(*meta.TorrentOverrides.InfoHash))
	}
}

func filterTrackerRuleFailures(failures map[string][]api.RuleFailure, allowTrackers []string) map[string][]api.RuleFailure {
	if len(failures) == 0 {
		return nil
	}
	if len(allowTrackers) == 0 {
		cloned := make(map[string][]api.RuleFailure, len(failures))
		for tracker, trackerFailures := range failures {
			cloned[tracker] = cloneRuleFailures(trackerFailures)
		}
		return cloned
	}

	allowed := make(map[string]struct{}, len(allowTrackers))
	for _, tracker := range allowTrackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name != "" {
			allowed[name] = struct{}{}
		}
	}

	filtered := make(map[string][]api.RuleFailure, len(failures))
	for tracker, trackerFailures := range failures {
		if _, ok := allowed[strings.ToUpper(strings.TrimSpace(tracker))]; ok {
			continue
		}
		filtered[tracker] = cloneRuleFailures(trackerFailures)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func cloneRuleFailures(failures []api.RuleFailure) []api.RuleFailure {
	if len(failures) == 0 {
		return nil
	}
	cloned := make([]api.RuleFailure, len(failures))
	copy(cloned, failures)
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

func mapDupeResults(results []api.DupeCheckResult) map[string]api.DupeCheckResult {
	mapped := make(map[string]api.DupeCheckResult, len(results))
	for _, result := range results {
		trackers := splitTrackerLabel(result.Tracker)
		if len(trackers) == 0 {
			name := strings.ToUpper(strings.TrimSpace(result.Tracker))
			if name != "" {
				mapped[name] = result
			}
			continue
		}
		for _, tracker := range trackers {
			copyResult := result
			copyResult.Tracker = tracker
			mapped[tracker] = copyResult
		}
	}
	return mapped
}

func cloneTrackerQuestionnaireAnswers(input map[string]map[string]string) map[string]map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]map[string]string, len(input))
	for tracker, values := range input {
		if len(values) == 0 {
			cloned[tracker] = map[string]string{}
			continue
		}
		inner := make(map[string]string, len(values))
		maps.Copy(inner, values)
		cloned[tracker] = inner
	}
	return cloned
}

func applyDupeSummaryToPreparedMeta(meta *api.PreparedMetadata, summary api.DupeCheckSummary) {
	if meta == nil {
		return
	}

	blocked := removeTrackerBlockReason(cloneBlockedTrackers(meta.BlockedTrackers), api.TrackerBlockReasonDupe)
	for _, result := range summary.Results {
		if !dupeResultBlocksTracker(result) {
			continue
		}

		trackers := splitTrackerLabel(result.Tracker)
		if len(trackers) == 0 {
			trackers = []string{result.Tracker}
		}
		for _, tracker := range trackers {
			blocked = addTrackerBlockReason(blocked, tracker, api.TrackerBlockReasonDupe)
		}
	}
	meta.BlockedTrackers = blocked
}

func dupeResultBlocksTracker(result api.DupeCheckResult) bool {
	if result.HasDupes || result.Skipped {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(result.Status), "failed") {
		return true
	}
	return strings.TrimSpace(result.Error) != ""
}

func cloneBlockedTrackers(input map[string][]api.TrackerBlockReason) map[string][]api.TrackerBlockReason {
	if len(input) == 0 {
		return nil
	}

	cloned := make(map[string][]api.TrackerBlockReason, len(input))
	for tracker, reasons := range input {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		if len(reasons) == 0 {
			cloned[name] = nil
			continue
		}
		clonedReasons := make([]api.TrackerBlockReason, len(reasons))
		copy(clonedReasons, reasons)
		cloned[name] = clonedReasons
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func addTrackerBlockReason(blocked map[string][]api.TrackerBlockReason, tracker string, reason api.TrackerBlockReason) map[string][]api.TrackerBlockReason {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if name == "" || strings.TrimSpace(string(reason)) == "" {
		return blocked
	}
	if blocked == nil {
		blocked = make(map[string][]api.TrackerBlockReason)
	}
	if slices.Contains(blocked[name], reason) {
		return blocked
	}
	blocked[name] = append(blocked[name], reason)
	return blocked
}

func removeTrackerBlockReason(blocked map[string][]api.TrackerBlockReason, reason api.TrackerBlockReason) map[string][]api.TrackerBlockReason {
	if len(blocked) == 0 {
		return nil
	}

	filtered := make(map[string][]api.TrackerBlockReason, len(blocked))
	for tracker, reasons := range blocked {
		if len(reasons) == 0 {
			filtered[tracker] = nil
			continue
		}

		kept := make([]api.TrackerBlockReason, 0, len(reasons))
		for _, existing := range reasons {
			if existing != reason {
				kept = append(kept, existing)
			}
		}
		if len(kept) > 0 {
			filtered[tracker] = kept
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func removeTrackerBlockReasonForTrackers(blocked map[string][]api.TrackerBlockReason, reason api.TrackerBlockReason, trackers []string) map[string][]api.TrackerBlockReason {
	if len(blocked) == 0 || len(trackers) == 0 {
		return blocked
	}

	filtered := cloneBlockedTrackers(blocked)
	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}

		reasons := filtered[name]
		if len(reasons) == 0 {
			continue
		}

		kept := make([]api.TrackerBlockReason, 0, len(reasons))
		for _, existing := range reasons {
			if existing != reason {
				kept = append(kept, existing)
			}
		}
		if len(kept) == 0 {
			delete(filtered, name)
			continue
		}
		filtered[name] = kept
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}
