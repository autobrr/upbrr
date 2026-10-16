// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/hex"
	"errors"
	"strings"
)

func (a *ExactMediaAssets) validateHDRAssets() error {
	if a.HDRAnalysis == nil && (len(a.HDRPlots) > 0 || len(a.HDRStats) > 0 || len(a.HDRUploads) > 0 || len(a.HDRUploadHosts) > 0) {
		return errors.New("exact HDR assets require analysis authority")
	}
	if a.HDRAnalysis != nil && (a.HDRAnalysis.ID == "" || a.HDRAnalysis.Revision == 0) {
		return errors.New("exact HDR analysis reference is invalid")
	}
	paths := make(map[string]struct{}, len(a.HDRPlots))
	targets := make(map[string]bool, len(a.HDRPlots))
	artifacts := make(map[PublicResourceID]bool, len(a.HDRPlots))
	for _, plot := range a.HDRPlots {
		kind := plot.Kind
		if plot.ArtifactID == "" {
			if plot.Format != "" || kind != "" {
				return errors.New("exact HDR image identity is incomplete")
			}
			kind = HDRArtifactHDR10PlusPlot
		} else {
			format := HDRNativeFormat("")
			//nolint:exhaustive // Only native-format artifacts carry a format discriminator.
			switch kind {
			case HDRArtifactHDR10PlusPlot, HDRArtifactBoundaryHDR10PlusSceneSheet:
				format = HDRNativeHDR10Plus
			case HDRArtifactDolbyVisionL1Plot, HDRArtifactBoundaryDVSceneSheet:
				format = HDRNativeDolbyVision
			}
			if artifacts[plot.ArtifactID] || plot.Format != format || format == "" && kind.DecodedMode() == "" {
				return errors.New("exact HDR image format or identity is invalid")
			}
			artifacts[plot.ArtifactID] = true
		}
		if err := validateHDRDescriptionIdentity(
			kind.DecodedMode() == HDRDecodedAudio,
			plot.AudioTrackID,
			plot.MeasurementFingerprint,
			plot.RenderFingerprint,
		); err != nil {
			return err
		}
		identity := plot.TargetID + "\x00" + string(kind) + "\x00" + string(plot.Format) + "\x00" + plot.AudioTrackID
		width, height := kind.ImageDimensions()
		if kind == HDRArtifactAudioWaveform || kind == HDRArtifactAudioSpectrogram {
			width, height = plot.Image.Width, plot.Image.Height
			if width < 1 || width > 8192 || height < 1 || height > 8192 {
				return errors.New("exact audio image dimensions are invalid")
			}
		}
		if !ValidHDRTargetID(plot.TargetID) || targets[identity] || plot.Image.Purpose != ScreenshotPurposeHDRAnalysis ||
			strings.TrimSpace(plot.Image.Path) == "" ||
			plot.Image.Width != width ||
			plot.Image.Height != height {
			return errors.New("exact HDR plot has invalid target, purpose, path or dimensions")
		}
		targets[identity] = true
		paths[plot.Image.Path] = struct{}{}
	}
	statsTargets := make(map[string]bool, len(a.HDRStats))
	for _, stats := range a.HDRStats {
		identity := stats.TargetID + "\x00" + stats.AudioTrackID
		if err := validateHDRDescriptionIdentity(true, stats.AudioTrackID, stats.MeasurementFingerprint, stats.RenderFingerprint); err != nil {
			return err
		}
		if stats.ArtifactID == "" || artifacts[stats.ArtifactID] || !ValidHDRTargetID(stats.TargetID) || statsTargets[identity] ||
			strings.TrimSpace(stats.Text) == "" || len(stats.Text) > AudioAnalysisStatsMaxBytes {
			return errors.New("exact HDR audio statistics have invalid identity or text")
		}
		artifacts[stats.ArtifactID] = true
		statsTargets[identity] = true
	}
	for _, upload := range a.HDRUploads {
		if upload.Purpose != ScreenshotPurposeHDRAnalysis {
			return errors.New("exact HDR upload has invalid purpose")
		}
	}
	for tracker, host := range a.HDRUploadHosts {
		if strings.TrimSpace(tracker) == "" || strings.TrimSpace(host) == "" {
			return errors.New("exact HDR upload host mapping is invalid")
		}
	}
	return validateExactMediaUploads("HDR analysis", a.HDRUploads, paths)
}

func validateHDRDescriptionIdentity(audio bool, track, measurement, render string) error {
	if track == "" && measurement == "" && render == "" {
		return nil // Legacy exact assets retain their original singular authority.
	}
	if audio && strings.TrimSpace(track) == "" || !audio && track != "" || len(track) > 128 || len(measurement) != 64 || len(render) != 64 {
		return errors.New("exact HDR artifact lacks complete track or rendering authority")
	}
	for _, fingerprint := range []string{measurement, render} {
		if _, err := hex.DecodeString(fingerprint); err != nil {
			return errors.New("exact HDR artifact fingerprint is invalid")
		}
	}
	return nil
}
