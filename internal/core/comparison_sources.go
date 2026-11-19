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

// comparisonNativeTiming describes the display interval and picture policy
// attached to one observed address. Neither can be borrowed from another read.
type comparisonNativeTiming struct {
	relative                              float64
	pts, duration, numerator, denominator int64
	pictureType                           string
}

func comparisonTimingOf(o screenshots.ComparisonFrameObservation) comparisonNativeTiming {
	return comparisonNativeTiming{o.RelativeTimestampSeconds, o.PTS, o.DurationTicks, o.TimeBaseNumerator, o.TimeBaseDenominator, o.PictureType}
}

type comparisonKnownPicture struct {
	timing comparisonNativeTiming
	replay *screenshots.ComparisonFrameReplay
}

func comparisonRecord(o screenshots.ComparisonFrameObservation) comparisonKnownPicture {
	return comparisonKnownPicture{timing: comparisonTimingOf(o), replay: o.Replay}
}
func (record comparisonKnownPicture) matches(o screenshots.ComparisonFrameObservation) bool {
	if record.timing != comparisonTimingOf(o) {
		return false
	}
	if record.replay == o.Replay {
		return true
	}
	if !record.replay.SamePicture(o.Replay) {
		return false
	}
	x, y := record.replay.File, o.Replay.File
	return x == nil || y == nil ||
		((x.Next == nil || y.Next == nil || *x.Next == *y.Next) && (x.Previous == nil || y.Previous == nil || *x.Previous == *y.Previous))
}

// An explicit correspondence binds two physical pictures inspected together.
type comparisonAppliedPair struct {
	Main, Secondary screenshots.ComparisonFrameReplay
}

// A public ID has one private address per source. Replacing one row's explicit
// selection must not redirect an image, preview or retry in another row.
func (b workflowMediaBuilder) storeComparisonWitness(
	ctx context.Context,
	plan api.ComparisonPlan,
	row int,
	id string,
	source *workflowComparisonSource,
	replay *screenshots.ComparisonFrameReplay,
) (bool, error) {
	key, err := api.CanonicalWorkflowFingerprint(replay.Identity)
	if err != nil {
		return false, fmt.Errorf("identify comparison picture: %w", err)
	}
	address := string(key)
	previous := source.Witnesses[address]
	same := previous.SamePicture(replay)
	if !same {
		for i, other := range plan.Rows {
			if i != row && slices.ContainsFunc(other.Cells, func(cell api.ComparisonCell) bool {
				return cell.SourceID == id && cell.ObservedIdentity != nil && *cell.ObservedIdentity == replay.Identity
			}) {
				if previous.CanReinspectDisc() {
					renderer, err := b.comparisonService()
					if err != nil {
						return false, err
					}
					verified, err := renderer.ReinspectComparisonReplay(ctx, screenshots.ComparisonSampleRequest{Source: source.Source}, &previous)
					if err != nil {
						return false, fmt.Errorf("reinspect saved comparison span: %w", err)
					}
					same = verified.SamePicture(replay)
				}
				if !same {
					return false, comparisonWitnessConflict()
				}
				break
			}
		}
	}
	if source.Witnesses == nil {
		source.Witnesses = map[string]screenshots.ComparisonFrameReplay{}
	}
	source.Witnesses[address] = cloneComparisonWitness(*replay)
	return same, nil
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
	retained.ComparisonSources, err = verifiedComparisonVideos(ctx, plan, retained)
	if err != nil {
		return snapshot, private, err
	}
	defer clearComparisonFileIdentities(retained.ComparisonSources)
	if err := b.refreshComparisonDisplays(ctx, retained.ComparisonSources); err != nil {
		return snapshot, private, err
	}
	applied := map[string]map[string]screenshots.ComparisonFrameReplay{}
	for i, selection := range selections {
		content := contents[i]
		source, ok := retained.ComparisonSources[selection.SourceID]
		var witness screenshots.ComparisonFrameReplay
		witnessErr := json.Unmarshal(content.ComparisonWitness, &witness)
		if !ok || witnessErr != nil || witness.Version != 2 || witness.MissingBoundary() || content.ObservedIdentity == nil ||
			witness.Identity != *content.ObservedIdentity ||
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
		same, err := b.storeComparisonWitness(ctx, plan, row, selection.SourceID, &source, &witness)
		if err != nil {
			return snapshot, private, err
		}
		if applied[selection.RowID] == nil {
			applied[selection.RowID] = map[string]screenshots.ComparisonFrameReplay{}
		}
		address := witness
		address.Window.Spans = nil
		applied[selection.RowID][selection.SourceID] = address
		if pair, ok := source.AppliedPairs[selection.RowID]; ok && !pair.Secondary.SamePicture(&address) {
			delete(source.AppliedPairs, selection.RowID)
		}
		retained.ComparisonSources[selection.SourceID] = source
		previous := plan.Rows[row].Cells[column]
		unchanged := same && previous.ObservedIdentity != nil && *previous.ObservedIdentity == witness.Identity &&
			previous.TimestampSeconds != nil && *previous.TimestampSeconds == content.TimestampSeconds
		if selection.SourceID == plan.MainSourceID {
			if err := comparisonMainTimeAllowed(plan, retained, content.TimestampSeconds); err != nil {
				return snapshot, private, err
			}
			priorEvidence := plan.Rows[row].Evidence
			if !unchanged || priorEvidence == nil || !priorEvidence.Eligible || priorEvidence.Profile != comparisons.SceneProfile {
				inspected, _, err := b.inspectManualMainScene(ctx, plan, retained, content.TimestampSeconds, screenshots.ComparisonSampleRequest{})
				if err != nil {
					return snapshot, private, err
				}
				if inspected.Identity == nil || *inspected.Identity != witness.Identity ||
					!witness.SamePicture(inspected.Replay) {
					return snapshot, private, comparisonSelectionFailure(
						api.OperationFailureStaleResult,
						"changed_main_picture",
						"Inspect the main picture again before selecting it.",
					)
				}
				plan.Rows[row].Evidence = inspected.Evidence
				plan.Rows[row].Scene = ""
				plan.SceneMix = nil
			}
		}
		if selection.SourceID == plan.MainSourceID && !unchanged {
			for _, other := range retained.ComparisonSources {
				if pair, ok := other.AppliedPairs[selection.RowID]; ok && !pair.Main.SamePicture(&witness) {
					delete(other.AppliedPairs, selection.RowID)
				}
			}
			for c := range plan.Rows[row].Cells {
				plan.Rows[row].Cells[c].Confidence = nil
			}
		}
		selected := api.ComparisonCell{
			SourceID:            selection.SourceID,
			TimestampSeconds:    new(content.TimestampSeconds),
			ObservedIdentity:    new(witness.Identity),
			ObservedPTS:         new(witness.Identity.PTS),
			TimeBaseNumerator:   witness.Identity.TimeBaseNumerator,
			TimeBaseDenominator: witness.Identity.TimeBaseDenominator,
			PictureType:         content.PictureType,
		}
		// Configure already verified these bytes against their private image.
		// Apply reuses them only for the same qualified picture and rendering.
		if unchanged && previous.ArtifactID != "" && previous.ContentSHA256 != "" && previous.PictureType == content.PictureType {
			rendering, err := comparisonRenderingFingerprint(plan, retained, selection.SourceID, b.config.ScreenshotHandling)
			if err != nil {
				return snapshot, private, err
			}
			image, found := retained.ArtifactImages[previous.ArtifactID]
			if found && image.Purpose == api.ScreenshotPurposeComparison && image.ContentSHA256 == previous.ContentSHA256 &&
				comparisonImageRenderingCurrent(retained, selection.SourceID, previous.ArtifactID, rendering) {
				selected.ArtifactID, selected.ContentSHA256, selected.FrameNumber = previous.ArtifactID, previous.ContentSHA256, previous.FrameNumber
				selected.Warning = comparisonCaptureTypeWarning(plan.Sources, selected)
			}
		}
		plan.Rows[row].Cells[column] = selected
	}
	for row, frames := range applied {
		main, ok := frames[plan.MainSourceID]
		if !ok {
			continue
		}
		for id, secondary := range frames {
			if id == plan.MainSourceID {
				continue
			}
			source := retained.ComparisonSources[id]
			if source.AppliedPairs == nil {
				source.AppliedPairs = map[string]comparisonAppliedPair{}
			}
			source.AppliedPairs[row] = comparisonAppliedPair{Main: main, Secondary: secondary}
			retained.ComparisonSources[id] = source
		}
	}
	plan.ApprovedFingerprint = ""
	plan.Problems = comparisons.Validate(plan)
	plan.Fingerprint, err = comparisons.ReviewFingerprint(plan)
	if err != nil {
		return snapshot, private, fmt.Errorf("comparison source: %w", err)
	}
	if err := verifyComparisonVideos(ctx, plan, retained); err != nil {
		return snapshot, private, err
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

func retainMatchedComparisonWitnesses(
	plan api.ComparisonPlan,
	observations map[string][]screenshots.ComparisonSample,
	retained workflowMediaPrivateArtifacts,
) error {
	for id, source := range retained.ComparisonSources {
		chosen := map[string]bool{}
		var applied map[string]comparisonAppliedPair
		for _, row := range plan.Rows {
			for _, cell := range row.Cells {
				if cell.SourceID == id && cell.ObservedIdentity != nil {
					key, _ := api.CanonicalWorkflowFingerprint(*cell.ObservedIdentity)
					chosen[string(key)] = true
					pair, bound := source.AppliedPairs[row.ID]
					if bound && cell.Confidence == nil && cell.TimestampSeconds != nil && *cell.ObservedIdentity == pair.Secondary.Identity &&
						slices.ContainsFunc(row.Cells, func(main api.ComparisonCell) bool {
							return main.SourceID == plan.MainSourceID && main.Confidence == nil && main.TimestampSeconds != nil &&
								main.ObservedIdentity != nil &&
								*main.ObservedIdentity == pair.Main.Identity
						}) {
						if applied == nil {
							applied = map[string]comparisonAppliedPair{}
						}
						applied[row.ID] = pair
					}
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
				if prior, ok := witnesses[observation.FrameID]; ok && prior.Version == 2 {
					if !observation.Replay.SamePicture(&prior) {
						return comparisonWitnessConflict()
					}
					// A historical disc inventory can select a picture whose
					// original span was already verified during capture.
					if observation.Replay.Version != 2 {
						continue
					}
				}
				witnesses[observation.FrameID] = cloneComparisonWitness(*observation.Replay)
			}
		}
		source.AppliedPairs = applied
		source.Witnesses = witnesses
		retained.ComparisonSources[id] = source
	}
	return nil
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

type resolvedComparisonVideo struct {
	fingerprint string
	input       *filesystem.ResolvedComparisonInput
	candidate   *api.ComparisonProgrammeCandidate
	file        os.FileInfo
}

func resolveComparisonVideo(
	ctx context.Context,
	path string,
	selection *api.ComparisonProgrammeSelection,
	subject *api.ScreenshotSubject,
) (resolvedComparisonVideo, error) {
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && !strings.EqualFold(filepath.Ext(path), ".iso") {
		return resolveComparisonFileVideo(ctx, path, selection)
	}
	discovery, private, err := comparisonDiscovery(ctx, path)
	if err != nil {
		return resolvedComparisonVideo{}, err
	}
	if len(private.Inputs) == 0 {
		return resolveComparisonFileVideo(ctx, path, selection)
	}
	if subject != nil {
		constrainMainComparisonDiscovery(&discovery, private, *subject)
	}
	if selection == nil {
		return resolvedComparisonVideo{}, comparisonSelectionFailure(
			api.OperationFailureMissingPrerequisite,
			"programme_required",
			"Discover and select one title or playlist for this comparison column.",
		)
	}
	if selection.DiscoveryFingerprint != discovery.Fingerprint {
		return resolvedComparisonVideo{}, comparisonSelectionFailure(
			api.OperationFailureStaleResult,
			"stale_discovery",
			"The comparison source changed. Discover its programmes again.",
		)
	}
	index := slices.IndexFunc(discovery.Candidates, func(candidate api.ComparisonProgrammeCandidate) bool {
		return candidate.ID == selection.CandidateID && candidate.DiscID == selection.DiscID && candidate.Kind == selection.Kind
	})
	if index < 0 {
		return resolvedComparisonVideo{}, comparisonSelectionFailure(
			api.OperationFailureInvalidSource,
			"invalid_programme",
			"Select a programme authorized for this comparison source.",
		)
	}
	candidate := discovery.Candidates[index]
	if !candidate.Supported {
		return resolvedComparisonVideo{}, comparisonSelectionFailure(
			api.OperationFailureInvalidSource,
			candidate.Reason,
			"The selected programme is outside the supported comparison capabilities.",
		)
	}
	input, err := filesystem.ResolveComparisonProgramme(ctx, private.Inputs[candidate.ID])
	if err != nil {
		return resolvedComparisonVideo{}, fmt.Errorf("comparison source: %w", err)
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Discovery string
		Input     filesystem.ResolvedComparisonInput
	}{discovery.Fingerprint, input})
	if err != nil {
		return resolvedComparisonVideo{}, fmt.Errorf("fingerprint comparison programme: %w", err)
	}
	return resolvedComparisonVideo{
		fingerprint: string(fingerprint),
		input:       &input,
		candidate:   &candidate,
	}, nil
}

func resolveComparisonFileVideo(ctx context.Context, path string, selection *api.ComparisonProgrammeSelection) (resolvedComparisonVideo, error) {
	if selection != nil {
		return resolvedComparisonVideo{}, comparisonSelectionFailure(
			api.OperationFailureInvalidSource,
			"unexpected_programme",
			"A regular video does not accept a disc programme selection.",
		)
	}
	fingerprint, file, err := preparedrelease.VerifyInputFileSource(ctx, path)
	if err != nil {
		return resolvedComparisonVideo{}, fmt.Errorf("verify comparison source: %w", err)
	}
	return resolvedComparisonVideo{fingerprint: fingerprint, file: file}, nil
}

func (b workflowMediaBuilder) refreshComparisonDisplays(ctx context.Context, sources map[string]workflowComparisonSource) error {
	for id, source := range sources {
		if err := b.refreshComparisonDisplay(ctx, &source.Source); err != nil {
			return err
		}
		sources[id] = source
	}
	return nil
}

func (b workflowMediaBuilder) refreshComparisonDisplay(ctx context.Context, source *screenshots.ComparisonSource) error {
	if !comparisonSizingProbeNeeded(*source) {
		return nil
	}
	renderer, err := b.comparisonService()
	if err != nil {
		return err
	}
	inspected, err := renderer.InspectComparisonDisplay(ctx, *source)
	if err != nil {
		return fmt.Errorf("refresh comparison display geometry: %w", err)
	}
	if comparisonSizingProbeNeeded(inspected) {
		return errors.New("the comparison source has no inspected display dimensions")
	}
	*source = inspected
	return nil
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
		source.AppliedPairs = maps.Clone(source.AppliedPairs)
		for row, pair := range source.AppliedPairs {
			source.AppliedPairs[row] = comparisonAppliedPair{Main: cloneComparisonWitness(pair.Main), Secondary: cloneComparisonWitness(pair.Secondary)}
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
	if witness.File != nil {
		window := *witness.File
		if window.PacketPosition != nil {
			window.PacketPosition = new(*window.PacketPosition)
		}
		if window.Previous != nil {
			window.Previous = new(*window.Previous)
		}
		if window.Next != nil {
			window.Next = new(*window.Next)
		}
		witness.File = &window
	}
	return witness
}
