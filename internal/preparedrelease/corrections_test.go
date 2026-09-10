// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/externalidentity"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCorrectionsPersistFalseResetAndFailedCollection(t *testing.T) {
	t.Parallel()
	source := writePreparedTestFile(t, "Example.2026.mkv", "media")
	store := newMemoryStore()
	module := newTestModule(t, store, &recordingCollector{})
	input := api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{
		ReleaseName: api.ReleaseNameOverrides{Tag: new("GRP")}, Metadata: api.MetadataOverrides{Commentary: new(false)},
	}}
	accepted, err := module.ResolveInput(t.Context(), input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Corrections.Revision == 0 {
		t.Fatal("explicit intent has no saved revision")
	}
	restarted := newTestModule(t, store, &recordingCollector{})
	loaded, err := restarted.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Input.Instructions.Metadata.Commentary == nil || *loaded.Input.Instructions.Metadata.Commentary {
		t.Fatal("explicit false did not survive restart")
	}
	if *loaded.Input.Instructions.ReleaseName.Tag != "GRP" {
		t.Fatal("group was lost")
	}
	reset, err := restarted.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{
		Mode:  api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{ExpectedRevision: new(loaded.Corrections.Revision), ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameTag}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reset.Input.Instructions.ReleaseName.Tag != nil || reset.Input.Instructions.Metadata.Commentary == nil {
		t.Fatal("field reset changed unrelated authority")
	}
	store.setCommitError(errors.New("provider/generation failure"))
	if _, err := restarted.PrepareResolved(t.Context(), reset); err == nil {
		t.Fatal("expected generation failure")
	}
	saved, err := store.LoadReleaseCorrections(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != reset.Corrections.Revision || saved.Corrections.ReleaseName.Tag != nil || saved.Corrections.Metadata.Commentary == nil {
		t.Fatal("failed preparation erased accepted intent")
	}
}

func TestProviderOwnedCorrectionsAreRetiredOnRestart(t *testing.T) {
	for _, category := range []api.CanonicalCategory{api.CanonicalCategoryMovie, api.CanonicalCategoryTV} {
		t.Run(string(category), func(t *testing.T) {
			t.Parallel()
			source := writePreparedTestFile(t, "Example.2026.mkv", "media")
			store := newMemoryStore()
			_, err := store.CompareAndSwapReleaseCorrections(t.Context(), source, 0, api.StoredReleaseCorrectionsV1{
				Version:     1,
				ReleaseName: api.ReleaseNameOverrides{Category: new(string(category)), ManualYear: new(2001)},
				Metadata: api.MetadataOverrides{
					Title:          new("Old title"),
					OriginalTitle:  new("Old original"),
					AlternateTitle: new("Retained AKA"),
				},
				ContentBindings: map[api.CorrectionField]api.ContentBinding{
					api.CorrectionFieldMetadataTitle:         {SourceFingerprint: "old"},
					api.CorrectionFieldMetadataOriginalTitle: {SourceFingerprint: "old"},
				},
				StaleContentFields: []api.CorrectionField{api.CorrectionFieldMetadataTitle, api.CorrectionFieldMetadataOriginalTitle},
			})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				module := newTestModule(t, store, &recordingCollector{})
				resolved, resolveErr := module.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
				if resolveErr != nil {
					t.Fatal(resolveErr)
				}
				instructions := resolved.Input.Instructions
				if instructions.Metadata.Title != nil || instructions.Metadata.OriginalTitle != nil ||
					instructions.Metadata.AlternateTitle == nil || *instructions.Metadata.AlternateTitle != "Retained AKA" {
					t.Fatal("provider-owned titles replayed or alternate title was lost")
				}
				if category == api.CanonicalCategoryTV && instructions.ReleaseName.ManualYear != nil {
					t.Fatal("TV year override survived restart")
				}
				if category == api.CanonicalCategoryMovie && (instructions.ReleaseName.ManualYear == nil || *instructions.ReleaseName.ManualYear != 2001) {
					t.Fatal("movie year override was lost")
				}
				if len(resolved.Corrections.Corrections.StaleContentFields) != 0 || len(resolved.Corrections.Corrections.ContentBindings) != 0 {
					t.Fatal("retired title overrides still require confirmation")
				}
			}
		})
	}
}

func TestResolvedTVCategoryDiscardsYearBeforeGenerationCommit(t *testing.T) {
	t.Parallel()
	source := writePreparedTestFile(t, "Example.S01E01.mkv", "media")
	store := newMemoryStore()
	module := newTestModule(t, store, &recordingCollector{})
	module.identity = tvCorrectionIdentityResolver{}
	result, err := module.Prepare(t.Context(), api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{
		ReleaseName: api.ReleaseNameOverrides{ManualYear: new(2001)},
		Metadata:    api.MetadataOverrides{Title: new("Blocked title"), OriginalTitle: new("Blocked original")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.EffectiveInstructions.ReleaseName.ManualYear != nil || result.EffectiveInstructions.Metadata.Title != nil ||
		result.EffectiveInstructions.Metadata.OriginalTitle != nil {
		t.Fatal("effective instructions retained provider-owned overrides")
	}
	saved, err := store.LoadReleaseCorrections(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Corrections.ReleaseName.ManualYear != nil || saved.Corrections.Metadata.Title != nil || saved.Corrections.Metadata.OriginalTitle != nil ||
		len(saved.Corrections.ContentBindings) != 0 {
		t.Fatal("generation commit retained provider-owned corrections or their bindings")
	}
}

type tvCorrectionIdentityResolver struct{}

func (tvCorrectionIdentityResolver) Resolve(ctx context.Context, request externalidentity.Request) (externalidentity.Result, error) {
	result, err := (staticIdentityResolver{}).Resolve(ctx, request)
	result.Identity.Category = api.CanonicalCategoryTV
	return result, err
}

func TestStaleTVCorrectionResponsePersistsSanitizedFields(t *testing.T) {
	t.Parallel()
	source := writePreparedTestFile(t, "Example.mkv", "media")
	store := newMemoryStore()
	stored, err := store.CompareAndSwapReleaseCorrections(t.Context(), source, 0, api.StoredReleaseCorrectionsV1{
		Version:     1,
		ReleaseName: api.ReleaseNameOverrides{ManualYear: new(2001)},
		Metadata: api.MetadataOverrides{
			Title:          new("Old title"),
			OriginalTitle:  new("Old original"),
			AlternateTitle: new("Confirm this AKA"),
		},
		ContentBindings: map[api.CorrectionField]api.ContentBinding{
			api.CorrectionFieldReleaseNameManualYear:  {Category: api.CanonicalCategoryMovie},
			api.CorrectionFieldMetadataTitle:          {Category: api.CanonicalCategoryMovie},
			api.CorrectionFieldMetadataOriginalTitle:  {Category: api.CanonicalCategoryMovie},
			api.CorrectionFieldMetadataAlternateTitle: {Category: api.CanonicalCategoryMovie},
		},
		StaleContentFields: []api.CorrectionField{api.CorrectionFieldReleaseNameManualYear, api.CorrectionFieldMetadataTitle,
			api.CorrectionFieldMetadataOriginalTitle, api.CorrectionFieldMetadataAlternateTitle},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		module := newTestModule(t, store, &recordingCollector{})
		_, err = module.finalizeCorrectionBindings(t.Context(), api.ResolvedPreparationInput{
			Input:             api.PrepareInput{SourcePath: source},
			Corrections:       stored,
			SourceFingerprint: "current",
		}, api.ExternalIdentity{Category: api.CanonicalCategoryTV})
		stale, ok := errors.AsType[*api.StaleContentCorrectionsError](err)
		if !ok {
			t.Fatalf("expected alternate-title confirmation, got %v", err)
		}
		corrections := stale.Corrections.Corrections
		if corrections.ReleaseName.ManualYear != nil || corrections.Metadata.Title != nil || corrections.Metadata.OriginalTitle != nil ||
			!reflect.DeepEqual(corrections.StaleContentFields, []api.CorrectionField{api.CorrectionFieldMetadataAlternateTitle}) ||
			len(corrections.ContentBindings) != 1 {
			t.Fatal("confirmation response restored retired fields")
		}
		stored, err = store.LoadReleaseCorrections(t.Context(), source)
		if err != nil || !reflect.DeepEqual(stored, stale.Corrections) {
			t.Fatal("restart would load an unsanitized correction snapshot")
		}
	}
}

func TestCategoryMismatchDoesNotCommitCollectedNames(t *testing.T) {
	for _, collectedCategory := range []api.CanonicalCategory{api.CanonicalCategoryMovie, api.CanonicalCategoryTV} {
		t.Run(string(collectedCategory), func(t *testing.T) {
			t.Parallel()
			source := writePreparedTestFile(t, "Example.mkv", "media")
			store := newMemoryStore()
			_, err := store.CompareAndSwapReleaseCorrections(t.Context(), source, 0, api.StoredReleaseCorrectionsV1{
				Version:     1,
				ReleaseName: api.ReleaseNameOverrides{ManualYear: new(2001)},
				ContentBindings: map[api.CorrectionField]api.ContentBinding{
					api.CorrectionFieldReleaseNameManualYear: {Category: api.CanonicalCategoryMovie},
				},
				StaleContentFields: []api.CorrectionField{api.CorrectionFieldReleaseNameManualYear},
			})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				module := newTestModule(t, store, categoryCorrectionCollector{category: collectedCategory})
				if collectedCategory == api.CanonicalCategoryMovie {
					module.identity = tvCorrectionIdentityResolver{}
				}
				_, err := module.Prepare(t.Context(), api.PrepareInput{SourcePath: source})
				conflict, ok := errors.AsType[*api.CorrectionConflictError](err)
				if !ok || conflict.Field != api.CorrectionFieldReleaseNameCategory || store.commitCount() != 0 {
					t.Fatalf("mismatched collected names reached generation commit: %v", err)
				}
				saved, err := store.LoadReleaseCorrections(t.Context(), source)
				if err != nil {
					t.Fatal(err)
				}
				if collectedCategory == api.CanonicalCategoryMovie {
					if saved.Corrections.ReleaseName.ManualYear != nil || len(saved.Corrections.ContentBindings) != 0 || len(saved.Corrections.StaleContentFields) != 0 {
						t.Fatal("final TV category retained a forbidden year or its confirmation evidence after mismatch")
					}
				} else if saved.Corrections.ReleaseName.ManualYear == nil || *saved.Corrections.ReleaseName.ManualYear != 2001 {
					t.Fatal("final movie category lost its manual year after mismatch")
				}
			}
		})
	}
}

type categoryCorrectionCollector struct{ category api.CanonicalCategory }

func (c categoryCorrectionCollector) Collect(ctx context.Context, request preparationstate.Request) (CollectedFacts, error) {
	facts, err := (&recordingCollector{}).Collect(ctx, request)
	facts.NamingCategory = c.category
	return facts, err
}

func TestFreshContentCorrectionFinalizesBindingAndInheritedIdentityChangeStopsCommit(t *testing.T) {
	t.Parallel()
	source := writePreparedTestFile(t, "Example.2026.mkv", "media")
	store := newMemoryStore()
	module := newTestModule(t, store, &recordingCollector{})
	input := api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{Metadata: api.MetadataOverrides{AlternateTitle: new("Manual alternate title")}}}
	first, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	binding := first.Corrections.Corrections.ContentBindings[api.CorrectionFieldMetadataAlternateTitle]
	if binding.ProviderIDs.TMDBID != first.Release.Identity.TMDBID {
		t.Fatal("fresh alternate title retained a provisional identity binding")
	}
	input.Instructions = first.EffectiveInstructions
	normalized, err := normalizePrepareInput(input)
	if err != nil {
		t.Fatal(err)
	}
	want, err := preparationCompatibility(normalized.input, first.Release.Compatibility.SourceFingerprint, first.Corrections.Revision)
	if err != nil || first.Release.Compatibility != want {
		t.Fatal("generation fingerprint uses a provisional correction revision")
	}
	module.identity = replacementIdentityResolver{}
	_, err = module.Prepare(t.Context(), api.PrepareInput{SourcePath: source, Force: true})
	if _, ok := errors.AsType[*api.StaleContentCorrectionsError](err); !ok {
		t.Fatalf("expected stale content action, got %v", err)
	}
	if store.commitCount() != 1 {
		t.Fatal("stale inherited alternate title published a generation")
	}
	saved, err := store.LoadReleaseCorrections(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Corrections.StaleContentFields) != 1 || saved.Corrections.Metadata.AlternateTitle == nil {
		t.Fatal("stale alternate title was not retained for confirmation")
	}
}

type replacementIdentityResolver struct{}

func TestCorrectionsMarkedStaleBeforeCollectionStopGenerationCommit(t *testing.T) {
	for _, change := range []string{"source", "category", "provider"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			source := writePreparedTestFile(t, "Example.2026.mkv", "media")
			store := newMemoryStore()
			module := newTestModule(t, store, &recordingCollector{})
			_, err := module.Prepare(t.Context(), api.PrepareInput{SourcePath: source,
				Instructions: api.ReleaseFactInstructions{Metadata: api.MetadataOverrides{AlternateTitle: new("Manual alternate title")}},
			})
			if err != nil {
				t.Fatal(err)
			}
			input := api.PrepareInput{SourcePath: source}
			switch change {
			case "source":
				if err := os.WriteFile(source, []byte("changed media"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "category":
				input.Instructions.ReleaseName.Category = new("TV")
			case "provider":
				input.Instructions.Identity.TMDBID = new(987654)
			}
			resolved, err := module.ResolveInput(t.Context(), input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
			if err != nil {
				t.Fatal(err)
			}
			if len(resolved.Corrections.Corrections.StaleContentFields) != 1 {
				t.Fatal("expected stale alternate title before collection")
			}
			_, err = module.PrepareResolved(t.Context(), resolved)
			if _, ok := errors.AsType[*api.StaleContentCorrectionsError](err); !ok {
				t.Fatalf("expected confirmation action, got %v", err)
			}
			if store.commitCount() != 1 {
				t.Fatal("stale alternate title published a generation")
			}
		})
	}
}

func (replacementIdentityResolver) Resolve(ctx context.Context, request externalidentity.Request) (externalidentity.Result, error) {
	result, err := (staticIdentityResolver{}).Resolve(ctx, request)
	result.Identity.TMDBID = 2345678
	return result, err
}

func TestConcurrentCorrectionRejectsGenerationCommit(t *testing.T) {
	t.Parallel()
	source := writePreparedTestFile(t, "Example.2026.mkv", "media")
	store := newMemoryStore()
	module := newTestModule(t, store, concurrentCorrectionCollector{store: store})
	_, err := module.Prepare(t.Context(), api.PrepareInput{SourcePath: source})
	if !errors.Is(err, api.ErrCorrectionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
	if store.commitCount() != 0 {
		t.Fatal("obsolete collected facts were committed")
	}
}

type concurrentCorrectionCollector struct{ store *memoryStore }

func (c concurrentCorrectionCollector) Collect(ctx context.Context, request preparationstate.Request) (CollectedFacts, error) {
	_, err := c.store.UpdateReleaseCorrections(ctx, request.Input.SourcePath, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{Commentary: new(false)}}},
	})
	if err != nil {
		return CollectedFacts{}, err
	}
	return (&recordingCollector{}).Collect(ctx, request)
}

func (s *memoryStore) LoadReleaseCorrections(_ context.Context, source string) (api.ReleaseCorrectionsSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadCorrections(source), nil
}

func (s *memoryStore) loadCorrections(source string) api.ReleaseCorrectionsSnapshot {
	if snapshot, ok := s.corrections[canonicalSourceKey(source)]; ok {
		return snapshot
	}
	return api.ReleaseCorrectionsSnapshot{Corrections: api.StoredReleaseCorrectionsV1{Version: 1}}
}

func (s *memoryStore) UpdateReleaseCorrections(_ context.Context, source string, update api.ReleaseCorrectionUpdate) (api.ReleaseCorrectionsSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.loadCorrections(source)
	record, err := api.ApplyReleaseCorrectionUpdate(previous, update)
	if err != nil {
		return api.ReleaseCorrectionsSnapshot{}, fmt.Errorf("test store apply corrections: %w", err)
	}
	return s.saveCorrections(source, previous.Revision, record)
}

func (s *memoryStore) CompareAndSwapReleaseCorrections(_ context.Context, source string, revision uint64, record api.StoredReleaseCorrectionsV1) (api.ReleaseCorrectionsSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveCorrections(source, revision, record)
}

func (s *memoryStore) saveCorrections(source string, revision uint64, record api.StoredReleaseCorrectionsV1) (api.ReleaseCorrectionsSnapshot, error) {
	previous := s.loadCorrections(source)
	if previous.Revision != revision {
		return api.ReleaseCorrectionsSnapshot{}, api.ErrCorrectionConflict
	}
	if reflect.DeepEqual(previous.Corrections, record) {
		return previous, nil
	}
	if s.corrections == nil {
		s.corrections = make(map[string]api.ReleaseCorrectionsSnapshot)
	}
	current := api.ReleaseCorrectionsSnapshot{Corrections: record, Revision: revision + 1}
	s.corrections[canonicalSourceKey(source)] = current
	return current, nil
}

func (s *memoryStore) CommitPreparedReleaseWithCorrections(_ context.Context, release api.PreparedRelease, revision uint64, record api.StoredReleaseCorrectionsV1) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commitErr != nil {
		return 0, s.commitErr
	}
	cloned, err := release.Clone()
	if err != nil {
		return 0, fmt.Errorf("memory store: clone prepared release: %w", err)
	}
	snapshot, err := s.saveCorrections(release.Source.SourcePath, revision, record)
	if err != nil {
		return 0, err
	}
	s.current[canonicalSourceKey(release.Source.SourcePath)] = cloned
	s.commits++
	return snapshot.Revision, nil
}
