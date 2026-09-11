// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// workflowInputReadiness borrows the registry for local Input policy evaluation.
type workflowInputReadiness struct{ registry *trackers.Registry }

func (w workflowInputReadiness) Evaluate(ctx context.Context, subject api.UploadSubject, selected []api.TrackerID) (api.InputReadinessEvaluation, error) {
	if err := ctx.Err(); err != nil {
		return api.InputReadinessEvaluation{}, fmt.Errorf("input readiness canceled: %w", err)
	}
	result, err := trackers.EvaluateInputReadiness(w.registry, selected, subject)
	if err != nil {
		return api.InputReadinessEvaluation{}, fmt.Errorf("input readiness: %w", err)
	}
	return result, nil
}

func (w workflowInputReadiness) Requirements(ctx context.Context, selected []api.TrackerID) (api.MetadataRequirementSet, error) {
	if err := ctx.Err(); err != nil {
		return api.MetadataRequirementSet{}, fmt.Errorf("input requirements canceled: %w", err)
	}
	result, err := trackers.CollectMetadataRequirements(w.registry, selected)
	if err != nil {
		return api.MetadataRequirementSet{}, fmt.Errorf("input requirements: %w", err)
	}
	return result, nil
}
