// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package standalone

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestValidatePreparationExecutionPolicy(t *testing.T) {
	t.Parallel()

	policy := func(disposition api.RuleDisposition) trackers.ValidationPolicyBinding {
		return trackers.ValidationPolicyBinding{
			ID: "test-policy-v1",
			Check: func(context.Context, api.TrackerValidationSubject, api.Logger) ([]api.RuleFailure, error) {
				return []api.RuleFailure{trackers.NewRuleFailure("test_rule", "test reason", disposition)}, nil
			},
		}
	}
	input := trackers.PreparationInput{
		Intent:  trackers.PreparationIntentUpload,
		Tracker: "EXAMPLE",
		Logger:  api.NopLogger{},
	}

	if err := ValidatePreparation(context.Background(), input, policy(api.RuleDispositionStrict)); err == nil ||
		!strings.Contains(err.Error(), "test_rule") {
		t.Fatalf("strict failure must block direct preparation: %v", err)
	}
	if err := ValidatePreparation(context.Background(), input, policy(api.RuleDispositionWaivable)); err == nil {
		t.Fatal("waivable failure must block normal direct preparation")
	}
	waivableFingerprint, err := trackers.WaivableRuleFailureFingerprint("EXAMPLE", []api.RuleFailure{
		trackers.NewRuleFailure("test_rule", "test reason", api.RuleDispositionWaivable),
	})
	if err != nil {
		t.Fatalf("fingerprint waivable failure: %v", err)
	}
	input.Projection = &api.TrackerReleaseProjection{
		WaivableRuleFingerprint:      waivableFingerprint,
		RuleAuthorizationFingerprint: waivableFingerprint,
	}
	if err := ValidatePreparation(context.Background(), input, policy(api.RuleDispositionWaivable)); err != nil {
		t.Fatalf("exactly authorized waivable failure must pass normal direct preparation: %v", err)
	}
	changedPolicy := trackers.ValidationPolicyBinding{
		ID: "test-policy-v2",
		Check: func(context.Context, api.TrackerValidationSubject, api.Logger) ([]api.RuleFailure, error) {
			return []api.RuleFailure{
				trackers.NewRuleFailure("test_rule", "changed reason", api.RuleDispositionWaivable),
			}, nil
		},
	}
	if err := ValidatePreparation(context.Background(), input, changedPolicy); err == nil {
		t.Fatal("changed waivable failure must invalidate retained authorization")
	}
	input.Projection = nil
	input.ExecutionMode = api.WorkflowExecutionModeDebug
	if err := ValidatePreparation(context.Background(), input, policy(api.RuleDispositionWaivable)); err != nil {
		t.Fatalf("debug must bypass waivable direct-preparation policy: %v", err)
	}
	if err := ValidatePreparation(context.Background(), input, policy(api.RuleDispositionStrict)); err == nil {
		t.Fatal("debug must not bypass strict direct-preparation failure")
	}
}

func TestValidatePreparationRejectsInvalidDirectDryRun(t *testing.T) {
	t.Parallel()

	policy := trackers.ValidationPolicyBinding{
		ID: "test-policy-v1",
		Check: func(context.Context, api.TrackerValidationSubject, api.Logger) ([]api.RuleFailure, error) {
			return []api.RuleFailure{trackers.NewRuleFailure(
				"required_questionnaire_answer",
				"answer required",
				api.RuleDispositionStrict,
			)}, nil
		},
	}
	err := ValidatePreparation(context.Background(), trackers.PreparationInput{
		Intent:  trackers.PreparationIntentDryRun,
		Tracker: "EXAMPLE",
		Logger:  api.NopLogger{},
	}, policy)
	if err == nil || !strings.Contains(err.Error(), "required_questionnaire_answer") {
		t.Fatalf("direct dry run must reject invalid facts: %v", err)
	}
}

func TestValidatePreparationDoesNotPromotePartialTypedBDInfo(t *testing.T) {
	t.Parallel()

	policy := trackers.ValidationPolicyBinding{
		ID: "prepared-media-v1",
		Check: func(_ context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if PreparedMediaReady(subject) {
				return nil, nil
			}
			return []api.RuleFailure{trackers.NewRuleFailure(
				"required_media",
				"prepared media is required",
				api.RuleDispositionStrict,
			)}, nil
		},
	}
	input := trackers.PreparationInput{
		Intent:  trackers.PreparationIntentUpload,
		Tracker: "EXAMPLE",
		Meta: api.UploadSubject{
			SourcePath:            `C:\media\Example.Release.2026`,
			DiscType:              "BDMV",
			SelectedBDMVPlaylists: []api.PlaylistInfo{{ID: "disc-one:00001.MPLS"}},
			Disc: api.DiscFacts{Items: []api.DiscItemFacts{
				{
					ID:   "disc-one",
					Name: "Disc 1",
					Type: "BDMV",
					Reports: []api.DiscReportFacts{{
						Playlist: api.PlaylistInfo{ID: "disc-one:00001.MPLS"},
						Summary:  "BDINFO ONE",
					}},
				},
				{
					ID:   "disc-two",
					Name: "Disc 2",
					Type: "BDMV",
					Reports: []api.DiscReportFacts{{
						Playlist: api.PlaylistInfo{ID: "disc-two:00001.MPLS"},
					}},
				},
			}},
		},
		Logger: api.NopLogger{},
	}
	if err := ValidatePreparation(context.Background(), input, policy); err == nil || !strings.Contains(err.Error(), "required_media") {
		t.Fatalf("partial typed BDInfo must remain blocked: %v", err)
	}

	input.Meta.Disc = api.DiscFacts{}
	if err := ValidatePreparation(context.Background(), input, policy); err != nil {
		t.Fatalf("legacy singular BDInfo compatibility failed: %v", err)
	}
}

func TestValidationAdapterPreservesEditionCategories(t *testing.T) {
	t.Parallel()
	original := api.UploadSubject{
		EditionSet:   "2in1",
		Cut:          "Extended",
		Edition:      "Collector's",
		Presentation: "Open Matte",
	}
	validated := api.NewTrackerValidationSubject(original, "EXAMPLE")
	restored := UploadSubjectForValidation(validated)
	if restored.EditionSet != original.EditionSet || restored.Cut != original.Cut || restored.Edition != original.Edition || restored.Presentation != original.Presentation {
		t.Fatalf("adapter lost finalized edition categories: %#v", restored)
	}
}

// combinedRuleDefinition exercises the real service-created input and standalone
// defensive check, stopping before any external upload protocol.
type combinedRuleDefinition struct {
	policy trackers.ValidationPolicyBinding
}

func (combinedRuleDefinition) Name() string           { return "COMBINED" }
func (combinedRuleDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }
func (combinedRuleDefinition) UploadContentMode() trackers.UploadContentMode {
	return trackers.UploadContentModeNone
}
func (combinedRuleDefinition) Rules() *trackers.RuleSet {
	return &trackers.RuleSet{RequireAudioLanguages: true, RequireUniqueID: true}
}
func (d combinedRuleDefinition) ValidationPolicy() trackers.ValidationPolicyBinding { return d.policy }
func (d combinedRuleDefinition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	if err := ValidatePreparation(ctx, input, d.policy); err != nil {
		return trackers.TrackerPlan{}, trackers.NewPreparationFailure(input.Tracker, "rules", err.Error(), err)
	}
	return trackers.NewUploadPlan(input.Tracker, api.TrackerDryRunEntry{Status: "ready"}, nil, nil), nil
}

func TestServicePreparationValidatesExactCombinedWarningSet(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"unchanged", "site warning changed", "generic strict added"} {
		t.Run(change, func(t *testing.T) {
			policy := trackers.ValidationPolicyBinding{ID: "combined-v1", Check: func(_ context.Context, s api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
				return []api.RuleFailure{trackers.NewRuleFailure("language_test", "Trumpable release: "+s.EffectiveMetadata.Title, api.RuleDispositionWaivable)}, nil
			}}
			registry := trackers.NewRegistry()
			if err := registry.Register(combinedRuleDefinition{policy: policy}); err != nil {
				t.Fatal(err)
			}
			subject := api.UploadSubject{
				SourcePath:        filepath.Join(t.TempDir(), "Example.mkv"),
				EffectiveMetadata: api.EffectiveMetadata{Title: "original"},
				Assessments:       api.ReleaseAssessments{MediaInfoUniqueID: api.UniqueIDStatusPresent},
			}
			failures, err := trackers.EvaluateTrackerValidationWithRegistry(t.Context(), registry, "COMBINED", api.NewTrackerValidationSubject(subject, "COMBINED"), api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			if len(failures) != 2 {
				t.Fatalf("need generic+site warning fixture: %+v", failures)
			}
			fingerprint, err := trackers.WaivableRuleFailureFingerprint("COMBINED", failures)
			if err != nil {
				t.Fatal(err)
			}
			if change == "site warning changed" {
				subject.EffectiveMetadata.Title = "changed"
			}
			if change == "generic strict added" {
				subject.Assessments.MediaInfoUniqueID = api.UniqueIDStatusMissing
			}
			service := trackers.NewServiceWithRegistry(config.Config{}, api.NopLogger{}, nil, registry)
			retained, err := service.PrepareRetainedUploadPlan(t.Context(), subject, []api.TrackerReleaseProjection{{
				TrackerID:                    "COMBINED",
				Readiness:                    api.ReadinessStatusReady,
				UploadReady:                  true,
				WaivableRuleFingerprint:      fingerprint,
				RuleAuthorizationFingerprint: fingerprint,
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = retained.Release() }()
			preparations := retained.Preparations()
			if len(preparations) != 1 || (preparations[0].Failure == nil) != (change == "unchanged") {
				t.Fatalf("%s preparation = %+v", change, preparations)
			}
			if change == "generic strict added" && !strings.Contains(preparations[0].Failure.Message, "require_unique_id") {
				t.Fatalf("generic strict gate lost: %+v", preparations[0].Failure)
			}
		})
	}
}
