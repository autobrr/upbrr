// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

type orphanHistoryWorkflow struct {
	scope     api.WorkflowScope
	paths     []string
	ambiguous bool
	empty     bool
}

// orphanHistoryWorkflowAuthority is the persisted State subset whose contents
// require a durable release binding. It deliberately uses the producer's
// aggregate type so Workflow's JSON field names stay aligned with its API
// contract.
type orphanHistoryWorkflowAuthority struct {
	Workflow               api.ReleaseWorkflow
	InputReadiness         map[string]json.RawMessage
	Catalogs               map[string]json.RawMessage
	Runtimes               map[string]json.RawMessage
	Selections             map[string]json.RawMessage
	ProjectionInstructions map[string]json.RawMessage
	Projections            map[string]json.RawMessage
	Preflights             map[string]json.RawMessage
	Dupes                  map[string]json.RawMessage
	TrackerApprovals       map[string]json.RawMessage
	Media                  map[string]json.RawMessage
	Descriptions           map[string]json.RawMessage
	DryRuns                map[string]json.RawMessage
	UploadResults          map[string]json.RawMessage
	Operations             map[string]api.WorkflowOperationStatus
	Receipts               map[string]orphanHistoryCommandReceipt
	Composite              *orphanHistoryCompositeAuthority
}

type orphanHistoryCommandReceipt struct {
	Result api.ReleaseWorkflowCurrent
}

type orphanHistoryCompositeAuthority struct {
	ActiveOperationID   api.WorkflowOperationID    `json:"activeOperationId"`
	LastOperationID     api.WorkflowOperationID    `json:"lastOperationId"`
	ApprovedDryRun      *api.UploadDryRunResultRef `json:"approvedDryRun"`
	ApprovedFingerprint api.WorkflowFingerprint    `json:"approvedFingerprint"`
	ApprovedTrackerIDs  []api.TrackerID            `json:"approvedTrackerIds"`
	FeedbackSequence    uint64                     `json:"feedbackSequence"`
	FeedbackReceipts    map[string]json.RawMessage `json:"feedbackReceipts"`
	TerminalReason      string                     `json:"terminalReason"`
}

// ListOrphanedHistoryPaths returns stored source paths and empty workflow
// scopes that are absent from visible History and have no active ownership.
// It preserves related parent and child paths, every path owned by a workflow
// linked to retained history. Ambiguous workflow source ownership defers all
// cleanup. Callers remove the returned paths and scopes inside
// [SQLiteRepository.WithHistoryDeletion].
func (r *SQLiteRepository) ListOrphanedHistoryPaths(ctx context.Context) ([]string, []api.WorkflowScope, error) {
	if r == nil || r.db == nil {
		return nil, nil, errors.New("db: repository not initialized")
	}
	storedPaths, err := r.ListStoredReleasePaths(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("db list orphaned history stored paths: %w", err)
	}
	historyEntries, err := r.ListHistoryEntries(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("db list orphaned history entries: %w", err)
	}
	protected, err := r.listHistoryProtectedSources(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("db list orphaned history protected paths: %w", err)
	}
	if protected.missingSource {
		return nil, nil, nil
	}
	workflows, err := r.listOrphanHistoryWorkflows(ctx)
	if err != nil {
		return nil, nil, err
	}
	legacyGroups, err := r.listLegacyUIStateSourceGroups(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("db list orphaned history legacy ui states: %w", err)
	}
	for _, workflow := range workflows {
		if workflow.ambiguous {
			// Missing ownership can refer to any stored source, including one
			// whose remaining rows otherwise look orphaned.
			if r.logger != nil {
				r.logger.Warnf("db: orphan history cleanup decision=defer reason=ambiguous_workflow_ownership")
			}
			return nil, nil, nil
		}
		if !workflow.empty {
			continue
		}
		hasEffectsOrFences, inspectErr := r.orphanHistoryWorkflowHasEffectsOrFences(ctx, workflow.scope)
		if inspectErr != nil {
			return nil, nil, inspectErr
		}
		if hasEffectsOrFences {
			if r.logger != nil {
				r.logger.Warnf("db: orphan history cleanup decision=defer reason=source_less_workflow_authority")
			}
			return nil, nil, nil
		}
	}

	retained := make([]string, 0, len(historyEntries)+len(protected.paths))
	for _, entry := range historyEntries {
		retained = addOrphanHistoryPath(retained, entry.SourcePath)
	}
	for _, path := range protected.paths {
		retained = addOrphanHistoryPath(retained, path)
	}
	for _, workflow := range workflows {
		if orphanHistoryWorkflowScopeProtected(workflow.scope, protected.scopes) {
			retained = addOrphanHistoryPaths(retained, workflow.paths)
		}
	}

	for changed := true; changed; {
		changed = false
		for _, path := range storedPaths {
			if orphanHistoryPathRelated(path, retained) {
				pathCount := len(retained)
				retained = addOrphanHistoryPath(retained, path)
				changed = changed || len(retained) != pathCount
			}
		}
		for _, workflow := range workflows {
			if orphanHistoryWorkflowScopeProtected(workflow.scope, protected.scopes) ||
				orphanHistoryPathRelatedAny(workflow.paths, retained) {
				for _, path := range workflow.paths {
					pathCount := len(retained)
					retained = addOrphanHistoryPath(retained, path)
					changed = changed || len(retained) != pathCount
				}
			}
		}
		for _, group := range legacyGroups {
			if orphanHistoryPathRelatedAny(group.paths, retained) {
				for _, path := range group.paths {
					pathCount := len(retained)
					retained = addOrphanHistoryPath(retained, path)
					changed = changed || len(retained) != pathCount
				}
			}
		}
	}

	orphanedPaths := make([]string, 0, len(storedPaths))
	for _, path := range storedPaths {
		if !orphanHistoryPathRelated(path, retained) {
			orphanedPaths = append(orphanedPaths, path)
		}
	}

	orphanedScopes := make([]api.WorkflowScope, 0)
	for _, workflow := range workflows {
		if !workflow.empty || orphanHistoryWorkflowScopeProtected(workflow.scope, protected.scopes) {
			continue
		}
		orphanedScopes = append(orphanedScopes, workflow.scope)
	}
	return orphanedPaths, orphanedScopes, nil
}

func (r *SQLiteRepository) listOrphanHistoryWorkflows(ctx context.Context) ([]orphanHistoryWorkflow, error) {
	rows, err := r.historyQuery(ctx).QueryContext(ctx, `
		SELECT owner_id, workflow_id, status, state_json
		FROM release_workflow_states
		ORDER BY owner_id ASC, workflow_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("db list orphaned history workflows: %w", err)
	}
	defer rows.Close()

	workflows := make([]orphanHistoryWorkflow, 0)
	for rows.Next() {
		var workflow orphanHistoryWorkflow
		var status api.WorkflowStatus
		var payload []byte
		if err := rows.Scan(&workflow.scope.OwnerID, &workflow.scope.WorkflowID, &status, &payload); err != nil {
			return nil, fmt.Errorf("db scan orphaned history workflow: %w", err)
		}
		workflow.paths, workflow.ambiguous, workflow.empty = classifyOrphanHistoryWorkflow(payload)
		if workflow.empty && status != api.WorkflowStatusDraft && status != api.WorkflowStatusBlocked {
			workflow.ambiguous = true
		}
		workflows = append(workflows, workflow)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate orphaned history workflows: %w", err)
	}
	return workflows, nil
}

func classifyOrphanHistoryWorkflow(payload []byte) ([]string, bool, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil || raw == nil {
		return nil, true, false
	}
	var state storedReleaseWorkflowSource
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, true, false
	}
	paths := storedReleaseWorkflowSourcePaths(payload)
	if len(state.Releases) == 0 {
		if len(paths) > 0 {
			return paths, false, false
		}
		if orphanHistoryWorkflowHasAuthority(payload) {
			return nil, true, false
		}
		return nil, false, true
	}

	for _, release := range state.Releases {
		sourcePath := strings.TrimSpace(release.Release.Source.SourcePath)
		if sourcePath == "" {
			return paths, true, false
		}
	}
	return paths, false, false
}

func orphanHistoryWorkflowHasAuthority(payload []byte) bool {
	var authority orphanHistoryWorkflowAuthority
	if err := json.Unmarshal(payload, &authority); err != nil {
		return true
	}
	if orphanHistoryReleaseWorkflowHasAuthority(authority.Workflow) {
		return true
	}
	if len(authority.InputReadiness) > 0 || len(authority.Catalogs) > 0 || len(authority.Runtimes) > 0 ||
		len(authority.Selections) > 0 || len(authority.ProjectionInstructions) > 0 || len(authority.Projections) > 0 ||
		len(authority.Preflights) > 0 || len(authority.Dupes) > 0 || len(authority.TrackerApprovals) > 0 ||
		len(authority.Media) > 0 || len(authority.Descriptions) > 0 || len(authority.DryRuns) > 0 ||
		len(authority.UploadResults) > 0 {
		return true
	}
	for _, operation := range authority.Operations {
		if operation.Result != nil {
			return true
		}
	}
	for _, receipt := range authority.Receipts {
		if orphanHistoryCurrentHasAuthority(receipt.Result) {
			return true
		}
	}
	if composite := authority.Composite; composite != nil {
		return composite.ActiveOperationID != "" || composite.LastOperationID != "" || composite.ApprovedDryRun != nil ||
			composite.ApprovedFingerprint != "" ||
			len(composite.ApprovedTrackerIDs) > 0 || composite.FeedbackSequence != 0 || len(composite.FeedbackReceipts) > 0 ||
			composite.TerminalReason != ""
	}
	return false
}

func orphanHistoryReleaseWorkflowHasAuthority(workflow api.ReleaseWorkflow) bool {
	return workflow.Release != nil || workflow.InputReadiness != nil || workflow.TrackerCatalog != nil ||
		workflow.TrackerRuntime != nil || workflow.Selection != nil || workflow.ProjectionInstructions != nil ||
		workflow.TrackerProjections != nil || workflow.TrackerPreflight != nil || workflow.Dupes != nil ||
		workflow.TrackerApproval != nil || workflow.Media != nil || workflow.Descriptions != nil ||
		workflow.DryRun != nil || workflow.UploadResult != nil
}

func orphanHistoryCurrentHasAuthority(current api.ReleaseWorkflowCurrent) bool {
	if orphanHistoryReleaseWorkflowHasAuthority(current.Workflow) || current.Release != nil || current.InputReadiness != nil ||
		current.Catalog != nil || current.Runtime != nil || current.Selection != nil || current.ProjectionInstructions != nil ||
		current.Projections != nil || current.Preflight != nil || current.Dupes != nil || current.TrackerApproval != nil ||
		current.Media != nil || current.Descriptions != nil || current.DryRun != nil || current.UploadResult != nil {
		return true
	}
	refs := current.Continuation.Refs
	return refs.Release != nil || refs.Projections != nil || refs.Preflight != nil || refs.Dupes != nil ||
		refs.TrackerApproval != nil || refs.Media != nil || refs.Descriptions != nil || refs.DryRun != nil ||
		refs.UploadResult != nil || (current.Operation != nil && current.Operation.Result != nil) ||
		len(current.Continuation.TrackerOutcomes) > 0
}

func (r *SQLiteRepository) orphanHistoryWorkflowHasEffectsOrFences(ctx context.Context, scope api.WorkflowScope) (bool, error) {
	var exists bool
	err := r.historyQuery(ctx).QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM release_workflow_effects WHERE owner_id = ? AND workflow_id = ?
			UNION ALL
			SELECT 1 FROM submission_fences WHERE owner_id = ? AND workflow_id = ?
		)
	`, scope.OwnerID, scope.WorkflowID, scope.OwnerID, scope.WorkflowID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("db inspect orphaned history workflow effects: %w", err)
	}
	return exists, nil
}

func orphanHistoryWorkflowScopeProtected(scope api.WorkflowScope, protected map[api.WorkflowScope]struct{}) bool {
	_, ok := protected[scope]
	return ok
}

func addOrphanHistoryPaths(paths []string, additions []string) []string {
	for _, path := range additions {
		paths = addOrphanHistoryPath(paths, path)
	}
	return paths
}

func addOrphanHistoryPath(paths []string, path string) []string {
	path = strings.TrimSpace(path)
	if path == "" || slices.Contains(paths, path) {
		return paths
	}
	return append(paths, path)
}

func orphanHistoryPathRelated(path string, candidates []string) bool {
	for _, candidate := range candidates {
		if pathutil.SamePath(path, candidate) || pathutil.IsWithinRoot(path, candidate) || pathutil.IsWithinRoot(candidate, path) {
			return true
		}
	}
	return false
}

func orphanHistoryPathRelatedAny(paths, candidates []string) bool {
	for _, path := range paths {
		if orphanHistoryPathRelated(path, candidates) {
			return true
		}
	}
	return false
}
