// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

const privateResourceKindHDRComparison = "releaseworkflow/hdr-comparison/v1"

// HDRComparisonSource is private source authority. Only Ref is publicly projected.
type HDRComparisonSource struct {
	Ref      api.HDRComparisonSourceRef
	Path     string
	Evidence api.HDRSourceEvidence
}

// HDRComparisonInspector probes a canonical local path and its verified source fingerprint.
// Returned facts describe decoder eligibility without granting primary-input ownership.
type HDRComparisonInspector func(context.Context, api.ReleaseRef, string, string) (api.HDRSourceEvidence, error)

// WithHDRComparisonInspector supplies per-file evidence for explicit comparison selection.
func WithHDRComparisonInspector(inspector HDRComparisonInspector) Option {
	return func(m *Module) error { m.hdrComparisonInspector = inspector; return nil }
}

func hdrComparisonPrivateResourceID(id string) string { return "hdr-comparison:" + id }

func (s HDRComparisonSource) MarshalPrivateResource() (string, []byte, error) {
	payload, err := json.Marshal(s)
	if err != nil {
		return "", nil, fmt.Errorf("encode HDR comparison source: %w", err)
	}
	return privateResourceKindHDRComparison, payload, nil
}

func decodeHDRComparisonSource(payload []byte) (any, error) {
	if len(payload) > 64<<10 {
		return nil, ErrPrivateResourceIntegrity
	}
	var source HDRComparisonSource
	if err := json.Unmarshal(payload, &source); err != nil {
		return nil, fmt.Errorf("decode HDR comparison source: %w", err)
	}
	if source.Ref.ID == "" || source.Path == "" || source.Ref.Fingerprint == "" || source.Ref.Release.Generation == 0 || source.Ref.ExpiresAt.IsZero() {
		return nil, ErrPrivateResourceIntegrity
	}
	return source, nil
}

// SelectHDRComparisonCommand registers private source B for one prepared generation.
// It preserves the primary active input and supplies a new handle for subsequent comparisons.
type SelectHDRComparisonCommand struct {
	ForComposite     bool
	WorkflowID       api.WorkflowID
	ExpectedRevision api.WorkflowRevision
	Release          api.ReleaseRef
	SourcePath       string
	IdempotencyKey   string
}

func (SelectHDRComparisonCommand) commandName() string              { return "select_hdr_comparison" }
func (SelectHDRComparisonCommand) userIntent()                      {}
func (SelectHDRComparisonCommand) operationKind() api.OperationKind { return api.OperationKindUnknown }
func (c SelectHDRComparisonCommand) commandFingerprint() (api.WorkflowFingerprint, error) {
	if c.Release.SourcePath == "" || c.Release.Generation == 0 || strings.TrimSpace(c.SourcePath) == "" || len(c.SourcePath) > 32768 {
		return "", errors.New("HDR comparison requires a source and prepared generation")
	}
	return canonicalCommandFingerprint(struct {
		Revision     api.WorkflowRevision
		Release      api.ReleaseRef
		Path         string
		ForComposite bool
	}{c.ExpectedRevision, c.Release, c.SourcePath, c.ForComposite})
}

// VerifyHDRComparisonSource uses the same bounded input identity as primary
// preparation. It never opens/replaces the singleton active input.
func VerifyHDRComparisonSource(ctx context.Context, path string, verifier InputVerifier) (api.InputRecord, error) {
	if verifier == nil {
		return api.InputRecord{}, ErrInvalidTransition
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return api.InputRecord{}, errors.New("HDR comparison requires an accessible regular file")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".mp4", ".ts", ".m2ts", ".hevc", ".h265", ".265":
	default:
		return api.InputRecord{}, errors.New("HDR comparison source format is unsupported")
	}
	record, err := verifier(ctx, api.PrepareInput{SourcePath: path})
	if err != nil {
		return api.InputRecord{}, fmt.Errorf("verify HDR comparison source: %w", err)
	}
	var verified api.VerifiedInputSource
	if err := json.Unmarshal(record.Manifest, &verified); err != nil {
		return api.InputRecord{}, ErrPrivateResourceIntegrity
	}
	if len(verified.Identity.Files) != 1 || record.SourceVersion == "" || record.SourceVersion != verified.Identity.Digest ||
		!pathing.SamePath(verified.Identity.Files[0].LocalPath, record.CanonicalPath) {
		return api.InputRecord{}, ErrPrivateResourceIntegrity
	}
	return record, nil
}

func (m *Module) selectHDRComparison(
	ctx context.Context,
	owner string,
	state *State,
	now time.Time,
	command SelectHDRComparisonCommand,
) (CommandResult, error) {
	release, err := currentReleaseSnapshot(state)
	if err != nil {
		return CommandResult{}, err
	}
	ref := api.ReleaseRef{SourcePath: release.Release.Source.SourcePath, Generation: release.Release.Generation}
	if command.Release != ref || m.hdrComparisonInspector == nil {
		return CommandResult{}, ErrInvalidTransition
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	record, err := VerifyHDRComparisonSource(ctx, command.SourcePath, m.inputVerifier)
	if err != nil {
		return CommandResult{}, err
	}
	evidence, err := m.hdrComparisonInspector(ctx, ref, record.CanonicalPath, record.SourceVersion)
	if err != nil {
		return CommandResult{}, fmt.Errorf("inspect HDR comparison: %w", err)
	}
	verified, err := VerifyHDRComparisonSource(ctx, record.CanonicalPath, m.inputVerifier)
	if err != nil {
		return CommandResult{}, err
	}
	if record.SourceVersion != verified.SourceVersion || !pathing.SamePath(record.CanonicalPath, verified.CanonicalPath) {
		return CommandResult{}, api.ErrActiveInputChanged
	}
	if _, err := m.activeMutationContext(ctx, owner, state.Workflow.ID); err != nil {
		return CommandResult{}, err
	}
	if err := m.requireOperationOwnership(ctx, owner, state.Workflow.ID); err != nil {
		return CommandResult{}, err
	}
	id, err := m.newID("hdr-comparison")
	if err != nil {
		return CommandResult{}, err
	}
	source := HDRComparisonSource{
		Ref: api.HDRComparisonSourceRef{
			ID:          id,
			Fingerprint: record.SourceVersion,
			Release:     ref,
			ExpiresAt:   now.Add(workflowCommandTTL),
		},
		Path:     record.CanonicalPath,
		Evidence: evidence,
	}
	if err := m.private.Put(owner, state.Workflow.ID, hdrComparisonPrivateResourceID(id), source, source.Ref.ExpiresAt); err != nil {
		return CommandResult{}, fmt.Errorf("retain HDR comparison: %w", err)
	}
	if command.ForComposite {
		if state.Composite == nil || state.Composite.ComparisonSelection == nil ||
			state.Composite.ComparisonSelection.Path != command.SourcePath || state.Composite.Intent.HDRAnalysis == nil ||
			state.Composite.Intent.HDRAnalysis.Decoded == nil || state.Composite.Intent.HDRAnalysis.Decoded.Comparison == nil {
			m.private.Delete(owner, state.Workflow.ID, hdrComparisonPrivateResourceID(id))
			return CommandResult{}, ErrInvalidTransition
		}
		request := *state.Composite.Intent.HDRAnalysis
		decoded := *request.Decoded
		comparison := *decoded.Comparison
		comparison.SourceID = id
		decoded.Comparison, request.Decoded = &comparison, &decoded
		state.Composite.Intent.HDRAnalysis = &request
		state.Composite.ComparisonSelection = nil
	}
	state.Workflow.HDRComparison = &source.Ref
	return CommandResult{HDRComparison: &source.Ref}, nil
}

func (m *Module) resolveHDRComparison(owner string, state State, instructions api.HDRAnalysisInstructions) (*HDRComparisonSource, error) {
	if instructions.Decoded == nil || instructions.Decoded.Comparison == nil {
		return nil, nil
	}
	id := instructions.Decoded.Comparison.SourceID
	ref := state.Workflow.HDRComparison
	if ref == nil || ref.ID != id || ref.Release != instructions.Release {
		return nil, ErrPrivateResourceUnavailable
	}
	value, err := m.private.Get(owner, state.Workflow.ID, hdrComparisonPrivateResourceID(id), m.clock.Now().UTC())
	if err != nil {
		if instructions.Decoded.Automatic && state.OwnerID == owner && errors.Is(err, ErrPrivateResourceUnavailable) {
			// The exact owner/generation/handle was already selected. Expiry
			// removes authority to read it, while primary analysis remains valid.
			return nil, nil
		}
		return nil, fmt.Errorf("resolve HDR comparison source: %w", err)
	}
	source, ok := value.(HDRComparisonSource)
	if !ok || source.Ref != *ref {
		return nil, ErrPrivateResourceIntegrity
	}
	return &source, nil
}
