// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestContinuePTPQuestionnaireRebindsOnlyCompatibleDuplicateEvidence(t *testing.T) {
	testContinueQuestionnaireReuse(t, "PTP", "")
}

func TestContinueANTQuestionnaireRebindsOnlyCompatibleDuplicateEvidence(t *testing.T) {
	testContinueQuestionnaireReuse(t, "ANT", "")
}

func TestContinueQuestionnaireReusesEvidenceWithConfirmedSibling(t *testing.T) {
	testContinueQuestionnaireReuse(t, "PTP", "ANT")
}

func testContinueQuestionnaireReuse(t *testing.T, trackerID, submittedTracker api.TrackerID) {
	t.Helper()
	registry := trackerimpl.MustNewRegistry()
	cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "pixhost"}, Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{string(trackerID): {
		APIKey:     "example",
		PTPAPIUser: "example",
		PTPAPIKey:  "example",
	}}}}
	mediaInfoPath := filepath.Join(t.TempDir(), "mediainfo.txt")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nFormat : Matroska"), 0o600); err != nil {
		t.Fatal(err)
	}
	projector, err := trackers.NewWorkflowProjector(registry, cfg, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	const releaseName = "Example.Movie.2026.1080p.BluRay.x264-GRP"
	sourcePath := filepath.Join(t.TempDir(), releaseName+".mkv")
	content := []byte("verified questionnaire release bytes")
	if err := os.WriteFile(sourcePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	sourceIdentity := api.SourceContentIdentity{
		Version: api.SourceContentIdentityVersion,
		Digest:  hex.EncodeToString(digest[:]),
		Files: []api.VerifiedSourceFile{{
			LocalPath: sourcePath,
			Size:      int64(len(content)),
			SHA256:    hex.EncodeToString(digest[:]),
		}},
	}
	fences := workflowSubmissionFenceRepositoryFake{records: make(map[string]api.SubmissionFenceRecord)}
	if submittedTracker != "" {
		identity, err := workflowSubmissionContentIdentity(api.TorrentSubject{SourcePath: sourcePath}, sourceIdentity)
		if err != nil {
			t.Fatal(err)
		}
		descriptor, ok := registry.LookupDescriptor(string(submittedTracker))
		if !ok {
			t.Fatal("submitted tracker is not registered")
		}
		site, err := trackers.CanonicalSubmissionTrackerSite(descriptor.Name, descriptor.BaseURL)
		if err != nil {
			t.Fatal(err)
		}
		fences.records[identity.Digest+"|"+site] = api.SubmissionFenceRecord{Status: api.WorkflowEffectStatusSucceeded, ConfirmedAt: new(time.Now().UTC())}
	}
	languageFacts := api.LanguageFacts{
		OriginalLanguages: []string{"French"},
 OriginalLanguagesKnown: true,
		ProgrammeLanguages: []string{"French"},
 ProgrammeStatus: api.MetadataEvidenceStatusComplete,
		SubtitleStatus: api.MetadataEvidenceStatusComplete,
 PrimaryAudioTrackID: "audio-1",
		Tracks: []api.MediaTrackFacts{{
ID: "audio-1",
 Kind: api.MediaTrackAudio,
 Role: api.AudioRoleProgramme,
 Languages: []string{"French"},
 Default: true,
}},
	}
	preparations := 0
	preparer := releaseworkflow.ReleasePreparerFunc{
		PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			preparations++
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
				SourcePath:     input.Release.SourcePath,
				SourceIdentity: sourceIdentity,
				Trackers:       input.Trackers,
				ReleaseName:    releaseName,
				Release: api.ReleaseInfo{
					Title:      "Example Movie",
					Year:       2026,
					Category:   "MOVIE",
					Resolution: "1080p",
				},
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
					IMDBID:   123,
					TMDBID:   123,
				},
				MediaInfoTextPath: mediaInfoPath,
				ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
					TMDBID: 123,
					Title:  "Example Movie",
					Year:   2026,
				}},
				Container:                   "mkv",
				Source:                      "BluRay",
				Type:                        "ENCODE",
				VideoCodec:                  "H.264",
				AudioLanguages:              []string{"French"},
				LanguageFacts:               languageFacts.Clone(),
				TrackerQuestionnaireAnswers: input.QuestionnaireAnswers,
			}, nil
		},
		DuplicateFunc: func(_ context.Context, input api.DuplicateCheckInput) (api.DuplicateSubject, error) {
			return api.DuplicateSubject{SourcePath: input.Release.SourcePath, ReleaseName: releaseName}, nil
		},
	}
	authCalls := 0
	dupes := &workflowDupeServiceFake{results: []api.DupeCheckResult{{
		Tracker: string(trackerID),
		Status:  "completed",
		Search:  api.DupeSearchEvidence{Complete: true},
	}}}
	repository := releaseworkflow.NewMemoryRepository()
	module, err := releaseworkflow.New(repository, releaseworkflow.NewMemoryPrivateResourceStore(), preparer,
		releaseworkflow.WithSubmissionHistoryFilter(workflowSubmissionHistoryFilter{fences: fences, registry: registry}),
		releaseworkflow.WithTrackerProjectionBuilder(projector),
		releaseworkflow.WithTrackerPreflightBuilder(workflowPreflightBuilder{
			registry: registry,
			config:   cfg,
			auth: workflowPreflightAuthFake{
				capabilities:  []api.TrackerAuthCapability{{TrackerID: string(trackerID), SupportsLogin: true}},
				statuses:      []api.TrackerAuthStatus{{TrackerID: string(trackerID), State: trackerauth.StateConfigured}},
				validateCalls: &authCalls,
			},
		}),
		releaseworkflow.WithDupeAssessmentBuilder(workflowDupeBuilder{service: dupes}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Shutdown(context.Background()) })
	core := &Core{workflow: module, logger: api.NopLogger{}}
	ctx := releaseworkflow.WithTrackerDecisionMode(t.Context(), releaseworkflow.TrackerDecisionModeWebUIControls)
	const owner = "questionnaire-owner"
	current, err := module.Execute(ctx, owner, releaseworkflow.CreateWorkflowCommand{})
	if err != nil {
		t.Fatal(err)
	}
	current, err = module.Execute(ctx, owner, releaseworkflow.PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: sourcePath},
	})
	if err != nil {
		t.Fatal(err)
	}
	intent := api.WorkflowIntent{TrackerIDs: []api.TrackerID{trackerID}}
	if submittedTracker != "" {
		intent.TrackerIDs = append(intent.TrackerIDs, submittedTracker)
	}
	attempt := 0
	settle := func(goal api.WorkflowGoal) {
		attempt++
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			revision := current.Workflow.Revision
			current, err = core.ContinueReleaseWorkflow(ctx, owner, api.ContinueReleaseWorkflowRequest{
				Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: revision},
				IdempotencyKey: fmt.Sprintf("%s-%d-%d", goal, attempt, revision),
				Goal:           goal,
				Intent:         intent,
			})
			if err != nil {
				t.Fatalf("continue failed: %v (%+v)", err, errors.Unwrap(err))
			}
			for current.Operation != nil && (current.Operation.Status == api.StageStatusQueued || current.Operation.Status == api.StageStatusRunning || current.Operation.Status == api.StageStatusPending || current.Operation.Status == api.StageStatusReady) {
				if !time.Now().Before(deadline) {
					t.Fatal("operation did not finish")
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
			if current.Operation != nil && current.Operation.Status == api.StageStatusFailed {
				t.Fatalf("operation failed: %+v", current.Operation)
			}
			if current.Workflow.Revision == revision {
				return
			}
		}
		t.Fatal("continuation did not settle")
	}
	answerKey, initialAnswer := "trumpable_review", "no"
	if trackerID == "ANT" {
		answerKey, initialAnswer = "tags", "drama"
	}
	setAnswers := func(review string, tags *string) {
		answers := map[string]*string{answerKey: new(review)}
		if trackerID == "PTP" {
			answers["subtitle_tags"] = tags
			languageSubject := api.TrackerValidationSubject{
Tracker: "PTP",
 SourcePath: sourcePath,
 Source: "BluRay",
 Type: "ENCODE",
 LanguageFacts: languageFacts,
}
			answers[trackers.LanguageQuestionKey(languageSubject, "english_subtitle_manager")] = new("available")
			answers[trackers.LanguageQuestionKey(languageSubject, "forced_english_dialogue")] = new("not_required")
		}
		intent.ProjectionInstructions = map[api.TrackerID]api.TrackerProjectionInstructions{trackerID: {Questionnaire: answers}}
		if submittedTracker != "" {
			intent.ProjectionInstructions[submittedTracker] = api.TrackerProjectionInstructions{
				UploadReleaseName: api.WorkflowPatch[string]{Present: true, Value: "Already.Submitted.Name-GRP"},
				Questionnaire:     map[string]*string{"tags": new("retained submitted answer")},
			}
		}
	}
	settle(api.WorkflowGoalTrackersProjected)
	if current.Operation == nil || current.Operation.Status != api.StageStatusBlocked {
		t.Fatal("pending tracker question did not block discovery operation")
	}
	if preparations != 1 || authCalls != 0 || len(dupes.projections) != 0 || current.Preflight != nil || current.InputReadiness != nil {
		t.Fatalf("projection discovery did remote work: preparations=%d auth=%d dupes=%d", preparations, authCalls, len(dupes.projections))
	}
	if current.Projections == nil || current.Projections.Projections[0].Questionnaire[0].Key != answerKey {
		t.Fatal("Tracker questions were not projected before duplicate checking")
	}
	setAnswers(initialAnswer, nil)
	settle(api.WorkflowGoalDuplicatesDecided)
	if current.Dupes == nil || len(dupes.projections) != 1 {
		t.Fatalf("initial duplicates = %#v calls=%d", current.Dupes, len(dupes.projections))
	}
	baseline := *current.Dupes
	type questionnaireEdit struct {
		review string
		tags   *string
		ready  bool
	}
	steps := []questionnaireEdit{
		{"yes", nil, false},
		{"yes", new("English Softsubs Exist (Mislabeled)"), true},
		{"no", nil, true},
		{"", nil, false},
		{"no", nil, true},
	}
	if trackerID == "ANT" {
		steps = []questionnaireEdit{{"drama, mystery", nil, true}, {"", nil, false}, {"drama", nil, true}}
	}
	if submittedTracker != "" {
		steps = []questionnaireEdit{{"yes", new("English Softsubs Exist (Mislabeled)"), true}, {"no", nil, true}, {"", nil, false}, {"no", nil, true}}
	}
	for _, step := range steps {
		setAnswers(step.review, step.tags)
		settle(api.WorkflowGoalTrackersAssessed)
		if len(dupes.projections) != 1 {
			t.Fatal("Apply repeated a remote duplicate search")
		}
		if submittedTracker != "" {
			if !slices.Equal(current.Selection.TrackerIDs, []api.TrackerID{trackerID}) || len(current.Projections.Projections) != 1 || current.Projections.Projections[0].TrackerID != trackerID ||
				len(current.Workflow.SubmissionExclusions) != 1 || current.Workflow.SubmissionExclusions[0].TrackerID != submittedTracker {
				t.Fatal("questionnaire Apply restored an already submitted lane")
			}
			retained := current.ProjectionInstructions.Instructions[submittedTracker]
			if retained.UploadReleaseName.Value != "Already.Submitted.Name-GRP" || retained.Questionnaire["tags"] == nil || *retained.Questionnaire["tags"] != "retained submitted answer" {
				t.Fatal("Apply discarded retained excluded-tracker instructions")
			}
		}
		if !step.ready {
			if current.Dupes != nil || current.Workflow.DryRun != nil {
				t.Fatal("incomplete questionnaire retained duplicate/upload authority")
			}
			state, loadErr := repository.Load(ctx, owner, current.Workflow.ID)
			if loadErr != nil || state.PendingDuplicateReuse == nil {
				t.Fatalf("incomplete answer lost baseline: %v", loadErr)
			}
			continue
		}
		if current.Dupes == nil {
			t.Fatalf("valid %s answer lost compatible duplicate evidence; projection=%+v", step.review, current.Projections.Projections[0])
		}
		if current.Dupes.ID == baseline.ID || current.Dupes.ProjectionSet.ID != current.Projections.ID || current.Dupes.ProjectionSet.Revision != current.Projections.Revision {
			t.Fatal("rebound evidence does not reference the current exact projection")
		}
		if !current.Dupes.Results[0].FreshUntil.Equal(baseline.Results[0].FreshUntil) || !current.Dupes.Results[0].CheckedAt.Equal(baseline.Results[0].CheckedAt) {
			t.Fatal("answer edit extended duplicate freshness")
		}
		if got := current.Projections.Projections[0].QuestionnaireAnswers[answerKey]; got != step.review {
			t.Fatalf("Continue short-circuited with old answer %q", got)
		}
	}
	// Non-answer settings cannot carry old authority through the questionnaire seam.
	instruction := intent.ProjectionInstructions[trackerID]
	instruction.TrackerConfig.Anon = new(false)
	intent.ProjectionInstructions[trackerID] = instruction
	settle(api.WorkflowGoalTrackersAssessed)
	if current.Dupes != nil {
		t.Fatal("configuration edit retained duplicate authority")
	}
}
