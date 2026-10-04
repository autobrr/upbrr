// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestOpenActiveInputPreservesCorrectionReviewThroughReverification(t *testing.T) {
	for _, scenario := range []string{"partial keep then reset", "changed source mixed patch", "changed provider identity", "stale correction revision"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newActiveCorrectionFixture(t)
			patch := &api.ReleaseCorrectionPatch{
				ExpectedRevision: new(fixture.stored.Revision),
				ConfirmFields:    []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataAlternateTitle}},
			}
			fixture.mu.Lock()
			switch scenario {
			case "changed source mixed patch":
				fixture.binding.SourceFingerprint = "changed-again"
				patch.Values.Metadata.OriginalLanguage = new("fr")
				patch.ResetFields = []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataGenres}}
			case "changed provider identity":
				fixture.binding.ProviderIDs.TMDBID++
			case "stale correction revision":
				*patch.ExpectedRevision--
			}
			fixture.mu.Unlock()
			view, err := fixture.open(t, patch, "apply-review")
			if scenario == "stale correction revision" {
				if err == nil {
					t.Fatal("stale correction revision was accepted")
				}
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
				if fixture.patchCalls != 0 || fixture.stored.Revision != 7 {
					t.Fatalf("stale patch changed corrections: calls=%d revision=%d", fixture.patchCalls, fixture.stored.Revision)
				}
				return
			}
			if err != nil {
				t.Fatalf("apply correction through active input: %v: %v", err, errors.Unwrap(err))
			}
			if view.Current == nil || view.Current.Release != nil || len(view.Current.Workflow.RequiredActions) != 1 {
				t.Fatalf("remaining correction review = %#v", view)
			}
			review := view.Current.Workflow.RequiredActions[0].CorrectionConfirmation
			if review == nil || review.CurrentBinding != fixture.binding {
				t.Fatalf("review binding = %#v, want %#v", review, fixture.binding)
			}
			fixture.mu.Lock()
			if fixture.verifications != 2 || fixture.stored.Corrections.ReleaseName.Edition == nil || *fixture.stored.Corrections.ReleaseName.Edition != "Collector" {
				t.Fatalf("reverification or unrelated correction lost: calls=%d stored=%#v", fixture.verifications, fixture.stored)
			}
			if scenario == "changed source mixed patch" {
				if fixture.patchCalls != 0 || fixture.stored.Corrections.Metadata.OriginalLanguage != nil || fixture.stored.Corrections.Metadata.Genres == nil ||
					!slices.Contains(review.Fields, api.CorrectionFieldMetadataAlternateTitle) {
					t.Fatalf("changed source applied stale mixed choices: calls=%d stored=%#v", fixture.patchCalls, fixture.stored)
				}
			}
			if scenario == "changed provider identity" && !slices.Contains(review.Fields, api.CorrectionFieldMetadataAlternateTitle) {
				t.Fatal("changed provider identity accepted the previous confirmation")
			}
			fixture.mu.Unlock()
			if scenario == "changed source mixed patch" {
				replayed, err := fixture.open(t, patch, "apply-review")
				if err != nil || replayed.Current == nil || replayed.Current.Release != nil ||
					len(replayed.Current.Workflow.RequiredActions) != 1 {
					t.Fatalf("discarded mixed patch replay did not preserve review: view=%#v err=%v", replayed, err)
				}
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
				if fixture.patchCalls != 0 || fixture.stored.Corrections.Metadata.OriginalLanguage != nil || fixture.stored.Corrections.Metadata.Genres == nil {
					t.Fatalf("replay accepted discarded choices: calls=%d stored=%#v", fixture.patchCalls, fixture.stored)
				}
			}
			if scenario != "partial keep then reset" {
				return
			}
			if !slices.Equal(review.Fields, []api.CorrectionField{api.CorrectionFieldMetadataGenres}) {
				t.Fatalf("partial keep approved unrelated fields: %v", review.Fields)
			}
			replayed, err := fixture.open(t, patch, "apply-review")
			if err != nil || replayed.Current == nil || replayed.Current.Workflow.Revision != view.Current.Workflow.Revision {
				t.Fatalf("successful correction replay changed the outcome: view=%#v err=%v", replayed, err)
			}
			changedPatch := *patch
			changedPatch.Values.Metadata.OriginalLanguage = new("fr")
			if _, err := fixture.open(t, &changedPatch, "apply-review"); !errors.Is(err, releaseworkflow.ErrIdempotencyConflict) {
				t.Fatalf("changed correction reused admission key: %v", err)
			}
			ready, err := fixture.open(t, &api.ReleaseCorrectionPatch{
				ExpectedRevision: new(review.Revision),
				ResetFields:      []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataGenres}},
			}, "reset-remaining")
			if err != nil || ready.Current == nil || ready.Current.Release == nil || len(ready.Current.Workflow.RequiredActions) != 0 {
				t.Fatalf("remaining reset did not prepare: view=%#v err=%v", ready, err)
			}
		})
	}
}

type activeCorrectionFixture struct {
	mu            sync.Mutex
	core          *Core
	source        string
	stored        api.ReleaseCorrectionsSnapshot
	binding       api.ContentBinding
	patchCalls    int
	verifications int
}

func newActiveCorrectionFixture(t *testing.T) *activeCorrectionFixture {
	t.Helper()
	fixture := &activeCorrectionFixture{
		source: filepath.Join(t.TempDir(), "Example.Release.mkv"),
		binding: api.ContentBinding{
			SourceFingerprint: "verified",
			Category:          api.CanonicalCategoryMovie,
			ProviderIDs:       api.ProviderIDSet{TMDBID: 2},
		},
		stored: api.ReleaseCorrectionsSnapshot{Revision: 7, Corrections: api.StoredReleaseCorrectionsV1{
			Version:     1,
			Metadata:    api.MetadataOverrides{AlternateTitle: new("Saved alternate"), Genres: new([]string{"Drama"})},
			ReleaseName: api.ReleaseNameOverrides{Edition: new("Collector")},
			ContentBindings: map[api.CorrectionField]api.ContentBinding{
				api.CorrectionFieldMetadataAlternateTitle: {SourceFingerprint: "previous"},
				api.CorrectionFieldMetadataGenres:         {SourceFingerprint: "previous"},
			},
			StaleContentFields: []api.CorrectionField{api.CorrectionFieldMetadataAlternateTitle, api.CorrectionFieldMetadataGenres},
		}},
	}
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
	workflow, err := releaseworkflow.New(persistent, releaseworkflow.NewMemoryPrivateResourceStore(), releaseworkflow.ReleasePreparerFunc{
		ResolveInputFunc: func(_ context.Context, input api.PrepareInput, update api.ReleaseCorrectionUpdate) (api.ResolvedPreparationInput, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if update.Mode == api.ReleaseCorrectionUpdatePatch {
				accepted, err := api.ApplyReleaseCorrectionUpdate(fixture.stored, update)
				if err != nil {
					return api.ResolvedPreparationInput{}, fmt.Errorf("apply test corrections: %w", err)
				}
				for _, field := range update.Patch.ConfirmFields {
					if update.Confirmation == nil || update.Confirmation.CurrentBinding.SourceFingerprint != fixture.binding.SourceFingerprint {
						return api.ResolvedPreparationInput{}, &api.CorrectionConflictError{Reason: "source changed"}
					}
					accepted.ContentBindings[field.Field] = update.Confirmation.CurrentBinding
					accepted.StaleContentFields = slices.DeleteFunc(accepted.StaleContentFields, func(stale api.CorrectionField) bool { return stale == field.Field })
				}
				fixture.stored.Corrections = accepted
				fixture.stored.Revision++
				fixture.patchCalls++
			}
			input.Instructions.Metadata = fixture.stored.Corrections.Metadata
			input.Instructions.ReleaseName = fixture.stored.Corrections.ReleaseName
			stored, err := fixture.stored.Clone()
			if err != nil {
				return api.ResolvedPreparationInput{}, fmt.Errorf("clone resolved test corrections: %w", err)
			}
			return api.ResolvedPreparationInput{
				Input:             input,
				Corrections:       stored,
				SourceFingerprint: fixture.binding.SourceFingerprint,
			}, nil
		},
		PrepareResolvedFunc: func(_ context.Context, input api.ResolvedPreparationInput) (api.PrepareResult, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			for field, binding := range fixture.stored.Corrections.ContentBindings {
				if binding != fixture.binding && !slices.Contains(fixture.stored.Corrections.StaleContentFields, field) {
					fixture.stored.Corrections.StaleContentFields = append(fixture.stored.Corrections.StaleContentFields, field)
					fixture.stored.Revision++
				}
			}
			slices.Sort(fixture.stored.Corrections.StaleContentFields)
			stored, err := fixture.stored.Clone()
			if err != nil {
				return api.PrepareResult{}, fmt.Errorf("clone prepared test corrections: %w", err)
			}
			if len(stored.Corrections.StaleContentFields) > 0 {
				return api.PrepareResult{}, &api.StaleContentCorrectionsError{Corrections: stored, CurrentBinding: fixture.binding}
			}
			return api.PrepareResult{
				Release:               api.PreparedRelease{Generation: 1, Source: api.SourceManifest{SourcePath: input.Input.SourcePath}},
				Corrections:           stored,
				EffectiveInstructions: input.Input.Instructions,
			}, nil
		},
		DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
			return api.PreparedReleaseDisplay{}, nil
		},
	}, releaseworkflow.WithActiveInputs(repo, func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.verifications++
		return api.InputRecord{
			CanonicalPath: input.SourcePath,
			SourceVersion: fixture.binding.SourceFingerprint,
			Manifest:      []byte(fmt.Sprintf(`{"identity":{"digest":%q}}`, fixture.binding.SourceFingerprint)),
		}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workflow.Shutdown(context.Background()) })
	fixture.core = &Core{workflow: workflow, logger: api.NopLogger{}}
	if _, err := fixture.open(t, nil, "initial-open"); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f *activeCorrectionFixture) open(t *testing.T, patch *api.ReleaseCorrectionPatch, key string) (api.ActiveInputSnapshot, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	before, err := f.core.GetActiveInput(ctx, "input-owner")
	if err != nil {
		return api.ActiveInputSnapshot{}, err
	}
	view, err := f.core.OpenActiveInput(ctx, "input-owner", api.OpenActiveInputRequest{
		ExpectedRevision: before.Revision,
		Request: api.ContinueReleaseWorkflowRequest{
			Goal:           api.WorkflowGoalPrepared,
			IdempotencyKey: key,
			Intent:         api.WorkflowIntent{Preparation: &api.PrepareInput{SourcePath: f.source}, CorrectionPatch: patch},
		},
	})
	if err != nil {
		return view, err
	}
	for {
		current := view.Current
		if current == nil {
			return view, errors.New("active workflow missing")
		}
		if current.Operation == nil || (current.Operation.Status != api.StageStatusQueued && current.Operation.Status != api.StageStatusRunning) {
			if current.Workflow.Status == api.WorkflowStatusBlocked || current.Release != nil {
				return view, nil
			}
			_, err = f.core.ContinueReleaseWorkflow(ctx, "input-owner", api.ContinueReleaseWorkflowRequest{
				Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
				Goal:           api.WorkflowGoalPrepared,
				IdempotencyKey: key,
				Intent:         api.WorkflowIntent{Preparation: &api.PrepareInput{SourcePath: f.source, Instructions: current.FactInstructions.Instructions}},
			})
			if err != nil {
				return view, err
			}
		}
		select {
		case <-ctx.Done():
			return view, fmt.Errorf("wait for test input preparation: %w", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
		view, err = f.core.GetActiveInput(ctx, "input-owner")
		if err != nil {
			return view, err
		}
	}
}
