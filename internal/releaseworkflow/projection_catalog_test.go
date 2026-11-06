// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCatalogRefreshPreservesExternalOutcomeAuthority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		result *api.UploadResultRef
		effect api.WorkflowExternalEffectKind
	}{
		{name: "retained upload result", result: &api.UploadResultRef{ID: "upload", Revision: 1}},
		{
			name:   "uncertain tracker submission",
			result: &api.UploadResultRef{ID: "upload", Revision: 1},
			effect: api.WorkflowExternalEffectTrackerSubmission,
		},
		{name: "uncertain image hosting", effect: api.WorkflowExternalEffectImageHosting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			module := Module{trackerProjector: upgradedCatalogProjector{stale: "old-catalog"}}
			current := CommandResult{
				Workflow:    api.ReleaseWorkflow{Status: api.WorkflowStatusBlocked, UploadResult: tc.result},
				Release:     &api.ReleaseSnapshot{},
				Catalog:     &api.TrackerCatalogSnapshot{ID: "old-catalog"},
				Projections: &api.TrackerReleaseProjectionSet{ID: "retained-projections"},
			}
			if tc.effect != "" {
				current.Workflow.RequiredActions = []api.RequiredAction{{
					Kind:       api.RequiredActionReconcileSubmission,
					Status:     api.RequiredActionStatusPending,
					EffectKind: tc.effect,
				}}
			}
			command, err := module.planCatalogRefresh(api.ContinueReleaseWorkflowRequest{Goal: api.WorkflowGoalUploaded}, current, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if command != nil {
				t.Fatalf("catalog refresh would replace external outcome authority: %#v", command)
			}
		})
	}
}
