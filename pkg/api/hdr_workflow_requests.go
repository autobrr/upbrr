// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

type AnalyzeReleaseWorkflowHDRRequest struct {
	ReleaseWorkflowCommandContext
	Instructions HDRAnalysisInstructions `json:"instructions"`
}

func (r AnalyzeReleaseWorkflowHDRRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	_, err := r.Instructions.Normalize()
	return err
}

type SetReleaseWorkflowHDRAnalysisEnabledRequest struct {
	ReleaseWorkflowCommandContext
	Enabled bool `json:"enabled"`
}

func (r SetReleaseWorkflowHDRAnalysisEnabledRequest) Validate() error {
	return r.ReleaseWorkflowCommandContext.Validate()
}
