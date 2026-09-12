// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompositeUploadHydrationRestoresDurableDemandForPreparedRelease(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	if err := os.WriteFile(sourcePath, []byte("synthetic media"), 0o600); err != nil {
		t.Fatalf("write preparation source: %v", err)
	}
	databasePath := filepath.Join(t.TempDir(), "composite-hydration.sqlite")
	repoA, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("open first repository: %v", err)
	}
	if err := repoA.Migrate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("migrate first repository: %v", err)
	}
	persistentA, err := NewPersistentRepository(repoA)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first persistent repository: %v", err)
	}
	preparerA := newCompositePersistencePreparer(t, repoA)
	requirements := api.MetadataRequirementSet{
		Version: "durable-composite-hydration-v1",
		Requirements: []api.MetadataRequirement{{
			Scope:       api.MetadataRequirementScopeAny,
			AnyOf:       []api.MetadataRequirementField{"original_title"},
			Disposition: api.RuleDispositionStrict,
		}},
	}
	moduleA, err := New(
		persistentA,
		NewMemoryPrivateResourceStore(),
		preparerA,
		WithClock(fixedClock{now: time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithInputReadinessEvaluator(compositeUploadDemandEvaluator{requirements: requirements}),
	)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first workflow module: %v", err)
	}
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "durable-composite-hydration")
	request.Source.Path = sourcePath
	session, instructions, err := normalizeCompositeUploadRequest(request)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("normalize composite request: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{
		WorkflowID:          "durable-composite-hydration",
		Instructions:        instructions,
		IdempotencyKey:      request.IdempotencyKey,
		RequestFingerprint:  session.RequestFingerprint,
		TrackerDecisionMode: TrackerDecisionModePostDupeGate,
		Composite:           session,
	})
	prepared := executeCommand(t, moduleA, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            *session.Intent.Preparation,
		TrackerIDs:       session.Intent.TrackerIDs,
		IdempotencyKey:   "prepare-durable-composite-hydration",
	})
	if prepared.Release == nil {
		_ = repoA.Close()
		t.Fatal("prepared release is unavailable")
	}
	if err := repoA.Close(); err != nil {
		t.Fatalf("close first repository: %v", err)
	}

	repoB, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repoB.Close() })
	if err := repoB.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}
	persistentB, err := NewPersistentRepository(repoB)
	if err != nil {
		t.Fatalf("new reopened persistent repository: %v", err)
	}
	preparerB := newCompositePersistencePreparer(t, repoB)
	moduleB, err := New(persistentB, NewMemoryPrivateResourceStore(), preparerB)
	if err != nil {
		t.Fatalf("new restarted workflow module: %v", err)
	}
	state, err := persistentB.Load(t.Context(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatalf("load restarted durable state: %v", err)
	}
	if state.Workflow.Release == nil {
		t.Fatal("restarted workflow release is unavailable")
	}
	release, ok := state.Releases[state.Workflow.Release.ID]
	if !ok {
		t.Fatalf("restarted release snapshot %q is unavailable", state.Workflow.Release.ID)
	}
	current := CommandResult{Release: &release}
	if state.Composite == nil || state.Composite.Intent.Preparation == nil ||
		!reflect.DeepEqual(state.PreparationDemand, requirements) || !reflect.DeepEqual(state.Composite.Intent.Preparation.MetadataRequirements, api.MetadataRequirementSet{}) {
		t.Fatalf("restarted composite preparation state = %#v", state)
	}
	if err := moduleB.hydrateCompositePreparedRelease(t.Context(), current, state.Composite, state.PreparationDemand); err != nil {
		t.Fatalf("hydrate compatible prepared release: %v", err)
	}

	forced := *state.Composite.Intent.Preparation
	forced.SourcePath = current.Release.Release.Source.SourcePath
	forced.MetadataRequirements = state.PreparationDemand
	forced.Force = true
	if _, err := preparerB.Prepare(t.Context(), forced); err != nil {
		t.Fatalf("replace prepared generation: %v", err)
	}
	if err := moduleB.hydrateCompositePreparedRelease(t.Context(), current, state.Composite, state.PreparationDemand); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("hydrate changed generation error = %v", err)
	}
}

func newCompositePersistencePreparer(t *testing.T, store preparedrelease.Store) *preparedrelease.Module {
	t.Helper()
	preparer, err := preparedrelease.New(store, compositePersistenceIdentityResolver{}, compositePersistenceCollector{})
	if err != nil {
		t.Fatalf("new prepared release module: %v", err)
	}
	return preparer
}

type compositePersistenceIdentityResolver struct{}

func (compositePersistenceIdentityResolver) Resolve(
	_ context.Context,
	request externalidentity.Request,
) (externalidentity.Result, error) {
	now := time.Now().UTC()
	return externalidentity.Result{
		Identity: api.ExternalIdentity{
			SourcePath: request.SourcePath,
			Generation: request.Generation,
			TMDBID:     1234567,
			Category:   api.CanonicalCategoryMovie,
			Provenance: api.IdentityProvenanceSet{
				TMDB:     api.IdentityProvenanceProvider,
				Category: api.IdentityProvenanceProvider,
			},
			Conflict: api.IdentityConflictNone,
			Resolution: api.IdentityResolutionKey{
				SourceFingerprint: request.SourceFingerprint,
				IntentFingerprint: "durable-composite-hydration",
				ContractVersion:   externalidentity.ContractVersion,
			},
			ResolvedAt: now,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: request.SourcePath,
			Generation: request.Generation,
			UpdatedAt:  now,
		},
	}, nil
}

type compositePersistenceCollector struct{}

func (compositePersistenceCollector) Collect(
	_ context.Context,
	request preparationstate.Request,
) (preparedrelease.CollectedFacts, error) {
	return preparedrelease.CollectedFacts{
		Naming: api.NamingFacts{
			Filename:         filepath.Base(request.Manifest.SourcePath),
			ReleaseName:      "Example.Release.2026.1080p-GRP",
			NamePresentation: api.ReleaseNamePresentation{Version: api.ReleaseNamePresentationVersionV1, OmitYear: true},
		},
		Assessments: api.ReleaseAssessments{
			MediaInfoUniqueID:       api.UniqueIDStatusPresent,
			MediaInfoEncodeSettings: api.EncodeSettingsStatusMissing,
			Naming:                  api.NamingAssessment{Status: api.NamingStatusComplete},
		},
	}, nil
}

func (compositePersistenceCollector) HydratePrivateResources(
	_ context.Context,
	request preparationstate.Request,
) (preparedrelease.CollectedResources, error) {
	return preparedrelease.CollectedResources{SourcePath: request.Manifest.SourcePath}, nil
}

var _ preparedrelease.Collector = compositePersistenceCollector{}
var _ preparedrelease.IdentityResolver = compositePersistenceIdentityResolver{}
