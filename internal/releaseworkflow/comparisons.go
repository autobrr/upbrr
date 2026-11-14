// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// ComparisonMediaBuilder owns private comparison sources and images. The module
// retains revision, owner and explicit approval authority around these operations.
type ComparisonMediaBuilder interface {
	EditComparisons(context.Context, api.ReleaseRef, api.TrackerReleaseProjectionSet, *api.MediaArtifactSet, any,
		api.ComparisonInstructions, []StagedMediaAttachment, time.Time) (api.MediaArtifactSet, RetainedMediaResource, error)
	PreviewComparisonFrame(context.Context, api.ReleaseRef, api.MediaArtifactSet, any, string, float64, int) (MediaPreviewContent, error)
}

// ComparisonMediaRestorer validates retained comparison sources against a new
// prepared release without restoring ordinary media or prior upload authority.
type ComparisonMediaRestorer interface {
	RestoreComparisons(context.Context, api.ReleaseRef, api.TrackerReleaseProjectionSet, api.MediaArtifactSet, any,
		time.Time) (api.MediaArtifactSet, RetainedMediaResource, error)
}

// adoptInputComparisons materializes an owner-local pending snapshot only after
// OpenInput verifies the same canonical source version. It does not activate
// media or carry tracker, upload or comparison-review authority into the new run.
func (m *Module) adoptInputComparisons(owner string, state *State, prior State,
	canonicalPath string, now time.Time,
) (bool, error) {
	if prior.Workflow.Status == api.WorkflowStatusCanceled || prior.Workflow.Status == api.WorkflowStatusCompleted {
		return false, nil
	}
	ref := prior.ComparisonMedia
	if ref == nil {
		ref = prior.Workflow.Media
	}
	if ref == nil {
		return false, nil
	}
	previous, exists := prior.Media[ref.ID]
	if !exists || previous.Revision != ref.Revision || previous.Comparisons == nil ||
		!pathing.SamePath(previous.ReleaseRef.SourcePath, canonicalPath) {
		return false, nil
	}
	private, err := m.private.Get(owner, prior.Workflow.ID, mediaPrivateResourceID(ref.ID), now)
	if errors.Is(err, ErrPrivateResourceUnavailable) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load prior input comparisons: %w", err)
	}
	snapshot, err := previous.Clone()
	if err != nil {
		return false, fmt.Errorf("copy prior input comparisons: %w", err)
	}
	id, err := m.newID("media")
	if err != nil {
		return false, err
	}
	snapshot.ID = api.MediaArtifactSetID(id)
	snapshot.WorkflowID = state.Workflow.ID
	snapshot.Revision = state.Workflow.Revision + 1
	snapshot.Release = api.ReleaseSnapshotRef{}
	snapshot.ProjectionSet = api.TrackerReleaseProjectionSetRef{}
	snapshot.TrackerApproval = nil
	snapshot.RequiredActions = nil
	snapshot.Failures = nil
	snapshot.Status = api.StageStatusStale
	snapshot.ImageRequirementsPrepared = false
	snapshot.ImageHostUploadSkipped = false
	snapshot.AutomaticScreenshotSelection = false
	snapshot.Comparisons.ApprovedFingerprint = ""
	snapshot.CreatedAt = now
	comparisonIDs := make(map[api.PublicResourceID]bool)
	for _, artifact := range snapshot.Artifacts {
		if artifact.Kind == api.MediaArtifactComparison {
			comparisonIDs[artifact.ID] = true
		}
	}
	snapshot.Artifacts = slices.DeleteFunc(snapshot.Artifacts, func(artifact api.MediaArtifact) bool {
		return !comparisonIDs[artifact.ID] && (artifact.Kind != api.MediaArtifactHostedImage || !comparisonIDs[api.PublicResourceID(artifact.Source)])
	})
	snapshot.HostAttempts = nil
	// The opaque value remains read-only. RestoreComparisons copies and validates
	// only comparison-owned content against the fresh prepared generation.
	if err := m.private.Put(owner, state.Workflow.ID, mediaPrivateResourceID(snapshot.ID), private, now.Add(24*time.Hour)); err != nil {
		return false, fmt.Errorf("retain reopened input comparisons: %w", err)
	}
	state.Media[snapshot.ID] = snapshot
	state.ComparisonMedia = &api.MediaArtifactSetRef{ID: snapshot.ID, Revision: snapshot.Revision}
	return true, nil
}

// RestoreComparisonsCommand rebinds retained comparison intent after preparation.
// The retained reference comes from workflow state, never the caller.
type RestoreComparisonsCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	IdempotencyKey   string
}

func (RestoreComparisonsCommand) commandName() string              { return "restore_comparisons" }
func (RestoreComparisonsCommand) userIntent()                      {}
func (RestoreComparisonsCommand) operationKind() api.OperationKind { return api.OperationKindMedia }
func (c RestoreComparisonsCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(c.ExpectedRevision)
}

func (m *Module) retainedComparisons(ctx context.Context, owner string, state *State,
	release api.ReleaseRef, eligible api.TrackerReleaseProjectionSet, now time.Time,
) (*api.MediaArtifactSet, RetainedMediaResource, error) {
	ref := state.ComparisonMedia
	if ref == nil {
		return nil, nil, nil
	}
	restorer, ok := m.mediaBuilder.(ComparisonMediaRestorer)
	if !ok {
		return nil, nil, fmt.Errorf("%w: comparison restoration service is unavailable", ErrInvalidTransition)
	}
	snapshot, exists := state.Media[ref.ID]
	if !exists || snapshot.Revision != ref.Revision || snapshot.Comparisons == nil {
		return nil, nil, fmt.Errorf("%w: retained comparison revision is unavailable; reimport or clear comparisons", ErrInvalidTransition)
	}
	private, err := m.private.Get(owner, state.Workflow.ID, mediaPrivateResourceID(ref.ID), now)
	if err != nil {
		return nil, nil, fmt.Errorf("load retained comparisons; reimport or clear comparisons if expired: %w", err)
	}
	restored, retained, err := restorer.RestoreComparisons(ctx, release, eligible, snapshot, private, now)
	if err != nil {
		return nil, nil, fmt.Errorf("restore comparison sources: %w", err)
	}
	if restored.Comparisons == nil || retained == nil {
		return nil, nil, fmt.Errorf("%w: comparison restoration lost retained intent", ErrPrivateResourceIntegrity)
	}
	restored.Comparisons.ApprovedFingerprint = ""
	restored.ImageRequirementsPrepared = false
	restored.ImageHostUploadSkipped = false
	if err := validateMediaRequirementsFingerprint(restored, eligible); err != nil {
		return nil, nil, err
	}
	return &restored, retained, nil
}

func (m *Module) restoreComparisons(ctx context.Context, owner string, state *State,
	revision api.WorkflowRevision, now time.Time,
) (CommandResult, error) {
	if err := requireReconciledExternalEffects(state.Workflow); err != nil {
		return CommandResult{}, err
	}
	if state.Workflow.Media != nil || state.ComparisonMedia == nil {
		return CommandResult{}, fmt.Errorf("%w: comparison restoration is not pending", ErrInvalidTransition)
	}
	release, eligible, _, _, local, err := m.comparisonExtensionContext(ctx, owner, state, nil, now)
	if err != nil {
		return CommandResult{}, err
	}
	snapshot, private, err := m.retainedComparisons(ctx, owner, state, release, eligible, now)
	if err != nil {
		return CommandResult{}, err
	}
	return m.publishComparisonMutation(owner, state, revision, now, *snapshot, private, local)
}

func rejectLegacyCompositeComparisons(session *compositeUploadSession) error {
	if session.ManualMedia.Comparisons == nil && slices.ContainsFunc(session.ManualMedia.Attachments,
		func(attachment api.MediaAttachment) bool {
			return attachment.Kind == api.MediaArtifactScreenshot && attachment.Purpose == api.ScreenshotPurposeFinal
		}) {
		return fmt.Errorf("%w: legacy comparison import has no row review; start a fresh workflow and reimport comparison images", ErrInvalidTransition)
	}
	return nil
}

// LocalMediaArtifactResource exposes verified paths only to in-process review
// consumers. These paths are never included in public workflow projections.
type LocalMediaArtifactResource interface {
	MediaArtifactPath(api.MediaArtifactSet, api.PublicResourceID) (string, error)
}

// MediaArtifactPath resolves a current owner-scoped local artifact for CLI
// inspection. Public transports continue serving opaque artifact streams.
func (m *Module) MediaArtifactPath(ctx context.Context, owner string, workflow api.WorkflowID,
	media api.MediaArtifactSetRef, artifact api.PublicResourceID) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("resolve media artifact path: %w", err)
	}
	owner = strings.TrimSpace(owner)
	if owner == "" || workflow == "" || media.ID == "" || media.Revision == 0 || artifact == "" {
		return "", ErrWorkflowNotFound
	}
	lock := m.commandLock(owner + "\x00" + string(workflow))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(ctx, owner, workflow)
	if err != nil {
		return "", fmt.Errorf("load media artifact owner: %w", err)
	}
	if state.Workflow.Media == nil || *state.Workflow.Media != media {
		return "", ErrRevisionConflict
	}
	snapshot, exists := state.Media[media.ID]
	if !exists || snapshot.Revision != media.Revision {
		return "", ErrRevisionConflict
	}
	value, err := m.private.Get(owner, workflow, mediaPrivateResourceID(media.ID), m.clock.Now().UTC())
	if err != nil {
		return "", fmt.Errorf("load media artifact path resource: %w", err)
	}
	resource, ok := value.(LocalMediaArtifactResource)
	if !ok {
		return "", ErrPrivateResourceUnavailable
	}
	path, err := resource.MediaArtifactPath(snapshot, artifact)
	if err != nil {
		return "", fmt.Errorf("resolve private media artifact path: %w", err)
	}
	return path, nil
}

// EditComparisonsCommand changes one exact workflow's comparison intent or images.
type EditComparisonsCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Media            *api.MediaArtifactSetRef
	Instructions     api.ComparisonInstructions
	IdempotencyKey   string
}

func (EditComparisonsCommand) commandName() string              { return "edit_comparisons" }
func (EditComparisonsCommand) userIntent()                      {}
func (EditComparisonsCommand) operationKind() api.OperationKind { return api.OperationKindMedia }
func (c EditComparisonsCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	if err := c.Instructions.Validate(); err != nil {
		return "", fmt.Errorf("comparison instructions: %w", err)
	}
	return canonicalCommandFingerprint(struct {
		Revision     api.WorkflowRevision
		Media        *api.MediaArtifactSetRef
		Instructions api.ComparisonInstructions
	}{c.ExpectedRevision, c.Media, c.Instructions})
}

// ComparisonReviewSatisfied checks the retained row/mapping receipt. The media
// owner separately verifies current image bytes before any host side effect.
func ComparisonReviewSatisfied(plan *api.ComparisonPlan) bool {
	if plan == nil {
		return true
	}
	if len(comparisons.Validate(*plan)) != 0 || plan.ApprovedFingerprint == "" {
		return false
	}
	fingerprint, err := comparisons.ReviewFingerprint(*plan)
	return err == nil && fingerprint == plan.Fingerprint && fingerprint == plan.ApprovedFingerprint
}

// ComparisonMediaReviewSatisfied also binds every active cell to its selected
// local artifact, rejecting orphan images and partial row selection.
func ComparisonMediaReviewSatisfied(snapshot api.MediaArtifactSet) bool {
	if !ComparisonReviewSatisfied(snapshot.Comparisons) {
		return false
	}
	cells := make(map[api.PublicResourceID]struct{})
	if snapshot.Comparisons != nil {
		for _, row := range snapshot.Comparisons.Rows {
			if !row.Excluded {
				for _, cell := range row.Cells {
					cells[cell.ArtifactID] = struct{}{}
				}
			}
		}
	}
	for _, artifact := range snapshot.Artifacts {
		if artifact.Kind != api.MediaArtifactComparison || !artifact.Selected {
			continue
		}
		if _, reviewed := cells[artifact.ID]; !reviewed || artifact.Purpose != api.ScreenshotPurposeComparison {
			return false
		}
		delete(cells, artifact.ID)
	}
	return len(cells) == 0
}

// ComparisonMainScreenshotIDs identifies complete-row images eligible for
// ordinary screenshot reuse. Other columns never satisfy screenshot minimums.
func ComparisonMainScreenshotIDs(plan *api.ComparisonPlan) map[api.PublicResourceID]struct{} {
	result := make(map[api.PublicResourceID]struct{})
	if plan == nil || !plan.ReuseMainScreenshots || len(comparisons.Validate(*plan)) != 0 {
		return result
	}
	for _, row := range plan.Rows {
		if row.Excluded {
			continue
		}
		for _, cell := range row.Cells {
			if cell.SourceID == plan.MainSourceID {
				result[cell.ArtifactID] = struct{}{}
			}
		}
	}
	return result
}

func comparisonCellMutation(snapshot api.MediaArtifactSet, ids []api.PublicResourceID) bool {
	return slices.ContainsFunc(snapshot.Artifacts, func(artifact api.MediaArtifact) bool {
		return artifact.Kind == api.MediaArtifactComparison && slices.Contains(ids, artifact.ID)
	})
}

func invalidateDeletedComparisonCells(snapshot *api.MediaArtifactSet, deleted map[api.PublicResourceID]struct{}) error {
	if snapshot.Comparisons == nil {
		return nil
	}
	plan := *snapshot.Comparisons
	plan.Rows = slices.Clone(plan.Rows)
	changed := false
	for rowIndex := range plan.Rows {
		row := &plan.Rows[rowIndex]
		row.Cells = slices.Clone(row.Cells)
		for cellIndex := range row.Cells {
			cell := &row.Cells[cellIndex]
			if _, removed := deleted[cell.ArtifactID]; removed {
				cell.ArtifactID = ""
				cell.ContentSHA256 = ""
				cell.PictureType = ""
				cell.Confidence = nil
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	plan.ApprovedFingerprint = ""
	plan.Problems = comparisons.Validate(plan)
	var err error
	plan.Fingerprint, err = comparisons.ReviewFingerprint(plan)
	if err != nil {
		return fmt.Errorf("fingerprint deleted comparison cell: %w", err)
	}
	snapshot.Comparisons = &plan
	return nil
}

func (m *Module) editComparisons(ctx context.Context, owner string, state *State, revision api.WorkflowRevision,
	now time.Time, command EditComparisonsCommand) (CommandResult, error) {
	if command.Instructions.Action == "restore" {
		return m.restoreComparisons(ctx, owner, state, revision, now)
	}
	if err := requireReconciledExternalEffects(state.Workflow); err != nil {
		return CommandResult{}, err
	}
	builder, ok := m.mediaBuilder.(ComparisonMediaBuilder)
	if !ok {
		return CommandResult{}, fmt.Errorf("%w: comparison media service is unavailable", ErrInvalidTransition)
	}
	release, eligible, prior, private, local, err := m.comparisonExtensionContext(ctx, owner, state, command.Media, now)
	if err != nil {
		return CommandResult{}, err
	}
	instructions := command.Instructions
	clearComparisons := instructions.Action == "configure" && len(instructions.Plan.Sources) == 0 && len(instructions.Plan.Rows) == 0 &&
		len(instructions.Attachments) == 0 && len(instructions.VideoSources) == 0
	if prior == nil && state.ComparisonMedia != nil && !clearComparisons && instructions.Action != "approve" {
		var restored RetainedMediaResource
		prior, restored, err = m.retainedComparisons(ctx, owner, state, release, eligible, now)
		if err != nil {
			return CommandResult{}, err
		}
		private = restored
	}
	if instructions.Action == "approve" {
		if prior == nil || prior.Comparisons == nil {
			return CommandResult{}, fmt.Errorf("%w: capture or import comparison images before review", ErrInvalidTransition)
		}
		fingerprint, fingerprintErr := comparisons.ReviewFingerprint(*prior.Comparisons)
		if fingerprintErr != nil || fingerprint != prior.Comparisons.Fingerprint || fingerprint != instructions.ExpectedFingerprint {
			return CommandResult{}, fmt.Errorf("%w: comparison images or settings changed; review them again", ErrInvalidTransition)
		}
		// Approval never applies unreviewed edits carried in the request body.
		instructions.Plan = *prior.Comparisons
	}
	attachments := make([]StagedMediaAttachment, 0, len(instructions.Attachments))
	seen := make(map[api.WorkflowResourceID]bool, len(instructions.Attachments))
	for _, attachment := range instructions.Attachments {
		if attachment.Resource.ID == "" || seen[attachment.Resource.ID] {
			return CommandResult{}, fmt.Errorf("%w: comparison image resources must be non-empty and unique", ErrInvalidTransition)
		}
		seen[attachment.Resource.ID] = true
		value, loadErr := m.private.Get(owner, state.Workflow.ID, stagedMediaPrivateResourceID(attachment.Resource.ID), now)
		if loadErr != nil {
			return CommandResult{}, fmt.Errorf("load staged comparison image: %w", loadErr)
		}
		content, contentOK := value.(StagedMediaContent)
		if !contentOK {
			return CommandResult{}, ErrPrivateResourceIntegrity
		}
		attachments = append(attachments, StagedMediaAttachment{Attachment: attachment, Content: content})
	}
	updated, retained, err := builder.EditComparisons(ctx, release, eligible, prior, private, instructions, attachments, now)
	if err != nil {
		return CommandResult{}, fmt.Errorf("edit comparisons: %w", err)
	}
	if instructions.Action == "approve" {
		if updated.Comparisons == nil || updated.Comparisons.ApprovedFingerprint != instructions.ExpectedFingerprint ||
			!ComparisonReviewSatisfied(updated.Comparisons) {
			return CommandResult{}, fmt.Errorf("%w: comparison review no longer matches the retained images", ErrInvalidTransition)
		}
	} else if updated.Comparisons != nil {
		updated.Comparisons.ApprovedFingerprint = ""
	}
	if err := validateMediaRequirementsFingerprint(updated, eligible); err != nil {
		return CommandResult{}, err
	}
	updated.ImageRequirementsPrepared = false
	updated.ImageHostUploadSkipped = false
	if clearComparisons {
		state.ComparisonMedia = nil
	}
	return m.publishComparisonMutation(owner, state, revision, now, updated, retained, local)
}

// comparisonExtensionContext permits local image work on exact prepared/projected
// facts without granting the fresh tracker authority required by hosting.
func (m *Module) comparisonExtensionContext(ctx context.Context, owner string, state *State, media *api.MediaArtifactSetRef, now time.Time,
) (api.ReleaseRef, api.TrackerReleaseProjectionSet, *api.MediaArtifactSet, any, bool, error) {
	release, _, eligible, snapshot, private, err := m.mediaExtensionContext(ctx, owner, state, media, now)
	if err == nil || !errors.Is(err, ErrInvalidTransition) {
		return release, eligible, snapshot, private, false, err
	}
	prepared, err := currentReleaseSnapshot(state)
	if err != nil {
		return api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, nil, nil, true, err
	}
	release = api.ReleaseRef{SourcePath: prepared.Release.Source.SourcePath, Generation: prepared.Release.Generation}
	ref := state.Workflow.TrackerProjections
	if ref == nil {
		return release, api.TrackerReleaseProjectionSet{}, nil, nil, true, fmt.Errorf(
			"%w: select trackers and project their settings before comparison setup",
			ErrInvalidTransition,
		)
	}
	projections, exists := state.Projections[ref.ID]
	if !exists || projections.Revision != ref.Revision || projections.Release != *state.Workflow.Release || projections.ReleaseRef != release {
		return release, projections, nil, nil, true, fmt.Errorf("%w: comparison preparation is stale", ErrInvalidTransition)
	}
	snapshot, private, err = m.mediaExtensionSnapshot(owner, state, media, now)
	return release, projections, snapshot, private, true, err
}

func (m *Module) publishComparisonMutation(owner string, state *State, revision api.WorkflowRevision, now time.Time,
	snapshot api.MediaArtifactSet, private RetainedMediaResource, local bool) (CommandResult, error) {
	if !local {
		return m.publishMediaMutation(owner, state, revision, now, snapshot, private)
	}
	// Local review never establishes readiness for tracker-dependent media work.
	snapshot.TrackerApproval = nil
	refreshMutatedMediaStatus(&snapshot, nil, nil)
	if snapshot.Status == api.StageStatusCompleted {
		snapshot.Status = api.StageStatusPending
	}
	return m.publishRetainedMediaMutation(owner, state, revision, now, snapshot, private)
}

// PreviewComparisonFrame inspects one retained source without changing the main
// input, captured rows or approval. The preview expires with ordinary previews.
func (m *Module) PreviewComparisonFrame(ctx context.Context, owner string, workflow api.WorkflowID,
	revision api.WorkflowRevision, sourceID string, timestamp float64, step int) (api.FramePreview, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || workflow == "" || revision == 0 {
		return api.FramePreview{}, ErrWorkflowNotFound
	}
	if sourceID == "" || step < -1 || step > 1 || timestamp < 0 || math.IsNaN(timestamp) || math.IsInf(timestamp, 0) {
		return api.FramePreview{}, errors.New("comparison preview requires a source and finite non-negative timestamp")
	}
	ctx, err := m.activeMutationContext(ctx, owner, workflow)
	if err != nil {
		return api.FramePreview{}, err
	}
	if err := m.requireActiveConfig(ctx); err != nil {
		return api.FramePreview{}, err
	}
	builder, ok := m.mediaBuilder.(ComparisonMediaBuilder)
	if !ok {
		return api.FramePreview{}, ErrInvalidTransition
	}
	lock := m.commandLock(owner + "\x00" + string(workflow))
	lock.Lock()
	defer lock.Unlock()
	state, err := m.repository.Load(ctx, owner, workflow)
	if err != nil {
		return api.FramePreview{}, fmt.Errorf("load comparison preview workflow: %w", err)
	}
	if state.Workflow.Revision != revision {
		return api.FramePreview{}, ErrRevisionConflict
	}
	if state.Workflow.Media == nil {
		return api.FramePreview{}, ErrInvalidTransition
	}
	now := m.clock.Now().UTC()
	release, _, snapshot, private, _, err := m.comparisonExtensionContext(ctx, owner, &state, state.Workflow.Media, now)
	if err != nil {
		return api.FramePreview{}, err
	}
	if snapshot == nil || snapshot.Comparisons == nil {
		return api.FramePreview{}, ErrInvalidTransition
	}
	content, err := builder.PreviewComparisonFrame(ctx, release, *snapshot, private, sourceID, timestamp, step)
	if err != nil {
		return api.FramePreview{}, fmt.Errorf("preview comparison frame: %w", err)
	}
	if content.ComparisonSourceID != sourceID {
		return api.FramePreview{}, ErrPrivateResourceIntegrity
	}
	id, err := m.newID("preview")
	if err != nil {
		return api.FramePreview{}, err
	}
	previewID := api.PublicResourceID(id)
	expires := now.Add(workflowPreviewTTL)
	if err := m.private.Put(owner, workflow, previewPrivateResourceID(previewID), content, expires); err != nil {
		return api.FramePreview{}, fmt.Errorf("retain comparison preview: %w", err)
	}
	return api.FramePreview{
		Processing:          &content.Processing,
		ID:                  previewID,
		WorkflowID:          workflow,
		WorkflowRevision:    revision,
		Release:             *state.Workflow.Release,
		ComparisonSourceID:  sourceID,
		TimestampSeconds:    content.TimestampSeconds,
		PictureType:         content.PictureType,
		ObservedPTS:         content.ObservedPTS,
		TimeBaseNumerator:   content.TimeBaseNumerator,
		TimeBaseDenominator: content.TimeBaseDenominator,
		ExpiresAt:           expires,
	}, nil
}
