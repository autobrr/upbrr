// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lst"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lume"
	"github.com/autobrr/upbrr/pkg/api"
)

type workflowAudioOrderDefinition struct {
	workflowImageHostPolicyDefinition
	questionnaire func(trackers.PreparationInput) *api.TrackerQuestionnaire
}

func (d workflowAudioOrderDefinition) ProjectionQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	return d.questionnaire(input)
}

func TestContinueReleaseWorkflowAudioOrderWarningsNeedNoAcknowledgement(t *testing.T) {
	for _, mode := range []releaseworkflow.TrackerDecisionMode{
		releaseworkflow.TrackerDecisionModePostDupeGate,
		releaseworkflow.TrackerDecisionModeWebUIControls,
	} {
		for _, personal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/personal=%t", mode, personal), func(t *testing.T) {
				t.Parallel()
				registry := trackers.NewRegistry()
				for _, profile := range []unit3d.Profile{lst.Profile(), lume.Profile()} {
					if err := registry.RegisterDescriptor(trackers.Descriptor{
						Name: profile.Name,
						Definition: workflowAudioOrderDefinition{
							workflowImageHostPolicyDefinition: workflowImageHostPolicyDefinition{name: profile.Name},
							questionnaire:                     profile.Site.ProjectionQuestionnaire,
						},
						Validation: profile.ValidationPolicy,
					}); err != nil {
						t.Fatal(err)
					}
				}
				projector, err := trackers.NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
				if err != nil {
					t.Fatal(err)
				}
				media := api.MediaFacts{
					OriginalLanguage:      "Japanese",
					SubtitleLanguages:     []string{"English (Full)"},
					TrackCoverageComplete: true,
					PrimaryAudioTrackID:   "German",
				}
				// Original audio stays default, but follows a dub and separates normal dubs.
				for index, language := range []string{"German", "Japanese", "English"} {
					media.Tracks = append(media.Tracks, api.MediaTrackFacts{
						ID:               language,
						Kind:             api.MediaTrackAudio,
						Role:             api.AudioRoleProgramme,
						Title:            language + " main audio",
						Languages:        []string{language},
						Codec:            "FLAC",
						Default:          language == "Japanese",
						DefaultKnown:     true,
						StreamOrder:      index,
						StreamOrderKnown: true,
					})
				}
				media.Tracks = append(media.Tracks, api.MediaTrackFacts{
					ID:           "English-subtitles",
					Kind:         api.MediaTrackSubtitle,
					Title:        "English full subtitles",
					Languages:    []string{"English (Full)"},
					Default:      true,
					DefaultKnown: true,
				})
				const releaseName = "Example.Release.2026.1080p.WEB-DL-GRP"
				preparer := releaseworkflow.ReleasePreparerFunc{
					PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
						return api.PrepareResult{Release: api.PreparedRelease{
							Generation: 1,
							Source:     api.SourceManifest{SourcePath: input.SourcePath},
							Naming:     api.NamingFacts{ReleaseName: releaseName},
						}}, nil
					},
					DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
						return api.PreparedReleaseDisplay{ReleaseName: releaseName}, nil
					},
					SubjectFunc: func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
						return api.UploadSubject{
							SourcePath:      input.Release.SourcePath,
							ReleaseName:     releaseName,
							Source:          "Web",
							Type:            "WEBDL",
							Container:       "mkv",
							PersonalRelease: personal,
							LanguageFacts:   mediafacts.ResolveLanguages(media),
							Release: api.ReleaseInfo{
								Title:      "Example Release",
								Year:       2026,
								Resolution: "1080p",
							},
						}, nil
					},
					DuplicateFunc: func(_ context.Context, input api.DuplicateCheckInput) (api.DuplicateSubject, error) {
						return api.DuplicateSubject{SourcePath: input.Release.SourcePath, ReleaseName: releaseName}, nil
					},
				}
				dupes := &workflowDupeServiceFake{results: []api.DupeCheckResult{
					{
						Tracker: "LST",
						Status:  "completed",
						Search:  api.DupeSearchEvidence{Complete: true},
					},
					{
						Tracker: "LUME",
						Status:  "completed",
						Search:  api.DupeSearchEvidence{Complete: true},
					},
				}}
				module, err := releaseworkflow.New(
					releaseworkflow.NewMemoryRepository(), releaseworkflow.NewMemoryPrivateResourceStore(), preparer,
					releaseworkflow.WithTrackerProjectionBuilder(projector),
					releaseworkflow.WithTrackerPreflightBuilder(workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: registry}),
					releaseworkflow.WithDupeAssessmentBuilder(workflowDupeBuilder{service: dupes}),
				)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = module.Shutdown(context.Background()) })
				core := &Core{workflow: module, logger: api.NopLogger{}}
				ctx := releaseworkflow.WithTrackerDecisionMode(t.Context(), mode)
				const owner = "audio-order-owner"
				request := api.ContinueReleaseWorkflowRequest{
					IdempotencyKey: "audio-order",
					Goal:           api.WorkflowGoalDuplicatesDecided,
					Intent: api.WorkflowIntent{
						Preparation: &api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), releaseName+".mkv")},
						TrackerIDs:  []api.TrackerID{"LST", "LUME"},
					},
				}
				var current api.ReleaseWorkflowCurrent
				deadline := time.Now().Add(30 * time.Second)
				for {
					if !time.Now().Before(deadline) {
						t.Fatalf("audio-order workflow did not settle: %#v", current)
					}
					current, err = core.ContinueReleaseWorkflow(ctx, owner, request)
					if err != nil {
						t.Fatal(err)
					}
					for current.Operation != nil && (current.Operation.Status == api.StageStatusQueued ||
						current.Operation.Status == api.StageStatusRunning || current.Operation.Status == api.StageStatusPending ||
						current.Operation.Status == api.StageStatusReady) {
						if !time.Now().Before(deadline) {
							t.Fatalf("audio-order operation did not finish: %#v", current.Operation)
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
					if request.Authority != nil && current.Workflow.Revision == request.Authority.ExpectedRevision {
						break
					}
					request.Authority = &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision}
				}
				if current.Projections == nil || len(current.Projections.Projections) != 2 || current.Preflight == nil || len(current.Preflight.Results) != 2 {
					t.Fatalf("audio order blocked preflight: %#v", current)
				}
				for _, projection := range current.Projections.Projections {
					if projection.Readiness != api.ReadinessStatusReady || !projection.DupeReady ||
						projection.RuleAuthorizationFingerprint != "" || projection.WaivableRuleFingerprint != "" || len(projection.RequiredActions) != 0 {
						t.Fatalf("audio order requires permission: %#v", projection)
					}
					codes := []string{"language_original_order"}
					if projection.TrackerID == "LUME" {
						codes = append(codes, "language_dub_order")
					}
					for _, code := range codes {
						if !slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
							return decision.Code == code && decision.Disposition == api.RuleDispositionAdvisory && !decision.Blocking
						}) {
							t.Fatalf("missing non-blocking %s warning: %#v", code, projection)
						}
					}
				}
				for _, result := range current.Preflight.Results {
					if result.State != api.TrackerPreflightStateReady || len(result.RequiredActions) != 0 {
						t.Fatalf("audio-order preflight requires action: %#v", result)
					}
				}
				if slices.ContainsFunc(current.Workflow.RequiredActions, func(action api.RequiredAction) bool {
					return action.Kind == api.RequiredActionAuthorizeRules || action.Kind == api.RequiredActionAnswerQuestionnaire
				}) {
					t.Fatalf("audio order requested acknowledgement: %#v", current.Workflow.RequiredActions)
				}
				if current.Dupes == nil || len(current.Dupes.Results) != 2 || len(dupes.projections) != 2 {
					t.Fatalf("audio order blocked duplicate progression: results=%#v checked=%#v", current.Dupes, dupes.projections)
				}
				for _, result := range current.Dupes.Results {
					if result.Decision != api.DupeDecisionNoMatch {
						t.Fatalf("unexpected duplicate outcome: %#v", result)
					}
				}
			})
		}
	}
}
