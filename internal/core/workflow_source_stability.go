// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/pkg/api"
)

// workflowSourceEffectReporter checks the captured source immediately before
// each tracker admission, including plans held while the user reviews them.
type workflowSourceEffectReporter struct {
	reporter api.WorkflowExternalEffectReporter
	manifest api.SourceManifest
}

func (r workflowSourceEffectReporter) Begin(ctx context.Context, effect api.WorkflowExternalEffect) (api.WorkflowExternalEffectReceipt, error) {
	if effect.Kind == api.WorkflowExternalEffectTrackerSubmission {
		if err := preparedrelease.VerifySourceManifestStability(ctx, r.manifest); err != nil {
			return api.WorkflowExternalEffectReceipt{}, fmt.Errorf("workflow submission source stability: %w", err)
		}
	}
	receipt, err := r.reporter.Begin(ctx, effect)
	if err != nil {
		return api.WorkflowExternalEffectReceipt{}, fmt.Errorf("workflow source effect admission: %w", err)
	}
	return receipt, nil
}

func (r workflowSourceEffectReporter) Complete(ctx context.Context, receipt api.WorkflowExternalEffectReceipt, succeeded bool) error {
	if err := r.reporter.Complete(ctx, receipt, succeeded); err != nil {
		return fmt.Errorf("workflow source effect completion: %w", err)
	}
	return nil
}
