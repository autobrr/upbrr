// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"errors"
	"maps"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCorrectionConfirmationRejectsChangedAuthorityBeforeWriting(t *testing.T) {
	for _, change := range []string{"missing", "revision", "source", "fields", "previous binding", "submitted identity"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			module, store, source, stale := staleConfirmationFixture(t)
			confirmation := confirmationFromStale(stale)
			update := api.ReleaseCorrectionUpdate{
				Mode: api.ReleaseCorrectionUpdatePatch,
				Patch: &api.ReleaseCorrectionPatch{
					ExpectedRevision: new(stale.Corrections.Revision),
					ConfirmFields:    []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataTitle}},
				},
				Confirmation: confirmation,
			}
			switch change {
			case "missing":
				update.Confirmation = nil
			case "revision":
				confirmation.Revision++
			case "source":
				confirmation.CurrentBinding.SourceFingerprint = "changed-source"
			case "fields":
				confirmation.Fields = []api.CorrectionField{api.CorrectionFieldReleaseNameManualYear}
			case "previous binding":
				binding := confirmation.PreviousBindings[api.CorrectionFieldMetadataTitle]
				binding.ProviderIDs.TMDBID++
				confirmation.PreviousBindings[api.CorrectionFieldMetadataTitle] = binding
			case "submitted identity":
				update.Patch.Values.Identity.TMDBID = new(3456789)
			}
			_, err := module.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, update)
			if !errors.Is(err, api.ErrCorrectionConflict) {
				t.Fatalf("expected confirmation conflict, got %v", err)
			}
			saved, err := store.LoadReleaseCorrections(t.Context(), source)
			if err != nil || !reflect.DeepEqual(saved, stale.Corrections) {
				t.Fatal("rejected confirmation changed saved intent")
			}
		})
	}
}

func TestConfirmedCorrectionRemainsBoundAcrossRestartAndDiscovery(t *testing.T) {
	for _, change := range []string{"unchanged", "provider", "source"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			module, store, source, stale := staleConfirmationFixture(t)
			confirmation := confirmationFromStale(stale)
			accepted, err := module.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{
				Mode: api.ReleaseCorrectionUpdatePatch,
				Patch: &api.ReleaseCorrectionPatch{
					ExpectedRevision: new(stale.Corrections.Revision),
					ConfirmFields:    []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataTitle}},
				},
				Confirmation: confirmation,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(accepted.ExplicitFields) != 0 || accepted.Corrections.Corrections.ContentBindings[api.CorrectionFieldMetadataTitle] != confirmation.CurrentBinding {
				t.Fatal("confirmation became fresh intent or lost its approved identity")
			}
			confirmation.Revision = accepted.Corrections.Revision
			restarted := newTestModule(t, store, &recordingCollector{})
			restarted.identity = replacementIdentityResolver{}
			if change == "provider" {
				restarted.identity = changedConfirmationIdentityResolver{}
			}
			if change == "source" {
				if err := os.WriteFile(source, []byte("replacement media"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			resolved, err := restarted.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{
				Mode:         api.ReleaseCorrectionUpdateInherit,
				Confirmation: confirmation,
			})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := restarted.PrepareResolved(t.Context(), resolved)
			if change == "unchanged" {
				if err != nil || store.commitCount() != 2 || prepared.EffectiveInstructions.Metadata.Title == nil {
					t.Fatalf("confirmed identity did not publish after restart: %v", err)
				}
				return
			}
			changed, ok := errors.AsType[*api.StaleContentCorrectionsError](err)
			if !ok || !slices.Contains(changed.Corrections.Corrections.StaleContentFields, api.CorrectionFieldMetadataTitle) || store.commitCount() != 1 {
				t.Fatalf("changed identity reused old confirmation: %v", err)
			}
			if changed.CurrentBinding == confirmation.CurrentBinding {
				t.Fatal("replacement action retained obsolete identity evidence")
			}
		})
	}
}

func TestPartialCorrectionConfirmationDoesNotApproveRemainingFields(t *testing.T) {
	for _, identityChange := range []string{"provider", "source"} {
		for _, resolution := range []string{"reset", "replace"} {
			t.Run(identityChange+"/"+resolution, func(t *testing.T) {
				t.Parallel()
				source := writePreparedTestFile(t, "Example.2026.mkv", "media")
				store := newMemoryStore()
				module := newTestModule(t, store, &recordingCollector{})
				_, err := module.Prepare(t.Context(), api.PrepareInput{
					SourcePath: source,
					Instructions: api.ReleaseFactInstructions{Metadata: api.MetadataOverrides{
						Title: new("Manual title"), Genres: new([]string{"Drama"}),
					}},
				})
				if err != nil {
					t.Fatal(err)
				}
				if identityChange == "source" {
					if err := os.WriteFile(source, []byte("changed media"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					module.identity = replacementIdentityResolver{}
				}
				_, err = module.Prepare(t.Context(), api.PrepareInput{SourcePath: source, Force: true})
				stale, ok := errors.AsType[*api.StaleContentCorrectionsError](err)
				if !ok || len(stale.Corrections.Corrections.StaleContentFields) != 2 {
					t.Fatalf("expected two stale corrections, got %v", err)
				}
				confirmation := confirmationFromStale(stale)
				accepted, err := module.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{
					Mode: api.ReleaseCorrectionUpdatePatch,
					Patch: &api.ReleaseCorrectionPatch{
						ExpectedRevision: new(stale.Corrections.Revision),
						ConfirmFields:    []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataTitle}},
					},
					Confirmation: confirmation,
				})
				if err != nil {
					t.Fatal(err)
				}
				confirmation.Revision = accepted.Corrections.Revision
				confirmation.Fields = []api.CorrectionField{api.CorrectionFieldMetadataTitle}
				resolved, err := module.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{
					Mode: api.ReleaseCorrectionUpdateInherit, Confirmation: confirmation,
				})
				if err != nil {
					t.Fatal(err)
				}
				_, err = module.PrepareResolved(t.Context(), resolved)
				remaining, ok := errors.AsType[*api.StaleContentCorrectionsError](err)
				if !ok || !slices.Equal(remaining.Corrections.Corrections.StaleContentFields, []api.CorrectionField{api.CorrectionFieldMetadataGenres}) {
					t.Fatalf("partial confirmation approved another field: %v", err)
				}
				if remaining.Corrections.Corrections.ContentBindings[api.CorrectionFieldMetadataTitle] != confirmation.CurrentBinding || store.commitCount() != 1 {
					t.Fatal("partial confirmation lost its binding or published incomplete input")
				}
				patch := &api.ReleaseCorrectionPatch{ExpectedRevision: new(remaining.Corrections.Revision)}
				if resolution == "reset" {
					patch.ResetFields = []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataGenres}}
				} else {
					patch.Values.Metadata.Genres = new([]string{"Mystery"})
				}
				confirmation.Revision = remaining.Corrections.Revision
				accepted, err = module.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{
					Mode:         api.ReleaseCorrectionUpdatePatch,
					Patch:        patch,
					Confirmation: confirmation,
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(accepted.Corrections.Corrections.StaleContentFields) != 0 || accepted.Input.Instructions.Metadata.Title == nil {
					t.Fatal("resolving the remaining field invalidated the approved title")
				}
				confirmation.Revision = accepted.Corrections.Revision
				resolved, err = module.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{
					Mode: api.ReleaseCorrectionUpdateInherit, Confirmation: confirmation,
				})
				if err != nil {
					t.Fatal(err)
				}
				resolved.ExplicitFields = accepted.ExplicitFields
				prepared, err := module.PrepareResolved(t.Context(), resolved)
				if err != nil || store.commitCount() != 2 || prepared.EffectiveInstructions.Metadata.Title == nil {
					t.Fatalf("resolving the remaining field did not publish confirmed input: %v", err)
				}
			})
		}
	}
}

func staleConfirmationFixture(t *testing.T) (*Module, *memoryStore, string, *api.StaleContentCorrectionsError) {
	t.Helper()
	source := writePreparedTestFile(t, "Example.2026.mkv", "media")
	store := newMemoryStore()
	module := newTestModule(t, store, &recordingCollector{})
	_, err := module.Prepare(t.Context(), api.PrepareInput{
		SourcePath:   source,
		Instructions: api.ReleaseFactInstructions{Metadata: api.MetadataOverrides{Title: new("Manual title")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	module.identity = replacementIdentityResolver{}
	_, err = module.Prepare(t.Context(), api.PrepareInput{SourcePath: source, Force: true})
	stale, ok := errors.AsType[*api.StaleContentCorrectionsError](err)
	if !ok {
		t.Fatalf("expected identity confirmation, got %v", err)
	}
	return module, store, source, stale
}

func confirmationFromStale(stale *api.StaleContentCorrectionsError) *api.CorrectionConfirmation {
	return &api.CorrectionConfirmation{
		Revision:         stale.Corrections.Revision,
		Fields:           slices.Clone(stale.Corrections.Corrections.StaleContentFields),
		PreviousBindings: maps.Clone(stale.Corrections.Corrections.ContentBindings),
		CurrentBinding:   stale.CurrentBinding,
	}
}

type changedConfirmationIdentityResolver struct{}

func (changedConfirmationIdentityResolver) Resolve(ctx context.Context, request externalidentity.Request) (externalidentity.Result, error) {
	result, err := (staticIdentityResolver{}).Resolve(ctx, request)
	result.Identity.TMDBID = 3456789
	return result, err
}
