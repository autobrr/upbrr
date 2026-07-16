// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

// PreparationIntent selects how deeply an adapter prepares a tracker operation.
type PreparationIntent string

const (
	// PreparationIntentDescriptionPreview resolves description content without upload artifacts or mutation.
	PreparationIntentDescriptionPreview PreparationIntent = "description_preview"
	// PreparationIntentDryRun resolves upload artifacts and payload preview without tracker mutation.
	PreparationIntentDryRun PreparationIntent = "dry_run"
	// PreparationIntentUpload prepares a single-use plan that may submit to the tracker.
	PreparationIntentUpload PreparationIntent = "upload"
)

var (
	// ErrPlanNotSubmittable indicates that a plan has no upload action.
	ErrPlanNotSubmittable = errors.New("tracker plan is not submittable")
	// ErrPlanAlreadyUsed indicates that an upload plan already authorized one submission attempt.
	ErrPlanAlreadyUsed = errors.New("tracker plan already submitted")
	// ErrPlanReleased indicates that plan resources were released before submission.
	ErrPlanReleased = errors.New("tracker plan already released")
)

// PreparationFailure is a tracker-local, presentation-safe preparation failure.
//
//nolint:errname // Failure is the accepted domain outcome term used by the tracker-plan contract.
type PreparationFailure struct {
	tracker string
	code    string
	message string
	cause   error
}

// NewPreparationFailure constructs a safe tracker-local failure.
func NewPreparationFailure(tracker string, code string, message string, cause error) *PreparationFailure {
	trimmedMessage := strings.TrimSpace(redaction.RedactValue(message, nil))
	if trimmedMessage == "" {
		trimmedMessage = "tracker preparation failed"
	}
	return &PreparationFailure{
		tracker: strings.ToUpper(strings.TrimSpace(tracker)),
		code:    strings.TrimSpace(code),
		message: trimmedMessage,
		cause:   cause,
	}
}

// Error returns sanitized tracker-scoped failure text.
func (f *PreparationFailure) Error() string {
	if f == nil {
		return "tracker preparation failed"
	}
	if f.tracker == "" {
		return f.message
	}
	return fmt.Sprintf("trackers: %s: %s", f.tracker, f.message)
}

// Unwrap exposes the diagnostic preparation cause.
func (f *PreparationFailure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.cause
}

// Tracker returns the normalized tracker identifier attributed to the failure.
func (f *PreparationFailure) Tracker() string {
	if f == nil {
		return ""
	}
	return f.tracker
}

// Code returns the stable preparation failure class.
func (f *PreparationFailure) Code() string {
	if f == nil {
		return ""
	}
	return f.code
}

// Message returns sanitized operator-facing failure detail.
func (f *PreparationFailure) Message() string {
	if f == nil {
		return ""
	}
	return f.message
}

type planState struct {
	mu       sync.Mutex
	used     bool
	released bool
	submit   func(context.Context) (api.UploadSummary, error)
	release  func() error
}

// TrackerPlan is an immutable operation-scoped adapter plan. Its private state
// authorizes at most one submission and releases owned resources exactly once.
type TrackerPlan struct {
	tracker     string
	intent      PreparationIntent
	description DescriptionResult
	dryRun      api.TrackerDryRunEntry
	state       *planState
}

// NewDescriptionPlan constructs a shallow, non-submittable description plan.
func NewDescriptionPlan(tracker string, result DescriptionResult) TrackerPlan {
	return TrackerPlan{
		tracker:     strings.ToUpper(strings.TrimSpace(tracker)),
		intent:      PreparationIntentDescriptionPreview,
		description: result,
		state:       &planState{},
	}
}

// NewDryRunPlan constructs a non-submittable dry-run plan.
func NewDryRunPlan(tracker string, preview api.TrackerDryRunEntry, release func() error) TrackerPlan {
	return TrackerPlan{
		tracker: strings.ToUpper(strings.TrimSpace(tracker)),
		intent:  PreparationIntentDryRun,
		dryRun:  cloneTrackerDryRunEntry(preview),
		state:   &planState{release: release},
	}
}

// NewUploadPlan constructs a single-use upload plan from already-prepared payload state.
func NewUploadPlan(
	tracker string,
	preview api.TrackerDryRunEntry,
	submit func(context.Context) (api.UploadSummary, error),
	release func() error,
) TrackerPlan {
	return TrackerPlan{
		tracker: strings.ToUpper(strings.TrimSpace(tracker)),
		intent:  PreparationIntentUpload,
		dryRun:  cloneTrackerDryRunEntry(preview),
		state:   &planState{submit: submit, release: release},
	}
}

// PrepareAdapter implements the common intent dispatch while keeping tracker-specific builders local.
func PrepareAdapter(
	ctx context.Context,
	input PreparationInput,
	description func(context.Context, PreparationInput) (DescriptionResult, error),
	dryRun func(context.Context, PreparationInput) (api.TrackerDryRunEntry, error),
	submit func(context.Context, PreparationInput) (api.UploadSummary, error),
) (TrackerPlan, *PreparationFailure) {
	input = clonePreparationInput(input)
	if err := ctx.Err(); err != nil {
		return TrackerPlan{}, NewPreparationFailure(input.Tracker, "canceled", "preparation canceled", err)
	}
	switch input.Intent {
	case PreparationIntentDescriptionPreview:
		result, err := description(ctx, input)
		if err != nil {
			return TrackerPlan{}, NewPreparationFailure(input.Tracker, "description", err.Error(), err)
		}
		return NewDescriptionPlan(input.Tracker, result), nil
	case PreparationIntentDryRun:
		preview, err := dryRun(ctx, input)
		if err != nil {
			return TrackerPlan{}, NewPreparationFailure(input.Tracker, "dry_run", err.Error(), err)
		}
		return NewDryRunPlan(input.Tracker, preview, nil), nil
	case PreparationIntentUpload:
		preview, err := dryRun(ctx, input)
		if err != nil {
			return TrackerPlan{}, NewPreparationFailure(input.Tracker, "upload", err.Error(), err)
		}
		return NewUploadPlan(input.Tracker, preview, func(submitCtx context.Context) (api.UploadSummary, error) {
			return submit(submitCtx, input)
		}, nil), nil
	default:
		return TrackerPlan{}, NewPreparationFailure(input.Tracker, "intent", "unsupported preparation intent", nil)
	}
}

func clonePreparationInput(input PreparationInput) PreparationInput {
	if input.Assets != nil {
		assets, _ := PreparedDescriptionAssets(input.Assets)
		input.Assets = &assets
	}
	return input
}

// Tracker returns the normalized tracker identifier bound to the plan.
func (p TrackerPlan) Tracker() string { return p.tracker }

// Intent returns the preparation depth used to create the plan.
func (p TrackerPlan) Intent() PreparationIntent { return p.intent }

// Description returns the prepared description result.
func (p TrackerPlan) Description() DescriptionResult { return p.description }

// DryRun returns a defensive copy of the prepared payload preview.
func (p TrackerPlan) DryRun() api.TrackerDryRunEntry { return cloneTrackerDryRunEntry(p.dryRun) }

// Submit invokes the upload action at most once.
func (p TrackerPlan) Submit(ctx context.Context) (api.UploadSummary, error) {
	if p.state == nil || p.intent != PreparationIntentUpload {
		return api.UploadSummary{}, ErrPlanNotSubmittable
	}
	p.state.mu.Lock()
	switch {
	case p.state.released:
		p.state.mu.Unlock()
		return api.UploadSummary{}, ErrPlanReleased
	case p.state.used:
		p.state.mu.Unlock()
		return api.UploadSummary{}, ErrPlanAlreadyUsed
	case p.state.submit == nil:
		p.state.mu.Unlock()
		return api.UploadSummary{}, ErrPlanNotSubmittable
	}
	p.state.used = true
	submit := p.state.submit
	p.state.mu.Unlock()
	return submit(ctx)
}

// Release releases plan-owned resources exactly once. Repeated calls are no-ops.
func (p TrackerPlan) Release() error {
	if p.state == nil {
		return nil
	}
	p.state.mu.Lock()
	if p.state.released {
		p.state.mu.Unlock()
		return nil
	}
	p.state.released = true
	release := p.state.release
	p.state.mu.Unlock()
	if release == nil {
		return nil
	}
	return release()
}

func cloneTrackerDryRunEntry(entry api.TrackerDryRunEntry) api.TrackerDryRunEntry {
	entry.Payload = maps.Clone(entry.Payload)
	entry.Files = append([]api.TrackerDryRunFile(nil), entry.Files...)
	entry.DebugSections = append([]api.TrackerDryRunDebugSection(nil), entry.DebugSections...)
	for idx := range entry.DebugSections {
		entry.DebugSections[idx].Payload = maps.Clone(entry.DebugSections[idx].Payload)
		entry.DebugSections[idx].Files = append([]api.TrackerDryRunFile(nil), entry.DebugSections[idx].Files...)
	}
	if entry.Questionnaire != nil {
		questionnaire := *entry.Questionnaire
		questionnaire.Fields = append([]api.TrackerQuestionnaireField(nil), questionnaire.Fields...)
		for idx := range questionnaire.Fields {
			questionnaire.Fields[idx].Options = append([]string(nil), questionnaire.Fields[idx].Options...)
		}
		entry.Questionnaire = &questionnaire
	}
	entry.ImageHost.AllowedHosts = append([]string(nil), entry.ImageHost.AllowedHosts...)
	entry.ImageHost.Warnings = append([]api.ImageHostWarning(nil), entry.ImageHost.Warnings...)
	return entry
}
