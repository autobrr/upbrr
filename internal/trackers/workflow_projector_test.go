// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

type projectionInputDefinition struct{ inputSchemaDefinition }

func (projectionInputDefinition) InputReadiness(subject api.UploadSubject) []api.InputReadinessFieldOutcome {
	if subject.TrackerQuestionnaireAnswers["ONE"]["choice"] == "valid" {
		return nil
	}
	return []api.InputReadinessFieldOutcome{{
		Key:         "tracker_input.choice",
		Status:      api.InputReadinessFieldInvalid,
		Disposition: api.RuleDispositionStrict,
		Message:     "Correct the input choice",
	}}
}

func TestWorkflowProjectionIsolatesInvalidInputAndPreservesItsAnswerOwner(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(projectionInputDefinition{inputSchemaDefinition{name: "ONE", options: []string{"valid", "invalid"}}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(stubDefinition{name: "TWO"}); err != nil {
		t.Fatal(err)
	}
	projector, err := NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	for _, answer := range []string{"invalid", "valid"} {
		instructions := map[api.TrackerID]api.TrackerProjectionInstructions{"ONE": {Questionnaire: map[string]*string{"choice": new("valid"), "poster": new("retained")}}}
		_, _, _, result, err := projector.Build(t.Context(), api.ReleaseSnapshot{}, api.UploadSubject{
			ReleaseName:                 "Example.Release.2026.1080p-GRP",
			TrackerQuestionnaireAnswers: map[string]map[string]string{"ONE": {"choice": answer}},
		}, []api.TrackerID{"ONE", "TWO"}, instructions, nil, api.WorkflowExecutionModeNormal)
		if err != nil {
			t.Fatal(err)
		}
		for _, projection := range result.Projections {
			if projection.TrackerID == "ONE" && answer == "invalid" {
				if projection.Readiness != api.ReadinessStatusIneligible || projection.DupeReady || projection.UploadReady {
					t.Fatalf("invalid input crossed projection: %#v", projection)
				}
			} else if projection.Readiness != api.ReadinessStatusReady || !projection.DupeReady || !projection.UploadReady {
				t.Fatalf("valid lane blocked: %#v", projection)
			}
		}
		if instructions["ONE"].Questionnaire["choice"] == nil {
			t.Fatal("projection mutated caller questionnaire")
		}
	}
}

func TestWorkflowProjectorUsesContextLogger(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:              "EXAMPLE",
		Definition:        stubDefinition{name: "EXAMPLE"},
		UploadContentMode: UploadContentModeDescription,
		Validation: ValidationPolicyBinding{
			ID: "example-validation-v1",
			Check: func(context.Context, api.TrackerValidationSubject, api.Logger) ([]api.RuleFailure, error) {
				return []api.RuleFailure{NewRuleFailure("example_rule", "example reason", api.RuleDispositionStrict)}, nil
			},
		},
	}); err != nil {
		t.Fatalf("register tracker: %v", err)
	}
	rootLogger := &warningLogger{}
	contextLogger := &warningLogger{}
	projector, err := NewWorkflowProjector(registry, config.Config{}, rootLogger)
	if err != nil {
		t.Fatalf("new workflow projector: %v", err)
	}

	ctx := logging.WithOperationLogger(context.Background(), contextLogger)
	if _, _, _, _, err := projector.Build(
		ctx,
		api.ReleaseSnapshot{},
		api.UploadSubject{ReleaseName: "Example.Release.2026.1080p-GRP"},
		[]api.TrackerID{"EXAMPLE"},
		nil,
		nil,
		api.WorkflowExecutionModeNormal,
	); err != nil {
		t.Fatalf("build projections: %v", err)
	}
	if len(rootLogger.warnings) != 0 || len(contextLogger.warnings) != 1 {
		t.Fatalf("projection warnings: root=%#v context=%#v", rootLogger.warnings, contextLogger.warnings)
	}
}

func TestResolveTrackerIDsIgnoresUnsupportedConfiguredDefaults(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDefinition{name: "SUPPORTED"}); err != nil {
		t.Fatalf("register tracker: %v", err)
	}
	logger := &warningLogger{}
	projector, err := NewWorkflowProjector(registry, config.Config{
		Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"SUPPORTED", "RETIRED"}},
	}, logger)
	if err != nil {
		t.Fatalf("new workflow projector: %v", err)
	}

	selected, err := projector.resolveTrackerIDs(nil)
	if err != nil {
		t.Fatalf("resolve configured defaults: %v", err)
	}
	if len(selected) != 1 || selected[0] != "SUPPORTED" {
		t.Fatalf("selected trackers = %v, want [SUPPORTED]", selected)
	}
	if len(logger.warnings) != 1 || logger.warnings[0] != "trackers: projection tracker=RETIRED state=unregistered decision=skip count=1" {
		t.Fatalf("configured default warnings = %#v", logger.warnings)
	}

	if _, err := projector.resolveTrackerIDs([]api.TrackerID{"RETIRED"}); err == nil {
		t.Fatal("expected explicit unsupported tracker to fail")
	}
}

func TestApplyWorkflowProjectionRequirementsDoesNotInferDVDMenuMinimumFromCaptureCap(t *testing.T) {
	t.Parallel()

	cfg := config.Config{}
	cfg.ScreenshotHandling.Screens = 4
	cfg.ScreenshotHandling.MaxMenuItems = 6
	projection := api.TrackerReleaseProjection{}

	applyWorkflowProjectionRequirements(
		&projection,
		Descriptor{Name: "EXAMPLE", UploadContentMode: UploadContentModeDescription},
		nil,
		cfg,
	)

	if projection.Artifacts.ScreenshotCount != 4 || projection.Artifacts.DVDMenuCount != 0 {
		t.Fatalf("projected media requirements = %#v", projection.Artifacts)
	}
}

func TestApplyWorkflowProjectionRequirementsUsesOptionalScreenshotOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		trackerImageCount int
		override          *int
		want              int
	}{
		{
			name:     "zero bypasses global fallback",
			override: new(0),
			want:     0,
		},
		{
			name:              "override increases tracker minimum",
			trackerImageCount: 3,
			override:          new(5),
			want:              5,
		},
		{
			name:              "tracker minimum wins",
			trackerImageCount: 5,
			override:          new(3),
			want:              5,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{
				ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 4},
				Trackers:           config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"EXAMPLE": {ImageCount: test.trackerImageCount}}},
			}
			projection := api.TrackerReleaseProjection{}
			applyWorkflowProjectionRequirements(
				&projection,
				Descriptor{Name: "EXAMPLE", UploadContentMode: UploadContentModeDescription},
				test.override,
				cfg,
			)
			if projection.Artifacts.ScreenshotCount != test.want {
				t.Fatalf("projected screenshot count = %d, want %d", projection.Artifacts.ScreenshotCount, test.want)
			}
		})
	}
}

func TestWorkflowProjectorPassesScreenshotOverride(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:              "EXAMPLE",
		Definition:        stubDefinition{name: "EXAMPLE"},
		UploadContentMode: UploadContentModeDescription,
	}); err != nil {
		t.Fatalf("register tracker: %v", err)
	}
	projector, err := NewWorkflowProjector(registry, config.Config{
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 4},
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("new workflow projector: %v", err)
	}
	override := 0
	_, _, _, result, err := projector.Build(
		t.Context(),
		api.ReleaseSnapshot{},
		api.UploadSubject{ReleaseName: "Example.Release.2026.1080p-GRP"},
		[]api.TrackerID{"EXAMPLE"},
		map[api.TrackerID]api.TrackerProjectionInstructions{"EXAMPLE": {ScreenshotCount: &override}},
		nil,
		api.WorkflowExecutionModeNormal,
	)
	if err != nil {
		t.Fatalf("build projections: %v", err)
	}
	if len(result.Projections) != 1 || result.Projections[0].Artifacts.ScreenshotCount != 0 {
		t.Fatalf("projected screenshot requirements = %#v", result.Projections)
	}
}

func TestApplyWorkflowProjectionRequirementsKeepsExplicitDVDMenuMinimum(t *testing.T) {
	t.Parallel()

	projection := api.TrackerReleaseProjection{}
	applyWorkflowProjectionRequirements(
		&projection,
		Descriptor{
			Name:              "EXAMPLE",
			UploadContentMode: UploadContentModeDescription,
			WorkflowMedia:     &WorkflowMediaRequirements{DVDMenuCount: 2},
		},
		nil,
		config.Config{},
	)

	if projection.Artifacts.DVDMenuCount != 2 {
		t.Fatalf("projected DVD menu minimum = %d, want 2", projection.Artifacts.DVDMenuCount)
	}
}

func TestApplyQuestionnaireInstructionMergesAndClearsTrackerAnswers(t *testing.T) {
	t.Parallel()

	base := api.UploadSubject{TrackerQuestionnaireAnswers: map[string]map[string]string{
		"PTP": {"no_english_subtitles": "yes"},
	}}
	projected := base
	poster := "https://images.example/poster.jpg"
	applyQuestionnaireInstruction(&projected, "PTP", api.TrackerProjectionInstructions{
		Questionnaire: map[string]*string{"poster": &poster},
	})
	answers := projected.TrackerQuestionnaireAnswers["PTP"]
	if answers["no_english_subtitles"] != "yes" || answers["poster"] != poster {
		t.Fatalf("merged PTP answers = %#v", answers)
	}
	if _, found := base.TrackerQuestionnaireAnswers["PTP"]["poster"]; found {
		t.Fatalf("projection instruction mutated base answers: %#v", base.TrackerQuestionnaireAnswers)
	}

	applyQuestionnaireInstruction(&projected, "PTP", api.TrackerProjectionInstructions{
		Questionnaire: map[string]*string{"no_english_subtitles": nil},
	})
	answers = projected.TrackerQuestionnaireAnswers["PTP"]
	if _, found := answers["no_english_subtitles"]; found || answers["poster"] != poster {
		t.Fatalf("automatic PTP answer reset = %#v", answers)
	}
}

func TestWorkflowProjectorFingerprintIncludesSelectedTrackerAnswers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDefinition{name: "PTP"}); err != nil {
		t.Fatalf("register tracker: %v", err)
	}
	projector, err := NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatalf("new workflow projector: %v", err)
	}
	build := func(subject api.UploadSubject) api.TrackerReleaseProjectionSet {
		t.Helper()
		_, _, _, projections, buildErr := projector.Build(
			t.Context(),
			api.ReleaseSnapshot{},
			subject,
			[]api.TrackerID{"PTP"},
			nil,
			nil,
			api.WorkflowExecutionModeNormal,
		)
		if buildErr != nil {
			t.Fatalf("build projections: %v", buildErr)
		}
		return projections
	}
	withAnswer := build(api.UploadSubject{
		ReleaseName: "Example.Release.2026.1080p-GRP",
		TrackerQuestionnaireAnswers: map[string]map[string]string{
			"PTP": {"no_english_subtitles": "yes"},
		},
	})
	withUnselectedAnswer := build(api.UploadSubject{
		ReleaseName: "Example.Release.2026.1080p-GRP",
		TrackerQuestionnaireAnswers: map[string]map[string]string{
			"PTP":   {"no_english_subtitles": "yes"},
			"OTHER": {"edition": "director"},
		},
	})
	auto := build(api.UploadSubject{ReleaseName: "Example.Release.2026.1080p-GRP"})
	if withAnswer.InputFingerprint == auto.InputFingerprint {
		t.Fatalf("answer reset did not change projection fingerprint: %q", withAnswer.InputFingerprint)
	}
	if withAnswer.InputFingerprint != withUnselectedAnswer.InputFingerprint {
		t.Fatalf("unselected answers changed projection fingerprint: with=%q unselected=%q", withAnswer.InputFingerprint, withUnselectedAnswer.InputFingerprint)
	}
}
