// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type persistedDupeResourceForTest struct {
	kind    string
	payload []byte
}

func (r persistedDupeResourceForTest) MarshalPrivateResource() (string, []byte, error) {
	return r.kind, r.payload, nil
}

func TestWorkflowDupeRestartRequiresCurrentSearchContract(t *testing.T) {
	t.Parallel()

	for _, status := range []api.WorkflowStatus{api.WorkflowStatusActive, api.WorkflowStatusFailed} {
		for _, legacy := range []bool{true, false} {
			name := string(status) + "/current"
			if legacy {
				name = string(status) + "/legacy"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				now := time.Now().UTC()
				root := t.TempDir()
				databasePath := filepath.Join(root, "workflow.sqlite")
				vaultRoot := filepath.Join(root, "vault")
				ownerID := "dupe-recovery-owner"
				workflowID := api.WorkflowID("dupe-recovery-workflow")
				subject := api.DuplicateSubject{SourcePath: filepath.Join(root, "Example.Release.2026.1080p-GRP.mkv")}
				assessment := dupechecking.NewAssessment(subject, config.Config{}, []dupechecking.AssessmentEvidence{{
					Tracker: "SAM", Disposition: dupechecking.DispositionResolved,
				}})
				evidence := workflowDupePrivateEvidence{
					Summary: api.DupeCheckSummary{Results: []api.DupeCheckResult{{
						Tracker: "SAM",
						Status:  "completed",
						Search:  api.DupeSearchEvidence{Complete: true},
					}}},
					Assessment: &assessment,
				}
				kind, payload, err := evidence.MarshalPrivateResource()
				if err != nil {
					t.Fatal(err)
				}
				if legacy {
					// Pre-upgrade evidence has the same payload schema without a search-contract stamp.
					var stored map[string]json.RawMessage
					if err := json.Unmarshal(payload, &stored); err != nil {
						t.Fatal(err)
					}
					delete(stored, "searchContract")
					payload, err = json.Marshal(stored)
					if err != nil {
						t.Fatal(err)
					}
				}
				vaultA, err := releaseworkflow.NewPrivateArtifactVault(vaultRoot, workflowPrivateResourceCodecs(workflowMediaBuilder{}, workflowAudioAnalysisBuilder{})...)
				if err != nil {
					t.Fatal(err)
				}
				if err := vaultA.Put(ownerID, workflowID, "dupe:dupes-1", persistedDupeResourceForTest{kind: kind, payload: payload}, now.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				repoA, err := db.Open(databasePath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = repoA.Close() })
				if err := repoA.Migrate(); err != nil {
					t.Fatal(err)
				}
				persistentA, err := releaseworkflow.NewPersistentRepository(repoA)
				if err != nil {
					t.Fatal(err)
				}
				moduleA, err := releaseworkflow.New(persistentA, vaultA, releaseworkflow.ReleasePreparerFunc{}, releaseworkflow.WithProcessEpoch("before-upgrade"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = moduleA.Shutdown(context.Background()) })
				created, err := moduleA.Execute(t.Context(), ownerID, releaseworkflow.CreateWorkflowCommand{WorkflowID: workflowID})
				if err != nil {
					t.Fatal(err)
				}
				state, err := persistentA.Load(t.Context(), ownerID, workflowID)
				if err != nil {
					t.Fatal(err)
				}
				state.Workflow.Revision++
				state.Workflow.Status = status
				state.Workflow.UpdatedAt = time.Now().UTC()
				state.Workflow.Release = &api.ReleaseSnapshotRef{ID: "release-1", Revision: 1}
				state.Workflow.TrackerCatalog = &api.TrackerCatalogSnapshotRef{ID: "catalog-1", Revision: 1}
				state.Workflow.TrackerRuntime = &api.TrackerRuntimeSnapshotRef{ID: "runtime-1", Revision: 1}
				state.Workflow.Selection = &api.TrackerSelectionRef{ID: "selection-1", Revision: 1}
				state.Workflow.ProjectionInstructions = &api.TrackerProjectionInstructionSnapshotRef{ID: "instructions-1", Revision: 1}
				state.Workflow.TrackerProjections = &api.TrackerReleaseProjectionSetRef{ID: "projections-1", Revision: 1}
				state.Workflow.TrackerPreflight = &api.TrackerPreflightAssessmentRef{ID: "preflight-1", Revision: 1}
				state.Workflow.Dupes = &api.DupeAssessmentRef{ID: "dupes-1", Revision: 1}
				state.Releases["release-1"] = api.ReleaseSnapshot{ID: "release-1", Revision: 1}
				state.Selections["selection-1"] = api.TrackerSelection{
					ID:         "selection-1",
					Revision:   1,
					TrackerIDs: []api.TrackerID{"SAM"},
				}
				state.ProjectionInstructions["instructions-1"] = api.TrackerProjectionInstructionSnapshot{ID: "instructions-1", Revision: 1}
				state.Projections["projections-1"] = api.TrackerReleaseProjectionSet{
					ID:        "projections-1",
					Revision:  1,
					Preflight: state.Workflow.TrackerPreflight,
					Projections: []api.TrackerReleaseProjection{{
						TrackerID:   "SAM",
						Readiness:   api.ReadinessStatusReady,
						DupeReady:   true,
						UploadReady: true,
					}},
				}
				state.Preflights["preflight-1"] = api.TrackerPreflightAssessment{
					ID:        "preflight-1",
					Revision:  1,
					Status:    api.StageStatusReady,
					ExpiresAt: now.Add(time.Hour),
				}
				state.Dupes["dupes-1"] = api.DupeAssessment{
					ID:            "dupes-1",
					Revision:      1,
					ProjectionSet: *state.Workflow.TrackerProjections,
					Selection:     *state.Workflow.Selection,
					ExpiresAt:     now.Add(time.Hour),
					Results: []api.TrackerDupeAssessment{{
						TrackerID: "SAM",
						Decision:  api.DupeDecisionNoMatch,
						Status:    api.StageStatusCompleted,
					}},
				}
				if err := state.Workflow.Validate(); err != nil {
					t.Fatal(err)
				}
				if err := persistentA.Save(t.Context(), ownerID, created.Workflow.Revision, state); err != nil {
					t.Fatal(err)
				}
				if err := moduleA.Shutdown(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := repoA.Close(); err != nil {
					t.Fatal(err)
				}
				repoB, err := db.Open(databasePath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = repoB.Close() })
				persistentB, err := releaseworkflow.NewPersistentRepository(repoB)
				if err != nil {
					t.Fatal(err)
				}
				vaultB, err := releaseworkflow.NewPrivateArtifactVault(vaultRoot, workflowPrivateResourceCodecs(workflowMediaBuilder{}, workflowAudioAnalysisBuilder{})...)
				if err != nil {
					t.Fatal(err)
				}
				moduleB, err := releaseworkflow.New(persistentB, vaultB, releaseworkflow.ReleasePreparerFunc{}, releaseworkflow.WithProcessEpoch("after-upgrade"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = moduleB.Shutdown(context.Background()) })
				recovered, err := moduleB.Current(t.Context(), ownerID, workflowID)
				if err != nil {
					t.Fatal(err)
				}
				if legacy {
					if recovered.Projections != nil || recovered.Preflight != nil || recovered.Dupes != nil {
						t.Fatalf("legacy narrow search remains reusable after restart: %#v", recovered.Workflow)
					}
				} else if recovered.Projections == nil || recovered.Preflight == nil || recovered.Dupes == nil {
					t.Fatalf("current evidence was discarded: %#v", recovered.Workflow)
				}
				if !legacy && recovered.Workflow.Status != status {
					t.Fatalf("valid evidence changed workflow status: got %s, want %s", recovered.Workflow.Status, status)
				}
				if recovered.Release == nil || recovered.Selection == nil || recovered.ProjectionInstructions == nil {
					t.Fatal("recovery discarded prepared facts or tracker intent")
				}
				again, err := moduleB.Current(t.Context(), ownerID, workflowID)
				if err != nil || again.Workflow.Revision != recovered.Workflow.Revision {
					t.Fatalf("repeated recovery changed revision: %d, error=%v", again.Workflow.Revision, err)
				}
				if err := vaultB.CleanupExpired(now.Add(2 * time.Hour)); err != nil {
					t.Fatalf("expired evidence cleanup: %v", err)
				}
				if _, err := vaultB.Get(ownerID, workflowID, "dupe:dupes-1", now.Add(2*time.Hour)); !errors.Is(err, releaseworkflow.ErrPrivateResourceUnavailable) {
					t.Fatalf("expired evidence remains available: %v", err)
				}
			})
		}
	}
}
