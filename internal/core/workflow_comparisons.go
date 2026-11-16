// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

// workflowComparisonSource retains independent source identity without changing
// the main prepared release. Only inspected, path-free facts reach the browser.
type workflowComparisonSource struct {
	Source      screenshots.ComparisonSource
	Fingerprint string
	Path        string
	Selection   *api.ComparisonProgrammeSelection
	Witnesses   map[string]screenshots.ComparisonFrameReplay
	Calibration *comparisonCalibration `json:",omitempty"`
}

type comparisonFrameRenderer interface {
	AnalyzeComparisonSource(context.Context, screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error)
	InspectComparisonSource(context.Context, string) (screenshots.ComparisonSource, error)
	SampleComparisonSource(context.Context, screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error)
	ComparisonPreview(context.Context, screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error)
	ComparisonCapture(context.Context, screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error)
}

func (b workflowMediaBuilder) comparisonService() (comparisonFrameRenderer, error) {
	if b.comparisonRenderer != nil {
		return b.comparisonRenderer, nil
	}
	root, err := db.Subdir(b.config.MainSettings.DBPath, "tmp")
	if err != nil {
		return nil, fmt.Errorf("resolve comparison renderer cache: %w", err)
	}
	logger := api.Logger(api.NopLogger{})
	if b.media != nil && b.media.logger != nil {
		logger = b.media.logger
	}
	return screenshots.NewService(b.config, logger, root, nil), nil
}

// EditComparisons keeps setup, matched rows and review authority in the existing
// immutable media snapshot. Hosting is always a separate, approval-gated action.
func (b workflowMediaBuilder) EditComparisons(ctx context.Context, release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet, prior *api.MediaArtifactSet, private any,
	instructions api.ComparisonInstructions, attachments []releaseworkflow.StagedMediaAttachment, _ time.Time,
) (api.MediaArtifactSet, releaseworkflow.RetainedMediaResource, error) {
	if b.media == nil {
		return api.MediaArtifactSet{}, nil, errors.New("comparison media service is unavailable")
	}
	snapshot, retained, err := b.mediaMutationBase(release, projections, prior, private)
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	priorMixedResolution := snapshot.Comparisons != nil && comparisonMixedResolution(*snapshot.Comparisons, retained)
	snapshot.RequirementsFingerprint, err = workflowMediaRequirementsFingerprint(projections.Projections)
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	pruneComparisonSources(&retained, snapshot.Comparisons)
	if instructions.Action == "approve" {
		if snapshot.Comparisons == nil {
			return api.MediaArtifactSet{}, nil, errors.New("comparison setup is unavailable")
		}
		if err := verifyComparisonCells(*snapshot.Comparisons, retained, true, &b.config.ScreenshotHandling.ToneMap); err != nil {
			return api.MediaArtifactSet{}, nil, err
		}
		fingerprint, fingerprintErr := comparisons.ReviewFingerprint(*snapshot.Comparisons)
		if fingerprintErr != nil || snapshot.Comparisons.Fingerprint != fingerprint || fingerprint != instructions.ExpectedFingerprint {
			return api.MediaArtifactSet{}, nil, errors.New("comparison review is stale; inspect the current rows again")
		}
		if err := verifyComparisonVideos(ctx, *snapshot.Comparisons, retained); err != nil {
			return api.MediaArtifactSet{}, nil, err
		}
		snapshot.Comparisons.ApprovedFingerprint = instructions.ExpectedFingerprint
		return snapshot, retained, nil
	}
	plan, err := instructions.Plan.Clone()
	if err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("copy comparison choices: %w", err)
	}
	for i := range plan.Sources {
		if !plan.Imported && !plan.Sources[i].Imported {
			plan.Sources[i].Overlay = true
		}
	}
	if err := plan.Preferences.Validate(); !plan.Imported && err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("comparison scene analysis: %w", err)
	}
	if plan.Preferences.ScanMode == "" {
		plan.Preferences.ScanMode = "fast"
	}
	if !plan.Imported {
		plan.RenderPolicyVersion = 1
		if plan.ResizeLargerOnly || plan.ResizeSmaller || plan.SizeReferenceSourceID != "" && plan.SizeReferenceSourceID != plan.MainSourceID {
			return api.MediaArtifactSet{}, nil, comparisonSelectionFailure(
				api.OperationFailureInvalidInput,
				"main_canvas_required",
				"Generated comparisons use the main input display size with proportional fitting and black padding. Apply the main canvas setting.",
			)
		}
		plan.SizeReferenceSourceID = plan.MainSourceID
	}
	if plan.Preferences.Count == 0 {
		plan.Preferences.Count = 4
	}
	if _, _, err := comparisons.SceneCounts(plan.Preferences, plan.Preferences.Count); err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("validate comparison scene targets: %w", err)
	}
	retainComparisonCellFacts(&plan, prior)
	invalidateOutdatedComparisonRendering(&plan, retained, b.config.ScreenshotHandling.ToneMap)
	plan.ApprovedFingerprint = ""
	pruneComparisonSources(&retained, &plan)
	if instructions.Action == "configure" && len(plan.Sources) == 0 && len(plan.Rows) == 0 && len(attachments) == 0 && len(instructions.VideoSources) == 0 {
		removeComparisonArtifacts(&snapshot, &retained)
		snapshot.Comparisons = nil
		return snapshot, retained, nil
	}
	if instructions.Action == "configure" && len(attachments) > 0 {
		plan, err = b.importWorkflowComparisons(ctx, release, &snapshot, &retained, plan, attachments)
	} else if !plan.Imported && (instructions.Action == "configure" || len(instructions.VideoSources) > 0) {
		err = b.configureComparisonVideos(ctx, release, &retained, &plan, instructions.VideoSources)
	}
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	if !plan.Imported && priorMixedResolution != comparisonMixedResolution(plan, retained) {
		for r := range plan.Rows {
			for c := range plan.Rows[r].Cells {
				if !slices.ContainsFunc(plan.Sources, func(source api.ComparisonSource) bool {
					return source.ID == plan.Rows[r].Cells[c].SourceID && !source.Imported
				}) {
					continue
				}
				plan.Rows[r].Cells[c].ArtifactID = ""
				plan.Rows[r].Cells[c].ContentSHA256 = ""
			}
		}
	}
	if instructions.Action == "capture" || instructions.Action == "match" {
		snapshot.Failures = slices.DeleteFunc(snapshot.Failures, func(f api.WorkflowFailure) bool {
			return f.Failure.ComparisonReason == "capture_cancelled" || f.Failure.ComparisonReason == "capture_deadline" ||
				instructions.Action == "match" &&
					(f.Failure.ComparisonReason == "match_cancelled" || f.Failure.ComparisonReason == "match_deadline" || f.Failure.ComparisonReason == "discovery_incomplete")
		})
	}
	activeAction := instructions.Action
	switch instructions.Action {
	case "match":
		progress := b.comparisonProgress(ctx, 5)
		defer progress.close()
		plan, err = b.matchWorkflowComparisons(ctx, &retained, plan, progress)
		if err == nil && (instructions.CaptureAfterMatch == nil || *instructions.CaptureAfterMatch) {
			activeAction = "capture"
			plan, err = b.captureWorkflowComparisons(ctx, release, &snapshot, &retained, plan, progress, true)
		}
	case "capture":
		progress := b.comparisonProgress(ctx, 1)
		defer progress.close()
		plan, err = b.captureWorkflowComparisons(ctx, release, &snapshot, &retained, plan, progress, false)
	}

	incomplete, partial := errors.AsType[*releaseworkflow.ComparisonIncompleteError](err)
	if !partial && (activeAction == "match" || activeAction == "capture") &&
		(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		reason := activeAction + "_deadline"
		if errors.Is(err, context.Canceled) {
			reason = activeAction + "_cancelled"
		}
		incomplete = &releaseworkflow.ComparisonIncompleteError{Cause: err, Reason: reason}
		partial = true
	}
	if err != nil && !partial {
		return api.MediaArtifactSet{}, nil, err
	}
	if partial {
		verificationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if verifyErr := verifyComparisonVideos(verificationCtx, plan, retained); verifyErr != nil {
			return api.MediaArtifactSet{}, nil, verifyErr
		}
		snapshot.Failures = append(snapshot.Failures, api.WorkflowFailure{Failure: api.OperationFailure{
			Code:             api.OperationFailureMissingPrerequisite,
			Operation:        api.OperationKindMedia,
			Recovery:         api.OperationRecoveryRetry,
			ComparisonReason: incomplete.Reason,
			Message:          redaction.RedactValue(incomplete.Error(), nil),
		}})
	}

	// Public mapping edits cannot replace the private bytes or invent processing.
	retainMatchedComparisonWitnesses(plan, nil, retained)
	if err = verifyComparisonCells(plan, retained, false, &b.config.ScreenshotHandling.ToneMap); err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	if !plan.Imported && plan.Analysis == nil && retained.ComparisonSources[plan.MainSourceID].Source.DurationSeconds > 0 {
		source := retained.ComparisonSources[plan.MainSourceID].Source
		duration := source.VideoDurationSeconds
		if duration <= 0 {
			duration = source.DurationSeconds
		}
		window, excluded, rangeErr := comparisons.SelectionRange(plan.Preferences, duration, comparisonTelevision(retained))
		if rangeErr != nil {
			return api.MediaArtifactSet{}, nil, fmt.Errorf("comparison scene analysis: %w", rangeErr)
		}
		plan.Analysis = &api.ComparisonAnalysisSummary{
			Version:         1,
			Profile:         comparisons.SceneProfile,
			ScanMode:        plan.Preferences.ScanMode,
			DurationSeconds: duration,
			Selection:       window,
			Excluded:        excluded,
			Reasons:         []string{"not_scanned"},
		}
	}
	if !plan.Imported && plan.Analysis != nil && plan.Preferences.SelectionWindow != nil {
		normalized := *plan.Preferences.SelectionWindow
		normalized.Excluded = slices.Clone(plan.Analysis.Excluded)
		plan.Preferences.SelectionWindow = &normalized
	}
	plan.Problems = comparisons.Validate(plan)
	plan.Fingerprint, err = comparisons.ReviewFingerprint(plan)
	if err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("fingerprint comparison review: %w", err)
	}
	snapshot.Comparisons = &plan
	syncComparisonSelections(&snapshot, plan)
	snapshot.ImageRequirementsPrepared = false
	snapshot.ImageHostUploadSkipped = false
	if partial {
		return snapshot, retained, incomplete
	}
	return snapshot, retained, nil
}

// invalidateOutdatedComparisonRendering retains decoded identity for recapture,
// while refusing to reuse pixels with stale captions or tone-mapping settings.
func invalidateOutdatedComparisonRendering(plan *api.ComparisonPlan, retained workflowMediaPrivateArtifacts, inheritedToneMap bool) {
	if plan.Imported {
		return
	}
	for r := range plan.Rows {
		for c := range plan.Rows[r].Cells {
			cell := &plan.Rows[r].Cells[c]
			for _, source := range plan.Sources {
				if source.ID == cell.SourceID && !source.Imported && cell.ArtifactID != "" &&
					(retained.ComparisonCaptions[cell.ArtifactID] != comparisonSourceCaptionVersion(retained.ComparisonSources[cell.SourceID]) ||
						!comparisonToneMapMatches(source, cell.ArtifactID, retained, inheritedToneMap)) {
					cell.ArtifactID, cell.ContentSHA256 = "", ""
				}
			}
		}
	}
}

func comparisonToneMapMatches(source api.ComparisonSource, id api.PublicResourceID, retained workflowMediaPrivateArtifacts, inherited bool) bool {
	if !retained.ComparisonSources[source.ID].Source.HDRFacts.SupportsToneMapping() {
		return true
	}
	expected := inherited
	if source.ToneMap != nil {
		expected = *source.ToneMap
	}
	captured, known := retained.ComparisonToneMaps[id]
	return known && captured == expected
}

// pruneComparisonSources prevents removed columns from supplying a retained
// video handle when a later column reuses their ID.
func pruneComparisonSources(retained *workflowMediaPrivateArtifacts, plan *api.ComparisonPlan) {
	maps.DeleteFunc(retained.ComparisonSources, func(id string, _ workflowComparisonSource) bool {
		return plan == nil || !slices.ContainsFunc(plan.Sources, func(source api.ComparisonSource) bool { return source.ID == id })
	})
}

func (b workflowMediaBuilder) configureComparisonVideos(ctx context.Context, release api.ReleaseRef,
	retained *workflowMediaPrivateArtifacts, plan *api.ComparisonPlan, selections []api.ComparisonVideoSelection) error {
	renderer, err := b.comparisonService()
	if err != nil {
		return fmt.Errorf("resolve main comparison source: %w", err)
	}
	if retained.ComparisonSources == nil {
		retained.ComparisonSources = map[string]workflowComparisonSource{}
	}
	selectionsByID := map[string]api.ComparisonVideoSelection{}
	for _, selection := range selections {
		if !slices.ContainsFunc(plan.Sources, func(source api.ComparisonSource) bool { return source.ID == selection.ID }) {
			return errors.New("comparison video selection names an unavailable column")
		}
		selectionsByID[selection.ID] = selection
	}
	if b.resolver == nil {
		return errors.New("comparison main-input resolver is unavailable")
	}
	subject, err := b.resolver.ResolveScreenshotSubject(ctx, api.MediaPlanInput{Release: release, Purpose: api.ScreenshotPurposeFinal})
	if err != nil {
		return fmt.Errorf("resolve main comparison source: %w", err)
	}
	retained.screenshotSubject = subject
	mainFound := false
	for i := range plan.Sources {
		source := &plan.Sources[i]
		selection, selected := selectionsByID[source.ID]
		path := selection.Path
		if source.ID == plan.MainSourceID {
			mainFound = true
			if path != "" && !pathing.SamePath(path, mainComparisonPath(subject)) {
				return errors.New("the main comparison source must remain the prepared release input")
			}
			path = mainComparisonPath(subject)
		}
		previous, exists := retained.ComparisonSources[source.ID]
		if path == "" && exists {
			path = previous.Path
			if path == "" {
				path = previous.Source.Path
			}
		}
		if !selected && exists {
			selection.Programme = previous.Selection
		}
		var mainSubject *api.ScreenshotSubject
		if source.ID == plan.MainSourceID {
			mainSubject = &subject
		}
		fingerprint, input, candidate, err := resolveComparisonVideo(ctx, path, selection.Programme, mainSubject)
		if err != nil {
			return fmt.Errorf("resolve main comparison source: %w", err)
		}
		inspected := previous.Source
		if !exists || previous.Fingerprint != fingerprint || inspected.FrameCount == 0 && inspected.VideoDurationSeconds == 0 {
			if input != nil {
				discRenderer, ok := renderer.(interface {
					InspectResolvedComparisonSource(context.Context, filesystem.ResolvedComparisonInput, string, string) (screenshots.ComparisonSource, error)
				})
				if !ok {
					return errors.New("comparison title decoder is unavailable")
				}
				root, rootErr := db.Subdir(b.config.MainSettings.DBPath, "tmp")
				if rootErr != nil {
					return fmt.Errorf("comparison scratch directory: %w", rootErr)
				}
				scratch, scratchErr := comparisonScratchDir(root, release.SourcePath, source.ID)
				if scratchErr != nil {
					return scratchErr
				}
				inspected, err = discRenderer.InspectResolvedComparisonSource(ctx, *input, fingerprint, scratch)
			} else {
				inspected, err = renderer.InspectComparisonSource(ctx, path)
			}
			if err != nil {
				return fmt.Errorf("inspect comparison column %s: %w", source.Label, err)
			}
			checked, _, _, err := resolveComparisonVideo(ctx, path, selection.Programme, mainSubject)
			if err != nil || checked != fingerprint {
				return api.ErrActiveInputChanged
			}
			changed := exists && previous.Fingerprint != fingerprint
			mainChanged := changed && source.ID == plan.MainSourceID
			if changed {
				plan.Anchors = slices.DeleteFunc(plan.Anchors, func(anchor api.ComparisonAnchor) bool {
					return mainChanged || anchor.SourceID == source.ID
				})
				plan.SceneMix = nil
			}
			if mainChanged {
				plan.Analysis = nil
				retained.ComparisonInventory = nil
			}
			for r := range plan.Rows {
				if mainChanged {
					plan.Rows[r].Scene = ""
					plan.Rows[r].Evidence = nil
				}
				for c := range plan.Rows[r].Cells {
					cell := &plan.Rows[r].Cells[c]
					if changed && (mainChanged || cell.SourceID == source.ID) {
						*cell = api.ComparisonCell{SourceID: cell.SourceID}
					} else if source.ID == plan.SizeReferenceSourceID && (!exists || changed) {
						cell.ArtifactID, cell.ContentSHA256 = "", ""
					}
				}
			}
		}
		source.Fingerprint = fingerprint
		source.Programme = candidate
		source.Capabilities = &api.ComparisonSourceCapabilities{
			ProgrammeSelectionRequired: input != nil,
			ToneMappingAvailable:       inspected.HDRFacts.SupportsToneMapping(),
		}
		source.Imported = false
		source.DurationSeconds = inspected.DurationSeconds
		source.FrameRate = inspected.FrameRate
		source.FrameRateNumerator, source.FrameRateDenominator = inspected.FrameRateNumerator, inspected.FrameRateDenominator
		source.Width, source.Height = inspected.DisplaySize()
		inspected.InputFingerprint = fingerprint
		witnesses := previous.Witnesses
		calibration := previous.Calibration.clone()
		if previous.Fingerprint != fingerprint {
			witnesses = nil
			calibration = nil
		}
		programme := selection.Programme
		if programme != nil {
			programme = new(*programme)
		}
		retained.ComparisonSources[source.ID] = workflowComparisonSource{
			Source:      inspected,
			Fingerprint: fingerprint,
			Path:        path,
			Selection:   programme,
			Witnesses:   witnesses,
			Calibration: calibration,
		}
	}
	if !mainFound {
		return errors.New("include the main release input in the comparison columns")
	}
	return nil
}

func comparisonVideoFingerprint(ctx context.Context, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("comparison video is inaccessible or not a regular file")
	}
	verified, err := preparedrelease.VerifyInputSource(ctx, api.PrepareInput{SourcePath: path})
	if err != nil {
		return "", fmt.Errorf("verify comparison source: %w", err)
	}
	return verified.Identity.Digest, nil
}

func verifyComparisonVideos(ctx context.Context, plan api.ComparisonPlan, retained workflowMediaPrivateArtifacts) error {
	for _, source := range plan.Sources {
		if source.Imported {
			continue
		}
		private, ok := retained.ComparisonSources[source.ID]
		if !ok || private.Fingerprint != source.Fingerprint {
			return errors.New("comparison source identity is unavailable; configure this column again")
		}
		path := private.Path
		if path == "" {
			path = private.Source.Path
		}
		var main *api.ScreenshotSubject
		if source.ID == plan.MainSourceID {
			main = &retained.screenshotSubject
		}
		current, _, _, err := resolveComparisonVideo(ctx, path, private.Selection, main)
		if ctx.Err() != nil {
			return fmt.Errorf("verify comparison source: %w", ctx.Err())
		}
		if err != nil || current != private.Fingerprint {
			return fmt.Errorf("%w: comparison video changed; rematch and recapture its rows", api.ErrActiveInputChanged)
		}
	}
	return nil
}

func (b workflowMediaBuilder) importWorkflowComparisons(ctx context.Context, release api.ReleaseRef,
	snapshot *api.MediaArtifactSet, retained *workflowMediaPrivateArtifacts, requested api.ComparisonPlan,
	attachments []releaseworkflow.StagedMediaAttachment) (api.ComparisonPlan, error) {
	requested.MainSourceID = comparisons.NormalizeImportID(requested.MainSourceID)
	for i := range requested.Sources {
		requested.Sources[i].ID = comparisons.NormalizeImportID(requested.Sources[i].ID)
	}
	for r := range requested.Rows {
		requested.Rows[r].ID = comparisons.NormalizeImportID(requested.Rows[r].ID)
		for c := range requested.Rows[r].Cells {
			requested.Rows[r].Cells[c].SourceID = comparisons.NormalizeImportID(requested.Rows[r].Cells[c].SourceID)
		}
	}
	images := make([]comparisons.ImportedImage, 0, len(attachments))
	incoming := map[api.PublicResourceID]releaseworkflow.StagedMediaAttachment{}
	used := map[api.PublicResourceID]bool{}
	for _, attachment := range attachments {
		incoming[api.PublicResourceID(attachment.Attachment.Resource.ID)] = attachment
	}
	labels := map[string]string{}
	for _, source := range requested.Sources {
		labels[source.ID] = source.Label
	}
	// A replacement imports only new bytes. Untouched cells keep their exact
	// identities, while explicit mapping/order edits are reviewed as new intent.
	for _, row := range requested.Rows {
		for _, cell := range row.Cells {
			if cell.ArtifactID == "" {
				continue
			}
			input := comparisons.ImportedImage{
				ID:       string(cell.ArtifactID),
				RowID:    row.ID,
				SourceID: cell.SourceID,
				Label:    labels[cell.SourceID],
			}
			if attachment, ok := incoming[cell.ArtifactID]; ok {
				input.Name, input.Bytes = attachment.Content.Name, attachment.Content.Bytes
				used[cell.ArtifactID] = true
			} else if image, ok := retained.ArtifactImages[cell.ArtifactID]; ok {
				var err error
				input.Bytes, err = os.ReadFile(image.Path)
				if err != nil {
					return api.ComparisonPlan{}, errors.New("a retained comparison image is unavailable; replace it")
				}
			} else {
				return api.ComparisonPlan{}, errors.New("a mapped comparison image is unavailable")
			}
			images = append(images, input)
		}
	}
	for _, attachment := range attachments {
		if err := ctx.Err(); err != nil {
			return api.ComparisonPlan{}, fmt.Errorf("import comparison images: %w", err)
		}
		id := api.PublicResourceID(attachment.Attachment.Resource.ID)
		if !used[id] {
			images = append(images, comparisons.ImportedImage{
				ID:    string(id),
				Name:  attachment.Content.Name,
				Bytes: attachment.Content.Bytes,
			})
		}
	}
	plan, _ := comparisons.Import(images, requested.MainSourceID)
	for _, wanted := range requested.Rows {
		index := slices.IndexFunc(plan.Rows, func(row api.ComparisonRow) bool { return row.ID == wanted.ID })
		if index < 0 {
			plan.Rows = append(plan.Rows, api.ComparisonRow{ID: wanted.ID, Cells: []api.ComparisonCell{}})
			index = len(plan.Rows) - 1
		}
		for _, cell := range wanted.Cells {
			if cell.ArtifactID == "" {
				plan.Rows[index].Cells = append(plan.Rows[index].Cells, api.ComparisonCell{SourceID: cell.SourceID})
			}
		}
	}
	if len(requested.Sources) > 0 {
		ordered := make([]api.ComparisonSource, 0, len(plan.Sources))
		seen := map[string]bool{}
		for _, wanted := range requested.Sources {
			ordered = append(ordered, api.ComparisonSource{
				ID:       wanted.ID,
				Label:    wanted.Label,
				Imported: true,
				Overlay:  wanted.Overlay,
			})
			seen[wanted.ID] = true
		}
		for _, source := range plan.Sources {
			if !seen[source.ID] {
				ordered = append(ordered, source)
			}
		}
		plan.Sources = ordered
	}
	plan.ReuseMainScreenshots = requested.ReuseMainScreenshots
	plan.Preferences = requested.Preferences
	unchanged := map[api.PublicResourceID]bool{}
	for _, input := range images {
		id := api.PublicResourceID(input.ID)
		digest := sha256.Sum256(input.Bytes)
		if image, exists := retained.ArtifactImages[id]; exists && image.ContentSHA256 == hex.EncodeToString(digest[:]) {
			unchanged[id] = true
		}
	}
	removeComparisonArtifactsExcept(snapshot, retained, unchanged)
	for _, input := range images {
		if unchanged[api.PublicResourceID(input.ID)] {
			continue
		}
		config, format, err := comparisons.InspectImage(input.Bytes)
		if err != nil {
			continue
		}
		extension := "." + format
		if format == "jpeg" {
			extension = ".jpg"
		}
		path, err := b.writeComparisonImage(release, input.ID, input.Bytes, extension)
		if err != nil {
			return api.ComparisonPlan{}, fmt.Errorf("import comparison images: %w", err)
		}
		sum := sha256.Sum256(input.Bytes)
		screenshot := api.ScreenshotImage{
			Path:          path,
			Purpose:       api.ScreenshotPurposeComparison,
			ContentSHA256: hex.EncodeToString(sum[:]),
			Format:        format,
			Width:         config.Width,
			Height:        config.Height,
			SizeBytes:     int64(len(input.Bytes)),
		}
		id := api.PublicResourceID(input.ID)
		retained.ArtifactImages[id] = screenshot
		snapshot.Artifacts = append(snapshot.Artifacts, comparisonMediaArtifact(id, screenshot, len(snapshot.Artifacts)))
	}
	rowOrder := map[string]int{}
	for i, row := range requested.Rows {
		rowOrder[row.ID] = i
	}
	slices.SortStableFunc(plan.Rows, func(a, b api.ComparisonRow) int {
		ai, ok := rowOrder[a.ID]
		if !ok {
			ai = len(rowOrder)
		}
		bi, ok := rowOrder[b.ID]
		if !ok {
			bi = len(rowOrder)
		}
		return ai - bi
	})
	for i := range plan.Rows {
		for _, requestedRow := range requested.Rows {
			if requestedRow.ID == plan.Rows[i].ID {
				plan.Rows[i].Excluded = requestedRow.Excluded
			}
		}
	}
	return plan, nil
}

func (b workflowMediaBuilder) writeComparisonImage(release api.ReleaseRef, cell string, data []byte, extension string) (string, error) {
	root, err := db.Subdir(b.config.MainSettings.DBPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("write retained comparison image: %w", err)
	}
	owner, _, err := paths.ReleaseTempDirFor(root, release.SourcePath, api.ReleaseInfo{})
	if err != nil {
		return "", fmt.Errorf("write retained comparison image: %w", err)
	}
	dir := filepath.Join(owner, "comparisons")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("write retained comparison image: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(cell))
	_, _ = hash.Write(data)
	destination := filepath.Join(dir, hex.EncodeToString(hash.Sum(nil))+extension)
	if current, err := os.ReadFile(destination); err == nil && bytes.Equal(current, data) {
		return destination, nil
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		return "", fmt.Errorf("write retained comparison image: %w", err)
	}
	return destination, nil
}

func (b workflowMediaBuilder) matchWorkflowComparisons(
	ctx context.Context,
	retained *workflowMediaPrivateArtifacts,
	plan api.ComparisonPlan,
	progress *comparisonProgress,
) (api.ComparisonPlan, error) {
	started := time.Now()
	plan.RenderPolicyVersion = 1
	if plan.Preferences.ScanMode == "" {
		plan.Preferences.ScanMode = "fast"
	}
	plan.SizeReferenceSourceID = plan.MainSourceID
	plan.ResizeLargerOnly, plan.ResizeSmaller = false, false
	for i := range plan.Sources {
		source := retained.ComparisonSources[plan.Sources[i].ID]
		plan.Sources[i].Width, plan.Sources[i].Height = source.Source.DisplaySize()
	}
	for id, source := range retained.ComparisonSources {
		if source.Source.InputFingerprint == "" {
			source.Source.InputFingerprint = source.Fingerprint
			retained.ComparisonSources[id] = source
		}
	}

	duration := retained.ComparisonSources[plan.MainSourceID].Source.VideoDurationSeconds
	if duration <= 0 {
		duration = retained.ComparisonSources[plan.MainSourceID].Source.DurationSeconds
	}
	allowance := 300 * time.Second
	if plan.Preferences.ScanMode == "deep" {
		allowance += comparisonDiscoveryAllowance(duration)
	}
	ctx = context.WithValue(ctx, comparisonOperationStartKey{}, started)
	ctx, cancel := context.WithTimeout(ctx, allowance)
	defer cancel()
	if err := verifyComparisonVideos(ctx, plan, *retained); err != nil {
		return plan, err
	}
	options := comparisons.MatchOptions{Count: plan.Preferences.Count, FlatCount: plan.Preferences.FlatCount}
	if options.Count <= 0 {
		options.Count = 4
	}
	var err error
	options.DarkCount, options.MotionCount, err = comparisons.SceneCounts(plan.Preferences, options.Count)
	if err != nil {
		return plan, fmt.Errorf("comparison scene analysis: %w", err)
	}
	for _, row := range plan.Rows {
		if row.Excluded {
			continue
		}
		for _, cell := range row.Cells {
			if cell.SourceID == plan.MainSourceID && cell.TimestampSeconds != nil && cell.Confidence == nil {
				options.MainMoments = append(options.MainMoments, *cell.TimestampSeconds)
			}
		}
	}
	if len(options.MainMoments) == 0 {
		for _, anchor := range plan.Anchors {
			if !slices.Contains(options.MainMoments, anchor.MainSeconds) {
				options.MainMoments = append(options.MainMoments, anchor.MainSeconds)
			}
		}
	}
	if len(options.MainMoments) > 0 {
		matched, err := b.matchManualComparisonMoments(ctx, *retained, plan, options, progress)
		if err != nil {
			return matched, err
		}
		if err := verifyComparisonVideos(ctx, matched, *retained); err != nil {
			return matched, err
		}
		return matched, nil
	}
	result, observations, accepted, err := b.tryAlignedComparisons(ctx, retained, plan, options, progress)
	if err == nil && accepted {
		err = verifyComparisonVideos(ctx, plan, *retained)
	}
	if err != nil {
		incomplete, stopped := errors.AsType[*releaseworkflow.ComparisonIncompleteError](err)
		if (stopped || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && retained.ComparisonInventory != nil {
			verificationCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer stop()
			profile, profileErr := b.comparisonSceneProfile(*retained, plan)
			owner, ownerErr := b.comparisonInventoryOwner(*retained, plan)
			ref := retained.ComparisonInventory
			if profileErr != nil || ownerErr != nil || ref.SourceProfile != profile {
				return plan, err
			}
			if verifyErr := verifyComparisonVideos(verificationCtx, plan, *retained); verifyErr != nil {
				return plan, verifyErr
			}
			if inventoryErr := readComparisonInventory(
				verificationCtx,
				ref,
				owner,
				func(screenshots.ComparisonSample) bool { return true },
			); inventoryErr != nil {
				return plan, inventoryErr
			}
			if result.Analysis == nil {
				window, excluded, rangeErr := comparisons.SelectionRange(plan.Preferences, duration, comparisonTelevision(*retained))
				if rangeErr != nil {
					return plan, fmt.Errorf("project interrupted comparison selection: %w", rangeErr)
				}
				retained.ComparisonInventory = comparisonInventoryForSelection(ref, window, excluded)
				result = comparisons.MatchResult{
					RequestedDark:   options.DarkCount,
					RequestedMotion: options.MotionCount,
					RequestedFlat:   options.FlatCount,
					Analysis:        &retained.ComparisonInventory.Summary,
				}
			}
			plan = applyComparisonMatches(plan, result, observations, options)
			retainMatchedComparisonWitnesses(plan, observations, *retained)
			reason := "match_deadline"
			if errors.Is(err, context.Canceled) {
				reason = "match_cancelled"
			}
			if stopped {
				reason = incomplete.Reason
			}
			if !slices.Contains(plan.Analysis.Reasons, reason) {
				plan.Analysis.Reasons = append(plan.Analysis.Reasons, reason)
			}
			if stopped {
				return plan, incomplete
			}
			return plan, &releaseworkflow.ComparisonIncompleteError{Cause: err, Reason: reason}
		}
		return plan, err
	}
	if !accepted {
		return plan, comparisonSelectionFailure(
			api.OperationFailureInvalidSource,
			"bounded_matching_inconclusive",
			"Bounded comparison matching was inconclusive. Inspect correspondence or revise the selection range before rematching.",
		)
	}
	plan = applyComparisonMatches(plan, result, observations, options)
	retainMatchedComparisonWitnesses(plan, observations, *retained)
	return plan, nil
}

func applyComparisonMatches(plan api.ComparisonPlan, result comparisons.MatchResult,
	observations map[string][]screenshots.ComparisonSample, options comparisons.MatchOptions) api.ComparisonPlan {
	plan.Rows = []api.ComparisonRow{}
	plan.Analysis = nil
	if result.Analysis != nil {
		analysis := *result.Analysis
		analysis.Reasons = slices.Clone(analysis.Reasons)
		analysis.Excluded = slices.Clone(analysis.Excluded)
		plan.Analysis = &analysis
	}
	plan.SceneMix = &api.ComparisonSceneMix{
		RequestedDark:   result.RequestedDark,
		RequestedFlat:   result.RequestedFlat,
		AchievedFlat:    result.AchievedFlat,
		RequestedMotion: result.RequestedMotion,
		AchievedDark:    result.AchievedDark,
		AchievedMotion:  result.AchievedMotion,
	}
	plan.Warnings = slices.Clone(result.Warnings)
	for i, row := range result.Rows {
		mapped := api.ComparisonRow{
			ID:       strconv.Itoa(i + 1),
			Scene:    row.Scene,
			Evidence: row.Evidence,
			Cells:    []api.ComparisonCell{},
		}
		for _, frame := range row.Frames {
			cell := api.ComparisonCell{SourceID: frame.SourceID, Warning: frame.Reason}
			if frame.Resolved {
				cell.TimestampSeconds = new(frame.TimestampSeconds)
				cell.PictureType = frame.FrameType
				if frame.Method != "manual" && (frame.SourceID != plan.MainSourceID || len(options.MainMoments) == 0) {
					cell.Confidence = new(frame.Confidence)
				}
				for _, observation := range observations[frame.SourceID] {
					if comparisonObservationID(observation.ComparisonFrameObservation) == frame.FrameID {
						cell.ObservedIdentity = observation.Identity
						cell.ObservedPTS = new(observation.PTS)
						cell.TimeBaseNumerator, cell.TimeBaseDenominator = observation.TimeBaseNumerator, observation.TimeBaseDenominator
						break
					}
				}
			}
			if frame.TypeFallback {
				cell.Warning = strings.TrimSpace(
					cell.Warning + " Requested picture types were unavailable at this matching moment; selected actual type " + frame.FrameType + ".",
				)
			}
			mapped.Cells = append(mapped.Cells, cell)
		}
		plan.Rows = append(plan.Rows, mapped)
	}
	return plan
}

func orderedComparisonSamples(samples []screenshots.ComparisonSample) ([]screenshots.ComparisonSample, error) {
	seen := map[string]screenshots.ComparisonFrameObservation{}
	for _, sample := range samples {
		observation := sample.ComparisonFrameObservation
		key := comparisonObservationID(observation)
		if previous, exists := seen[key]; exists {
			if previous.RelativeTimestampSeconds != observation.RelativeTimestampSeconds || previous.PTS != observation.PTS ||
				previous.PictureType != observation.PictureType || previous.PixelFormat != observation.PixelFormat ||
				previous.TimeBaseNumerator != observation.TimeBaseNumerator ||
				previous.TimeBaseDenominator != observation.TimeBaseDenominator ||
				previous.PictureChecksum != observation.PictureChecksum {
				return nil, comparisonSelectionFailure(
					api.OperationFailureInvalidSource,
					"inconsistent_frame_origins",
					"Different decode windows disagreed about the same comparison picture.",
				)
			}
		}
		seen[key] = observation
	}
	slices.SortFunc(samples, func(a, b screenshots.ComparisonSample) int {
		if a.RelativeTimestampSeconds < b.RelativeTimestampSeconds {
			return -1
		}
		if a.RelativeTimestampSeconds > b.RelativeTimestampSeconds {
			return 1
		}
		return 0
	})
	return slices.CompactFunc(samples, func(a, b screenshots.ComparisonSample) bool {
		return comparisonObservationID(a.ComparisonFrameObservation) == comparisonObservationID(b.ComparisonFrameObservation)
	}), nil
}

func comparisonSamples(frames []screenshots.ComparisonSample) []comparisons.FrameSample {
	samples := make([]comparisons.FrameSample, 0, len(frames))
	var previous []byte
	for _, frame := range frames {
		sample := comparisons.FrameSample{
			NativeSequence:      frame.NativeSequence,
			TimebaseTickSeconds: float64(frame.TimeBaseNumerator) / float64(frame.TimeBaseDenominator),
			FrameID:             comparisonObservationID(frame.ComparisonFrameObservation),
			TimestampSeconds:    frame.RelativeTimestampSeconds,
			FrameType:           frame.PictureType,
			Evidence:            frame.Evidence,
			Luma:                frame.Pixels,
		}
		if frame.Identity != nil {
			sample.Occurrence = frame.Identity.Occurrence
		}
		if frame.DurationTicks > 0 {
			sample.DisplayEndSeconds = sample.TimestampSeconds + float64(frame.DurationTicks)*sample.TimebaseTickSeconds
		}
		var brightness, motion float64
		for i, pixel := range frame.Pixels {
			brightness += float64(pixel)
			if len(previous) == len(frame.Pixels) {
				motion += math.Abs(float64(pixel) - float64(previous[i]))
			}
		}
		if len(frame.Pixels) > 0 {
			sample.Brightness = brightness / float64(len(frame.Pixels)) / 255
			sample.Motion = motion / float64(len(frame.Pixels)) / 255
		}
		samples = append(samples, sample)
		previous = frame.Pixels
	}
	return samples
}

func (b workflowMediaBuilder) captureWorkflowComparisons(
	ctx context.Context,
	release api.ReleaseRef,
	snapshot *api.MediaArtifactSet,
	retained *workflowMediaPrivateArtifacts,
	plan api.ComparisonPlan,
	progress *comparisonProgress,
	skipUnresolved bool,
) (api.ComparisonPlan, error) {
	cells := 0
	for _, row := range plan.Rows {
		if !row.Excluded {
			for _, cell := range row.Cells {
				if cell.ArtifactID == "" && cell.TimestampSeconds != nil {
					cells++
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(min(900, 30+15*cells))*time.Second)
	defer cancel()
	if retained.ArtifactImages == nil {
		retained.ArtifactImages = map[api.PublicResourceID]api.ScreenshotImage{}
	}
	if plan.Imported {
		return plan, errors.New("imported comparison pixels are retained unchanged; replace an image instead of recapturing it")
	}
	if err := verifyComparisonVideos(ctx, plan, *retained); err != nil {
		return plan, err
	}
	renderer, err := b.comparisonService()
	if err != nil {
		return plan, err
	}
	// Unchanged rows retain their images; only explicitly changed cells recapture.
	total, completed := 0, 0
	for _, row := range plan.Rows {
		if !row.Excluded {
			for _, cell := range row.Cells {
				if !skipUnresolved || cell.TimestampSeconds != nil {
					total++
				}
			}
		}
	}
	progress.begin("capture", total)
	for r := range plan.Rows {
		row := &plan.Rows[r]
		if row.Excluded {
			continue
		}
		for cellIndex := range row.Cells {
			cell := &row.Cells[cellIndex]
			if cell.SourceID == plan.MainSourceID && cell.TimestampSeconds != nil {
				if err := comparisonMainTimeAllowed(plan, *retained, *cell.TimestampSeconds); err != nil {
					return plan, err
				}
				if row.Evidence != nil && (!row.Evidence.Eligible || row.Evidence.Profile != comparisons.SceneProfile) {
					return plan, comparisonSelectionFailure(
						api.OperationFailureInvalidInput,
						"scene_ineligible",
						"Rematch this ineligible main picture before capture.",
					)
				}
				if row.Evidence == nil {
					if cell.Confidence != nil {
						return plan, comparisonSelectionFailure(
							api.OperationFailureInvalidInput,
							"analysis_policy_changed",
							"Rematch this automatic row using the current scene analysis before capture.",
						)
					}
					guard, err := b.inspectManualMainScene(ctx, plan, *retained, *cell.TimestampSeconds, nil, 0)
					if err != nil {
						return plan, err
					}
					if guard.Identity == nil || guard.Replay == nil || cell.ObservedIdentity != nil && *cell.ObservedIdentity != *guard.Identity {
						return plan, comparisonSelectionFailure(
							api.OperationFailureStaleResult,
							"changed_main_picture",
							"Inspect the main picture again before capture.",
						)
					}
					row.Evidence = guard.Evidence
					row.Scene = ""
					cell.TimestampSeconds = new(guard.RelativeTimestampSeconds)
					cell.ObservedIdentity = guard.Identity
					cell.ObservedPTS = new(guard.PTS)
					cell.TimeBaseNumerator = guard.TimeBaseNumerator
					cell.TimeBaseDenominator = guard.TimeBaseDenominator
					source := retained.ComparisonSources[cell.SourceID]
					if source.Witnesses == nil {
						source.Witnesses = map[string]screenshots.ComparisonFrameReplay{}
					}
					source.Witnesses[guard.FrameID] = cloneComparisonWitness(*guard.Replay)
					retained.ComparisonSources[cell.SourceID] = source
				}
			}
		}
		if len(row.Cells) != len(plan.Sources) {
			return plan, errors.New("match every comparison column before capture")
		}
		for c := range row.Cells {
			cell := &row.Cells[c]
			if cell.TimestampSeconds == nil && skipUnresolved {
				continue
			}
			progressID := row.ID + "/" + cell.SourceID
			label := fmt.Sprintf("Row %d / column %d", r+1, c+1)
			progress.item(completed, total, progressID, "comparison_cell", label, api.StageStatusRunning, 0)

			if cell.TimestampSeconds == nil {
				return plan, errors.New("resolve or exclude unmatched comparison rows before capture")
			}
			if cell.ArtifactID != "" && cell.ContentSHA256 != "" {
				progress.item(completed, total, progressID, "comparison_cell", label, api.StageStatusCompleted, 1000)
				completed++
				continue
			}
			source, ok := retained.ComparisonSources[cell.SourceID]
			if !ok {
				return plan, errors.New("comparison source handle is unavailable")
			}
			request, err := comparisonCaptureRequest(plan, *retained, cell.SourceID, *cell.TimestampSeconds)
			if err != nil {
				return plan, err
			}
			if cell.ObservedIdentity != nil {
				key, _ := api.CanonicalWorkflowFingerprint(*cell.ObservedIdentity)
				if witness, found := source.Witnesses[string(key)]; found {
					request.Replay = new(witness)
				} else if source.Source.Input != nil {
					return plan, errors.New("inspect or rematch this comparison frame before capture")
				}
			}
			captured, err := renderer.ComparisonCapture(ctx, request)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					reason := "capture_deadline"
					if errors.Is(err, context.Canceled) {
						reason = "capture_cancelled"
					}
					return plan, &releaseworkflow.ComparisonIncompleteError{Cause: err, Reason: reason}
				}
				return plan, fmt.Errorf("capture comparison row %s column %s: %w", row.ID, cell.SourceID, err)
			}
			if cell.ObservedPTS != nil &&
				(*cell.ObservedPTS != captured.PTS || cell.TimeBaseNumerator != captured.TimeBaseNumerator || cell.TimeBaseDenominator != captured.TimeBaseDenominator) {
				return plan, errors.New("the captured frame changed; rematch the comparison row before review")
			}
			if cell.ObservedIdentity != nil && (captured.Identity == nil || *cell.ObservedIdentity != *captured.Identity) {
				return plan, comparisonSelectionFailure(
					api.OperationFailureStaleReview,
					"changed_frame_identity",
					"The captured frame changed. Rematch and review this row again.",
				)
			}
			if captured.Replay != nil {
				if source.Witnesses == nil {
					source.Witnesses = map[string]screenshots.ComparisonFrameReplay{}
				}
				source.Witnesses[captured.FrameID] = cloneComparisonWitness(*captured.Replay)
				retained.ComparisonSources[cell.SourceID] = source
			}
			sum := sha256.Sum256(captured.ImageBytes)
			identity := sha256.Sum256(
				fmt.Appendf(
					nil,
					"%s\x00%s\x00%s\x00%s\x00%d/%d/%d\x00%s",
					row.ID,
					cell.SourceID,
					source.Fingerprint,
					hex.EncodeToString(sum[:]),
					captured.PTS,
					captured.TimeBaseNumerator,
					captured.TimeBaseDenominator,
					captured.FrameID,
				),
			)
			id := api.PublicResourceID("comparison_" + hex.EncodeToString(identity[:16]))
			path, err := b.writeComparisonImage(release, string(id), captured.ImageBytes, ".png")
			if err != nil {
				return plan, err
			}
			tonemapping := api.ScreenshotNotTonemapped
			if captured.Processing.Tonemapped {
				tonemapping = api.ScreenshotTonemapped
			}
			image := api.ScreenshotImage{
				Path:             path,
				Purpose:          api.ScreenshotPurposeComparison,
				ContentSHA256:    hex.EncodeToString(sum[:]),
				Format:           "png",
				Tonemapping:      tonemapping,
				Processing:       &captured.Processing,
				TimestampSeconds: captured.RelativeTimestampSeconds,
				Width:            captured.Width,
				Height:           captured.Height,
				SizeBytes:        int64(len(captured.ImageBytes)),
			}
			retained.ArtifactImages[id] = image
			if retained.ComparisonCaptions == nil {
				retained.ComparisonCaptions = make(map[api.PublicResourceID]int)
			}
			retained.ComparisonCaptions[id] = comparisonSourceCaptionVersion(source)
			if retained.ComparisonToneMaps == nil {
				retained.ComparisonToneMaps = make(map[api.PublicResourceID]bool)
			}
			toneMap := b.config.ScreenshotHandling.ToneMap
			if request.ToneMap != nil {
				toneMap = *request.ToneMap
			}
			retained.ComparisonToneMaps[id] = toneMap
			artifact := comparisonMediaArtifact(id, image, len(snapshot.Artifacts))
			existingIndex := slices.IndexFunc(snapshot.Artifacts, func(a api.MediaArtifact) bool { return a.ID == id })
			if existingIndex >= 0 {
				artifact.Order = snapshot.Artifacts[existingIndex].Order
				snapshot.Artifacts[existingIndex] = artifact
			} else {
				snapshot.Artifacts = append(snapshot.Artifacts, artifact)
			}
			cell.ArtifactID = id
			cell.ContentSHA256 = image.ContentSHA256
			cell.TimestampSeconds = new(captured.RelativeTimestampSeconds)
			cell.ObservedPTS = new(captured.PTS)
			cell.ObservedIdentity = captured.Identity
			cell.TimeBaseNumerator, cell.TimeBaseDenominator = captured.TimeBaseNumerator, captured.TimeBaseDenominator
			cell.PictureType = captured.PictureType
			for _, configured := range plan.Sources {
				if configured.ID == cell.SourceID && !configured.PreferBFrames && len(configured.AllowedFrameTypes) > 0 &&
					!slices.Contains(configured.AllowedFrameTypes, captured.PictureType) {
					cell.Warning = "Requested picture types " + strings.Join(
						configured.AllowedFrameTypes,
						"/",
					) + " do not include this selected matching frame; actual type is " + captured.PictureType + ". Rematch to search for a compatible frame."
				}
			}
			progress.item(completed, total, progressID, "comparison_cell", label, api.StageStatusCompleted, 1000)
			completed++
		}
	}
	if err := verifyComparisonVideos(ctx, plan, *retained); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reason := "capture_deadline"
			if errors.Is(err, context.Canceled) {
				reason = "capture_cancelled"
			}
			return plan, &releaseworkflow.ComparisonIncompleteError{Cause: err, Reason: reason}
		}
		return plan, err
	}
	progress.complete()
	return plan, nil
}

func removeComparisonArtifacts(snapshot *api.MediaArtifactSet, retained *workflowMediaPrivateArtifacts) {
	removeComparisonArtifactsExcept(snapshot, retained, nil)
}

func removeComparisonArtifactsExcept(snapshot *api.MediaArtifactSet, retained *workflowMediaPrivateArtifacts, keep map[api.PublicResourceID]bool) {
	removed := map[api.PublicResourceID]bool{}
	for _, artifact := range snapshot.Artifacts {
		if artifact.Kind == api.MediaArtifactComparison && !keep[artifact.ID] {
			removed[artifact.ID] = true
			delete(retained.ArtifactImages, artifact.ID)
			delete(retained.ComparisonCaptions, artifact.ID)
			delete(retained.ComparisonToneMaps, artifact.ID)
		}
	}
	snapshot.Artifacts = slices.DeleteFunc(snapshot.Artifacts, func(artifact api.MediaArtifact) bool {
		if removed[artifact.ID] {
			return true
		}
		if artifact.Kind == api.MediaArtifactHostedImage && removed[retained.HostedSources[artifact.ID]] {
			delete(retained.HostedSources, artifact.ID)
			delete(retained.HostedImages, artifact.ID)
			return true
		}
		return false
	})
}

func syncComparisonSelections(snapshot *api.MediaArtifactSet, plan api.ComparisonPlan) {
	selected := map[api.PublicResourceID]bool{}
	for _, row := range plan.Rows {
		if row.Excluded {
			continue
		}
		for _, cell := range row.Cells {
			selected[cell.ArtifactID] = true
		}
	}
	for i := range snapshot.Artifacts {
		artifact := &snapshot.Artifacts[i]
		if artifact.Kind == api.MediaArtifactComparison {
			artifact.Selected = selected[artifact.ID]
		}
	}
}

// A nil inheritedToneMap permits byte-only reads without a renderer configuration.
// Approval and hosting supply the current setting to reject stale rendered pixels.
func verifyComparisonCells(plan api.ComparisonPlan, retained workflowMediaPrivateArtifacts, requireComplete bool, inheritedToneMap *bool) error {
	if requireComplete && len(comparisons.Validate(plan)) > 0 {
		return errors.New("correct or exclude incomplete comparison rows before approval")
	}
	for _, row := range plan.Rows {
		if row.Excluded {
			continue
		}
		for _, cell := range row.Cells {
			if requireComplete && !plan.Imported && cell.SourceID == plan.MainSourceID && cell.TimestampSeconds != nil {
				if err := comparisonMainTimeAllowed(plan, retained, *cell.TimestampSeconds); err != nil {
					return err
				}
			}
			if cell.ArtifactID == "" {
				if requireComplete {
					return errors.New("comparison images must be captured before approval")
				}
				continue
			}
			image, ok := retained.ArtifactImages[cell.ArtifactID]
			if !ok {
				return errors.New("comparison image identity is unavailable")
			}
			if !plan.Imported && retained.ComparisonCaptions[cell.ArtifactID] != comparisonSourceCaptionVersion(retained.ComparisonSources[cell.SourceID]) &&
				slices.ContainsFunc(plan.Sources, func(source api.ComparisonSource) bool { return source.ID == cell.SourceID && !source.Imported }) {
				return errors.New("comparison caption changed; recapture and review the images")
			}
			if !plan.Imported && inheritedToneMap != nil {
				for _, source := range plan.Sources {
					if source.ID == cell.SourceID && !source.Imported && !comparisonToneMapMatches(source, cell.ArtifactID, retained, *inheritedToneMap) {
						return errors.New("comparison tone-mapping setting changed; recapture and review the images")
					}
				}
			}
			data, err := os.ReadFile(image.Path)
			if err != nil {
				return errors.New("comparison image is unavailable; replace or recapture the row")
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != cell.ContentSHA256 || image.ContentSHA256 != cell.ContentSHA256 ||
				image.Purpose != api.ScreenshotPurposeComparison {
				return errors.New("comparison image changed; replace or recapture the row and review it again")
			}
			if plan.Imported && (image.Processing != nil || image.Tonemapping != "") {
				return errors.New("imported comparison processing must remain unknown")
			}
		}
	}
	return nil
}

// verifyWorkflowComparisons is the last shared gate before hosting or projecting
// any retained comparison images, including indirect description preparation.
func verifyWorkflowComparisons(ctx context.Context, snapshot api.MediaArtifactSet, retained workflowMediaPrivateArtifacts, inheritedToneMap *bool) error {
	if !releaseworkflow.ComparisonMediaReviewSatisfied(snapshot) {
		return errors.New("review and explicitly approve the current complete comparison rows before image upload")
	}
	if snapshot.Comparisons == nil {
		return nil
	}
	if err := verifyComparisonCells(*snapshot.Comparisons, retained, true, inheritedToneMap); err != nil {
		return err
	}
	return verifyComparisonVideos(ctx, *snapshot.Comparisons, retained)
}

// PreviewComparisonFrame renders one independent source without changing the
// active release, matching rows, captured images or their approval.
func (b workflowMediaBuilder) PreviewComparisonFrame(ctx context.Context, _ api.ReleaseRef,
	snapshot api.MediaArtifactSet, private any, sourceID string, timestamp float64, step int) (releaseworkflow.MediaPreviewContent, error) {
	retained, ok := private.(workflowMediaPrivateArtifacts)
	if !ok || snapshot.Comparisons == nil {
		return releaseworkflow.MediaPreviewContent{}, errors.New("comparison setup is unavailable")
	}
	if err := verifyComparisonVideos(ctx, *snapshot.Comparisons, retained); err != nil {
		return releaseworkflow.MediaPreviewContent{}, fmt.Errorf("preview comparison source: %w", err)
	}
	source, ok := retained.ComparisonSources[sourceID]
	if !ok {
		return releaseworkflow.MediaPreviewContent{}, errors.New("comparison source is unavailable")
	}
	renderer, err := b.comparisonService()
	if err != nil {
		return releaseworkflow.MediaPreviewContent{}, fmt.Errorf("preview comparison source: %w", err)
	}
	var selected *screenshots.ComparisonFrameObservation
	if step != 0 {
		frames, err := renderer.SampleComparisonSource(ctx, screenshots.ComparisonSampleRequest{
			Source:          source.Source,
			StartSeconds:    max(0, timestamp-1),
			DurationSeconds: 2,
			StrideSeconds:   0.0001,
		})
		if err != nil {
			return releaseworkflow.MediaPreviewContent{}, fmt.Errorf("decode comparison frame step: %w", err)
		}
		found, nearest := false, 0.0
		for _, frame := range frames {
			candidate := frame.RelativeTimestampSeconds
			if step < 0 && candidate < timestamp-1e-7 && (!found || candidate > nearest) ||
				step > 0 && candidate > timestamp+1e-7 && (!found || candidate < nearest) {
				found, nearest = true, candidate
				selected = new(frame.ComparisonFrameObservation)
			}
		}
		if !found {
			return releaseworkflow.MediaPreviewContent{}, errors.New("no adjacent decoded frame in this preview window; scrub to another timestamp")
		}
		timestamp = nearest
	}
	request, err := comparisonCaptureRequest(*snapshot.Comparisons, retained, sourceID, timestamp)
	if err != nil {
		return releaseworkflow.MediaPreviewContent{}, err
	}
	if selected != nil {
		request.Replay = selected.Replay
	}
	preview, err := renderer.ComparisonPreview(ctx, request)
	if err != nil {
		return releaseworkflow.MediaPreviewContent{}, fmt.Errorf("preview comparison source: %w", err)
	}
	if selected != nil && (selected.PTS != preview.PTS || selected.TimeBaseNumerator != preview.TimeBaseNumerator ||
		selected.TimeBaseDenominator != preview.TimeBaseDenominator) {
		return releaseworkflow.MediaPreviewContent{}, errors.New("the decoded preview frame changed while stepping; scrub and inspect the source again")
	}
	witness, err := json.Marshal(preview.Replay)
	if err != nil {
		return releaseworkflow.MediaPreviewContent{}, fmt.Errorf("retain comparison preview witness: %w", err)
	}
	return releaseworkflow.MediaPreviewContent{
		ObservedIdentity:    preview.Identity,
		ComparisonWitness:   witness,
		Processing:          preview.Processing,
		Bytes:               preview.ImageBytes,
		ContentType:         "image/png",
		Width:               preview.Width,
		Height:              preview.Height,
		TimestampSeconds:    preview.RelativeTimestampSeconds,
		ComparisonSourceID:  sourceID,
		PictureType:         preview.PictureType,
		ObservedPTS:         new(preview.PTS),
		TimeBaseNumerator:   int(preview.TimeBaseNumerator),
		TimeBaseDenominator: int(preview.TimeBaseDenominator),
	}, nil
}

// PreviewReleaseWorkflowComparisonFrame retains the same owner/revision authority
// as ordinary previews while selecting an independently registered source column.
func (c *Core) PreviewReleaseWorkflowComparisonFrame(
	ctx context.Context,
	owner string,
	workflow api.WorkflowID,
	revision api.WorkflowRevision,
	source string,
	timestamp float64,
	step int,
) (api.FramePreview, error) {
	preview, err := c.workflow.PreviewComparisonFrame(ctx, owner, workflow, revision, source, timestamp, step)
	if err != nil {
		return api.FramePreview{}, classifyOperationError(api.OperationKindMedia, err)
	}
	return preview, nil
}

func comparisonMediaArtifact(id api.PublicResourceID, image api.ScreenshotImage, order int) api.MediaArtifact {
	artifact := publicMediaArtifact("", "comparison", order, api.MediaArtifactComparison, api.ScreenshotPurposeComparison, image)
	artifact.ID = id
	return artifact
}

func comparisonOverlayLabel(plan api.ComparisonPlan, sourceID string) string {
	for _, source := range plan.Sources {
		if source.ID == sourceID && !source.Imported && !plan.Imported {
			return source.Label
		}
	}
	return ""
}

// comparisonCaptureRequest resolves rendering choices against private inspected
// sources, never client-supplied dimensions. Preview and capture share this policy.
func comparisonCaptureRequest(plan api.ComparisonPlan, retained workflowMediaPrivateArtifacts, sourceID string,
	timestamp float64) (screenshots.ComparisonCaptureRequest, error) {
	source, ok := retained.ComparisonSources[sourceID]
	if !ok {
		return screenshots.ComparisonCaptureRequest{}, errors.New("comparison source handle is unavailable")
	}
	request := screenshots.ComparisonCaptureRequest{
		Source:           source.Source,
		TimestampSeconds: timestamp,
		OverlayLabel:     comparisonOverlayLabel(plan, sourceID),
		ShowResolution:   comparisonMixedResolution(plan, retained),
	}
	if strings.TrimSpace(request.OverlayLabel) == "" {
		return request, errors.New("choose a video-column label before comparison preview or capture")
	}
	for _, configured := range plan.Sources {
		if configured.ID == sourceID {
			request.ToneMap = configured.ToneMap
		}
	}
	reference, exists := retained.ComparisonSources[plan.MainSourceID]
	if !exists || !slices.ContainsFunc(plan.Sources, func(source api.ComparisonSource) bool { return source.ID == plan.MainSourceID && !source.Imported }) {
		return request, errors.New("the main comparison source is unavailable")
	}
	request.OutputWidth, request.OutputHeight = reference.Source.DisplaySize()
	if request.OutputWidth <= 0 || request.OutputHeight <= 0 {
		return request, errors.New("the main comparison source has no inspected display dimensions")
	}
	return request, nil
}

func comparisonMixedResolution(plan api.ComparisonPlan, retained workflowMediaPrivateArtifacts) bool {
	width, height := 0, 0
	for _, source := range plan.Sources {
		private, ok := retained.ComparisonSources[source.ID]
		if !ok || source.Imported {
			continue
		}
		w, h := private.Source.DisplaySize()
		if width != 0 && (w != width || h != height) {
			return true
		}
		width, height = w, h
	}
	return false
}

// retainComparisonCellFacts permits manual seeking and imported remapping without
// allowing a client edit to relabel old video pixels as a different decoded frame.
func retainComparisonCellFacts(plan *api.ComparisonPlan, prior *api.MediaArtifactSet) {
	plan.SceneMix = nil
	plan.Analysis = nil
	sameIntent := prior != nil && prior.Comparisons != nil && !plan.Imported &&
		prior.Comparisons.MainSourceID == plan.MainSourceID && sameComparisonAnalysisIntent(prior.Comparisons.Preferences, plan.Preferences)
	sameScenes := sameIntent
	priorRows := map[string]api.ComparisonRow{}
	unchangedMain := map[string]bool{}
	if prior != nil && prior.Comparisons != nil {
		for _, row := range prior.Comparisons.Rows {
			priorRows[row.ID] = row
		}
	}
	for r := range plan.Rows {
		row := &plan.Rows[r]
		previous, ok := priorRows[row.ID]
		same := ok && sameIntent
		var before, after *float64
		for _, cell := range previous.Cells {
			if cell.SourceID == plan.MainSourceID {
				before = cell.TimestampSeconds
			}
		}
		for _, cell := range row.Cells {
			if cell.SourceID == plan.MainSourceID {
				after = cell.TimestampSeconds
			}
		}
		same = same && before != nil && after != nil && *before == *after
		unchangedMain[row.ID] = same
		if same {
			row.Scene = previous.Scene
			row.Evidence = previous.Evidence
		} else {
			row.Scene = ""
			row.Evidence = nil
			sameScenes = false
			if !plan.Imported && !sameIntent && slices.ContainsFunc(previous.Cells, func(cell api.ComparisonCell) bool {
				return cell.SourceID == plan.MainSourceID && cell.Confidence != nil
			}) {
				// These automatic moments must be selected again under the new
				// analysis intent. Keeping timestamps without confidence would
				// silently turn them into explicit manual selections.
				row.Cells = []api.ComparisonCell{}
			}
		}
	}
	if sameScenes && prior.Comparisons.SceneMix != nil {
		summary := *prior.Comparisons.SceneMix
		summary.RequestedDark, summary.RequestedMotion, _ = comparisons.SceneCounts(plan.Preferences, plan.Preferences.Count)
		summary.AchievedDark, summary.AchievedMotion, summary.AchievedFlat = 0, 0, 0
		summary.RequestedFlat = plan.Preferences.FlatCount
		plan.Analysis = prior.Comparisons.Analysis
		for _, row := range plan.Rows {
			if row.Excluded {
				continue
			}
			if comparisons.SceneQualified(row.Evidence, 1) {
				summary.AchievedDark++
			}
			if comparisons.SceneQualified(row.Evidence, 0) {
				summary.AchievedMotion++
			}
			if comparisons.SceneQualified(row.Evidence, 2) {
				summary.AchievedFlat++
			}
		}
		plan.SceneMix = &summary
		if summary.AchievedDark < summary.RequestedDark || summary.AchievedMotion < summary.RequestedMotion || summary.AchievedFlat < summary.RequestedFlat {
			plan.Warnings = append(plan.Warnings, "The current rows do not meet the requested dark or motion scene mix.")
		}
	}
	cells := map[api.PublicResourceID]api.ComparisonCell{}
	moments := map[string]api.ComparisonCell{}
	if prior != nil && prior.Comparisons != nil {
		for _, row := range prior.Comparisons.Rows {
			for _, cell := range row.Cells {
				if cell.ArtifactID != "" {
					cells[cell.ArtifactID] = cell
				}
				moments[row.ID+"\x00"+cell.SourceID] = cell
			}
		}
	}
	for r := range plan.Rows {
		for c := range plan.Rows[r].Cells {
			cell := &plan.Rows[r].Cells[c]
			if plan.Imported {
				cell.ObservedIdentity = nil
				cell.TimestampSeconds = nil
				cell.FrameNumber = nil
				cell.ObservedPTS = nil
				cell.TimeBaseNumerator = 0
				cell.TimeBaseDenominator = 0
				cell.PictureType = ""
				cell.Confidence = nil
				continue
			}
			previous, knownImage := cells[cell.ArtifactID]
			if !knownImage {
				previous = moments[plan.Rows[r].ID+"\x00"+cell.SourceID]
			}
			sameMoment := previous.SourceID == cell.SourceID && previous.TimestampSeconds != nil && cell.TimestampSeconds != nil &&
				*previous.TimestampSeconds == *cell.TimestampSeconds
			changed := !sameMoment
			renderChanged := false
			if prior != nil && prior.Comparisons != nil {
				renderChanged = prior.Comparisons.SizeReferenceSourceID != plan.SizeReferenceSourceID ||
					prior.Comparisons.ResizeLargerOnly != plan.ResizeLargerOnly || prior.Comparisons.ResizeSmaller != plan.ResizeSmaller
				for _, source := range plan.Sources {
					if source.ID != cell.SourceID {
						continue
					}
					for _, old := range prior.Comparisons.Sources {
						if old.ID == source.ID &&
							(old.Overlay != source.Overlay || old.Label != source.Label || !sameOptionalBool(old.ToneMap, source.ToneMap)) {
							renderChanged = true
						}
						if old.ID == source.ID && (old.PreferBFrames != source.PreferBFrames ||
							!slices.Equal(old.AllowedFrameTypes, source.AllowedFrameTypes)) {
							changed = true
						}
					}
				}
			}
			if !knownImage || changed || renderChanged {
				cell.ArtifactID = ""
				cell.ContentSHA256 = ""
			}
			cell.FrameNumber = nil
			if changed {
				cell.ObservedIdentity = nil
				cell.ObservedPTS = nil
				cell.TimeBaseNumerator = 0
				cell.TimeBaseDenominator = 0
				cell.PictureType = ""
				cell.Confidence = nil
				if cell.SourceID == plan.MainSourceID && unchangedMain[plan.Rows[r].ID] {
					// Rendering invalidation must not turn an automatic main moment into a manual pin.
					cell.Confidence = previous.Confidence
				}
				continue
			}
			if knownImage && !renderChanged {
				cell.ContentSHA256 = previous.ContentSHA256
			}
			cell.ObservedPTS = previous.ObservedPTS
			cell.ObservedIdentity = previous.ObservedIdentity
			cell.TimeBaseNumerator = previous.TimeBaseNumerator
			cell.TimeBaseDenominator = previous.TimeBaseDenominator
			cell.PictureType = previous.PictureType
			cell.Confidence = previous.Confidence
			if cell.SourceID != plan.MainSourceID && !unchangedMain[plan.Rows[r].ID] {
				cell.Confidence = nil
			}
		}
	}
}

func sameOptionalBool(a, b *bool) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// MediaArtifactPath exposes a verified retained path only to the owning local
// CLI adapter; browser projections continue using opaque resource identities.
func (a workflowMediaPrivateArtifacts) MediaArtifactPath(snapshot api.MediaArtifactSet, id api.PublicResourceID) (string, error) {
	image, err := a.imageForArtifact(snapshot, id)
	if err != nil {
		return "", err
	}
	if snapshot.Comparisons != nil {
		if err := verifyComparisonCells(*snapshot.Comparisons, a, false, nil); err != nil {
			return "", err
		}
	}
	return image.Path, nil
}

// ReleaseWorkflowMediaArtifactPath gives local CLI review the retained pixels,
// not the potentially modified original import file.
func (c *Core) ReleaseWorkflowMediaArtifactPath(
	ctx context.Context,
	owner string,
	workflow api.WorkflowID,
	media api.MediaArtifactSetRef,
	id api.PublicResourceID,
) (string, error) {
	path, err := c.workflow.MediaArtifactPath(ctx, owner, workflow, media, id)
	if err != nil {
		return "", fmt.Errorf("inspect retained comparison image: %w", err)
	}
	return path, nil
}

// RestoreComparisons rebinds only comparison-owned media after preparation changes.
// It preserves corrections and pixels, never ordinary media or prior approval.
func (b workflowMediaBuilder) RestoreComparisons(ctx context.Context, release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet, previous api.MediaArtifactSet, private any, _ time.Time,
) (api.MediaArtifactSet, releaseworkflow.RetainedMediaResource, error) {
	old, ok := private.(workflowMediaPrivateArtifacts)
	if !ok || previous.Comparisons == nil {
		return api.MediaArtifactSet{}, nil, errors.New("retained comparison setup is unavailable")
	}
	snapshot, retained, err := b.mediaMutationBase(release, projections, nil, nil)
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	plan, err := previous.Comparisons.Clone()
	if err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("restore comparison setup: %w", err)
	}
	plan.ApprovedFingerprint = ""
	if !plan.Imported {
		plan.RenderPolicyVersion = 1
		if plan.SizeReferenceSourceID != plan.MainSourceID || plan.ResizeLargerOnly || plan.ResizeSmaller {
			plan.Warnings = append(plan.Warnings, "Rendering policy changed; recapture and review the main-size canvas.")
		}
		plan.SizeReferenceSourceID = plan.MainSourceID
		plan.ResizeLargerOnly = false
		plan.ResizeSmaller = false
		if plan.Preferences.ScanMode == "" {
			plan.Preferences.ScanMode = "fast"
		}
	}
	retained.ComparisonSources = cloneComparisonSources(old.ComparisonSources)
	retained.ComparisonInventory = cloneComparisonInventory(old.ComparisonInventory)
	retained.ComparisonAnalysisExecutable = old.ComparisonAnalysisExecutable
	retained.ComparisonCaptions = maps.Clone(old.ComparisonCaptions)
	retained.ComparisonToneMaps = maps.Clone(old.ComparisonToneMaps)
	mainChanged := previous.ReleaseRef.SourcePath != "" && !pathing.SamePath(previous.ReleaseRef.SourcePath, release.SourcePath)
	if b.resolver != nil {
		subject, err := b.resolver.ResolveScreenshotSubject(ctx, api.MediaPlanInput{Release: release, Purpose: api.ScreenshotPurposeFinal})
		if err != nil {
			return api.MediaArtifactSet{}, nil, fmt.Errorf("restore comparison main source: %w", err)
		}
		retained.screenshotSubject = subject
		if source, ok := old.ComparisonSources[plan.MainSourceID]; ok {
			path := source.Path
			if path == "" {
				path = source.Source.Path
			}
			if !pathing.SamePath(path, mainComparisonPath(subject)) {
				mainChanged = true
			}
		}
	}
	changed := map[string]bool{}
	for _, source := range plan.Sources {
		if source.Imported {
			continue
		}
		original, ok := old.ComparisonSources[source.ID]
		if !ok {
			changed[source.ID] = true
			continue
		}
		path := original.Path
		if path == "" {
			path = original.Source.Path
		}
		var subject *api.ScreenshotSubject
		if source.ID == plan.MainSourceID && b.resolver != nil {
			subject = &retained.screenshotSubject
		}
		fingerprint, _, _, err := resolveComparisonVideo(ctx, path, original.Selection, subject)
		if err != nil || fingerprint != original.Fingerprint {
			changed[source.ID] = true
		}
	}
	mainChanged = mainChanged || changed[plan.MainSourceID]
	if mainChanged {
		delete(retained.ComparisonSources, plan.MainSourceID)
		retained.ComparisonInventory = nil
		plan.Analysis = nil
		for i := range plan.Rows {
			plan.Rows[i].Scene = ""
			plan.Rows[i].Evidence = nil
		}
		plan.Anchors = nil
		plan.SceneMix = nil
	}
	for r := range plan.Rows {
		for c := range plan.Rows[r].Cells {
			cell := &plan.Rows[r].Cells[c]
			if mainChanged || changed[cell.SourceID] {
				*cell = api.ComparisonCell{SourceID: cell.SourceID, Warning: "The comparison input changed; select the source again and rematch this row."}
				continue
			}
			image, ok := old.ArtifactImages[cell.ArtifactID]
			if !ok {
				cell.ArtifactID = ""
				cell.ContentSHA256 = ""
				continue
			}
			content, err := os.ReadFile(image.Path)
			sum := sha256.Sum256(content)
			if err != nil || hex.EncodeToString(sum[:]) != cell.ContentSHA256 {
				cell.ArtifactID = ""
				cell.ContentSHA256 = ""
				cell.Warning = "This image is unavailable or changed; replace or recapture it before review."
				continue
			}
			retained.ArtifactImages[cell.ArtifactID] = image
		}
	}
	for _, source := range plan.Sources {
		if changed[source.ID] {
			plan.Anchors = slices.DeleteFunc(plan.Anchors, func(a api.ComparisonAnchor) bool { return a.SourceID == source.ID })
		}
	}
	if inventory := retained.ComparisonInventory; inventory != nil {
		owner, ownerErr := b.comparisonInventoryOwner(retained, plan)
		profile, profileErr := b.comparisonSceneProfile(retained, plan)
		if retained.ComparisonAnalysisExecutable != "" {
			renderer, rendererErr := b.comparisonService()
			if rendererErr != nil {
				profileErr = rendererErr
			} else if inspector, ok := renderer.(interface {
				ComparisonAnalysisExecutableIdentity(context.Context) (string, error)
			}); ok {
				identity, identityErr := inspector.ComparisonAnalysisExecutableIdentity(ctx)
				if identityErr != nil || identity != retained.ComparisonAnalysisExecutable {
					profileErr = errors.New("comparison analysis executable changed")
				}
			} else {
				profileErr = errors.New("comparison analysis executable is unavailable")
			}
		}
		var inventoryErr error
		if ownerErr == nil && profileErr == nil && profile == inventory.SourceProfile {
			inventoryErr = readComparisonInventory(ctx, inventory, owner, func(screenshots.ComparisonSample) bool { return true })
		} else {
			inventoryErr = errors.New("comparison inventory profile changed")
		}
		if inventoryErr != nil {
			retained.ComparisonInventory = nil
			if profileErr != nil || profile != inventory.SourceProfile {
				for i := range plan.Rows {
					plan.Rows[i].Scene = ""
					plan.Rows[i].Evidence = nil
				}
				plan.SceneMix = nil
			}
			if plan.Analysis != nil {
				plan.Analysis.Complete = false
				plan.Analysis.RankingComplete = false
				plan.Analysis.Reasons = append(plan.Analysis.Reasons, "inventory_unavailable")
			}
		}
	}
	invalidateOutdatedComparisonRendering(&plan, retained, b.config.ScreenshotHandling.ToneMap)
	kept := map[api.PublicResourceID]bool{}
	for _, row := range plan.Rows {
		for _, cell := range row.Cells {
			if cell.ArtifactID != "" {
				kept[cell.ArtifactID] = true
			}
		}
	}
	for _, artifact := range previous.Artifacts {
		if artifact.Kind == api.MediaArtifactComparison && kept[artifact.ID] {
			snapshot.Artifacts = append(snapshot.Artifacts, artifact)
			continue
		}
		source := old.HostedSources[artifact.ID]
		if artifact.Kind != api.MediaArtifactHostedImage || !kept[source] {
			continue
		}
		snapshot.Artifacts = append(snapshot.Artifacts, artifact)
		retained.HostedSources[artifact.ID] = source
		retained.HostedImages[artifact.ID] = old.HostedImages[artifact.ID]
	}
	plan.Problems = comparisons.Validate(plan)
	plan.Fingerprint, err = comparisons.ReviewFingerprint(plan)
	if err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("fingerprint restored comparisons: %w", err)
	}
	snapshot.Comparisons = &plan
	syncComparisonSelections(&snapshot, plan)
	return snapshot, retained, nil
}

func sameComparisonAnalysisIntent(a, b api.ComparisonPreferences) bool {
	if a.ScanMode == "" {
		a.ScanMode = "fast"
	}
	if b.ScanMode == "" {
		b.ScanMode = "fast"
	}
	before, _ := api.CanonicalWorkflowFingerprint(struct {
		Mode      string
		Selection *api.ComparisonSelectionWindow
	}{a.ScanMode, a.SelectionWindow})
	after, _ := api.CanonicalWorkflowFingerprint(struct {
		Mode      string
		Selection *api.ComparisonSelectionWindow
	}{b.ScanMode, b.SelectionWindow})
	return before == after
}
