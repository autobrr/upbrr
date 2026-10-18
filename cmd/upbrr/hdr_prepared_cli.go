// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func standalonePreparedHDR(ctx context.Context, session *hdranalysis.Session, opts cliOptions, source, fingerprint, directory string,
	formats api.HDRAnalysisFormats, peak api.HDRPeakSource, options api.HDRDecodedOptions,
) (bool, error) {
	bindCtx, bindCancel := context.WithTimeout(ctx, 15*time.Second)
	bound, err := releaseworkflow.VerifyHDRComparisonSource(bindCtx, source, verifyStandaloneHDRInput)
	bindCancel()
	if err != nil {
		return false, fmt.Errorf("bind standalone HDR input: %w", err)
	}
	if bound.SourceVersion != fingerprint {
		return false, api.ErrActiveInputChanged
	}
	source = bound.CanonicalPath
	facts, err := hdranalysis.InspectFileSource(ctx, source)
	if err != nil {
		return false, fmt.Errorf("prepare standalone HDR: %w", err)
	}
	input := hdranalysis.DecodedInput{Path: source, Fingerprint: fingerprint}
	if facts.Decoded != nil {
		input.Source = *facts.Decoded
	}
	target := api.HDRAnalysisTargetResult{TargetID: api.HDRTargetID(fingerprint, "")}
	renderFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Profile, Renderer, Title string
		Peak                     api.HDRPeakSource
	}{
		api.HDRAnalysisProfileV3, "hdr-plots-v3", "HDR analysis", peak})
	if err != nil {
		return false, fmt.Errorf("prepare standalone HDR: %w", err)
	}
	complete := false
	checkpointed := false
	var evidence *hdranalysis.NativeEvidence
	var plan *hdranalysis.BoundaryPlan
	var reports []*hdranalysis.MeasurementReport
	var renderErr error
	publish := func(report *hdranalysis.MeasurementReport) error {
		name := string(report.Result.Mode)
		if report.AudioAuthority != nil {
			name += "-track-" + strconv.Itoa(report.AudioAuthority.Ordinal)
		}
		digest := sha256.New()
		if err := hdranalysis.PublishFile(ctx, filepath.Join(directory, name+".json"), func(out io.Writer) error {
			return hdranalysis.WriteMeasurement(ctx, io.MultiWriter(out, digest), report)
		}); err != nil {
			return fmt.Errorf("prepare standalone HDR: %w", err)
		}
		complete = true
		measurement := hex.EncodeToString(digest.Sum(nil))
		if report.AudioAuthority != nil {
			var err error
			measurement, err = hdranalysis.AudioMeasurementIdentity(report)
			if err != nil {
				return fmt.Errorf("prepare standalone HDR: %w", err)
			}
		}
		result := report.Result
		result.Artifacts = nil
		for _, artifact := range hdranalysis.MeasurementArtifacts(report) {
			artifact.ID = api.PublicResourceID(name + "-" + string(artifact.Kind))
			artifact.MeasurementFingerprint, artifact.RenderFingerprint = measurement, string(renderFingerprint)
			if err := hdranalysis.PublishFile(ctx, filepath.Join(directory, artifact.Filename()), func(out io.Writer) error {
				return session.RenderMeasurementArtifact(ctx, report, artifact.Kind, "HDR analysis", out)
			}); err != nil {
				result.Reason = "render_unavailable"
				continue
			}
			target.Artifacts = append(target.Artifacts, artifact)
			result.Artifacts = append(result.Artifacts, artifact.ID)
		}
		if report.AudioAuthority != nil {
			hdranalysis.AppendAudioOutcome(&target, report, result)
		} else {
			target.Decoded = append(target.Decoded, result)
			reports = append(reports, report)
		}
		return nil
	}
	checkpoint := func(native *hdranalysis.NativeExtraction, pictures []*hdranalysis.MeasurementReport) error {
		defer native.Release()
		var err error
		evidence, err = native.Evidence(formats, facts)
		if err != nil {
			return fmt.Errorf("prepare standalone HDR: %w", err)
		}
		target.Formats = evidence.Results
		nativeDirectory := filepath.Join(directory, "native")
		if err := os.Mkdir(nativeDirectory, 0o700); err != nil {
			return fmt.Errorf("prepare standalone HDR: %w", err)
		}
		_, retained, err := hdranalysis.PublishNativeEvidence(ctx, nativeDirectory, hdranalysis.ExtractionIdentity{
			SourceFingerprint: fingerprint,
			TargetID:          target.TargetID,
			SelectionPolicy:   "unique_hevc",
			ResolvedTrackID:   native.TrackID,
		}, formats, evidence)
		if err != nil {
			return fmt.Errorf("prepare standalone HDR: %w", err)
		}
		complete = true
		for index := range target.Formats {
			format := &target.Formats[index]
			if format.State != api.HDRExtractionComplete || format.Format == api.HDRNativeDolbyVision && format.L1Samples == 0 {
				continue
			}
			kind := api.HDRArtifactHDR10PlusPlot
			if format.Format == api.HDRNativeDolbyVision {
				kind = api.HDRArtifactDolbyVisionL1Plot
			}
			width, height := kind.ImageDimensions()
			artifact := api.HDRTypedArtifact{
				ID:                     api.PublicResourceID(kind),
				Kind:                   kind,
				Format:                 format.Format,
				Profile:                api.HDRAnalysisProfileV3,
				MIME:                   "image/png",
				Width:                  width,
				Height:                 height,
				MeasurementFingerprint: retained.SHA256,
				RenderFingerprint:      string(renderFingerprint),
			}
			if err := session.RenderNative(ctx, evidence, format.Format, peak, "HDR metadata", filepath.Join(directory, artifact.Filename()), 0); err != nil {
				renderErr = errors.Join(renderErr, err)
				check := api.HDRCheckResult{
					Code:    "plot_unavailable",
					State:   api.HDRCheckUnsupported,
					Message: "The plot could not be rendered; retained metadata remains available for retry.",
				}
				if errors.Is(err, context.Canceled) {
					check.Code, check.State = "render_canceled", api.HDRCheckNotRun
				}
				if errors.Is(err, context.DeadlineExceeded) {
					check.Code, check.State = "render_interrupted", api.HDRCheckNotRun
				}
				format.Checks = append(format.Checks, check)
				continue
			}
			target.Artifacts = append(target.Artifacts, artifact)
		}
		if options.BoundarySync {
			input.Native = evidence
			plan, _ = hdranalysis.PlanBoundary(input, options.ActiveArea)
		}
		if err := evidence.RetainReportSummary(); err != nil {
			return fmt.Errorf("prepare standalone HDR: %w", err)
		}
		if options.Automatic || options.BoundarySync || options.Audio {
			evidence.Release()
		}
		for _, picture := range pictures {
			if err := publish(picture); err != nil {
				return fmt.Errorf("prepare standalone HDR: %w", err)
			}
		}
		checkpointed = true
		return nil
	}
	var runErr error
	if options.Requested(api.HDRDecodedAudio) {
		subject, inspectErr := audioanalysis.NewService(nil).InspectFileSubject(ctx, source, fingerprint)
		capture := hdranalysis.PreparedAudioCapture{
			Subject:          subject,
			CheckpointNative: checkpoint,
			Publish:          publish,
		}
		if inspectErr == nil {
			for _, track := range subject.Tracks {
				if options.AllAudioTracks || track.ID == subject.PrimaryTrackID {
					capture.Selected = append(capture.Selected, track.ID)
				}
			}
		}
		if inspectErr != nil || len(capture.Selected) > 64 {
			reason := "prepared_audio_scope_unavailable"
			state := api.HDRDecodedUnsupported
			var checks []api.HDRCheckResult
			if len(capture.Selected) > 64 {
				reason = "audio_track_limit"
				checks = []api.HDRCheckResult{{
					Code:    reason,
					State:   api.HDRCheckUnsupported,
					Message: fmt.Sprintf("The request selected %d audio tracks; the supported maximum is 64. No subset was analyzed.", len(capture.Selected)),
				}}
			} else if failure, ok := api.AsAudioAnalysisFailure(inspectErr); ok && failure.Code == api.AudioAnalysisFailureNoAudio {
				state, reason = api.HDRDecodedNotRun, "no_audio"
			}
			target.Decoded = append(target.Decoded, api.HDRDecodedResult{
				Mode:     api.HDRDecodedAudio,
				State:    state,
				Profile:  api.AudioAnalysisProfileVersion,
				Coverage: api.HDRCoverageUnknown,
				Reason:   reason,
				Checks:   checks,
			})
			capture.Subject = api.AudioAnalysisSubject{VideoPath: source, SourceFingerprint: fingerprint}
			capture.Selected = nil
		}
		native, _, err := standaloneAutomaticHDR(ctx, session, opts, source, fingerprint, directory, formats, &capture)
		if native != nil {
			native.Release()
		}
		runErr = err
		hdranalysis.CompletePreparedAudio(&capture, &target, ctx.Err() != nil)
	} else {
		native, err := session.ExtractNativeFile(ctx, source, formats)
		if err == nil {
			err = checkpoint(native, nil)
		}
		if native != nil {
			native.Release()
		}
		runErr = err
	}
	if errors.Is(runErr, api.ErrActiveInputChanged) {
		return false, runErr
	}
	runErr = errors.Join(runErr, renderErr)
	if evidence == nil {
		return complete, runErr
	}
	defer evidence.Release()
	if !checkpointed {
		return complete, runErr
	}
	if options.BoundarySync {
		if err := evidence.FinishSharedCapture(0); err != nil {
			return complete, fmt.Errorf("prepare standalone HDR: %w", err)
		}
		runErr = errors.Join(runErr, session.MeasureBoundary(ctx, input, plan, options, directory, 0, publish))
	} else if !options.Automatic && !options.Audio {
		input.Native = evidence
		// The existing comparison resolver owns source-B authority and geometry.
		measured, err := standaloneHDRMeasurements(ctx, session, opts, source, directory, fingerprint, evidence, &target)
		reports, runErr = append(reports, measured...), errors.Join(runErr, err)
	}
	verifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	verified, err := releaseworkflow.VerifyHDRComparisonSource(verifyCtx, source, verifyStandaloneHDRInput)
	if err != nil || verified.SourceVersion != fingerprint {
		return false, api.ErrActiveInputChanged
	}
	modes := api.HDRDecodedModes()
	slices.SortStableFunc(target.Decoded, func(a, b api.HDRDecodedResult) int { return slices.Index(modes, a.Mode) - slices.Index(modes, b.Mode) })
	target.Status = target.AnalysisStatus()
	if err := hdranalysis.PublishFile(verifyCtx, filepath.Join(directory, "summary.json"), func(out io.Writer) error {
		return evidence.WriteReport(verifyCtx, out, peak, "", target, reports)
	}); err != nil {
		return complete, fmt.Errorf("publish prepared HDR summary: %w", err)
	}
	if runErr == nil && target.Status != api.StageStatusCompleted {
		runErr = api.NewHDRAnalysisError(api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureIncomplete,
			Message: "Requested HDR evidence is incomplete; usable outputs were retained."}, nil)
	}
	if runErr != nil {
		return true, fmt.Errorf("analyze prepared HDR: %w", runErr)
	}
	return true, nil
}
