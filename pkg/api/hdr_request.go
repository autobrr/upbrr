// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "errors"

// HDRAnalysisRequest requests complete HDR analysis before hosting and descriptions.
// An omitted target set is resolved only when one prepared target exists.
type HDRAnalysisRequest struct {
	TargetIDs  []string      `json:"targetIds,omitempty"`
	PeakSource HDRPeakSource `json:"peakSource,omitempty"`
}

// Normalize validates target IDs, copies the selection and defaults the estimator.
// An empty target selection remains deferred to prepared-target resolution.
func (r HDRAnalysisRequest) Normalize() (HDRAnalysisRequest, error) {
	peak, err := r.PeakSource.Normalize()
	if err != nil {
		return HDRAnalysisRequest{}, err
	}
	if len(r.TargetIDs) > HDRAnalysisMaxTargets {
		return HDRAnalysisRequest{}, errors.New("too many HDR targets")
	}
	seen := make(map[string]bool, len(r.TargetIDs))
	for _, id := range r.TargetIDs {
		if !ValidHDRTargetID(id) || seen[id] {
			return HDRAnalysisRequest{}, errors.New("HDR target IDs must be valid and unique")
		}
		seen[id] = true
	}
	r.PeakSource, r.TargetIDs = peak, append([]string(nil), r.TargetIDs...)
	return r, nil
}
