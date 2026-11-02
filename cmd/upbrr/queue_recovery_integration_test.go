// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func newQueueRecoveryModule(t *testing.T, repo *db.SQLiteRepository) *releaseworkflow.Module {
	t.Helper()
	persistent, err := releaseworkflow.NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	module, err := releaseworkflow.New(persistent, releaseworkflow.NewMemoryPrivateResourceStore(), releaseworkflow.ReleasePreparerFunc{},
		releaseworkflow.WithActiveInputs(repo, func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
			payload, err := os.ReadFile(input.SourcePath)
			if err != nil {
				return api.InputRecord{}, fmt.Errorf("test source read: %w", err)
			}
			sum := sha256.Sum256(payload)
			digest := hex.EncodeToString(sum[:])
			manifest, err := json.Marshal(api.VerifiedInputSource{
				Manifest: api.SourceManifest{SourcePath: input.SourcePath},
				Identity: api.SourceContentIdentity{Digest: digest},
			})
			if err != nil {
				return api.InputRecord{}, fmt.Errorf("test source manifest: %w", err)
			}
			return api.InputRecord{
				CanonicalPath: input.SourcePath,
				SourceVersion: digest,
				Manifest:      manifest,
			}, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	return module
}

// This fixture uses real admission, effects, submission fences and queue files.
// The synthetic remote closes A's response after receiving its request; it does
// not exercise any tracker protocol adapter or live client.
func TestNamedQueueIsolatesUnknownSubmissionAcrossDatabaseRestart(t *testing.T) {
	ctx := t.Context()
	dbPath, root, paths := queueFixture(t)
	for i, path := range paths {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("synthetic distinct source %d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	requests := make(map[string]int)
	var acceptA atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/submit/a.mkv" && !acceptA.Load() {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("synthetic HTTP server cannot close the submission response")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			connection, _, err := hijacker.Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"accepted":true}`)
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	module := newQueueRecoveryModule(t, repo)
	queue, err := openCLINamedQueue(ctx, dbPath, "isolated", root, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = queue.Close()
		_ = module.Shutdown(context.Background())
		_ = repo.Close()
	}()
	restart := func() {
		t.Helper()
		if err := queue.Close(); err != nil {
			t.Fatal(err)
		}
		if err := module.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
		repo, err = db.OpenContext(ctx, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		module = newQueueRecoveryModule(t, repo)
		if err := module.ResetIdleInputOnStartup(ctx); err != nil {
			t.Fatal(err)
		}
		queue, err = openCLINamedQueue(ctx, dbPath, "isolated", root, false, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
	}
	var originalA api.ReleaseWorkflowEffectRecord
	identities := make(map[string]api.SubmissionContentIdentity)
	process := func(itemCtx context.Context, item cliPreparationItem) (outcome cliQueueOutcome, result error) {
		slot, err := repo.LoadActiveInput(itemCtx)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := module.OpenInput(itemCtx, cliWorkflowOwnerID, releaseworkflow.OpenInputRequest{
			ExpectedRevision: slot.Revision,
			Input:            api.PrepareInput{SourcePath: item.originalPath},
			IdempotencyKey:   fmt.Sprintf("open-%s-%d", filepath.Base(item.originalPath), slot.Revision),
		})
		if err != nil {
			return cliQueueOutcome{}, fmt.Errorf("test input open: %w", err)
		}
		defer func() {
			result = errors.Join(result, module.ReleaseInput(itemCtx, cliWorkflowOwnerID, opened.Revision))
		}()
		outcome.WorkflowID = opened.WorkflowID
		if item.originalPath == paths[2] {
			return outcome, errors.New("synthetic local validation failure before submission")
		}
		payload, err := os.ReadFile(item.originalPath)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(payload)
		identity, err := api.NewSubmissionContentIdentity(api.SubmissionContentScopeSingleFile, []api.SubmissionContentFile{{Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}})
		if err != nil {
			t.Fatal(err)
		}
		identities[item.originalPath] = identity
		now := time.Now().UTC()
		effect := api.ReleaseWorkflowEffectRecord{
			OwnerID:             cliWorkflowOwnerID,
			WorkflowID:          opened.WorkflowID,
			OperationID:         "submit",
			EffectID:            "submit-" + string(opened.WorkflowID),
			Kind:                string(api.WorkflowExternalEffectTrackerSubmission),
			ScopeID:             "ALPHA",
			SemanticFingerprint: "synthetic-submission",
			StartedAt:           now,
			UpdatedAt:           now,
			Submission: &api.SubmissionFenceAuthority{
				ContentIdentity: identity,
				TrackerSite:     "ALPHA|https://alpha.example/",
				CoordinatorID:   opened.CoordinatorID,
				Fence:           opened.Fence,
			},
		}
		authority := api.WithActiveInputAuthority(itemCtx, api.ActiveInputAuthority{CoordinatorID: opened.CoordinatorID, Fence: opened.Fence})
		effect, replay, err := repo.BeginReleaseWorkflowEffect(authority, effect)
		if err != nil || replay {
			t.Fatalf("fresh submission was not admitted exactly once: replay=%v err=%v", replay, err)
		}
		request, err := http.NewRequestWithContext(itemCtx, http.MethodPost, server.URL+"/submit/"+filepath.Base(item.originalPath), strings.NewReader(string(payload)))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/octet-stream")
		response, submissionErr := client.Do(request)
		if submissionErr != nil {
			if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(authority, cliWorkflowOwnerID, opened.WorkflowID, effect.OperationID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			originalA = effect
			session := &cliWorkflowSession{current: releaseworkflow.CommandResult{Workflow: api.ReleaseWorkflow{ID: opened.WorkflowID}, UploadResult: &api.UploadResult{Results: []api.UploadTrackerResult{{
				TrackerID:        "ALPHA",
				SubmissionStatus: api.StageStatusUnavailable,
				Failures:         []api.WorkflowFailure{{Failure: api.OperationFailure{Code: api.OperationFailureUnknownOutcome, Operation: api.OperationKindUploadExecute}}},
			}}}}}
			return cliQueueWorkflowOutcome(session, false), submissionErr
		}
		var receipt struct{ Accepted bool }
		decodeErr := json.NewDecoder(response.Body).Decode(&receipt)
		_ = response.Body.Close()
		if decodeErr != nil || !receipt.Accepted {
			t.Fatalf("invalid synthetic acceptance: receipt=%+v err=%v", receipt, decodeErr)
		}
		completed := time.Now().UTC()
		effect.UpdatedAt, effect.CompletedAt = completed, &completed
		if err := repo.CompleteReleaseWorkflowEffect(authority, api.WorkflowEffectStatusSucceeded, effect); err != nil {
			t.Fatal(err)
		}
		request, err = http.NewRequestWithContext(itemCtx, http.MethodPost, server.URL+"/inject/"+filepath.Base(item.originalPath), strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		injection, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = injection.Body.Close()
		return cliQueueOutcome{
			Status:           "completed",
			WorkflowID:       opened.WorkflowID,
			ConfirmedUploads: []api.TrackerID{"ALPHA"},
		}, nil
	}
	run := func(selected []string) error {
		batch := newCLIPreparationBatch(api.Request{}, selected)
		batch.queue = queue
		return processCLIPreparationItems(ctx, batch, true, time.Minute, cliIO{}, api.NopLogger{}, process)
	}
	if err := run(queue.paths(false, 1)); err == nil || originalA.WorkflowID == "" {
		t.Fatalf("A did not retain genuine uncertainty: err=%v effect=%+v", err, originalA)
	}
	restart()
	if got := queue.paths(false, 0); !reflect.DeepEqual(got, paths[1:]) {
		t.Fatalf("restart selected A or omitted pending work: %v", got)
	}
	if err := run(queue.paths(false, 0)); err == nil || !strings.Contains(err.Error(), "1 of 3") {
		t.Fatalf("B/C/D queue result = %v", err)
	}
	restart()
	for i, status := range []string{"interrupted", "completed", "failed", "completed"} {
		if item := queue.inventory.Items[i]; item.Status != status || item.Attempts != 1 || (i == 0 && len(item.ConfirmedUploads) != 0) {
			t.Fatalf("persisted item %d = %+v, want %s", i, item, status)
		}
	}
	fence, err := repo.LoadSubmissionFence(ctx, identities[paths[0]], originalA.Submission.TrackerSite)
	if err != nil || fence.Status != api.WorkflowEffectStatusUnknown || fence.WorkflowID != originalA.WorkflowID || fence.OperationID != originalA.OperationID || fence.EffectID != originalA.EffectID || fence.ConfirmedAt != nil {
		t.Fatalf("later items replaced A authority: fence=%+v err=%v", fence, err)
	}
	closed, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.OpenInput(ctx, cliWorkflowOwnerID, releaseworkflow.OpenInputRequest{
		ExpectedRevision: closed.Revision,
		Input:            api.PrepareInput{SourcePath: paths[0]},
		IdempotencyKey:   "blocked-A-retry",
	}); !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
		t.Fatalf("A reopened without reconciliation: %v", err)
	}
	if _, err := module.RecoverLegacyInput(ctx, cliWorkflowOwnerID, originalA.WorkflowID); err != nil {
		t.Fatal(err)
	}
	current, err := module.Current(ctx, cliWorkflowOwnerID, originalA.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	action := pendingCLIWorkflowAction(current.Workflow.RequiredActions, api.RequiredActionReconcileSubmission)
	if action == nil || action.TrackerID != "ALPHA" || action.EffectScopeID != originalA.ScopeID {
		t.Fatalf("wrong A recovery action: %+v", action)
	}
	if _, err := module.Execute(ctx, cliWorkflowOwnerID, releaseworkflow.ResolveActionCommand{
		WorkflowID:       originalA.WorkflowID,
		ExpectedRevision: current.Workflow.Revision,
		IdempotencyKey:   "confirm-A-not-completed",
		Answer: api.RequiredActionAnswer{
			ActionID:         action.ID,
			WorkflowRevision: current.Workflow.Revision,
			SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(queue.paths(false, 0)) != 0 {
		t.Fatal("reconciliation automatically queued another submission")
	}
	acceptA.Store(true)
	if err := run(queue.paths(true, 1)); err != nil {
		t.Fatalf("explicit reconciled A retry failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := map[string]int{
		"/submit/a.mkv": 2,
		"/submit/b.mkv": 1,
		"/submit/d.mkv": 1,
		"/inject/a.mkv": 1,
		"/inject/b.mkv": 1,
		"/inject/d.mkv": 1,
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("unexpected submission/injection replay: got=%v want=%v", requests, want)
	}
}
