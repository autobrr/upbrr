// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// applyDiscTaxonomy adds optional disc metadata before site-specific payload
// handling. Unknown names are omitted; site validation may still require them.
func applyDiscTaxonomy(req trackers.PreparationInput, data map[string]string, profile SiteProfile) {
	if !isDiscType(req.Meta.DiscType) {
		return
	}
	set := func(key, value string, resolve func(string) string) {
		id := resolve(value)
		if id != "" {
			data[key] = id
		}
		if req.Logger != nil && strings.TrimSpace(value) != "" {
			decision := "mapped"
			if id == "" {
				decision = "omitted_unknown"
			}
			req.Logger.Debugf("trackers: disc taxonomy tracker=%s field=%s decision=%s id=%s", req.Tracker, key, decision, id)
		}
	}
	set("region_id", req.Meta.Region, profile.RegionID)
	set("distributor_id", req.Meta.Distributor, profile.DistributorID)
}

func applySubmissionRequirements(req trackers.PreparationInput, data map[string]string, profile SiteProfile) error {
	if profile.SubmissionRequirements != nil {
		requirements, err := profile.SubmissionRequirements(req)
		if err != nil {
			return fmt.Errorf("site submission requirements: %w", err)
		}
		if requirements.ForceModeration {
			data["mod_queue_opt_in"] = "1"
		}
		if appendix := strings.TrimSpace(
			requirements.DescriptionAppendix,
		); appendix != "" &&
			!strings.HasSuffix(strings.TrimSpace(data["description"]), appendix) {
			data["description"] = strings.TrimSpace(data["description"] + "\n\n" + appendix)
		}
	}
	if profile.ValidateFinalDescription != nil && api.NormalizeWorkflowExecutionMode(req.ExecutionMode) != api.WorkflowExecutionModeDebug {
		if err := profile.ValidateFinalDescription(req.Meta, data["description"], data["mediainfo"], data["bdinfo"]); err != nil {
			return fmt.Errorf("final description requirements: %w", err)
		}
	}
	return nil
}
