// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"strings"
	"time"
)

// HDRComparisonSourceRef identifies private source B without exposing its path.
// Its handle is valid only for Release and before ExpiresAt.
type HDRComparisonSourceRef struct {
	ID          string     `json:"id"`
	Fingerprint string     `json:"fingerprint"`
	Release     ReleaseRef `json:"release"`
	ExpiresAt   time.Time  `json:"expiresAt"`
}

// SelectReleaseWorkflowHDRComparisonRequest selects a server-local file for source B.
// Selection belongs to the current prepared release and does not replace the primary input.
type SelectReleaseWorkflowHDRComparisonRequest struct {
	ReleaseWorkflowCommandContext
	Release    ReleaseRef `json:"release"`
	SourcePath string     `json:"sourcePath"`
}

// Validate checks command authority fields and path bounds; source inspection is deferred to the workflow.
func (r SelectReleaseWorkflowHDRComparisonRequest) Validate() error {
	if err := r.ReleaseWorkflowCommandContext.Validate(); err != nil {
		return err
	}
	if r.Release.SourcePath == "" || r.Release.Generation == 0 {
		return errors.New("HDR comparison requires an exact prepared release")
	}
	if strings.TrimSpace(r.SourcePath) == "" || len(r.SourcePath) > 32768 {
		return errors.New("HDR comparison requires an explicit bounded source path")
	}
	return nil
}

// HDRComparisonSelection is explicit source-selection intent for an initial upload.
// It is replaced by a private generation-bound handle after preparation.
type HDRComparisonSelection struct {
	Path string `json:"path"`
}

// Validate requires unresolved comparison options: the request's SourceID must still be empty.
func (s HDRComparisonSelection) Validate(request *HDRAnalysisRequest) error {
	if strings.TrimSpace(s.Path) == "" || len(s.Path) > 32768 || request == nil || request.Decoded == nil ||
		request.Decoded.Comparison == nil || request.Decoded.Comparison.SourceID != "" {
		return errors.New("comparison selection requires a path and pending comparison options without a source ID")
	}
	_, err := request.normalize(true)
	return err
}
