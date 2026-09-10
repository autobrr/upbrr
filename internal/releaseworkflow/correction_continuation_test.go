// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestContinuationRefreshesCorrectionsChangedByAnotherWorkflow(t *testing.T) {
	for _, initialRevision := range []uint64{0, 1} {
		t.Run(strconv.FormatUint(initialRevision, 10), func(t *testing.T) {
			var mu sync.Mutex
			preparedInputs := make([]api.PrepareInput, 0, 4)
			stored := api.ReleaseCorrectionsSnapshot{Revision: initialRevision, Corrections: api.StoredReleaseCorrectionsV1{Version: 1}}
			if initialRevision != 0 {
				stored.Corrections.Metadata.Title = new("Previous title")
			}
			preparer := testPreparer()
			base := preparer.PrepareFunc
			preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				mu.Lock()
				preparedInputs = append(preparedInputs, input)
				mu.Unlock()
				if !input.Instructions.Playlist.Set || !slices.Equal(input.Instructions.Playlist.Selected, []string{"disc-one:00001.mpls"}) {
					return api.PrepareResult{}, &api.PlaylistSelectionRequiredError{
						SourcePath: input.SourcePath,
						Candidates: []api.PlaylistInfo{{
							ID: "disc-one:00001.mpls",
						}},
					}
				}
				return base(ctx, input)
			}
			preparer.CorrectionsCurrentFunc = func(_ context.Context, _ string, revision uint64) (bool, error) {
				mu.Lock()
				defer mu.Unlock()
				return revision == stored.Revision, nil
			}
			preparer.ResolveInputFunc = func(_ context.Context, input api.PrepareInput, update api.ReleaseCorrectionUpdate) (api.ResolvedPreparationInput, error) {
				mu.Lock()
				defer mu.Unlock()
				if update.Mode == api.ReleaseCorrectionUpdatePatch {
					corrections, err := api.ApplyReleaseCorrectionUpdate(stored, update)
					if err != nil {
						return api.ResolvedPreparationInput{}, fmt.Errorf("apply release correction update: %w", err)
					}
					stored.Corrections = corrections
					stored.Revision++
				} else if input.Instructions.Metadata.Title != nil {
					return api.ResolvedPreparationInput{}, errors.New("replay attempted to restore previous title")
				}
				input.Instructions.Metadata = stored.Corrections.Metadata
				return api.ResolvedPreparationInput{Input: input, Corrections: stored}, nil
			}
			preparer.PrepareResolvedFunc = func(ctx context.Context, input api.ResolvedPreparationInput) (api.PrepareResult, error) {
				result, err := preparer.Prepare(ctx, input.Input)
				result.EffectiveInstructions = input.Input.Instructions
				result.Corrections = input.Corrections
				return result, err
			}
			module, _ := newTestModule(t, preparer, WithClock(&selectionEnrichmentClock{}))
			forceRecheck := true
			input := api.PrepareInput{
				SourcePath: `C:\releases\Shared.Example.mkv`,
				Instructions: api.ReleaseFactInstructions{
					Playlist: api.PlaylistInstruction{Set: true, Selected: []string{"disc-one:00001.mpls"}},
				},
				Controls: api.PreparationControls{ConfirmBDMVRescan: true, ForceRecheck: &forceRecheck},
			}
			first := executeCommand(t, module, CreateWorkflowCommand{Instructions: input.Instructions})
			first = executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       first.Workflow.ID,
				ExpectedRevision: first.Workflow.Revision,
				Input:            input,
			})
			second := executeCommand(t, module, CreateWorkflowCommand{})
			executeCommand(t, module, ReplaceFactInstructionsCommand{
				WorkflowID:       second.Workflow.ID,
				ExpectedRevision: second.Workflow.Revision,
				SourcePath:       input.SourcePath,
				CorrectionPatch:  &api.ReleaseCorrectionPatch{ExpectedRevision: new(initialRevision), Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{Title: new("Shared corrected title")}}},
			})
			request := api.ContinueReleaseWorkflowRequest{
				Authority:      &api.WorkflowAuthority{WorkflowID: first.Workflow.ID, ExpectedRevision: first.Workflow.Revision},
				IdempotencyKey: "refresh-shared-corrections",
				Goal:           api.WorkflowGoalPrepared,
				Intent:         api.WorkflowIntent{Preparation: &input, FactInstructions: &input.Instructions},
			}
			updated, err := module.Continue(t.Context(), testOwnerID, request)
			if err != nil {
				t.Fatal(err)
			}
			if updated.Operation == nil {
				t.Fatal("changed source corrections reused old preparation")
			}
			waitForWorkflowOperation(t, module, updated.Workflow.ID, updated.Operation.ID, func(status api.WorkflowOperationStatus) bool { return isTerminalProgressStatus(status.Status) })
			updated, err = module.Current(t.Context(), testOwnerID, first.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.FactInstructions.CorrectionRevision != initialRevision+1 || updated.FactInstructions.Instructions.Metadata.Title == nil || *updated.FactInstructions.Instructions.Metadata.Title != "Shared corrected title" {
				t.Fatalf("shared correction did not refresh: %#v", updated.FactInstructions)
			}
			request.Authority.ExpectedRevision = updated.Workflow.Revision
			repeated, err := module.Continue(t.Context(), testOwnerID, request)
			if err != nil {
				t.Fatal(err)
			}
			if repeated.Workflow.Revision != updated.Workflow.Revision {
				t.Fatal("accepted correction refresh did not stabilize")
			}
			patchOnly := api.ContinueReleaseWorkflowRequest{
				Authority:      &api.WorkflowAuthority{WorkflowID: updated.Workflow.ID, ExpectedRevision: updated.Workflow.Revision},
				IdempotencyKey: "patch-with-retained-input",
				Goal:           api.WorkflowGoalPrepared,
				Intent: api.WorkflowIntent{CorrectionPatch: &api.ReleaseCorrectionPatch{
					ExpectedRevision: new(initialRevision + 1), Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{Title: new("Patch only title")}},
				}},
			}
			accepted, err := module.Continue(t.Context(), testOwnerID, patchOnly)
			if err != nil {
				t.Fatal(err)
			}
			patchOnly.Authority.ExpectedRevision = accepted.Workflow.Revision
			resumed, err := module.Continue(t.Context(), testOwnerID, patchOnly)
			if err != nil {
				t.Fatal(err)
			}
			if resumed.Operation == nil {
				t.Fatal("patch-only request lost retained preparation input")
			}
			waitForWorkflowOperation(t, module, resumed.Workflow.ID, resumed.Operation.ID, func(status api.WorkflowOperationStatus) bool { return isTerminalProgressStatus(status.Status) })
			resumed, err = module.Current(t.Context(), testOwnerID, first.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if resumed.Release == nil || resumed.FactInstructions.CorrectionRevision != initialRevision+2 || resumed.FactInstructions.Instructions.Metadata.Title == nil || *resumed.FactInstructions.Instructions.Metadata.Title != "Patch only title" {
				t.Fatalf("patch-only request did not resume preparation: %#v", resumed.FactInstructions)
			}
			if resumed.Workflow.Status != api.WorkflowStatusActive || len(resumed.Workflow.RequiredActions) != 0 {
				t.Fatalf("patch-only request reset playlist selection: %#v", resumed.Workflow)
			}
			mu.Lock()
			lastPreparedInput := preparedInputs[len(preparedInputs)-1]
			mu.Unlock()
			if !lastPreparedInput.Instructions.Playlist.Set ||
				!slices.Equal(lastPreparedInput.Instructions.Playlist.Selected, input.Instructions.Playlist.Selected) ||
				!lastPreparedInput.Controls.ConfirmBDMVRescan ||
				lastPreparedInput.Controls.ForceRecheck == nil || !*lastPreparedInput.Controls.ForceRecheck {
				t.Fatalf("patch-only preparation input = %#v", lastPreparedInput)
			}
		})
	}
}

func TestContinuationRetainsInheritedCorrectionsAfterPreparation(t *testing.T) {
	preparer := testPreparer()
	base := preparer.PrepareFunc
	preparer.PrepareResolvedFunc = func(ctx context.Context, input api.ResolvedPreparationInput) (api.PrepareResult, error) {
		result, err := base(ctx, input.Input)
		result.EffectiveInstructions = input.Input.Instructions
		result.EffectiveInstructions.Metadata.Title = new("Retained title")
		result.Corrections = api.ReleaseCorrectionsSnapshot{Revision: 3, Corrections: api.StoredReleaseCorrectionsV1{Version: 1, Metadata: api.MetadataOverrides{Title: new("Retained title")}}}
		return result, err
	}
	module, _ := newTestModule(t, preparer)
	current := executeCommand(t, module, CreateWorkflowCommand{})
	input := api.PrepareInput{SourcePath: `C:\releases\Example.mkv`}
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            input,
	})
	result, err := module.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
		IdempotencyKey: "continue-prepared",
		Goal:           api.WorkflowGoalPrepared,
		Intent:         api.WorkflowIntent{Preparation: &input, FactInstructions: &input.Instructions},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Workflow.Revision != current.Workflow.Revision || result.FactInstructions.Instructions.Metadata.Title == nil || *result.FactInstructions.Instructions.Metadata.Title != "Retained title" {
		t.Fatalf("continued source erased retained correction: %#v", result.FactInstructions)
	}
}

func TestContinuationCorrectionReceiptConsumesOnlyMatchingPatch(t *testing.T) {
	module, repository := newTestModule(t, testPreparer())
	current := executeCommand(t, module, CreateWorkflowCommand{})
	patch := &api.ReleaseCorrectionPatch{ExpectedRevision: new(uint64), Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{Title: new("Manual title")}}}
	command := ReplaceFactInstructionsCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		SourcePath:       `C:\releases\Example.mkv`,
		CorrectionPatch:  patch,
		IdempotencyKey:   continuationIdempotencyKey("correction", "correction-patch", 0),
	}
	current = executeCommand(t, module, command)
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "correction",
		Goal:           api.WorkflowGoalPrepared,
		Intent:         api.WorkflowIntent{CorrectionPatch: patch, Preparation: &api.PrepareInput{SourcePath: command.SourcePath}},
	}
	if err := consumeAcceptedCorrectionPatch(&request, current, state); err != nil {
		t.Fatal(err)
	}
	if request.Intent.CorrectionPatch != nil {
		t.Fatal("accepted patch was replayed")
	}
	changed := *patch
	changed.Values.Metadata.Title = new("Different title")
	request.Intent.CorrectionPatch = &changed
	if err := consumeAcceptedCorrectionPatch(&request, current, state); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed patch reuses receipt: %v", err)
	}
}

func TestReplaceFactInstructionsRejectsUntrustedCorrectionConfirmation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*State, *ReplaceFactInstructionsCommand)
	}{
		{
			name: "without action",
			mutate: func(state *State, _ *ReplaceFactInstructionsCommand) {
				state.Workflow.RequiredActions = nil
			},
		},
		{
			name: "stale workflow action",
			mutate: func(state *State, _ *ReplaceFactInstructionsCommand) {
				state.Workflow.RequiredActions[0].WorkflowRevision--
			},
		},
		{
			name: "mismatched correction revision",
			mutate: func(state *State, _ *ReplaceFactInstructionsCommand) {
				state.Workflow.RequiredActions[0].CorrectionConfirmation.Revision++
			},
		},
		{
			name: "wrong stale fields",
			mutate: func(state *State, _ *ReplaceFactInstructionsCommand) {
				state.Workflow.RequiredActions[0].CorrectionConfirmation.Fields = []api.CorrectionField{api.CorrectionFieldMetadataTitle}
			},
		},
		{
			name: "wrong previous binding",
			mutate: func(state *State, _ *ReplaceFactInstructionsCommand) {
				state.Workflow.RequiredActions[0].CorrectionConfirmation.PreviousBindings[api.CorrectionFieldMetadataTitle] = api.ContentBinding{SourceFingerprint: "other"}
			},
		},
		{
			name: "wrong source",
			mutate: func(_ *State, command *ReplaceFactInstructionsCommand) {
				command.SourcePath = `C:\releases\Other.mkv`
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := confirmationTestState()
			command := confirmationTestCommand()
			test.mutate(&state, &command)
			resolveCalls := 0
			preparer := testPreparer()
			preparer.ResolveInputFunc = func(context.Context, api.PrepareInput, api.ReleaseCorrectionUpdate) (api.ResolvedPreparationInput, error) {
				resolveCalls++
				return api.ResolvedPreparationInput{}, errors.New("correction store must not be touched")
			}
			module, _ := newTestModule(t, preparer)
			_, err := module.replaceFactInstructions(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, time.Now().UTC(), command)
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("replace correction confirmation error = %v", err)
			}
			if resolveCalls != 0 {
				t.Fatalf("untrusted confirmation resolved corrections %d times", resolveCalls)
			}
		})
	}
}

func TestCorrectionConfirmationCarriesOnlyApprovedFieldsUntilPreparationSucceeds(t *testing.T) {
	state := confirmationTestState()
	command := confirmationTestCommand()
	approved := state.Workflow.RequiredActions[0].CorrectionConfirmation.CurrentBinding
	resolvedCorrections, err := state.Corrections.Clone()
	if err != nil {
		t.Fatalf("clone corrections: %v", err)
	}
	resolvedCorrections.Revision++
	for _, field := range resolvedCorrections.Corrections.StaleContentFields {
		resolvedCorrections.Corrections.ContentBindings[field] = approved
	}
	resolvedCorrections.Corrections.StaleContentFields = nil
	resolveCalls := 0
	failPreparation := true
	preparer := testPreparer()
	preparer.ResolveInputFunc = func(_ context.Context, input api.PrepareInput, update api.ReleaseCorrectionUpdate) (api.ResolvedPreparationInput, error) {
		resolveCalls++
		switch update.Mode {
		case api.ReleaseCorrectionUpdatePatch:
			if update.Confirmation == nil || !sameCorrectionFields(update.Confirmation.Fields, []api.CorrectionField{api.CorrectionFieldMetadataTitle, api.CorrectionFieldMetadataAlternateTitle}) ||
				update.Confirmation.Revision != state.Corrections.Revision || update.Confirmation.CurrentBinding != approved {
				t.Fatalf("accepted confirmation update = %#v", update)
			}
		case api.ReleaseCorrectionUpdateInherit:
			if update.Confirmation == nil || !slices.Equal(update.Confirmation.Fields, []api.CorrectionField{api.CorrectionFieldMetadataTitle}) ||
				update.Confirmation.Revision != resolvedCorrections.Revision || update.Confirmation.CurrentBinding != approved {
				t.Fatalf("preparation confirmation update = %#v", update)
			}
		case api.ReleaseCorrectionUpdateReplace, api.ReleaseCorrectionUpdateResetAll:
			t.Fatalf("unexpected correction update = %#v", update)
		}
		return api.ResolvedPreparationInput{Input: input, Corrections: resolvedCorrections}, nil
	}
	preparer.PrepareResolvedFunc = func(_ context.Context, input api.ResolvedPreparationInput) (api.PrepareResult, error) {
		if failPreparation {
			return api.PrepareResult{}, errors.New("provider unavailable")
		}
		return api.PrepareResult{
			Release: api.PreparedRelease{
				Generation: 1,
				Source:     api.SourceManifest{SourcePath: input.Input.SourcePath},
				Naming:     api.NamingFacts{ReleaseName: "Example.Release.2026.1080p-GRP"},
			},
			EffectiveInstructions: input.Input.Instructions,
			Corrections:           input.Corrections,
		}, nil
	}
	module, _ := newTestModule(t, preparer)
	if _, err := module.replaceFactInstructions(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, time.Now().UTC(), command); err != nil {
		t.Fatalf("accept confirmed correction: %v", err)
	}
	if state.PendingCorrectionConfirmation == nil ||
		!slices.Equal(state.PendingCorrectionConfirmation.Fields, []api.CorrectionField{api.CorrectionFieldMetadataTitle}) ||
		state.PendingCorrectionConfirmation.Revision != resolvedCorrections.Revision ||
		state.PendingCorrectionConfirmation.CurrentBinding != approved {
		t.Fatalf("pending confirmation = %#v", state.PendingCorrectionConfirmation)
	}
	if facts := state.FactInstructions[state.Workflow.FactInstructions.ID]; len(facts.ExplicitCorrectionFields) != 0 {
		t.Fatalf("confirmed fields became fresh explicit fields: %#v", facts)
	}
	cloned, err := cloneState(state)
	if err != nil {
		t.Fatalf("serialize pending confirmation: %v", err)
	}
	if cloned.PendingCorrectionConfirmation == nil ||
		!slices.Equal(cloned.PendingCorrectionConfirmation.Fields, state.PendingCorrectionConfirmation.Fields) {
		t.Fatalf("serialized pending confirmation = %#v", cloned.PendingCorrectionConfirmation)
	}
	if _, err := module.prepareRelease(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, time.Now().UTC(), PrepareReleaseCommand{
		WorkflowID: state.Workflow.ID,
		Input:      api.PrepareInput{SourcePath: `C:\releases\Example.mkv`},
	}); err == nil {
		t.Fatal("provider failure prepared a release")
	}
	if state.PendingCorrectionConfirmation == nil {
		t.Fatal("provider failure cleared pending confirmation")
	}
	failPreparation = false
	if _, err := module.prepareRelease(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, time.Now().UTC(), PrepareReleaseCommand{
		WorkflowID: state.Workflow.ID,
		Input:      api.PrepareInput{SourcePath: `C:\releases\Example.mkv`},
	}); err != nil {
		t.Fatalf("prepare confirmed correction: %v", err)
	}
	if resolveCalls != 3 || state.PendingCorrectionConfirmation != nil {
		t.Fatalf("confirmation lifecycle calls=%d pending=%#v", resolveCalls, state.PendingCorrectionConfirmation)
	}
}

func TestReplaceFactInstructionsClearsPendingConfirmationForNonConfirmationPatches(t *testing.T) {
	for _, patch := range []*api.ReleaseCorrectionPatch{
		{ExpectedRevision: new(uint64), ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataTitle}}},
		{ExpectedRevision: new(uint64), Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{Title: new("Replacement")}}},
	} {
		state := confirmationTestState()
		state.PendingCorrectionConfirmation = cloneCorrectionConfirmation(state.Workflow.RequiredActions[0].CorrectionConfirmation)
		*patch.ExpectedRevision = state.Corrections.Revision
		preparer := testPreparer()
		preparer.ResolveInputFunc = func(_ context.Context, input api.PrepareInput, update api.ReleaseCorrectionUpdate) (api.ResolvedPreparationInput, error) {
			if update.Confirmation != nil {
				t.Fatalf("non-confirmation patch carried pending authority: %#v", update)
			}
			return api.ResolvedPreparationInput{Input: input, Corrections: *state.Corrections}, nil
		}
		module, _ := newTestModule(t, preparer)
		command := confirmationTestCommand()
		command.CorrectionPatch = patch
		if _, err := module.replaceFactInstructions(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, time.Now().UTC(), command); err != nil {
			t.Fatalf("replace non-confirmation patch: %v", err)
		}
		if state.PendingCorrectionConfirmation != nil {
			t.Fatalf("non-confirmation patch retained pending confirmation: %#v", state.PendingCorrectionConfirmation)
		}
	}
}

func TestRemainingCorrectionPatchPreservesApprovedConfirmation(t *testing.T) {
	for _, test := range []struct {
		name             string
		patch            api.ReleaseCorrectionPatch
		expectedRevision bool
	}{
		{
			name:             "reset with revision",
			patch:            api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataGenres}}},
			expectedRevision: true,
		},
		{
			name:             "replace with revision",
			patch:            api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{Genres: new([]string{"Drama"})}}},
			expectedRevision: true,
		},
		{
			name:  "reset without revision",
			patch: api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataGenres}}},
		},
		{
			name:  "replace without revision",
			patch: api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{Genres: new([]string{"Drama"})}}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := partiallyConfirmedCorrectionState()
			if test.expectedRevision {
				test.patch.ExpectedRevision = new(state.Corrections.Revision)
			}
			resolvedCorrections, err := state.Corrections.Clone()
			if err != nil {
				t.Fatalf("clone remaining corrections: %v", err)
			}
			resolvedCorrections.Revision++
			resolvedCorrections.Corrections.StaleContentFields = nil
			approved := state.PendingCorrectionConfirmation.CurrentBinding
			resolveCalls := 0
			preparer := testPreparer()
			preparer.ResolveInputFunc = func(_ context.Context, input api.PrepareInput, update api.ReleaseCorrectionUpdate) (api.ResolvedPreparationInput, error) {
				resolveCalls++
				if update.Confirmation == nil || !slices.Equal(update.Confirmation.Fields, []api.CorrectionField{api.CorrectionFieldMetadataTitle}) ||
					update.Confirmation.CurrentBinding != approved {
					t.Fatalf("remaining correction update = %#v", update)
				}
				return api.ResolvedPreparationInput{Input: input, Corrections: resolvedCorrections}, nil
			}
			preparer.PrepareResolvedFunc = func(_ context.Context, input api.ResolvedPreparationInput) (api.PrepareResult, error) {
				if input.Corrections.Revision != resolvedCorrections.Revision {
					t.Fatalf("preparation corrections = %#v", input.Corrections)
				}
				return api.PrepareResult{
					Release: api.PreparedRelease{
						Generation: 1,
						Source:     api.SourceManifest{SourcePath: input.Input.SourcePath},
						Naming:     api.NamingFacts{ReleaseName: "Example.Release.2026.1080p-GRP"},
					},
					EffectiveInstructions: input.Input.Instructions,
					Corrections:           input.Corrections,
				}, nil
			}
			module, _ := newTestModule(t, preparer)
			command := confirmationTestCommand()
			command.CorrectionPatch = &test.patch
			if _, err := module.replaceFactInstructions(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, time.Now().UTC(), command); err != nil {
				t.Fatalf("replace remaining correction: %v", err)
			}
			if state.PendingCorrectionConfirmation == nil ||
				!slices.Equal(state.PendingCorrectionConfirmation.Fields, []api.CorrectionField{api.CorrectionFieldMetadataTitle}) ||
				state.PendingCorrectionConfirmation.Revision != resolvedCorrections.Revision {
				t.Fatalf("pending remaining confirmation = %#v", state.PendingCorrectionConfirmation)
			}
			if _, err := module.prepareRelease(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, time.Now().UTC(), PrepareReleaseCommand{
				WorkflowID: state.Workflow.ID,
				Input:      api.PrepareInput{SourcePath: `C:\releases\Example.mkv`},
			}); err != nil {
				t.Fatalf("prepare remaining correction: %v", err)
			}
			if resolveCalls != 2 || state.PendingCorrectionConfirmation != nil {
				t.Fatalf("remaining correction calls=%d pending=%#v", resolveCalls, state.PendingCorrectionConfirmation)
			}
		})
	}
}

func TestRemainingCorrectionPatchDoesNotRetainApprovedConfirmationForMismatchedRevision(t *testing.T) {
	state := partiallyConfirmedCorrectionState()
	revision := state.Corrections.Revision + 1
	patch := &api.ReleaseCorrectionPatch{
		ExpectedRevision: &revision,
		ResetFields:      []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataGenres}},
	}
	preparer := testPreparer()
	preparer.ResolveInputFunc = func(_ context.Context, input api.PrepareInput, update api.ReleaseCorrectionUpdate) (api.ResolvedPreparationInput, error) {
		if update.Confirmation != nil {
			t.Fatalf("mismatched revision retained confirmation: %#v", update.Confirmation)
		}
		return api.ResolvedPreparationInput{Input: input, Corrections: *state.Corrections}, nil
	}
	module, _ := newTestModule(t, preparer)
	command := confirmationTestCommand()
	command.CorrectionPatch = patch
	if _, err := module.replaceFactInstructions(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, time.Now().UTC(), command); err != nil {
		t.Fatalf("replace mismatched correction: %v", err)
	}
	if state.PendingCorrectionConfirmation != nil {
		t.Fatalf("mismatched revision retained pending confirmation: %#v", state.PendingCorrectionConfirmation)
	}
}

func TestCorrectionConfirmationClearsWhenIdentityChangesAgain(t *testing.T) {
	state := partiallyConfirmedCorrectionState()
	changed := state.PendingCorrectionConfirmation.CurrentBinding
	changed.ProviderIDs.TMDBID++
	preparer := testPreparer()
	preparer.ResolveInputFunc = func(_ context.Context, input api.PrepareInput, _ api.ReleaseCorrectionUpdate) (api.ResolvedPreparationInput, error) {
		return api.ResolvedPreparationInput{Input: input, Corrections: *state.Corrections}, nil
	}
	preparer.PrepareResolvedFunc = func(context.Context, api.ResolvedPreparationInput) (api.PrepareResult, error) {
		return api.PrepareResult{}, &api.StaleContentCorrectionsError{Corrections: *state.Corrections, CurrentBinding: changed}
	}
	module, _ := newTestModule(t, preparer)
	if _, err := module.prepareRelease(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, time.Now().UTC(), PrepareReleaseCommand{
		WorkflowID: state.Workflow.ID,
		Input:      api.PrepareInput{SourcePath: `C:\releases\Example.mkv`},
	}); err != nil {
		t.Fatalf("prepare changed identity: %v", err)
	}
	if state.PendingCorrectionConfirmation != nil {
		t.Fatalf("changed identity retained approved confirmation: %#v", state.PendingCorrectionConfirmation)
	}
}

func confirmationTestState() State {
	previous := api.ContentBinding{
SourceFingerprint: "previous",
 Category: api.CanonicalCategoryMovie,
 ProviderIDs: api.ProviderIDSet{TMDBID: 1},
}
	current := api.ContentBinding{
SourceFingerprint: "current",
 Category: api.CanonicalCategoryMovie,
 ProviderIDs: api.ProviderIDSet{TMDBID: 2},
}
	corrections := api.ReleaseCorrectionsSnapshot{
		Revision: 7,
		Corrections: api.StoredReleaseCorrectionsV1{
			Version:            1,
			ContentBindings:    map[api.CorrectionField]api.ContentBinding{api.CorrectionFieldMetadataTitle: previous, api.CorrectionFieldMetadataAlternateTitle: previous},
			StaleContentFields: []api.CorrectionField{api.CorrectionFieldMetadataTitle, api.CorrectionFieldMetadataAlternateTitle},
		},
	}
	return State{
		Workflow: api.ReleaseWorkflow{
			ID:       "workflow-confirmation",
			Revision: 4,
			Status:   api.WorkflowStatusBlocked,
			FactInstructions: api.ReleaseFactInstructionSnapshotRef{
				ID: "facts-confirmation", Revision: 4,
			},
			RequiredActions: []api.RequiredAction{{
				ID:               "action-confirmation",
				Kind:             api.RequiredActionConfirmCorrections,
				Status:           api.RequiredActionStatusPending,
				WorkflowRevision: 4,
				CorrectionConfirmation: &api.CorrectionConfirmation{
					Revision:         corrections.Revision,
					Fields:           slices.Clone(corrections.Corrections.StaleContentFields),
					PreviousBindings: maps.Clone(corrections.Corrections.ContentBindings),
					CurrentBinding:   current,
				},
			}},
		},
		Corrections:      &corrections,
		PreparationInput: &api.PrepareInput{SourcePath: `C:\releases\Example.mkv`},
		FactInstructions: map[api.ReleaseFactInstructionSnapshotID]api.ReleaseFactInstructionSnapshot{
			"facts-confirmation": {ID: "facts-confirmation", Revision: 4},
		},
		Releases: make(map[api.ReleaseSnapshotID]api.ReleaseSnapshot),
	}
}

func partiallyConfirmedCorrectionState() State {
	state := confirmationTestState()
	approved := state.Workflow.RequiredActions[0].CorrectionConfirmation.CurrentBinding
	previous := state.Corrections.Corrections.ContentBindings[api.CorrectionFieldMetadataTitle]
	corrections := api.ReleaseCorrectionsSnapshot{
		Revision: 8,
		Corrections: api.StoredReleaseCorrectionsV1{
			Version: 1,
			ContentBindings: map[api.CorrectionField]api.ContentBinding{
				api.CorrectionFieldMetadataTitle:  approved,
				api.CorrectionFieldMetadataGenres: previous,
			},
			StaleContentFields: []api.CorrectionField{api.CorrectionFieldMetadataGenres},
		},
	}
	state.Corrections = &corrections
	state.Workflow.RequiredActions[0].CorrectionConfirmation = &api.CorrectionConfirmation{
		Revision:         corrections.Revision,
		Fields:           slices.Clone(corrections.Corrections.StaleContentFields),
		PreviousBindings: maps.Clone(corrections.Corrections.ContentBindings),
		CurrentBinding:   approved,
	}
	state.PendingCorrectionConfirmation = &api.CorrectionConfirmation{
		Revision:         corrections.Revision,
		Fields:           []api.CorrectionField{api.CorrectionFieldMetadataTitle},
		PreviousBindings: maps.Clone(corrections.Corrections.ContentBindings),
		CurrentBinding:   approved,
	}
	return state
}

func confirmationTestCommand() ReplaceFactInstructionsCommand {
	revision := uint64(7)
	return ReplaceFactInstructionsCommand{
		WorkflowID:       "workflow-confirmation",
		ExpectedRevision: 4,
		SourcePath:       `C:\releases\Example.mkv`,
		CorrectionPatch: &api.ReleaseCorrectionPatch{
			ConfirmFields:    []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataTitle}},
			ExpectedRevision: &revision,
		},
	}
}
