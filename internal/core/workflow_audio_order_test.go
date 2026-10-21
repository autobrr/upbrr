// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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

func TestContinueReleaseWorkflowAudioOrderPreservesRequiredGates(t *testing.T) {
	for _, test := range []struct {
		mode releaseworkflow.TrackerDecisionMode
		pack bool
	}{
		{releaseworkflow.TrackerDecisionModePostDupeGate, false},
		{releaseworkflow.TrackerDecisionModePostDupeGate, true},
		{releaseworkflow.TrackerDecisionModeWebUIControls, false},
		{releaseworkflow.TrackerDecisionModeWebUIControls, true},
	} {
		for _, personal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/pack=%t/personal=%t", test.mode, test.pack, personal), func(t *testing.T) {
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
					TrackCoverageComplete: !test.pack,
					PrimaryAudioTrackID:   "German",
				}
				if test.pack {
					// A finalized subtitle correction satisfies LUME's separate subtitle
					// requirement without claiming collection-wide programme coverage.
					media.SubtitleLanguagesProvenance = api.FactProvenanceManual
				}
				// Original audio stays default, but follows a dub and separates normal dubs.
				for index, language := range []string{"German", "Japanese", "English"} {
					media.Tracks = append(media.Tracks, api.MediaTrackFacts{
						ID:                  language,
						Kind:                api.MediaTrackAudio,
						ResourceID:          "selected-resource",
						ManifestFingerprint: "selected-report",
						Role:                api.AudioRoleProgramme,
						Title:               language + " main audio",
						Languages:           []string{language},
						Codec:               "FLAC",
						Channels:            2,
						ChannelsKnown:       true,
						Default:             language == "Japanese",
						DefaultKnown:        true,
						StreamOrder:         index,
						StreamOrderKnown:    true,
					})
				}
				media.Tracks = append(media.Tracks, api.MediaTrackFacts{
					ID:           "English-subtitles",
					ResourceID:   "selected-resource",
					Kind:         api.MediaTrackSubtitle,
					Title:        "English full subtitles",
					Languages:    []string{"English (Full)"},
					Default:      true,
					DefaultKnown: true,
				})
				const releaseName = "Example.Release.2026.1080p.WEB-DL-GRP"
				sourcePath := filepath.Join(t.TempDir(), releaseName+".mkv")
				videoPath := sourcePath
				files := []string{sourcePath}
				if test.pack {
					sourcePath = filepath.Dir(sourcePath)
					videoPath = filepath.Join(sourcePath, "Example.S01E01.mkv")
					files = []string{videoPath, filepath.Join(sourcePath, "Example.S01E02.mkv")}
				}
				canonicalFacts := mediafacts.ResolveLanguages(media)
				before := canonicalFacts.Clone()
				if test.pack && (canonicalFacts.TrackCoverageComplete || canonicalFacts.ProgrammeStatus != api.MetadataEvidenceStatusPartial) {
					t.Fatalf("fixture promoted uninspected pack evidence: %+v", canonicalFacts)
				}
				mediaFiles := api.MediaFileFacts{
					Status:            api.MetadataEvidenceStatusComplete,
					TechnicalStatus:   api.MetadataEvidenceStatusComplete,
					LanguageStatus:    api.MetadataEvidenceStatusComplete,
					ExpectedFileCount: len(files),
					OriginalLanguage:  "Japanese",
					Files: []api.MediaFileFact{{
						ResourceID:        "selected-resource",
						FileName:          videoPath,
						Primary:           true,
						Container:         "mkv",
						Resolution:        "1080p",
						VideoTrackCount:   1,
						AudioLanguages:    []string{"German", "Japanese", "English"},
						SubtitleLanguages: []string{"English (Full)"},
						AudioStatus:       api.MetadataEvidenceStatusComplete,
						SubtitleStatus:    api.MetadataEvidenceStatusComplete,
					}},
				}
				if test.pack {
					mediaFiles.Status = api.MetadataEvidenceStatusPartial
					mediaFiles.TechnicalStatus = api.MetadataEvidenceStatusPartial
					mediaFiles.LanguageStatus = api.MetadataEvidenceStatusPartial
				}
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
						subject := api.UploadSubject{
							SourcePath:      input.Release.SourcePath,
							VideoPath:       videoPath,
							FileList:        slices.Clone(files),
							ReleaseName:     releaseName,
							Source:          "Web",
							Type:            "WEBDL",
							Container:       "mkv",
							PersonalRelease: personal,
							LanguageFacts:   canonicalFacts.Clone(),
							MediaFileFacts:  mediaFiles.Clone(),
							Release: api.ReleaseInfo{
								Title:      "Example Release",
								Year:       2026,
								Resolution: "1080p",
							},
						}
						answers := map[string]string{}
						questionnaire := lst.Profile().Site.ProjectionQuestionnaire(trackers.PreparationInput{
							Tracker:    "LST",
							Meta:       subject,
							Projection: &api.TrackerReleaseProjection{UploadReleaseName: releaseName},
						})
						for _, field := range questionnaire.Fields {
							switch {
							case strings.HasPrefix(field.Key, "stream_processing_"):
								answers[field.Key] = "reviewed_no_user_processing_or_improper_splicing"
							case strings.HasPrefix(field.Key, "presentation_track_presentation_"), strings.HasPrefix(field.Key, "presentation_web_source_naming_"):
								answers[field.Key] = "reviewed_correct"
							}
						}
						subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"LST": answers}
						return subject, nil
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
				ctx := releaseworkflow.WithTrackerDecisionMode(t.Context(), test.mode)
				const owner = "audio-order-owner"
				request := api.ContinueReleaseWorkflowRequest{
					IdempotencyKey: "audio-order",
					Goal:           api.WorkflowGoalDuplicatesDecided,
					Intent: api.WorkflowIntent{
						Preparation: &api.PrepareInput{SourcePath: sourcePath},
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
				lstBlocked := test.pack || personal
				for _, projection := range current.Projections.Projections {
					blocked := projection.TrackerID == "LST" && lstBlocked
					wantReadiness := api.ReadinessStatusReady
					if blocked {
						wantReadiness = api.ReadinessStatusIneligible
					}
					if projection.Readiness != wantReadiness || projection.DupeReady == blocked ||
						projection.RuleAuthorizationFingerprint != "" || projection.WaivableRuleFingerprint != "" || len(projection.RequiredActions) != 0 {
						t.Fatalf("ordering or resource policy changed: %#v", projection)
					}
					if projection.TrackerID == "LST" && test.pack && !slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
						return decision.Code == "lst_container_evidence" && decision.Disposition == api.RuleDispositionStrict &&
							decision.EvidenceStatus == api.MetadataEvidenceStatusPartial && decision.Blocking
					}) {
						t.Fatalf("uninspected pack lost its required evidence gate: %#v", projection)
					}
					for _, decision := range projection.PolicyDecisions {
						if decision.Blocking && (projection.TrackerID != "LST" ||
							(!personal || decision.Code != "language_original_order") && (!test.pack || decision.Code != "lst_container_evidence")) {
							t.Fatalf("unrelated prerequisite masks ordering behavior: %#v", decision)
						}
					}
					codes := []string{"language_original_order"}
					if projection.TrackerID == "LUME" {
						codes = append(codes, "language_dub_order")
					}
					for _, code := range codes {
						strict := projection.TrackerID == "LST" && personal
						wantDisposition := api.RuleDispositionAdvisory
						if strict {
							wantDisposition = api.RuleDispositionStrict
						}
						if !slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
							return decision.Code == code && decision.Disposition == wantDisposition && decision.Blocking == strict
						}) {
							t.Fatalf("missing expected %s ordering decision: %#v", code, projection)
						}
					}
					if guidance := slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
						return decision.Code == "guidance_programme_pack_coverage" && decision.Disposition == api.RuleDispositionAdvisory &&
							decision.EvidenceStatus == api.MetadataEvidenceStatusPartial && !decision.Blocking
					}); guidance != test.pack {
						t.Fatalf("pack coverage guidance=%t, want %t: %#v", guidance, test.pack, projection.PolicyDecisions)
					}
				}
				for _, result := range current.Preflight.Results {
					wantState := api.TrackerPreflightStateReady
					if result.TrackerID == "LST" && lstBlocked {
						wantState = api.TrackerPreflightStateFailed
					}
					if result.State != wantState || len(result.RequiredActions) != 0 {
						t.Fatalf("audio-order preflight requires action: %#v", result)
					}
				}
				if slices.ContainsFunc(current.Workflow.RequiredActions, func(action api.RequiredAction) bool {
					return action.Kind == api.RequiredActionAuthorizeRules || action.Kind == api.RequiredActionAnswerQuestionnaire
				}) {
					t.Fatalf("audio order requested acknowledgement: %#v", current.Workflow.RequiredActions)
				}
				wantChecked := 2
				if lstBlocked {
					wantChecked = 1
				}
				if current.Dupes == nil || len(current.Dupes.Results) != 2 || len(dupes.projections) != wantChecked {
					t.Fatalf("audio order blocked duplicate progression: results=%#v checked=%#v", current.Dupes, dupes.projections)
				}
				for _, result := range current.Dupes.Results {
					wantDecision := api.DupeDecisionNoMatch
					if result.TrackerID == "LST" && lstBlocked {
						wantDecision = api.DupeDecisionSkipped
					}
					if result.Decision != wantDecision {
						t.Fatalf("unexpected duplicate outcome: %#v", result)
					}
				}
				if !reflect.DeepEqual(canonicalFacts, before) {
					t.Fatal("workflow eligibility changed canonical programme facts or coverage")
				}
			})
		}
	}
}
