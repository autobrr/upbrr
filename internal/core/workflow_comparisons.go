// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/preparedrelease"
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
}

type comparisonFrameRenderer interface {
	InspectComparisonSource(context.Context, string) (screenshots.ComparisonSource, error)
	SampleComparisonSource(context.Context, screenshots.ComparisonSource, float64, float64, float64) ([]screenshots.ComparisonSample, error)
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
	instructions api.ComparisonInstructions, attachments []releaseworkflow.StagedMediaAttachment, now time.Time,
) (api.MediaArtifactSet, releaseworkflow.RetainedMediaResource, error) {
	if b.media == nil {
		return api.MediaArtifactSet{}, nil, errors.New("comparison media service is unavailable")
	}
	snapshot, retained, err := b.mediaMutationBase(release, projections, prior, private)
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	if instructions.Action == "approve" {
		if snapshot.Comparisons == nil {
			return api.MediaArtifactSet{}, nil, errors.New("comparison setup is unavailable")
		}
		if err := verifyComparisonCells(*snapshot.Comparisons, retained, true); err != nil {
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
	retainComparisonCellFacts(&plan, prior)
	plan.ApprovedFingerprint = ""
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
	switch instructions.Action {
	case "match":
		plan, err = b.matchWorkflowComparisons(ctx, retained, plan)
		if err == nil {
			// Retained strict captures make resolved proposals inspectable in the
			// row grid immediately. Unresolved rows remain visible and unapproved.
			for r, row := range plan.Rows {
				if row.Excluded || slices.ContainsFunc(row.Cells, func(cell api.ComparisonCell) bool { return cell.TimestampSeconds == nil }) {
					continue
				}
				proposal := plan
				proposal.Rows = []api.ComparisonRow{row}
				proposal, err = b.captureWorkflowComparisons(ctx, release, &snapshot, &retained, proposal, now)
				if err != nil {
					break
				}
				plan.Rows[r] = proposal.Rows[0]
			}
		}
	case "capture":
		plan, err = b.captureWorkflowComparisons(ctx, release, &snapshot, &retained, plan, now)
	}
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	// Public mapping edits cannot replace the private bytes or invent processing.
	if err = verifyComparisonCells(plan, retained, false); err != nil {
		return api.MediaArtifactSet{}, nil, err
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
	return snapshot, retained, nil
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
	pathsByID := map[string]string{}
	for _, selection := range selections {
		pathsByID[selection.ID] = selection.Path
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
		path := pathsByID[source.ID]
		if source.ID == plan.MainSourceID {
			mainFound = true
			if path != "" && !pathing.SamePath(path, subject.VideoPath) {
				return errors.New("the main comparison source must remain the prepared release input")
			}
			path = subject.VideoPath
		}
		previous, exists := retained.ComparisonSources[source.ID]
		if path == "" && exists {
			path = previous.Source.Path
		}
		fingerprint, err := comparisonVideoFingerprint(ctx, path)
		if err != nil {
			return fmt.Errorf("resolve main comparison source: %w", err)
		}
		inspected := previous.Source
		if !exists || previous.Fingerprint != fingerprint {
			inspected, err = renderer.InspectComparisonSource(ctx, path)
			if err != nil {
				return fmt.Errorf("inspect comparison column %s: %w", source.Label, err)
			}
			checked, err := comparisonVideoFingerprint(ctx, path)
			if err != nil || checked != fingerprint {
				return api.ErrActiveInputChanged
			}
			if exists {
				mainChanged := source.ID == plan.MainSourceID
				plan.Anchors = slices.DeleteFunc(plan.Anchors, func(anchor api.ComparisonAnchor) bool {
					return mainChanged || anchor.SourceID == source.ID
				})
				plan.SceneMix = nil
				for r := range plan.Rows {
					for c := range plan.Rows[r].Cells {
						cell := &plan.Rows[r].Cells[c]
						if mainChanged || cell.SourceID == source.ID {
							*cell = api.ComparisonCell{SourceID: cell.SourceID}
						}
					}
				}
			}
		}
		source.Fingerprint = fingerprint
		source.Imported = false
		source.DurationSeconds = inspected.DurationSeconds
		source.FrameRate = inspected.FrameRate
		source.Width, source.Height = inspected.Width, inspected.Height
		retained.ComparisonSources[source.ID] = workflowComparisonSource{Source: inspected, Fingerprint: fingerprint}
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
		current, err := comparisonVideoFingerprint(ctx, private.Source.Path)
		if err != nil || current != private.Fingerprint {
			return errors.New("comparison video changed; rematch and recapture its rows")
		}
	}
	return nil
}

func (b workflowMediaBuilder) importWorkflowComparisons(ctx context.Context, release api.ReleaseRef,
	snapshot *api.MediaArtifactSet, retained *workflowMediaPrivateArtifacts, requested api.ComparisonPlan,
	attachments []releaseworkflow.StagedMediaAttachment) (api.ComparisonPlan, error) {
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
	if len(requested.Sources) > 0 {
		ordered := make([]api.ComparisonSource, 0, len(plan.Sources))
		seen := map[string]bool{}
		for _, wanted := range requested.Sources {
			for _, source := range plan.Sources {
				if source.ID == wanted.ID {
					source.Label = wanted.Label
					source.Overlay = wanted.Overlay
					ordered = append(ordered, source)
					seen[source.ID] = true
					break
				}
			}
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
	removeComparisonArtifacts(snapshot, retained)
	for _, input := range images {
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
	retained workflowMediaPrivateArtifacts,
	plan api.ComparisonPlan,
) (api.ComparisonPlan, error) {
	if err := verifyComparisonVideos(ctx, plan, retained); err != nil {
		return plan, fmt.Errorf("match comparison sources: %w", err)
	}
	renderer, err := b.comparisonService()
	if err != nil {
		return plan, fmt.Errorf("match comparison sources: %w", err)
	}
	sampled := make([]comparisons.SampledSource, 0, len(plan.Sources))
	observations := map[string][]screenshots.ComparisonSample{}
	for _, source := range plan.Sources {
		private, ok := retained.ComparisonSources[source.ID]
		if !ok {
			return plan, errors.New("comparison video source is unavailable")
		}
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:    "comparisons",
			ItemID:   source.ID,
			Kind:     "comparison_source",
			Label:    source.Label,
			Status:   api.StageStatusRunning,
			ItemOnly: true,
		})
		frames, err := renderer.SampleComparisonSource(ctx, private.Source, 0, private.Source.DurationSeconds, 0.5)
		if err != nil {
			return plan, fmt.Errorf("sample comparison column %s: %w", source.Label, err)
		}
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:    "comparisons",
			ItemID:   source.ID,
			Kind:     "comparison_source",
			Label:    source.Label,
			Status:   api.StageStatusCompleted,
			ItemOnly: true,
		})
		observations[source.ID] = frames
		samples := comparisons.SampledSource{ID: source.ID, AllowedTypes: slices.Clone(source.AllowedFrameTypes)}
		for _, anchor := range plan.Anchors {
			if anchor.SourceID == source.ID {
				samples.Anchors = append(samples.Anchors, comparisons.MatchAnchor{MainSeconds: anchor.MainSeconds, SourceSeconds: anchor.SourceSeconds})
			}
		}
		// Explicitly inspected moments may fall between coarse samples. Decode
		// their neighborhoods before matching, including otherwise ambiguous scenes.
		var manualMoments []float64
		for _, anchor := range plan.Anchors {
			if source.ID == plan.MainSourceID {
				manualMoments = append(manualMoments, anchor.MainSeconds)
			} else if anchor.SourceID == source.ID {
				manualMoments = append(manualMoments, anchor.SourceSeconds)
			}
		}
		if source.ID == plan.MainSourceID {
			for _, row := range plan.Rows {
				for _, cell := range row.Cells {
					if !row.Excluded && cell.SourceID == source.ID && cell.TimestampSeconds != nil {
						manualMoments = append(manualMoments, *cell.TimestampSeconds)
					}
				}
			}
		}
		for _, moment := range manualMoments {
			dense, err := renderer.SampleComparisonSource(ctx, private.Source, max(0, moment-1), 2, 0.0001)
			if err != nil {
				return plan, fmt.Errorf("decode manual comparison moment: %w", err)
			}
			frames = append(frames, dense...)
		}
		frames = orderedComparisonSamples(frames)
		observations[source.ID] = frames
		samples.Frames = comparisonSamples(frames)
		sampled = append(sampled, samples)
	}
	options := comparisons.MatchOptions{
		Count:          plan.Preferences.Count,
		DarkFraction:   float64(plan.Preferences.DarkPercent) / 100,
		MotionFraction: float64(plan.Preferences.MotionPercent) / 100,
	}
	if options.Count <= 0 {
		options.Count = 4
	}
	// Manually selected row moments remain explicit; each other source is still
	// matched visually unless an exact inspected anchor confirms correspondence.
	for _, row := range plan.Rows {
		if row.Excluded {
			continue
		}
		for _, cell := range row.Cells {
			if cell.SourceID == plan.MainSourceID && cell.TimestampSeconds != nil {
				options.MainMoments = append(options.MainMoments, *cell.TimestampSeconds)
			}
		}
	}
	result, err := comparisons.Match(sampled, plan.MainSourceID, options)
	if err != nil {
		return plan, fmt.Errorf("match comparison sources: %w", err)
	}
	// Refine near every proposed scene with actual decoded frames. A picture-type
	// preference can choose a nearby match, never an unrelated distant scene.
	for i := range sampled {
		source := retained.ComparisonSources[sampled[i].ID]
		refined := slices.Clone(observations[sampled[i].ID])
		for _, row := range result.Rows {
			for _, frame := range row.Frames {
				if frame.SourceID != sampled[i].ID || !frame.Resolved {
					continue
				}
				start := max(0, frame.TimestampSeconds-1)
				dense, err := renderer.SampleComparisonSource(ctx, source.Source, start, 2, 0.0001)
				if err != nil {
					return plan, fmt.Errorf("match comparison sources: %w", err)
				}
				refined = append(refined, dense...)
			}
		}
		refined = orderedComparisonSamples(refined)
		observations[sampled[i].ID] = refined
		sampled[i].Frames = comparisonSamples(refined)
	}
	result, err = comparisons.Match(sampled, plan.MainSourceID, options)
	if err != nil {
		return plan, fmt.Errorf("match comparison sources: %w", err)
	}
	plan.Rows = nil
	plan.SceneMix = &api.ComparisonSceneMix{
		RequestedDark:   result.RequestedDark,
		RequestedMotion: result.RequestedMotion,
		AchievedDark:    result.AchievedDark,
		AchievedMotion:  result.AchievedMotion,
	}
	plan.Warnings = slices.Clone(result.Warnings)
	for i, row := range result.Rows {
		mapped := api.ComparisonRow{ID: strconv.Itoa(i + 1), Scene: row.Scene}
		for _, frame := range row.Frames {
			cell := api.ComparisonCell{SourceID: frame.SourceID, Warning: frame.Reason}
			if frame.Resolved {
				cell.TimestampSeconds = new(frame.TimestampSeconds)
				cell.PictureType = frame.FrameType
				if frame.Method != "manual" {
					cell.Confidence = new(frame.Confidence)
				}
				for _, observation := range observations[frame.SourceID] {
					if math.Abs(observation.RelativeTimestampSeconds-frame.TimestampSeconds) < 1e-7 {
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
	if err := verifyComparisonVideos(ctx, plan, retained); err != nil {
		return plan, fmt.Errorf("match comparison sources: %w", err)
	}
	return plan, nil
}

func orderedComparisonSamples(samples []screenshots.ComparisonSample) []screenshots.ComparisonSample {
	slices.SortFunc(samples, func(a, b screenshots.ComparisonSample) int {
		if a.PTS < b.PTS {
			return -1
		}
		if a.PTS > b.PTS {
			return 1
		}
		return 0
	})
	return slices.CompactFunc(samples, func(a, b screenshots.ComparisonSample) bool {
		return a.PTS == b.PTS && a.TimeBaseNumerator == b.TimeBaseNumerator && a.TimeBaseDenominator == b.TimeBaseDenominator
	})
}

func comparisonSamples(frames []screenshots.ComparisonSample) []comparisons.FrameSample {
	samples := make([]comparisons.FrameSample, 0, len(frames))
	var previous []byte
	for _, frame := range frames {
		sample := comparisons.FrameSample{
			TimestampSeconds: frame.RelativeTimestampSeconds,
			FrameType:        frame.PictureType,
			Luma:             frame.Pixels,
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

func (b workflowMediaBuilder) captureWorkflowComparisons(ctx context.Context, release api.ReleaseRef,
	snapshot *api.MediaArtifactSet, retained *workflowMediaPrivateArtifacts, plan api.ComparisonPlan, _ time.Time) (api.ComparisonPlan, error) {
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
			total += len(row.Cells)
		}
	}
	for r := range plan.Rows {
		row := &plan.Rows[r]
		if row.Excluded {
			continue
		}
		if len(row.Cells) != len(plan.Sources) {
			return plan, errors.New("match every comparison column before capture")
		}
		for c := range row.Cells {
			cell := &row.Cells[c]
			api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
				Phase:     "comparisons",
				ItemID:    row.ID + "/" + cell.SourceID,
				Kind:      "comparison_cell",
				Label:     "Row " + row.ID + " / " + cell.SourceID,
				Status:    api.StageStatusRunning,
				Completed: completed,
				Total:     total,
			})
			if cell.TimestampSeconds == nil {
				return plan, errors.New("resolve or exclude unmatched comparison rows before capture")
			}
			if cell.ArtifactID != "" && cell.ContentSHA256 != "" {
				completed++
				continue
			}
			source, ok := retained.ComparisonSources[cell.SourceID]
			if !ok {
				return plan, errors.New("comparison source handle is unavailable")
			}
			captured, err := renderer.ComparisonCapture(ctx, screenshots.ComparisonCaptureRequest{
				Source:           source.Source,
				TimestampSeconds: *cell.TimestampSeconds,
				OverlayLabel:     comparisonOverlayLabel(plan, cell.SourceID),
			})
			if err != nil {
				return plan, fmt.Errorf("capture comparison row %s column %s: %w", row.ID, cell.SourceID, err)
			}
			if cell.ObservedPTS != nil &&
				(*cell.ObservedPTS != captured.PTS || cell.TimeBaseNumerator != captured.TimeBaseNumerator || cell.TimeBaseDenominator != captured.TimeBaseDenominator) {
				return plan, errors.New("the captured frame changed; rematch the comparison row before review")
			}
			sum := sha256.Sum256(captured.ImageBytes)
			identity := sha256.Sum256(
				fmt.Appendf(
					nil,
					"%s\x00%s\x00%s\x00%s\x00%d/%d/%d",
					row.ID,
					cell.SourceID,
					source.Fingerprint,
					hex.EncodeToString(sum[:]),
					captured.PTS,
					captured.TimeBaseNumerator,
					captured.TimeBaseDenominator,
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
			cell.TimeBaseNumerator, cell.TimeBaseDenominator = captured.TimeBaseNumerator, captured.TimeBaseDenominator
			cell.PictureType = captured.PictureType
			for _, configured := range plan.Sources {
				if configured.ID == cell.SourceID && len(configured.AllowedFrameTypes) > 0 &&
					!slices.Contains(configured.AllowedFrameTypes, captured.PictureType) {
					cell.Warning = "Requested picture types " + strings.Join(
						configured.AllowedFrameTypes,
						"/",
					) + " do not include this selected matching frame; actual type is " + captured.PictureType + ". Rematch to search for a compatible frame."
				}
			}
			completed++
			api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
				Phase:     "comparisons",
				ItemID:    row.ID + "/" + cell.SourceID,
				Kind:      "comparison_cell",
				Label:     "Row " + row.ID + " / " + cell.SourceID,
				Status:    api.StageStatusCompleted,
				Completed: completed,
				Total:     total,
			})
		}
	}
	if err := verifyComparisonVideos(ctx, plan, *retained); err != nil {
		return plan, err
	}
	return plan, nil
}

func removeComparisonArtifacts(snapshot *api.MediaArtifactSet, retained *workflowMediaPrivateArtifacts) {
	removed := map[api.PublicResourceID]bool{}
	for _, artifact := range snapshot.Artifacts {
		if artifact.Kind == api.MediaArtifactComparison {
			removed[artifact.ID] = true
			delete(retained.ArtifactImages, artifact.ID)
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

func verifyComparisonCells(plan api.ComparisonPlan, retained workflowMediaPrivateArtifacts, requireComplete bool) error {
	if requireComplete && len(comparisons.Validate(plan)) > 0 {
		return errors.New("correct or exclude incomplete comparison rows before approval")
	}
	for _, row := range plan.Rows {
		if row.Excluded {
			continue
		}
		for _, cell := range row.Cells {
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
func verifyWorkflowComparisons(ctx context.Context, snapshot api.MediaArtifactSet, retained workflowMediaPrivateArtifacts) error {
	if !releaseworkflow.ComparisonMediaReviewSatisfied(snapshot) {
		return errors.New("review and explicitly approve the current complete comparison rows before image upload")
	}
	if snapshot.Comparisons == nil {
		return nil
	}
	if err := verifyComparisonCells(*snapshot.Comparisons, retained, true); err != nil {
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
		frames, err := renderer.SampleComparisonSource(ctx, source.Source, max(0, timestamp-1), 2, 0.0001)
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
	preview, err := renderer.ComparisonPreview(ctx, screenshots.ComparisonCaptureRequest{
		Source:           source.Source,
		TimestampSeconds: timestamp,
		OverlayLabel:     comparisonOverlayLabel(*snapshot.Comparisons, sourceID),
	})
	if err != nil {
		return releaseworkflow.MediaPreviewContent{}, fmt.Errorf("preview comparison source: %w", err)
	}
	if selected != nil && (selected.PTS != preview.PTS || selected.TimeBaseNumerator != preview.TimeBaseNumerator ||
		selected.TimeBaseDenominator != preview.TimeBaseDenominator) {
		return releaseworkflow.MediaPreviewContent{}, errors.New("the decoded preview frame changed while stepping; scrub and inspect the source again")
	}
	return releaseworkflow.MediaPreviewContent{
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
		if source.ID == sourceID && source.Overlay {
			return source.Label
		}
	}
	return ""
}

// retainComparisonCellFacts permits manual seeking and imported remapping without
// allowing a client edit to relabel old video pixels as a different decoded frame.
func retainComparisonCellFacts(plan *api.ComparisonPlan, prior *api.MediaArtifactSet) {
	plan.SceneMix = nil
	sameScenes := prior != nil && prior.Comparisons != nil && !plan.Imported
	priorRows := map[string]api.ComparisonRow{}
	if sameScenes {
		for _, row := range prior.Comparisons.Rows {
			priorRows[row.ID] = row
		}
	}
	for r := range plan.Rows {
		row := &plan.Rows[r]
		previous, ok := priorRows[row.ID]
		same := ok
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
		if same {
			row.Scene = previous.Scene
		} else {
			row.Scene = ""
			sameScenes = false
		}
	}
	if sameScenes && prior.Comparisons.SceneMix != nil {
		summary := *prior.Comparisons.SceneMix
		summary.RequestedDark = int(math.Ceil(float64(plan.Preferences.Count) * float64(plan.Preferences.DarkPercent) / 100))
		summary.RequestedMotion = int(math.Ceil(float64(plan.Preferences.Count) * float64(plan.Preferences.MotionPercent) / 100))
		summary.AchievedDark, summary.AchievedMotion = 0, 0
		for _, row := range plan.Rows {
			if row.Excluded {
				continue
			}
			if strings.Contains(row.Scene, "dark") {
				summary.AchievedDark++
			}
			if strings.Contains(row.Scene, "motion") {
				summary.AchievedMotion++
			}
		}
		plan.SceneMix = &summary
		if summary.AchievedDark < summary.RequestedDark || summary.AchievedMotion < summary.RequestedMotion {
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
			if prior != nil && prior.Comparisons != nil {
				for _, source := range plan.Sources {
					if source.ID != cell.SourceID {
						continue
					}
					for _, old := range prior.Comparisons.Sources {
						if old.ID == source.ID && (old.Overlay != source.Overlay || source.Overlay && old.Label != source.Label ||
							!slices.Equal(old.AllowedFrameTypes, source.AllowedFrameTypes)) {
							changed = true
						}
					}
				}
			}
			if !knownImage || changed {
				cell.ArtifactID = ""
				cell.ContentSHA256 = ""
			}
			cell.FrameNumber = nil
			if changed {
				cell.ObservedPTS = nil
				cell.TimeBaseNumerator = 0
				cell.TimeBaseDenominator = 0
				cell.PictureType = ""
				cell.Confidence = nil
				continue
			}
			if knownImage {
				cell.ContentSHA256 = previous.ContentSHA256
			}
			cell.ObservedPTS = previous.ObservedPTS
			cell.TimeBaseNumerator = previous.TimeBaseNumerator
			cell.TimeBaseDenominator = previous.TimeBaseDenominator
			cell.PictureType = previous.PictureType
			cell.Confidence = previous.Confidence
		}
	}
}

// MediaArtifactPath exposes a verified retained path only to the owning local
// CLI adapter; browser projections continue using opaque resource identities.
func (a workflowMediaPrivateArtifacts) MediaArtifactPath(snapshot api.MediaArtifactSet, id api.PublicResourceID) (string, error) {
	image, err := a.imageForArtifact(snapshot, id)
	if err != nil {
		return "", err
	}
	if snapshot.Comparisons != nil {
		if err := verifyComparisonCells(*snapshot.Comparisons, a, false); err != nil {
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
	retained.ComparisonSources = maps.Clone(old.ComparisonSources)
	mainChanged := previous.ReleaseRef.SourcePath != "" && !pathing.SamePath(previous.ReleaseRef.SourcePath, release.SourcePath)
	if b.resolver != nil {
		subject, err := b.resolver.ResolveScreenshotSubject(ctx, api.MediaPlanInput{Release: release, Purpose: api.ScreenshotPurposeFinal})
		if err != nil {
			return api.MediaArtifactSet{}, nil, fmt.Errorf("restore comparison main source: %w", err)
		}
		retained.screenshotSubject = subject
		if source, ok := old.ComparisonSources[plan.MainSourceID]; ok && !pathing.SamePath(source.Source.Path, subject.VideoPath) {
			mainChanged = true
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
		fingerprint, err := comparisonVideoFingerprint(ctx, original.Source.Path)
		if err != nil || fingerprint != original.Fingerprint {
			changed[source.ID] = true
		}
	}
	mainChanged = mainChanged || changed[plan.MainSourceID]
	if mainChanged {
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
