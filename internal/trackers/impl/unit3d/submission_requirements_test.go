// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/submission"
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
	if calls != 2 {
		t.Fatal("debug preparation skipped final description validation")
	}
	want := errors.New("missing final assets")
	profile.SubmissionRequirements = nil
	profile.ValidateFinalDescription = func(api.UploadSubject, string, string, string) error { return want }
	for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
		if err := applySubmissionRequirements(trackers.PreparationInput{ExecutionMode: mode}, data, profile); !errors.Is(err, want) || !errors.Is(err, errFinalDescriptionRequirements) {
			t.Fatalf("final validation mode=%s error=%v", mode, err)
		}
	}
}

func TestDebugFinalDescriptionFailureRetainsPayload(t *testing.T) {
	mediaPath := filepath.Join(t.TempDir(), "mediainfo.txt")
	if err := os.WriteFile(mediaPath, []byte("General\nFormat: Matroska"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := trackers.PreparationInput{
		Tracker:       "EXAMPLE",
		TrackerConfig: config.TrackerConfig{APIKey: "test-key"},
		Projection: &api.TrackerReleaseProjection{
			TrackerID:         "EXAMPLE",
			UploadReleaseName: "Example.Release.2026.1080p.WEB-DL-GRP",
			Readiness:         api.ReadinessStatusReady,
			UploadReady:       true,
			DiagnosticReady:   true,
		},
		Meta: api.UploadSubject{
			TorrentPath:       filepath.Join(t.TempDir(), "example.torrent"),
			MediaInfoTextPath: mediaPath,
			Identity:          api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 1234567},
			Type:              "WEBDL",
			Release:           api.ReleaseInfo{Resolution: "1080p"},
			Assessments:       api.ReleaseAssessments{MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent},
		},
		Assets: &trackers.DescriptionAssets{Description: "description", Final: true},
	}
	if err := os.WriteFile(input.Meta.TorrentPath, []byte("synthetic torrent"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := errors.New("missing final assets")
	profile := SiteProfile{ValidateFinalDescription: func(api.UploadSubject, string, string, string) error { return want }}
	if _, err := buildUploadDryRunUnit3D(t.Context(), input, "https://tracker.example.invalid", profile); !errors.Is(err, want) {
		t.Fatalf("normal description validation = %v", err)
	}
	input.ExecutionMode = api.WorkflowExecutionModeDebug
	preview, err := buildUploadDryRunUnit3D(t.Context(), input, "https://tracker.example.invalid", profile)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != "blocked" || !strings.Contains(preview.Message, want.Error()) || preview.Payload["description"] != "description" || len(preview.Files) != 1 {
		t.Fatal("debug lost final payload or its blocked validation outcome")
	}
	definition := NewWithProfile(Profile{
		Name:    "EXAMPLE",
		BaseURL: "https://tracker.example.invalid",
		Site:    profile,
		ReleaseNamePolicy: trackers.SubjectReleaseNamePolicy("example/v1", func(api.UploadSubject, config.TrackerConfig) string {
			return "Example.Release.2026.1080p.WEB-DL-GRP"
		}),
	})
	input.Projection = nil
	input.Intent = trackers.PreparationIntentUpload
	input, nameFailure := trackers.PrepareInputWithReleaseNamePolicy(input, definition.ReleaseNamePolicy())
	if nameFailure != nil {
		t.Fatal(nameFailure)
	}
	input.Projection.Readiness = api.ReadinessStatusBlocked
	input.Projection.UploadReady = false
	input.Projection.DiagnosticReady = true
	plan, failure := definition.Prepare(t.Context(), input)
	if failure != nil {
		t.Fatal(failure)
	}
	prepared := plan.DryRun()
	if prepared.Status != "blocked" || !strings.Contains(prepared.Message, want.Error()) || prepared.Payload["description"] != "description" {
		t.Fatal("definition preparation lost diagnostic payload")
	}
	if _, err := plan.Submit(t.Context()); !errors.Is(err, trackers.ErrPlanNotSubmittable) || submission.OutcomeOf(err) != submission.NotSent {
		t.Fatal("debug definition permitted a tracker submission")
	}
}
