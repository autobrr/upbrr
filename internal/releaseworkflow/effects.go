// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/pkg/api"
)

type durableExternalEffectReporter struct {
	module      *Module
	ownerID     string
	workflowID  api.WorkflowID
	operationID api.WorkflowOperationID
	mu          sync.Mutex
	attempts    map[string]api.ReleaseWorkflowEffectRecord
}

func newDurableExternalEffectReporter(
	module *Module,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
) *durableExternalEffectReporter {
	return &durableExternalEffectReporter{
		module:      module,
		ownerID:     strings.TrimSpace(ownerID),
		workflowID:  workflowID,
		operationID: operationID,
		attempts:    make(map[string]api.ReleaseWorkflowEffectRecord),
	}
}

func (r *durableExternalEffectReporter) Begin(
	ctx context.Context,
	effect api.WorkflowExternalEffect,
) (api.WorkflowExternalEffectReceipt, error) {
	effectID, err := r.module.newID("effect")
	if err != nil {
		return api.WorkflowExternalEffectReceipt{}, err
	}
	now := r.module.clock.Now().UTC()
	record, idempotent, err := r.module.durability.BeginEffect(ctx, api.ReleaseWorkflowEffectRecord{
		OwnerID:             r.ownerID,
		WorkflowID:          r.workflowID,
		OperationID:         r.operationID,
		EffectID:            effectID,
		Kind:                string(effect.Kind),
		ScopeID:             strings.TrimSpace(effect.ScopeID),
		SemanticFingerprint: effect.SemanticFingerprint,
		Status:              api.WorkflowEffectStatusStarted,
		StartedAt:           now,
		UpdatedAt:           now,
	})
	if err != nil {
		return api.WorkflowExternalEffectReceipt{}, fmt.Errorf("release workflow fence external effect: %w", err)
	}
	if idempotent && record.Status == api.WorkflowEffectStatusSucceeded {
		return api.WorkflowExternalEffectReceipt{
			EffectID:         record.EffectID,
			AlreadySucceeded: true,
		}, nil
	}
	r.mu.Lock()
	r.attempts[record.EffectID] = record
	r.mu.Unlock()
	return api.WorkflowExternalEffectReceipt{EffectID: record.EffectID}, nil
}

func (r *durableExternalEffectReporter) Complete(
	ctx context.Context,
	receipt api.WorkflowExternalEffectReceipt,
	succeeded bool,
) error {
	r.mu.Lock()
	record, ok := r.attempts[receipt.EffectID]
	if ok {
		delete(r.attempts, receipt.EffectID)
	}
	r.mu.Unlock()
	if !ok {
		return api.ErrReleaseWorkflowEffectConflict
	}
	now := r.module.clock.Now().UTC()
	record.UpdatedAt = now
	record.CompletedAt = &now
	status := api.WorkflowEffectStatusFailed
	if succeeded {
		status = api.WorkflowEffectStatusSucceeded
	}
	if err := r.module.durability.CompleteEffect(context.WithoutCancel(ctx), status, record); err != nil {
		return fmt.Errorf("release workflow retain external effect receipt: %w", err)
	}
	return nil
}
