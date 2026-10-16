// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// persistActiveInputTestRelease gives fake preparation the same durable generation
// authority required by active-input finalization in production.
func persistActiveInputTestRelease(ctx context.Context, repo *db.SQLiteRepository, result api.PrepareResult) (api.PrepareResult, error) {
	release := &result.Release
	release.Compatibility = api.PreparationCompatibility{
		SourceFingerprint:          "test-source",
		FactInstructionFingerprint: "test-facts",
		PolicyFingerprint:          "test-policy",
		ContractVersion:            "test-contract",
	}
	release.Identity.SourcePath, release.Identity.Generation = release.Source.SourcePath, release.Generation
	release.Identity.Category, release.Identity.Conflict = api.CanonicalCategoryMovie, api.IdentityConflictNone
	release.ProviderMetadata.SourcePath, release.ProviderMetadata.Generation = release.Source.SourcePath, release.Generation
	release.Assessments = api.ReleaseAssessments{
		MediaInfoUniqueID:       api.UniqueIDStatusPresent,
		MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent,

		Naming: api.NamingAssessment{Status: api.NamingStatusComplete},
	}
	if err := repo.CommitPreparedRelease(ctx, result.Release); err != nil {
		return result, fmt.Errorf("persist active input test release: %w", err)
	}
	return result, nil
}

func TestInputSourcePathPreservesVerifiedCasingAndRejectsDetachedAuthority(t *testing.T) {
	repo, err := db.Open(filepath.Join(t.TempDir(), "source-path.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "Synthetic.HDR.2026.mkv")
	canonical := source
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(source)
	}
	verified := api.VerifiedInputSource{Manifest: api.SourceManifest{SourcePath: source}, Identity: api.SourceContentIdentity{Digest: "version"}}
	manifest, err := json.Marshal(verified)
	if err != nil {
		t.Fatal(err)
	}
	record, err := repo.SaveInputRecord(t.Context(), api.InputRecord{
		ID:            "verified-source",
		CanonicalPath: canonical,
		SourceVersion: "version",
		Manifest:      manifest,
		UpdatedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	module := &Module{activeInputs: repo}
	path, err := module.InputSourcePath(t.Context(), record.ID)
	if err != nil || path != source {
		t.Fatalf("verified display path=%q err=%v", path, err)
	}
	verified.Identity.Digest = "other-version"
	manifest, err = json.Marshal(verified)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveInputRecord(t.Context(), api.InputRecord{
		ID:            record.ID,
		CanonicalPath: canonical,
		SourceVersion: "version",
		Manifest:      manifest,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := module.InputSourcePath(t.Context(), record.ID); !errors.Is(err, api.ErrActiveInputChanged) {
		t.Fatalf("detached input authority accepted: %v", err)
	}
}

func TestActiveInputOpenRefreshRollbackAndOwnerIsolation(t *testing.T) {
	t.Parallel()
	repo, err := db.Open(filepath.Join(t.TempDir(), "input.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	verifications := 0
	fail := false
	module, err := New(persistent, NewMemoryPrivateResourceStore(), ReleasePreparerFunc{}, WithActiveInputs(repo,
		func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
			verifications++
			if fail {
				return api.InputRecord{}, errors.New("synthetic source failure")
			}
			return api.InputRecord{
				CanonicalPath: input.SourcePath,
				SourceVersion: "verified",
				Manifest:      []byte(`{}`),
			}, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		module.activeMu.Lock()
		cancel, done := module.activeCancel, module.activeDone
		module.activeMu.Unlock()
		if cancel != nil {
			cancel()
			<-done
		}
	})
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	request := OpenInputRequest{Input: api.PrepareInput{SourcePath: sourcePath}, IdempotencyKey: "first"}
	first, err := module.OpenInput(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != api.ActiveInputActive || first.WorkflowID == "" || verifications != 1 {
		t.Fatalf("open = %#v, calls %d", first, verifications)
	}
	if _, err := module.ActiveInput(t.Context(), "foreign"); !errors.Is(err, api.ErrActiveInputBusy) {
		t.Fatalf("foreign read = %v", err)
	}
	if _, err := module.OpenInput(t.Context(), testOwnerID, request); err != nil || verifications != 1 {
		t.Fatalf("idempotent open = %v, calls %d", err, verifications)
	}
	request.ExpectedRevision, request.IdempotencyKey = first.Revision, "refresh"
	refreshed, err := module.OpenInput(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.InputID != first.InputID || refreshed.WorkflowID != first.WorkflowID || verifications != 2 {
		t.Fatalf("refresh = %#v, calls %d", refreshed, verifications)
	}
	fail = true
	request.ExpectedRevision, request.IdempotencyKey = refreshed.Revision, "failed-switch"
	request.Input.SourcePath = filepath.Join(t.TempDir(), "other.mkv")
	if _, err := module.OpenInput(t.Context(), testOwnerID, request); err == nil {
		t.Fatal("failed verification accepted")
	}
	restored, err := module.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.InputID != refreshed.InputID || restored.WorkflowID != refreshed.WorkflowID || restored.State != api.ActiveInputActive {
		t.Fatalf("rollback = %#v", restored)
	}
	if err := module.ReleaseInput(t.Context(), testOwnerID, restored.Revision); err != nil {
		t.Fatal(err)
	}
	state, err := persistent.Load(t.Context(), testOwnerID, refreshed.WorkflowID)
	if err != nil {
		t.Fatalf("load released workflow: %v", err)
	}
	if state.SourcePath != sourcePath {
		t.Fatalf("released workflow source path = %q, want %q", state.SourcePath, sourcePath)
	}
}

func TestRetainActiveInputRequiresUnchangedCurrentAuthority(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"unchanged", "changed source", "changed facts", "changed corrections", "correction patch", "unchanged correction patch", "correction confirmation", "force", "provider refresh",
		"provider load", "client recheck", "HDR capture", "BDMV rescan", "expired duplicates", "missing evidence", "stale generation", "successful upload",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			preparer := testPreparer()
			preparer.CorrectionsCurrentFunc = func(context.Context, string, uint64) (bool, error) {
				return name != "changed corrections", nil
			}
			preparer.CorrectionsUnchangedFunc = func(context.Context, api.PrepareInput, api.ReleaseCorrectionUpdate, uint64) (bool, error) {
				return name == "unchanged correction patch" || name == "correction confirmation", nil
			}
			module, repository := newTestModule(t, preparer)
			input := api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "source.mkv")}
			created := executeCommand(t, module, CreateWorkflowCommand{SourcePath: input.SourcePath})
			prepared := executeCommand(t, module, PrepareReleaseCommand{

				WorkflowID:       created.Workflow.ID,
				ExpectedRevision: created.Workflow.Revision,
				Input:            input,
			})
			state, err := repository.Load(t.Context(), testOwnerID, prepared.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			dupes := api.DupeAssessment{
				ID:        "dupes-current",
				Revision:  state.Workflow.Revision,
				ExpiresAt: module.clock.Now().Add(time.Hour),

				Results: []api.TrackerDupeAssessment{{TrackerID: "ALPHA", Decision: api.DupeDecisionIgnored}},
			}
			state.Dupes[dupes.ID] = dupes
			state.Workflow.Dupes = &api.DupeAssessmentRef{ID: dupes.ID, Revision: dupes.Revision}
			if name != "missing evidence" {
				if err := module.private.Put(testOwnerID, state.Workflow.ID, dupePrivateResourceID(dupes.ID), "evidence", dupes.ExpiresAt); err != nil {
					t.Fatal(err)
				}
			}
			prior := api.ActiveInputRecord{SourceVersion: "verified"}
			record := api.InputRecord{SourceVersion: "verified"}
			request := OpenInputRequest{Input: input}
			switch name {
			case "changed source":
				record.SourceVersion = "changed"
			case "changed facts":
				request.Input.Instructions.Identity.TMDBID = new(123)
			case "correction patch", "unchanged correction patch":
				request.CorrectionPatch = &api.ReleaseCorrectionPatch{}
			case "correction confirmation":
				request.CorrectionPatch = &api.ReleaseCorrectionPatch{ConfirmFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameTag}}}
			case "force":
				request.Input.Force = true
			case "provider refresh":
				request.Input.ExternalFreshness = api.ExternalFreshnessRefresh
			case "provider load":
				request.Input.ExternalFreshness = api.ExternalFreshnessLoad
			case "client recheck":
				request.Input.Controls.ForceRecheck = new(true)
			case "HDR capture":
				request.Input.Controls.CaptureHDRMetadata = true
			case "BDMV rescan":
				request.Input.Controls.ConfirmBDMVRescan = true
			case "expired duplicates":
				dupes.ExpiresAt = module.clock.Now()
				state.Dupes[dupes.ID] = dupes
			case "stale generation":
				preparer.DisplayFunc = func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
					return api.PreparedReleaseDisplay{}, errors.New("stale generation")
				}
				module.preparer = preparer
			case "successful upload":
				state.Workflow.UploadResult = &api.UploadResultRef{ID: "uploaded", Revision: state.Workflow.Revision}
				state.Workflow.SubmissionExclusions = []api.SubmissionExclusion{{TrackerID: "ALPHA", Reason: "already_uploaded"}}
			}
			retained, err := module.canRetainActiveInput(t.Context(), testOwnerID, state, prior, record, request)
			want := name == "unchanged" || name == "successful upload" || name == "unchanged correction patch"
			if err != nil || retained != want {
				t.Fatalf("retained=%t want=%t err=%v", retained, want, err)
			}
		})
	}
}

func TestReopenVerifiedInputRestoresAudioAnalysis(t *testing.T) {
	t.Parallel()
	repo, err := db.Open(filepath.Join(t.TempDir(), "input.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	version := "verified-a"
	resource := &audioAnalysisReleaseProbe{}
	preparer := audioAnalysisPreparerForTest()
	prepare := preparer.PrepareFunc
	var generation api.PreparedGeneration
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		result, err := prepare(ctx, input)
		if err != nil {
			return result, err
		}
		generation++
		result.Release.Generation = generation
		return persistActiveInputTestRelease(ctx, repo, result)
	}
	module, err := New(persistent, NewMemoryPrivateResourceStore(), preparer,
		WithAudioAnalysisBuilder(&audioAnalysisBuilderFake{resource: resource}),
		WithActiveInputs(repo, func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
			return api.InputRecord{
				CanonicalPath: input.SourcePath,
				SourceVersion: version,
				Manifest:      []byte(`{"Identity":{"Digest":"` + version + `"}}`),
			}, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		module.activeMu.Lock()
		cancel, done := module.activeCancel, module.activeDone
		module.activeMu.Unlock()
		if cancel != nil {
			cancel()
			<-done
		}
	})
	input := api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.mkv")}
	first, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{Input: input, IdempotencyKey: "open-first"})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := module.Execute(t.Context(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       first.WorkflowID,
		ExpectedRevision: 1,
		Input:            input,
		IdempotencyKey:   "prepare-first",
	})
	if err != nil {
		t.Fatal(err)
	}
	analyzed, err := module.Execute(t.Context(), testOwnerID, AnalyzeAudioCommand{
		WorkflowID:       first.WorkflowID,
		ExpectedRevision: prepared.Workflow.Revision,
		Instructions:     audioAnalysisInstructionsForTest(*prepared.Release),
		IdempotencyKey:   "analyze-first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := module.ReleaseInput(t.Context(), testOwnerID, first.Revision); err != nil {
		t.Fatal(err)
	}
	empty, err := module.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := module.OpenInput(t.Context(), "other-owner", OpenInputRequest{
		ExpectedRevision: empty.Revision,
		Input:            input,
		IdempotencyKey:   "open-other-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := module.ReleaseInput(t.Context(), "other-owner", foreign.Revision); err != nil {
		t.Fatal(err)
	}
	empty, err = module.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		ExpectedRevision: empty.Revision,
		Input:            input,
		IdempotencyKey:   "open-again",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.WorkflowID == first.WorkflowID {
		t.Fatal("reopening replaced the prior workflow history")
	}
	current, err := module.Current(t.Context(), testOwnerID, reopened.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	if current.AudioAnalysis != nil || current.Workflow.AudioAnalysis != nil {
		t.Fatal("audio analysis was exposed before re-preparation")
	}
	preparedAgain, err := module.Execute(t.Context(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       reopened.WorkflowID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            input,
		IdempotencyKey:   "prepare-again",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preparedAgain.Workflow.AudioAnalysis == nil || !preparedAgain.Workflow.AudioAnalysisEnabled ||
		preparedAgain.AudioAnalysis == nil || preparedAgain.AudioAnalysis.WorkflowID != reopened.WorkflowID ||
		preparedAgain.AudioAnalysis.Release.Generation <= analyzed.AudioAnalysis.Release.Generation ||
		preparedAgain.AudioAnalysis.Release.Generation != preparedAgain.Release.Release.Generation {
		t.Fatalf("reopened audio analysis = %#v", preparedAgain.Workflow.AudioAnalysis)
	}
	content, err := module.AudioAnalysisArtifact(t.Context(), testOwnerID, reopened.WorkflowID,
		*preparedAgain.Workflow.AudioAnalysis, preparedAgain.AudioAnalysis.Tracks[0].Artifacts[0].ID)
	if err != nil {
		t.Fatalf("open restored audio artifact: %v", err)
	}
	if err := content.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resource.releaseCount() != 0 {
		t.Fatal("reopening the same source released its audio artifact")
	}
	otherInput := api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Other.Release.2026.mkv")}
	other, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		ExpectedRevision: reopened.Revision,
		Input:            otherInput,
		IdempotencyKey:   "open-other",
	})
	if err != nil {
		t.Fatal(err)
	}
	switchedBack, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		ExpectedRevision: other.Revision,
		Input:            input,
		IdempotencyKey:   "switch-back",
	})
	if err != nil {
		t.Fatal(err)
	}
	if switchedBack.WorkflowID == first.WorkflowID || switchedBack.WorkflowID == reopened.WorkflowID {
		t.Fatalf("switch-back replaced prior workflow history: %s", switchedBack.WorkflowID)
	}
	current, err = module.Current(t.Context(), testOwnerID, switchedBack.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	preparedAfterSwitch, err := module.Execute(t.Context(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       switchedBack.WorkflowID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            input,
		IdempotencyKey:   "prepare-after-switch",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preparedAfterSwitch.Workflow.AudioAnalysis == nil || preparedAfterSwitch.AudioAnalysis == nil ||
		preparedAfterSwitch.AudioAnalysis.WorkflowID != switchedBack.WorkflowID {
		t.Fatalf("switched-back audio analysis = %#v", preparedAfterSwitch.Workflow.AudioAnalysis)
	}
	if err := module.ReleaseInput(t.Context(), testOwnerID, switchedBack.Revision); err != nil {
		t.Fatal(err)
	}
	legacyState, err := persistent.Load(t.Context(), testOwnerID, switchedBack.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	legacyState.Workflow.Revision++
	legacyState.Workflow.Release = nil
	legacyState.Workflow.AudioAnalysis = nil
	legacyState.Workflow.AudioAnalysisEnabled = false
	legacyRecord, err := workflowStateRecord(testOwnerID, legacyState)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE release_workflow_states
		SET revision = ?, state_json = ? WHERE owner_id = ? AND workflow_id = ?`,
		legacyRecord.Revision, legacyRecord.Payload, testOwnerID, switchedBack.WorkflowID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE input_workflow_associations
		SET audio_analysis_id = ? WHERE canonical_path = ? AND owner_id = ?`,
		preparedAfterSwitch.AudioAnalysis.ID, input.SourcePath, testOwnerID); err != nil {
		t.Fatal(err)
	}
	empty, err = module.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	legacyOpen, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		ExpectedRevision: empty.Revision,
		Input:            input,
		IdempotencyKey:   "open-legacy-refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyCurrent, err := module.Current(t.Context(), testOwnerID, legacyOpen.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	legacyPrepared, err := module.Execute(t.Context(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       legacyOpen.WorkflowID,
		ExpectedRevision: legacyCurrent.Workflow.Revision,
		Input:            input,
		IdempotencyKey:   "prepare-legacy-refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if legacyPrepared.AudioAnalysis == nil || legacyPrepared.Workflow.AudioAnalysis == nil {
		t.Fatal("migrated cleared audio reference was not restored")
	}
	if err := module.ReleaseInput(t.Context(), testOwnerID, legacyOpen.Revision); err != nil {
		t.Fatal(err)
	}
	version = "verified-b"
	empty, err = module.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
		ExpectedRevision: empty.Revision,
		Input:            input,
		IdempotencyKey:   "open-changed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed.WorkflowID == first.WorkflowID {
		t.Fatal("changed source reused its previous workflow")
	}
}

func TestContinueInitialOpenLoadsAndSameInputApplies(t *testing.T) {
	t.Parallel()
	repo, err := db.Open(filepath.Join(t.TempDir(), "input.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	var verified []api.PrepareInput
	prepared := make(chan api.PrepareInput, 1)
	preparer := testPreparer()
	preparer.CorrectionsUnchangedFunc = func(_ context.Context, _ api.PrepareInput, update api.ReleaseCorrectionUpdate, revision uint64) (bool, error) {
		return update.Patch != nil && update.Patch.Values.ReleaseName.ManualYear == nil && revision == 0, nil
	}
	prepare := preparer.PrepareFunc
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		prepared <- input
		result, err := prepare(ctx, input)
		if err != nil {
			return result, err
		}
		return persistActiveInputTestRelease(ctx, repo, result)
	}
	module, err := New(persistent, NewMemoryPrivateResourceStore(), preparer, WithActiveInputs(repo,
		func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
			verified = append(verified, input)
			return api.InputRecord{
				CanonicalPath: input.SourcePath,
				SourceVersion: "verified",
				Manifest:      []byte(`{"identity":{"digest":"verified"}}`),
			}, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		module.activeMu.Lock()
		cancel, done := module.activeCancel, module.activeDone
		module.activeMu.Unlock()
		if cancel != nil {
			cancel()
			<-done
		}
	})
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "initial-open-refresh",
		Goal:           api.WorkflowGoalPrepared,
		Intent: api.WorkflowIntent{Preparation: &api.PrepareInput{
			SourcePath: filepath.Join(t.TempDir(), "source.mkv"),
		}},
	}
	opened, err := module.Continue(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatalf("continue initial open: %v", err)
	}
	if len(verified) != 1 || verified[0].ExternalFreshness != api.ExternalFreshnessLoad {
		t.Fatalf("verified preparation inputs = %#v", verified)
	}

	// Admission replay must retain its receipt when the original load is now active.
	if _, err := module.Continue(t.Context(), testOwnerID, request); err != nil {
		t.Fatalf("replay initial open: %v", err)
	}
	if len(verified) != 1 {
		t.Fatalf("replay re-verified input: %#v", verified)
	}
	collect := func(current CommandResult, want api.ExternalFreshness) {
		t.Helper()
		continued := request
		continued.Authority = &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision}
		advanced, continueErr := module.Continue(t.Context(), testOwnerID, continued)
		if continueErr != nil {
			t.Fatalf("continue admitted preparation: %v", continueErr)
		}
		if advanced.Operation == nil {
			t.Fatal("preparation did not start")
		}
		waitForWorkflowOperation(t, module, current.Workflow.ID, advanced.Operation.ID, func(status api.WorkflowOperationStatus) bool {
			return isTerminalProgressStatus(status.Status)
		})
		select {
		case input := <-prepared:
			if input.ExternalFreshness != want || input.VerifiedSource == nil || input.VerifiedSource.Identity.Digest != "verified" {
				t.Fatalf("collected freshness/source = %q/%#v, want %q/verified", input.ExternalFreshness, input.VerifiedSource, want)
			}
		default:
			t.Fatal("admitted preparation was not collected")
		}
		state, loadErr := persistent.Load(t.Context(), testOwnerID, current.Workflow.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if state.PreparationInput == nil || state.PreparationInput.ExternalFreshness != api.ExternalFreshnessReuse {
			t.Fatalf("one-shot freshness survived successful preparation: %#v", state.PreparationInput)
		}
	}
	collect(opened, api.ExternalFreshnessLoad)
	retained, err := persistent.Load(t.Context(), testOwnerID, opened.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected := retained.Workflow.Revision
	retained.Workflow.Revision++
	retained.Workflow.SubmissionExclusions = []api.SubmissionExclusion{{TrackerID: "ALPHA", Reason: "already_uploaded"}}
	dupes := api.DupeAssessment{
		ID:        "retained-dupes",
		Revision:  retained.Workflow.Revision,
		ExpiresAt: module.clock.Now().Add(time.Hour),

		Results: []api.TrackerDupeAssessment{{TrackerID: "ALPHA", Decision: api.DupeDecisionIgnored}},
	}
	retained.Dupes[dupes.ID] = dupes
	retained.Workflow.Dupes = &api.DupeAssessmentRef{ID: dupes.ID, Revision: dupes.Revision}
	if err := module.private.Put(testOwnerID, retained.Workflow.ID, dupePrivateResourceID(dupes.ID), "retained evidence", dupes.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	activeContext, err := module.activeMutationContext(t.Context(), testOwnerID, opened.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistent.Save(activeContext, testOwnerID, expected, retained); err != nil {
		t.Fatal(err)
	}
	beforeApply, err := module.Current(t.Context(), testOwnerID, opened.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	request.IdempotencyKey = "apply-same-input"
	applied, err := module.Continue(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatalf("apply active input without workflow authority: %v", err)
	}
	if len(verified) != 2 || verified[1].ExternalFreshness != api.ExternalFreshnessReuse {
		t.Fatalf("same-input correction preparation = %#v", verified)
	}
	if applied.Release == nil || beforeApply.Release == nil || applied.Release.ID != beforeApply.Release.ID {
		t.Fatal("unchanged input replaced the prepared release")
	}
	if len(applied.Workflow.SubmissionExclusions) != 1 || applied.Workflow.SubmissionExclusions[0].TrackerID != "ALPHA" {
		t.Fatal("unchanged input cleared confirmed upload exclusions")
	}
	select {
	case <-prepared:
		t.Fatal("unchanged input repeated preparation")
	default:
	}
	slot, err := module.ActiveInput(t.Context(), testOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	// Admission commits an unchanged correction receipt before any continuation.
	// An interrupted response can replay without applying that patch again.
	patch := &api.ReleaseCorrectionPatch{}
	noOp := OpenInputRequest{
		ExpectedRevision: slot.Revision,
		Input:            *request.Intent.Preparation,

		CorrectionPatch: patch,
		IdempotencyKey:  "unchanged-correction",
	}
	accepted, err := module.OpenInput(t.Context(), testOwnerID, noOp)
	if err != nil {
		t.Fatal(err)
	}
	acceptedState, err := persistent.Load(t.Context(), testOwnerID, accepted.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	if acceptedState.Workflow.Release == nil || *acceptedState.Workflow.Release != *retained.Workflow.Release ||
		acceptedState.Workflow.Dupes == nil || *acceptedState.Workflow.Dupes != *retained.Workflow.Dupes ||
		acceptedState.Dupes[dupes.ID].Results[0].Decision != api.DupeDecisionIgnored {
		t.Fatal("no-op admission discarded acknowledged assessment or prepared authority")
	}
	if acceptedState.Workflow.Revision != applied.Workflow.Revision+1 {
		t.Fatal("no-op receipt did not advance workflow exactly once")
	}
	// Replaying admission uses the committed receipt even if a later evaluator
	// would no longer classify the request as unchanged.
	preparer.CorrectionsUnchangedFunc = func(context.Context, api.PrepareInput, api.ReleaseCorrectionUpdate, uint64) (bool, error) {
		t.Fatal("committed admission replay reevaluated corrections")
		return false, nil
	}
	module.preparer = preparer
	replayed, err := module.OpenInput(t.Context(), testOwnerID, noOp)
	if err != nil || replayed.Revision != accepted.Revision {
		t.Fatalf("replay changed accepted slot: %#v, %v", replayed, err)
	}
	current, err := module.Current(t.Context(), testOwnerID, accepted.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	continuation := api.ContinueReleaseWorkflowRequest{IdempotencyKey: noOp.IdempotencyKey,
		Intent: api.WorkflowIntent{Preparation: &noOp.Input, CorrectionPatch: patch}}
	if err := consumeAcceptedCorrectionPatch(&continuation, current, acceptedState); err != nil || continuation.Intent.CorrectionPatch != nil {
		t.Fatalf("no-op receipt not consumed: %v", err)
	}
	changed := noOp
	changed.CorrectionPatch = &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{ManualYear: new(2000)}}}
	if _, err := module.OpenInput(t.Context(), testOwnerID, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed admission reused committed key: %v", err)
	}
	slot = accepted
	forced := noOp.Input
	forced.Force = true
	slot, err = module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{

		ExpectedRevision: slot.Revision,
		Input:            forced,
		IdempotencyKey:   "forced-same-source",
	})
	if err != nil {
		t.Fatal(err)
	}
	forcedState, err := persistent.Load(t.Context(), testOwnerID, slot.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	if len(forcedState.Workflow.SubmissionExclusions) != 1 || forcedState.Workflow.SubmissionExclusions[0].TrackerID != "ALPHA" ||
		forcedState.Workflow.Dupes != nil || forcedState.PendingDuplicateReuse != nil {
		t.Fatal("forced refresh lost confirmed submission or retained duplicate authority")
	}
	foreignProcess := slot
	foreignProcess.CoordinatorID = "prior-process"
	input, err := module.PreparationForInputOpen(t.Context(), foreignProcess, *request.Intent.Preparation)
	if err != nil || input.ExternalFreshness != api.ExternalFreshnessLoad {
		t.Fatalf("prior-process load freshness = %q, error %v", input.ExternalFreshness, err)
	}
	if err := module.ReleaseInput(t.Context(), testOwnerID, slot.Revision); err != nil {
		t.Fatal(err)
	}
	request.IdempotencyKey = "reload-closed-input"
	reloaded, err := module.Continue(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatalf("reload closed input: %v", err)
	}
	collect(reloaded, api.ExternalFreshnessLoad)
	request.IdempotencyKey = "explicit-provider-refresh"
	request.Intent.Preparation.ExternalFreshness = api.ExternalFreshnessRefresh
	refreshed, err := module.Continue(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatalf("explicit provider refresh: %v", err)
	}
	collect(refreshed, api.ExternalFreshnessRefresh)
	request.IdempotencyKey = "open-different-input"
	request.Intent.Preparation = &api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "other.mkv")}
	switched, err := module.Continue(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatalf("load different input: %v", err)
	}
	collect(switched, api.ExternalFreshnessLoad)
	for _, failure := range []string{"canceled", "concurrent generation", "concurrent correction"} {
		t.Run(failure, func(t *testing.T) {
			slot, err := module.ActiveInput(t.Context(), testOwnerID)
			if err != nil {
				t.Fatal(err)
			}
			before, err := persistent.Load(t.Context(), testOwnerID, slot.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var replaced *api.PreparedRelease
			display := testPreparer().DisplayFunc
			preparer.DisplayFunc = func(ctx context.Context, ref api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
				result, err := display(ctx, ref)
				if failure == "concurrent generation" {
					previous, loadErr := repo.LoadPreparedRelease(ctx, ref.SourcePath)
					if loadErr != nil {
						t.Fatal(loadErr)
					}
					replaced = &previous
					next := previous
					next.Generation++
					next.Identity.Generation, next.ProviderMetadata.Generation = next.Generation, next.Generation
					if err := repo.CommitPreparedRelease(ctx, next); err != nil {
						t.Fatal(err)
					}
				}
				return result, err
			}
			preparer.CorrectionsUnchangedFunc = func(ctx context.Context, _ api.PrepareInput, _ api.ReleaseCorrectionUpdate, _ uint64) (bool, error) {
				switch failure {
				case "canceled":
					cancel()
				case "concurrent correction":
					if _, err := repo.CompareAndSwapReleaseCorrections(ctx, request.Intent.Preparation.SourcePath, 0,
						api.StoredReleaseCorrectionsV1{Version: 1, ReleaseName: api.ReleaseNameOverrides{Tag: new("changed")}}); err != nil {
						t.Fatal(err)
					}
				}
				return true, nil
			}
			module.preparer = preparer
			_, err = module.OpenInput(ctx, testOwnerID, OpenInputRequest{
				ExpectedRevision: slot.Revision,

				Input:           *request.Intent.Preparation,
				CorrectionPatch: &api.ReleaseCorrectionPatch{},
				IdempotencyKey:  failure,
			})
			if err == nil || (failure == "concurrent correction" && !errors.Is(err, api.ErrCorrectionConflict)) ||
				(failure == "concurrent generation" && !errors.Is(err, api.ErrActiveInputChanged)) {
				t.Fatalf("unsafe no-op admission was not rejected: %v", err)
			}
			after, err := persistent.Load(t.Context(), testOwnerID, slot.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Workflow.Revision != before.Workflow.Revision || len(after.Receipts) != len(before.Receipts) {
				t.Fatal("failed finalization persisted a no-op receipt or workflow mutation")
			}
			if replaced != nil {
				// Restore this subtest's synthetic competing writer for the next case.
				if err := repo.CommitPreparedRelease(t.Context(), *replaced); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestClosedInputWorkflowSourceAssociatesTerminalHistoryPurge(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                string
		prepare             bool
		async               bool
		wantOperationStatus api.StageStatus
		wantWorkflowStatus  api.WorkflowStatus
		configure           func(*ReleasePreparerFunc)
	}{
		{name: "draft", wantWorkflowStatus: api.WorkflowStatusDraft},
		{
			name:               "playlist blocked",
			prepare:            true,
			wantWorkflowStatus: api.WorkflowStatusBlocked,
			configure: func(preparer *ReleasePreparerFunc) {
				preparer.PrepareFunc = func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
					return api.PrepareResult{}, &api.PlaylistSelectionRequiredError{
						SourcePath: input.SourcePath,
						Candidates: []api.PlaylistInfo{{ID: "disc:00001.mpls", File: "00001.mpls"}},
					}
				}
			},
		},
		{
			name:                "failed",
			prepare:             true,
			async:               true,
			wantOperationStatus: api.StageStatusFailed,
			wantWorkflowStatus:  api.WorkflowStatusDraft,
			configure: func(preparer *ReleasePreparerFunc) {
				preparer.PrepareFunc = func(context.Context, api.PrepareInput) (api.PrepareResult, error) {
					return api.PrepareResult{}, errors.New("synthetic preparation failure")
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repo, err := db.Open(filepath.Join(t.TempDir(), "history.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			persistent, err := NewPersistentRepository(repo)
			if err != nil {
				t.Fatal(err)
			}
			preparer := testPreparer()
			if test.configure != nil {
				test.configure(&preparer)
			}
			module, err := New(persistent, NewMemoryPrivateResourceStore(), preparer, WithActiveInputs(repo,
				func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
					return api.InputRecord{
						CanonicalPath: input.SourcePath,
						SourceVersion: "verified",
						Manifest:      []byte(`{"Identity":{"Digest":"verified"}}`),
					}, nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				module.activeMu.Lock()
				cancel, done := module.activeCancel, module.activeDone
				module.activeMu.Unlock()
				if cancel != nil {
					cancel()
					<-done
				}
			})

			sourcePath := filepath.Join(t.TempDir(), "source.mkv")
			input := api.PrepareInput{SourcePath: sourcePath}
			active, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{Input: input, IdempotencyKey: "open-" + test.name})
			if err != nil {
				t.Fatalf("open input: %v", err)
			}
			if test.prepare {
				command := PrepareReleaseCommand{
					WorkflowID:       active.WorkflowID,
					ExpectedRevision: 1,
					Input:            input,
					IdempotencyKey:   "prepare-" + test.name,
				}
				if !test.async {
					if _, executeErr := module.Execute(t.Context(), testOwnerID, command); executeErr != nil {
						t.Fatalf("prepare workflow: %v", executeErr)
					}
				} else {
					operation, startErr := module.Start(t.Context(), testOwnerID, command)
					if startErr != nil {
						t.Fatalf("start preparation: %v", startErr)
					}
					terminal := waitForWorkflowOperation(t, module, active.WorkflowID, operation.ID, func(status api.WorkflowOperationStatus) bool {
						return isTerminalProgressStatus(status.Status)
					})
					if test.wantOperationStatus != "" && terminal.Status != test.wantOperationStatus {
						t.Fatalf("preparation terminal status = %#v", terminal)
					}
				}
			}
			state, stateErr := persistent.Load(t.Context(), testOwnerID, active.WorkflowID)
			if stateErr != nil {
				t.Fatalf("load retained workflow: %v", stateErr)
			}
			if state.Workflow.Status != test.wantWorkflowStatus {
				t.Fatalf("retained workflow status = %q, want %q", state.Workflow.Status, test.wantWorkflowStatus)
			}
			active, err = module.ActiveInput(t.Context(), testOwnerID)
			if err != nil {
				t.Fatalf("load active input: %v", err)
			}
			if err := module.ReleaseInput(t.Context(), testOwnerID, active.Revision); err != nil {
				t.Fatalf("release input: %v", err)
			}
			if err := repo.PurgeContentData(t.Context(), sourcePath); err != nil {
				t.Fatalf("purge closed %s workflow: %v", test.name, err)
			}
			if _, err := repo.LoadReleaseWorkflowState(t.Context(), testOwnerID, active.WorkflowID); !errors.Is(err, api.ErrReleaseWorkflowStateNotFound) {
				t.Fatalf("workflow after purge = %v", err)
			}
		})
	}
}

func TestRefreshCompletedInputPreservesRetryAuthorityAndConfirmedExclusions(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"successful upload", "all excluded", "failed upload", "partial client injection", "blocked", "failed workflow"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo, err := db.OpenContext(t.Context(), filepath.Join(t.TempDir(), "terminal-refresh.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.MigrateContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			persistent, err := NewPersistentRepository(repo)
			if err != nil {
				t.Fatal(err)
			}
			preparer := testPreparer()
			prepare := preparer.PrepareFunc
			preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				result, err := prepare(ctx, input)
				if err != nil {
					return result, err
				}
				return persistActiveInputTestRelease(ctx, repo, result)
			}
			module, err := New(persistent, NewMemoryPrivateResourceStore(), preparer,
				WithSubmissionHistoryFilter(confirmedSubmissionFilter{}),
				WithActiveInputs(repo, func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
					return api.InputRecord{
						CanonicalPath: input.SourcePath,
						SourceVersion: "verified",
						Manifest:      []byte(`{"identity":{"digest":"verified"}}`),
					}, nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				module.activeMu.Lock()
				cancel, done := module.activeCancel, module.activeDone
				module.activeMu.Unlock()
				if cancel != nil {
					cancel()
					<-done
				}
			})
			input := api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "source.mkv")}
			admitted, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{Input: input, IdempotencyKey: "open"})
			if err != nil {
				t.Fatal(err)
			}
			current, err := module.Current(t.Context(), testOwnerID, admitted.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			opened, err := module.Execute(t.Context(), testOwnerID, PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            input,
			})
			if err != nil {
				t.Fatal(err)
			}
			if opened.Release == nil || opened.Workflow.Release == nil {
				t.Fatalf("fixture did not prepare verified input: %#v", opened)
			}
			state, err := persistent.Load(t.Context(), testOwnerID, opened.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := state.Workflow.Revision
			state.Workflow.Revision++
			state.Workflow.Status = api.WorkflowStatusCompleted
			state.Workflow.SubmissionExclusions = []api.SubmissionExclusion{{TrackerID: "ALPHA", Reason: "already_uploaded"}}
			dupes := api.DupeAssessment{
				ID:        "terminal-dupes",
				Revision:  state.Workflow.Revision,
				ExpiresAt: module.clock.Now().Add(time.Hour),
				Results:   []api.TrackerDupeAssessment{{TrackerID: "ALPHA", Decision: api.DupeDecisionIgnored}},
			}
			state.Dupes[dupes.ID] = dupes
			state.Workflow.Dupes = &api.DupeAssessmentRef{ID: dupes.ID, Revision: dupes.Revision}
			if err := module.private.Put(testOwnerID, state.Workflow.ID, dupePrivateResourceID(dupes.ID), "evidence", dupes.ExpiresAt); err != nil {
				t.Fatal(err)
			}
			upload := api.UploadResult{
				ID:       "terminal-upload",
				Revision: state.Workflow.Revision,
				Status:   api.StageStatusCompleted,
				Results: []api.UploadTrackerResult{{
					TrackerID:             "ALPHA",
					SubmissionStatus:      api.StageStatusCompleted,
					ClientInjectionStatus: api.StageStatusCompleted,
				}},
			}
			switch name {
			case "failed upload":
				upload.Status = api.StageStatusFailed
				upload.Results[0].SubmissionStatus = api.StageStatusFailed
			case "partial client injection":
				upload.Status = api.StageStatusPartial
				upload.Results[0].ClientInjectionStatus = api.StageStatusFailed
			case "blocked":
				state.Workflow.Status = api.WorkflowStatusBlocked
				state.Workflow.RequiredActions = []api.RequiredAction{{
					ID:               "reconcile-client",
					Kind:             api.RequiredActionReconcileSubmission,
					Status:           api.RequiredActionStatusPending,
					WorkflowRevision: state.Workflow.Revision,
					TrackerID:        "ALPHA",
					EffectKind:       api.WorkflowExternalEffectClientInjection,
					EffectScopeID:    "upload:ALPHA",
					Prompt:           "Verify client injection outcome.",
					CreatedAt:        module.clock.Now().UTC(),
				}}
			case "failed workflow":
				state.Workflow.Status = api.WorkflowStatusFailed
			case "all excluded":
				state.Workflow.Dupes = nil
			}
			if name != "all excluded" {
				state.UploadResults[upload.ID] = upload
				state.Workflow.UploadResult = &api.UploadResultRef{ID: upload.ID, Revision: upload.Revision}
			}
			ctx, err := module.activeMutationContext(t.Context(), testOwnerID, state.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := persistent.Save(ctx, testOwnerID, expected, state); err != nil {
				t.Fatal(err)
			}
			active, err := module.ActiveInput(t.Context(), testOwnerID)
			if err != nil {
				t.Fatal(err)
			}
			refreshed, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
				ExpectedRevision: active.Revision,
				Input:            input,
				IdempotencyKey:   "refresh",
			})
			if err != nil {
				t.Fatal(err)
			}
			after, err := persistent.Load(t.Context(), testOwnerID, refreshed.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Workflow.SubmissionExclusions) != 1 || after.Workflow.SubmissionExclusions[0] != state.Workflow.SubmissionExclusions[0] {
				t.Fatalf("refresh lost confirmed submission exclusions: %#v", after.Workflow.SubmissionExclusions)
			}
			if name != "successful upload" && name != "all excluded" {
				if after.Workflow.Status != state.Workflow.Status || after.Workflow.Revision != state.Workflow.Revision ||
					after.Workflow.Dupes == nil || *after.Workflow.Dupes != *state.Workflow.Dupes ||
					after.Workflow.UploadResult == nil || *after.Workflow.UploadResult != *state.Workflow.UploadResult {
					t.Fatalf("refresh discarded retry authority: %#v", after.Workflow)
				}
				if !reflect.DeepEqual(after.Workflow.RequiredActions, state.Workflow.RequiredActions) {
					t.Fatalf("refresh changed pending reconciliation: %#v", after.Workflow.RequiredActions)
				}
				if retained := after.Dupes[dupes.ID]; retained.Results[0].Decision != api.DupeDecisionIgnored || !retained.ExpiresAt.Equal(dupes.ExpiresAt) {
					t.Fatalf("refresh changed acknowledged assessment: %#v", retained)
				}
				if _, err := module.private.Get(testOwnerID, state.Workflow.ID, dupePrivateResourceID(dupes.ID), module.clock.Now()); err != nil {
					t.Fatalf("refresh lost duplicate evidence: %v", err)
				}
				return
			}
			if after.Workflow.Status != api.WorkflowStatusDraft || after.Workflow.Release != nil || after.Workflow.Dupes != nil ||
				after.Workflow.Media != nil || after.Workflow.UploadResult != nil {
				t.Fatalf("successful refresh did not reopen safely: %#v", after.Workflow)
			}
			prepared, err := module.Execute(t.Context(), testOwnerID, PrepareReleaseCommand{
				WorkflowID:       after.Workflow.ID,
				ExpectedRevision: after.Workflow.Revision,
				Input:            input,
			})
			if err != nil {
				t.Fatal(err)
			}
			projected, err := module.Execute(t.Context(), testOwnerID, ProjectTrackersCommand{
				WorkflowID:       prepared.Workflow.ID,
				ExpectedRevision: prepared.Workflow.Revision,
				TrackerIDs:       []api.TrackerID{"ALPHA"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !projected.Workflow.AllSelectedTrackersAlreadyUploaded() || projected.Workflow.Dupes != nil ||
				projected.Workflow.Media != nil || projected.Workflow.UploadResult != nil || projected.Projections != nil {
				t.Fatalf("confirmed success was not excluded before downstream work: %#v", projected)
			}
		})
	}
}

func TestCompletedInputReopeningRequiresExactSuccessfulResult(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"success", "missing result", "wrong ID", "wrong revision", "empty results", "failed submission", "failed injection", "unconfirmed exclusion"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := api.UploadResult{
				ID:       "upload",
				Revision: 2,
				Status:   api.StageStatusCompleted,
				Results: []api.UploadTrackerResult{{
					TrackerID:             "ALPHA",
					SubmissionStatus:      api.StageStatusCompleted,
					ClientInjectionStatus: api.StageStatusCompleted,
				}},
			}
			state := State{Workflow: api.ReleaseWorkflow{
				Status:       api.WorkflowStatusCompleted,
				UploadResult: &api.UploadResultRef{ID: result.ID, Revision: result.Revision},
			}}
			switch name {
			case "wrong ID":
				result.ID = "other"
			case "wrong revision":
				result.Revision++
			case "empty results":
				result.Results = nil
			case "failed submission":
				result.Results[0].SubmissionStatus = api.StageStatusFailed
			case "failed injection":
				result.Results[0].ClientInjectionStatus = api.StageStatusFailed
			case "unconfirmed exclusion":
				state.Workflow.UploadResult = nil
				state.Workflow.SubmissionExclusions = []api.SubmissionExclusion{{TrackerID: "ALPHA", Reason: "not_confirmed"}}
			}
			if name != "missing result" {
				state.UploadResults = map[api.UploadResultID]api.UploadResult{"upload": result}
			}
			if got := completedInputCanReopen(state); got != (name == "success") {
				t.Fatalf("completed input reopening = %t", got)
			}
		})
	}
}
