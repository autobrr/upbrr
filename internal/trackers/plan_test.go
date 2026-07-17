// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackerPlanIsImmutableSingleUseAndExactOnceRelease(t *testing.T) {
	t.Parallel()
	preview := api.TrackerDryRunEntry{
		Tracker: "AITHER",
		Status:  "ready",
		Payload: map[string]string{"name": "Example.Release.2026.1080p-GRP"},
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent",
			Path:    "example.torrent",
			Present: true,
		}},
	}
	var submits atomic.Int32
	var releases atomic.Int32
	plan := NewUploadPlan("AITHER", preview, func(context.Context) (api.UploadSummary, error) {
		submits.Add(1)
		return api.UploadSummary{Uploaded: 1}, nil
	}, func() error {
		releases.Add(1)
		return nil
	})

	preview.Payload["name"] = "mutated"
	preview.Files[0].Path = "mutated"
	first := plan.DryRun()
	if first.Payload["name"] != "Example.Release.2026.1080p-GRP" || first.Files[0].Path != "example.torrent" {
		t.Fatalf("plan retained caller mutation: %#v", first)
	}
	first.Payload["name"] = "mutated again"
	if plan.DryRun().Payload["name"] != "Example.Release.2026.1080p-GRP" {
		t.Fatal("dry-run accessor exposes mutable plan state")
	}

	summary, err := plan.Submit(context.Background())
	if err != nil || summary.Uploaded != 1 {
		t.Fatalf("submit = %#v, %v", summary, err)
	}
	if _, err := plan.Submit(context.Background()); !errors.Is(err, ErrPlanAlreadyUsed) {
		t.Fatalf("second submit error = %v", err)
	}
	if err := plan.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := plan.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
	if submits.Load() != 1 || releases.Load() != 1 {
		t.Fatalf("submits=%d releases=%d", submits.Load(), releases.Load())
	}
}

func TestTrackerPlanReleaseBeforeSubmitRejectsSubmission(t *testing.T) {
	t.Parallel()
	plan := NewUploadPlan("BLU", api.TrackerDryRunEntry{}, func(context.Context) (api.UploadSummary, error) {
		return api.UploadSummary{Uploaded: 1}, nil
	}, nil)
	if err := plan.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := plan.Submit(context.Background()); !errors.Is(err, ErrPlanReleased) {
		t.Fatalf("submit after release error = %v", err)
	}
}

func TestNonUploadPlansCannotSubmit(t *testing.T) {
	t.Parallel()
	for _, plan := range []TrackerPlan{
		NewDescriptionPlan("AITHER", DescriptionResult{Group: "unit3d", Description: "example"}),
		NewDryRunPlan("AITHER", api.TrackerDryRunEntry{Tracker: "AITHER"}, nil),
	} {
		if _, err := plan.Submit(context.Background()); !errors.Is(err, ErrPlanNotSubmittable) {
			t.Fatalf("submit error = %v", err)
		}
	}
}

func TestPrepareAdapterBuildsUploadOperationOnce(t *testing.T) {
	t.Parallel()

	var builds atomic.Int32
	preparedName := ""
	input := PreparationInput{
		Intent:  PreparationIntentUpload,
		Tracker: "AITHER",
		Meta:    api.UploadSubject{ReleaseName: "Example.Release.2026.1080p-GRP"},
	}
	plan, failure := PrepareAdapter(
		context.Background(),
		input,
		func(context.Context, PreparationInput) (DescriptionResult, error) {
			return DescriptionResult{}, nil
		},
		func(_ context.Context, preparedInput PreparationInput) (PreparedOperation, error) {
			builds.Add(1)
			preparedName = preparedInput.Meta.ReleaseName
			preview := api.TrackerDryRunEntry{Tracker: preparedInput.Tracker, Payload: map[string]string{"name": preparedName}}
			return NewPreparedOperation(preview, func(context.Context) (api.UploadSummary, error) {
				if preparedName != "Example.Release.2026.1080p-GRP" {
					t.Fatalf("captured release name = %q", preparedName)
				}
				return api.UploadSummary{Uploaded: 1}, nil
			}, nil), nil
		},
	)
	if failure != nil {
		t.Fatalf("prepare upload: %v", failure)
	}
	input.Meta.ReleaseName = "mutated"
	if got := plan.DryRun().Payload["name"]; got != "Example.Release.2026.1080p-GRP" {
		t.Fatalf("preview name = %q", got)
	}
	if _, err := plan.Submit(context.Background()); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if builds.Load() != 1 {
		t.Fatalf("upload preparation builds = %d", builds.Load())
	}
}

func TestPrepareAdapterKeepsPreviewIntentsNonSubmittable(t *testing.T) {
	t.Parallel()

	for _, intent := range []PreparationIntent{PreparationIntentDryRun, PreparationIntentUploadReview} {
		t.Run(string(intent), func(t *testing.T) {
			t.Parallel()
			var releases atomic.Int32
			plan, failure := PrepareAdapter(
				context.Background(),
				PreparationInput{Intent: intent, Tracker: "BLU"},
				func(context.Context, PreparationInput) (DescriptionResult, error) {
					return DescriptionResult{}, nil
				},
				func(context.Context, PreparationInput) (PreparedOperation, error) {
					return NewPreparedOperation(
						api.TrackerDryRunEntry{Tracker: "BLU"},
						func(context.Context) (api.UploadSummary, error) {
							t.Fatal("preview intent reached submission")
							return api.UploadSummary{}, nil
						},
						func() error {
							releases.Add(1)
							return nil
						},
					), nil
				},
			)
			if failure != nil {
				t.Fatalf("prepare %s: %v", intent, failure)
			}
			if plan.Intent() != intent {
				t.Fatalf("plan intent = %q, want %q", plan.Intent(), intent)
			}
			if _, err := plan.Submit(context.Background()); !errors.Is(err, ErrPlanNotSubmittable) {
				t.Fatalf("submit error = %v", err)
			}
			if err := plan.Release(); err != nil {
				t.Fatalf("release: %v", err)
			}
			if releases.Load() != 1 {
				t.Fatalf("releases = %d", releases.Load())
			}
		})
	}
}

func TestPreparationInputExcludesBroadRuntimeDependencies(t *testing.T) {
	t.Parallel()
	typeOf := reflect.TypeFor[PreparationInput]()
	for _, forbidden := range []string{"AppConfig", "Repo", "Registry", "Images"} {
		if _, ok := typeOf.FieldByName(forbidden); ok {
			t.Fatalf("PreparationInput retains broad dependency field %s", forbidden)
		}
	}
}

func TestPreparationFailureSanitizesMessage(t *testing.T) {
	t.Parallel()
	failure := NewPreparationFailure("AITHER", "auth", "request failed: https://tracker.invalid/upload?api_key=secret-value", errors.New("raw cause"))
	if strings.Contains(failure.Message(), "secret-value") || strings.Contains(failure.Error(), "secret-value") {
		t.Fatalf("failure exposed credential: %q", failure.Error())
	}
	if !errors.Is(failure, failure.Unwrap()) {
		t.Fatal("failure did not retain its private diagnostic cause")
	}
}
