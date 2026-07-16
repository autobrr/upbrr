// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

// BuildUploadReview returns dupe, rule, dry-run, and banned-group review data
// for one prepared source path, committing WebUI cache updates only after review work succeeds.
// Partial WebUI reviews preserve unreviewed tracker cache state and can commit from
// a request-refreshed cache entry only when that entry matches the current request.
func (c *Core) BuildUploadReview(ctx context.Context, req api.Request) (api.UploadReview, error) {
	return c.upload.buildReview(ctx, req)
}

// ReviewAcceptedUpload reviews one exact canonical prepared generation and
// returns the outcomes required for later execution.
func (c *Core) ReviewAcceptedUpload(ctx context.Context, input api.UploadReviewInput) (api.ReviewedUpload, error) {
	reviewed, err := c.upload.reviewAccepted(ctx, input)
	return reviewed, classifyOperationError(api.OperationKindUploadReview, err)
}

// reviewAccepted refreshes operation-local evidence and produces the complete
// outcome required to execute one accepted upload review.
func (m *uploadModule) reviewAccepted(ctx context.Context, input api.UploadReviewInput) (api.ReviewedUpload, error) {
	if m == nil || m.preparedFacts == nil {
		return api.ReviewedUpload{}, errors.New("core: canonical preparation is not configured")
	}
	if m.dupes == nil || m.torrents == nil || m.trackers == nil {
		return api.ReviewedUpload{}, errors.New("core: upload review services are not configured")
	}
	subject, err := m.preparedFacts.ResolveUploadSubject(ctx, input)
	if err != nil {
		return api.ReviewedUpload{}, fmt.Errorf("core: resolve upload review subject: %w", err)
	}
	resolvedTrackers := trackerspkg.ResolveExplicitTrackersWithRegistry(input.Trackers, m.logger, m.registry)
	if len(resolvedTrackers) == 0 {
		return api.ReviewedUpload{}, noEligibleTrackersError(api.OperationKindUploadReview)
	}
	subject.Trackers = append([]string(nil), resolvedTrackers...)
	if err := m.hydrateCanonicalTrackerState(ctx, &subject); err != nil {
		return api.ReviewedUpload{}, err
	}
	if m.discovery != nil {
		evidence, discoveryErr := m.discovery.Discover(ctx, clientdiscovery.SearchInput{
			SourcePath: subject.SourcePath,
			FileList:   subject.FileList,
			DiscType:   subject.DiscType,
			Policy: api.ClientSearchPolicy{
				Skip:   input.Options.SkipAutoTorrent,
				Client: input.ClientOverrides.Client,
			},
			ForceRecheck: input.ClientOverrides.ForceRecheck,
			Debug:        input.Options.Debug,
		})
		if discoveryErr != nil {
			return api.ReviewedUpload{}, fmt.Errorf("core: discover upload-review client state: %w", discoveryErr)
		}
		applyClientSearchToUploadSubject(&subject, evidence)
	}
	if len(input.TrackerIDOverrides) > 0 {
		if subject.TrackerIDs == nil {
			subject.TrackerIDs = make(map[string]string)
		}
		for key, value := range input.TrackerIDOverrides {
			key = strings.ToLower(strings.TrimSpace(key))
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				subject.TrackerIDs[key] = value
			}
		}
	}

	policy := api.UploadReviewOutcome{ResolvedTrackers: append([]string(nil), resolvedTrackers...)}
	if m.policy != nil {
		policy, err = m.policy.EvaluateUploadPolicy(ctx, subject, resolvedTrackers)
		if err != nil {
			return api.ReviewedUpload{}, fmt.Errorf("core: upload policy: %w", err)
		}
	} else {
		policy.TrackerRuleFailures = evaluateSubjectRules(ctx, m.registry, subject, resolvedTrackers, m.logger)
	}
	policy.TrackerRuleFailures = filterTrackerRuleFailures(policy.TrackerRuleFailures, input.IgnoreRuleFailuresFor)

	duplicate := duplicateSubjectFromUpload(subject)
	duplicate.MatchedTrackers = append([]string(nil), subject.MatchedTrackers...)
	duplicate.BlockedTrackers = cloneBlockedTrackers(policy.BlockedTrackers)
	duplicate.TrackerRuleFailures = cloneTrackerRuleFailures(policy.TrackerRuleFailures)
	summary, assessment, err := checkDuplicateAssessment(ctx, m.dupes, duplicate, resolvedTrackers, dupechecking.CheckOptions{
		SkipRemote: input.SkipDuplicateCheck,
	})
	if err != nil {
		return api.ReviewedUpload{}, fmt.Errorf("core: %w", err)
	}
	if len(input.IgnoreDupesFor) > 0 {
		assessment, err = assessment.Authorize(duplicate, m.cfg, input.IgnoreDupesFor)
		if err != nil {
			return api.ReviewedUpload{}, fmt.Errorf("core: duplicate authorization: %w", err)
		}
	}
	assessment.Apply(&duplicate)

	outcome := api.UploadReviewOutcome{
		ResolvedTrackers:    append([]string(nil), resolvedTrackers...),
		MatchedTrackers:     append([]string(nil), subject.MatchedTrackers...),
		BlockedTrackers:     cloneBlockedTrackers(duplicate.BlockedTrackers),
		TrackerRuleFailures: cloneTrackerRuleFailures(policy.TrackerRuleFailures),
		CrossSeedTorrents:   append([]api.UploadedTorrent(nil), duplicate.CrossSeedTorrents...),
	}
	subject.MatchedTrackers = append([]string(nil), outcome.MatchedTrackers...)
	subject.BlockedTrackers = cloneBlockedTrackers(outcome.BlockedTrackers)
	subject.TrackerRuleFailures = cloneTrackerRuleFailures(outcome.TrackerRuleFailures)
	subject.CrossSeedTorrents = append([]api.UploadedTorrent(nil), outcome.CrossSeedTorrents...)
	if m.resolveSubjectGroups == nil {
		return api.ReviewedUpload{}, errors.New("core: subject description resolver not configured")
	}
	subject.DescriptionGroups, err = m.resolveSubjectGroups(ctx, subject, input)
	if err != nil {
		return api.ReviewedUpload{}, err
	}
	torrent, err := m.torrents.Create(ctx, torrentSubjectFromUpload(subject, input))
	if err != nil {
		return api.ReviewedUpload{}, fmt.Errorf("core: %w", err)
	}
	subject.TorrentPath = torrent.Path
	dryRuns, err := m.trackers.BuildUploadDryRun(ctx, subject, resolvedTrackers)
	if err != nil {
		return api.ReviewedUpload{}, fmt.Errorf("core: %w", err)
	}
	annotateDryRunSubjectReleaseNames(subject, dryRuns)
	dryRunByTracker := make(map[string]api.TrackerDryRunEntry, len(dryRuns))
	for _, entry := range dryRuns {
		if name := strings.ToUpper(strings.TrimSpace(entry.Tracker)); name != "" {
			dryRunByTracker[name] = entry
		}
	}
	dupeResults := mapDupeResults(summary.Results)
	reviews := make([]api.TrackerReview, 0, len(resolvedTrackers))
	eligibilityAssessments := make([]api.TrackerEligibilityAssessment, 0, len(resolvedTrackers))
	for _, tracker := range resolvedTrackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		review := api.TrackerReview{
			Tracker:      name,
			RuleFailures: cloneRuleFailures(outcome.TrackerRuleFailures[name]),
			DupeCheck:    dupeResults[name],
			DryRun:       dryRunByTracker[name],
		}
		if review.DryRun.Tracker == "" {
			review.DryRun.Tracker = name
		}
		review.Banned = review.DryRun.Banned
		review.BannedReason = review.DryRun.BannedReason
		if reasons := outcome.BlockedTrackers[name]; len(reasons) > 0 {
			review.DryRun.Status = "blocked"
			review.DryRun.Message = "tracker blocked: " + formatBlockedReasons(reasons)
		} else if checkError := strings.TrimSpace(review.DryRun.BannedCheckError); checkError != "" {
			review.DryRun.Status = "blocked"
			review.DryRun.Message = checkError
		}
		review.Questionnaire = review.DryRun.Questionnaire
		reviews = append(reviews, review)
		eligibilityAssessments = append(eligibilityAssessments, api.TrackerEligibilityAssessment{
			Tracker:      name,
			Duplicate:    review.DupeCheck,
			RuleFailures: cloneRuleFailures(review.RuleFailures),
			PolicyBlocks: append([]api.TrackerBlockReason(nil), outcome.BlockedTrackers[name]...),
			AuthRequired: review.DupeCheck.SkipCode == dupeSkipCodeTrackerAuthNotReady,
			Banned:       review.Banned,
			BannedReason: review.BannedReason,
			Choices: api.TrackerReviewChoices{
				IgnoreDuplicate:    containsNormalizedTracker(input.IgnoreDupesFor, name),
				IgnoreRuleFailures: containsNormalizedTracker(input.IgnoreRuleFailuresFor, name),
			},
		})
	}
	eligibility := buildTrackerEligibility(api.TrackerEligibilityInput{
		Release:          input.Release,
		SelectedTrackers: resolvedTrackers,
		Assessments:      eligibilityAssessments,
	})
	if len(eligibility.EligibleTrackers) == 0 {
		return api.ReviewedUpload{}, noEligibleTrackersError(api.OperationKindUploadReview)
	}
	outcome.Eligibility = eligibility
	return api.ReviewedUpload{
		Review: api.UploadReview{
			SourcePath:  subject.SourcePath,
			Trackers:    reviews,
			Eligibility: eligibility,
		},
		Outcome: outcome,
	}, nil
}

func duplicateSubjectFromUpload(subject api.UploadSubject) api.DuplicateSubject {
	return api.DuplicateSubject{
		SourcePath:           subject.SourcePath,
		SourceSize:           subject.SourceSize,
		VideoPath:            subject.VideoPath,
		FileList:             append([]string(nil), subject.FileList...),
		Filename:             subject.Filename,
		SceneName:            subject.SceneName,
		ReleaseName:          subject.ReleaseName,
		Release:              subject.Release,
		ReleaseNameOverrides: subject.ReleaseNameOverrides,
		Identity:             subject.Identity,
		ProviderMetadata:     subject.ProviderMetadata,
		TrackerIDs:           cloneStringMap(subject.TrackerIDs),
		DiscType:             subject.DiscType,
		Type:                 subject.Type,
		Source:               subject.Source,
		Tag:                  subject.Tag,
		HDR:                  subject.HDR,
		UHD:                  subject.UHD,
		VideoEncode:          subject.VideoEncode,
		VideoCodec:           subject.VideoCodec,
		HasEncodeSettings:    subject.HasEncodeSettings,
		SeasonInt:            subject.SeasonInt,
		EpisodeInt:           subject.EpisodeInt,
		SeasonStr:            subject.SeasonStr,
		EpisodeStr:           subject.EpisodeStr,
		DailyEpisodeDate:     subject.DailyEpisodeDate,
		TVPack:               subject.TVPack,
		Anime:                subject.Anime,
	}
}

// applyClientSearchToUploadSubject refreshes reusable torrent and tracker state
// from one current client snapshot before explicit review overrides are applied.
func applyClientSearchToUploadSubject(subject *api.UploadSubject, search clientdiscovery.Evidence) {
	if subject == nil {
		return
	}
	if value := strings.TrimSpace(search.InfoHash); value != "" {
		subject.InfoHash = value
	}
	if value := strings.TrimSpace(search.TorrentPath); value != "" {
		subject.ClientTorrentPath = value
	}
	if len(search.TrackerIDs) > 0 {
		if subject.TrackerIDs == nil {
			subject.TrackerIDs = make(map[string]string)
		}
		maps.Copy(subject.TrackerIDs, search.TrackerIDs)
	}
	subject.MatchedTrackers = normalizeReviewedTrackers(search.MatchedTrackers)
}

func evaluateSubjectRules(
	ctx context.Context,
	registry *trackerspkg.Registry,
	subject api.UploadSubject,
	trackerNames []string,
	logger api.Logger,
) map[string][]api.RuleFailure {
	result := make(map[string][]api.RuleFailure)
	for _, tracker := range trackerNames {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if failures := trackerspkg.EvaluateRulesWithRegistry(ctx, registry, name, api.NewRuleSubject(subject), logger); len(failures) > 0 {
			result[name] = append([]api.RuleFailure(nil), failures...)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (m *uploadModule) buildReview(ctx context.Context, req api.Request) (api.UploadReview, error) {
	return m.buildCanonicalRequestReview(ctx, req)
}

func (m *uploadModule) buildCanonicalRequestReview(ctx context.Context, req api.Request) (api.UploadReview, error) {
	req = normalizeExecutionRequest(req)
	req = m.expandEntrypointTrackerDefaults(req)
	resolved, err := m.resolveDescriptionOverride(ctx, req)
	if err != nil {
		return api.UploadReview{}, err
	}
	prepareInput, err := api.MapPreparationRequest(resolved, api.PreparationIntentUpload)
	if err != nil {
		return api.UploadReview{}, fmt.Errorf("core: map upload-review preparation request: %w", err)
	}
	prepared, err := m.preparedFacts.Prepare(ctx, prepareInput)
	if err != nil {
		return api.UploadReview{}, fmt.Errorf("core: prepare upload review: %w", err)
	}
	reviewInput := uploadReviewInputFromRequest(resolved, api.ReleaseRef{
		SourcePath: prepared.Release.Source.SourcePath,
		Generation: prepared.Release.Generation,
	})
	reviewed, err := m.reviewAccepted(ctx, reviewInput)
	if err != nil {
		return api.UploadReview{}, err
	}
	return reviewed.Review, nil
}

// resolveUploadReviewTrackers applies WebUI replacement semantics while allowing
// non-debug, non-WebUI review flows to include or fall back to configured defaults
// unless site upload pins one tracker.

// resolveUploadReviewPTBRMetadata refreshes localized TMDB metadata after review
// tracker resolution when selected or default tracker targets require pt-BR data.

// uploadReviewNeedsPTBRMetadata reports whether a review dry-run needs a pt-BR refresh.

// hasPTBRTracker reports whether any tracker consumes localized pt-BR TMDB data.

// hasLocalizedPTBR reports whether TMDB metadata already contains complete pt-BR localized data.

// hasKnownTMDBID reports whether metadata has a source-current TMDB ID available for refresh.

// localizedPTBRComplete reports whether review metadata has the pt-BR title,
// genre, and overview fields needed to avoid another localized refresh for
// selected upload trackers.

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

// duplicateTrackerStateForRequest returns cached duplicate removal state after
// applying the request's dupe bypass semantics. Ignored or skipped matched
// trackers are removed from suppression state before later tracker resolution.

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
			warnings := api.WarningRuleFailures(trackerFailures)
			if len(warnings) > 0 {
				filtered[tracker] = warnings
			}
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
		name := strings.ToUpper(strings.TrimSpace(result.Tracker))
		if name != "" {
			mapped[name] = result
		}
	}
	return mapped
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

// mergeUploadReviewCacheMeta preserves unreviewed WebUI cache state while
// replacing refreshed tracker-scoped review state for the trackers reviewed by
// this request, including matched-tracker and removal state.

// mergeReviewedTrackerBlocks keeps base tracker blocks for unreviewed trackers
// and replaces only reviewed trackers with refreshed block results.

// mergeReviewedTrackerCrossSeeds keeps base cross-seed torrents for unreviewed
// trackers and replaces only reviewed trackers with updated cross-seed results.

// mergeReviewedTrackerRuleFailures keeps base rule failures for unreviewed
// trackers and replaces only reviewed trackers with refreshed failures.

// removeReviewedTrackerMapEntries deletes reviewed tracker entries using
// case-insensitive tracker names so stale mixed-case cache keys cannot survive.
