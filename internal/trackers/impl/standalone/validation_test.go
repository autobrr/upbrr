// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package standalone

import (
	"context"
	"strings"
	"testing"

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
