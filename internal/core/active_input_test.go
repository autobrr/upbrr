// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestVerifyWorkflowInputReportsBoundedSanitizedSourceProgress(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "release.mkv")
	content := strings.Repeat("verified-content", 128*1024)
	if err := os.WriteFile(sourcePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var updates []api.PreparationProgressUpdate
	ctx := api.WithPreparationProgressReporter(t.Context(), func(update api.PreparationProgressUpdate) {
		updates = append(updates, update)
	})
	record, err := verifyWorkflowInput(ctx, api.PrepareInput{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("verify workflow input: %v", err)
	}
	if record.SourceVersion == "" || len(record.Manifest) == 0 {
		t.Fatalf("verified input record = %#v", record)
	}
	if len(updates) < 3 || len(updates) > 24 {
		t.Fatalf("source inspection updates = %d, want bounded progress", len(updates))
	}
	if updates[0].Phase != api.PreparationPhaseSourceInspection || updates[0].Status != api.PreparationProgressRunning {
		t.Fatalf("source inspection start = %#v", updates[0])
	}
	if last := updates[len(updates)-1]; last.Status != api.PreparationProgressCompleted {
		t.Fatalf("source inspection completion = %#v", last)
	}
	var progress api.PreparationProgressUpdate
	for _, update := range updates {
		if update.CompletedBytes > 0 {
			progress = update
		}
		if strings.Contains(update.Message, sourcePath) || strings.Contains(update.Label, sourcePath) {
			t.Fatalf("source path leaked into progress update: %#v", update)
		}
	}
	if progress.CompletedBytes != api.SourceContentIdentitySampleBytes || progress.TotalBytes != api.SourceContentIdentitySampleBytes {
		t.Fatalf("source verification byte progress = %#v", progress)
	}
}

func TestVerifyWorkflowInputReportsCancellation(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "release.mkv")
	if err := os.WriteFile(sourcePath, []byte(strings.Repeat("cancelled-content", 128*1024)), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var updates []api.PreparationProgressUpdate
	ctx = api.WithPreparationProgressReporter(ctx, func(update api.PreparationProgressUpdate) {
		updates = append(updates, update)
		if update.CompletedBytes > 0 {
			cancel()
		}
	})

	_, err := verifyWorkflowInput(ctx, api.PrepareInput{SourcePath: sourcePath})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("verify canceled source error = %v", err)
	}
	if len(updates) == 0 || updates[len(updates)-1].Status != api.PreparationProgressFailed {
		t.Fatalf("canceled source updates = %#v", updates)
	}
}

func TestVerifyWorkflowInputUsesPreparedReleaseCanonicalSourceKeyOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path case normalization")
	}

	sourcePath := filepath.Join(t.TempDir(), "release.mkv")
	if err := os.WriteFile(sourcePath, []byte("verified-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := db.Open(filepath.Join(t.TempDir(), "input.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	first, err := verifyWorkflowInput(t.Context(), api.PrepareInput{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("verify lower-case input: %v", err)
	}
	first.ID, first.UpdatedAt = "first-input", time.Now().UTC()
	first, err = repository.SaveInputRecord(t.Context(), first)
	if err != nil {
		t.Fatalf("save lower-case input: %v", err)
	}
	second, err := verifyWorkflowInput(t.Context(), api.PrepareInput{SourcePath: strings.ToUpper(sourcePath)})
	if err != nil {
		t.Fatalf("verify upper-case input: %v", err)
	}
	second.ID, second.UpdatedAt = "second-input", time.Now().UTC()
	second, err = repository.SaveInputRecord(t.Context(), second)
	if err != nil {
		t.Fatalf("save upper-case input: %v", err)
	}
	if second.ID != first.ID || second.CanonicalPath != first.CanonicalPath || second.SourceVersion != first.SourceVersion {
		t.Fatalf("case variants saved distinct inputs: first=%#v second=%#v", first, second)
	}
}

func TestOpenActiveInputRequestsExternalProviderRefresh(t *testing.T) {
	repo, err := db.Open(filepath.Join(t.TempDir(), "input.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := releaseworkflow.NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	var verified []api.PrepareInput
	workflow, err := releaseworkflow.New(
		persistent,
		releaseworkflow.NewMemoryPrivateResourceStore(),
		releaseworkflow.ReleasePreparerFunc{PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			return api.PrepareResult{Release: api.PreparedRelease{
				Generation: 1,
				Source:     api.SourceManifest{SourcePath: input.SourcePath},
			}}, nil
		}},
		releaseworkflow.WithActiveInputs(repo, func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
			verified = append(verified, input)
			return api.InputRecord{
				CanonicalPath: input.SourcePath,
				SourceVersion: "verified",
				Manifest:      []byte(`{"identity":{"digest":"verified"}}`),
			}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	core := &Core{workflow: workflow, logger: api.NopLogger{}}
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	clearedMALID := 0
	opened, err := core.OpenActiveInput(t.Context(), "active-input-owner", api.OpenActiveInputRequest{
		Request: api.ContinueReleaseWorkflowRequest{
			IdempotencyKey: "open-refresh",
			Goal:           api.WorkflowGoalPrepared,
			Intent: api.WorkflowIntent{Preparation: &api.PrepareInput{
				SourcePath: sourcePath,
			}},
		},
	})
	if err != nil {
		t.Fatalf("open active input: %v", err)
	}
	t.Cleanup(func() {
		_, _ = core.ReleaseActiveInput(context.Background(), "active-input-owner", api.ReleaseActiveInputRequest{
			ExpectedRevision: opened.Revision,
		})
	})
	if len(verified) != 1 || verified[0].ExternalFreshness != api.ExternalFreshnessRefresh {
		t.Fatalf("verified preparation inputs = %#v", verified)
	}
	refreshed, err := core.OpenActiveInput(t.Context(), "active-input-owner", api.OpenActiveInputRequest{
		ExpectedRevision: opened.Revision,
		Request: api.ContinueReleaseWorkflowRequest{
			IdempotencyKey: "open-refresh-again",
			Goal:           api.WorkflowGoalPrepared,
			Intent: api.WorkflowIntent{
				CorrectionPatch: &api.ReleaseCorrectionPatch{
					ExpectedRevision: new(uint64),
					Values:           api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{MALID: &clearedMALID}},
				},
				Preparation: &api.PrepareInput{SourcePath: sourcePath},
			},
		},
	})
	if err != nil {
		t.Fatalf("refresh active input: %v", err)
	}
	if refreshed.Revision <= opened.Revision || len(verified) != 2 ||
		verified[1].ExternalFreshness != api.ExternalFreshnessRefresh {
		t.Fatalf("refreshed active input = %#v, verified preparations = %#v", refreshed, verified)
	}
}

func TestLegacyActiveInputRecoveryFlowsFromDiscoveryToEmpty(t *testing.T) {
	repo, err := db.Open(filepath.Join(t.TempDir(), "legacy-recovery.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := releaseworkflow.NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := releaseworkflow.New(
		persistent,
		releaseworkflow.NewMemoryPrivateResourceStore(),
		releaseworkflow.ReleasePreparerFunc{},
		releaseworkflow.WithActiveInputs(repo, func(context.Context, api.PrepareInput) (api.InputRecord, error) {
			return api.InputRecord{}, nil
		}),
		releaseworkflow.WithProcessEpoch("core-legacy-recovery"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workflow.Shutdown(context.Background()) })
	core := &Core{workflow: workflow, logger: api.NopLogger{}}
	created, err := workflow.Execute(t.Context(), "legacy-owner", releaseworkflow.CreateWorkflowCommand{IdempotencyKey: "legacy-workflow"})
	if err != nil {
		t.Fatalf("create legacy workflow: %v", err)
	}
	now := time.Now().UTC()
	if _, _, err := persistent.BeginEffect(t.Context(), api.ReleaseWorkflowEffectRecord{
		OwnerID:             "legacy-owner",
		WorkflowID:          created.Workflow.ID,
		OperationID:         "legacy-operation",
		EffectID:            "legacy-effect",
		Kind:                string(api.WorkflowExternalEffectTrackerSubmission),
		ScopeID:             "PTP",
		SemanticFingerprint: "legacy-submission",
		StartedAt:           now,
		UpdatedAt:           now,
	}); err != nil {
		t.Fatalf("begin legacy effect: %v", err)
	}

	discovered, err := core.GetActiveInput(t.Context(), "legacy-owner")
	if err != nil {
		t.Fatalf("discover legacy recovery: %v", err)
	}
	if discovered.State != api.ActiveInputEmpty || len(discovered.RecoveryWorkflowIDs) != 1 ||
		discovered.RecoveryWorkflowIDs[0] != created.Workflow.ID {
		t.Fatalf("legacy discovery = %#v", discovered)
	}
	if foreign, err := core.GetActiveInput(t.Context(), "other-owner"); err != nil || len(foreign.RecoveryWorkflowIDs) != 0 {
		t.Fatalf("foreign legacy discovery = %#v, %v", foreign, err)
	}

	recovered, err := core.RecoverLegacyActiveInput(t.Context(), "legacy-owner", api.RecoverLegacyActiveInputRequest{WorkflowID: created.Workflow.ID})
	if err != nil {
		t.Fatalf("recover legacy input: %v", err)
	}
	if recovered.State != api.ActiveInputRecovering || recovered.Current == nil || len(recovered.Current.Workflow.RequiredActions) != 1 {
		t.Fatalf("recovered legacy input = %#v", recovered)
	}
	restartedWorkflow, err := releaseworkflow.New(
		persistent,
		releaseworkflow.NewMemoryPrivateResourceStore(),
		releaseworkflow.ReleasePreparerFunc{},
		releaseworkflow.WithActiveInputs(repo, func(context.Context, api.PrepareInput) (api.InputRecord, error) {
			return api.InputRecord{}, nil
		}),
		releaseworkflow.WithProcessEpoch("core-legacy-restarted"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restartedWorkflow.Shutdown(context.Background()) })
	restartedCore := &Core{workflow: restartedWorkflow, logger: api.NopLogger{}}
	restartedView, err := restartedCore.GetActiveInput(t.Context(), "legacy-owner")
	if err != nil || restartedView.State != api.ActiveInputRecovering || restartedView.Current != nil ||
		!slices.Equal(restartedView.RecoveryWorkflowIDs, []api.WorkflowID{created.Workflow.ID}) {
		t.Fatalf("restarted legacy recovery = %#v, err=%v", restartedView, err)
	}
	reentered, err := core.RecoverLegacyActiveInput(t.Context(), "legacy-owner", api.RecoverLegacyActiveInputRequest{WorkflowID: created.Workflow.ID})
	if err != nil || reentered.State != api.ActiveInputRecovering || reentered.Current == nil {
		t.Fatalf("reenter legacy recovery = %#v, %v", reentered, err)
	}
	action := recovered.Current.Workflow.RequiredActions[0]
	resolved, err := core.ReconcileActiveInput(t.Context(), "legacy-owner", api.ReconcileActiveInputRequest{
		Authority: api.WorkflowAuthority{WorkflowID: created.Workflow.ID, ExpectedRevision: recovered.Current.Workflow.Revision},
		Answer: api.RequiredActionAnswer{
			ActionID:         action.ID,
			WorkflowRevision: recovered.Current.Workflow.Revision,
			SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
		},
		IdempotencyKey: "resolve-legacy-effect",
	})
	if err != nil {
		t.Fatalf("reconcile legacy input: %v", err)
	}
	if resolved.State != api.ActiveInputEmpty || len(resolved.RecoveryWorkflowIDs) != 0 {
		t.Fatalf("resolved legacy input = %#v", resolved)
	}
}

func TestPreviousProcessInputDoesNotAdvertiseLegacyRecovery(t *testing.T) {
	repo, err := db.Open(filepath.Join(t.TempDir(), "previous-input.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := releaseworkflow.NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	verifier := func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
		return api.InputRecord{
			CanonicalPath: input.SourcePath,
			SourceVersion: "verified",
			Manifest:      []byte(`{}`),
		}, nil
	}
	previous, err := releaseworkflow.New(
		persistent, releaseworkflow.NewMemoryPrivateResourceStore(), releaseworkflow.ReleasePreparerFunc{},
		releaseworkflow.WithActiveInputs(repo, verifier), releaseworkflow.WithProcessEpoch("previous-process"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = previous.Shutdown(context.Background()) })
	opened, err := previous.OpenInput(t.Context(), "cli", releaseworkflow.OpenInputRequest{
		Input: api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026-GRP.mkv")}, IdempotencyKey: "previous-open",
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := releaseworkflow.New(
		persistent, releaseworkflow.NewMemoryPrivateResourceStore(), releaseworkflow.ReleasePreparerFunc{},
		releaseworkflow.WithActiveInputs(repo, verifier), releaseworkflow.WithProcessEpoch("restarted-process"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Shutdown(context.Background()) })
	view, err := (&Core{workflow: restarted}).GetActiveInput(t.Context(), "cli")
	if err != nil || view.State != api.ActiveInputRecovering || view.Revision != opened.Revision ||
		view.Current != nil || len(view.RecoveryWorkflowIDs) != 0 {
		t.Fatalf("previous-process input = %#v, err=%v", view, err)
	}
}
