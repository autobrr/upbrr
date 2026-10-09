// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestContinueAitherAcknowledgementReusesPreparedSelectionFacts(t *testing.T) {
	for _, test := range []struct {
		name            string
		initialTrackers []api.TrackerID
		missingTMDB     bool
	}{
		{name: "no-initial-demand"},
		{name: "broader-initial-demand", initialTrackers: []api.TrackerID{"AITHER", "PTP"}},
		{name: "missing-provider-facts", missingTMDB: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			builtins := trackerimpl.MustNewRegistry()
			registry := trackers.NewRegistry()
			for _, name := range []string{"AITHER", "PTP", "OE"} {
				descriptor, ok := builtins.LookupDescriptor(name)
				if !ok {
					t.Fatalf("missing built-in tracker %s", name)
				}
				if err := registry.RegisterDescriptor(descriptor); err != nil {
					t.Fatal(err)
				}
			}
			cfg := config.Config{Trackers: config.TrackersConfig{
				DefaultTrackers: []string{"AITHER", "OE"},
				Trackers: map[string]config.TrackerConfig{
					"AITHER": {APIKey: "example"},
					"OE":     {APIKey: "example"},
				},
			}}
			projector, err := trackers.NewWorkflowProjector(registry, cfg, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			const releaseName = "Example.Movie.2026.1080p.WEB-DL-GRP"
			sourcePath := filepath.Join(root, "Example Movie 2026 1080p WEB-DL-GRP.mkv")
			var preparations atomic.Uint64
			prepared := func(generation api.PreparedGeneration) api.PreparedRelease {
				media := api.MediaFacts{
					Container:             "mkv",
					Source:                "Web",
					Type:                  "WEBDL",
					OriginalLanguage:      "English",
					TrackCoverageComplete: true,
					PrimaryAudioTrackID:   "main",
					AudioLanguages:        []string{"English"},
					Tracks: []api.MediaTrackFacts{{
						ID:        "main",
						Kind:      api.MediaTrackAudio,
						Role:      api.AudioRoleProgramme,
						Codec:     "AAC",
						Languages: []string{"English"},
					}},
				}
				media.LanguageFacts = mediafacts.ResolveLanguages(media)
				release := api.PreparedRelease{
					Generation: generation,
					Source: api.SourceManifest{SourcePath: sourcePath, Entries: []api.SourceManifestEntry{{
						Path: sourcePath, Type: api.SourceEntryTypeFile,
					}}},
					Naming: api.NamingFacts{
						ReleaseName: releaseName,
						Title:       "Example Movie",
						Year:        2026,
						Resolution:  "1080p",
						Group:       "GRP",
					},
					Media: media,
					Identity: api.ExternalIdentity{
						SourcePath: sourcePath,
						Generation: generation,
						Category:   api.CanonicalCategoryMovie,
						TMDBID:     1234567,
						IMDBID:     1234567,
					},
					ProviderMetadata: api.SourceScopedMetadata{
						SourcePath: sourcePath,
						Generation: generation,
						TMDB: &api.TMDBMetadata{
							TMDBID:   1234567,
							Category: "movie",
							Title:    "Example Movie",
							Year:     2026,
						},
					},
					Assessments: api.ReleaseAssessments{
						MediaInfoUniqueID:       api.UniqueIDStatusPresent,
						MediaInfoEncodeSettings: api.EncodeSettingsStatusNotApplicable,
					},
				}
				if test.missingTMDB && generation == 1 {
					release.ProviderMetadata.TMDB = nil
				}
				return release
			}
			preparer := releaseworkflow.ReleasePreparerFunc{
				PrepareFunc: func(context.Context, api.PrepareInput) (api.PrepareResult, error) {
					return api.PrepareResult{Release: prepared(api.PreparedGeneration(preparations.Add(1)))}, nil
				},
				DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
					return api.PreparedReleaseDisplay{ReleaseName: releaseName}, nil
				},
				SubjectFunc: func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
					release := prepared(input.Release.Generation)
					return api.UploadSubject{
						SourcePath:                  sourcePath,
						SourceManifest:              release.Source,
						FileList:                    []string{sourcePath},
						ReleaseName:                 releaseName,
						Source:                      release.Media.Source,
						Type:                        release.Media.Type,
						Container:                   release.Media.Container,
						LanguageFacts:               release.Media.LanguageFacts,
						AudioLanguages:              release.Media.AudioLanguages,
						Identity:                    release.Identity,
						ProviderMetadata:            release.ProviderMetadata,
						EffectiveMetadata:           release.MetadataFacts(),
						Assessments:                 release.Assessments,
						MediaInfoTextPath:           filepath.Join(root, "mediainfo.txt"),
						TrackerQuestionnaireAnswers: input.QuestionnaireAnswers,
						Release: api.ReleaseInfo{
							Title:      "Example Movie",
							Year:       2026,
							Resolution: "1080p",
							Group:      "GRP",
							Category:   "MOVIE",
						},
					}, nil
				},
				DuplicateFunc: func(_ context.Context, input api.DuplicateCheckInput) (api.DuplicateSubject, error) {
					return api.DuplicateSubject{SourcePath: input.Release.SourcePath, ReleaseName: releaseName}, nil
				},
			}
			database, err := db.Open(filepath.Join(root, "workflow.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			if err := database.Migrate(); err != nil {
				t.Fatal(err)
			}
			repository, err := releaseworkflow.NewPersistentRepository(database)
			if err != nil {
				t.Fatal(err)
			}
			dupes := &workflowDupeServiceFake{results: []api.DupeCheckResult{{
				Tracker: "AITHER",
				Status:  "completed",
				Search:  api.DupeSearchEvidence{Complete: true},
			}}}
			module, err := releaseworkflow.New(repository, releaseworkflow.NewMemoryPrivateResourceStore(), preparer,
				releaseworkflow.WithInputReadinessEvaluator(workflowInputReadiness{registry: registry}),
				releaseworkflow.WithTrackerProjectionBuilder(projector),
				releaseworkflow.WithTrackerPreflightBuilder(selectionAcknowledgementPreflight{}),
				releaseworkflow.WithDupeAssessmentBuilder(workflowDupeBuilder{service: dupes}),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = module.Shutdown(context.Background()) })
			core := &Core{workflow: module, logger: api.NopLogger{}}
			ctx := releaseworkflow.WithTrackerDecisionMode(t.Context(), releaseworkflow.TrackerDecisionModeWebUIControls)
			const owner = "selection-acknowledgement-owner"
			current, err := module.Execute(ctx, owner, releaseworkflow.CreateWorkflowCommand{})
			if err != nil {
				t.Fatal(err)
			}
			current, err = module.Execute(ctx, owner, releaseworkflow.PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: sourcePath},
				TrackerIDs:       test.initialTrackers,
			})
			if err != nil {
				t.Fatal(err)
			}
			initialState, err := repository.Load(ctx, owner, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			initialDemand, err := api.CanonicalWorkflowFingerprint(initialState.PreparationDemand)
			if err != nil {
				t.Fatal(err)
			}
			intent := api.WorkflowIntent{TrackerIDs: []api.TrackerID{"AITHER"}, Interaction: api.InteractionModeInteractive}
			settle := func(key string, goal api.WorkflowGoal) {
				t.Helper()
				deadline := time.Now().Add(30 * time.Second)
				for time.Now().Before(deadline) {
					revision := current.Workflow.Revision
					current, err = core.ContinueReleaseWorkflow(ctx, owner, api.ContinueReleaseWorkflowRequest{
						Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: revision},
						IdempotencyKey: fmt.Sprintf("%s-%d", key, revision),
						Goal:           goal,
						Intent:         intent,
					})
					if err != nil {
						t.Fatalf("continue %s: %v (%v)", key, err, errors.Unwrap(err))
					}
					for current.Operation != nil && (current.Operation.Status == api.StageStatusQueued ||
						current.Operation.Status == api.StageStatusRunning || current.Operation.Status == api.StageStatusPending ||
						current.Operation.Status == api.StageStatusReady) {
						if !time.Now().Before(deadline) {
							t.Fatalf("%s operation did not finish: %#v", key, current.Operation)
						}
						time.Sleep(5 * time.Millisecond)
						current, err = core.CurrentReleaseWorkflow(ctx, owner, current.Workflow.ID)
						if err != nil {
							t.Fatal(err)
						}
					}
					current, err = core.CurrentReleaseWorkflow(ctx, owner, current.Workflow.ID)
					if err != nil {
						t.Fatal(err)
					}
					if current.Operation != nil && current.Operation.Status == api.StageStatusFailed &&
						(!test.missingTMDB || current.Release.Release.Generation != 1 || goal != api.WorkflowGoalTrackersProjected) {
						t.Fatalf("%s operation failed: %#v", key, current.Operation)
					}
					if current.Workflow.Revision == revision {
						return
					}
				}
				t.Fatalf("%s continuation did not settle", key)
			}
			projection := func() api.TrackerReleaseProjection {
				t.Helper()
				if current.Selection == nil || !slices.Equal(current.Selection.TrackerIDs, []api.TrackerID{"AITHER"}) ||
					current.Projections == nil || len(current.Projections.Projections) != 1 || current.Projections.Projections[0].TrackerID != "AITHER" {
					t.Fatalf("continuation lost AITHER-only scope: selection=%#v projections=%#v", current.Selection, current.Projections)
				}
				return current.Projections.Projections[0]
			}
			pendingAction := func() api.RequiredAction {
				t.Helper()
				for _, action := range projection().RequiredActions {
					if action.Kind == api.RequiredActionAuthorizeRules && action.Status == api.RequiredActionStatusPending {
						return action
					}
				}
				t.Fatalf("missing AITHER rule acknowledgement: %#v", projection())
				return api.RequiredAction{}
			}
			settle("project-aither", api.WorkflowGoalTrackersProjected)
			if !slices.ContainsFunc(projection().PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
				return decision.Code == "modified_release" && decision.Disposition == api.RuleDispositionWaivable && decision.Blocking
			}) {
				t.Fatalf("fixture did not produce the modified-release warning: %#v", projection())
			}
			if test.missingTMDB {
				intent.TrackerIDs = nil
				settle("collect-missing-facts", api.WorkflowGoalDuplicatesDecided)
				if preparations.Load() != 2 || current.Release.Release.Generation != 2 || projection().RuleAuthorizationFingerprint != "" || pendingAction().ID == "" {
					t.Fatalf("missing facts did not require fresh preparation and acknowledgement: calls=%d release=%#v projection=%#v", preparations.Load(), current.Release, projection())
				}
				if len(dupes.projections) != 0 {
					t.Fatalf("new generation bypassed acknowledgement: checked=%#v", dupes.projections)
				}
				return
			}
			pending := pendingAction()
			intent.TrackerIDs = nil
			current, err = core.ContinueReleaseWorkflow(ctx, owner, api.ContinueReleaseWorkflowRequest{
				Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
				IdempotencyKey: "acknowledge-aither",
				Goal:           api.WorkflowGoalDuplicatesDecided,
				Intent:         intent,
				Answers: []api.RequiredActionAnswer{{
					ActionID:         pending.ID,
					WorkflowRevision: current.Workflow.Revision,
					Confirmed:        new(true),
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			authorized := projection()
			if authorized.RuleAuthorizationFingerprint == "" || authorized.RuleAuthorizationFingerprint != authorized.WaivableRuleFingerprint {
				t.Fatalf("warning was not acknowledged before duplicate continuation: %#v", authorized)
			}
			generation := current.Release.Release.Generation
			if preparations.Load() != 1 || generation != 1 {
				t.Fatalf("projection-only acknowledgement prepared again: calls=%d generation=%d", preparations.Load(), generation)
			}
			// The browser omits tracker IDs after selection; that must retain the exact scope and authority.
			intent.TrackerIDs = nil
			settle("check-aither-duplicates", api.WorkflowGoalDuplicatesDecided)
			settle("repeat-aither-duplicates", api.WorkflowGoalDuplicatesDecided)
			got := projection()
			if preparations.Load() != 1 || current.Release.Release.Generation != generation ||
				got.RuleAuthorizationFingerprint != authorized.RuleAuthorizationFingerprint || got.WaivableRuleFingerprint != authorized.WaivableRuleFingerprint {
				t.Fatalf("selection refresh reset accepted warning: preparations=%d generation=%d->%d authorization=%q->%q",
					preparations.Load(), generation, current.Release.Release.Generation, authorized.RuleAuthorizationFingerprint, got.RuleAuthorizationFingerprint)
			}
			persisted, err := repository.Load(ctx, owner, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			retainedDemand, err := api.CanonicalWorkflowFingerprint(persisted.PreparationDemand)
			if err != nil || retainedDemand != initialDemand {
				t.Fatalf("selection rewrote the original preparation demand: before=%#v after=%#v error=%v", initialState.PreparationDemand, persisted.PreparationDemand, err)
			}
			if !got.DupeReady || current.InputReadiness == nil || !slices.Equal(current.InputReadiness.SelectedTrackerIDs, []api.TrackerID{"AITHER"}) ||
				current.Dupes == nil || len(current.Dupes.Results) != 1 || current.Dupes.Results[0].TrackerID != "AITHER" || current.Dupes.Results[0].Decision != api.DupeDecisionNoMatch ||
				len(dupes.projections) != 1 || dupes.projections[0].TrackerID != "AITHER" {
				t.Fatalf("acknowledged AITHER did not complete exactly one duplicate check: projection=%#v dupes=%#v checked=%#v", got, current.Dupes, dupes.projections)
			}
		})
	}
}

// selectionAcknowledgementPreflight isolates remote checks while retaining the real projection's policy gates.
type selectionAcknowledgementPreflight struct{}

func (selectionAcknowledgementPreflight) Build(
	_ context.Context,
	_ api.UploadSubject,
	_ api.TrackerCatalogSnapshot,
	_ api.TrackerRuntimeSnapshot,
	initial api.TrackerReleaseProjectionSet,
	now time.Time,
) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(initial)
	if err != nil {
		return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("fingerprint selection preflight: %w", err)
	}
	assessment := api.TrackerPreflightAssessment{
		InputFingerprint: fingerprint,
		ExpiresAt:        now.Add(time.Hour),
		ExecutionMode:    initial.ExecutionMode,
	}
	for _, projection := range initial.Projections {
		fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
		if err != nil {
			return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("fingerprint selection projection: %w", err)
		}
		state := api.TrackerPreflightStateReady
		if !projection.DupeReady {
			state = api.TrackerPreflightStateActionRequired
		}
		assessment.Results = append(assessment.Results, api.TrackerPreflightResult{
			TrackerID:             projection.TrackerID,
			State:                 state,
			AuthReady:             true,
			ClaimsReady:           true,
			BannedGroupsReady:     true,
			RemoteMetadataReady:   true,
			ConfigFingerprint:     projection.ConfigFingerprint,
			ProjectionFingerprint: fingerprint,
			RequiredActions:       projection.RequiredActions,
			AssessedAt:            now,
			FreshUntil:            now.Add(time.Hour),
		})
	}
	return assessment, slices.Clone(initial.Projections), nil
}
