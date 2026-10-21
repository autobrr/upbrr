// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func validateCLIHDR(opts cliOptions, visited map[string]bool) error {
	if opts.HDRResumeWorkflow != "" {
		if opts.HDRAnalysis || opts.HDRAnalysisOnly {
			return errors.New("--hdr-resume-workflow cannot start a new HDR analysis request")
		}
		if (opts.HDRRetryAttempt == "") != (opts.HDRRetryModes == "") {
			return errors.New("decoded retry requires both --hdr-retry-attempt and --hdr-retry-modes")
		}
		if opts.HDRRetryModes != "" && opts.HDRImageIDs != "" {
			return errors.New("retry measurements before selecting their new artifact IDs")
		}
		for flag := range visited {
			switch flag {
			case "hdr-resume-workflow",
				"hdr-retry-attempt",
				"hdr-retry-modes",
				"hdr-image-ids",
				"config",
				"log-level",
				"console-log-level",
				"ui",
				"ui-keep-open",
				"unattended",
				"ua",
				"unattended_confirm",
				"uac":
			default:
				return fmt.Errorf("--%s cannot change a retained HDR resume request", flag)
			}
		}
		return nil
	}
	for _, flag := range []string{"hdr-retry-attempt", "hdr-retry-modes", "hdr-image-ids"} {
		if visited[flag] {
			return fmt.Errorf("--%s requires --hdr-resume-workflow", flag)
		}
	}
	if opts.HDRAnalysis && opts.HDRAnalysisOnly {
		return errors.New("--hdr-analysis and --hdr-analysis-only are mutually exclusive")
	}
	if visited["hdr-boundary-audio"] && !opts.HDRBoundarySync {
		return errors.New("--hdr-boundary-audio requires --hdr-boundary-sync, including when explicitly false")
	}
	for _, flag := range []string{"hdr-targets", "hdr-playlist", "hdr-peak-source", "hdr-output-dir", "hdr-formats", "hdr-metadata-only", "hdr-audio-only", "hdr-pictures", "hdr-full-frames", "hdr-scene-sync", "hdr-active-area", "hdr-compare-source", "hdr-compare-offset", "hdr-compare-crop", "hdr-grade", "hdr-colour", "hdr-boundary-sync", "hdr-boundary-audio", "hdr-all-audio-tracks", "hdr-decoder-threads"} {
		if visited[flag] && !opts.HDRAnalysis && !opts.HDRAnalysisOnly {
			return fmt.Errorf("--%s requires --hdr-analysis or --hdr-analysis-only", flag)
		}
	}
	if visited["hdr-targets"] && opts.HDRAnalysisOnly {
		return errors.New("--hdr-targets requires upload --hdr-analysis")
	}
	if (visited["hdr-playlist"] || visited["hdr-output-dir"]) && !opts.HDRAnalysisOnly {
		return errors.New("--hdr-playlist and --hdr-output-dir require --hdr-analysis-only")
	}
	if opts.HDRAnalysis || opts.HDRAnalysisOnly {
		if _, err := cliHDRDecoded(opts, ""); err != nil {
			return err
		}
		if _, err := api.HDRAnalysisFormats(opts.HDRFormats).Normalize(); err != nil {
			return fmt.Errorf("HDR formats: %w", err)
		}
		if _, err := api.HDRPeakSource(opts.HDRPeakSource).Normalize(); err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		if opts.HDRTargets != "" {
			if _, err := parseCLIHDRTargets(opts.HDRTargets); err != nil {
				return fmt.Errorf("hdr_analysis_cli: %w", err)
			}
		}
		if opts.HDRPlaylist != "" {
			if _, err := api.NormalizeHDRPlaylist(opts.HDRPlaylist); err != nil {
				return fmt.Errorf("hdr_analysis_cli: %w", err)
			}
		}
	}
	if opts.HDRAnalysisOnly {
		for flag := range visited {
			switch flag {
			case "hdr-scene-sync",
				"hdr-boundary-sync",
				"hdr-boundary-audio",
				"hdr-all-audio-tracks",
				"hdr-decoder-threads",
				"hdr-metadata-only",
				"hdr-full-frames",
				"hdr-pictures",
				"hdr-audio-only",
				"hdr-active-area",
				"hdr-compare-source",
				"hdr-compare-offset",
				"hdr-compare-crop",
				"hdr-grade",
				"hdr-colour",
				"hdr-analysis-only",
				"hdr-playlist",
				"hdr-peak-source",
				"hdr-output-dir",
				"hdr-formats",
				"ui",
				"ui-keep-open",
				"unattended",
				"ua",
				"unattended_confirm",
				"uac":
			default:
				return fmt.Errorf("--%s cannot be used with --hdr-analysis-only", flag)
			}
		}
		if strings.TrimSpace(opts.HDROutputDir) == "" {
			return errors.New("--hdr-analysis-only requires --hdr-output-dir <parent directory>")
		}
	}
	return nil
}

func parseCLIHDRTargets(raw string) ([]string, error) {
	ids := strings.Split(raw, ",")
	seen := make(map[string]bool)
	if len(ids) > api.HDRAnalysisMaxTargets {
		return nil, errors.New("too many HDR targets")
	}
	for index, id := range ids {
		id = strings.TrimSpace(id)
		if !api.ValidHDRTargetID(id) || seen[id] {
			return nil, errors.New("--hdr-targets requires unique prepared HDR target IDs in source order")
		}
		seen[id], ids[index] = true, id
	}
	return ids, nil
}

func cliHDRUploadRequest(opts cliOptions) *api.HDRAnalysisRequest {
	if !opts.HDRAnalysis {
		return nil
	}
	var ids []string
	if opts.HDRTargets != "" {
		ids, _ = parseCLIHDRTargets(opts.HDRTargets)
	}
	peak, _ := api.HDRPeakSource(opts.HDRPeakSource).Normalize()
	decoded, _ := cliHDRDecoded(opts, "")
	return &api.HDRAnalysisRequest{
		Decoded:    decoded,
		TargetIDs:  ids,
		PeakSource: peak,
		Formats:    api.HDRAnalysisFormats(opts.HDRFormats),
	}
}

// hdrOutputParent resolves existing ancestors before creating any output, including aliases.
func hdrOutputParent(source, output string, disc bool) (string, error) {
	sourceReal, err := filepath.EvalSymlinks(source)
	if err != nil {
		return "", fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	parent, err := filepath.Abs(output)
	if err != nil {
		return "", fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	ancestor := parent
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) || filepath.Dir(ancestor) == ancestor {
			return "", fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		ancestor = filepath.Dir(ancestor)
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	relative, err := filepath.Rel(ancestor, parent)
	if err != nil {
		return "", fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	resolved = filepath.Join(resolved, relative)
	if pathutil.SamePath(sourceReal, resolved) || disc && pathutil.IsWithinRoot(sourceReal, resolved) {
		return "", errors.New("HDR output must be outside the source")
	}
	if info, err := os.Stat(parent); err == nil && !info.IsDir() {
		return "", errors.New("HDR output parent must be a directory")
	}
	return resolved, nil
}

type hdrSourceStamp struct {
	Path     string
	Size     int64
	Modified int64
}

func hdrSourceIdentity(ctx context.Context, source string) (api.WorkflowFingerprint, error) {
	var stamps []hdrSourceStamp
	// #nosec G703 -- The local CLI explicitly permits inspecting user-selected disc source paths.
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("HDR disc sources cannot contain symlinks")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		if !info.Mode().IsRegular() {
			return errors.New("HDR source contains a non-regular file")
		}
		stamps = append(stamps, hdrSourceStamp{
			Path:     path,
			Size:     info.Size(),
			Modified: info.ModTime().UnixNano(),
		})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(stamps)
	if err != nil {
		return "", fmt.Errorf("fingerprint HDR source: %w", err)
	}
	return fingerprint, nil
}

func standaloneHDRPlaylist(result bd.Result, requested string) (string, error) {
	eligible := make([]string, 0)
	for _, timeline := range result.Timelines {
		if requested != "" && !strings.EqualFold(timeline.Name, requested) {
			continue
		}
		if !timeline.Complete || timeline.Err != nil || timeline.SelectedAngle != 0 || len(timeline.Items) == 0 {
			continue
		}
		seen := make(map[string]bool)
		valid := true
		for _, item := range timeline.Items {
			if item.Err != nil || item.SelectedAlternative != 0 || len(item.Angles) == 0 {
				valid = false
				break
			}
			angle := item.Angles[0]
			if angle.Err != nil || angle.Source.Kind != "m2ts" || angle.STC == nil {
				valid = false
				break
			}
			primary := 0
			for _, mapping := range angle.Video {
				if mapping.Role == video.Primary {
					if mapping.Codec != video.HEVC || mapping.EntryType != 1 {
						valid = false
					}
					primary++
				}
			}
			if primary != 1 {
				valid = false
			}
			key := fmt.Sprintf("%s:%d:%d", angle.Source.Path, item.In45, item.Out45)
			if seen[key] && requested == "" {
				valid = false
			}
			seen[key] = true
		}
		if valid {
			eligible = append(eligible, timeline.Name)
		}
	}
	if len(eligible) != 1 {
		return "", errors.New("select one supported non-looping primary HEVC playlist with --hdr-playlist")
	}
	return eligible[0], nil
}

func runHDRAnalysisOnly(ctx context.Context, opts cliOptions, paths []string, streams cliIO) error {
	if len(paths) != 1 {
		return exitError(2, errors.New("--hdr-analysis-only requires exactly one supported HDR video file or disc content root"))
	}
	source, err := filepath.Abs(paths[0])
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	// #nosec G703 -- The local CLI explicitly permits reading user-selected source paths.
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	disc := info.IsDir()
	extension := strings.ToLower(filepath.Ext(source))
	if disc {
		// #nosec G703 -- BDMV is inspected beneath the user-selected local disc source.
		if marker, err := os.Stat(filepath.Join(source, "BDMV")); err != nil || !marker.IsDir() {
			return errors.New("HDR disc input must be a content root containing BDMV")
		}
	} else if !info.Mode().IsRegular() || (extension != ".mkv" && extension != ".mp4" && extension != ".ts" && extension != ".m2ts" && extension != ".hevc" && extension != ".h265") {
		return errors.New("HDR input must be MKV, MP4, TS/M2TS, raw HEVC, or a disc content root")
	}
	if !disc && opts.HDRPlaylist != "" {
		return exitError(2, errors.New("--hdr-playlist requires a disc content root"))
	}
	parent, err := hdrOutputParent(source, opts.HDROutputDir, disc)
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	peak, err := api.HDRPeakSource(opts.HDRPeakSource).Normalize()
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	identity, err := hdrSourceIdentity(ctx, source)
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	primaryFingerprint := ""
	if !disc && (!opts.HDRMetadataOnly || opts.HDRSceneSync || opts.HDRActiveArea || opts.HDRGrade || opts.HDRColour) {
		verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		verified, verifyErr := releaseworkflow.VerifyHDRComparisonSource(verifyCtx, source, verifyStandaloneHDRInput)
		cancel()
		if verifyErr != nil {
			return fmt.Errorf("bind standalone HDR source: %w", verifyErr)
		}
		primaryFingerprint = verified.SourceVersion
	}
	playlist := ""
	if disc {
		if opts.HDRPlaylist != "" {
			playlist, err = api.NormalizeHDRPlaylist(opts.HDRPlaylist)
			if err != nil {
				return fmt.Errorf("hdr_analysis_cli: %w", err)
			}
		}
		settings := bd.DefaultSettings(parent)
		settings.PlaylistOnly = playlist
		discovered, err := hdranalysis.DiscoverDisc(ctx, bd.Options{
			Path:            source,
			Settings:        settings,
			IncludeTimeline: true,
		})
		if err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", hdranalysis.Classify(err))
		}
		playlist, err = standaloneHDRPlaylist(discovered, playlist)
		if err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	directory, err := os.MkdirTemp(parent, "hdr-analysis-")
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(directory)
		}
	}()
	ctx = hdranalysis.WithProgress(ctx, func(progress hdranalysis.Progress) {
		phase := strings.ReplaceAll(progress.Phase, "_", " ")
		if progress.Message != "" {
			fmt.Fprintf(streams.errOut, "HDR analysis: %s: %s\n", phase, progress.Message)
		} else {
			fmt.Fprintf(streams.errOut, "HDR analysis: %s\n", phase)
		}
	})
	service := hdranalysis.New(hdranalysis.ProcessAdmission(), nil)
	err = service.WithSession(ctx, func(session *hdranalysis.Session) error {
		formats, _ := api.HDRAnalysisFormats(opts.HDRFormats).Normalize()
		decoded, optionsErr := cliHDRDecoded(opts, "standalone-b")
		if optionsErr != nil {
			return optionsErr
		}
		if !disc && decoded != nil && decoded.RequiresV3() {
			var runErr error
			complete, runErr = standalonePreparedHDR(ctx, session, opts, source, primaryFingerprint, directory, formats, peak, *decoded)
			return runErr
		}
		var native *hdranalysis.NativeExtraction
		var timeline []hdranalysis.TimelineItem
		var automaticReports []*hdranalysis.MeasurementReport
		switch {
		case disc:
			settings := bd.DefaultSettings(directory)
			settings.PlaylistOnly = playlist
			settings.GenerateStreamDiagnostics = false
			outcome, scanErr := session.ScanNativeDisc(ctx, bd.Options{
				Path:            source,
				Settings:        settings,
				IncludeTimeline: true,
			}, formats, 0)
			defer outcome.Release()
			if scanErr != nil {
				return fmt.Errorf("scan HDR disc: %w", scanErr)
			}
			var takeErr error
			native, timeline, takeErr = outcome.TakePlaylist(playlist, 0)
			if takeErr != nil {
				return fmt.Errorf("assemble native HDR playlist: %w", takeErr)
			}
		default:
			var readErr error
			if decoded != nil && (decoded.Automatic || decoded.Audio) {
				native, automaticReports, readErr = standaloneAutomaticHDR(ctx, session, opts, source, primaryFingerprint, directory, formats, nil)
			} else {
				native, readErr = session.ExtractNativeFile(ctx, source, formats)
			}
			if readErr != nil {
				return fmt.Errorf("read HDR source: %w", readErr)
			}
		}
		defer native.Release()
		evidence, evidenceErr := native.Evidence(formats, api.HDRSourceEvidence{})
		if evidenceErr != nil {
			return fmt.Errorf("prepare HDR evidence: %w", evidenceErr)
		}
		defer evidence.Release()
		evidence.Timeline = timeline
		current, identityErr := hdrSourceIdentity(ctx, source)
		if identityErr != nil || current != identity {
			return errors.New("HDR source changed during extraction")
		}
		target := api.HDRAnalysisTargetResult{Formats: evidence.Results}
		var reports []*hdranalysis.MeasurementReport
		var measurementErr error
		switch {
		case disc && decoded != nil && decoded.RequiresV3():
			for _, mode := range api.HDRDecodedModes() {
				if !decoded.Requested(mode) {
					continue
				}
				profile, reason := "", "unsupported_playlist_mapping"
				switch mode { //nolint:exhaustive // Other unsupported modes have no profile.
				case api.HDRDecodedAudio:
					profile, reason = api.AudioAnalysisProfileVersion, "prepared_audio_scope_unavailable"
				case api.HDRDecodedBoundarySync:
					profile = "hdr-boundary-pq16-v1"
				}
				target.Decoded = append(target.Decoded, api.HDRDecodedResult{
					Mode:     mode,
					Profile:  profile,
					State:    api.HDRDecodedUnsupported,
					Coverage: api.HDRCoverageUnknown,
					Reason:   reason,
				})
			}
		case len(automaticReports) > 0:
			reports = automaticReports
			measurementErr = publishStandaloneAutomaticHDR(ctx, session, directory, reports, &target)
		default:
			reports, measurementErr = standaloneHDRMeasurements(ctx, session, opts, source, directory, primaryFingerprint, evidence, &target)
		}
		if errors.Is(measurementErr, api.ErrActiveInputChanged) {
			complete = false
			return measurementErr
		}
		if len(reports) > 0 {
			complete = true
		}
		for i := range target.Formats {
			format := &target.Formats[i]
			if format.State != api.HDRExtractionComplete || i == 1 && format.L1Samples == 0 {
				continue
			}
			name := "hdr10plus.png"
			title := "HDR10+ metadata"
			if i == 1 {
				name = "dolby-vision-l1.png"
				title = "Dolby Vision L1 metadata"
			}
			var measurementBytes int64
			if len(reports) > 0 {
				measurementBytes = 128 << 20
			}
			renderErr := session.RenderNative(ctx, evidence, format.Format, peak, title, filepath.Join(directory, name), measurementBytes)
			if renderErr != nil {
				code := "plot_unavailable"
				if errors.Is(renderErr, context.Canceled) {
					code = "render_canceled"
				} else if errors.Is(renderErr, context.DeadlineExceeded) {
					code = "render_interrupted"
				}
				format.Checks = append(format.Checks, api.HDRCheckResult{
					Code:    code,
					State:   api.HDRCheckUnsupported,
					Message: "The metadata plot could not be rendered.",
				})
			} else {
				complete = true
			}
		}
		// Preserve completed evidence when cancellation interrupts a later plot.
		publishCtx, cancelPublish := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancelPublish()
		current, identityErr = hdrSourceIdentity(publishCtx, source)
		if identityErr != nil || current != identity {
			complete = false
			return errors.New("HDR source changed during rendering")
		}
		target.Status = target.AnalysisStatus()
		if err := hdranalysis.PublishFile(
			publishCtx,
			filepath.Join(directory, "summary.json"),
			func(writer io.Writer) error {
				return evidence.WriteReport(publishCtx, writer, peak, playlist, target, reports)
			},
		); err != nil {
			return fmt.Errorf("publish HDR summary: %w", err)
		}
		complete = true
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("HDR rendering interrupted; completed evidence was retained: %w", err)
		}
		if measurementErr != nil {
			return fmt.Errorf("HDR picture checks incomplete; completed evidence was retained: %w", measurementErr)
		}
		if target.Status != api.StageStatusCompleted {
			return api.NewHDRAnalysisError(
				api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureIncomplete, Message: "Requested HDR evidence is incomplete; usable outputs were retained."},
				nil,
			)
		}

		return nil
	})
	if complete {
		fmt.Fprintf(streams.out, "HDR analysis output: %s\n", directory)
	}
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", hdranalysis.Classify(err))
	}
	return nil
}
