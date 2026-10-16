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
	// Sequence orders successful checkpoints; older records decode as zero.
	Sequence          uint64
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

// HDRMeasurementRecord binds one retained decoded mode to its exact native
// evidence and normalized measurement identity. Rendering is independently reusable.
type HDRMeasurementRecord struct {
	AudioTrackID          string
	AudioScopeFingerprint string
	ID                    string
	Sequence              uint64
	Release               api.ReleaseRef
	TargetID              string
	SourceFingerprint     string
	ExtractionID          api.HDRExtractionID
	ExtractionSHA256      string
	Identity              string
	Decoder               string
	Result                api.HDRDecodedResult
	Size                  int64
	SHA256                string
}

// PendingHDRRequest preserves explicit analysis intent independently of terminal
// results. Separate sequence boundaries preserve native and decoded checkpoints
// completed during this request when an interruption prevents terminal publication.
type PendingHDRRequest struct {
	Instructions       api.HDRAnalysisInstructions
	RetryAfterSequence uint64
	// Nil identifies requests saved before native retry boundaries were recorded.
	NativeRetryAfterSequence *uint64
}

// RetainedHDRMeasurementResource opens an integrity-checked numeric report.
// The caller closes the returned reader; opening never reads source media.
type RetainedHDRMeasurementResource interface {
	OpenMeasurement(context.Context, HDRMeasurementRecord) (io.ReadCloser, error)
}

// RetainedHDRExtractionResource opens private metadata bound to its retained record.
// The caller owns and must close a successfully returned reader.
type RetainedHDRExtractionResource interface {
	OpenExtraction(context.Context, HDRExtractionRecord) (io.ReadCloser, error)
}

// RetainedHDRSectionResource opens a section from the same verified attempt.
// The manifest's size and hash remain authoritative in addition to resource integrity.
type RetainedHDRSectionResource interface {
	OpenSection(context.Context, HDRExtractionRecord, string) (io.ReadCloser, error)
}

// RetainedHDRAnalysisResource resolves verified plots under an exact analysis result.
// Callers close OpenArtifact's body and keep LocalArtifactPath private.
type RetainedHDRAnalysisResource interface {
	OpenArtifact(context.Context, api.HDRAnalysisResult, api.PublicResourceID) (MediaArtifactContent, error)
	LocalArtifactPath(api.HDRAnalysisResult, api.PublicResourceID) (string, error)
}

// HDRAnalysisBuilder produces terminal target outcomes and privately retained artifacts.
// Its extraction callback checkpoints complete native metadata or verified absence;
// HDRMeasurementStore separately checkpoints closed numeric reports before rendering.
// Both checkpoints survive a later rendering failure or canceled terminal publication.
type HDRAnalysisBuilder interface {
	Build(context.Context, HDRPreparation, api.HDRAnalysisInstructions, string, time.Time, *api.HDRAnalysisResult, RetainedHDRAnalysisResource,
		map[api.HDRExtractionID]HDRExtractionRecord, map[api.HDRExtractionID]RetainedHDRExtractionResource,
		func(HDRExtractionRecord, RetainedHDRExtractionResource) error, *HDRMeasurementStore) (api.HDRAnalysisResult, RetainedHDRAnalysisResource, error)
}

// HDRPreparation carries the exact workflow-owned generation and its prepared target facts.
// It permits retained analysis without reopening media or hydrating unrelated resources.
type HDRPreparation struct {
	Release api.ReleaseRef
	Targets []api.HDRAnalysisTarget
}

func hdrPreparation(snapshot api.ReleaseSnapshot) HDRPreparation {
	return HDRPreparation{
		Release: api.ReleaseRef{SourcePath: snapshot.Release.Source.SourcePath, Generation: snapshot.Release.Generation},
		Targets: snapshot.Display.HDRTargets,
	}
}

// HDRAnalysisAuthorityValidator rechecks a retained result against its prepared source generation.
type HDRAnalysisAuthorityValidator interface {
	ValidateAuthority(context.Context, HDRPreparation, api.HDRAnalysisResult) error
}

// HDRAnalysisAttemptCleaner removes interrupted files except checkpointed metadata.
type HDRAnalysisAttemptCleaner interface {
	CleanupAttempt(api.ReleaseRef, string, map[api.HDRExtractionID]HDRExtractionRecord, map[string]HDRMeasurementRecord) error
}

// CompatibleHDRExtractionRestorer copies compatible retained metadata into a new prepared generation.
// Stale or invalid entries are omitted; returned resources own newly retained copies.
type CompatibleHDRExtractionRestorer interface {
	CloneExtractions(
		context.Context,
		HDRPreparation,
		map[api.HDRExtractionID]HDRExtractionRecord,
		map[api.HDRExtractionID]RetainedHDRExtractionResource,
		string,
	) (map[api.HDRExtractionID]HDRExtractionRecord, map[api.HDRExtractionID]RetainedHDRExtractionResource, error)
}

// AnalyzeHDRCommand starts an explicit analysis attempt for an exact prepared generation.
type AnalyzeHDRCommand struct {
	// Comparison is resolved by the module and retained only in its private command capsule.
	Comparison       *HDRComparisonSource
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
	ArtifactIDs      []api.PublicResourceID
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
		ArtifactIDs      []api.PublicResourceID
	}{c.ExpectedRevision, c.Enabled, c.ArtifactIDs})
}
