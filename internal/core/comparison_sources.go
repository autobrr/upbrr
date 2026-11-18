// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func comparisonSelectionFailure(code api.OperationFailureCode, reason, message string) error {
	recovery := api.OperationRecoveryEditInput
	if code == api.OperationFailureMissingPrerequisite {
		recovery = api.OperationRecoveryCompletePrerequisite
	}
	if code == api.OperationFailureStaleResult {
		recovery = api.OperationRecoveryRefreshRelease
	}
	return api.NewOperationError(api.OperationFailure{
		Code:             code,
		Operation:        api.OperationKindMedia,
		Recovery:         recovery,
		ComparisonReason: reason,
		Message:          message,
	}, filesystem.ErrComparisonIndex)
}

func (b workflowMediaBuilder) ApplyComparisonFrames(
	ctx context.Context,
	snapshot api.MediaArtifactSet,
	private releaseworkflow.RetainedMediaResource,
	selections []api.ComparisonFrameSelection,
	contents []releaseworkflow.MediaPreviewContent,
) (api.MediaArtifactSet, releaseworkflow.RetainedMediaResource, error) {
	retained, ok := private.(workflowMediaPrivateArtifacts)
	if !ok || snapshot.Comparisons == nil || len(selections) != len(contents) {
		return snapshot, private, errors.New("comparison frame selection context is unavailable")
	}
	plan, err := snapshot.Comparisons.Clone()
	if err != nil {
		return snapshot, private, fmt.Errorf("comparison source: %w", err)
	}
	retained.ComparisonSources = cloneComparisonSources(retained.ComparisonSources)
	for i, selection := range selections {
		content := contents[i]
		source, ok := retained.ComparisonSources[selection.SourceID]
		var witness screenshots.ComparisonFrameReplay
		witnessErr := json.Unmarshal(content.ComparisonWitness, &witness)
		if !ok || witnessErr != nil || content.ObservedIdentity == nil || witness.Identity != *content.ObservedIdentity ||
			witness.Identity.InputFingerprint != source.Fingerprint {
			return snapshot, private, comparisonSelectionFailure(
				api.OperationFailureStaleResult,
				"stale_preview",
				"The source changed. Inspect this frame again.",
			)
		}
		row := slices.IndexFunc(plan.Rows, func(row api.ComparisonRow) bool { return row.ID == selection.RowID })
		if row < 0 {
			return snapshot, private, errors.New("choose an existing comparison row")
		}
		column := slices.IndexFunc(plan.Rows[row].Cells, func(cell api.ComparisonCell) bool { return cell.SourceID == selection.SourceID })
		if column < 0 {
			return snapshot, private, errors.New("choose an existing comparison column")
		}
		key, err := api.CanonicalWorkflowFingerprint(witness.Identity)
		if err != nil {
			return snapshot, private, fmt.Errorf("comparison source: %w", err)
		}
		previousWitness := source.Witnesses[string(key)]
		pictureChanged := previousWitness.PictureChecksum != "" && previousWitness.PictureChecksum != witness.PictureChecksum
		if pictureChanged {
			for other, active := range plan.Rows {
				if other != row && slices.ContainsFunc(active.Cells, func(cell api.ComparisonCell) bool {
					return cell.SourceID == selection.SourceID && cell.ObservedIdentity != nil && *cell.ObservedIdentity == witness.Identity
				}) {
					return snapshot, private, comparisonWitnessConflict()
				}
			}
		}
		if source.Witnesses == nil {
			source.Witnesses = map[string]screenshots.ComparisonFrameReplay{}
		}
		source.Witnesses[string(key)] = cloneComparisonWitness(witness)
		retained.ComparisonSources[selection.SourceID] = source
		var guard *api.ComparisonSceneEvidence
		if selection.SourceID == plan.MainSourceID {
			inspected, err := b.inspectManualMainScene(ctx, plan, retained, content.TimestampSeconds, witness.PreferredDecoder(), nil, 0)
			if err != nil {
				return snapshot, private, err
			}
			if inspected.Identity == nil || *inspected.Identity != witness.Identity ||
				witness.PictureChecksum != "" && witness.PictureChecksum != inspected.PictureChecksum {
				return snapshot, private, comparisonSelectionFailure(
					api.OperationFailureStaleResult,
					"changed_main_picture",
					"Inspect the main picture again before selecting it.",
				)
			}
			guard = inspected.Evidence
		}
		previous := plan.Rows[row].Cells[column]
		if selection.SourceID == plan.MainSourceID &&
			(pictureChanged || previous.TimestampSeconds == nil || *previous.TimestampSeconds != content.TimestampSeconds ||
				previous.ObservedIdentity == nil || *previous.ObservedIdentity != witness.Identity) {
			plan.Rows[row].Scene = ""
			plan.Rows[row].Evidence = guard
			plan.SceneMix = nil
			for c := range plan.Rows[row].Cells {
				plan.Rows[row].Cells[c].Confidence = nil
			}
		}
		plan.Rows[row].Cells[column] = api.ComparisonCell{
			SourceID:            selection.SourceID,
			TimestampSeconds:    new(content.TimestampSeconds),
			ObservedIdentity:    new(witness.Identity),
			ObservedPTS:         new(witness.Identity.PTS),
			TimeBaseNumerator:   witness.Identity.TimeBaseNumerator,
			TimeBaseDenominator: witness.Identity.TimeBaseDenominator,
			PictureType:         content.PictureType,
		}
	}
	if err := verifyComparisonVideos(ctx, plan, retained); err != nil {
		return snapshot, private, err
	}
	plan.ApprovedFingerprint = ""
	plan.Problems = comparisons.Validate(plan)
	plan.Fingerprint, err = comparisons.ReviewFingerprint(plan)
	if err != nil {
		return snapshot, private, fmt.Errorf("comparison source: %w", err)
	}
	snapshot.Comparisons = &plan
	syncComparisonSelections(&snapshot, plan)
	return snapshot, retained, nil
}

// DiscoverReleaseWorkflowComparisonSource exposes the synchronized query through
// the same owner boundary as comparison edits and previews.
func (c *Core) DiscoverReleaseWorkflowComparisonSource(
	ctx context.Context,
	owner string,
	request api.DiscoverReleaseWorkflowComparisonSourceRequest,
) (api.ComparisonSourceDiscovery, error) {
	result, err := c.workflow.DiscoverComparisonSource(ctx, owner, request)
	if err != nil {
		return result, classifyOperationError(api.OperationKindMedia, err)
	}
	return result, nil
}

func comparisonWitnessConflict() error {
	return comparisonSelectionFailure(
		api.OperationFailureStaleResult,
		"conflicting_frame_witness",
		"The decoded picture changed. Rematch the conflicting comparison row.",
	)
}

// Save each selected frame from the window that authorized its row. Another
// row's overlapping decode can observe the same timestamp with different pixels.
func retainComparisonRowSamples(observations, windows map[string][]screenshots.ComparisonSample, row comparisons.MatchedRow) {
	for _, cell := range row.Frames {
		if !cell.Resolved {
			continue
		}
		for _, sample := range windows[cell.SourceID] {
			if comparisonObservationID(sample.ComparisonFrameObservation) == cell.FrameID {
				observations[cell.SourceID] = append(observations[cell.SourceID], sample)
				break
			}
		}
	}
}

func comparisonObservationID(observation screenshots.ComparisonFrameObservation) string {
	if observation.FrameID != "" {
		return observation.FrameID
	}
	return fmt.Sprintf("legacy:%d:%d/%d", observation.PTS, observation.TimeBaseNumerator, observation.TimeBaseDenominator)
}

func comparisonSourceCaptionVersion(source workflowComparisonSource) int {
	if source.Source.Input != nil {
		return screenshots.ComparisonDiscCaptionVersion
	}
	return screenshots.ComparisonCaptionVersion
}

func retainMatchedComparisonWitnesses(plan api.ComparisonPlan, observations map[string][]screenshots.ComparisonSample, retained workflowMediaPrivateArtifacts) {
	for id, source := range retained.ComparisonSources {
		chosen := map[string]bool{}
		for _, row := range plan.Rows {
			for _, cell := range row.Cells {
				if cell.SourceID == id && cell.ObservedIdentity != nil {
					key, _ := api.CanonicalWorkflowFingerprint(*cell.ObservedIdentity)
					chosen[string(key)] = true
				}
			}
		}
		witnesses := map[string]screenshots.ComparisonFrameReplay{}
		for key, witness := range source.Witnesses {
			if chosen[key] {
				witnesses[key] = witness
			}
		}
		for _, observation := range observations[id] {
			if observation.Replay != nil && chosen[observation.FrameID] {
				witnesses[observation.FrameID] = cloneComparisonWitness(*observation.Replay)
			}
		}
		source.Witnesses = witnesses
		retained.ComparisonSources[id] = source
	}
}

func comparisonDiscovery(ctx context.Context, path string) (api.ComparisonSourceDiscovery, filesystem.ComparisonDiscovery, error) {
	before, err := preparedrelease.VerifyInputSource(ctx, api.PrepareInput{SourcePath: path})
	if err != nil {
		return api.ComparisonSourceDiscovery{}, filesystem.ComparisonDiscovery{}, fmt.Errorf("comparison source: %w", err)
	}
	discovery, err := filesystem.DiscoverComparisonProgrammes(ctx, path)
	if err != nil {
		return api.ComparisonSourceDiscovery{}, discovery, fmt.Errorf("comparison source: %w", err)
	}
	after, err := preparedrelease.VerifyInputSource(ctx, api.PrepareInput{SourcePath: path})
	if err != nil || before.Identity.Digest != after.Identity.Digest {
		return api.ComparisonSourceDiscovery{}, discovery, api.ErrActiveInputChanged
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Version          int
		Source, Metadata string
	}{1, before.Identity.Digest, discovery.MetadataFingerprint})
	if err != nil {
		return api.ComparisonSourceDiscovery{}, discovery, fmt.Errorf("comparison source: %w", err)
	}
	return api.ComparisonSourceDiscovery{Fingerprint: string(fingerprint), Candidates: discovery.Candidates}, discovery, nil
}

func mainComparisonPath(subject api.ScreenshotSubject) string {
	if subject.DiscType != "" || len(subject.Discs) > 0 {
		return subject.SourcePath
	}
	return subject.VideoPath
}

func constrainMainComparisonDiscovery(discovery *api.ComparisonSourceDiscovery, private filesystem.ComparisonDiscovery, subject api.ScreenshotSubject) {
	discovery.Candidates = slices.DeleteFunc(discovery.Candidates, func(candidate api.ComparisonProgrammeCandidate) bool {
		input := private.Inputs[candidate.ID]
		playlists := subject.SelectedBDMVPlaylists
		if len(subject.Discs) > 0 {
			found := false
			for _, disc := range subject.Discs {
				if disc.ID == candidate.DiscID && pathing.SamePath(disc.Root, input.Root) {
					playlists = disc.SelectedBDMVPlaylists
					found = true
					break
				}
			}
			if !found {
				return true
			}
		}
		if input.Kind != "bluray" {
			return false
		}
		return !slices.ContainsFunc(playlists, func(playlist api.PlaylistInfo) bool { return strings.EqualFold(playlist.File, input.Playlist) })
	})
}

func (b workflowMediaBuilder) DiscoverComparisonSource(
	ctx context.Context,
	release api.ReleaseRef,
	sourceID, path string,
	main bool,
) (api.ComparisonSourceDiscovery, error) {
	if b.resolver == nil {
		return api.ComparisonSourceDiscovery{}, errors.New("comparison prepared-source resolver is unavailable")
	}
	subject, err := b.resolver.ResolveScreenshotSubject(ctx, api.MediaPlanInput{Release: release, Purpose: api.ScreenshotPurposeFinal})
	if err != nil {
		return api.ComparisonSourceDiscovery{}, fmt.Errorf("comparison source: %w", err)
	}
	if main {
		owned := mainComparisonPath(subject)
		if path != "" && !pathing.SamePath(path, owned) {
			return api.ComparisonSourceDiscovery{}, comparisonSelectionFailure(
				api.OperationFailureInvalidSource,
				"main_source",
				"The main comparison must use the prepared release.",
			)
		}
		path = owned
	}
	discovery, private, err := comparisonDiscovery(ctx, path)
	if err != nil {
		return discovery, err
	}
	discovery.SourceID = sourceID
	if main {
		constrainMainComparisonDiscovery(&discovery, private, subject)
	}
	return discovery, nil
}

func resolveComparisonVideo(
	ctx context.Context,
	path string,
	selection *api.ComparisonProgrammeSelection,
	subject *api.ScreenshotSubject,
) (string, *filesystem.ResolvedComparisonInput, *api.ComparisonProgrammeCandidate, error) {
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && !strings.EqualFold(filepath.Ext(path), ".iso") {
		if selection != nil {
			return "", nil, nil, comparisonSelectionFailure(
				api.OperationFailureInvalidSource,
				"unexpected_programme",
				"A regular video does not accept a disc programme selection.",
			)
		}
		fingerprint, err := comparisonVideoFingerprint(ctx, path)
		return fingerprint, nil, nil, err
	}
	discovery, private, err := comparisonDiscovery(ctx, path)
	if err != nil {
		return "", nil, nil, err
	}
	if len(private.Inputs) == 0 {
		if selection != nil {
			return "", nil, nil, comparisonSelectionFailure(
				api.OperationFailureInvalidSource,
				"unexpected_programme",
				"A regular video does not accept a disc programme selection.",
			)
		}
		fingerprint, err := comparisonVideoFingerprint(ctx, path)
		return fingerprint, nil, nil, err
	}
	if subject != nil {
		constrainMainComparisonDiscovery(&discovery, private, *subject)
	}
	if selection == nil {
		return "", nil, nil, comparisonSelectionFailure(
			api.OperationFailureMissingPrerequisite,
			"programme_required",
			"Discover and select one title or playlist for this comparison column.",
		)
	}
	if selection.DiscoveryFingerprint != discovery.Fingerprint {
		return "", nil, nil, comparisonSelectionFailure(
			api.OperationFailureStaleResult,
			"stale_discovery",
			"The comparison source changed. Discover its programmes again.",
		)
	}
	index := slices.IndexFunc(discovery.Candidates, func(candidate api.ComparisonProgrammeCandidate) bool {
		return candidate.ID == selection.CandidateID && candidate.DiscID == selection.DiscID && candidate.Kind == selection.Kind
	})
	if index < 0 {
		return "", nil, nil, comparisonSelectionFailure(
			api.OperationFailureInvalidSource,
			"invalid_programme",
			"Select a programme authorized for this comparison source.",
		)
	}
	candidate := discovery.Candidates[index]
	if !candidate.Supported {
		return "", nil, nil, comparisonSelectionFailure(
			api.OperationFailureInvalidSource,
			candidate.Reason,
			"The selected programme is outside the supported comparison capabilities.",
		)
	}
	input, err := filesystem.ResolveComparisonProgramme(ctx, private.Inputs[candidate.ID])
	if err != nil {
		return "", nil, nil, fmt.Errorf("comparison source: %w", err)
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Discovery string
		Input     filesystem.ResolvedComparisonInput
	}{discovery.Fingerprint, input})
	return string(fingerprint), &input, &candidate, err
}

func comparisonScratchDir(root, releasePath, id string) (string, error) {
	owner, _, err := paths.ReleaseTempDirFor(root, releasePath, api.ReleaseInfo{})
	if err != nil {
		return "", fmt.Errorf("comparison source: %w", err)
	}
	// IDs are hashed before becoming managed directory names.
	fingerprint, err := api.CanonicalWorkflowFingerprint(id)
	if err != nil {
		return "", fmt.Errorf("comparison source: %w", err)
	}
	return filepath.Join(owner, "comparisons", string(fingerprint)), nil
}

func cloneComparisonSources(sources map[string]workflowComparisonSource) map[string]workflowComparisonSource {
	cloned := maps.Clone(sources)
	for id, source := range cloned {
		source.Calibration = source.Calibration.clone()
		source.Source.HDRFacts.Formats = slices.Clone(source.Source.HDRFacts.Formats)
		source.Source.HDRFacts.FallbackFormats = slices.Clone(source.Source.HDRFacts.FallbackFormats)
		source.Source.HDRFacts.SourceFields = slices.Clone(source.Source.HDRFacts.SourceFields)
		source.Source.HDRFacts.Contradictions = slices.Clone(source.Source.HDRFacts.Contradictions)
		if source.Selection != nil {
			source.Selection = new(*source.Selection)
		}
		if source.Source.Input != nil {
			input := *source.Source.Input
			input.Occurrences = slices.Clone(input.Occurrences)
			for i := range input.Occurrences {
				input.Occurrences[i].CPI = slices.Clone(input.Occurrences[i].CPI)
			}
			if input.DVD != nil {
				title := *input.DVD
				title.TimeMap = slices.Clone(title.TimeMap)
				title.VOBU = slices.Clone(title.VOBU)
				title.Chain.ProgramMap = slices.Clone(title.Chain.ProgramMap)
				title.Chain.CellPlayback = slices.Clone(title.Chain.CellPlayback)
				title.Chain.CellPositions = slices.Clone(title.Chain.CellPositions)
				title.Chain.PreCommands = slices.Clone(title.Chain.PreCommands)
				title.Chain.PostCommands = slices.Clone(title.Chain.PostCommands)
				title.Chain.CellCommands = slices.Clone(title.Chain.CellCommands)
				input.DVD = &title
			}
			source.Source.Input = &input
		}
		source.Witnesses = maps.Clone(source.Witnesses)
		for key, witness := range source.Witnesses {
			source.Witnesses[key] = cloneComparisonWitness(witness)
		}
		cloned[id] = source
	}
	return cloned
}

func cloneComparisonWitness(witness screenshots.ComparisonFrameReplay) screenshots.ComparisonFrameReplay {
	witness.Window.Spans = slices.Clone(witness.Window.Spans)
	return witness
}
