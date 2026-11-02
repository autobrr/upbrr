// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type renderedFrame struct {
	Processing api.ScreenshotProcessing
	Timestamp  float64
	Payload    []byte
}

// softwareTonemappingCompatible never treats profile 5's IPT base layer as PQ.
// Other Dolby Vision inputs need positive evidence of an HDR-compatible layer.
func softwareTonemappingCompatible(facts api.HDRFacts) bool {
	profile, _, _ := strings.Cut(facts.DolbyVisionProfile, ".")
	if profile == "5" {
		return false
	}
	if profile == "" && !slices.Contains(facts.Formats, api.HDRFormatDolbyVision) {
		return true
	}
	for _, format := range []api.HDRFormat{api.HDRFormatHDR10, api.HDRFormatHDR10Plus, api.HDRFormatHLG, api.HDRFormatPQ10} {
		if slices.Contains(facts.Formats, format) || slices.Contains(facts.FallbackFormats, format) {
			return true
		}
	}
	return facts.DolbyVisionProfile == "8.1"
}

func renderFrame(ctx context.Context, runner Runner, executable string, req captureRequest, logger api.Logger) (renderedFrame, error) {
	frame := renderedFrame{Timestamp: req.Timestamp}
	if strings.TrimSpace(req.InputPath) == "" || req.Timestamp < 0 {
		return frame, errors.New("screenshots: valid input path and timestamp required")
	}
	requested := "none"
	modes := []string{"none"}
	if req.ToneMap {
		requested = "software"
		modes = []string{"software", "none"}
		if req.UseLibplacebo {
			requested = "libplacebo"
			modes = []string{"libplacebo", "software", "none"}
		}
	}
	frame.Processing.RequestedRenderer = requested
	var lastErr error
	for _, mode := range modes {
		if err := ctx.Err(); err != nil {
			return frame, fmt.Errorf("screenshots: rendering canceled: %w", err)
		}
		if mode == "software" && !softwareTonemappingCompatible(req.HDRFacts) {
			frame.Processing.Steps = append(frame.Processing.Steps, api.ScreenshotRenderStep{
				Renderer: mode,
				Stage:    "compatibility",
				Reason:   "skipped: Dolby Vision base layer is not known to support software tonemapping",
			})
			continue
		}
		attempt := req
		attempt.ToneMap = mode != "none"
		attempt.UseLibplacebo = mode == "libplacebo"
		for retry := range 2 {
			payload, result, err := renderAttempt(ctx, runner, executable, attempt)
			if err == nil {
				frame.Payload = payload
				frame.Processing.Renderer = mode
				frame.Processing.Tonemapped = attempt.ToneMap
				frame.Processing.Degraded = req.ToneMap && !attempt.ToneMap
				if len(frame.Processing.Steps) > 0 {
					frame.Processing.Warning = processingWarning(frame.Processing)
					screenshotLogger(logger).Warnf("screenshots: timestamp_seconds=%.3f %s", req.Timestamp, frame.Processing.Warning)
				} else {
					screenshotLogger(logger).Infof("screenshots: timestamp_seconds=%.3f renderer=%s tonemapped=%t",
						req.Timestamp, mode, attempt.ToneMap)
				}
				return frame, nil
			}
			lastErr = err
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
				errors.Is(err, internalerrors.ErrFrameCorruption) {
				return frame, err
			}
			stage := renderFailureStage(result, err)
			frame.Processing.Steps = append(frame.Processing.Steps, api.ScreenshotRenderStep{
				Renderer: mode,
				Stage:    stage,
				Reason:   "failed",
			})
			if mode != "libplacebo" || retry != 0 || stage != "device" {
				break
			}
		}
	}
	return frame, fmt.Errorf("screenshots: all applicable renderers failed: %w", lastErr)
}

func processingWarning(outcome api.ScreenshotProcessing) string {
	stages := make([]string, 0, len(outcome.Steps))
	for _, step := range outcome.Steps {
		stages = append(stages, step.Renderer+" "+step.Stage+" "+step.Reason)
	}
	warning := fmt.Sprintf("Requested %s; %s. Using %s; tonemapping applied: %t.",
		outcome.RequestedRenderer, strings.Join(stages, "; "), outcome.Renderer, outcome.Tonemapped)
	if outcome.Degraded {
		warning += " Non-tonemapped output; colours may be inaccurate."
	}
	return warning
}

func renderFailureStage(result CommandResult, err error) string {
	if errors.Is(err, errFFmpegDOVIMetadata) {
		return "dolby_vision_metadata"
	}
	if errors.Is(err, errFFmpegNoImage) || errors.Is(err, errFFmpegBlackImage) || result.ExitCode == 0 {
		return "image_validation"
	}
	message := strings.ToLower(string(result.Stderr))
	for _, indicator := range []string{"no such filter", "option not found", "error applying option", "unsupported", "not supported", "incompatible_driver"} {
		if strings.Contains(message, indicator) {
			return "capability"
		}
	}
	for _, indicator := range []string{"device lost", "device creation failed", "failed to create vulkan", "instance creation failure", "out of device memory"} {
		if strings.Contains(message, indicator) {
			return "device"
		}
	}
	return "rendering"
}

// renderAttempt validates the same renderer without text before adding overlays.
// It never retries or changes frame identity, and removes every rejected output.
func renderAttempt(ctx context.Context, runner Runner, executable string, req captureRequest) ([]byte, CommandResult, error) {
	profile, _, _ := strings.Cut(req.HDRFacts.DolbyVisionProfile, ".")
	needsDOVIMetadata := req.UseLibplacebo && profile == "5"
	req.InspectFrame = req.InspectFrame || needsDOVIMetadata
	if req.FrameOverlay {
		validation := req
		if req.OutputPath != "" {
			file, err := os.CreateTemp(filepath.Dir(req.OutputPath), ".upbrr-overlay-check-*.png")
			if err != nil {
				return nil, CommandResult{}, fmt.Errorf("screenshots: create overlay check: %w", err)
			}
			name := file.Name()
			defer func() { _ = os.Remove(name) }()
			if err := file.Close(); err != nil {
				return nil, CommandResult{}, fmt.Errorf("screenshots: close overlay check: %w", err)
			}
			validation.OutputPath = name
		}
		validation.FrameOverlay = false
		validation.InspectFrame = true
		_, result, err := renderAttempt(ctx, runner, executable, validation)
		if err != nil {
			return nil, result, err
		}
		if observed := parseShowInfo(string(result.Stderr)); observed.FrameType != "" {
			req.FrameInfo = observed
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, CommandResult{}, fmt.Errorf("screenshots: rendering canceled: %w", err)
	}
	if req.OutputPath != "" {
		if err := os.Remove(req.OutputPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, CommandResult{}, fmt.Errorf("screenshots: clear prior capture: %w", err)
		}
	}
	result, err := runner.Run(ctx, executable, buildFFmpegArgs(req, req.UseLibplacebo), "")
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if corruption := ffmpegFrameCorruptionError(result.Stderr); corruption != nil {
		err = corruption
	} else if err == nil && result.ExitCode == 0 {
		switch {
		case needsDOVIMetadata && !showInfoDOVIMetadata.Match(result.Stderr):
			err = errFFmpegDOVIMetadata
		case req.OutputPath == "":
			err = validateImagePayload(result.Stdout)
		default:
			err = validateCaptureOutput(req.OutputPath)
		}
	} else if err == nil {
		err = errors.New("screenshots: FFmpeg rendering failed")
	}
	if err != nil {
		if req.OutputPath != "" {
			_ = os.Remove(req.OutputPath)
		}
		return nil, result, err
	}
	return result.Stdout, result, nil
}
