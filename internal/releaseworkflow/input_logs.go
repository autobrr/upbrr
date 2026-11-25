// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

func (m *Module) beginInputLog(ctx context.Context, path, kind string) (context.Context, func(string, string)) {
	root, _ := m.logger.(*logging.Logger)
	var store logging.InputLogRepository
	if repository, ok := m.repository.(*PersistentRepository); ok {
		store, _ = repository.states.(logging.InputLogRepository)
	}
	ctx, _, finish := root.BeginInputLog(ctx, store, path, kind)
	return ctx, finish
}

// Use the verified slot rather than the command path, which may be the
// reason preparation fails. Missing or changed authority is not attributable.
func (m *Module) activeOperationLogPath(ctx context.Context, owner string, workflowID api.WorkflowID) string {
	authority, ok := api.ActiveInputAuthorityFromContext(ctx)
	if !ok || m.activeInputs == nil {
		return ""
	}
	slot, err := m.ActiveInput(ctx, owner)
	if err != nil || slot.State != api.ActiveInputActive || slot.WorkflowID != workflowID ||
		slot.CoordinatorID != authority.CoordinatorID || slot.Fence != authority.Fence {
		return ""
	}
	record, err := m.activeInputs.LoadInputRecordByID(ctx, slot.InputID)
	if err != nil || record.SourceVersion != slot.SourceVersion {
		return ""
	}
	return record.CanonicalPath
}
