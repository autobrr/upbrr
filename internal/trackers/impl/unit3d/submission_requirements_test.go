// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"errors"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSubmissionRequirementsFinalizeDefaults(t *testing.T) {
	for _, modQ := range []string{"0", "1"} {
		data := map[string]string{"description": "User override", "mod_queue_opt_in": modQ}
		profile := SiteProfile{SubmissionRequirements: func(trackers.PreparationInput) (SubmissionRequirements, error) {
			return SubmissionRequirements{ForceModeration: true, DescriptionAppendix: "Required explanation"}, nil
		}}
		if err := applySubmissionRequirements(trackers.PreparationInput{}, data, profile); err != nil {
			t.Fatal(err)
		}
		if err := applySubmissionRequirements(trackers.PreparationInput{}, data, profile); err != nil {
			t.Fatal(err)
		}
		if data["mod_queue_opt_in"] != "1" || data["description"] != "User override\n\nRequired explanation" {
			t.Fatalf("final payload = %#v", data)
		}
	}
}

func TestSubmissionRequirementsPreserveOrdinaryDefaults(t *testing.T) {
	data := map[string]string{"description": "User override", "mod_queue_opt_in": "0"}
	if err := applySubmissionRequirements(trackers.PreparationInput{}, data, SiteProfile{}); err != nil {
		t.Fatal(err)
	}
	if data["mod_queue_opt_in"] != "0" || data["description"] != "User override" {
		t.Fatalf("final payload = %#v", data)
	}
	want := errors.New("missing explanation")
	profile := SiteProfile{SubmissionRequirements: func(trackers.PreparationInput) (SubmissionRequirements, error) { return SubmissionRequirements{}, want }}
	if err := applySubmissionRequirements(trackers.PreparationInput{}, data, profile); !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}

func TestFinalDescriptionValidationRunsAfterAppendix(t *testing.T) {
	calls := 0
	profile := SiteProfile{
		SubmissionRequirements: func(trackers.PreparationInput) (SubmissionRequirements, error) {
			return SubmissionRequirements{DescriptionAppendix: "required appendix"}, nil
		},
		ValidateFinalDescription: func(_ api.UploadSubject, description, mediaInfo, bdInfo string) error {
			calls++
			if description != "override\n\nrequired appendix" || mediaInfo != "MI" || bdInfo != "BD" {
				t.Fatalf("final fields = %q %q %q", description, mediaInfo, bdInfo)
			}
			return nil
		},
	}
	data := map[string]string{
		"description": "override",
		"mediainfo":   "MI",
		"bdinfo":      "BD",
	}
	if err := applySubmissionRequirements(trackers.PreparationInput{}, data, profile); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("final validation did not run")
	}
	if err := applySubmissionRequirements(trackers.PreparationInput{ExecutionMode: api.WorkflowExecutionModeDebug}, data, profile); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("debug preparation invoked live-only description gate")
	}
	want := errors.New("missing final assets")
	profile.SubmissionRequirements = nil
	profile.ValidateFinalDescription = func(api.UploadSubject, string, string, string) error { return want }
	if err := applySubmissionRequirements(trackers.PreparationInput{}, data, profile); !errors.Is(err, want) {
		t.Fatalf("final validation error=%v", err)
	}
}
