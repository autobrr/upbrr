// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"errors"
	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/trackers"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestValidationAdapterPreservesEditionCategories(t *testing.T) {
	t.Parallel()
	original := api.UploadSubject{
		EditionSet:   "2in1",
		Cut:          "Extended",
		Edition:      "Collector's",
		Presentation: "Open Matte",
	}
	validated := api.NewTrackerValidationSubject(original, "EXAMPLE")
	restored := unit3DUploadSubject(validated)
	if restored.EditionSet != original.EditionSet || restored.Cut != original.Cut || restored.Edition != original.Edition || restored.Presentation != original.Presentation {
		t.Fatalf("adapter lost finalized edition categories: %#v", restored)
	}
}

// combinedUnit3DDefinition retains Unit3D's actual late validation and stops at
// its explicit unimplemented transport boundary, without making network calls.
type combinedUnit3DDefinition struct{ *Definition }

func (*combinedUnit3DDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }

func (*combinedUnit3DDefinition) UploadContentMode() trackers.UploadContentMode {
	return trackers.UploadContentModeNone
}
func (d *combinedUnit3DDefinition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	_, err := d.prepareUpload(ctx, input)
	if !errors.Is(err, internalerrors.ErrNotImplemented) {
		message := "unexpected protocol result"
		if err != nil {
			message = err.Error()
		}
		return trackers.TrackerPlan{}, trackers.NewPreparationFailure(input.Tracker, "rules", message, err)
	}
	return trackers.NewUploadPlan(input.Tracker, api.TrackerDryRunEntry{Status: "ready"}, nil, nil), nil
}

func TestUnit3DServicePreparationUsesExactCombinedWarningSet(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"unchanged", "site warning changed", "generic strict added"} {
		t.Run(change, func(t *testing.T) {
			policy := trackers.ValidationPolicyBinding{ID: "combined-v1", Check: func(_ context.Context, s api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
				return []api.RuleFailure{trackers.NewRuleFailure("language_test", "Trumpable release: "+s.EffectiveMetadata.Title, api.RuleDispositionWaivable)}, nil
			}}
			definition := &combinedUnit3DDefinition{NewWithProfile(Profile{
				Name:             "COMBINED",
				Rules:            &trackers.RuleSet{RequireAudioLanguages: true, RequireUniqueID: true},
				ValidationPolicy: policy,
				MetadataPolicy:   &trackers.TrackerMetadataPolicy{},
			})}
			registry := trackers.NewRegistry()
			if err := registry.Register(definition); err != nil {
				t.Fatal(err)
			}
			subject := api.UploadSubject{
				SourcePath:        filepath.Join(t.TempDir(), "Example.mkv"),
				Type:              "WEBDL",
				Identity:          api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				Release:           api.ReleaseInfo{Resolution: "1080p"},
				EffectiveMetadata: api.EffectiveMetadata{Title: "original"},
				Assessments:       api.ReleaseAssessments{MediaInfoUniqueID: api.UniqueIDStatusPresent, MediaInfoEncodeSettings: api.EncodeSettingsStatusNotApplicable},
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
