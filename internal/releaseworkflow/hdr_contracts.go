// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// HDRExtractionRecord is private durable source authority, independent of plot attempts.
type HDRExtractionRecord struct {
	ID                api.HDRExtractionID
	Release           api.ReleaseRef
	SourceFingerprint string
	TargetID          string
	SelectionPolicy   string
	ResolvedTrackID   uint64
	Schema            string
	Dependency        string
	Size              int64
	SHA256            string
	Absent            bool
}

// RetainedHDRExtractionResource opens private metadata bound to its retained record.
// The caller owns and must close a successfully returned reader.
type RetainedHDRExtractionResource interface {
	OpenExtraction(context.Context, HDRExtractionRecord) (io.ReadCloser, error)
}

// RetainedHDRAnalysisResource resolves verified plots under an exact analysis result.
// Callers close OpenArtifact's body and keep LocalArtifactPath private.
type RetainedHDRAnalysisResource interface {
	OpenArtifact(context.Context, api.HDRAnalysisResult, api.PublicResourceID) (MediaArtifactContent, error)
	LocalArtifactPath(api.HDRAnalysisResult, api.PublicResourceID) (string, error)
}

// HDRAnalysisBuilder produces terminal target outcomes and privately retained plots.
// Its checkpoint callback durably retains complete extraction or verified absence before
// rendering, allowing metadata reuse after a plot failure or cancellation.
type HDRAnalysisBuilder interface {
	Build(context.Context, api.ReleaseRef, api.HDRAnalysisInstructions, string, time.Time, *api.HDRAnalysisResult, RetainedHDRAnalysisResource,
		map[api.HDRExtractionID]HDRExtractionRecord, map[api.HDRExtractionID]RetainedHDRExtractionResource,
		func(HDRExtractionRecord, RetainedHDRExtractionResource) error) (api.HDRAnalysisResult, RetainedHDRAnalysisResource, error)
}

// HDRAnalysisAuthorityValidator rechecks a retained result against its prepared source generation.
type HDRAnalysisAuthorityValidator interface {
	ValidateAuthority(context.Context, api.HDRAnalysisResult) error
}

// HDRAnalysisAttemptCleaner removes interrupted files except checkpointed metadata.
type HDRAnalysisAttemptCleaner interface {
	CleanupAttempt(api.ReleaseRef, string, map[api.HDRExtractionID]HDRExtractionRecord) error
}

// CompatibleHDRExtractionRestorer copies compatible retained metadata into a new prepared generation.
// Stale or invalid entries are omitted; returned resources own newly retained copies.
type CompatibleHDRExtractionRestorer interface {
	CloneExtractions(
		context.Context,
		api.ReleaseRef,
		map[api.HDRExtractionID]HDRExtractionRecord,
		map[api.HDRExtractionID]RetainedHDRExtractionResource,
		string,
	) (map[api.HDRExtractionID]HDRExtractionRecord, map[api.HDRExtractionID]RetainedHDRExtractionResource, error)
}

// AnalyzeHDRCommand starts an explicit analysis attempt for an exact prepared generation.
type AnalyzeHDRCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Instructions     api.HDRAnalysisInstructions
	IdempotencyKey   string
}

func (AnalyzeHDRCommand) commandName() string              { return "analyze_hdr" }
func (AnalyzeHDRCommand) userIntent()                      {}
func (AnalyzeHDRCommand) operationKind() api.OperationKind { return api.OperationKindHDRAnalysis }
func (c AnalyzeHDRCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	instructions, err := c.Instructions.Normalize()
	if err != nil {
		return "", fmt.Errorf("normalize HDR command: %w", err)
	}
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Instructions     api.HDRAnalysisInstructions
	}{c.ExpectedRevision, instructions})
}

// SetHDRAnalysisEnabledCommand changes description inclusion, validating retained plots when enabling it.
type SetHDRAnalysisEnabledCommand struct {
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Enabled          bool
	IdempotencyKey   string
}

func (SetHDRAnalysisEnabledCommand) commandName() string { return "set_hdr_analysis_enabled" }
func (SetHDRAnalysisEnabledCommand) userIntent()         {}
func (SetHDRAnalysisEnabledCommand) operationKind() api.OperationKind {
	return api.OperationKindUnknown
}
func (c SetHDRAnalysisEnabledCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	return canonicalCommandFingerprint(struct {
		ExpectedRevision api.WorkflowRevision
		Enabled          bool
	}{c.ExpectedRevision, c.Enabled})
}
