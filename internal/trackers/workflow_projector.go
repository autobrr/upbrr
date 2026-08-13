// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// WorkflowProjector adapts registry-owned tracker definitions and runtime
// config to immutable workflow catalog, runtime, selection, and projection snapshots.
type WorkflowProjector struct {
	registry *Registry
	config   config.Config
	logger   api.Logger
}

// NewWorkflowProjector constructs the tracker workflow projection adapter.
func NewWorkflowProjector(registry *Registry, cfg config.Config, logger api.Logger) (*WorkflowProjector, error) {
	if registry == nil {
		return nil, errors.New("trackers: workflow projector requires registry")
	}
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &WorkflowProjector{
		registry: registry,
		config:   cfg,
		logger:   logger,
	}, nil
}

// Build creates unstamped snapshot content; the workflow module owns IDs,
// revisions, lineage refs, timestamps, and publication. Rule authorizations are
// tracker-scoped server authority and apply only to an identical freshly
// evaluated waivable-failure fingerprint.
func (p *WorkflowProjector) Build(
	ctx context.Context,
	release api.ReleaseSnapshot,
	subject api.UploadSubject,
	trackerIDs []api.TrackerID,
	instructions map[api.TrackerID]api.TrackerProjectionInstructions,
	ruleAuthorizations map[api.TrackerID]api.WorkflowFingerprint,
	executionMode api.WorkflowExecutionMode,
) (
	api.TrackerCatalogSnapshot,
	api.TrackerRuntimeSnapshot,
	api.TrackerSelection,
	api.TrackerReleaseProjectionSet,
	error,
) {
	if err := ctx.Err(); err != nil {
		return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{},
			fmt.Errorf("trackers: build workflow projections: %w", err)
	}
	descriptors, err := p.registry.CatalogDescriptors()
	if err != nil {
		return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{}, err
	}
	catalog, err := (api.TrackerCatalogSnapshot{
		CatalogVersion: "tracker-projection-v1",
		Trackers:       descriptors,
	}).WithFingerprint()
	if err != nil {
		return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{},
			fmt.Errorf("trackers: catalog fingerprint: %w", err)
	}

	selected, err := p.resolveTrackerIDs(trackerIDs)
	if err != nil {
		return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{}, err
	}
	runtime, configFingerprints, err := p.runtimeSnapshot(catalog, descriptors)
	if err != nil {
		return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{}, err
	}
	selection, err := (api.TrackerSelection{TrackerIDs: selected}).WithFingerprint()
	if err != nil {
		return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{},
			fmt.Errorf("trackers: selection fingerprint: %w", err)
	}
	inputFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Release            api.WorkflowFingerprint
		TrackerIDs         []api.TrackerID
		Instructions       map[api.TrackerID]api.TrackerProjectionInstructions
		RuleAuthorizations map[api.TrackerID]api.WorkflowFingerprint
		ExecutionMode      api.WorkflowExecutionMode
	}{release.Fingerprint, selected, instructions, ruleAuthorizations, api.NormalizeWorkflowExecutionMode(executionMode)})
	if err != nil {
		return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{},
			fmt.Errorf("trackers: projection input fingerprint: %w", err)
	}
	projections, failures := p.projectSelected(
		ctx,
		subject,
		selected,
		instructions,
		ruleAuthorizations,
		inputFingerprint,
		catalog.Fingerprint,
		configFingerprints,
		executionMode,
	)
	policyFingerprint, err := projectionPolicyFingerprint(projections)
	if err != nil {
		return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{}, err
	}
	actions := make([]api.RequiredAction, 0)
	readyCount := 0
	for _, projection := range projections {
		actions = append(actions, projection.RequiredActions...)
		failures = append(failures, projection.Failures...)
		if projection.Readiness == api.ReadinessStatusReady && projection.DupeReady {
			readyCount++
		}
	}
	status := api.StageStatusReady
	if readyCount == 0 {
		status = api.StageStatusFailed
		if len(actions) > 0 {
			status = api.StageStatusBlocked
		}
	}
	return catalog, runtime, selection, api.TrackerReleaseProjectionSet{
		InputFingerprint:  inputFingerprint,
		PolicyFingerprint: policyFingerprint,
		ExecutionMode:     api.NormalizeWorkflowExecutionMode(executionMode),
		Projections:       projections,
		Status:            status,
		RequiredActions:   actions,
		Failures:          failures,
	}, nil
}

func (p *WorkflowProjector) resolveTrackerIDs(requested []api.TrackerID) ([]api.TrackerID, error) {
	if len(requested) == 0 {
		requested = make([]api.TrackerID, 0, len(p.config.Trackers.DefaultTrackers))
		for _, trackerID := range p.config.Trackers.DefaultTrackers {
			requested = append(requested, api.TrackerID(trackerID))
		}
	}
	selected := make([]api.TrackerID, 0, len(requested))
	seen := make(map[api.TrackerID]struct{}, len(requested))
	for _, requestedID := range requested {
		descriptor, ok := p.registry.LookupDescriptor(string(requestedID))
		if !ok {
			return nil, fmt.Errorf("trackers: tracker %s is not registered", requestedID)
		}
		trackerID := api.TrackerID(descriptor.Name)
		if _, ok := seen[trackerID]; ok {
			continue
		}
		seen[trackerID] = struct{}{}
		selected = append(selected, trackerID)
	}
	if len(selected) == 0 {
		return nil, errors.New("trackers: projection requires at least one tracker")
	}
	return selected, nil
}

func (p *WorkflowProjector) runtimeSnapshot(
	catalog api.TrackerCatalogSnapshot,
	descriptors []api.TrackerCatalogDescriptor,
) (api.TrackerRuntimeSnapshot, map[api.TrackerID]api.WorkflowFingerprint, error) {
	entries := make([]api.TrackerRuntimeEntry, 0, len(descriptors))
	fingerprints := make(map[api.TrackerID]api.WorkflowFingerprint, len(descriptors))
	for _, descriptor := range descriptors {
		trackerConfig := trackerConfigFor(p.config, string(descriptor.TrackerID))
		fingerprint, err := safeTrackerConfigFingerprint(trackerConfig)
		if err != nil {
			return api.TrackerRuntimeSnapshot{}, nil, err
		}
		fingerprints[descriptor.TrackerID] = fingerprint
		entries = append(entries, api.TrackerRuntimeEntry{
			TrackerID:            descriptor.TrackerID,
			Configured:           trackerConfigConfigured(trackerConfig),
			Default:              trackerIDInConfigList(descriptor.TrackerID, p.config.Trackers.DefaultTrackers),
			ConfigurationVersion: "tracker-config-v1",
			ConfigFingerprint:    fingerprint,
		})
	}
	runtime, err := (api.TrackerRuntimeSnapshot{
		Catalog:           api.TrackerCatalogSnapshotRef{ID: catalog.ID, Revision: catalog.Revision},
		RuntimeGeneration: "process-config-v1",
		Trackers:          entries,
	}).WithFingerprint()
	if err != nil {
		return api.TrackerRuntimeSnapshot{}, nil, fmt.Errorf("trackers: runtime fingerprint: %w", err)
	}
	return runtime, fingerprints, nil
}

func trackerIDInConfigList(trackerID api.TrackerID, values config.CSVList) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), string(trackerID)) {
			return true
		}
	}
	return false
}

func (p *WorkflowProjector) projectSelected(
	ctx context.Context,
	subject api.UploadSubject,
	selected []api.TrackerID,
	instructions map[api.TrackerID]api.TrackerProjectionInstructions,
	ruleAuthorizations map[api.TrackerID]api.WorkflowFingerprint,
	inputFingerprint api.WorkflowFingerprint,
	catalogFingerprint api.WorkflowFingerprint,
	configFingerprints map[api.TrackerID]api.WorkflowFingerprint,
	executionMode api.WorkflowExecutionMode,
) ([]api.TrackerReleaseProjection, []api.WorkflowFailure) {
	projections := make([]api.TrackerReleaseProjection, 0, len(selected))
	failures := make([]api.WorkflowFailure, 0)
	for _, trackerID := range selected {
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:   "projection",
			ItemID:  string(trackerID),
			Kind:    "tracker",
			Label:   string(trackerID),
			Status:  api.StageStatusQueued,
			Total:   len(selected),
			Message: "Tracker projection queued.",
		})
	}
	for index, trackerID := range selected {
		trackerSubject := subject
		trackerSubject.Trackers = []string{string(trackerID)}
		instruction := instructions[trackerID]
		trackerSubject.TrackerConfigOverrides = instruction.TrackerConfig
		trackerSubject.TrackerSiteOverrides = instruction.TrackerSite
		applyQuestionnaireInstruction(&trackerSubject, trackerID, instruction)
		requestedName := projectionRequestedUploadName(instruction)
		projection, failure := p.registry.ProjectRelease(ctx, PreparationInput{
			ExecutionMode:             executionMode,
			Tracker:                   string(trackerID),
			Meta:                      trackerSubject,
			RequestedUploadName:       requestedName,
			AdditionalReleaseNames:    projectionAdditionalNames(instruction),
			AuthorizedRuleFingerprint: ruleAuthorizations[trackerID],
			TrackerConfig:             applyTrackerConfigOverrides(trackerConfigFor(p.config, string(trackerID)), instruction.TrackerConfig),
			Runtime:                   PreparationRuntimeFromConfig(p.config),
			Logger:                    p.logger,
		}, inputFingerprint, catalogFingerprint, configFingerprints[trackerID])
		if descriptor, ok := p.registry.LookupDescriptor(string(trackerID)); ok {
			applyWorkflowProjectionRequirements(&projection, descriptor, subject, p.config)
		}
		if failure != nil {
			failures = append(failures, api.WorkflowFailure{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureMissingPrerequisite,
					Operation: api.OperationKindUploadDryRun,
					Message:   failure.Message(),
					Recovery:  api.OperationRecoveryCompletePrerequisite,
				},
				TrackerID: trackerID,
			})
		}
		projections = append(projections, projection)
		itemStatus := api.StageStatusCompleted
		message := "Tracker projection complete."
		if len(projection.RequiredActions) > 0 {
			itemStatus = api.StageStatusBlocked
			message = strings.TrimSpace(projection.RequiredActions[0].Prompt)
		} else if projection.Readiness != api.ReadinessStatusReady || !projection.DupeReady {
			itemStatus = api.StageStatusSkipped
			message = projectionIneligibleProgressMessage(projection)
		}
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:     "projection",
			ItemID:    string(trackerID),
			Kind:      "tracker",
			Label:     string(trackerID),
			Status:    itemStatus,
			Completed: index + 1,
			Total:     len(selected),
			Message:   message,
		})
	}
	return projections, failures
}

func projectionIneligibleProgressMessage(projection api.TrackerReleaseProjection) string {
	const fallback = "Tracker is not eligible for duplicate checking."
	for _, decision := range projection.PolicyDecisions {
		if !decision.Blocking && !strings.EqualFold(strings.TrimSpace(decision.Decision), "ineligible") {
			continue
		}
		code := strings.TrimSpace(decision.Code)
		reason := strings.TrimSpace(decision.Message)
		if code == "" || reason == "" {
			continue
		}
		return fmt.Sprintf("%s code=%s reason=%s", fallback, code, reason)
	}
	return fallback
}

func applyWorkflowProjectionRequirements(
	projection *api.TrackerReleaseProjection,
	descriptor Descriptor,
	_ api.UploadSubject,
	cfg config.Config,
) {
	if descriptor.UploadContentMode.UsesImages() {
		projection.Artifacts.ScreenshotCount = trackerConfigFor(cfg, descriptor.Name).ImageCount
		if projection.Artifacts.ScreenshotCount <= 0 {
			projection.Artifacts.ScreenshotCount = cfg.ScreenshotHandling.Screens
		}
	}
	if descriptor.WorkflowMedia != nil {
		projection.Artifacts.DVDMenuCount = max(projection.Artifacts.DVDMenuCount, descriptor.WorkflowMedia.DVDMenuCount)
	}
	projection.Artifacts.Description = descriptor.UploadContentMode.UsesDescription()
	projection.Artifacts.ImageHosting = projection.Artifacts.ImageHosting || len(projection.Artifacts.AllowedImageHosts) > 0
	projection.Artifacts.Torrent = true
}

func applyQuestionnaireInstruction(subject *api.UploadSubject, trackerID api.TrackerID, instruction api.TrackerProjectionInstructions) {
	if len(instruction.Questionnaire) == 0 {
		return
	}
	if subject.TrackerQuestionnaireAnswers == nil {
		subject.TrackerQuestionnaireAnswers = make(map[string]map[string]string)
	}
	answers := make(map[string]string, len(instruction.Questionnaire))
	for key, value := range instruction.Questionnaire {
		if value != nil {
			answers[key] = *value
		}
	}
	subject.TrackerQuestionnaireAnswers[string(trackerID)] = answers
}

func projectionRequestedUploadName(instruction api.TrackerProjectionInstructions) *string {
	if !instruction.UploadReleaseName.Present || instruction.UploadReleaseName.Reset {
		return nil
	}
	value := instruction.UploadReleaseName.Value
	return &value
}

func projectionAdditionalNames(instruction api.TrackerProjectionInstructions) []api.TrackerReleaseName {
	values := make([]api.TrackerReleaseName, 0, len(instruction.AdditionalNames))
	for role, value := range instruction.AdditionalNames {
		if value == nil {
			continue
		}
		values = append(values, api.TrackerReleaseName{
			Role:  api.TrackerReleaseNameRole(strings.TrimSpace(role)),
			Value: strings.TrimSpace(*value),
		})
	}
	return values
}

func safeTrackerConfigFingerprint(trackerConfig config.TrackerConfig) (api.WorkflowFingerprint, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		HasAPIKey          bool
		HasLogin           bool
		HasPasskey         bool
		UploaderStatus     bool
		CustomLayout       string
		TagForCustom       string
		CheckForRules      bool
		ModQ               bool
		Draft              bool
		Anon               bool
		ShowGroupIfAnon    bool
		CheckRequests      bool
		FullMediaInfo      bool
		ImageRehost        bool
		ImageHost          string
		UseSpanishTitle    bool
		UseItalianTitle    bool
		SkipIfRehash       bool
		AddWebSourceToDesc bool
		UseMetadataName    bool
		ImageCount         int
		Channel            string
		APIUpload          bool
		Exclusive          bool
		Internal           bool
	}{
		HasAPIKey:          strings.TrimSpace(trackerConfig.APIKey) != "" || strings.TrimSpace(trackerConfig.PTPAPIKey) != "",
		HasLogin:           strings.TrimSpace(trackerConfig.Username) != "" && strings.TrimSpace(trackerConfig.Password) != "",
		HasPasskey:         strings.TrimSpace(trackerConfig.Passkey) != "",
		UploaderStatus:     trackerConfig.UploaderStatus,
		CustomLayout:       trackerConfig.CustomLayout,
		TagForCustom:       trackerConfig.TagForCustomRelease,
		CheckForRules:      trackerConfig.CheckForRules,
		ModQ:               trackerConfig.ModQ,
		Draft:              trackerConfig.Draft,
		Anon:               trackerConfig.Anon,
		ShowGroupIfAnon:    trackerConfig.ShowGroupIfAnon,
		CheckRequests:      trackerConfig.CheckRequests,
		FullMediaInfo:      trackerConfig.FullMediainfo,
		ImageRehost:        trackerConfig.ImgRehost,
		ImageHost:          trackerConfig.ImageHost,
		UseSpanishTitle:    trackerConfig.UseSpanishTitle,
		UseItalianTitle:    trackerConfig.UseItalianTitle,
		SkipIfRehash:       trackerConfig.SkipIfRehash,
		AddWebSourceToDesc: trackerConfig.AddWebSourceToDesc,
		UseMetadataName:    trackerConfig.UseMetadataName,
		ImageCount:         trackerConfig.ImageCount,
		Channel:            trackerConfig.Channel,
		APIUpload:          trackerConfig.APIUpload,
		Exclusive:          trackerConfig.Exclusive,
		Internal:           trackerConfig.Internal,
	})
	if err != nil {
		return "", fmt.Errorf("trackers: safe config fingerprint: %w", err)
	}
	return fingerprint, nil
}

func trackerConfigConfigured(trackerConfig config.TrackerConfig) bool {
	return strings.TrimSpace(trackerConfig.APIKey) != "" || strings.TrimSpace(trackerConfig.Username) != "" ||
		strings.TrimSpace(trackerConfig.Passkey) != "" || strings.TrimSpace(trackerConfig.PTPAPIKey) != ""
}

func projectionPolicyFingerprint(projections []api.TrackerReleaseProjection) (api.WorkflowFingerprint, error) {
	values := make([]api.WorkflowFingerprint, len(projections))
	for index, projection := range projections {
		values[index] = projection.ProjectorFingerprint
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(values)
	if err != nil {
		return "", fmt.Errorf("trackers: projection policy fingerprint: %w", err)
	}
	return fingerprint, nil
}
