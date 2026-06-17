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

// BuildUploadReview returns dupe, rule, dry-run, and banned-group review data
// for one prepared source path, committing GUI cache updates only after review work succeeds.
// Partial GUI reviews preserve unreviewed tracker cache state and can commit from
// a request-refreshed cache entry only when that entry matches the current request.
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
		baseMeta   api.PreparedMetadata
		baseMetaOK bool
		meta       api.PreparedMetadata
		ok         bool
	)
	if req.Mode == api.ModeGUI {
		entry, _, found := c.lookupGUICachedMetaEntry(singleReq, uniquePaths[0])
		if found {
			ok = true
			entryMatchesRequest := entry.requestRefreshed && cachedPreparedMetaMatchesRequest(entry.meta, singleReq, uniquePaths[0])
			baseMetaOK = !entry.requestRefreshed || entryMatchesRequest
			if baseMetaOK {
				baseMeta = deepCopyPreparedMetadata(entry.meta)
			}
			if entryMatchesRequest {
				meta = deepCopyPreparedMetadata(entry.meta)
			} else {
				meta, err = c.applyRequestToCachedPreparedMeta(ctx, entry.meta, singleReq)
				if err != nil {
					return api.UploadReview{}, err
				}
			}
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

	resolvedTrackers, explicitEmpty := c.resolveUploadReviewTrackers(singleReq, meta)
	if explicitEmpty {
		return api.UploadReview{
			SourcePath: meta.SourcePath,
			Trackers:   []api.TrackerReview{},
		}, nil
	}

	dupeResults := make(map[string]api.DupeCheckResult)
	if singleReq.SkipDupeCheck {
		meta.BlockedTrackers = removeTrackerBlockReason(cloneBlockedTrackers(meta.BlockedTrackers), api.TrackerBlockReasonDupe)
		meta.CrossSeedTorrents = nil
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
		meta.CrossSeedTorrents = removeCrossSeedTorrentsForTrackers(meta.CrossSeedTorrents, singleReq.IgnoreDupesFor)
		dupeResults = mapDupeResults(summary.Results)
	}
	var cacheCommit func()
	if req.Mode == api.ModeGUI && cacheableGUIPreparedMetaRequest(singleReq) {
		if len(singleReq.Trackers) > 0 {
			if baseMetaOK {
				cacheMeta := mergeUploadReviewCacheMeta(baseMeta, meta, resolvedTrackers)
				cacheCommit = func() {
					c.storeDupeCache(uniquePaths[0], signature, cacheMeta)
				}
			}
		} else {
			cacheMeta := deepCopyPreparedMetadata(meta)
			cacheCommit = func() {
				c.storeRefreshedDupeCache(uniquePaths[0], signature, cacheMeta)
			}
		}
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

	if cacheCommit != nil {
		cacheCommit()
	}

	return api.UploadReview{
		SourcePath: meta.SourcePath,
		Trackers:   reviews,
	}, nil
}

// resolveUploadReviewTrackers applies GUI replacement semantics while allowing
// non-GUI review flows to include or fall back to configured defaults unless
// site upload pins one tracker.
func (c *Core) resolveUploadReviewTrackers(req api.Request, meta api.PreparedMetadata) ([]string, bool) {
	includeDefaults := req.Mode != api.ModeGUI && strings.TrimSpace(req.Execution.SiteUploadTracker) == ""
	remove := meta.TrackersRemove
	if req.Mode == api.ModeGUI && len(req.Trackers) > 0 && len(meta.MatchedTrackers) > 0 {
		reviewedMatchedTrackers := make([]string, 0, len(req.Trackers))
		for _, tracker := range req.Trackers {
			if containsTrackerName(meta.MatchedTrackers, tracker) {
				reviewedMatchedTrackers = append(reviewedMatchedTrackers, tracker)
			}
		}
		remove = removeReviewedTrackerNames(remove, reviewedMatchedTrackers)
		remove = mergeTrackerRemovals(remove, req.TrackersRemove)
	}
	return resolveTrackersPreservingExplicitEmpty(c.cfg, req.Trackers, remove, c.logger, includeDefaults, includeDefaults)
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
	existingTrackersRemove, existingMatchedTrackers := duplicateTrackerStateForRequest(meta, req)
	meta.Mode = req.Mode
	meta.Options = req.Options
	meta.Paths = append([]string{}, req.Paths...)
	if req.DescriptionGroups != nil {
		meta.DescriptionGroups = api.CloneDescriptionBuilderGroups(req.DescriptionGroups)
	}
	meta.Trackers = append([]string{}, req.Trackers...)
	meta.MatchedTrackers = existingMatchedTrackers
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
		meta.CrossSeedTorrents = nil
	} else if len(req.IgnoreDupesFor) > 0 {
		meta.BlockedTrackers = removeTrackerBlockReasonForTrackers(meta.BlockedTrackers, api.TrackerBlockReasonDupe, req.IgnoreDupesFor)
		meta.CrossSeedTorrents = removeCrossSeedTorrentsForTrackers(meta.CrossSeedTorrents, req.IgnoreDupesFor)
	}
	return meta
}

// duplicateTrackerStateForRequest returns cached duplicate removal state after
// applying the request's dupe bypass semantics. Ignored or skipped matched
// trackers are removed from suppression state before later tracker resolution.
func duplicateTrackerStateForRequest(meta api.PreparedMetadata, req api.Request) ([]string, []string) {
	remove := append([]string(nil), meta.TrackersRemove...)
	matched := append([]string(nil), meta.MatchedTrackers...)
	if req.SkipDupeCheck {
		return removeReviewedTrackerNames(remove, matched), nil
	}
	if len(req.IgnoreDupesFor) > 0 {
		return removeReviewedTrackerNames(remove, req.IgnoreDupesFor), removeReviewedTrackerNames(matched, req.IgnoreDupesFor)
	}
	return remove, matched
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
	crossSeeds := make([]api.UploadedTorrent, 0)
	seenCrossSeeds := make(map[string]struct{})
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
		if !result.HasDupes {
			continue
		}
		downloadURL := strings.TrimSpace(result.Match.MatchedDownload)
		if downloadURL == "" {
			continue
		}
		for _, tracker := range trackers {
			name := strings.ToUpper(strings.TrimSpace(tracker))
			if name == "" {
				continue
			}
			key := name + "\x00" + downloadURL
			if _, exists := seenCrossSeeds[key]; exists {
				continue
			}
			seenCrossSeeds[key] = struct{}{}
			crossSeeds = append(crossSeeds, api.UploadedTorrent{
				Tracker:     name,
				TorrentID:   strings.TrimSpace(result.Match.MatchedID),
				DownloadURL: downloadURL,
				TorrentURL:  strings.TrimSpace(result.Match.MatchedLink),
			})
		}
	}
	meta.BlockedTrackers = blocked
	meta.CrossSeedTorrents = crossSeeds
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

func removeCrossSeedTorrentsForTrackers(torrents []api.UploadedTorrent, trackers []string) []api.UploadedTorrent {
	if len(torrents) == 0 || len(trackers) == 0 {
		return torrents
	}
	ignored := make(map[string]struct{}, len(trackers))
	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name != "" {
			ignored[name] = struct{}{}
		}
	}
	if len(ignored) == 0 {
		return torrents
	}
	filtered := make([]api.UploadedTorrent, 0, len(torrents))
	for _, torrent := range torrents {
		name := strings.ToUpper(strings.TrimSpace(torrent.Tracker))
		if _, skip := ignored[name]; skip {
			continue
		}
		filtered = append(filtered, torrent)
	}
	return filtered
}

// mergeUploadReviewCacheMeta preserves unreviewed GUI cache state while
// replacing refreshed tracker-scoped review state for the trackers reviewed by
// this request, including matched-tracker and removal state.
func mergeUploadReviewCacheMeta(base api.PreparedMetadata, updated api.PreparedMetadata, reviewedTrackers []string) api.PreparedMetadata {
	merged := deepCopyPreparedMetadata(base)
	merged.BlockedTrackers = mergeReviewedTrackerBlocks(base.BlockedTrackers, updated.BlockedTrackers, reviewedTrackers)
	merged.CrossSeedTorrents = mergeReviewedTrackerCrossSeeds(base.CrossSeedTorrents, updated.CrossSeedTorrents, reviewedTrackers)
	merged.TrackerRuleFailures = mergeReviewedTrackerRuleFailures(base.TrackerRuleFailures, updated.TrackerRuleFailures, reviewedTrackers)
	merged.TrackersRemove = mergeReviewedTrackerNames(base.TrackersRemove, updated.TrackersRemove, reviewedTrackers)
	merged.MatchedTrackers = mergeReviewedTrackerNames(base.MatchedTrackers, updated.MatchedTrackers, reviewedTrackers)
	return merged
}

// mergeReviewedTrackerBlocks keeps base tracker blocks for unreviewed trackers
// and replaces only reviewed trackers with refreshed block results.
func mergeReviewedTrackerBlocks(
	base map[string][]api.TrackerBlockReason,
	updated map[string][]api.TrackerBlockReason,
	reviewedTrackers []string,
) map[string][]api.TrackerBlockReason {
	merged := removeReviewedTrackerMapEntries(cloneBlockedTrackers(base), reviewedTrackers)
	for _, tracker := range reviewedTrackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		reasons := append([]api.TrackerBlockReason(nil), lookupTrackerMapValue(updated, name)...)
		if len(reasons) == 0 {
			continue
		}
		if merged == nil {
			merged = make(map[string][]api.TrackerBlockReason)
		}
		merged[name] = reasons
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// mergeReviewedTrackerCrossSeeds keeps base cross-seed torrents for unreviewed
// trackers and replaces only reviewed trackers with updated cross-seed results.
func mergeReviewedTrackerCrossSeeds(
	base []api.UploadedTorrent,
	updated []api.UploadedTorrent,
	reviewedTrackers []string,
) []api.UploadedTorrent {
	merged := removeCrossSeedTorrentsForTrackers(base, reviewedTrackers)
	for _, torrent := range updated {
		name := strings.ToUpper(strings.TrimSpace(torrent.Tracker))
		if name == "" || !containsTrackerName(reviewedTrackers, name) {
			continue
		}
		merged = append(merged, torrent)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// mergeReviewedTrackerRuleFailures keeps base rule failures for unreviewed
// trackers and replaces only reviewed trackers with refreshed failures.
func mergeReviewedTrackerRuleFailures(
	base map[string][]api.RuleFailure,
	updated map[string][]api.RuleFailure,
	reviewedTrackers []string,
) map[string][]api.RuleFailure {
	merged := removeReviewedTrackerMapEntries(deepCopyTrackerRuleFailures(base), reviewedTrackers)
	for _, tracker := range reviewedTrackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		failures := cloneRuleFailures(lookupTrackerMapValue(updated, name))
		if len(failures) == 0 {
			continue
		}
		if merged == nil {
			merged = make(map[string][]api.RuleFailure)
		}
		merged[name] = failures
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func mergeReviewedTrackerNames(base []string, updated []string, reviewedTrackers []string) []string {
	merged := removeReviewedTrackerNames(base, reviewedTrackers)
	for _, tracker := range updated {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" || !containsTrackerName(reviewedTrackers, name) || containsTrackerName(merged, name) {
			continue
		}
		merged = append(merged, name)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func removeReviewedTrackerNames(trackers []string, reviewedTrackers []string) []string {
	if len(trackers) == 0 {
		return nil
	}
	if len(reviewedTrackers) == 0 {
		return append([]string(nil), trackers...)
	}
	filtered := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		if strings.TrimSpace(tracker) == "" || containsTrackerName(reviewedTrackers, tracker) {
			continue
		}
		filtered = append(filtered, tracker)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// removeReviewedTrackerMapEntries deletes reviewed tracker entries using
// case-insensitive tracker names so stale mixed-case cache keys cannot survive.
func removeReviewedTrackerMapEntries[T any](input map[string]T, reviewedTrackers []string) map[string]T {
	if len(input) == 0 {
		return nil
	}
	reviewed := make(map[string]struct{}, len(reviewedTrackers))
	for _, tracker := range reviewedTrackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		reviewed[name] = struct{}{}
	}
	if len(reviewed) == 0 {
		return input
	}
	for key := range input {
		name := strings.ToUpper(strings.TrimSpace(key))
		if _, ok := reviewed[name]; ok {
			delete(input, key)
		}
	}
	if len(input) == 0 {
		return nil
	}
	return input
}

func containsTrackerName(trackers []string, target string) bool {
	for _, tracker := range trackers {
		if strings.EqualFold(strings.TrimSpace(tracker), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func lookupTrackerMapValue[T any](input map[string]T, tracker string) T {
	var zero T
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if name == "" {
		return zero
	}
	for key, value := range input {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return value
		}
	}
	return zero
}
