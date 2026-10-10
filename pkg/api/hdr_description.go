// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"strings"
)

func (a *ExactMediaAssets) validateHDRAssets() error {
	if a.HDRAnalysis == nil && (len(a.HDRPlots) > 0 || len(a.HDRUploads) > 0 || len(a.HDRUploadHosts) > 0) {
		return errors.New("exact HDR assets require analysis authority")
	}
	if a.HDRAnalysis != nil && (a.HDRAnalysis.ID == "" || a.HDRAnalysis.Revision == 0) {
		return errors.New("exact HDR analysis reference is invalid")
	}
	paths := make(map[string]struct{}, len(a.HDRPlots))
	targets := make(map[string]bool, len(a.HDRPlots))
	for _, plot := range a.HDRPlots {
		if !ValidHDRTargetID(plot.TargetID) || targets[plot.TargetID] || plot.Image.Purpose != ScreenshotPurposeHDRAnalysis ||
			strings.TrimSpace(plot.Image.Path) == "" ||
			plot.Image.Width != HDRAnalysisWidth ||
			plot.Image.Height != HDRAnalysisHeight {
			return errors.New("exact HDR plot has invalid target, purpose, path or dimensions")
		}
		targets[plot.TargetID] = true
		paths[plot.Image.Path] = struct{}{}
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
