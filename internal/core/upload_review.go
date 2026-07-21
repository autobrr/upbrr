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

	"github.com/autobrr/upbrr/internal/logging"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

// BuildUploadReview prepares req.SourcePath, refreshes tracker policy and remote
// duplicate evidence, builds non-submitting tracker payloads, and persists the
// resulting rule decisions. It does not upload to trackers.
func (c *Core) BuildUploadReview(ctx context.Context, req api.Request) (api.UploadReview, error) {
	return c.upload.buildReview(ctx, req)
}

// ReviewAcceptedUpload reviews one exact canonical prepared generation and
// returns the outcomes required for later execution.
func (c *Core) ReviewAcceptedUpload(ctx context.Context, input api.UploadReviewInput) (api.ReviewedUpload, error) {
	reviewed, err := c.upload.reviewAccepted(ctx, input)
	return reviewed, classifyOperationError(api.OperationKindUploadReview, err)
}

// reviewAccepted refreshes operation authority and produces the complete
// outcome required to execute one accepted upload review.
func (m *uploadModule) reviewAccepted(ctx context.Context, input api.UploadReviewInput) (api.ReviewedUpload, error) {
	evidence, err := m.refreshUploadReviewAuthority(ctx, input)
	if err != nil {
		return api.ReviewedUpload{}, err
	}
	preview, err := m.buildTrackerPayloadPreview(ctx, input, trackerspkg.PreparationIntentUploadReview, evidence)
	if err != nil {
		return api.ReviewedUpload{}, err
	}
	if len(preview.outcome.Eligibility.EligibleTrackers) == 0 {
		return api.ReviewedUpload{}, noEligibleTrackersError(api.OperationKindUploadReview)
	}
	return api.ReviewedUpload{Review: preview.review, Outcome: preview.outcome}, nil
}

// trackerPayloadPreview contains the payload projection shared by upload review
// and explicit dry-run execution. It retains the base torrent only long enough
// for optional dry-run client injection.
type trackerPayloadPreview struct {
	subject api.UploadSubject
	review  api.UploadReview
	outcome api.UploadReviewOutcome
	torrent api.TorrentResult
}

// trackerPayloadEvidence contains accepted authority results consumed by
// payload construction. Building payloads never refreshes client or duplicate
// evidence.
type trackerPayloadEvidence struct {
	subject          api.UploadSubject
	resolvedTrackers []string
	outcome          api.UploadReviewOutcome
	duplicateResults []api.DupeCheckResult
}

// refreshUploadReviewAuthority refreshes policy and remote duplicate state
// immediately before final upload review. Client evidence is generation-owned.
func (m *uploadModule) refreshUploadReviewAuthority(
	ctx context.Context,
	input api.UploadReviewInput,
) (trackerPayloadEvidence, error) {
	if m == nil || m.dupes == nil {
		return trackerPayloadEvidence{}, errors.New("core: upload review duplicate service is not configured")
	}
	subject, resolvedTrackers, err := m.resolveTrackerPayloadSubject(ctx, input, api.OperationKindUploadReview)
	if err != nil {
		return trackerPayloadEvidence{}, err
	}
	applyTrackerIDOverrides(&subject, input.TrackerIDOverrides)

	policy, err := m.evaluateTrackerPayloadPolicy(ctx, subject, resolvedTrackers)
	if err != nil {
		return trackerPayloadEvidence{}, err
	}

	if err := trackerspkg.ValidateRuleAuthorizations(resolvedTrackers, policy.TrackerRuleFailures, input.RuleAuthorizations); err != nil {
		return trackerPayloadEvidence{}, fmt.Errorf("core: rule authorization: %w", err)
	}
	duplicate := duplicateSubjectFromUpload(subject)
	duplicate.MatchedTrackers = append([]string(nil), subject.MatchedTrackers...)
	duplicate.BlockedTrackers = cloneBlockedTrackers(policy.BlockedTrackers)
	summary, assessment, err := checkDuplicateAssessment(ctx, m.dupes, duplicate, resolvedTrackers, dupechecking.CheckOptions{
		SkipRemote: input.SkipDuplicateCheck,
	})
	if err != nil {
		return trackerPayloadEvidence{}, fmt.Errorf("core: %w", err)
	}
	if len(input.IgnoreDupesFor) > 0 {
		assessment, err = assessment.Authorize(duplicate, m.cfg, input.IgnoreDupesFor)
		if err != nil {
			return trackerPayloadEvidence{}, fmt.Errorf("core: duplicate authorization: %w", err)
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
	return trackerPayloadEvidence{
		subject:          subject,
		resolvedTrackers: append([]string(nil), resolvedTrackers...),
		outcome:          outcome,
		duplicateResults: api.CloneDupeCheckResults(summary.Results),
	}, nil
}

// acceptedDryRunEvidence resolves exact prepared facts and combines them with
// caller-accepted duplicate results without client discovery or another remote
// duplicate check.
func (m *uploadModule) acceptedDryRunEvidence(ctx context.Context, plan api.TrackerDryRunPlan) (trackerPayloadEvidence, error) {
	input := trackerDryRunReviewInput(plan.Input)
	subject, resolvedTrackers, err := m.resolveTrackerPayloadSubject(ctx, input, api.OperationKindDryRun)
	if err != nil {
		return trackerPayloadEvidence{}, err
	}
	results, err := validateAcceptedDuplicateEvidence(plan.Duplicate, plan.Input.Release, resolvedTrackers)
	if err != nil {
		return trackerPayloadEvidence{}, err
	}
	applyTrackerIDOverrides(&subject, input.TrackerIDOverrides)
	policy, err := m.evaluateTrackerPayloadPolicy(ctx, subject, resolvedTrackers)
	if err != nil {
		return trackerPayloadEvidence{}, err
	}
	outcome := api.UploadReviewOutcome{
		ResolvedTrackers:    append([]string(nil), resolvedTrackers...),
		MatchedTrackers:     append([]string(nil), subject.MatchedTrackers...),
		BlockedTrackers:     cloneBlockedTrackers(policy.BlockedTrackers),
		TrackerRuleFailures: cloneTrackerRuleFailures(policy.TrackerRuleFailures),
	}
	return trackerPayloadEvidence{
		subject:          subject,
		resolvedTrackers: append([]string(nil), resolvedTrackers...),
		outcome:          outcome,
		duplicateResults: results,
	}, nil
}

func (m *uploadModule) resolveTrackerPayloadSubject(
	ctx context.Context,
	input api.UploadReviewInput,
	operation api.OperationKind,
) (api.UploadSubject, []string, error) {
	if m == nil || m.preparedFacts == nil {
		return api.UploadSubject{}, nil, errors.New("core: canonical preparation is not configured")
	}
	if m.torrents == nil || m.trackers == nil {
		return api.UploadSubject{}, nil, errors.New("core: tracker payload services are not configured")
	}
	subject, err := m.preparedFacts.ResolveUploadSubject(ctx, input)
	if err != nil {
		return api.UploadSubject{}, nil, fmt.Errorf("core: resolve tracker payload subject: %w", err)
	}
	logger := logging.FromContext(ctx, m.logger)
	resolvedTrackers := trackerspkg.ResolveExplicitTrackersWithRegistry(input.Trackers, logger, m.registry)
	if len(resolvedTrackers) == 0 {
		return api.UploadSubject{}, nil, noEligibleTrackersError(operation)
	}
	subject.Trackers = append([]string(nil), resolvedTrackers...)
	if err := m.hydrateCanonicalTrackerState(ctx, &subject); err != nil {
		return api.UploadSubject{}, nil, err
	}
	return subject, resolvedTrackers, nil
}

func (m *uploadModule) evaluateTrackerPayloadPolicy(
	ctx context.Context,
	subject api.UploadSubject,
	resolvedTrackers []string,
) (api.UploadReviewOutcome, error) {
	policy := api.UploadReviewOutcome{ResolvedTrackers: append([]string(nil), resolvedTrackers...)}
	var err error
	if m.policy != nil {
		policy, err = m.policy.EvaluateUploadPolicy(ctx, subject, resolvedTrackers)
		if err != nil {
			return api.UploadReviewOutcome{}, fmt.Errorf("core: upload policy: %w", err)
		}
	} else {
		policy.TrackerRuleFailures, err = evaluateSubjectRules(ctx, m.registry, subject, resolvedTrackers, logging.FromContext(ctx, m.logger))
		if err != nil {
			return api.UploadReviewOutcome{}, err
		}
	}
	return policy, nil
}

func applyTrackerIDOverrides(subject *api.UploadSubject, overrides map[string]string) {
	if subject == nil || len(overrides) == 0 {
		return
	}
	if subject.TrackerIDs == nil {
		subject.TrackerIDs = make(map[string]string)
	}
	for key, value := range overrides {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			subject.TrackerIDs[key] = value
		}
	}
}

func validateAcceptedDuplicateEvidence(
	evidence api.AcceptedDuplicateEvidence,
	release api.ReleaseRef,
	resolvedTrackers []string,
) ([]api.DupeCheckResult, error) {
	if evidence.Release != release {
		return nil, dryRunEvidenceError(
			api.OperationFailureStaleGeneration,
			"Duplicate-check results are stale. Run duplicate checking again.",
			errors.New("duplicate evidence release does not match dry-run release"),
		)
	}
	evidenceTrackers := normalizeReviewedTrackers(evidence.Trackers)
	if !slices.Equal(evidenceTrackers, resolvedTrackers) {
		return nil, dryRunEvidenceError(
			api.OperationFailureMissingPrerequisite,
			"Duplicate-check results do not match the selected trackers. Run duplicate checking again.",
			errors.New("duplicate evidence tracker selection does not match dry-run selection"),
		)
	}
	selected := reviewedTrackerSet(resolvedTrackers)
	results := make(map[string]api.DupeCheckResult, len(evidence.Results))
	for _, raw := range api.CloneDupeCheckResults(evidence.Results) {
		name := normalizeEligibilityTracker(raw.Tracker)
		if name == "" {
			return nil, dryRunEvidenceError(
				api.OperationFailureMissingPrerequisite,
				"Duplicate-check results are incomplete. Run duplicate checking again.",
				errors.New("duplicate evidence contains an unnamed tracker result"),
			)
		}
		if _, ok := selected[name]; !ok {
			return nil, dryRunEvidenceError(
				api.OperationFailureMissingPrerequisite,
				"Duplicate-check results do not match the selected trackers. Run duplicate checking again.",
				fmt.Errorf("duplicate evidence contains unselected tracker %s", name),
			)
		}
		if _, exists := results[name]; exists || !terminalDuplicateResult(raw) {
			return nil, dryRunEvidenceError(
				api.OperationFailureMissingPrerequisite,
				"Duplicate-check results are incomplete. Run duplicate checking again.",
				fmt.Errorf("duplicate evidence result is duplicate or incomplete for tracker %s", name),
			)
		}
		raw.Tracker = name
		results[name] = raw
	}
	ordered := make([]api.DupeCheckResult, 0, len(resolvedTrackers))
	for _, tracker := range resolvedTrackers {
		result, ok := results[tracker]
		if !ok {
			return nil, dryRunEvidenceError(
				api.OperationFailureMissingPrerequisite,
				"Duplicate-check results are incomplete. Run duplicate checking again.",
				fmt.Errorf("duplicate evidence is missing tracker %s", tracker),
			)
		}
		ordered = append(ordered, result)
	}
	return ordered, nil
}

func terminalDuplicateResult(result api.DupeCheckResult) bool {
	if result.Skipped || strings.TrimSpace(result.Error) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(result.Status)) {
	case "completed", "skipped", "failed":
		return true
	default:
		return false
	}
}

func dryRunEvidenceError(code api.OperationFailureCode, message string, cause error) error {
	return api.NewOperationError(api.OperationFailure{
		Code:      code,
		Operation: api.OperationKindDryRun,
		Message:   message,
		Recovery:  api.OperationRecoveryCompletePrerequisite,
	}, cause)
}

// buildTrackerPayloadPreview constructs tracker payloads and eligibility from
// accepted evidence. It never performs client discovery or remote duplicate
// checking.
func (m *uploadModule) buildTrackerPayloadPreview(
	ctx context.Context,
	input api.UploadReviewInput,
	previewIntent trackerspkg.PreparationIntent,
	evidence trackerPayloadEvidence,
) (trackerPayloadPreview, error) {
	subject := evidence.subject
	resolvedTrackers := evidence.resolvedTrackers
	outcome := evidence.outcome
	var err error
	subject.MatchedTrackers = append([]string(nil), outcome.MatchedTrackers...)
	subject.BlockedTrackers = cloneBlockedTrackers(outcome.BlockedTrackers)
	subject.TrackerRuleFailures = cloneTrackerRuleFailures(outcome.TrackerRuleFailures)
	subject.CrossSeedTorrents = append([]api.UploadedTorrent(nil), outcome.CrossSeedTorrents...)
	if m.resolveSubjectGroups == nil {
		return trackerPayloadPreview{}, errors.New("core: subject description resolver not configured")
	}
	subject.DescriptionGroups, err = m.resolveSubjectGroups(ctx, subject, input)
	if err != nil {
		return trackerPayloadPreview{}, err
	}
	torrent, err := m.torrents.Create(ctx, torrentSubjectFromUpload(subject, input))
	if err != nil {
		return trackerPayloadPreview{}, fmt.Errorf("core: %w", err)
	}
	subject.TorrentPath = torrent.Path
	var dryRuns []api.TrackerDryRunEntry
	switch previewIntent {
	case trackerspkg.PreparationIntentDryRun:
		dryRuns, err = m.trackers.BuildUploadDryRun(ctx, subject, resolvedTrackers)
	case trackerspkg.PreparationIntentUploadReview:
		dryRuns, err = m.trackers.BuildUploadReview(ctx, subject, resolvedTrackers)
	case trackerspkg.PreparationIntentDescriptionPreview, trackerspkg.PreparationIntentUpload:
		return trackerPayloadPreview{}, fmt.Errorf("core: unsupported tracker payload intent %q", previewIntent)
	}
	if err != nil {
		return trackerPayloadPreview{}, fmt.Errorf("core: %w", err)
	}
	annotateDryRunSubjectReleaseNames(subject, dryRuns)
	dryRunByTracker := make(map[string]api.TrackerDryRunEntry, len(dryRuns))
	for _, entry := range dryRuns {
		if name := strings.ToUpper(strings.TrimSpace(entry.Tracker)); name != "" {
			dryRunByTracker[name] = entry
		}
	}
	dupeResults := mapDupeResults(evidence.duplicateResults)
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
		review.Questionnaire = review.DryRun.Questionnaire
		reviews = append(reviews, review)
		authorizedRules, authorizationErr := trackerspkg.AuthorizedRulesForTracker(input.RuleAuthorizations, name)
		if authorizationErr != nil {
			return trackerPayloadPreview{}, fmt.Errorf("core: rule authorization: %w", authorizationErr)
		}
		eligibilityAssessments = append(eligibilityAssessments, api.TrackerEligibilityAssessment{
			Tracker:        name,
			Duplicate:      review.DupeCheck,
			RuleFailures:   cloneRuleFailures(review.RuleFailures),
			PolicyBlocks:   append([]api.TrackerBlockReason(nil), outcome.BlockedTrackers[name]...),
			AuthRequired:   review.DupeCheck.SkipCode == dupeSkipCodeTrackerAuthNotReady,
			Banned:         review.Banned,
			BannedReason:   review.BannedReason,
			ContentFailure: cloneTrackerContentFailure(review.DryRun.ContentFailure),
			Choices: api.TrackerReviewChoices{
				IgnoreDuplicate:        containsNormalizedTracker(input.IgnoreDupesFor, name),
				AuthorizedRuleFailures: authorizedRules,
			},
		})
	}
	eligibility, err := buildTrackerEligibility(api.TrackerEligibilityInput{
		Release:          input.Release,
		SelectedTrackers: resolvedTrackers,
		Assessments:      eligibilityAssessments,
	})
	if err != nil {
		return trackerPayloadPreview{}, err
	}
	if err := persistTrackerRuleDecisions(ctx, m.trackerRepo, subject.SourcePath, eligibility); err != nil {
		return trackerPayloadPreview{}, err
	}
	logTrackerEligibility(ctx, m.logger, string(previewIntent), eligibility)
	outcome.Eligibility = eligibility
	return trackerPayloadPreview{
		subject: subject,
		review: api.UploadReview{
			SourcePath:  subject.SourcePath,
			Trackers:    reviews,
			Eligibility: eligibility,
		},
		outcome: outcome,
		torrent: torrent,
	}, nil
}

// persistTrackerRuleDecisions atomically replaces each selected tracker's
// stored rule evidence with Core's canonical authorization decisions.
func persistTrackerRuleDecisions(
	ctx context.Context,
	repo api.TrackerStateRepository,
	sourcePath string,
	eligibility api.TrackerEligibility,
) error {
	if repo == nil {
		return nil
	}
	for _, state := range eligibility.Trackers {
		failures := make([]api.TrackerRuleFailure, 0, len(state.RuleDecisions))
		for _, decision := range state.RuleDecisions {
			failures = append(failures, api.TrackerRuleFailure{
				SourcePath:  sourcePath,
				Tracker:     state.Tracker,
				Rule:        decision.Rule,
				Reason:      decision.Reason,
				Disposition: decision.Disposition,
				Authorized:  decision.Authorized,
			})
		}
		if err := repo.SaveTrackerRuleFailures(ctx, sourcePath, state.Tracker, failures); err != nil {
			return fmt.Errorf("core: persist tracker rule decisions tracker=%s: %w", state.Tracker, err)
		}
	}
	return nil
}

func cloneTrackerContentFailure(value *api.TrackerContentFailure) *api.TrackerContentFailure {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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

func evaluateSubjectRules(
	ctx context.Context,
	registry *trackerspkg.Registry,
	subject api.UploadSubject,
	trackerNames []string,
	logger api.Logger,
) (map[string][]api.RuleFailure, error) {
	result := make(map[string][]api.RuleFailure)
	for _, tracker := range trackerNames {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		failures, err := trackerspkg.EvaluateRulesWithRegistry(ctx, registry, name, api.NewRuleSubject(subject), logger)
		if err != nil {
			return nil, fmt.Errorf("core: evaluate tracker rules: %w", err)
		}
		if len(failures) > 0 {
			result[name] = append([]api.RuleFailure(nil), failures...)
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
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
