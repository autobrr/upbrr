// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/db/dbfixture"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/tl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPersistentTLPolicyUpgradeRebuildsBeforeDownstreamWork(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                                  string
		legacy, reviewed, betaOnly, completed bool
		closed                                api.WorkflowStatus
		reconciliation                        api.WorkflowExternalEffectKind
	}{
		{name: "old pre-dupe API eligibility", legacy: true},
		{
			name:           "old image reconciliation",
			legacy:         true,
			reviewed:       true,
			reconciliation: api.WorkflowExternalEffectImageHosting,
		},
		{
			name:           "old submission reconciliation",
			legacy:         true,
			reviewed:       true,
			reconciliation: api.WorkflowExternalEffectTrackerSubmission,
		},
		{
			name:           "current image reconciliation",
			reviewed:       true,
			reconciliation: api.WorkflowExternalEffectImageHosting,
		},
		{
			name:           "current submission reconciliation",
			reviewed:       true,
			reconciliation: api.WorkflowExternalEffectTrackerSubmission,
		},
		{
			name:     "old reviewed evidence and description",
			legacy:   true,
			reviewed: true,
		},
		{
			name:     "unrelated lane remains current",
			legacy:   true,
			betaOnly: true,
		},
		{name: "current TL policy remains current"},
		{
			name:      "confirmed outcome remains authoritative",
			legacy:    true,
			reviewed:  true,
			completed: true,
		},
		{
			name:   "legacy completed audit remains closed",
			legacy: true,
			closed: api.WorkflowStatusCompleted,
		},
		{
			name:   "canceled workflow remains closed",
			legacy: true,
			closed: api.WorkflowStatusCanceled,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"TL": {APIUpload: true, Passkey: "synthetic"}, "BETA": {}}}}
			preparer := testPreparer()
			preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
				metadata := api.SourceScopedMetadata{
					SourcePath: input.Release.SourcePath,
					Generation: 1,
					IMDB:       &api.IMDBMetadata{IMDBID: 456, Title: "Example Show"},
				}
				if !test.legacy {
					metadata.AniList = &api.AniListMetadata{MALID: 111, AniListID: 222}
				}
				return api.UploadSubject{
					SourcePath:  input.Release.SourcePath,
					Anime:       true,
					ReleaseName: "Example.Show.S01E01.1080p.WEB-DL-GRP",
					Identity: api.ExternalIdentity{
						SourcePath: input.Release.SourcePath,
						Generation: 1,
						Category:   api.CanonicalCategoryTV,
						IMDBID:     456,
						MALID:      111,
					},
					ProviderMetadata: metadata,
					Release:          api.ReleaseInfo{Title: "Example Show", Resolution: "1080p"},
				}, nil
			}
			databasePath := filepath.Join(t.TempDir(), "policy-restart.sqlite")
			dbfixture.WriteMigrated(t, databasePath)
			repoA, err := db.Open(databasePath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repoA.Close() })
			persistentA, err := NewPersistentRepository(repoA)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, time.November, 6, 1, 0, 0, 0, time.UTC)
			private := NewMemoryPrivateResourceStore()
			moduleA, err := New(persistentA, private, preparer, WithProcessEpoch("before-upgrade"), WithClock(fixedClock{now: now}), WithTrackerProjectionBuilder(policyUpgradeProjector(t, cfg, test.legacy)), WithTrackerPreflightBuilder(readyPreflightBuilder(t)), WithIDGenerator(&sequenceIDGenerator{}))
			if err != nil {
				t.Fatal(err)
			}
			created := executeCommand(t, moduleA, CreateWorkflowCommand{})
			prepared := executeCommand(t, moduleA, PrepareReleaseCommand{
				WorkflowID:       created.Workflow.ID,
				ExpectedRevision: created.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Show.mkv")},
			})
			ids := []api.TrackerID{"TL", "BETA"}
			if test.betaOnly {
				ids = []api.TrackerID{"BETA"}
			}
			projected := executeCommand(t, moduleA, ProjectTrackersCommand{
				WorkflowID:       prepared.Workflow.ID,
				ExpectedRevision: prepared.Workflow.Revision,
				TrackerIDs:       ids,
			})
			for _, lane := range projected.Projections.Projections {
				if !lane.DupeReady {
					t.Fatalf("old projection was not actually eligible: %+v", lane)
				}
			}
			if test.reviewed {
				projected = executeCommand(t, moduleA, PreflightTrackersCommand{WorkflowID: projected.Workflow.ID, ExpectedRevision: projected.Workflow.Revision})
			}
			state, err := persistentA.Load(t.Context(), testOwnerID, projected.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			oldProjection := *state.Workflow.TrackerProjections
			expected := state.Workflow.Revision
			state.Workflow.Revision++
			if test.reviewed {
				state.Workflow.Dupes = &api.DupeAssessmentRef{ID: "old-dupes", Revision: 1}
				release := state.Releases[state.Workflow.Release.ID].Release
				exact := api.ReleaseRef{SourcePath: release.Source.SourcePath, Generation: release.Generation}
				dupes := api.DupeAssessment{
					ID:               "old-dupes",
					WorkflowID:       state.Workflow.ID,
					Revision:         1,
					Release:          *state.Workflow.Release,
					ReleaseRef:       exact,
					Selection:        *state.Workflow.Selection,
					ProjectionSet:    oldProjection,
					Preflight:        state.Workflow.TrackerPreflight,
					InputFingerprint: testFingerprint(t, "old-dupes"),
					CreatedAt:        now,
					ExpiresAt:        now.Add(time.Hour),
					Status:           api.StageStatusCompleted,
				}
				for _, lane := range state.Projections[oldProjection.ID].Projections {
					result := policyUpgradeDupeResult(t, lane, now)
					if lane.TrackerID == "TL" {
						result.Decision = api.DupeDecisionIgnored
						result.Search.Complete, result.Search.WorkScope = false, "title_fallback"
						result.Matches = []api.DupeMatchProjection{{
							ID:       "synthetic-1",
							Name:     "Example.Show.S01E01.1080p.WEB-DL-GRP",
							Relation: api.DupeRelationInsufficientEvidence,
						}}
					}
					dupes.Results = append(dupes.Results, result)
				}
				if err := dupes.Validate(); err != nil {
					t.Fatal(err)
				}
				state.Dupes["old-dupes"] = dupes
				state.Workflow.Media = &api.MediaArtifactSetRef{ID: "old-media", Revision: 1}
				media := api.MediaArtifactSet{
					ID:                        "old-media",
					WorkflowID:                state.Workflow.ID,
					Revision:                  1,
					Release:                   *state.Workflow.Release,
					ReleaseRef:                exact,
					ProjectionSet:             oldProjection,
					CaptureFingerprint:        testFingerprint(t, "old-capture"),
					RequirementsFingerprint:   testFingerprint(t, "old-requirements"),
					ImageRequirementsPrepared: true,
					Status:                    api.StageStatusCompleted,
					CreatedAt:                 now,
				}
				if err := media.Validate(); err != nil {
					t.Fatal(err)
				}
				state.Media["old-media"] = media
				state.Workflow.Descriptions = &api.DescriptionSetRef{ID: "old-description", Revision: 1}
				descriptions := api.DescriptionSet{
					ID:                  "old-description",
					WorkflowID:          state.Workflow.ID,
					Revision:            1,
					Release:             *state.Workflow.Release,
					ReleaseRef:          exact,
					ProjectionSet:       oldProjection,
					Media:               state.Workflow.Media,
					InputFingerprint:    testFingerprint(t, "old-description-input"),
					TemplateFingerprint: testFingerprint(t, "old-template"),
					Status:              api.StageStatusCompleted,
					CreatedAt:           now,
					Descriptions: []api.RenderedDescription{{
						GroupKey:           "tl",
						TrackerIDs:         []api.TrackerID{"TL"},
						Source:             "Reviewed old text [img]https://images.example.invalid/old.png[/img]",
						Rendered:           "Reviewed old text",
						ContentFingerprint: testFingerprint(t, "old-rendered"),
					}},
				}
				if err := descriptions.Validate(); err != nil {
					t.Fatal(err)
				}
				state.Descriptions["old-description"] = descriptions
				for _, resource := range []string{dupePrivateResourceID("old-dupes"), mediaPrivateResourceID("old-media"), descriptionPrivateResourceID("old-description")} {
					if err := private.Put(testOwnerID, state.Workflow.ID, resource, "available old authority", now.Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if test.reconciliation != "" {
				action, err := moduleA.newReconcileAction(state.Workflow.Revision, now, "TL", test.reconciliation, "synthetic-scope", "Confirm the uncertain effect did not complete")
				if err != nil {
					t.Fatal(err)
				}
				effect := api.ReleaseWorkflowEffectRecord{
					OwnerID:             testOwnerID,
					WorkflowID:          state.Workflow.ID,
					OperationID:         "uncertain-operation",
					EffectID:            "uncertain-effect",
					Kind:                string(test.reconciliation),
					ScopeID:             action.EffectScopeID,
					SemanticFingerprint: testFingerprint(t, "uncertain"),
					StartedAt:           now,
					UpdatedAt:           now,
				}
				if _, _, err := persistentA.BeginEffect(t.Context(), effect); err != nil {
					t.Fatal(err)
				}
				if err := persistentA.MarkOperationEffectsUnknown(t.Context(), testOwnerID, state.Workflow.ID, effect.OperationID, now); err != nil {
					t.Fatal(err)
				}
				action.EffectID, action.EffectOperationID = effect.EffectID, effect.OperationID
				state.Workflow.RequiredActions = []api.RequiredAction{action}
				state.Workflow.Status = api.WorkflowStatusBlocked
			}
			if test.completed {
				state.Workflow.Status = api.WorkflowStatusCompleted
				state.Workflow.UploadResult = &api.UploadResultRef{ID: "confirmed", Revision: 1}
				state.UploadResults["confirmed"] = api.UploadResult{
					ID:       "confirmed",
					Revision: 1,
					Status:   api.StageStatusCompleted,
				}
			}
			if test.closed != "" {
				state.Workflow.Status = test.closed
			}
			if err := persistentA.Save(t.Context(), testOwnerID, expected, state); err != nil {
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
			persistentB, err := NewPersistentRepository(repoB)
			if err != nil {
				t.Fatal(err)
			}
			moduleB, err := New(persistentB, private, preparer, WithProcessEpoch("after-upgrade"), WithClock(fixedClock{now: now.Add(time.Minute)}), WithTrackerProjectionBuilder(policyUpgradeProjector(t, cfg, false)))
			if err != nil {
				t.Fatal(err)
			}
			current, err := moduleB.Current(t.Context(), testOwnerID, state.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if test.reconciliation != "" {
				if current.Projections.Status == api.StageStatusStale || current.Workflow.Dupes == nil || len(current.Workflow.RequiredActions) != 1 {
					t.Fatal("pending reconciliation authority was withdrawn")
				}
				reconciled := executeCommand(t, moduleB, ResolveActionCommand{
					WorkflowID:       current.Workflow.ID,
					ExpectedRevision: current.Workflow.Revision,
					Answer: api.RequiredActionAnswer{
						ActionID:         current.Workflow.RequiredActions[0].ID,
						WorkflowRevision: current.Workflow.Revision,
						SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
					},
				})
				if len(reconciled.Workflow.RequiredActions) != 0 || reconciled.Workflow.Status != api.WorkflowStatusActive {
					t.Fatalf("reconciliation did not finish: %+v", reconciled.Workflow)
				}
				if test.legacy && (reconciled.Workflow.Dupes != nil || reconciled.Media != nil) {
					t.Fatal("reconciliation returned obsolete downstream authority")
				}
				current, err = moduleB.Current(t.Context(), testOwnerID, state.Workflow.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			wantStale := test.legacy && !test.betaOnly && !test.completed && test.closed == ""
			if (current.Projections.Status == api.StageStatusStale) != wantStale {
				t.Fatalf("projection status = %s", current.Projections.Status)
			}
			for _, lane := range current.Projections.Projections {
				if lane.TrackerID == "BETA" && !lane.DupeReady {
					t.Fatal("unrelated lane lost readiness")
				}
				if wantStale && lane.TrackerID == "TL" && (lane.DupeReady || lane.UploadReady || lane.Readiness != api.ReadinessStatusStale) {
					t.Fatal("obsolete TL lane retained authority")
				}
			}
			if wantStale {
				if current.Workflow.Dupes != nil || current.Workflow.Media != nil || current.Workflow.Descriptions != nil || current.Workflow.DryRun != nil {
					t.Fatal("obsolete downstream authority survived")
				}
				for _, goal := range []api.WorkflowGoal{api.WorkflowGoalDuplicatesDecided, api.WorkflowGoalDescriptionsReady, api.WorkflowGoalUploaded} {
					command, _ := planContinuationCommandWithReadiness(api.ContinueReleaseWorkflowRequest{Goal: goal, Intent: api.WorkflowIntent{TrackerIDs: ids}}, current, now, false, 0)
					if _, ok := command.(ProjectTrackersCommand); !ok {
						t.Fatalf("goal %s planned %T before rebuilding policies", goal, command)
					}
				}
				rebuilt := executeCommand(t, moduleB, ProjectTrackersCommand{
					WorkflowID:       current.Workflow.ID,
					ExpectedRevision: current.Workflow.Revision,
					TrackerIDs:       ids,
				})
				for _, lane := range rebuilt.Projections.Projections {
					if lane.TrackerID == "TL" && lane.DupeReady {
						t.Fatal("API anime mapping bypassed after rebuilding")
					}
				}
			}
			if test.completed && current.Workflow.UploadResult == nil {
				t.Fatal("confirmed outcome was withdrawn")
			}
			if test.closed != "" && current.Workflow.Status != test.closed {
				t.Fatal("closed workflow was reopened")
			}
			loaded, err := persistentB.Load(t.Context(), testOwnerID, state.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.EqualFunc(loaded.Projections[oldProjection.ID].Projections, state.Projections[oldProjection.ID].Projections, func(a, b api.TrackerReleaseProjection) bool {
				return a.Readiness == b.Readiness && a.DupeReady == b.DupeReady
			}) {
				t.Fatal("historical projection audit was modified")
			}
		})
	}
}

func policyUpgradeProjector(t *testing.T, cfg config.Config, legacy bool) *trackers.WorkflowProjector {
	t.Helper()
	registry := trackers.NewRegistry()
	profile := tl.Profile()
	if legacy {
		profile.ReleaseNamePolicy.ID = "standalone/tl/v1"
		profile.DupePolicy = nil
		profile.ValidationPolicy = trackers.NoExtraValidationPolicy("standalone-tl-constructibility-v1")
	}
	if err := registry.Register(standalone.MustNew(profile)); err != nil {
		t.Fatal(err)
	}
	beta := tl.Profile()
	beta.Name, beta.DescriptionGroup = "BETA", "beta"
	beta.ReleaseNamePolicy.ID = "test/beta/v1"
	beta.DupePolicy, beta.MetadataPolicy = nil, nil
	beta.ValidationPolicy = trackers.NoExtraValidationPolicy("test/beta/v1")
	if err := registry.Register(standalone.MustNew(beta)); err != nil {
		t.Fatal(err)
	}
	builder, err := trackers.NewWorkflowProjector(registry, cfg, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	return builder
}

func TestTLPolicyUpgradeInterruptsObsoleteResumableChecks(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"preflight", "duplicates", "composite-preflight", "composite-duplicates", "capture", "hosting", "descriptions"} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/legacy=%t", stage, legacy), func(t *testing.T) {
				composite := strings.HasPrefix(stage, "composite-")
				now := time.Date(2026, time.November, 6, 2, 0, 0, 0, time.UTC)
				cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"TL": {APIUpload: true, Passkey: "synthetic"}, "BETA": {}}}}
				preparer := testPreparer()
				preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
					metadata := api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 456, Title: "Example Show"}}
					if !legacy {
						metadata.AniList = &api.AniListMetadata{MALID: 111, AniListID: 222}
					}
					return api.UploadSubject{
						SourcePath:  input.Release.SourcePath,
						ReleaseName: "Example.Show.S01E01.1080p.WEB-DL-GRP",
						Anime:       true,
						Identity: api.ExternalIdentity{
							Category: api.CanonicalCategoryTV,
							MALID:    111,
							IMDBID:   456,
						},
						ProviderMetadata: metadata,
					}, nil
				}
				repository := NewMemoryRepository()
				private := NewMemoryPrivateResourceStore()
				moduleA, err := New(repository, private, preparer, WithProcessEpoch("old-worker"), WithClock(fixedClock{now: now}), WithIDGenerator(&sequenceIDGenerator{}), WithTrackerProjectionBuilder(policyUpgradeProjector(t, cfg, legacy)), WithTrackerPreflightBuilder(readyPreflightBuilder(t)))
				if err != nil {
					t.Fatal(err)
				}
				created := executeCommand(t, moduleA, CreateWorkflowCommand{TrackerDecisionMode: TrackerDecisionModeWebUIControls})
				prepared := executeCommand(t, moduleA, PrepareReleaseCommand{
					WorkflowID:       created.Workflow.ID,
					ExpectedRevision: created.Workflow.Revision,
					Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Show.mkv")},
				})
				projected := executeCommand(t, moduleA, ProjectTrackersCommand{
					WorkflowID:       prepared.Workflow.ID,
					ExpectedRevision: prepared.Workflow.Revision,
					TrackerIDs:       []api.TrackerID{"TL"},
				})
				started, releaseOld, resumed := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var resumedCalls atomic.Int32
				readyPreflight := readyPreflightBuilder(t)
				var command Command
				if strings.HasSuffix(stage, "preflight") {
					moduleA.trackerPreflight = trackerPreflightBuilderFunc(func(context.Context, api.UploadSubject, api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerReleaseProjectionSet, time.Time) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
						close(started)
						<-releaseOld
						return api.TrackerPreflightAssessment{}, nil, errors.New("old worker interrupted")
					})
					command = PreflightTrackersCommand{
						WorkflowID:       projected.Workflow.ID,
						ExpectedRevision: projected.Workflow.Revision,
						IdempotencyKey:   "retained-preflight",
					}
				} else {
					assessed := executeCommand(t, moduleA, PreflightTrackersCommand{WorkflowID: projected.Workflow.ID, ExpectedRevision: projected.Workflow.Revision})
					moduleA.dupeBuilder = dupeAssessmentBuilderFunc(func(context.Context, api.DuplicateSubject, api.TrackerReleaseProjectionSet, api.TrackerPreflightAssessment, time.Time, bool) (api.DupeAssessment, any, error) {
						close(started)
						<-releaseOld
						return api.DupeAssessment{}, nil, errors.New("old worker interrupted")
					})
					command = CheckDuplicatesCommand{
						WorkflowID:       assessed.Workflow.ID,
						ExpectedRevision: assessed.Workflow.Revision,
						IdempotencyKey:   "retained-duplicates",
					}
					if stage == "capture" || stage == "hosting" || stage == "descriptions" {
						moduleA.dupeBuilder = dupeAssessmentBuilderFunc(func(_ context.Context, _ api.DuplicateSubject, projection api.TrackerReleaseProjectionSet, preflight api.TrackerPreflightAssessment, at time.Time, _ bool) (api.DupeAssessment, any, error) {
							return policyUpgradeDupes(t, projection, preflight, at), "private evidence", nil
						})
						checked := executeCommand(t, moduleA, CheckDuplicatesCommand{
							WorkflowID:       assessed.Workflow.ID,
							ExpectedRevision: assessed.Workflow.Revision,
						})
						blockOld := func() error { close(started); <-releaseOld; return errors.New("old worker interrupted") }
						if stage == "capture" {
							moduleA.mediaBuilder = mediaArtifactBuilderFunc(func(context.Context, api.ReleaseRef, api.TrackerReleaseProjectionSet, api.MediaCaptureInstructions, time.Time) (api.MediaArtifactSet, any, error) {
								return api.MediaArtifactSet{}, nil, blockOld()
							})
							command = CaptureMediaCommand{
								WorkflowID:       checked.Workflow.ID,
								ExpectedRevision: checked.Workflow.Revision,
								IdempotencyKey:   "retained-capture",
							}
						} else {
							moduleA.mediaBuilder = mediaArtifactBuilderFunc(func(_ context.Context, _ api.ReleaseRef, projection api.TrackerReleaseProjectionSet, _ api.MediaCaptureInstructions, _ time.Time) (api.MediaArtifactSet, any, error) {
								fingerprint, err := mediaRequirementsFingerprint(projection.Projections)
								return api.MediaArtifactSet{
									RequirementsFingerprint: fingerprint,
									CaptureFingerprint:      testFingerprint(t, "capture"),
									Status:                  api.StageStatusCompleted,
									Artifacts: []api.MediaArtifact{{
										ID:       "shot-1",
										Kind:     api.MediaArtifactScreenshot,
										Purpose:  api.ScreenshotPurposeFinal,
										Selected: true,
									}},
								}, &retainedMediaResourceFake{stats: &retainedMediaResourceStats{}}, err
							})
							captured := executeCommand(t, moduleA, CaptureMediaCommand{WorkflowID: checked.Workflow.ID, ExpectedRevision: checked.Workflow.Revision})
							if stage == "hosting" {
								moduleA.mediaBuilder = policyUpgradeImageBuilder{MediaArtifactBuilder: moduleA.mediaBuilder, upload: blockOld}
								command = UploadMediaImagesCommand{
									WorkflowID:       captured.Workflow.ID,
									ExpectedRevision: captured.Workflow.Revision,
									Media:            *captured.Workflow.Media,
									IdempotencyKey:   "retained-hosting",
								}
							} else {
								moduleA.descriptionBuilder = policyUpgradeDescriptionBuilder{descriptionBuilderFake: &descriptionBuilderFake{testing: t}, build: blockOld}
								command = GenerateDescriptionsCommand{
									WorkflowID:       captured.Workflow.ID,
									ExpectedRevision: captured.Workflow.Revision,
									IdempotencyKey:   "retained-descriptions",
								}
							}
						}
					}
				}
				if composite {
					state, err := repository.Load(t.Context(), testOwnerID, projected.Workflow.ID)
					if err != nil {
						t.Fatal(err)
					}
					fingerprint := testFingerprint(t, "retained-composite")
					previousRevision := state.Workflow.Revision
					state.Workflow.Revision++
					state.Composite = &compositeUploadSession{
						Version:               compositeUploadSessionVersion,
						RequestFingerprint:    fingerprint,
						StartedRevision:       state.Workflow.Revision,
						LastCommittedRevision: state.Workflow.Revision,
						Goal:                  api.WorkflowGoalDryRun,
						Intent:                api.WorkflowIntent{TrackerIDs: []api.TrackerID{"TL"}, Preparation: state.PreparationInput},
					}
					if err := repository.Save(t.Context(), testOwnerID, previousRevision, state); err != nil {
						t.Fatal(err)
					}
					command = CompositeUploadCommand{
						WorkflowID:         state.Workflow.ID,
						ExpectedRevision:   state.Workflow.Revision,
						SessionFingerprint: fingerprint,
						Goal:               api.WorkflowGoalDryRun,
						IdempotencyKey:     "retained-composite",
					}
				}
				operation, err := moduleA.Start(t.Context(), testOwnerID, command)
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-started:
				case <-time.After(10 * time.Second):
					status, _ := moduleA.Operation(t.Context(), testOwnerID, projected.Workflow.ID, operation.ID)
					t.Fatalf("retained stage did not start: %+v", status)
				}
				moduleA.operationWorkersMu.Lock()
				oldWorker := moduleA.operationWorkers[operation.ID]
				moduleA.operationWorkersMu.Unlock()
				t.Cleanup(func() { close(releaseOld); <-oldWorker.done })
				var rawEffects []api.ReleaseWorkflowEffectRecord
				if legacy && stage == "composite-duplicates" {
					// Model a crash after recording remote effects but before publishing
					// their aggregate result. Interruption must retain reconciliation.
					for _, scope := range []string{"TL", "BETA"} {
						effect := api.ReleaseWorkflowEffectRecord{
							OwnerID:             testOwnerID,
							WorkflowID:          projected.Workflow.ID,
							OperationID:         operation.ID,
							EffectID:            "raw-" + scope,
							Kind:                string(api.WorkflowExternalEffectTrackerSubmission),
							ScopeID:             scope,
							SemanticFingerprint: testFingerprint(t, scope),
							StartedAt:           now,
							UpdatedAt:           now,
						}
						if _, _, err := repository.BeginEffect(t.Context(), effect); err != nil {
							t.Fatal(err)
						}
						if scope == "BETA" {
							effect.UpdatedAt, effect.CompletedAt = now, &now
							if err := repository.CompleteEffect(t.Context(), api.WorkflowEffectStatusSucceeded, effect); err != nil {
								t.Fatal(err)
							}
						}
						rawEffects = append(rawEffects, effect)
					}
					if err := private.PutWithoutExpiry(testOwnerID, projected.Workflow.ID, "retained-effect-authority", "private outcome evidence"); err != nil {
						t.Fatal(err)
					}
				}
				moduleB, err := New(repository, private, preparer, WithProcessEpoch("new-worker"), WithClock(fixedClock{now: now.Add(2 * workflowWorkLeaseTTL)}), WithTrackerProjectionBuilder(policyUpgradeProjector(t, cfg, false)),
					WithTrackerPreflightBuilder(trackerPreflightBuilderFunc(func(ctx context.Context, subject api.UploadSubject, catalog api.TrackerCatalogSnapshot, runtime api.TrackerRuntimeSnapshot, projection api.TrackerReleaseProjectionSet, at time.Time) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
						resumedCalls.Add(1)
						close(resumed)
						if composite {
							return api.TrackerPreflightAssessment{}, nil, errors.New("stop after current-policy resumption")
						}
						return readyPreflight.Build(ctx, subject, catalog, runtime, projection, at)
					})),
					WithDupeAssessmentBuilder(dupeAssessmentBuilderFunc(func(_ context.Context, _ api.DuplicateSubject, projection api.TrackerReleaseProjectionSet, preflight api.TrackerPreflightAssessment, at time.Time, _ bool) (api.DupeAssessment, any, error) {
						resumedCalls.Add(1)
						close(resumed)
						if composite {
							return api.DupeAssessment{}, nil, errors.New("stop after current-policy resumption")
						}
						return policyUpgradeDupes(t, projection, preflight, at), "private current evidence", nil
					})))
				if err != nil {
					t.Fatal(err)
				}
				resumeDownstream := func() error {
					resumedCalls.Add(1)
					close(resumed)
					return errors.New("stop after current-policy resumption")
				}
				moduleB.mediaBuilder = policyUpgradeImageBuilder{
					MediaArtifactBuilder: mediaArtifactBuilderFunc(func(context.Context, api.ReleaseRef, api.TrackerReleaseProjectionSet, api.MediaCaptureInstructions, time.Time) (api.MediaArtifactSet, any, error) {
						return api.MediaArtifactSet{}, nil, resumeDownstream()
					}),
					upload: resumeDownstream,
				}
				moduleB.descriptionBuilder = policyUpgradeDescriptionBuilder{descriptionBuilderFake: &descriptionBuilderFake{testing: t}, build: resumeDownstream}
				current, err := moduleB.Current(t.Context(), testOwnerID, created.Workflow.ID)
				if err != nil {
					t.Fatal(err)
				}
				if legacy {
					status, err := moduleB.Operation(t.Context(), testOwnerID, created.Workflow.ID, operation.ID)
					if err != nil || status.Status != api.StageStatusInterrupted || resumedCalls.Load() != 0 || current.Projections.Status != api.StageStatusStale || current.Projections.Projections[0].DupeReady {
						t.Fatalf("obsolete operation status=%+v calls=%d current=%+v err=%v", status, resumedCalls.Load(), current.Projections, err)
					}
					for _, effect := range rawEffects {
						retained, reused, err := repository.BeginEffect(t.Context(), effect)
						if effect.ScopeID == "TL" {
							if !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) || reused || retained.Status != api.WorkflowEffectStatusUnknown {
								t.Fatalf("uncertain submission fence lost: %+v reused=%t err=%v", retained, reused, err)
							}
						} else if err != nil || !reused || retained.Status != api.WorkflowEffectStatusSucceeded {
							t.Fatalf("confirmed submission fence lost: %+v reused=%t err=%v", retained, reused, err)
						}
						if retained.EffectID != effect.EffectID || retained.OperationID != effect.OperationID {
							t.Fatal("effect reconciliation identity changed")
						}
					}
					if len(rawEffects) > 0 {
						ids, err := repository.ListLegacyRecoveryWorkflowIDs(t.Context(), testOwnerID)
						if err != nil || !slices.Contains(ids, projected.Workflow.ID) {
							t.Fatalf("uncertain submission missing from recovery: %v err=%v", ids, err)
						}
						if evidence, err := private.Get(testOwnerID, projected.Workflow.ID, "retained-effect-authority", now.Add(2*workflowWorkLeaseTTL)); err != nil || evidence != "private outcome evidence" {
							t.Fatalf("private reconciliation evidence lost: %v err=%v", evidence, err)
						}
					}
				} else {
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					defer cancel()
					select {
					case <-resumed:
					case <-ctx.Done():
						t.Fatal("current policy operation did not resume")
					}
					if err := moduleB.waitForOperationCleanup(ctx, operation.ID); err != nil {
						t.Fatal(err)
					}
					status, err := moduleB.Operation(ctx, testOwnerID, created.Workflow.ID, operation.ID)
					wantStatus := api.StageStatusCompleted
					if composite || stage == "capture" || stage == "hosting" || stage == "descriptions" {
						wantStatus = api.StageStatusFailed
					}
					if err != nil || status.Status != wantStatus || resumedCalls.Load() != 1 {
						t.Fatalf("current operation=%+v calls=%d err=%v", status, resumedCalls.Load(), err)
					}
				}
			})
		}
	}
}

func policyUpgradeDupeResult(t *testing.T, projection api.TrackerReleaseProjection, now time.Time) api.TrackerDupeAssessment {
	t.Helper()
	result := testDupeResult(t, projection.TrackerID, projection.UploadReleaseName, now)
	fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
	if err != nil {
		t.Fatal(err)
	}
	result.ProjectionFingerprint = fingerprint
	result.Criteria, result.CriteriaFingerprint = projection.DuplicateCriteria, projection.CriteriaFingerprint
	result.TargetFingerprint, result.SearchFingerprint = projection.DuplicateTargetFingerprint, projection.DuplicateSearchFingerprint
	result.PolicyID, result.PolicyFingerprint = projection.DuplicatePolicyID, projection.DuplicatePolicyFingerprint
	result.EvidenceFingerprint = testFingerprint(t, "retained-evidence")
	result.Search.Complete = true
	return result
}

func policyUpgradeDupes(t *testing.T, projection api.TrackerReleaseProjectionSet, preflight api.TrackerPreflightAssessment, now time.Time) api.DupeAssessment {
	t.Helper()
	return api.DupeAssessment{
		InputFingerprint: testFingerprint(t, "current-dupes"),
		ProjectionSet:    api.TrackerReleaseProjectionSetRef{ID: projection.ID, Revision: projection.Revision},
		Preflight:        &api.TrackerPreflightAssessmentRef{ID: preflight.ID, Revision: preflight.Revision},
		Status:           api.StageStatusReady,
		ExpiresAt:        now.Add(time.Hour),
		Results:          []api.TrackerDupeAssessment{policyUpgradeDupeResult(t, projection.Projections[0], now)},
	}
}

type policyUpgradeImageBuilder struct {
	MediaArtifactBuilder
	MediaArtifactMutator
	upload func() error
}

func (b policyUpgradeImageBuilder) UploadImages(context.Context, api.ReleaseRef, api.TrackerReleaseProjectionSet, api.MediaArtifactSet, any, []api.PublicResourceID, string, bool, *string, time.Time) (api.MediaArtifactSet, RetainedMediaResource, []api.HostedImageAttempt, error) {
	return api.MediaArtifactSet{}, nil, nil, b.upload()
}

type policyUpgradeDescriptionBuilder struct {
	*descriptionBuilderFake
	build func() error
}

func (b policyUpgradeDescriptionBuilder) Build(context.Context, api.ReleaseRef, api.TrackerReleaseProjectionSet, api.MediaArtifactSet, any, api.DescriptionInstructions, time.Time) (api.DescriptionSet, error) {
	return api.DescriptionSet{}, b.build()
}
