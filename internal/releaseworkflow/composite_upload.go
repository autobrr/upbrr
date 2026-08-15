// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	compositeUploadSessionVersion  = 2
	compositeUploadTransitionLimit = 64
	deprecatedAuthFeedbackMessage  = "Tracker authentication must be resolved outside the upload workflow. Start a fresh attempt."
)

type compositeUploadSession struct {
	Version               int                                         `json:"version"`
	RequestFingerprint    api.WorkflowFingerprint                     `json:"requestFingerprint"`
	Intent                api.WorkflowIntent                          `json:"intent"`
	Goal                  api.WorkflowGoal                            `json:"goal"`
	Confirm               bool                                        `json:"confirm"`
	PreparedRelease       api.ReleaseWorkflowPreparedReleaseMode      `json:"preparedRelease,omitempty"`
	DuplicateDisposition  api.ReleaseWorkflowDuplicateDisposition     `json:"duplicateDisposition"`
	RemoveTrackers        []api.TrackerID                             `json:"removeTrackers,omitempty"`
	DefaultProjection     *api.ReleaseWorkflowUploadTrackerProjection `json:"defaultProjection,omitempty"`
	ManualMedia           compositeUploadManualMedia                  `json:"manualMedia,omitempty"`
	ActiveOperationID     api.WorkflowOperationID                     `json:"activeOperationId,omitempty"`
	LastOperationID       api.WorkflowOperationID                     `json:"lastOperationId,omitempty"`
	LastCommittedRevision api.WorkflowRevision                        `json:"lastCommittedRevision"`
	Cursor                string                                      `json:"cursor,omitempty"`
	ApprovedDryRun        *api.UploadDryRunResultRef                  `json:"approvedDryRun,omitempty"`
	ApprovedFingerprint   api.WorkflowFingerprint                     `json:"approvedFingerprint,omitempty"`
	ApprovedTrackerIDs    []api.TrackerID                             `json:"approvedTrackerIds,omitempty"`
	FeedbackSequence      uint64                                      `json:"feedbackSequence,omitempty"`
	FeedbackReceipts      map[string]compositeUploadFeedbackReceipt   `json:"feedbackReceipts,omitempty"`
	TerminalReason        string                                      `json:"terminalReason,omitempty"`
}

type compositeUploadManualMedia struct {
	Attachments []api.MediaAttachment `json:"attachments,omitempty"`
	Attached    bool                  `json:"attached,omitempty"`
}

type compositeUploadFeedbackReceipt struct {
	Fingerprint api.WorkflowFingerprint `json:"fingerprint"`
	Revision    api.WorkflowRevision    `json:"revision"`
	Sequence    uint64                  `json:"sequence"`
}

type compositeUploadMediaInputsContextKey struct{}

// CompositeUploadMediaInputs contains adapter-resolved local image bytes.
// Values live only in the request context until they are staged in the private vault.
type CompositeUploadMediaInputs struct {
	Comparisons []StagedMediaContent
	DVDMenus    []StagedMediaContent
}

// WithCompositeUploadMediaInputs supplies private adapter-resolved media to StartUpload.
func WithCompositeUploadMediaInputs(ctx context.Context, inputs CompositeUploadMediaInputs) context.Context {
	return context.WithValue(ctx, compositeUploadMediaInputsContextKey{}, inputs)
}

func compositeUploadMediaInputsFromContext(ctx context.Context) CompositeUploadMediaInputs {
	inputs, _ := ctx.Value(compositeUploadMediaInputsContextKey{}).(CompositeUploadMediaInputs)
	return inputs
}

// CompositeUploadCommand resumes retained workflow intent from its durable
// checkpoint and drives it to the upload or debug goal.
type CompositeUploadCommand struct {
	WorkflowID         api.WorkflowID
	ExpectedRevision   api.WorkflowRevision
	SessionFingerprint api.WorkflowFingerprint
	Goal               api.WorkflowGoal
	IdempotencyKey     string
}

func (CompositeUploadCommand) commandName() string { return "composite_upload" }
func (CompositeUploadCommand) userIntent()         {}
func (c CompositeUploadCommand) operationKind() api.OperationKind {
	if c.Goal == api.WorkflowGoalDryRun {
		return api.OperationKindUploadDryRun
	}
	return api.OperationKindUploadExecute
}
func (c CompositeUploadCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		SessionFingerprint api.WorkflowFingerprint
		Goal               api.WorkflowGoal
	}{
		SessionFingerprint: c.SessionFingerprint,
		Goal:               c.Goal,
	})
}

type applyCompositeUploadFeedbackCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	ActionID         api.RequiredActionID
	Response         compositeUploadFeedbackResponse
	IdempotencyKey   string
}

func (applyCompositeUploadFeedbackCommand) commandName() string {
	return "apply_composite_upload_feedback"
}
func (c applyCompositeUploadFeedbackCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ActionID api.RequiredActionID
		Response compositeUploadFeedbackResponse
	}{
		ActionID: c.ActionID,
		Response: c.Response,
	})
}

type compositeUploadFeedbackResponse struct {
	Kind              api.ReleaseWorkflowUploadFeedbackKind
	SelectedValues    []string
	Confirmed         bool
	UseAll            bool
	TrackerID         api.TrackerID
	Projection        *api.ReleaseWorkflowUploadTrackerProjection
	Questionnaire     map[string]*string
	DuplicateDecision api.DupeDecision
	TrackerIDs        []api.TrackerID
	Facts             *api.ReleaseWorkflowUploadFacts
	Preparation       *api.ReleaseWorkflowUploadPreparation
	Reconciliation    string
}

// StartUpload validates and normalizes one owner-scoped request, idempotently
// creates its workflow, stages private media, and starts the durable composite
// operation unless the same request is already active or terminal.
func (m *Module) StartUpload(
	ctx context.Context,
	ownerID string,
	request api.CreateReleaseWorkflowUploadRequest,
) (CommandResult, error) {
	if err := request.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow start upload: %w", err)
	}
	session, instructions, err := normalizeCompositeUploadRequest(request)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow normalize upload: %w", err)
	}
	created, err := m.Execute(ctx, ownerID, CreateWorkflowCommand{
		Instructions:        instructions,
		IdempotencyKey:      request.IdempotencyKey,
		RequestFingerprint:  session.RequestFingerprint,
		TrackerDecisionMode: TrackerDecisionModePostDupeGate,
		Composite:           session,
	})
	if err != nil {
		return CommandResult{}, err
	}
	current, err := m.Current(ctx, ownerID, created.Workflow.ID)
	if err != nil {
		return CommandResult{}, err
	}
	if current.Operation != nil && current.Operation.Command == (CompositeUploadCommand{}).commandName() {
		return current, nil
	}
	if current.Workflow.Status == api.WorkflowStatusCompleted || current.UploadResult != nil ||
		(session.Goal == api.WorkflowGoalDryRun && current.DryRun != nil) {
		return current, nil
	}
	if err := m.ensureCompositeUploadMediaInputs(
		ctx,
		ownerID,
		current.Workflow.ID,
		current.Workflow.Revision,
		request,
		compositeUploadMediaInputsFromContext(ctx),
	); err != nil {
		return CommandResult{}, err
	}
	current, err = m.Current(ctx, ownerID, created.Workflow.ID)
	if err != nil {
		return CommandResult{}, err
	}
	operation, err := m.Start(ctx, ownerID, CompositeUploadCommand{
		WorkflowID:         current.Workflow.ID,
		ExpectedRevision:   current.Workflow.Revision,
		SessionFingerprint: session.RequestFingerprint,
		Goal:               session.Goal,
		IdempotencyKey:     compositeUploadOperationKey(request.IdempotencyKey, 0),
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow start composite upload: %w", err)
	}
	current, err = m.Current(ctx, ownerID, operation.WorkflowID)
	if err != nil {
		return CommandResult{}, err
	}
	current.Operation = &operation
	return current, nil
}

func normalizeCompositeUploadRequest(
	request api.CreateReleaseWorkflowUploadRequest,
) (*compositeUploadSession, api.ReleaseFactInstructions, error) {
	sourcePath := filepath.FromSlash(strings.TrimSpace(request.Source.Path))
	sourcePath = filepath.Clean(sourcePath)
	if sourcePath == "." || sourcePath == "" {
		return nil, api.ReleaseFactInstructions{}, errors.New("source path is required")
	}
	instructions, err := compositeUploadFactInstructions(request.Preparation.Facts, request.Trackers.SourceIDs)
	if err != nil {
		return nil, api.ReleaseFactInstructions{}, err
	}
	interaction := api.InteractionModeUnattended
	if request.Unattended.Confirm {
		interaction = api.InteractionModeUnattendedConfirm
	}
	mode := api.WorkflowExecutionModeNormal
	goal := api.WorkflowGoalUploaded
	if request.Execution.Mode == api.ReleaseWorkflowUploadModeDebug {
		mode = api.WorkflowExecutionModeDebug
		goal = api.WorkflowGoalDryRun
	}
	prepareInput := api.PrepareInput{
		SourcePath:   sourcePath,
		Intent:       api.PreparationIntentUpload,
		Instructions: instructions,
		Policy: api.PreparationPolicy{
			KeepFolder: optionalBool(request.Preparation.Policy.KeepFolder),
			KeepImages: optionalBool(request.Preparation.Policy.KeepImages),
			OnlyID:     optionalBool(request.Preparation.Policy.OnlyID),
		},
		Search: api.ClientSearchPolicy{
			Skip:   optionalBool(request.Preparation.ClientSearch.Skip),
			Client: cloneStringPointer(request.Preparation.ClientSearch.Client),
		},
		Controls: api.PreparationControls{
			Interaction:       interaction,
			ConfirmBDMVRescan: request.Preparation.ConfirmRescan,
			ForceRecheck:      cloneBoolPointer(request.Client.ForceRecheck),
		},
		Force: request.Preparation.Force,
	}
	if request.Execution.PreparedRelease == api.ReleaseWorkflowPreparedReleaseRequire {
		prepareInput.RequirePrepared = true
	}
	projections := compositeUploadProjectionInstructions(request.Trackers.Projection)
	trackerIDs := normalizeCompositeTrackerIDs(request.Trackers.Include)
	if len(trackerIDs) > 0 {
		trackerIDs = slices.DeleteFunc(trackerIDs, func(id api.TrackerID) bool {
			return slices.Contains(normalizeCompositeTrackerIDs(request.Trackers.Remove), id)
		})
	}
	checkCount := uint8(1)
	if request.Duplicates.CheckCount != nil && *request.Duplicates.CheckCount == 2 {
		checkCount = 2
	}
	decisions := make(map[api.TrackerID]api.DupeDecision)
	for _, trackerID := range normalizeCompositeTrackerIDs(request.Duplicates.AllowUpload) {
		decisions[trackerID] = api.DupeDecisionIgnored
	}
	media, selection := compositeUploadMediaIntent(request.Media)
	descriptions, err := compositeUploadDescriptionIntent(request, interaction)
	if err != nil {
		return nil, api.ReleaseFactInstructions{}, err
	}
	intent := api.WorkflowIntent{
		FactInstructions:       &instructions,
		Preparation:            &prepareInput,
		Interaction:            interaction,
		ExecutionMode:          mode,
		TrackerIDs:             trackerIDs,
		ProjectionInstructions: projections,
		SkipRemoteDuplicates:   request.Duplicates.RemoteCheck != nil && !*request.Duplicates.RemoteCheck,
		DuplicateCheckCount:    checkCount,
		DuplicateDecisions:     decisions,
		Media:                  &media,
		MediaSelection:         selection,
		Descriptions:           &descriptions,
		NoSeed:                 optionalBool(request.Client.NoSeed),
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(requestFingerprintPayload(request))
	if err != nil {
		return nil, api.ReleaseFactInstructions{}, fmt.Errorf("fingerprint upload request: %w", err)
	}
	onEvidence := request.Duplicates.OnEvidence
	if onEvidence == "" {
		if request.Unattended.Confirm {
			onEvidence = api.ReleaseWorkflowDuplicateAsk
		} else {
			onEvidence = api.ReleaseWorkflowDuplicateBlock
		}
	}
	session := &compositeUploadSession{
		Version:              compositeUploadSessionVersion,
		RequestFingerprint:   fingerprint,
		Intent:               intent,
		Goal:                 goal,
		Confirm:              request.Unattended.Confirm,
		PreparedRelease:      request.Execution.PreparedRelease,
		DuplicateDisposition: onEvidence,
		RemoveTrackers:       normalizeCompositeTrackerIDs(request.Trackers.Remove),
		DefaultProjection:    cloneCompositeUploadProjection(request.Trackers.DefaultProjection),
		FeedbackReceipts:     make(map[string]compositeUploadFeedbackReceipt),
	}
	return session, instructions, nil
}

func cloneCompositeUploadProjection(
	value *api.ReleaseWorkflowUploadTrackerProjection,
) *api.ReleaseWorkflowUploadTrackerProjection {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.AdditionalNames = cloneStringPointerMap(value.AdditionalNames)
	cloned.Questionnaire = cloneStringPointerMap(value.Questionnaire)
	return &cloned
}

func (m *Module) ensureCompositeUploadMediaInputs(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	expectedRevision api.WorkflowRevision,
	request api.CreateReleaseWorkflowUploadRequest,
	inputs CompositeUploadMediaInputs,
) error {
	requiresComparisons := len(request.Media.Screenshots.ComparisonPaths) > 0
	requiresMenus := len(request.Media.DVDMenus.MenuPaths) > 0
	if !requiresComparisons && !requiresMenus {
		return nil
	}
	if requiresComparisons && len(inputs.Comparisons) == 0 {
		return errors.New("release workflow start upload: comparison paths were not resolved by the input adapter")
	}
	if requiresMenus && len(inputs.DVDMenus) == 0 {
		return errors.New("release workflow start upload: DVD menu paths were not resolved by the input adapter")
	}
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return fmt.Errorf("release workflow load staged upload media: %w", err)
	}
	if state.Composite == nil {
		return fmt.Errorf("%w: composite upload session is unavailable", ErrInvalidTransition)
	}
	if len(state.Composite.ManualMedia.Attachments) > 0 {
		return nil
	}
	attachments := make([]api.MediaAttachment, 0, len(inputs.Comparisons)+len(inputs.DVDMenus))
	stage := func(content StagedMediaContent, kind api.MediaArtifactKind, purpose api.ScreenshotPurpose) error {
		resource, stageErr := m.StageMediaResource(ctx, ownerID, workflowID, expectedRevision, content)
		if stageErr != nil {
			return stageErr
		}
		attachments = append(attachments, api.MediaAttachment{
			Resource: resource,
			Kind:     kind,
			Purpose:  purpose,
			Order:    len(attachments),
		})
		return nil
	}
	for _, content := range inputs.Comparisons {
		if err := stage(content, api.MediaArtifactScreenshot, api.ScreenshotPurposeFinal); err != nil {
			return fmt.Errorf("release workflow stage comparison image: %w", err)
		}
	}
	for _, content := range inputs.DVDMenus {
		if err := stage(content, api.MediaArtifactDVDMenu, api.ScreenshotPurposeMenu); err != nil {
			return fmt.Errorf("release workflow stage DVD menu image: %w", err)
		}
	}
	lock := m.commandLock(strings.TrimSpace(ownerID) + "\x00" + string(workflowID))
	lock.Lock()
	defer lock.Unlock()
	state, err = m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return fmt.Errorf("release workflow reload staged upload media: %w", err)
	}
	if state.Workflow.Revision != expectedRevision || state.Composite == nil {
		return ErrRevisionConflict
	}
	if len(state.Composite.ManualMedia.Attachments) == 0 {
		state.Composite.ManualMedia.Attachments = attachments
		if err := m.saveCompositeMetadata(ctx, ownerID, &state); err != nil {
			return fmt.Errorf("release workflow retain staged upload media: %w", err)
		}
	}
	return nil
}

func requestFingerprintPayload(request api.CreateReleaseWorkflowUploadRequest) api.CreateReleaseWorkflowUploadRequest {
	request.IdempotencyKey = ""
	return request
}

func compositeUploadFactInstructions(
	facts api.ReleaseWorkflowUploadFacts,
	sourceIDs map[api.TrackerID]string,
) (api.ReleaseFactInstructions, error) {
	identity := api.ExternalIDOverrides{}
	if facts.ExternalIDs.TMDB != nil {
		identity.TMDBID = cloneIntPointer(facts.ExternalIDs.TMDB.Value)
	}
	if facts.ExternalIDs.TVDB != nil {
		identity.TVDBID = cloneIntPointer(facts.ExternalIDs.TVDB.Value)
	}
	if facts.ExternalIDs.TVmaze != nil {
		identity.TVmazeID = cloneIntPointer(facts.ExternalIDs.TVmaze.Value)
	}
	if facts.ExternalIDs.MAL != nil {
		identity.MALID = cloneIntPointer(facts.ExternalIDs.MAL.Value)
	}
	if facts.ExternalIDs.IMDB != nil && facts.ExternalIDs.IMDB.Value != nil {
		value := strings.TrimSpace(*facts.ExternalIDs.IMDB.Value)
		id := 0
		if value != "" {
			parsed, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(value), "tt"))
			if err != nil {
				return api.ReleaseFactInstructions{}, errors.New("IMDb ID is invalid")
			}
			id = parsed
		}
		identity.IMDBID = &id
	}
	trackerIDs := make(map[string]string, len(sourceIDs))
	for trackerID, sourceID := range sourceIDs {
		normalized := strings.ToUpper(strings.TrimSpace(string(trackerID)))
		if normalized != "" {
			trackerIDs[normalized] = strings.TrimSpace(sourceID)
		}
	}
	releaseName := facts.ReleaseName
	instructions := api.ReleaseFactInstructions{
		Identity: identity,
		Category: facts.Category,
		ReleaseName: api.ReleaseNameOverrides{
			Category:         cloneStringPointer(releaseName.Category),
			Type:             cloneStringPointer(releaseName.Type),
			Source:           cloneStringPointer(releaseName.Source),
			Resolution:       cloneStringPointer(releaseName.Resolution),
			Tag:              cloneStringPointer(releaseName.Tag),
			Service:          cloneStringPointer(releaseName.Service),
			Edition:          cloneStringPointer(releaseName.Edition),
			Season:           cloneStringPointer(releaseName.Season),
			Episode:          cloneStringPointer(releaseName.Episode),
			EpisodeTitle:     cloneStringPointer(releaseName.EpisodeTitle),
			ManualYear:       cloneIntPointer(releaseName.ManualYear),
			ManualDate:       cloneStringPointer(releaseName.Daily),
			UseSeasonEpisode: cloneBoolPointer(releaseName.UseSeasonEpisode),
			NoSeason:         cloneBoolPointer(releaseName.NoSeason),
			NoYear:           cloneBoolPointer(releaseName.NoYear),
			NoAKA:            cloneBoolPointer(releaseName.NoAKA),
			NoTag:            cloneBoolPointer(releaseName.NoTag),
			NoEpisodeTitle:   cloneBoolPointer(releaseName.NoEpisodeTitle),
			NoDistributor:    cloneBoolPointer(releaseName.NoDistributor),
			NoEdition:        cloneBoolPointer(releaseName.NoEdition),
			NoDub:            cloneBoolPointer(releaseName.NoDub),
			NoDual:           cloneBoolPointer(releaseName.NoDual),
			DualAudio:        cloneBoolPointer(releaseName.DualAudio),
			Region:           cloneStringPointer(releaseName.Region),
		},
		Metadata: api.MetadataOverrides{
			Distributor:      cloneStringPointer(facts.Metadata.Distributor),
			OriginalLanguage: cloneStringPointer(facts.Metadata.OriginalLanguage),
			PersonalRelease:  cloneBoolPointer(facts.Metadata.PersonalRelease),
			Commentary:       cloneBoolPointer(facts.Metadata.Commentary),
			WebDV:            cloneBoolPointer(facts.Metadata.WebDV),
			StreamOptimized:  cloneBoolPointer(facts.Metadata.StreamOptimized),
			Anime:            cloneBoolPointer(facts.Metadata.Anime),
		},
		TrackerIDs: trackerIDs,
	}
	if facts.SourceLookup != nil {
		instructions.SourceLookup = strings.TrimSpace(*facts.SourceLookup)
	}
	if facts.Playlist != nil {
		instructions.Playlist = api.PlaylistInstruction{
			Set:      true,
			Selected: cloneTrimmedStrings(facts.Playlist.Selected),
			UseAll:   facts.Playlist.UseAll,
		}
	}
	return instructions, nil
}

func compositeUploadProjectionInstructions(
	input map[api.TrackerID]api.ReleaseWorkflowUploadTrackerProjection,
) map[api.TrackerID]api.TrackerProjectionInstructions {
	if input == nil {
		return nil
	}
	result := make(map[api.TrackerID]api.TrackerProjectionInstructions, len(input))
	for trackerID, projection := range input {
		normalized := api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if normalized == "" {
			continue
		}
		result[normalized] = api.TrackerProjectionInstructions{
			UploadReleaseName: projection.UploadReleaseName,
			AdditionalNames:   cloneStringPointerMap(projection.AdditionalNames),
			Questionnaire:     cloneStringPointerMap(projection.Questionnaire),
			TrackerConfig: api.TrackerConfigOverrides{
				Anon:    cloneBoolPointer(projection.Config.Anon),
				Draft:   cloneBoolPointer(projection.Config.Draft),
				ModQ:    cloneBoolPointer(projection.Config.ModQ),
				Channel: cloneStringPointer(projection.Config.Channel),
			},
			TrackerSite: api.TrackerSiteOverrides{
				TIK: api.TIKOverrides{
					Foreign:  cloneBoolPointer(projection.Site.TIK.Foreign),
					Opera:    cloneBoolPointer(projection.Site.TIK.Opera),
					Asian:    cloneBoolPointer(projection.Site.TIK.Asian),
					DiscType: cloneStringPointer(projection.Site.TIK.DiscType),
				},
			},
		}
	}
	return result
}

func compositeUploadMediaIntent(
	input api.ReleaseWorkflowUploadMedia,
) (api.MediaCaptureInstructions, *api.WorkflowMediaSelection) {
	count := 0
	if input.Screenshots.Count != nil {
		count = *input.Screenshots.Count
	}
	selections := make([]api.ScreenshotSelection, 0, len(input.Screenshots.Frames))
	for index, frame := range input.Screenshots.Frames {
		selections = append(selections, api.ScreenshotSelection{
			Index:  index,
			Frame:  frame,
			Source: "manual",
		})
	}
	media := api.MediaCaptureInstructions{
		ScreenshotCount: count,
		Purpose:         api.ScreenshotPurposeFinal,
		Selections:      selections,
		CaptureDVDMenus: optionalBool(input.DVDMenus.Capture),
	}
	if input.DVDMenus.MaxItems != nil {
		media.MaxDVDMenuItems = *input.DVDMenus.MaxItems
	}
	var selection *api.WorkflowMediaSelection
	if input.Screenshots.ArtifactIDs != nil {
		selection = &api.WorkflowMediaSelection{ArtifactIDs: append([]api.PublicResourceID(nil), input.Screenshots.ArtifactIDs...)}
	}
	return media, selection
}

func compositeUploadDescriptionIntent(
	request api.CreateReleaseWorkflowUploadRequest,
	interaction api.InteractionMode,
) (api.DescriptionInstructions, error) {
	overrides := make([]api.DescriptionOverrideInput, 0, len(request.Descriptions.Overrides))
	for _, override := range request.Descriptions.Overrides {
		if override.Inline == nil {
			return api.DescriptionInstructions{}, errors.New("description file and URL inputs must be resolved by the input adapter")
		}
		overrides = append(overrides, api.DescriptionOverrideInput{
			GroupKey: strings.TrimSpace(override.GroupKey),
			Source:   *override.Inline,
		})
	}
	options := api.UploadOptions{
		InteractionMode: interaction,
		NoSeed:          optionalBool(request.Client.NoSeed),
		SkipAutoTorrent: optionalBool(request.Client.SkipAutoDiscovery),
	}
	if request.Media.Screenshots.Count != nil {
		options.Screens = *request.Media.Screenshots.Count
	}
	if request.Execution.RunLogLevel != nil {
		options.RunLogLevel = *request.Execution.RunLogLevel
	}
	templateVersion := ""
	if request.Descriptions.TemplateVersion != nil {
		templateVersion = *request.Descriptions.TemplateVersion
	}
	return api.DescriptionInstructions{
		Overrides: overrides,
		Options:   options,
		ImageHost: api.ImageHostOverrides{
			PreferredHost: cloneStringPointer(request.ImageHosting.PreferredHost),
			SkipUpload:    cloneBoolPointer(request.ImageHosting.SkipUpload),
		},
		Client: api.ClientOverrides{
			Client:       cloneStringPointer(request.Client.Selected),
			QbitCategory: cloneStringPointer(request.Client.QbitCategory),
			QbitTag:      cloneStringPointer(request.Client.QbitTag),
			ForceRecheck: cloneBoolPointer(request.Client.ForceRecheck),
		},
		Torrent: api.TorrentOverrides{
			InfoHash:        cloneStringPointer(request.Torrent.InfoHash),
			MaxPieceSizeMiB: cloneIntPointer(request.Torrent.MaxPieceSizeMiB),
			NoHash:          cloneBoolPointer(request.Torrent.NoHash),
			Rehash:          cloneBoolPointer(request.Torrent.Rehash),
		},
		TemplateVersion: templateVersion,
	}, nil
}

func normalizeCompositeTrackerIDs(values []api.TrackerID) []api.TrackerID {
	result := make([]api.TrackerID, 0, len(values))
	for _, value := range values {
		normalized := api.TrackerID(strings.ToUpper(strings.TrimSpace(string(value))))
		if normalized != "" && !slices.Contains(result, normalized) {
			result = append(result, normalized)
		}
	}
	slices.Sort(result)
	return result
}

func optionalBool(value *bool) bool {
	return value != nil && *value
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTrimmedStrings(values []string) []string {
	if values == nil {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func cloneStringPointerMap(values map[string]*string) map[string]*string {
	if values == nil {
		return nil
	}
	result := make(map[string]*string, len(values))
	for key, value := range values {
		result[key] = cloneStringPointer(value)
	}
	return result
}

func compositeUploadOperationKey(base string, sequence uint64) string {
	return fmt.Sprintf("%s:composite:%d", strings.TrimSpace(base), sequence)
}

func (m *Module) checkpointCompositeStage(
	state *State,
	operationID api.WorkflowOperationID,
	command mutation,
	nextRevision api.WorkflowRevision,
) {
	if state.Composite == nil || state.Composite.ActiveOperationID != operationID {
		return
	}
	state.Composite.Cursor = command.commandName()
	state.Composite.LastCommittedRevision = nextRevision
	state.Composite.ApprovedDryRun = clearStaleCompositeApproval(state.Composite, state.Workflow)
	switch command.(type) {
	case PrepareReleaseCommand, ResetReleaseCommand:
		if state.Composite.Intent.Preparation != nil {
			state.Composite.Intent.Preparation.Controls.ConfirmBDMVRescan = false
		}
	}
	if _, attached := command.(AttachMediaArtifactsCommand); attached {
		state.Composite.ManualMedia.Attached = true
	}
}

func clearStaleCompositeApproval(
	session *compositeUploadSession,
	workflow api.ReleaseWorkflow,
) *api.UploadDryRunResultRef {
	if session.ApprovedDryRun == nil || workflow.DryRun == nil || *session.ApprovedDryRun != *workflow.DryRun {
		session.ApprovedFingerprint = ""
		session.ApprovedTrackerIDs = nil
		return nil
	}
	return session.ApprovedDryRun
}

func (m *Module) saveCompositeMetadata(
	ctx context.Context,
	ownerID string,
	state *State,
) error {
	if state == nil || state.Composite == nil {
		return fmt.Errorf("%w: composite upload session is unavailable", ErrInvalidTransition)
	}
	expected := state.Workflow.Revision
	nextRevision := expected + 1
	state.Workflow.Revision = nextRevision
	state.Workflow.UpdatedAt = m.clock.Now().UTC()
	for index := range state.Workflow.RequiredActions {
		state.Workflow.RequiredActions[index].WorkflowRevision = nextRevision
	}
	state.Composite.LastCommittedRevision = nextRevision
	if err := state.Workflow.Validate(); err != nil {
		return fmt.Errorf("validate composite metadata checkpoint: %w", err)
	}
	if err := m.repository.Save(ctx, ownerID, expected, *state); err != nil {
		return fmt.Errorf("save composite metadata checkpoint: %w", err)
	}
	return nil
}

func (m *Module) finishCompositeSession(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	reason string,
) error {
	return m.checkpointCompositeTerminalSession(ctx, ownerID, workflowID, operationID, reason, nil)
}

func (m *Module) failCompositeSession(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	reason string,
	failure api.OperationFailure,
) error {
	return m.checkpointCompositeTerminalSession(ctx, ownerID, workflowID, operationID, reason, &failure)
}

func (m *Module) checkpointCompositeTerminalSession(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	reason string,
	failure *api.OperationFailure,
) error {
	lock := m.commandLock(strings.TrimSpace(ownerID) + "\x00" + string(workflowID))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(context.WithoutCancel(ctx), ownerID, workflowID)
	if err != nil {
		return fmt.Errorf("release workflow finish composite session: %w", err)
	}
	if state.Composite == nil || state.Composite.ActiveOperationID != operationID {
		return nil
	}
	state.Composite.ActiveOperationID = ""
	state.Composite.LastOperationID = operationID
	state.Composite.TerminalReason = strings.TrimSpace(reason)
	if failure != nil {
		state.Workflow.Status = api.WorkflowStatusFailed
		state.Workflow.RequiredActions = nil
		state.Workflow.Failures = []api.WorkflowFailure{{Failure: *failure}}
	}
	if err := m.saveCompositeMetadata(context.WithoutCancel(ctx), ownerID, &state); err != nil {
		return fmt.Errorf("release workflow save composite terminal checkpoint: %w", err)
	}
	return nil
}

func (m *Module) runCompositeUpload(
	ctx context.Context,
	ownerID string,
	command CompositeUploadCommand,
) (CommandResult, error) {
	operationID, _ := ctx.Value(operationExecutionContextKey{}).(api.WorkflowOperationID)
	initial, session, err := m.currentCompositeUpload(ctx, ownerID, command, operationID)
	if err != nil {
		return CommandResult{}, err
	}
	if err := m.hydrateCompositePreparedRelease(ctx, initial, session); err != nil {
		return CommandResult{}, err
	}
	for range compositeUploadTransitionLimit {
		if err := ctx.Err(); err != nil {
			return CommandResult{}, fmt.Errorf("release workflow composite upload: %w", err)
		}
		current, session, err := m.currentCompositeUpload(ctx, ownerID, command, operationID)
		if err != nil {
			return CommandResult{}, err
		}
		if compositeUploadGoalReached(current, session) {
			if err := m.finishCompositeSession(ctx, ownerID, command.WorkflowID, operationID, "goal_reached"); err != nil {
				return CommandResult{}, err
			}
			return m.Current(ctx, ownerID, command.WorkflowID)
		}
		if compositeUploadAllTrackersRemoved(current, session) {
			if err := m.finishCompositeSession(ctx, ownerID, command.WorkflowID, operationID, "no_trackers_remaining"); err != nil {
				return CommandResult{}, err
			}
			return CommandResult{}, api.NewOperationError(api.OperationFailure{
				Code:      api.OperationFailureNoEligibleTrackers,
				Operation: command.operationKind(),
				Message:   "No trackers remain after unavailable trackers were skipped.",
				Recovery:  api.OperationRecoverySelectTrackers,
			}, errors.New("release workflow composite upload has no remaining trackers"))
		}
		if blocked := compositeUploadPendingAction(current, session); blocked != nil {
			if err := m.finishCompositeSession(ctx, ownerID, command.WorkflowID, operationID, "feedback_required"); err != nil {
				return CommandResult{}, err
			}
			return m.Current(ctx, ownerID, command.WorkflowID)
		}
		if changed, updateErr := m.applyCompositeAutomaticPolicy(ctx, ownerID, current, session, operationID); updateErr != nil {
			return CommandResult{}, updateErr
		} else if changed {
			continue
		}
		if current.Media != nil && len(session.ManualMedia.Attachments) > 0 && !session.ManualMedia.Attached {
			m.reportCompositeStage(
				ctx,
				ownerID,
				command.WorkflowID,
				operationID,
				current,
				"capture-media",
				api.StageStatusRunning,
				0,
			)
			if _, err := m.execute(ctx, ownerID, AttachMediaArtifactsCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Media:            current.Workflow.Media,
				Attachments:      append([]api.MediaAttachment(nil), session.ManualMedia.Attachments...),
				IdempotencyKey: compositeUploadOperationKey(
					string(session.RequestFingerprint)+":attach-media",
					uint64(current.Workflow.Revision),
				),
			}); err != nil {
				return CommandResult{}, fmt.Errorf("release workflow composite attach media: %w", err)
			}
			continue
		}
		request := api.ContinueReleaseWorkflowRequest{
			Authority: &api.WorkflowAuthority{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
			},
			IdempotencyKey: compositeUploadOperationKey(string(session.RequestFingerprint), uint64(current.Workflow.Revision)),
			Goal:           session.Goal,
			Intent:         session.Intent,
		}
		next, stage := planContinuationCommand(request, current, m.clock.Now().UTC())
		if next == nil {
			if stage == "no-eligible-trackers" {
				failure := compositeNoEligibleTrackersFailure(current, command.operationKind())
				if err := m.failCompositeSession(
					ctx,
					ownerID,
					command.WorkflowID,
					operationID,
					"no_eligible_trackers",
					failure,
				); err != nil {
					return CommandResult{}, err
				}
				return CommandResult{}, api.NewOperationError(
					failure,
					errors.New("release workflow composite upload has no eligible tracker lanes"),
				)
			}
			if err := m.finishCompositeSession(ctx, ownerID, command.WorkflowID, operationID, "no_viable_transition"); err != nil {
				return CommandResult{}, err
			}
			return m.Current(ctx, ownerID, command.WorkflowID)
		}
		m.reportCompositeStage(ctx, ownerID, command.WorkflowID, operationID, current, stage, api.StageStatusRunning, 0)
		if _, err := m.execute(ctx, ownerID, next); err != nil {
			return CommandResult{}, fmt.Errorf("release workflow composite %s: %w", stage, err)
		}
		updated, err := m.Current(ctx, ownerID, command.WorkflowID)
		if err != nil {
			return CommandResult{}, err
		}
		m.reportCompositeStage(ctx, ownerID, command.WorkflowID, operationID, updated, stage, api.StageStatusCompleted, 100)
	}
	if err := m.finishCompositeSession(ctx, ownerID, command.WorkflowID, operationID, "transition_limit"); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{}, api.NewOperationError(api.OperationFailure{
		Code:      api.OperationFailureInternal,
		Operation: command.operationKind(),
		Message:   "The upload exceeded its defensive transition limit.",
		Recovery:  api.OperationRecoveryRetry,
	}, errors.New("release workflow composite upload transition limit exceeded"))
}

func compositeNoEligibleTrackersFailure(current CommandResult, operation api.OperationKind) api.OperationFailure {
	failure := api.OperationFailure{
		Code:      api.OperationFailureNoEligibleTrackers,
		Operation: operation,
		Message:   "No selected trackers are eligible for this upload attempt.",
		Recovery:  api.OperationRecoverySelectTrackers,
	}
	if compositeAllTrackerLanesAuthBlocked(current) {
		failure.Message = "No selected trackers are eligible because tracker authentication is not ready. Resolve authentication, then restart the upload workflow."
		failure.Recovery = api.OperationRecoveryAuthenticateTrackers
	}
	return failure
}

func compositeAllTrackerLanesAuthBlocked(current CommandResult) bool {
	if current.Projections == nil || current.Preflight == nil || len(current.Projections.Projections) == 0 {
		return false
	}
	authBlocked := make(map[api.TrackerID]struct{}, len(current.Preflight.Results))
	for _, result := range current.Preflight.Results {
		if slices.ContainsFunc(result.Failures, func(failure api.WorkflowFailure) bool {
			return failure.Failure.Code == api.OperationFailureTrackerAuthRequired
		}) {
			authBlocked[result.TrackerID] = struct{}{}
		}
	}
	for _, projection := range current.Projections.Projections {
		if projection.Readiness == api.ReadinessStatusReady && projection.DupeReady {
			return false
		}
		if _, ok := authBlocked[projection.TrackerID]; !ok {
			return false
		}
	}
	return true
}

func (m *Module) hydrateCompositePreparedRelease(
	ctx context.Context,
	current CommandResult,
	session *compositeUploadSession,
) error {
	if current.Release == nil {
		return nil
	}
	if session == nil || session.Intent.Preparation == nil {
		return fmt.Errorf("%w: composite preparation intent is unavailable", ErrInvalidTransition)
	}
	input := *session.Intent.Preparation
	input.SourcePath = current.Release.Release.Source.SourcePath
	input.Force = false
	input.RequirePrepared = true
	input.Controls.ConfirmBDMVRescan = false
	input.Controls.ForceRecheck = nil
	prepared, err := m.preparer.Prepare(ctx, input)
	if err != nil {
		return fmt.Errorf("release workflow hydrate composite prepared release: %w", err)
	}
	if prepared.Release.Generation != current.Release.Release.Generation ||
		prepared.Release.Compatibility != current.Release.Release.Compatibility {
		return fmt.Errorf("%w: persisted prepared generation changed", ErrInvalidTransition)
	}
	return nil
}

func (m *Module) currentCompositeUpload(
	ctx context.Context,
	ownerID string,
	command CompositeUploadCommand,
	operationID api.WorkflowOperationID,
) (CommandResult, *compositeUploadSession, error) {
	current, err := m.Current(ctx, ownerID, command.WorkflowID)
	if err != nil {
		return CommandResult{}, nil, err
	}
	state, err := m.repository.Load(ctx, ownerID, command.WorkflowID)
	if err != nil {
		return CommandResult{}, nil, fmt.Errorf("release workflow load composite session: %w", err)
	}
	if state.Composite == nil || state.Composite.Version != compositeUploadSessionVersion ||
		state.Composite.RequestFingerprint != command.SessionFingerprint ||
		state.Composite.ActiveOperationID != operationID {
		return CommandResult{}, nil, fmt.Errorf("%w: composite upload session failed integrity validation", ErrInvalidTransition)
	}
	return current, state.Composite, nil
}

func compositeUploadGoalReached(current CommandResult, session *compositeUploadSession) bool {
	switch session.Goal {
	case api.WorkflowGoalDryRun:
		return current.DryRun != nil
	case api.WorkflowGoalUploaded:
		return current.UploadResult != nil
	case api.WorkflowGoalPrepared,
		api.WorkflowGoalTrackersAssessed,
		api.WorkflowGoalDuplicatesDecided,
		api.WorkflowGoalMediaReady,
		api.WorkflowGoalDescriptionsReady,
		api.WorkflowGoalUploadReviewed:
		return continuationGoalReached(current, api.ContinueReleaseWorkflowRequest{
			Goal:   session.Goal,
			Intent: session.Intent,
		})
	}
	return false
}

func compositeUploadAllTrackersRemoved(current CommandResult, session *compositeUploadSession) bool {
	if current.Selection == nil || len(current.Selection.TrackerIDs) == 0 || len(session.RemoveTrackers) == 0 {
		return false
	}
	return !slices.ContainsFunc(current.Selection.TrackerIDs, func(id api.TrackerID) bool {
		return !slices.Contains(session.RemoveTrackers, id)
	})
}

func compositeUploadPendingAction(
	current CommandResult,
	session *compositeUploadSession,
) *api.RequiredAction {
	for index := range current.Continuation.RequiredActions {
		action := &current.Continuation.RequiredActions[index]
		if action.Status != api.RequiredActionStatusPending {
			continue
		}
		if !session.Confirm && continuationUnattendedSkipsTrackerAction(session.Intent, *action) {
			continue
		}
		if action.Kind == api.RequiredActionReviewDuplicates {
			if _, decided := session.Intent.DuplicateDecisions[action.TrackerID]; decided {
				continue
			}
		}
		if action.TrackerID != "" &&
			!continuationActionBlocksAllLanesForMode(current, *action, TrackerDecisionModePostDupeGate) {
			continue
		}
		return action
	}
	return nil
}

func (m *Module) applyCompositeAutomaticPolicy(
	ctx context.Context,
	ownerID string,
	current CommandResult,
	session *compositeUploadSession,
	operationID api.WorkflowOperationID,
) (bool, error) {
	if trackerIDs, changed := compositeUploadTrackerRemovalUpdate(current, session); changed {
		return true, m.updateCompositeIntent(ctx, ownerID, current.Workflow.ID, current.Workflow.Revision, operationID, func(intent *api.WorkflowIntent) {
			intent.TrackerIDs = trackerIDs
		})
	}
	if current.Selection != nil && session.DefaultProjection != nil {
		next := make(map[api.TrackerID]api.TrackerProjectionInstructions, len(session.Intent.ProjectionInstructions))
		maps.Copy(next, session.Intent.ProjectionInstructions)
		changed := false
		for _, trackerID := range current.Selection.TrackerIDs {
			merged := mergeCompositeProjectionDefaults(next[trackerID], *session.DefaultProjection)
			before, fingerprintErr := api.CanonicalWorkflowFingerprint(next[trackerID])
			if fingerprintErr != nil {
				return false, fmt.Errorf("release workflow fingerprint current tracker defaults: %w", fingerprintErr)
			}
			after, fingerprintErr := api.CanonicalWorkflowFingerprint(merged)
			if fingerprintErr != nil {
				return false, fmt.Errorf("release workflow fingerprint merged tracker defaults: %w", fingerprintErr)
			}
			if before != after {
				next[trackerID] = merged
				changed = true
			}
		}
		if changed {
			return true, m.updateCompositeIntent(
				ctx,
				ownerID,
				current.Workflow.ID,
				current.Workflow.Revision,
				operationID,
				func(intent *api.WorkflowIntent) {
					intent.ProjectionInstructions = next
				},
			)
		}
	}
	if current.Dupes == nil {
		return false, nil
	}
	decisions := make(map[api.TrackerID]api.DupeDecision)
	onEvidence := session.DuplicateDisposition
	if onEvidence == api.ReleaseWorkflowDuplicateAsk {
		changed, err := m.ensureCompositeDuplicateReviewActions(ctx, ownerID, current, session, operationID)
		if err != nil || changed {
			return changed, err
		}
	}
	for _, result := range current.Dupes.Results {
		if result.Decision != api.DupeDecisionPending && result.Decision != api.DupeDecisionAccepted {
			continue
		}
		if _, exists := session.Intent.DuplicateDecisions[result.TrackerID]; exists {
			continue
		}
		if result.Decision == api.DupeDecisionAccepted && strictDupeResult(result) {
			continue
		}
		switch onEvidence {
		case api.ReleaseWorkflowDuplicateUpload:
			decisions[result.TrackerID] = api.DupeDecisionIgnored
		case api.ReleaseWorkflowDuplicateBlock:
			decisions[result.TrackerID] = api.DupeDecisionAccepted
		case api.ReleaseWorkflowDuplicateAsk:
		}
	}
	if len(decisions) == 0 {
		return false, nil
	}
	return true, m.updateCompositeIntent(ctx, ownerID, current.Workflow.ID, current.Workflow.Revision, operationID, func(intent *api.WorkflowIntent) {
		if intent.DuplicateDecisions == nil {
			intent.DuplicateDecisions = make(map[api.TrackerID]api.DupeDecision)
		}
		maps.Copy(intent.DuplicateDecisions, decisions)
	})
}

func compositeUploadTrackerRemovalUpdate(
	current CommandResult,
	session *compositeUploadSession,
) ([]api.TrackerID, bool) {
	if current.Selection == nil || len(session.RemoveTrackers) == 0 {
		return nil, false
	}
	trackerIDs := slices.DeleteFunc(append([]api.TrackerID(nil), current.Selection.TrackerIDs...), func(id api.TrackerID) bool {
		return slices.Contains(session.RemoveTrackers, id)
	})
	if len(trackerIDs) == len(current.Selection.TrackerIDs) ||
		slices.Equal(normalizeContinuationTrackerIDs(session.Intent.TrackerIDs), normalizeContinuationTrackerIDs(trackerIDs)) {
		return nil, false
	}
	return trackerIDs, true
}

func (m *Module) ensureCompositeDuplicateReviewActions(
	ctx context.Context,
	ownerID string,
	current CommandResult,
	session *compositeUploadSession,
	operationID api.WorkflowOperationID,
) (bool, error) {
	if current.Dupes == nil {
		return false, nil
	}
	trackerIDs := make([]api.TrackerID, 0)
	expiresAt := make(map[api.TrackerID]time.Time)
	for _, result := range current.Dupes.Results {
		if result.Decision != api.DupeDecisionAccepted || strictDupeResult(result) {
			continue
		}
		if _, decided := session.Intent.DuplicateDecisions[result.TrackerID]; decided {
			continue
		}
		if slices.ContainsFunc(current.Workflow.RequiredActions, func(action api.RequiredAction) bool {
			return action.Kind == api.RequiredActionReviewDuplicates &&
				action.TrackerID == result.TrackerID &&
				action.Status == api.RequiredActionStatusPending
		}) {
			continue
		}
		trackerIDs = append(trackerIDs, result.TrackerID)
		expiresAt[result.TrackerID] = result.FreshUntil
	}
	if len(trackerIDs) == 0 {
		return false, nil
	}

	lock := m.commandLock(strings.TrimSpace(ownerID) + "\x00" + string(current.Workflow.ID))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(ctx, ownerID, current.Workflow.ID)
	if err != nil {
		return false, fmt.Errorf("release workflow load duplicate review actions: %w", err)
	}
	if state.Workflow.Revision != current.Workflow.Revision || state.Composite == nil ||
		state.Composite.ActiveOperationID != operationID {
		return false, ErrRevisionConflict
	}
	now := m.clock.Now().UTC()
	for _, trackerID := range trackerIDs {
		id, idErr := m.newID("action")
		if idErr != nil {
			return false, idErr
		}
		expiry := expiresAt[trackerID]
		state.Workflow.RequiredActions = append(state.Workflow.RequiredActions, api.RequiredAction{
			ID:        api.RequiredActionID(id),
			Kind:      api.RequiredActionReviewDuplicates,
			Status:    api.RequiredActionStatusPending,
			TrackerID: trackerID,
			Prompt:    "Review duplicate evidence before continuing this tracker.",
			CreatedAt: now,
			ExpiresAt: &expiry,
		})
	}
	state.Workflow.Status = api.WorkflowStatusBlocked
	if err := m.saveCompositeMetadata(ctx, ownerID, &state); err != nil {
		return false, fmt.Errorf("release workflow save duplicate review actions: %w", err)
	}
	return true, nil
}

func mergeCompositeProjectionDefaults(
	current api.TrackerProjectionInstructions,
	defaults api.ReleaseWorkflowUploadTrackerProjection,
) api.TrackerProjectionInstructions {
	if defaults.UploadReleaseName.Present && !current.UploadReleaseName.Present {
		current.UploadReleaseName = defaults.UploadReleaseName
	}
	if defaults.AdditionalNames != nil {
		if current.AdditionalNames == nil {
			current.AdditionalNames = make(map[string]*string)
		}
		for key, value := range defaults.AdditionalNames {
			if _, exists := current.AdditionalNames[key]; !exists {
				current.AdditionalNames[key] = cloneStringPointer(value)
			}
		}
	}
	if defaults.Questionnaire != nil {
		if current.Questionnaire == nil {
			current.Questionnaire = make(map[string]*string)
		}
		for key, value := range defaults.Questionnaire {
			if _, exists := current.Questionnaire[key]; !exists {
				current.Questionnaire[key] = cloneStringPointer(value)
			}
		}
	}
	if defaults.Config.Anon != nil && current.TrackerConfig.Anon == nil {
		current.TrackerConfig.Anon = cloneBoolPointer(defaults.Config.Anon)
	}
	if defaults.Config.Draft != nil && current.TrackerConfig.Draft == nil {
		current.TrackerConfig.Draft = cloneBoolPointer(defaults.Config.Draft)
	}
	if defaults.Config.ModQ != nil && current.TrackerConfig.ModQ == nil {
		current.TrackerConfig.ModQ = cloneBoolPointer(defaults.Config.ModQ)
	}
	if defaults.Config.Channel != nil && current.TrackerConfig.Channel == nil {
		current.TrackerConfig.Channel = cloneStringPointer(defaults.Config.Channel)
	}
	if defaults.Site.TIK.Foreign != nil && current.TrackerSite.TIK.Foreign == nil {
		current.TrackerSite.TIK.Foreign = cloneBoolPointer(defaults.Site.TIK.Foreign)
	}
	if defaults.Site.TIK.Opera != nil && current.TrackerSite.TIK.Opera == nil {
		current.TrackerSite.TIK.Opera = cloneBoolPointer(defaults.Site.TIK.Opera)
	}
	if defaults.Site.TIK.Asian != nil && current.TrackerSite.TIK.Asian == nil {
		current.TrackerSite.TIK.Asian = cloneBoolPointer(defaults.Site.TIK.Asian)
	}
	if defaults.Site.TIK.DiscType != nil && current.TrackerSite.TIK.DiscType == nil {
		current.TrackerSite.TIK.DiscType = cloneStringPointer(defaults.Site.TIK.DiscType)
	}
	return current
}

func (m *Module) updateCompositeIntent(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	expected api.WorkflowRevision,
	operationID api.WorkflowOperationID,
	update func(*api.WorkflowIntent),
) error {
	lock := m.commandLock(strings.TrimSpace(ownerID) + "\x00" + string(workflowID))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return fmt.Errorf("release workflow load composite intent: %w", err)
	}
	if state.Workflow.Revision != expected || state.Composite == nil || state.Composite.ActiveOperationID != operationID {
		return ErrRevisionConflict
	}
	update(&state.Composite.Intent)
	if err := m.saveCompositeMetadata(ctx, ownerID, &state); err != nil {
		return fmt.Errorf("release workflow save composite intent: %w", err)
	}
	return nil
}

var compositeUploadStages = []struct {
	ID    string
	Label string
}{
	{ID: "prepare", Label: "Prepare release"},
	{ID: "project-trackers", Label: "Project trackers"},
	{ID: "preflight-trackers", Label: "Preflight trackers"},
	{ID: "check-duplicates", Label: "Check duplicates"},
	{ID: "decide-duplicates", Label: "Resolve duplicate decisions"},
	{ID: "capture-media", Label: "Capture or select media"},
	{ID: "prepare-image-requirements", Label: "Host images"},
	{ID: "generate-descriptions", Label: "Generate descriptions"},
	{ID: "review-uploads", Label: "Prepare upload review"},
	{ID: "execute-uploads", Label: "Execute upload or finish dry-run"},
}

func (m *Module) reportCompositeStage(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	current CommandResult,
	stage string,
	status api.StageStatus,
	stageProgress int,
) {
	_, _ = m.mutateOperation(ctx, ownerID, workflowID, operationID, func(operation *api.WorkflowOperationStatus) {
		items, completedUnits := compositeUploadProgressItems(current, stage, status, stageProgress)
		operation.Items = items
		operation.Phase = stage
		operation.Total = len(compositeUploadStages) * 100
		operation.Completed = min(operation.Total, completedUnits)
		operation.Progress = operation.Completed * 100 / operation.Total
		operation.Message = compositeUploadStageLabel(stage)
	})
}

func compositeUploadProgressItems(
	current CommandResult,
	active string,
	activeStatus api.StageStatus,
	activeProgress int,
) ([]api.WorkflowOperationItem, int) {
	completed := compositeCompletedStageCount(current)
	items := make([]api.WorkflowOperationItem, 0, len(compositeUploadStages))
	completedUnits := 0
	for index, stage := range compositeUploadStages {
		status := api.StageStatusPending
		progress := 0
		switch {
		case index < completed:
			status = api.StageStatusCompleted
			progress = 100
		case stage.ID == active:
			status = activeStatus
			progress = min(100, max(0, activeProgress))
		}
		items = append(items, api.WorkflowOperationItem{
			ID:        stage.ID,
			Kind:      "upload_stage",
			Label:     stage.Label,
			Phase:     stage.ID,
			Status:    status,
			Completed: progress,
			Total:     100,
		})
		completedUnits += progress
	}
	return items, completedUnits
}

func compositeCompletedStageCount(current CommandResult) int {
	switch {
	case current.UploadResult != nil || current.DryRun != nil:
		return 10
	case current.Descriptions != nil:
		return 8
	case current.Media != nil && mediaRequirementsPrepared(current.Media):
		return 7
	case current.Media != nil:
		return 6
	case current.Dupes != nil && duplicateDecisionsComplete(current.Dupes):
		return 5
	case current.Dupes != nil:
		return 4
	case current.Preflight != nil:
		return 3
	case current.Projections != nil:
		return 2
	case current.Release != nil:
		return 1
	default:
		return 0
	}
}

func compositeUploadStageLabel(stage string) string {
	for _, candidate := range compositeUploadStages {
		if candidate.ID == stage {
			return candidate.Label
		}
	}
	return "Upload"
}

func compositeUploadInitialItems() []api.WorkflowOperationItem {
	items, _ := compositeUploadProgressItems(CommandResult{}, "", api.StageStatusPending, 0)
	return items
}

func compositeUploadTerminalStatus(result CommandResult) api.StageStatus {
	if slices.ContainsFunc(result.Continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.Status == api.RequiredActionStatusPending
	}) {
		return api.StageStatusBlocked
	}
	if result.UploadResult != nil {
		return api.StageStatusExecuted
	}
	if result.DryRun != nil {
		return api.StageStatusCompleted
	}
	terminalProjection := result
	terminalProjection.Operation = nil
	disposition := terminalProjection.Continuation.Disposition
	switch disposition {
	case api.WorkflowDispositionNeedsAction:
		return api.StageStatusBlocked
	case api.WorkflowDispositionPartial:
		return api.StageStatusPartial
	case api.WorkflowDispositionFailed:
		return api.StageStatusFailed
	case api.WorkflowDispositionCanceled:
		return api.StageStatusCanceled
	case api.WorkflowDispositionSucceeded, api.WorkflowDispositionNone:
		if result.UploadResult != nil {
			return api.StageStatusExecuted
		}
		return api.StageStatusCompleted
	}
	return api.StageStatusCompleted
}

func compositeUploadResult(result CommandResult) *api.WorkflowOperationResult {
	switch {
	case result.UploadResult != nil:
		return &api.WorkflowOperationResult{
			Kind:             api.WorkflowOperationResultUpload,
			WorkflowRevision: result.Workflow.Revision,
			RefID:            string(result.UploadResult.ID),
			RefRevision:      result.UploadResult.Revision,
		}
	case result.DryRun != nil:
		return &api.WorkflowOperationResult{
			Kind:             api.WorkflowOperationResultDryRun,
			WorkflowRevision: result.Workflow.Revision,
			RefID:            string(result.DryRun.ID),
			RefRevision:      result.DryRun.Revision,
		}
	default:
		return nil
	}
}

func compositeUploadFeedbackActionKind(kind api.ReleaseWorkflowUploadFeedbackKind) api.RequiredActionKind {
	switch kind {
	case api.ReleaseWorkflowUploadFeedbackPlaylistSelection:
		return api.RequiredActionSelectPlaylist
	case api.ReleaseWorkflowUploadFeedbackMetadataSelection:
		return api.RequiredActionSelectMetadata
	case api.ReleaseWorkflowUploadFeedbackRescanConfirmation:
		return api.RequiredActionConfirmRescan
	case legacyTrackerAuthFeedbackKind:
		return legacyTrackerAuthActionKind
	case legacyTrackerTwoFactorFeedbackKind:
		return legacyTrackerTwoFactorActionKind
	case api.ReleaseWorkflowUploadFeedbackTrackerInput:
		return api.RequiredActionProvideTrackerInput
	case api.ReleaseWorkflowUploadFeedbackQuestionnaire:
		return api.RequiredActionAnswerQuestionnaire
	case api.ReleaseWorkflowUploadFeedbackRuleAuthorization:
		return api.RequiredActionAuthorizeRules
	case api.ReleaseWorkflowUploadFeedbackTrackerPreparation:
		return api.RequiredActionResolveTrackerPreparation
	case api.ReleaseWorkflowUploadFeedbackDuplicateReview:
		return api.RequiredActionReviewDuplicates
	case api.ReleaseWorkflowUploadFeedbackTrackerApproval:
		return api.RequiredActionApproveTrackers
	case api.ReleaseWorkflowUploadFeedbackUploadApproval: //nolint:staticcheck // Map retained v1 feedback for explicit rejection.
		return api.RequiredActionApproveUpload //nolint:staticcheck // Retained v1 action mapping.
	case api.ReleaseWorkflowUploadFeedbackReprepare:
		return api.RequiredActionReprepare
	case api.ReleaseWorkflowUploadFeedbackReconciliation:
		return api.RequiredActionReconcileSubmission
	}
	return ""
}

func normalizedCompositeFeedback(feedback api.ReleaseWorkflowUploadFeedback) compositeUploadFeedbackResponse {
	response := compositeUploadFeedbackResponse{Kind: feedback.Response.Kind}
	switch feedback.Response.Kind {
	case api.ReleaseWorkflowUploadFeedbackPlaylistSelection:
		response.SelectedValues = cloneTrimmedStrings(feedback.Response.PlaylistSelection.Selected)
		response.UseAll = feedback.Response.PlaylistSelection.UseAll
	case api.ReleaseWorkflowUploadFeedbackMetadataSelection:
		response.SelectedValues = cloneTrimmedStrings(feedback.Response.MetadataSelection.SelectedValues)
		response.Facts = feedback.Response.MetadataSelection.Facts
	case api.ReleaseWorkflowUploadFeedbackRescanConfirmation:
		response.Confirmed = feedback.Response.RescanConfirmation.Confirmed
	case legacyTrackerAuthFeedbackKind,
		legacyTrackerTwoFactorFeedbackKind:
		// Deprecated auth feedback is rejected before durable normalization.
	case api.ReleaseWorkflowUploadFeedbackTrackerInput:
		response.TrackerID = normalizeCompositeTrackerID(feedback.Response.TrackerInput.TrackerID)
		projection := feedback.Response.TrackerInput.Projection
		response.Projection = &projection
	case api.ReleaseWorkflowUploadFeedbackQuestionnaire:
		response.TrackerID = normalizeCompositeTrackerID(feedback.Response.Questionnaire.TrackerID)
		response.Questionnaire = cloneStringPointerMap(feedback.Response.Questionnaire.Answers)
	case api.ReleaseWorkflowUploadFeedbackRuleAuthorization:
		response.Confirmed = feedback.Response.RuleAuthorization.Confirmed
	case api.ReleaseWorkflowUploadFeedbackTrackerPreparation:
		response.Confirmed = feedback.Response.TrackerPreparation.Confirmed
	case api.ReleaseWorkflowUploadFeedbackDuplicateReview:
		response.TrackerID = normalizeCompositeTrackerID(feedback.Response.DuplicateReview.TrackerID)
		response.DuplicateDecision = feedback.Response.DuplicateReview.Decision
	case api.ReleaseWorkflowUploadFeedbackTrackerApproval:
		response.Confirmed = feedback.Response.TrackerApproval.Confirmed
		response.TrackerIDs = normalizeContinuationTrackerIDs(feedback.Response.TrackerApproval.TrackerIDs)
	case api.ReleaseWorkflowUploadFeedbackUploadApproval: //nolint:staticcheck // Normalize retained v1 feedback before explicit rejection.
		response.Confirmed = feedback.Response.UploadApproval.Confirmed //nolint:staticcheck // Retained v1 decoding compatibility.
		response.TrackerIDs = normalizeContinuationTrackerIDs(
			feedback.Response.UploadApproval.TrackerIDs, //nolint:staticcheck // Retained v1 decoding compatibility.
		)
	case api.ReleaseWorkflowUploadFeedbackReprepare:
		response.Confirmed = feedback.Response.Reprepare.Confirmed
		response.Preparation = feedback.Response.Reprepare.Preparation
	case api.ReleaseWorkflowUploadFeedbackReconciliation:
		response.Reconciliation = feedback.Response.Reconciliation.Selection
	}
	return response
}

func normalizeCompositeTrackerID(value api.TrackerID) api.TrackerID {
	return api.TrackerID(strings.ToUpper(strings.TrimSpace(string(value))))
}

// SubmitUploadFeedback validates exact action authority, idempotently applies
// one response, and starts the resumed composite operation.
func (m *Module) SubmitUploadFeedback(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	feedback api.ReleaseWorkflowUploadFeedback,
) (CommandResult, error) {
	if feedback.Response.Kind == legacyTrackerAuthFeedbackKind ||
		feedback.Response.Kind == legacyTrackerTwoFactorFeedbackKind {
		return CommandResult{}, deprecatedAuthFeedbackError()
	}
	if err := feedback.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("release workflow upload feedback: %w", err)
	}
	if feedback.Action.WorkflowRevision == 0 {
		return CommandResult{}, ErrRevisionConflict
	}
	response := normalizedCompositeFeedback(feedback)
	feedbackCommand := applyCompositeUploadFeedbackCommand{
		WorkflowID:       workflowID,
		ExpectedRevision: feedback.Action.WorkflowRevision,
		ActionID:         feedback.Action.ID,
		Response:         response,
		IdempotencyKey:   feedback.IdempotencyKey,
	}
	state, err := m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow load upload feedback receipt: %w", err)
	}
	fingerprint, err := acceptedCommandFingerprint(feedbackCommand)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow fingerprint upload feedback: %w", err)
	}
	receiptKey := commandReceiptKey(feedbackCommand.commandName(), feedback.IdempotencyKey, fingerprint)
	if receipt, ok := state.Receipts[receiptKey]; ok {
		if receipt.Fingerprint != fingerprint {
			return CommandResult{}, ErrIdempotencyConflict
		}
		if state.Composite == nil {
			return CommandResult{}, fmt.Errorf("%w: composite upload session is unavailable", ErrInvalidTransition)
		}
		compositeReceipt, retained := state.Composite.FeedbackReceipts[feedback.IdempotencyKey]
		if !retained {
			return m.Current(ctx, ownerID, workflowID)
		}
		operation, startErr := m.Start(ctx, ownerID, CompositeUploadCommand{
			WorkflowID:         workflowID,
			ExpectedRevision:   compositeReceipt.Revision,
			SessionFingerprint: state.Composite.RequestFingerprint,
			Goal:               state.Composite.Goal,
			IdempotencyKey:     compositeUploadOperationKey(feedback.IdempotencyKey, compositeReceipt.Sequence),
		})
		if startErr != nil {
			return CommandResult{}, fmt.Errorf("release workflow replay composite upload feedback: %w", startErr)
		}
		current, currentErr := m.Current(ctx, ownerID, workflowID)
		if currentErr != nil {
			return CommandResult{}, currentErr
		}
		current.Operation = &operation
		return current, nil
	}
	if err := m.requireOperationOwnership(ctx, ownerID, workflowID); err != nil {
		return CommandResult{}, err
	}
	current, err := m.Current(ctx, ownerID, workflowID)
	if err != nil {
		return CommandResult{}, err
	}
	if current.Workflow.Revision != feedback.Action.WorkflowRevision {
		return CommandResult{}, ErrRevisionConflict
	}
	actionIndex := slices.IndexFunc(current.Continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.ID == feedback.Action.ID && action.Status == api.RequiredActionStatusPending
	})
	if actionIndex < 0 {
		return CommandResult{}, fmt.Errorf("%w: upload feedback action is stale", ErrRevisionConflict)
	}
	action := current.Continuation.RequiredActions[actionIndex]
	if action.WorkflowRevision != current.Workflow.Revision {
		return CommandResult{}, fmt.Errorf("%w: upload feedback action revision is stale", ErrRevisionConflict)
	}
	if action.ExpiresAt != nil && !action.ExpiresAt.After(m.clock.Now().UTC()) {
		return CommandResult{}, fmt.Errorf("%w: upload feedback action expired", ErrRevisionConflict)
	}
	if action.Kind != compositeUploadFeedbackActionKind(feedback.Response.Kind) {
		return CommandResult{}, fmt.Errorf("%w: upload feedback kind does not match action", ErrInvalidTransition)
	}
	if err := validateCompositeFeedbackAction(action, response); err != nil {
		return CommandResult{}, err
	}
	feedbackCommand.ExpectedRevision = current.Workflow.Revision
	result, err := m.execute(ctx, ownerID, feedbackCommand)
	if err != nil {
		return CommandResult{}, err
	}
	state, err = m.repository.Load(ctx, ownerID, workflowID)
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow reload feedback session: %w", err)
	}
	if state.Composite == nil {
		return CommandResult{}, fmt.Errorf("%w: composite upload session is unavailable", ErrInvalidTransition)
	}
	operation, err := m.Start(ctx, ownerID, CompositeUploadCommand{
		WorkflowID:         workflowID,
		ExpectedRevision:   result.Workflow.Revision,
		SessionFingerprint: state.Composite.RequestFingerprint,
		Goal:               state.Composite.Goal,
		IdempotencyKey:     compositeUploadOperationKey(feedback.IdempotencyKey, state.Composite.FeedbackSequence),
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow resume composite upload: %w", err)
	}
	current, err = m.Current(ctx, ownerID, operation.WorkflowID)
	if err != nil {
		return CommandResult{}, err
	}
	current.Operation = &operation
	return current, nil
}

func validateCompositeFeedbackAction(action api.RequiredAction, response compositeUploadFeedbackResponse) error {
	if response.TrackerID != "" && action.TrackerID != "" && response.TrackerID != action.TrackerID {
		return fmt.Errorf("%w: feedback tracker does not match action", ErrInvalidTransition)
	}
	if len(response.SelectedValues) > 0 && len(action.Options) > 0 {
		for _, selected := range response.SelectedValues {
			if !slices.ContainsFunc(action.Options, func(option api.RequiredActionOption) bool {
				return option.Value == selected
			}) {
				return fmt.Errorf("%w: feedback option is not available", ErrInvalidTransition)
			}
		}
	}
	if len(response.TrackerIDs) > 0 && len(action.Options) > 0 {
		for _, trackerID := range response.TrackerIDs {
			if !slices.ContainsFunc(action.Options, func(option api.RequiredActionOption) bool {
				return option.Value == string(trackerID)
			}) {
				return fmt.Errorf("%w: feedback tracker %s is not available", ErrInvalidTransition, trackerID)
			}
		}
	}
	return nil
}

func (m *Module) applyCompositeUploadFeedback(
	ctx context.Context,
	ownerID string,
	state *State,
	nextRevision api.WorkflowRevision,
	now time.Time,
	command applyCompositeUploadFeedbackCommand,
) (CommandResult, error) {
	if state.Composite == nil {
		return CommandResult{}, fmt.Errorf("%w: composite upload session is unavailable", ErrInvalidTransition)
	}
	index := slices.IndexFunc(state.Workflow.RequiredActions, func(action api.RequiredAction) bool {
		return action.ID == command.ActionID && action.Status == api.RequiredActionStatusPending
	})
	var (
		action api.RequiredAction
	)
	switch {
	case index >= 0:
		action = state.Workflow.RequiredActions[index]
	case command.Response.Kind == api.ReleaseWorkflowUploadFeedbackTrackerApproval:
		projected, _, err := projectedTrackerApprovalAction(state, now)
		if err != nil {
			return CommandResult{}, err
		}
		if projected == nil || projected.ID != command.ActionID {
			return CommandResult{}, fmt.Errorf("%w: upload feedback action is stale", ErrRevisionConflict)
		}
		action = *projected
	default:
		return CommandResult{}, fmt.Errorf("%w: upload feedback action is stale", ErrRevisionConflict)
	}
	resolvedByCommand := false
	if action.Kind == api.RequiredActionReconcileSubmission {
		if _, err := m.resolveAction(ctx, ownerID, state, nextRevision, now, ResolveActionCommand{
			WorkflowID:       command.WorkflowID,
			ExpectedRevision: command.ExpectedRevision,
			Answer: api.RequiredActionAnswer{
				ActionID:         action.ID,
				WorkflowRevision: command.ExpectedRevision,
				SelectedValues:   []string{command.Response.Reconciliation},
			},
			IdempotencyKey: command.IdempotencyKey,
		}); err != nil {
			return CommandResult{}, err
		}
		resolvedByCommand = true
	}
	switch command.Response.Kind {
	case api.ReleaseWorkflowUploadFeedbackPlaylistSelection:
		if state.Composite.Intent.Preparation == nil {
			return CommandResult{}, fmt.Errorf("%w: preparation intent is unavailable", ErrInvalidTransition)
		}
		state.Composite.Intent.Preparation.Instructions.Playlist = api.PlaylistInstruction{
			Set:      true,
			Selected: append([]string(nil), command.Response.SelectedValues...),
			UseAll:   command.Response.UseAll,
		}
		state.Composite.Intent.Preparation.Force = true
		instructions := state.Composite.Intent.Preparation.Instructions
		state.Composite.Intent.FactInstructions = &instructions
	case api.ReleaseWorkflowUploadFeedbackMetadataSelection:
		if command.Response.Facts != nil {
			instructions, err := compositeUploadFactInstructions(*command.Response.Facts, nil)
			if err != nil {
				return CommandResult{}, err
			}
			if state.Composite.Intent.Preparation == nil {
				return CommandResult{}, fmt.Errorf("%w: preparation intent is unavailable", ErrInvalidTransition)
			}
			state.Composite.Intent.Preparation.Instructions = instructions
			state.Composite.Intent.Preparation.Force = true
			state.Composite.Intent.FactInstructions = &instructions
		} else {
			if len(command.Response.SelectedValues) != 1 || state.Composite.Intent.Preparation == nil {
				return CommandResult{}, fmt.Errorf(
					"%w: metadata feedback requires one retained candidate or replacement facts",
					ErrInvalidTransition,
				)
			}
			state.Composite.Intent.Preparation.Instructions.BlurayReleaseID = command.Response.SelectedValues[0]
			state.Composite.Intent.Preparation.Force = true
			instructions := state.Composite.Intent.Preparation.Instructions
			state.Composite.Intent.FactInstructions = &instructions
		}
	case api.ReleaseWorkflowUploadFeedbackRescanConfirmation:
		state.Composite.Intent.Preparation.Controls.ConfirmBDMVRescan = true
		state.Composite.Intent.Preparation.Force = true
	case legacyTrackerAuthFeedbackKind,
		legacyTrackerTwoFactorFeedbackKind:
		return CommandResult{}, deprecatedAuthFeedbackError()
	case api.ReleaseWorkflowUploadFeedbackTrackerInput:
		if command.Response.Projection != nil {
			if state.Composite.Intent.ProjectionInstructions == nil {
				state.Composite.Intent.ProjectionInstructions = make(map[api.TrackerID]api.TrackerProjectionInstructions)
			}
			mapped := compositeUploadProjectionInstructions(map[api.TrackerID]api.ReleaseWorkflowUploadTrackerProjection{
				command.Response.TrackerID: *command.Response.Projection,
			})
			state.Composite.Intent.ProjectionInstructions[command.Response.TrackerID] = mapped[command.Response.TrackerID]
			invalidateTrackerAndDownstream(&state.Workflow)
		}
	case api.ReleaseWorkflowUploadFeedbackQuestionnaire:
		if state.Composite.Intent.ProjectionInstructions == nil {
			state.Composite.Intent.ProjectionInstructions = make(map[api.TrackerID]api.TrackerProjectionInstructions)
		}
		instruction := state.Composite.Intent.ProjectionInstructions[command.Response.TrackerID]
		instruction.Questionnaire = cloneStringPointerMap(command.Response.Questionnaire)
		state.Composite.Intent.ProjectionInstructions[command.Response.TrackerID] = instruction
		invalidateTrackerAndDownstream(&state.Workflow)
	case api.ReleaseWorkflowUploadFeedbackDuplicateReview:
		if state.Composite.Intent.DuplicateDecisions == nil {
			state.Composite.Intent.DuplicateDecisions = make(map[api.TrackerID]api.DupeDecision)
		}
		trackerID := command.Response.TrackerID
		if trackerID == "" {
			trackerID = action.TrackerID
		}
		state.Composite.Intent.DuplicateDecisions[trackerID] = command.Response.DuplicateDecision
	case api.ReleaseWorkflowUploadFeedbackTrackerApproval:
		if state.Workflow.Dupes == nil {
			return CommandResult{}, fmt.Errorf("%w: duplicate assessment is unavailable", ErrRevisionConflict)
		}
		if _, err := m.approveTrackers(state, nextRevision, now, ApproveTrackersCommand{
			WorkflowID:       command.WorkflowID,
			ExpectedRevision: command.ExpectedRevision,
			Approval: api.TrackerApproval{
				ActionID:         action.ID,
				Dupes:            *state.Workflow.Dupes,
				InputFingerprint: state.Dupes[state.Workflow.Dupes.ID].InputFingerprint,
				TrackerIDs:       append([]api.TrackerID(nil), command.Response.TrackerIDs...),
			},
			IdempotencyKey: command.IdempotencyKey,
		}); err != nil {
			return CommandResult{}, err
		}
		resolvedByCommand = true
	case api.ReleaseWorkflowUploadFeedbackUploadApproval: //nolint:staticcheck // Reject retained v1 feedback explicitly.
		return CommandResult{}, fmt.Errorf("%w: final upload approval is no longer accepted", ErrInvalidTransition)
	case api.ReleaseWorkflowUploadFeedbackReprepare:
		if command.Response.Preparation != nil {
			instructions, err := compositeUploadFactInstructions(command.Response.Preparation.Facts, nil)
			if err != nil {
				return CommandResult{}, err
			}
			state.Composite.Intent.Preparation.Instructions = instructions
			state.Composite.Intent.Preparation.Policy = api.PreparationPolicy{
				KeepFolder: optionalBool(command.Response.Preparation.Policy.KeepFolder),
				KeepImages: optionalBool(command.Response.Preparation.Policy.KeepImages),
				OnlyID:     optionalBool(command.Response.Preparation.Policy.OnlyID),
			}
			state.Composite.Intent.FactInstructions = &instructions
		}
		state.Composite.Intent.Preparation.Force = true
	case api.ReleaseWorkflowUploadFeedbackRuleAuthorization:
		if command.Response.Confirmed {
			confirmed := true
			if _, err := m.resolveAction(ctx, ownerID, state, nextRevision, now, ResolveActionCommand{
				WorkflowID:       command.WorkflowID,
				ExpectedRevision: command.ExpectedRevision,
				Answer: api.RequiredActionAnswer{
					ActionID:         action.ID,
					WorkflowRevision: command.ExpectedRevision,
					Confirmed:        &confirmed,
				},
				IdempotencyKey: command.IdempotencyKey,
			}); err != nil {
				return CommandResult{}, err
			}
			resolvedByCommand = true
			break
		}
		trackerID := normalizeCompositeTrackerID(action.TrackerID)
		if trackerID == "" {
			return CommandResult{}, fmt.Errorf("%w: tracker rule rejection requires tracker ID", ErrInvalidTransition)
		}
		if !slices.Contains(state.Composite.RemoveTrackers, trackerID) {
			state.Composite.RemoveTrackers = append(state.Composite.RemoveTrackers, trackerID)
		}
		m.logger.Infof("release workflow: declined tracker rule override tracker=%s decision=skip", trackerID)
	case api.ReleaseWorkflowUploadFeedbackTrackerPreparation:
		confirmed := command.Response.Confirmed
		if _, err := m.resolveAction(ctx, ownerID, state, nextRevision, now, ResolveActionCommand{
			WorkflowID:       command.WorkflowID,
			ExpectedRevision: command.ExpectedRevision,
			Answer: api.RequiredActionAnswer{
				ActionID:         action.ID,
				WorkflowRevision: command.ExpectedRevision,
				Confirmed:        &confirmed,
			},
			IdempotencyKey: command.IdempotencyKey,
		}); err != nil {
			return CommandResult{}, err
		}
		resolvedByCommand = true
	case api.ReleaseWorkflowUploadFeedbackReconciliation:
	}
	if !resolvedByCommand {
		state.Workflow.RequiredActions = slices.DeleteFunc(state.Workflow.RequiredActions, func(candidate api.RequiredAction) bool {
			return candidate.ID == action.ID
		})
	}
	if !hasPendingRequiredAction(state.Workflow.RequiredActions) && state.Workflow.Status == api.WorkflowStatusBlocked {
		state.Workflow.Status = api.WorkflowStatusActive
	}
	state.Composite.ActiveOperationID = ""
	state.Composite.FeedbackSequence++
	state.Composite.LastCommittedRevision = nextRevision
	if state.Composite.FeedbackReceipts == nil {
		state.Composite.FeedbackReceipts = make(map[string]compositeUploadFeedbackReceipt)
	}
	fingerprint, err := command.commandFingerprint()
	if err != nil {
		return CommandResult{}, fmt.Errorf("release workflow fingerprint retained upload feedback: %w", err)
	}
	state.Composite.FeedbackReceipts[command.IdempotencyKey] = compositeUploadFeedbackReceipt{
		Fingerprint: fingerprint,
		Revision:    nextRevision,
		Sequence:    state.Composite.FeedbackSequence,
	}
	return CommandResult{}, nil
}

func deprecatedAuthFeedbackError() error {
	cause := fmt.Errorf("%w: %s", ErrInvalidTransition, deprecatedAuthFeedbackMessage)
	return api.NewOperationError(api.OperationFailure{
		Code:      api.OperationFailureTrackerAuthRequired,
		Operation: api.OperationKindUploadExecute,
		Message:   deprecatedAuthFeedbackMessage,
		Recovery:  api.OperationRecoveryAuthenticateTrackers,
	}, cause)
}

func compositeUploadRecoveryValid(
	command CompositeUploadCommand,
	operation api.ReleaseWorkflowOperationRecord,
	state State,
) bool {
	return state.Composite != nil &&
		state.Composite.Version == compositeUploadSessionVersion &&
		state.Composite.RequestFingerprint == command.SessionFingerprint &&
		state.Composite.ActiveOperationID == operation.OperationID &&
		state.Composite.LastCommittedRevision <= state.Workflow.Revision
}

func compositeUploadCommandTarget(command Command) (CompositeUploadCommand, bool) {
	switch typed := command.(type) {
	case CompositeUploadCommand:
		return typed, true
	case *CompositeUploadCommand:
		return *typed, true
	default:
		return CompositeUploadCommand{}, false
	}
}
