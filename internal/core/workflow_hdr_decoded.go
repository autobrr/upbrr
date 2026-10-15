// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

var hdrDecodedModes = api.HDRDecodedModes()

func hdrModeOptions(options api.HDRDecodedOptions, mode api.HDRDecodedMode) api.HDRDecodedOptions {
	result := api.HDRDecodedOptions{}
	switch mode {
	case api.HDRDecodedBoundarySync:
		result.BoundarySync = true
	case api.HDRDecodedAutomatic:
		result.Automatic, result.Sampled, result.Comparison = true, options.Sampled, options.Comparison
	case api.HDRDecodedAudio:
		result.Automatic = true
	case api.HDRDecodedSceneSync:
		result.SceneSync = true
	case api.HDRDecodedActiveArea:
		result.ActiveArea = true
		result.BoundarySync = options.BoundarySync
	case api.HDRDecodedGrade, api.HDRDecodedColour:
		comparison := *options.Comparison
		comparison.Grade, comparison.Colour = mode == api.HDRDecodedGrade, mode == api.HDRDecodedColour
		result.Comparison = &comparison
	}
	return result
}

func hdrMeasurementIdentity(native releaseworkflow.HDRExtractionRecord, options api.HDRDecodedOptions, mode api.HDRDecodedMode) (string, error) {
	profile := hdranalysis.SceneMeasurementProfile
	if mode == api.HDRDecodedBoundarySync {
		profile = hdranalysis.BoundaryMeasurementProfile
	}
	if mode == api.HDRDecodedAutomatic {
		profile = hdranalysis.AutomaticMeasurementProfile
		if options.Sampled {
			profile = hdranalysis.SampledMeasurementProfile
		}
	}
	if mode == api.HDRDecodedAudio {
		profile = api.AudioAnalysisProfileVersion
	}
	if mode == api.HDRDecodedActiveArea {
		profile = hdranalysis.AreaMeasurementProfile
		if options.BoundarySync {
			profile = hdranalysis.BoundaryAreaProfile
		}
	}
	if mode == api.HDRDecodedGrade || mode == api.HDRDecodedColour {
		profile = hdranalysis.GradeMeasurementProfile
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		NativeID     api.HDRExtractionID
		NativeSHA256 string
		Options      api.HDRDecodedOptions
		Dependency   string
		Mode         api.HDRDecodedMode
		Profile      string
	}{native.ID, native.SHA256, hdrModeOptions(options, mode), hdranalysis.NativeDependencyFingerprint, mode, profile})
	if err != nil {
		return "", fmt.Errorf("fingerprint HDR measurement: %w", err)
	}
	return string(fingerprint), nil
}

func restoreHDRMeasurement(ctx context.Context, record releaseworkflow.HDRMeasurementRecord,
	resource releaseworkflow.RetainedHDRMeasurementResource,
) (*hdranalysis.MeasurementReport, error) {
	if resource == nil {
		return nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	file, err := resource.OpenMeasurement(ctx, record)
	if err != nil {
		return nil, fmt.Errorf("open retained HDR measurement: %w", err)
	}
	report, readErr := hdranalysis.ReadMeasurement(ctx, file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return nil, fmt.Errorf("restore HDR measurement: %w", err)
	}
	identity, err := hdrMeasurementIdentity(
		releaseworkflow.HDRExtractionRecord{ID: record.ExtractionID, SHA256: record.ExtractionSHA256},
		report.Options,
		report.Result.Mode,
	)
	if report.AudioAuthority != nil {
		identity, err = hdranalysis.AudioMeasurementIdentity(report)
		scope, scopeErr := hdrAudioScopeFingerprint(*report.AudioAuthority)
		if scopeErr != nil || record.AudioTrackID != report.AudioAuthority.TrackID || scope != record.AudioScopeFingerprint {
			return nil, releaseworkflow.ErrPrivateResourceIntegrity
		}
	} else if record.AudioTrackID != "" || record.AudioScopeFingerprint != "" {
		return nil, releaseworkflow.ErrPrivateResourceIntegrity
	}
	if err != nil || identity != record.Identity || report.Sources[0] != record.SourceFingerprint ||
		report.Result.Mode != record.Result.Mode || report.Result.State != record.Result.State ||
		report.Result.Measured != record.Result.Measured || report.Result.Selected != record.Result.Selected || report.Decoder.Fingerprint != record.Decoder {
		return nil, releaseworkflow.ErrPrivateResourceIntegrity
	}
	return report, nil
}

func (b workflowHDRAnalysisBuilder) measureHDRTarget(ctx context.Context, session *hdranalysis.Session,
	subject api.HDRAnalysisSubject, target api.HDRAnalysisTargetSubject, instructions api.HDRAnalysisInstructions,
	evidence *hdranalysis.NativeEvidence, native releaseworkflow.HDRExtractionRecord, store *releaseworkflow.HDRMeasurementStore,
	entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord,
	retained map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource,
	publishNative func(releaseworkflow.HDRExtractionRecord, releaseworkflow.RetainedHDRExtractionResource) error,
	attempt string, index int, outcome *api.HDRAnalysisTargetResult,
	fresh []*hdranalysis.MeasurementReport,
	boundaryPlan *hdranalysis.BoundaryPlan,
) ([]*hdranalysis.MeasurementReport, error) {
	if instructions.Decoded == nil {
		return nil, nil
	}
	used, err := evidence.AccountedBytes()
	reserve := int64(128 << 20)
	if (instructions.Decoded.Automatic || instructions.Decoded.Audio) && len(fresh) == 0 {
		// Bound encoded JSON, typed full-frame arrays, retained audio and renderer
		// scratch together before reading any automatic measurement checkpoint.
		reserve = 1536 << 20
	}
	if err != nil || used > hdranalysis.MaxRestoreBytes-reserve {
		return nil, errors.New("retained HDR evidence leaves insufficient room for measurement reports")
	}
	options := *instructions.Decoded
	if options.Comparison != nil {
		options.Comparison = new(*options.Comparison)
	}
	var reports []*hdranalysis.MeasurementReport
	if (options.Automatic || options.Audio) && len(fresh) > 0 {
		for _, report := range fresh {
			if report.Result.Mode != api.HDRDecodedAudio {
				report, err = hdranalysis.ProjectPictureReport(report, instructions.ProfileVersion)
				if err != nil {
					return reports, fmt.Errorf("project fresh HDR picture: %w", err)
				}
			}
			report.Options = *instructions.Decoded
			report.Tracks[0] = strconv.FormatUint(native.ResolvedTrackID, 10)
			identity, err := hdrMeasurementIdentity(native, report.Options, report.Result.Mode)
			if err != nil {
				return nil, err
			}
			if store == nil || store.Publish == nil || native.ID == "" {
				return nil, errors.New("automatic measurement lacks native checkpoint authority")
			}
			id := attempt + "-" + strconv.Itoa(index+1) + "-" + string(report.Result.Mode)
			record, resource, err := b.retainMeasurement(ctx, native, identity, id, report)
			if err != nil {
				return nil, err
			}
			if err := store.Publish(record, resource); err != nil {
				return nil, fmt.Errorf("publish automatic HDR measurement: %w", err)
			}
			reports = append(reports, report)
		}
		if !options.BoundarySync {
			return reports, nil
		}
		options.Audio = false
	}
	var decoder string
	var decoderErr error
	decoderChecked := false
	for _, mode := range hdrDecodedModes {
		if mode == api.HDRDecodedAudio && instructions.ProfileVersion == api.HDRAnalysisProfileV3 {
			continue // Prepared audio owns per-track restoration and aggregation.
		}
		if !options.Requested(mode) {
			continue
		}
		identity, err := hdrMeasurementIdentity(native, *instructions.Decoded, mode)
		if err != nil {
			return nil, err
		}
		previous := latestHDRMeasurement(store, instructions.Release, target.Target.ID, identity)
		retry := instructions.DecodedRetry != nil && slices.Contains(instructions.DecodedRetry.Modes, mode)
		if retry && store != nil && store.RetryAfterSequence != nil && previous.Sequence > *store.RetryAfterSequence {
			retry = false
		}
		if previous.ID == "" || retry {
			continue
		}
		if previous.Decoder != "" {
			if !decoderChecked {
				decoder, decoderErr = hdranalysis.CurrentDecoderFingerprint(ctx)
				decoderChecked = true
			}
			if decoderErr == nil && decoder != previous.Decoder {
				continue
			}
		}
		report, err := restoreHDRMeasurement(ctx, previous, store.Resources[previous.ID])
		if decoderErr != nil && previous.Decoder != "" {
			err = decoderErr
		}
		if err == nil &&
			(mode == api.HDRDecodedGrade || mode == api.HDRDecodedColour || mode == api.HDRDecodedAutomatic && report.Automatic != nil && len(report.Automatic.Reference) > 0) &&
			(store.Comparison == nil || report.Sources[1] != store.Comparison.Ref.Fingerprint) {
			if mode == api.HDRDecodedAutomatic {
				report.OmitAutomaticReference()
			} else {
				err = releaseworkflow.ErrPrivateResourceIntegrity
			}
		}
		if err == nil && mode != api.HDRDecodedAudio {
			report, err = hdranalysis.ProjectPictureReport(report, instructions.ProfileVersion)
		}
		if err != nil {
			outcome.Decoded = append(outcome.Decoded, api.HDRDecodedResult{
				Mode:    mode,
				State:   api.HDRDecodedFailed,
				Reason:  "resource_unavailable",
				Profile: previous.Result.Profile,
			})
		} else {
			reports = append(reports, report)
		}
		switch mode {
		case api.HDRDecodedBoundarySync:
			options.BoundarySync = false
		case api.HDRDecodedAutomatic, api.HDRDecodedAudio:
			// Both automatic modes are restored independently; avoid clearing
			// their shared selection before checking the sibling checkpoint.
		case api.HDRDecodedSceneSync:
			options.SceneSync = false
		case api.HDRDecodedActiveArea:
			options.ActiveArea = false
		case api.HDRDecodedGrade:
			options.Comparison.Grade = false
		case api.HDRDecodedColour:
			options.Comparison.Colour = false
		}
	}
	if (options.Automatic || options.Audio) && !instructions.Decoded.BoundarySync {
		for _, mode := range []api.HDRDecodedMode{api.HDRDecodedAutomatic, api.HDRDecodedAudio} {
			if !options.Requested(mode) {
				continue
			}
			found := slices.ContainsFunc(reports, func(report *hdranalysis.MeasurementReport) bool { return report.Result.Mode == mode }) ||
				slices.ContainsFunc(outcome.Decoded, func(result api.HDRDecodedResult) bool { return result.Mode == mode })
			if !found {
				profile := hdranalysis.AutomaticMeasurementProfile
				if options.Sampled {
					profile = hdranalysis.SampledMeasurementProfile
				}
				if mode == api.HDRDecodedAudio {
					profile = api.AudioAnalysisProfileVersion
				}
				outcome.Decoded = append(outcome.Decoded, api.HDRDecodedResult{
					Mode:    mode,
					State:   api.HDRDecodedUnsupported,
					Reason:  "automatic_measurement_unavailable",
					Profile: profile,
				})
			}
		}
		return reports, nil
	}
	if options.Comparison != nil && !options.Comparison.Grade && !options.Comparison.Colour {
		options.Comparison = nil
	}
	if !options.BoundarySync && !options.SceneSync && !options.ActiveArea && options.Comparison == nil {
		return reports, nil
	}
	if err := b.resolver.ValidateHDRAnalysisSource(ctx, subject.Release); err != nil {
		return reports, fmt.Errorf("validate decoded HDR source: %w", err)
	}
	input := hdranalysis.DecodedInput{
		Path:        target.Path,
		Fingerprint: subject.SourceFingerprint,
		Native:      evidence,
	}
	if target.Target.Evidence.Decoded != nil {
		input.Source = *target.Target.Evidence.Decoded
	}
	var comparison *hdranalysis.DecodedInput
	if options.Comparison != nil {
		var source *releaseworkflow.HDRComparisonSource
		if store != nil {
			source = store.Comparison
		}
		var err error
		comparison, err = b.hdrComparisonInput(ctx, session, source, evidence, instructions.Release, entries, retained, publishNative, attempt)
		if err != nil {
			for _, mode := range []api.HDRDecodedMode{api.HDRDecodedGrade, api.HDRDecodedColour} {
				if options.Requested(mode) {
					outcome.Decoded = append(outcome.Decoded, api.HDRDecodedResult{
						Mode:   mode,
						State:  api.HDRDecodedFailed,
						Reason: "comparison_source_unavailable",
					})
				}
			}
			options.Comparison = nil
			if !options.SceneSync && !options.ActiveArea {
				return reports, fmt.Errorf("prepare HDR comparison: %w", err)
			}
		} else {
			defer comparison.Native.Release()
		}
	}
	// Playlist decoding requires its own occurrence-to-decoder mapping. The
	// service rejects its timeline while native playlist evidence stays usable.
	publishMeasurement := func(report *hdranalysis.MeasurementReport) error {
		if instructions.Decoded.BoundarySync && report.Result.Mode == api.HDRDecodedBoundarySync && !options.BoundarySync {
			return nil
		}
		report, err = hdranalysis.ProjectPictureReport(report, instructions.ProfileVersion)
		if err != nil {
			return fmt.Errorf("project fresh HDR picture: %w", err)
		}
		report.Options = *instructions.Decoded
		if comparison != nil {
			if err := verifyHDRComparison(ctx, *store.Comparison); err != nil {
				return err
			}
		}
		if err := b.resolver.ValidateHDRAnalysisSource(ctx, subject.Release); err != nil {
			return fmt.Errorf("revalidate decoded HDR source: %w", err)
		}
		if store == nil || store.Publish == nil || native.ID == "" {
			return errors.New("decoded measurement lacks native checkpoint authority")
		}
		identity, err := hdrMeasurementIdentity(native, report.Options, report.Result.Mode)
		if err != nil {
			return err
		}
		id := attempt + "-" + strconv.Itoa(index+1) + "-" + string(report.Result.Mode)
		record, resource, err := b.retainMeasurement(ctx, native, identity, id, report)
		if err != nil {
			return err
		}
		if err := store.Publish(record, resource); err != nil {
			return fmt.Errorf("publish HDR measurement: %w", err)
		}
		reports = append(reports, report)
		return nil
	}
	if instructions.Decoded.BoundarySync {
		if boundaryPlan != nil {
			if err := boundaryPlan.SelectModes(options.BoundarySync, options.ActiveArea); err != nil {
				return reports, fmt.Errorf("prepare retained HDR modes: %w", err)
			}
		}
		directory, directoryErr := hdrAttemptRoot(b.root, instructions.Release, "hdr-boundary", attempt+"-"+strconv.Itoa(index+1))
		if directoryErr != nil {
			return reports, directoryErr
		}
		if err := createHDRDirectory(directory); err != nil {
			return reports, err
		}
		defer func() { _ = os.Remove(directory) }()
		sparseOptions := options
		sparseOptions.BoundarySync = true
		err = session.MeasureBoundary(ctx, input, boundaryPlan, sparseOptions, directory, used, publishMeasurement)
	} else {
		err = session.MeasureDecoded(ctx, input, comparison, options, 0, publishMeasurement)
	}
	if err != nil {
		return reports, fmt.Errorf("measure decoded HDR pictures: %w", err)
	}
	return reports, nil
}

func renderHDRMeasurements(ctx context.Context, session *hdranalysis.Session, title, attempt string, index int,
	reports []*hdranalysis.MeasurementReport, outcome *api.HDRAnalysisTargetResult, resource hdrManagedResource,
	native releaseworkflow.HDRExtractionRecord, renderFingerprint string,
) {
	for _, report := range reports {
		result := report.Result
		result.Artifacts = nil
		if result.Measured > 0 && (result.State == api.HDRDecodedComplete || result.State == api.HDRDecodedInconclusive) {
			for _, artifact := range hdranalysis.MeasurementArtifacts(report) {
				if artifact.Profile == api.HDRAnalysisProfileV3 {
					identity, err := hdrMeasurementIdentity(native, report.Options, report.Result.Mode)
					if report.AudioAuthority != nil {
						identity, err = hdranalysis.AudioMeasurementIdentity(report)
					}
					if err != nil {
						result.Reason = "render_identity_unavailable"
						continue
					}
					artifact.MeasurementFingerprint, artifact.RenderFingerprint = identity, renderFingerprint
				}
				artifact.ID = api.PublicResourceID("hdr-" + attempt + "-" + strconv.Itoa(index+1) + "-" + string(artifact.Kind))
				if report.AudioAuthority != nil {
					artifact.ID += api.PublicResourceID("-track-" + strconv.Itoa(report.AudioAuthority.Ordinal))
				}
				path := filepath.Join(resource.Directory, strconv.Itoa(index+1)+"-"+artifact.Filename())
				err := hdranalysis.PublishFile(ctx, path, func(out io.Writer) error {
					return session.RenderMeasurementArtifact(ctx, report, artifact.Kind, title, out)
				})
				if err == nil {
					var file hdrManagedFile
					file, err = hdrFileIntegrity(path)
					if err == nil {
						resource.Files[artifact.ID] = file
						outcome.Artifacts = append(outcome.Artifacts, artifact)
						result.Artifacts = append(result.Artifacts, artifact.ID)
					}
				}
				if err != nil {
					_ = os.Remove(path)
					result.Reason = "render_unavailable"
					if ctx.Err() != nil {
						result.State, result.Reason = api.HDRDecodedCanceled, "render_canceled"
					}
				}
			}
		}
		if report.AudioAuthority != nil {
			hdranalysis.AppendAudioOutcome(outcome, report, result)
		} else {
			outcome.Decoded = append(outcome.Decoded, result)
		}
	}
}

func latestHDRMeasurement(store *releaseworkflow.HDRMeasurementStore, release api.ReleaseRef, target, identity string) releaseworkflow.HDRMeasurementRecord {
	var previous releaseworkflow.HDRMeasurementRecord
	if store != nil {
		for _, record := range store.Records {
			if record.Release == release && record.TargetID == target && record.Identity == identity &&
				(record.Sequence > previous.Sequence || record.Sequence == previous.Sequence && record.ID > previous.ID) {
				previous = record
			}
		}
	}
	return previous
}
