// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"fmt"
	"strconv"
	"strings"

	dovi "github.com/Audionut/go-dovi-extract"

	"github.com/autobrr/upbrr/pkg/api"
)

func nativeDVProfiles(summary dovi.Summary) string {
	profiles := make([]string, len(summary.Profiles))
	for i, profile := range summary.Profiles {
		profiles[i] = strconv.FormatUint(profile.Value, 10)
	}
	if len(profiles) == 0 {
		return ""
	}
	return strings.Join(profiles, ", ") + " (coarse RPU header)"
}

func nativeDVChecks(analysis *dovi.Analysis, summary dovi.Summary) []api.HDRCheckResult {
	refresh := api.HDRCheckResult{
		Code:    "scene_refresh_markers",
		State:   api.HDRCheckInconclusive,
		Message: fmt.Sprintf("%d metadata refresh markers were observed. Picture synchronization has not been measured.", len(summary.SceneMarkers)),
	}
	if len(summary.SceneMarkers) == 0 {
		refresh.State = api.HDRCheckWarning
		refresh.Message = "No scene-refresh markers were observed. This is a review cue, not proof of incorrect synchronization."
	}
	checks := []api.HDRCheckResult{refresh}
	var first *dovi.L1
	flat := true
	for _, record := range analysis.Records {
		if record.DM == nil || record.DM.L1 == nil {
			continue
		}
		if first == nil {
			first = record.DM.L1
		} else if *first != *record.DM.L1 {
			flat = false
			break
		}
	}
	if flat && summary.Counts.L1Samples > 1 {
		checks = append(checks, api.HDRCheckResult{
			Code:    "flat_l1_metadata",
			State:   api.HDRCheckWarning,
			Message: "All observed L1 values are identical. Review the source context; flat metadata alone does not establish a faulty grade.",
		})
	}
	if len(summary.ActiveAreas) > 0 {
		checks = append(checks, api.HDRCheckResult{
			Code:    "active_area_coordinates",
			State:   api.HDRCheckInconclusive,
			Message: "The report preserves L5 intervals and gaps. Geometry and picture borders are unverified because no applicable coded-raster coordinate mapping was supplied.",
		})
	}
	return checks
}
