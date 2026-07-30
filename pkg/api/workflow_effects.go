// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// WorkflowExternalEffectKind identifies an externally visible side effect.
type WorkflowExternalEffectKind string

const (
	WorkflowExternalEffectTrackerSubmission WorkflowExternalEffectKind = "tracker_submission"
	WorkflowExternalEffectClientInjection   WorkflowExternalEffectKind = "client_injection"
	WorkflowExternalEffectImageHosting      WorkflowExternalEffectKind = "image_hosting"
)

// WorkflowExternalEffect identifies one exact external semantic attempt.
type WorkflowExternalEffect struct {
	Kind                WorkflowExternalEffectKind
	ScopeID             string
	SemanticFingerprint WorkflowFingerprint
}

// WorkflowExternalEffectReceipt is private in-process authority to complete a
// previously persisted attempt_started record.
type WorkflowExternalEffectReceipt struct {
	EffectID         string
	AlreadySucceeded bool
}

// WorkflowExternalEffectReporter persists attempt fences around side effects.
type WorkflowExternalEffectReporter interface {
	Begin(context.Context, WorkflowExternalEffect) (WorkflowExternalEffectReceipt, error)
	Complete(context.Context, WorkflowExternalEffectReceipt, bool) error
}

type workflowExternalEffectReporterContextKey struct{}

// WithWorkflowExternalEffectReporter installs one operation-scoped effect fence.
func WithWorkflowExternalEffectReporter(
	ctx context.Context,
	reporter WorkflowExternalEffectReporter,
) context.Context {
	if ctx == nil || reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, workflowExternalEffectReporterContextKey{}, reporter)
}

// BeginWorkflowExternalEffect durably fences one external attempt when the
// current workflow operation installed a reporter.
func BeginWorkflowExternalEffect(
	ctx context.Context,
	effect WorkflowExternalEffect,
) (WorkflowExternalEffectReceipt, error) {
	if err := validateWorkflowExternalEffect(effect); err != nil {
		return WorkflowExternalEffectReceipt{}, err
	}
	reporter, _ := ctx.Value(workflowExternalEffectReporterContextKey{}).(WorkflowExternalEffectReporter)
	if reporter == nil {
		return WorkflowExternalEffectReceipt{}, nil
	}
	receipt, err := reporter.Begin(ctx, effect)
	if err != nil {
		return WorkflowExternalEffectReceipt{}, fmt.Errorf("workflow external effect begin: %w", err)
	}
	return receipt, nil
}

// CompleteWorkflowExternalEffect persists a known success or failure receipt.
func CompleteWorkflowExternalEffect(
	ctx context.Context,
	receipt WorkflowExternalEffectReceipt,
	succeeded bool,
) error {
	if strings.TrimSpace(receipt.EffectID) == "" || receipt.AlreadySucceeded {
		return nil
	}
	reporter, _ := ctx.Value(workflowExternalEffectReporterContextKey{}).(WorkflowExternalEffectReporter)
	if reporter == nil {
		return nil
	}
	if err := reporter.Complete(ctx, receipt, succeeded); err != nil {
		return fmt.Errorf("workflow external effect complete: %w", err)
	}
	return nil
}

func validateWorkflowExternalEffect(effect WorkflowExternalEffect) error {
	switch effect.Kind {
	case WorkflowExternalEffectTrackerSubmission, WorkflowExternalEffectClientInjection, WorkflowExternalEffectImageHosting:
	case "":
		return errors.New("workflow external effect kind is required")
	default:
		return fmt.Errorf("unsupported workflow external effect kind %q", effect.Kind)
	}
	if strings.TrimSpace(effect.ScopeID) == "" || effect.SemanticFingerprint == "" {
		return errors.New("workflow external effect scope and semantic fingerprint are required")
	}
	return nil
}
