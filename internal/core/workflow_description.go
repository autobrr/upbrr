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
	"time"

	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/pkg/api"
)

type workflowDescriptionSubjectResolver interface {
	ResolveUploadSubject(context.Context, api.UploadSubjectInput) (api.UploadSubject, error)
}

type workflowDescriptionTrackerService interface {
	BuildPreparation(context.Context, api.DescriptionSubject, []string) (api.PreparationPreview, error)
}

type workflowDescriptionBuilder struct {
	resolver workflowDescriptionSubjectResolver
	trackers workflowDescriptionTrackerService
}

func (b workflowDescriptionBuilder) Fingerprints(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	media api.MediaArtifactSet,
	privateMedia any,
	instructions api.DescriptionInstructions,
) (api.WorkflowFingerprint, api.WorkflowFingerprint, error) {
	subject, err := b.resolveSubject(ctx, release, projections, instructions)
	if err != nil {
		return "", "", err
	}
	exactMedia, err := resolveWorkflowExactMedia(privateMedia, media)
	if err != nil {
		return "", "", err
	}
	return workflowDescriptionFingerprints(release, projections, media, instructions, subject, exactMedia)
}

func workflowDescriptionFingerprints(
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	media api.MediaArtifactSet,
	instructions api.DescriptionInstructions,
	subject api.UploadSubject,
	exactMedia *api.ExactMediaAssets,
) (api.WorkflowFingerprint, api.WorkflowFingerprint, error) {
	templateFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Template string
		Version  string
	}{subject.DescriptionTemplate, strings.TrimSpace(instructions.TemplateVersion)})
	if err != nil {
		return "", "", fmt.Errorf("workflow description template fingerprint: %w", err)
	}
	inputFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Release             api.ReleaseRef
		ProjectionID        api.TrackerReleaseProjectionSetID
		ProjectionRevision  api.WorkflowRevision
		ProjectionInput     api.WorkflowFingerprint
		ProjectionPolicy    api.WorkflowFingerprint
		MediaID             api.MediaArtifactSetID
		MediaRevision       api.WorkflowRevision
		MediaCapture        api.WorkflowFingerprint
		MediaRequirements   api.WorkflowFingerprint
		Artifacts           []api.MediaArtifact
		ExactMedia          *api.ExactMediaAssets
		TargetTrackerIDs    []string
		Instructions        api.DescriptionInstructions
		TemplateFingerprint api.WorkflowFingerprint
	}{
		Release:             release,
		ProjectionID:        projections.ID,
		ProjectionRevision:  projections.Revision,
		ProjectionInput:     projections.InputFingerprint,
		ProjectionPolicy:    projections.PolicyFingerprint,
		MediaID:             media.ID,
		MediaRevision:       media.Revision,
		MediaCapture:        media.CaptureFingerprint,
		MediaRequirements:   media.RequirementsFingerprint,
		Artifacts:           media.Artifacts,
		ExactMedia:          exactMedia,
		TargetTrackerIDs:    workflowProjectionTrackerNames(workflowDescriptionTargets(projections.Projections)),
		Instructions:        instructions,
		TemplateFingerprint: templateFingerprint,
	})
	if err != nil {
		return "", "", fmt.Errorf("workflow description input fingerprint: %w", err)
	}
	return inputFingerprint, templateFingerprint, nil
}

func (b workflowDescriptionBuilder) Build(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	media api.MediaArtifactSet,
	privateMedia any,
	instructions api.DescriptionInstructions,
	_ time.Time,
) (api.DescriptionSet, error) {
	if err := ctx.Err(); err != nil {
		return api.DescriptionSet{}, fmt.Errorf("workflow descriptions: %w", err)
	}
	if b.trackers == nil {
		return api.DescriptionSet{}, errors.New("workflow descriptions: tracker service is required")
	}
	subject, err := b.resolveSubject(ctx, release, projections, instructions)
	if err != nil {
		return api.DescriptionSet{}, err
	}
	exactMedia, err := resolveWorkflowExactMedia(privateMedia, media)
	if err != nil {
		return api.DescriptionSet{}, err
	}
	inputFingerprint, templateFingerprint, err := workflowDescriptionFingerprints(
		release,
		projections,
		media,
		instructions,
		subject,
		exactMedia,
	)
	if err != nil {
		return api.DescriptionSet{}, err
	}
	subject.ExactMedia = exactMedia
	skipUpload := true
	subject.ImageHostOverrides.SkipUpload = &skipUpload
	descriptionTargets := workflowDescriptionTargets(projections.Projections)
	trackerNames := workflowProjectionTrackerNames(descriptionTargets)
	if len(descriptionTargets) == 0 {
		return api.DescriptionSet{
			InputFingerprint:    inputFingerprint,
			TemplateFingerprint: templateFingerprint,
			Status:              api.StageStatusSkipped,
		}, nil
	}
	for _, projection := range descriptionTargets {
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:   "descriptions",
			ItemID:  string(projection.TrackerID),
			Kind:    "description_group",
			Label:   projection.DescriptionGroup,
			Status:  api.StageStatusQueued,
			Total:   len(descriptionTargets),
			Message: "Description generation queued.",
		})
	}
	preview, err := b.trackers.BuildPreparation(ctx, api.NewDescriptionSubject(subject), trackerNames)
	if err != nil {
		return api.DescriptionSet{}, fmt.Errorf("workflow descriptions: build tracker preparation: %w", err)
	}
	snapshot := api.DescriptionSet{
		InputFingerprint:    inputFingerprint,
		TemplateFingerprint: templateFingerprint,
		Status:              api.StageStatusCompleted,
	}
	projectionByName := workflowProjectionByName(descriptionTargets)
	trackerResults := make(map[api.TrackerID]api.DescriptionTrackerResult, len(descriptionTargets))
	terminalTrackers := 0
	recordTrackerResult := func(
		trackerID api.TrackerID,
		label string,
		status api.StageStatus,
		message string,
		failure bool,
	) {
		if trackerID == "" {
			return
		}
		if _, exists := trackerResults[trackerID]; exists {
			return
		}
		message = strings.TrimSpace(message)
		trackerResults[trackerID] = api.DescriptionTrackerResult{
			TrackerID: trackerID,
			Status:    status,
			Message:   message,
		}
		if failure {
			snapshot.Failures = append(snapshot.Failures, workflowDescriptionFailure(message, trackerID, label))
		}
		terminalTrackers++
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:     "descriptions",
			ItemID:    string(trackerID),
			Kind:      "description_group",
			Label:     strings.TrimSpace(label),
			Status:    status,
			Completed: terminalTrackers,
			Total:     len(descriptionTargets),
			Message:   message,
		})
	}
	for _, entry := range preview.Descriptions {
		trackerIDs := workflowDescriptionTrackerIDs(entry, descriptionTargets, projectionByName)
		source := strings.TrimSpace(entry.RawDescription)
		if source == "" {
			source = strings.TrimSpace(entry.Description)
		}
		if source == "" {
			message := "Generated description was empty. Review description inputs and retry."
			if len(trackerIDs) == 0 {
				snapshot.Failures = append(snapshot.Failures, workflowDescriptionFailure(message, "", entry.GroupKey))
			}
			for _, trackerID := range trackerIDs {
				recordTrackerResult(trackerID, entry.GroupKey, api.StageStatusFailed, message, true)
			}
			continue
		}
		rendered := strings.TrimSpace(entry.RawDescriptionHTML)
		if rendered == "" {
			rendered = strings.TrimSpace(entry.DescriptionHTML)
		}
		if rendered == "" {
			rendered = description.Render(source)
		}
		contentFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
			GroupKey   string
			TrackerIDs []api.TrackerID
			Source     string
			Rendered   string
			ImageHost  api.ImageHostFeedback
		}{entry.GroupKey, trackerIDs, source, rendered, entry.ImageHost})
		if err != nil {
			return api.DescriptionSet{}, fmt.Errorf("workflow descriptions: content fingerprint: %w", err)
		}
		snapshot.Descriptions = append(snapshot.Descriptions, api.RenderedDescription{
			GroupKey:           strings.TrimSpace(entry.GroupKey),
			TrackerIDs:         trackerIDs,
			Source:             source,
			Rendered:           rendered,
			ContentFingerprint: contentFingerprint,
		})
		for _, trackerID := range trackerIDs {
			recordTrackerResult(trackerID, entry.GroupKey, api.StageStatusCompleted, "Description generated.", false)
		}
	}
	for _, failure := range preview.ContentFailures {
		trackerID := workflowTrackerIDForName(failure.Tracker, projectionByName)
		if failure.Code == api.TrackerContentFailureImageHostUnavailable {
			recordTrackerResult(trackerID, failure.Tracker, api.StageStatusFailed, failure.Message, true)
			continue
		}
		if trackerID == "" {
			snapshot.Failures = append(snapshot.Failures, workflowDescriptionFailure(failure.Message, "", failure.Tracker))
			continue
		}
		recordTrackerResult(trackerID, failure.Tracker, api.StageStatusFailed, failure.Message, true)
	}
	for _, projection := range descriptionTargets {
		if result, ok := trackerResults[projection.TrackerID]; ok {
			snapshot.TrackerResults = append(snapshot.TrackerResults, result)
		}
	}
	switch {
	case len(snapshot.Descriptions) == 0 && len(snapshot.Failures) > 0:
		snapshot.Status = api.StageStatusFailed
	case len(snapshot.Descriptions) == 0:
		snapshot.Status = api.StageStatusSkipped
	}
	return snapshot, nil
}

type workflowExactLocalMedia struct {
	artifact   api.MediaArtifact
	screenshot api.ScreenshotImage
	menu       api.DVDMenuCaptureImage
}

func resolveWorkflowExactMedia(
	privateMedia any,
	media api.MediaArtifactSet,
) (*api.ExactMediaAssets, error) {
	privateArtifacts, ok := privateMedia.(workflowMediaPrivateArtifacts)
	if !ok {
		return nil, errors.New("workflow exact media: retained artifacts are incompatible")
	}
	screenshots := make([]workflowExactLocalMedia, 0, len(privateArtifacts.Screenshots))
	menus := make([]workflowExactLocalMedia, 0, len(privateArtifacts.DVDMenus))
	sourceKinds := make(map[api.PublicResourceID]api.MediaArtifactKind)
	sourceOrders := make(map[api.PublicResourceID]int)
	selectedSources := make(map[api.PublicResourceID]struct{})
	screenshotIndex, menuIndex := 0, 0
	for _, artifact := range media.Artifacts {
		switch artifact.Kind {
		case api.MediaArtifactScreenshot:
			if screenshotIndex >= len(privateArtifacts.Screenshots) {
				return nil, errors.New("workflow exact media: retained screenshots do not match public media")
			}
			image := privateArtifacts.Screenshots[screenshotIndex]
			screenshotIndex++
			if artifact.Purpose != api.ScreenshotPurposeFinal || image.Purpose != api.ScreenshotPurposeFinal {
				return nil, errors.New("workflow exact media: normal screenshot has invalid purpose")
			}
			sourceKinds[artifact.ID] = artifact.Kind
			sourceOrders[artifact.ID] = artifact.Order
			if artifact.Selected {
				selectedSources[artifact.ID] = struct{}{}
				screenshots = append(screenshots, workflowExactLocalMedia{artifact: artifact, screenshot: image})
			}
		case api.MediaArtifactDVDMenu:
			if menuIndex >= len(privateArtifacts.DVDMenus) {
				return nil, errors.New("workflow exact media: retained DVD menus do not match public media")
			}
			menu := privateArtifacts.DVDMenus[menuIndex]
			menuIndex++
			if artifact.Purpose != api.ScreenshotPurposeMenu || menu.Purpose != api.ScreenshotPurposeMenu {
				return nil, errors.New("workflow exact media: DVD menu has invalid purpose")
			}
			sourceKinds[artifact.ID] = artifact.Kind
			sourceOrders[artifact.ID] = artifact.Order
			if artifact.Selected {
				selectedSources[artifact.ID] = struct{}{}
				menus = append(menus, workflowExactLocalMedia{artifact: artifact, menu: menu})
			}
		case api.MediaArtifactHostedImage:
		}
	}
	if screenshotIndex != len(privateArtifacts.Screenshots) || menuIndex != len(privateArtifacts.DVDMenus) {
		return nil, errors.New("workflow exact media: retained artifacts do not match public media")
	}
	slices.SortStableFunc(screenshots, compareLocalMediaOrder)
	slices.SortStableFunc(menus, compareLocalMediaOrder)
	exact := &api.ExactMediaAssets{
		Screenshots: make([]api.ScreenshotImage, 0, len(screenshots)),
		DVDMenus:    make([]api.DVDMenuCaptureImage, 0, len(menus)),
	}
	for _, item := range screenshots {
		exact.Screenshots = append(exact.Screenshots, item.screenshot)
	}
	for _, item := range menus {
		exact.DVDMenus = append(exact.DVDMenus, item.menu)
	}

	type hostedMedia struct {
		artifact    api.MediaArtifact
		sourceOrder int
		upload      api.UploadedImageLink
		kind        api.MediaArtifactKind
	}
	hosted := make([]hostedMedia, 0, len(privateArtifacts.HostedImages))
	for _, artifact := range media.Artifacts {
		if artifact.Kind != api.MediaArtifactHostedImage || !artifact.Selected {
			continue
		}
		upload, exists := privateArtifacts.HostedImages[artifact.ID]
		if !exists {
			return nil, errors.New("workflow exact media: hosted artifact content is unavailable")
		}
		sourceID, exists := privateArtifacts.HostedSources[artifact.ID]
		if !exists {
			return nil, errors.New("workflow exact media: hosted artifact source is unavailable")
		}
		if _, selected := selectedSources[sourceID]; !selected {
			continue
		}
		kind, exists := sourceKinds[sourceID]
		if !exists {
			return nil, errors.New("workflow exact media: hosted artifact source is not local media")
		}
		expectedPurpose := api.ScreenshotPurposeFinal
		if kind == api.MediaArtifactDVDMenu {
			expectedPurpose = api.ScreenshotPurposeMenu
		}
		if artifact.Purpose != expectedPurpose {
			return nil, errors.New("workflow exact media: hosted artifact purpose does not match its source channel")
		}
		hosted = append(hosted, hostedMedia{
			artifact:    artifact,
			sourceOrder: sourceOrders[sourceID],
			upload:      upload,
			kind:        kind,
		})
	}
	slices.SortStableFunc(hosted, func(left, right hostedMedia) int {
		if left.sourceOrder != right.sourceOrder {
			return left.sourceOrder - right.sourceOrder
		}
		if left.artifact.Order != right.artifact.Order {
			return left.artifact.Order - right.artifact.Order
		}
		return strings.Compare(string(left.artifact.ID), string(right.artifact.ID))
	})
	for _, item := range hosted {
		switch item.kind {
		case api.MediaArtifactScreenshot:
			exact.ScreenshotUploads = append(exact.ScreenshotUploads, item.upload)
		case api.MediaArtifactDVDMenu:
			exact.DVDMenuUploads = append(exact.DVDMenuUploads, item.upload)
		case api.MediaArtifactHostedImage:
			return nil, errors.New("workflow exact media: hosted artifact cannot source another hosted artifact")
		}
	}
	if err := exact.Validate(); err != nil {
		return nil, fmt.Errorf("workflow exact media: %w", err)
	}
	return exact, nil
}

func compareLocalMediaOrder(left, right workflowExactLocalMedia) int {
	if left.artifact.Order != right.artifact.Order {
		return left.artifact.Order - right.artifact.Order
	}
	return strings.Compare(string(left.artifact.ID), string(right.artifact.ID))
}

func (b workflowDescriptionBuilder) resolveSubject(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	instructions api.DescriptionInstructions,
) (api.UploadSubject, error) {
	if b.resolver == nil {
		return api.UploadSubject{}, errors.New("workflow descriptions: subject resolver is required")
	}
	descriptionTargets := workflowDescriptionTargets(projections.Projections)
	trackerNames := workflowProjectionTrackerNames(descriptionTargets)
	groups := make([]api.DescriptionBuilderGroup, 0, len(instructions.Overrides))
	for _, override := range instructions.Overrides {
		baseGroup := strings.TrimSpace(strings.SplitN(override.GroupKey, "|", 2)[0])
		trackersForGroup := make([]string, 0, len(descriptionTargets))
		for _, projection := range descriptionTargets {
			if strings.EqualFold(baseGroup, "default") ||
				strings.EqualFold(strings.TrimSpace(projection.DescriptionGroup), baseGroup) {
				trackersForGroup = append(trackersForGroup, string(projection.TrackerID))
			}
		}
		groups = append(groups, api.DescriptionBuilderGroup{
			GroupKey:       strings.TrimSpace(override.GroupKey),
			Trackers:       trackersForGroup,
			Description:    strings.TrimSpace(override.Source),
			RawDescription: strings.TrimSpace(override.Source),
			HasOverride:    true,
		})
	}
	questionnaire := make(map[string]map[string]string, len(instructions.QuestionnaireAnswers))
	for trackerID, answers := range instructions.QuestionnaireAnswers {
		cloned := make(map[string]string, len(answers))
		maps.Copy(cloned, answers)
		questionnaire[string(trackerID)] = cloned
	}
	subject, err := b.resolver.ResolveUploadSubject(ctx, api.UploadSubjectInput{
		Release:                release,
		Trackers:               trackerNames,
		QuestionnaireAnswers:   questionnaire,
		DescriptionGroups:      groups,
		TrackerConfigOverrides: instructions.TrackerConfig,
		TrackerSiteOverrides:   instructions.TrackerSite,
		ClientOverrides:        instructions.Client,
		ImageHostOverrides:     instructions.ImageHost,
		TorrentOverrides:       instructions.Torrent,
		Options:                instructions.Options,
	})
	if err != nil {
		return api.UploadSubject{}, fmt.Errorf("workflow descriptions: resolve subject: %w", err)
	}
	subject.Trackers = trackerNames
	return subject, nil
}

func workflowProjectionTrackerNames(projections []api.TrackerReleaseProjection) []string {
	result := make([]string, len(projections))
	for index, projection := range projections {
		result[index] = string(projection.TrackerID)
	}
	return result
}

func workflowDescriptionTargets(projections []api.TrackerReleaseProjection) []api.TrackerReleaseProjection {
	targets := make([]api.TrackerReleaseProjection, 0, len(projections))
	for _, projection := range projections {
		if projection.Artifacts.Description {
			targets = append(targets, projection)
		}
	}
	return targets
}

func workflowProjectionByName(projections []api.TrackerReleaseProjection) map[string]api.TrackerID {
	result := make(map[string]api.TrackerID, len(projections)*2)
	for _, projection := range projections {
		result[strings.ToUpper(strings.TrimSpace(string(projection.TrackerID)))] = projection.TrackerID
		result[strings.ToUpper(strings.TrimSpace(projection.DisplayName))] = projection.TrackerID
	}
	return result
}

func workflowDescriptionTrackerIDs(
	entry api.PreparationDescription,
	projections []api.TrackerReleaseProjection,
	projectionByName map[string]api.TrackerID,
) []api.TrackerID {
	result := make([]api.TrackerID, 0, len(entry.Trackers))
	for _, tracker := range entry.Trackers {
		if trackerID := workflowTrackerIDForName(tracker, projectionByName); trackerID != "" && !slices.Contains(result, trackerID) {
			result = append(result, trackerID)
		}
	}
	if len(result) > 0 {
		return result
	}
	baseGroup := strings.TrimSpace(strings.SplitN(entry.GroupKey, "|", 2)[0])
	for _, projection := range projections {
		if strings.EqualFold(baseGroup, "default") ||
			strings.EqualFold(strings.TrimSpace(projection.DescriptionGroup), baseGroup) {
			result = append(result, projection.TrackerID)
		}
	}
	return result
}

func workflowTrackerIDForName(name string, projectionByName map[string]api.TrackerID) api.TrackerID {
	return projectionByName[strings.ToUpper(strings.TrimSpace(name))]
}

func workflowDescriptionFailure(message string, trackerID api.TrackerID, resource string) api.WorkflowFailure {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Description generation failed. Review inputs and retry."
	}
	return api.WorkflowFailure{
		Failure: api.OperationFailure{
			Code:      api.OperationFailureMissingPrerequisite,
			Operation: api.OperationKindDescription,
			Message:   message,
			Recovery:  api.OperationRecoveryCompletePrerequisite,
		},
		TrackerID: trackerID,
		Resource:  strings.TrimSpace(resource),
	}
}
