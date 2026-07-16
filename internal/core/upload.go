// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// uploadModule owns synchronous upload review and execution policy. It borrows
// prepared-release state and runtime services; Core retains resource lifetime.
type uploadModule struct {
	cfg           config.Config
	logger        api.Logger
	policy        uploadPolicyEvaluator
	trackers      api.TrackerService
	torrents      api.TorrentService
	clients       api.ClientService
	filesystem    api.FilesystemService
	dupes         api.DupeService
	repo          api.ReleaseStateRepository
	trackerRepo   api.TrackerStateRepository
	registry      *trackers.Registry
	preparedFacts *preparedrelease.Module
	discovery     *clientdiscovery.Module

	resolveDescriptionOverride func(context.Context, api.Request) (api.Request, error)
	resolveSubjectGroups       func(context.Context, api.UploadSubject, api.UploadReviewInput) ([]api.DescriptionBuilderGroup, error)
	importAcceptedMenuImages   func(context.Context, api.MediaPlanInput, []string) error
}

type uploadPolicyEvaluator interface {
	EvaluateUploadPolicy(context.Context, api.UploadSubject, []string) (api.UploadReviewOutcome, error)
}

// newUploadModule composes upload review and execution around exact prepared
// generations, current client discovery, and borrowed runtime services.
func newUploadModule(
	cfg config.Config,
	logger api.Logger,
	services api.ServiceSet,
	repo api.ReleaseStateRepository,
	trackerRepo api.TrackerStateRepository,
	registry *trackers.Registry,
	preparedFacts *preparedrelease.Module,
	resolveDescriptionOverride func(context.Context, api.Request) (api.Request, error),
	resolveSubjectGroups func(context.Context, api.UploadSubject, api.UploadReviewInput) ([]api.DescriptionBuilderGroup, error),
	importAcceptedMenuImages func(context.Context, api.MediaPlanInput, []string) error,
	discovery *clientdiscovery.Module,
) *uploadModule {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &uploadModule{
		cfg:                        cfg,
		logger:                     logger,
		policy:                     uploadPolicyEvaluatorFrom(services.Metadata),
		trackers:                   services.Trackers,
		torrents:                   services.Torrents,
		clients:                    services.Clients,
		filesystem:                 services.Filesystem,
		dupes:                      services.Dupes,
		repo:                       repo,
		trackerRepo:                trackerRepo,
		registry:                   registry,
		preparedFacts:              preparedFacts,
		discovery:                  discovery,
		resolveDescriptionOverride: resolveDescriptionOverride,
		resolveSubjectGroups:       resolveSubjectGroups,
		importAcceptedMenuImages:   importAcceptedMenuImages,
	}
}

func uploadPolicyEvaluatorFrom(value any) uploadPolicyEvaluator {
	evaluator, _ := value.(uploadPolicyEvaluator)
	return evaluator
}

func (m *uploadModule) runPrepared(ctx context.Context, req api.Request) (api.Result, error) {
	req = normalizeExecutionRequest(req)
	resolvedReq, err := m.resolveDescriptionOverride(ctx, req)
	if err != nil {
		return api.Result{}, err
	}
	return m.runCanonicalRequest(ctx, resolvedReq)
}

func (m *uploadModule) runCanonicalRequest(ctx context.Context, req api.Request) (api.Result, error) {
	req = m.expandEntrypointTrackerDefaults(req)
	if m.filesystem == nil {
		return api.Result{}, errors.New("core: filesystem service not configured")
	}
	normalizedPaths, err := m.filesystem.ValidatePaths(ctx, []string{req.SourcePath})
	if err != nil {
		return api.Result{}, fmt.Errorf("core: %w", err)
	}
	if len(normalizedPaths) != 1 {
		return api.Result{}, internalerrors.ErrInvalidInput
	}
	req.SourcePath = normalizedPaths[0]
	prepareInput, err := api.MapPreparationRequest(req, api.PreparationIntentUpload)
	if err != nil {
		return api.Result{}, fmt.Errorf("core: map upload preparation request: %w", err)
	}
	prepared, err := m.preparedFacts.Prepare(ctx, prepareInput)
	if err != nil {
		return api.Result{}, fmt.Errorf("core: prepare upload release: %w", err)
	}
	reviewInput := uploadReviewInputFromRequest(req, api.ReleaseRef{
		SourcePath: prepared.Release.Source.SourcePath,
		Generation: prepared.Release.Generation,
	})
	reviewed, err := m.reviewAccepted(ctx, reviewInput)
	if err != nil {
		return api.Result{}, err
	}
	return m.runCanonicalAccepted(ctx, api.UploadExecutionPlan{Input: reviewInput, Outcome: reviewed.Outcome})
}

// expandEntrypointTrackerDefaults keeps default expansion at high-level
// CLI/Core entrypoints. Exact accepted operations preserve explicit empty.
func (m *uploadModule) expandEntrypointTrackerDefaults(req api.Request) api.Request {
	if len(req.Trackers) != 0 {
		return req
	}
	req.Trackers = trackers.ResolveTrackersWithRegistry(m.cfg, nil, req.TrackersRemove, m.logger, m.registry)
	return req
}

func (m *uploadModule) runAccepted(ctx context.Context, plan api.UploadExecutionPlan) (api.Result, error) {
	result, err := m.runCanonicalAccepted(ctx, plan)
	return result, classifyOperationError(api.OperationKindUploadExecute, err)
}

func cloneOperationQuestionnaireAnswers(input map[string]map[string]string) map[string]map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]map[string]string, len(input))
	for tracker, answers := range input {
		cloned := make(map[string]string, len(answers))
		maps.Copy(cloned, answers)
		result[tracker] = cloned
	}
	return result
}

func (m *uploadModule) runCanonicalAccepted(ctx context.Context, plan api.UploadExecutionPlan) (api.Result, error) {
	subject, err := m.preparedFacts.ResolveUploadSubject(ctx, plan.Input)
	if err != nil {
		return api.Result{}, fmt.Errorf("core: resolve upload subject: %w", err)
	}
	resolvedTrackers := intersectReviewedTrackers(plan.Input.Trackers, plan.Outcome.Eligibility.EligibleTrackers)
	if len(resolvedTrackers) == 0 {
		m.logger.Debugf("core: reviewed upload resolved no trackers source=%s", subject.SourcePath)
		return api.Result{}, noEligibleTrackersError(api.OperationKindUploadExecute)
	}
	subject.Trackers = resolvedTrackers
	subject.MatchedTrackers = filterReviewedNames(plan.Outcome.MatchedTrackers, resolvedTrackers)
	subject.TrackersRemove = append([]string(nil), subject.MatchedTrackers...)
	subject.BlockedTrackers = filterReviewedBlocks(plan.Outcome.BlockedTrackers, resolvedTrackers)
	subject.TrackerRuleFailures = filterReviewedRuleFailures(plan.Outcome.TrackerRuleFailures, resolvedTrackers)
	subject.CrossSeedTorrents = filterReviewedTorrents(plan.Outcome.CrossSeedTorrents, resolvedTrackers)
	if err := m.hydrateCanonicalTrackerState(ctx, &subject); err != nil {
		return api.Result{}, err
	}

	req := uploadRequestFromPlan(plan, resolvedTrackers)
	if len(req.ScreenshotOverrides.MenuPaths) > 0 {
		if m.importAcceptedMenuImages == nil {
			return api.Result{}, errors.New("core: accepted menu-image importer not configured")
		}
		if err := m.importAcceptedMenuImages(ctx, api.MediaPlanInput{Release: plan.Input.Release}, req.ScreenshotOverrides.MenuPaths); err != nil {
			return api.Result{}, fmt.Errorf("core: import menu images failed: %w", err)
		}
	}
	uploaded, err := m.executeAcceptedUpload(ctx, req, subject, plan.Input)
	return api.Result{UploadedCount: uploaded}, err
}

// executeAcceptedUpload consumes only the exact reviewed subject and workflow
// input. Mutable preparation state is neither reconstructed nor consulted.
func (m *uploadModule) executeAcceptedUpload(
	ctx context.Context,
	req api.Request,
	subject api.UploadSubject,
	input api.UploadReviewInput,
) (int, error) {
	if m.resolveSubjectGroups == nil {
		return 0, errors.New("core: subject description resolver not configured")
	}
	descriptionGroups, err := m.resolveSubjectGroups(ctx, subject, input)
	if err != nil {
		return 0, err
	}
	subject.DescriptionGroups = descriptionGroups

	emitPreparedUploadProgress(ctx, req, subject.SourcePath, "", "torrent", "running", "Preparing torrent")
	torrent, err := m.torrents.Create(ctx, torrentSubjectFromUpload(subject, input))
	if err != nil {
		return 0, fmt.Errorf("core: %w", err)
	}
	subject.TorrentPath = torrent.Path
	if m.repo != nil && torrent.InfoHash != "" {
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("context canceled: %w", err)
		}
		if err := m.persistPreparedInfoHash(ctx, subject.SourcePath, torrent.InfoHash); err != nil {
			return 0, fmt.Errorf("metadata: persist info hash: %w", err)
		}
	}

	if input.Options.DryRun || input.Options.Debug {
		if !input.Options.NoSeed {
			entries, err := m.trackers.BuildUploadDryRun(ctx, subject, subject.Trackers)
			if err != nil {
				return 0, fmt.Errorf("core: %w", err)
			}
			annotateDryRunSubjectReleaseNames(subject, entries)
			if err := m.injectTrackerDryRunSubjects(ctx, req, subject, input, entries, torrent); err != nil {
				return 0, err
			}
		}
		m.logger.Infof("core: dry-run or debug enabled, skipping tracker upload source=%s", subject.SourcePath)
		emitPreparedUploadProgress(ctx, req, subject.SourcePath, "", "upload", "completed", "Dry run complete")
		return 0, nil
	}

	emitPreparedUploadProgress(ctx, req, subject.SourcePath, "", "tracker_upload", "running", "Uploading to tracker")
	summary, uploadErr := m.trackers.Upload(ctx, subject)
	if summary.Uploaded < 0 {
		return 0, fmt.Errorf("upload summary invalid: %d", summary.Uploaded)
	}
	if uploadErr != nil && summary.Uploaded == 0 {
		emitPreparedUploadProgress(ctx, req, subject.SourcePath, "", "tracker_upload", "failed", "Tracker upload failed")
		return 0, fmt.Errorf("core: %w", uploadErr)
	}

	if !input.Options.NoSeed {
		if err := m.injectReviewedCrossSeeds(ctx, req, subject, input); err != nil {
			if uploadErr != nil {
				return summary.Uploaded, fmt.Errorf("core: cross-seed injection failed after tracker-local upload failures: %w", err)
			}
			return summary.Uploaded, err
		}
	}
	if uploadErr != nil {
		emitPreparedUploadProgress(ctx, req, subject.SourcePath, "", "tracker_upload", "failed", "Tracker upload failed")
		return summary.Uploaded, fmt.Errorf("core: %w", uploadErr)
	}
	emitPreparedUploadProgress(ctx, req, subject.SourcePath, "", "tracker_upload", "completed", "Tracker upload complete")

	if !input.Options.NoSeed {
		if len(summary.UploadedTorrents) == 0 {
			m.logger.Warnf("core: no tracker torrent artifacts available for injection for %s", subject.SourcePath)
		}
		for _, uploaded := range summary.UploadedTorrents {
			torrentPath := strings.TrimSpace(uploaded.TorrentPath)
			torrentURL := strings.TrimSpace(uploaded.DownloadURL)
			if torrentPath == "" && torrentURL == "" {
				continue
			}
			emitPreparedUploadProgress(ctx, req, subject.SourcePath, uploaded.Tracker, "client_injection", "running", "Injecting torrent into client")
			if err := m.clients.Inject(ctx, clientSubjectFromUpload(subject, input), api.TorrentResult{
				Path:    torrentPath,
				URL:     torrentURL,
				Tracker: uploaded.Tracker,
			}); err != nil {
				emitPreparedUploadProgress(ctx, req, subject.SourcePath, uploaded.Tracker, "client_injection", "failed", "Client injection failed")
				return summary.Uploaded, fmt.Errorf("core: %w", err)
			}
			emitPreparedUploadProgress(ctx, req, subject.SourcePath, uploaded.Tracker, "client_injection", "completed", "Client injection complete")
		}
	}
	return summary.Uploaded, nil
}

func torrentSubjectFromUpload(subject api.UploadSubject, input api.UploadReviewInput) api.TorrentSubject {
	return api.TorrentSubject{
		SourcePath:        subject.SourcePath,
		SourceSize:        subject.SourceSize,
		FileList:          append([]string(nil), subject.FileList...),
		DiscType:          subject.DiscType,
		ClientTorrentPath: subject.ClientTorrentPath,
		Trackers:          append([]string(nil), subject.Trackers...),
		TorrentOverrides:  input.TorrentOverrides,
	}
}

func clientSubjectFromUpload(subject api.UploadSubject, input api.UploadReviewInput) api.ClientSubject {
	return api.ClientSubject{
		SourcePath:      subject.SourcePath,
		FileList:        append([]string(nil), subject.FileList...),
		DiscType:        subject.DiscType,
		ClientOverrides: input.ClientOverrides,
		Debug:           input.Options.Debug,
	}
}

func (m *uploadModule) injectReviewedCrossSeeds(
	ctx context.Context,
	req api.Request,
	subject api.UploadSubject,
	input api.UploadReviewInput,
) error {
	if !m.cfg.PostUpload.CrossSeeding || len(subject.CrossSeedTorrents) == 0 {
		return nil
	}
	if m.clients == nil {
		return errors.New("core: client service not configured")
	}
	for _, crossSeed := range subject.CrossSeedTorrents {
		torrentPath := strings.TrimSpace(crossSeed.TorrentPath)
		torrentURL := strings.TrimSpace(crossSeed.DownloadURL)
		if torrentPath == "" && torrentURL == "" {
			continue
		}
		tracker := strings.ToUpper(strings.TrimSpace(crossSeed.Tracker))
		emitPreparedUploadProgress(ctx, req, subject.SourcePath, tracker, "client_injection", "running", "Injecting cross-seed torrent into client")
		if err := m.clients.Inject(ctx, clientSubjectFromUpload(subject, input), api.TorrentResult{
			Path:      torrentPath,
			URL:       torrentURL,
			Tracker:   tracker,
			CrossSeed: true,
		}); err != nil {
			emitPreparedUploadProgress(ctx, req, subject.SourcePath, tracker, "client_injection", "failed", "Cross-seed client injection failed")
			return fmt.Errorf("core: %w", err)
		}
		emitPreparedUploadProgress(ctx, req, subject.SourcePath, tracker, "client_injection", "completed", "Cross-seed client injection complete")
	}
	return nil
}

func (m *uploadModule) injectTrackerDryRunSubjects(
	ctx context.Context,
	req api.Request,
	subject api.UploadSubject,
	input api.UploadReviewInput,
	entries []api.TrackerDryRunEntry,
	fallback api.TorrentResult,
) error {
	ready := make([]api.TrackerDryRunEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.EqualFold(strings.TrimSpace(entry.Status), "ready") {
			ready = append(ready, entry)
		}
	}
	for _, entry := range ready {
		trackerName := strings.ToUpper(strings.TrimSpace(entry.Tracker))
		injectSubject := subject
		injectSubject.Trackers = []string{trackerName}
		injectSubject.TorrentPath = trackerDryRunTorrentPath(entry)
		trackerCfg := config.TrackerConfig{}
		for name, cfg := range m.cfg.Trackers.Trackers {
			if strings.EqualFold(strings.TrimSpace(name), trackerName) {
				trackerCfg = cfg
				break
			}
		}
		prepared, err := trackers.PrepareDryRunInjectionTorrentWithRegistry(injectSubject, m.cfg.MainSettings.DBPath, trackerName, trackerCfg, m.registry)
		if err != nil {
			return fmt.Errorf("core: tracker dry-run injection torrent artifact tracker=%s: %w", trackerName, err)
		}
		injectTorrent := api.TorrentResult{
			Path:     strings.TrimSpace(prepared.TorrentPath),
			InfoHash: fallback.InfoHash,
			Tracker:  trackerName,
		}
		if injectTorrent.Path == "" {
			injectTorrent.Path = strings.TrimSpace(fallback.Path)
		}
		if injectTorrent.Path == "" {
			continue
		}
		emitPreparedUploadProgress(ctx, req, subject.SourcePath, trackerName, "client_injection", "running", "Injecting torrent into client")
		if err := m.clients.Inject(ctx, clientSubjectFromUpload(injectSubject, input), injectTorrent); err != nil {
			emitPreparedUploadProgress(ctx, req, subject.SourcePath, trackerName, "client_injection", "failed", "Client injection failed")
			return fmt.Errorf("core: %w", err)
		}
		emitPreparedUploadProgress(ctx, req, subject.SourcePath, trackerName, "client_injection", "completed", "Client injection complete")
	}
	return nil
}

func (m *uploadModule) fetchAcceptedTrackerDryRun(ctx context.Context, input api.TrackerDryRunInput) (api.TrackerDryRunPreview, error) {
	if m.preparedFacts == nil {
		return api.TrackerDryRunPreview{}, errors.New("core: canonical preparation is not configured")
	}
	reviewInput := api.UploadReviewInput{
		Release:                input.Release,
		Trackers:               append([]string(nil), input.Trackers...),
		IgnoreDupesFor:         append([]string(nil), input.IgnoreDupesFor...),
		IgnoreRuleFailuresFor:  append([]string(nil), input.IgnoreRuleFailuresFor...),
		QuestionnaireAnswers:   cloneOperationQuestionnaireAnswers(input.QuestionnaireAnswers),
		DescriptionGroups:      api.CloneDescriptionBuilderGroups(input.DescriptionGroups),
		TrackerConfigOverrides: input.TrackerConfigOverrides,
		TrackerSiteOverrides:   input.TrackerSiteOverrides,
		ImageHostOverrides:     input.ImageHostOverrides,
		TorrentOverrides:       input.TorrentOverrides,
		Options:                input.Options,
	}
	reviewInput.Options.DryRun = true
	subject, err := m.preparedFacts.ResolveUploadSubject(ctx, reviewInput)
	if err != nil {
		return api.TrackerDryRunPreview{}, fmt.Errorf("core: resolve tracker dry-run subject: %w", err)
	}
	resolvedTrackers := trackers.ResolveExplicitTrackersWithRegistry(input.Trackers, m.logger, m.registry)
	if len(resolvedTrackers) == 0 {
		return api.TrackerDryRunPreview{}, noEligibleTrackersError(api.OperationKindDryRun)
	}
	subject.Trackers = resolvedTrackers
	subject.Options.DryRun = true
	if err := m.hydrateCanonicalTrackerState(ctx, &subject); err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	req := uploadRequestFromPlan(api.UploadExecutionPlan{Input: reviewInput}, resolvedTrackers)
	if m.resolveSubjectGroups == nil {
		return api.TrackerDryRunPreview{}, errors.New("core: subject description resolver not configured")
	}
	descriptionGroups, err := m.resolveSubjectGroups(ctx, subject, reviewInput)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	subject.DescriptionGroups = api.CloneDescriptionBuilderGroups(descriptionGroups)
	torrent, err := m.torrents.Create(ctx, torrentSubjectFromUpload(subject, reviewInput))
	if err != nil {
		return api.TrackerDryRunPreview{}, fmt.Errorf("core: %w", err)
	}
	subject.TorrentPath = torrent.Path
	entries, err := m.trackers.BuildUploadDryRun(ctx, subject, resolvedTrackers)
	if err != nil {
		return api.TrackerDryRunPreview{}, fmt.Errorf("core: %w", err)
	}
	annotateDryRunSubjectReleaseNames(subject, entries)
	if !subject.Options.NoSeed {
		if err := m.injectTrackerDryRunSubjects(ctx, req, subject, reviewInput, entries, torrent); err != nil {
			return api.TrackerDryRunPreview{}, err
		}
	}
	return api.TrackerDryRunPreview{SourcePath: subject.SourcePath, Trackers: sanitizeTrackerDryRunEntries(entries)}, nil
}

func normalizeReviewedTrackers(trackers []string) []string {
	result := make([]string, 0, len(trackers))
	seen := make(map[string]struct{}, len(trackers))
	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func uploadRequestFromPlan(plan api.UploadExecutionPlan, trackers []string) api.Request {
	input := plan.Input
	return api.Request{
		SourcePath:                   input.Release.SourcePath,
		Options:                      input.Options,
		DescriptionGroups:            api.CloneDescriptionBuilderGroups(input.DescriptionGroups),
		Trackers:                     append([]string(nil), trackers...),
		IgnoreTrackerRuleFailuresFor: append([]string(nil), input.IgnoreRuleFailuresFor...),
		IgnoreDupesFor:               append([]string(nil), input.IgnoreDupesFor...),
		TrackerQuestionnaireAnswers:  cloneOperationQuestionnaireAnswers(input.QuestionnaireAnswers),
		TrackerConfigOverrides:       input.TrackerConfigOverrides,
		TrackerSiteOverrides:         input.TrackerSiteOverrides,
		ClientOverrides:              input.ClientOverrides,
		ImageHostOverrides:           input.ImageHostOverrides,
		ScreenshotOverrides:          input.ScreenshotOverrides,
		TorrentOverrides:             input.TorrentOverrides,
	}
}

func intersectReviewedTrackers(selected []string, reviewed []string) []string {
	allowed := make(map[string]struct{}, len(reviewed))
	for _, tracker := range reviewed {
		if name := strings.ToUpper(strings.TrimSpace(tracker)); name != "" {
			allowed[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, tracker := range selected {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if _, ok := allowed[name]; !ok || name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func reviewedTrackerSet(trackers []string) map[string]struct{} {
	result := make(map[string]struct{}, len(trackers))
	for _, tracker := range trackers {
		if name := strings.ToUpper(strings.TrimSpace(tracker)); name != "" {
			result[name] = struct{}{}
		}
	}
	return result
}

func filterReviewedNames(values []string, trackers []string) []string {
	allowed := reviewedTrackerSet(trackers)
	result := make([]string, 0, len(values))
	for _, value := range values {
		name := strings.ToUpper(strings.TrimSpace(value))
		if _, ok := allowed[name]; ok {
			result = append(result, name)
		}
	}
	return result
}

func filterReviewedBlocks(values map[string][]api.TrackerBlockReason, trackers []string) map[string][]api.TrackerBlockReason {
	allowed := reviewedTrackerSet(trackers)
	result := make(map[string][]api.TrackerBlockReason)
	for tracker, reasons := range values {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if _, ok := allowed[name]; ok {
			result[name] = append([]api.TrackerBlockReason(nil), reasons...)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func filterReviewedRuleFailures(values map[string][]api.RuleFailure, trackers []string) map[string][]api.RuleFailure {
	allowed := reviewedTrackerSet(trackers)
	result := make(map[string][]api.RuleFailure)
	for tracker, failures := range values {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if _, ok := allowed[name]; ok {
			result[name] = append([]api.RuleFailure(nil), failures...)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func filterReviewedTorrents(values []api.UploadedTorrent, trackers []string) []api.UploadedTorrent {
	allowed := reviewedTrackerSet(trackers)
	result := make([]api.UploadedTorrent, 0, len(values))
	for _, torrent := range values {
		if _, ok := allowed[strings.ToUpper(strings.TrimSpace(torrent.Tracker))]; ok {
			result = append(result, torrent)
		}
	}
	return result
}

func (m *uploadModule) hydrateCanonicalTrackerState(ctx context.Context, subject *api.UploadSubject) error {
	if m.trackerRepo == nil || subject == nil || subject.Options.SkipAutoTorrent {
		return nil
	}
	records, err := m.trackerRepo.ListTrackerMetadataByPath(ctx, subject.SourcePath)
	if err != nil {
		return fmt.Errorf("core: tracker metadata lookup: %w", err)
	}
	allowed := reviewedTrackerSet(subject.Trackers)
	for _, record := range records {
		name := strings.ToUpper(strings.TrimSpace(record.Tracker))
		if _, ok := allowed[name]; !ok {
			continue
		}
		subject.TrackerData = append(subject.TrackerData, record)
		if record.Matched {
			subject.MatchedTrackers = appendUniqueNormalizedTracker(subject.MatchedTrackers, name)
		}
		if trackerID := strings.TrimSpace(record.TrackerID); trackerID != "" {
			if subject.TrackerIDs == nil {
				subject.TrackerIDs = make(map[string]string)
			}
			subject.TrackerIDs[strings.ToLower(name)] = trackerID
		}
		if subject.InfoHash == "" {
			subject.InfoHash = strings.TrimSpace(record.InfoHash)
		}
	}
	return nil
}

// executePreparedUpload returns the number of tracker uploads accepted before any
// later upload, injection, validation, or cancellation error.

// Cross-seed torrents come from dupe matches and should be injected even when
// the tracker upload summary later reports no successful uploads.

// persistPreparedInfoHash stores the prepared torrent hash without replacing
// existing release metadata used by history views.
func (m *uploadModule) persistPreparedInfoHash(ctx context.Context, sourcePath string, infoHash string) error {
	if m == nil || m.repo == nil {
		return nil
	}
	trimmedPath := strings.TrimSpace(sourcePath)
	trimmedHash := strings.TrimSpace(infoHash)
	if trimmedPath == "" || trimmedHash == "" {
		return nil
	}

	metadata, err := m.repo.GetByPath(ctx, trimmedPath)
	if err != nil {
		if !errors.Is(err, internalerrors.ErrNotFound) {
			return fmt.Errorf("lookup existing metadata: %w", err)
		}
		m.logger.Debugf("metadata: skip info hash persistence without stored metadata for %s", trimmedPath)
		return nil
	} else if strings.TrimSpace(metadata.Path) == "" {
		metadata.Path = trimmedPath
	}
	metadata.InfoHash = trimmedHash
	metadata.UpdatedAt = time.Now().UTC()
	if err := m.repo.Save(ctx, metadata); err != nil {
		return fmt.Errorf("save metadata: %w", err)
	}
	return nil
}

// injectTrackerDryRunTorrents injects only ready dry-run tracker torrents into
// configured clients so debug runs can exercise client handling without upload.

// prepareDryRunInjectionMeta returns metadata pointing at the tracker-specific
// dry-run torrent artifact when one exists, preserving the base torrent as a
// fallback for client injection.
