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

type trackerEffectReporterStub struct {
	beginErr         error
	completeErr      error
	alreadySucceeded bool
	begun            []api.WorkflowExternalEffect
}

func (r *trackerEffectReporterStub) Begin(
	_ context.Context,
	effect api.WorkflowExternalEffect,
) (api.WorkflowExternalEffectReceipt, error) {
	r.begun = append(r.begun, effect)
	if r.beginErr != nil {
		return api.WorkflowExternalEffectReceipt{}, r.beginErr
	}
	return api.WorkflowExternalEffectReceipt{
		EffectID:         "effect-1",
		AlreadySucceeded: r.alreadySucceeded,
	}, nil
}

func (r *trackerEffectReporterStub) Complete(
	context.Context,
	api.WorkflowExternalEffectReceipt,
	bool,
) error {
	return r.completeErr
}

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
		RequiredActions: []api.RequiredAction{{
			Kind:    api.RequiredActionResolveTrackerPreparation,
			Options: []api.RequiredActionOption{{Value: "resolve", Label: "Try alternative"}},
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
	preview.RequiredActions[0].Options[0].Label = "mutated"
	first := plan.DryRun()
	if first.Payload["name"] != "Example.Release.2026.1080p-GRP" || first.Files[0].Path != "example.torrent" ||
		first.RequiredActions[0].Options[0].Label != "Try alternative" {
		t.Fatalf("plan retained caller mutation: %#v", first)
	}
	first.Payload["name"] = "mutated again"
	first.RequiredActions[0].Options[0].Label = "mutated again"
	if plan.DryRun().Payload["name"] != "Example.Release.2026.1080p-GRP" ||
		plan.DryRun().RequiredActions[0].Options[0].Label != "Try alternative" {
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

func TestTrackerSubmissionFencePreventsRetryAfterUnknownOutcome(t *testing.T) {
	t.Parallel()

	var submits atomic.Int32
	reporter := &trackerEffectReporterStub{beginErr: api.ErrReleaseWorkflowEffectOutcomeUnknown}
	ctx := api.WithWorkflowExternalEffectReporter(context.Background(), reporter)
	slots := []trackerPlanSlot{{
		tracker: "ALPHA",
		plan: NewUploadPlan(
			"ALPHA",
			api.TrackerDryRunEntry{Tracker: "ALPHA", Status: "ready"},
			func(context.Context) (api.UploadSummary, error) {
				submits.Add(1)
				return api.UploadSummary{Uploaded: 1}, nil
			},
			nil,
		),
	}}
	(&Service{}).submitTrackerPlans(ctx, api.UploadSubject{SourcePath: "C:\\synthetic"}, slots)
	if submits.Load() != 0 || slots[0].failure == nil || slots[0].failure.Code != "unknown_outcome" {
		t.Fatalf("fenced tracker submission submits=%d failure=%#v", submits.Load(), slots[0].failure)
	}
	if len(reporter.begun) != 1 || reporter.begun[0].Kind != api.WorkflowExternalEffectTrackerSubmission ||
		reporter.begun[0].ScopeID != "ALPHA" || reporter.begun[0].SemanticFingerprint == "" {
		t.Fatalf("tracker submission effect=%#v", reporter.begun)
	}
}

func TestTrackerSubmissionReceiptFailureBecomesUnknownOutcome(t *testing.T) {
	t.Parallel()

	var submits atomic.Int32
	reporter := &trackerEffectReporterStub{completeErr: errors.New("synthetic receipt failure")}
	ctx := api.WithWorkflowExternalEffectReporter(context.Background(), reporter)
	slots := []trackerPlanSlot{{
		tracker: "ALPHA",
		plan: NewUploadPlan(
			"ALPHA",
			api.TrackerDryRunEntry{Tracker: "ALPHA", Status: "ready"},
			func(context.Context) (api.UploadSummary, error) {
				submits.Add(1)
				return api.UploadSummary{Uploaded: 1}, nil
			},
			nil,
		),
	}}
	(&Service{}).submitTrackerPlans(ctx, api.UploadSubject{SourcePath: "C:\\synthetic"}, slots)
	if submits.Load() != 1 || slots[0].failure == nil || slots[0].failure.Code != "unknown_outcome" {
		t.Fatalf("unretained tracker receipt submits=%d failure=%#v", submits.Load(), slots[0].failure)
	}
}

func TestTrackerSubmissionSucceededReceiptWithoutResultBecomesUnknownOutcome(t *testing.T) {
	t.Parallel()

	var submits atomic.Int32
	reporter := &trackerEffectReporterStub{alreadySucceeded: true}
	ctx := api.WithWorkflowExternalEffectReporter(t.Context(), reporter)
	slots := []trackerPlanSlot{{
		tracker: "ALPHA",
		plan: NewUploadPlan(
			"ALPHA",
			api.TrackerDryRunEntry{Tracker: "ALPHA", Status: "ready"},
			func(context.Context) (api.UploadSummary, error) {
				submits.Add(1)
				return api.UploadSummary{Uploaded: 1}, nil
			},
			nil,
		),
	}}
	(&Service{}).submitTrackerPlans(ctx, api.UploadSubject{SourcePath: "Example.Release.2026"}, slots)
	if submits.Load() != 0 || slots[0].failure == nil || slots[0].failure.Code != "unknown_outcome" ||
		!errors.Is(slots[0].failure.cause, api.ErrReleaseWorkflowEffectAlreadySucceeded) {
		t.Fatalf("retained tracker receipt submits=%d failure=%#v", submits.Load(), slots[0].failure)
	}
	if slots[0].summary.Uploaded != 0 || len(slots[0].summary.UploadedTorrents) != 0 || slots[0].summary.PendingPublication {
		t.Fatalf("retained tracker receipt synthesized summary=%#v", slots[0].summary)
	}
}

func TestNonUploadPlansCannotSubmit(t *testing.T) {
	t.Parallel()
	for _, plan := range []TrackerPlan{
		NewDescriptionPlan("AITHER", DescriptionResult{Group: "unit3d", Description: "example"}),
		newPreviewPlan("AITHER", PreparationIntentDryRun, api.TrackerDryRunEntry{Tracker: "AITHER"}, nil),
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

func TestTrackerPlanResolveActionKeepsOrReplacesPreparedOperation(t *testing.T) {
	t.Parallel()

	newPlan := func(t *testing.T) (TrackerPlan, *atomic.Int32) {
		t.Helper()
		var resolutions atomic.Int32
		plan, failure := PrepareAdapter(
			context.Background(),
			PreparationInput{Intent: PreparationIntentUpload, Tracker: "BTN"},
			nil,
			func(context.Context, PreparationInput) (PreparedOperation, error) {
				preview := api.TrackerDryRunEntry{
					Tracker: "BTN",
					Payload: map[string]string{"source": "tvdb"},
					RequiredActions: []api.RequiredAction{{
						Kind: api.RequiredActionResolveTrackerPreparation,
					}},
				}
				return NewPreparedOperationWithResolver(
					preview,
					func(context.Context) (api.UploadSummary, error) {
						return api.UploadSummary{Uploaded: 1}, nil
					},
					nil,
					func(context.Context) (PreparedOperation, error) {
						resolutions.Add(1)
						return NewPreparedOperation(
							api.TrackerDryRunEntry{Tracker: "BTN", Payload: map[string]string{"source": "release_name"}},
							func(context.Context) (api.UploadSummary, error) {
								return api.UploadSummary{Uploaded: 1}, nil
							},
							nil,
						), nil
					},
				), nil
			},
		)
		if failure != nil {
			t.Fatalf("prepare plan: %v", failure)
		}
		return plan, &resolutions
	}

	t.Run("confirm keeps current payload", func(t *testing.T) {
		plan, resolutions := newPlan(t)
		resolved, err := plan.ResolveAction(context.Background(), api.RequiredActionResolveTrackerPreparation, true)
		if err != nil {
			t.Fatalf("resolve action: %v", err)
		}
		if len(resolved.DryRun().RequiredActions) != 0 || resolved.DryRun().Payload["source"] != "tvdb" {
			t.Fatalf("resolved preview = %#v", resolved.DryRun())
		}
		if resolutions.Load() != 0 {
			t.Fatalf("tracker resolutions = %d", resolutions.Load())
		}
		if _, err := plan.ResolveAction(context.Background(), api.RequiredActionResolveTrackerPreparation, false); !errors.Is(err, ErrPlanActionUnavailable) {
			t.Fatalf("replayed action error = %v", err)
		}
		if _, err := resolved.Submit(context.Background()); err != nil {
			t.Fatalf("submit retained payload: %v", err)
		}
	})

	t.Run("decline replaces payload", func(t *testing.T) {
		plan, resolutions := newPlan(t)
		resolved, err := plan.ResolveAction(context.Background(), api.RequiredActionResolveTrackerPreparation, false)
		if err != nil {
			t.Fatalf("resolve action: %v", err)
		}
		if resolved.DryRun().Payload["source"] != "release_name" || resolutions.Load() != 1 {
			t.Fatalf("resolved preview = %#v resolutions=%d", resolved.DryRun(), resolutions.Load())
		}
		if _, err := plan.Submit(context.Background()); !errors.Is(err, ErrPlanReleased) {
			t.Fatalf("replaced plan submit error = %v", err)
		}
		if _, err := resolved.Submit(context.Background()); err != nil {
			t.Fatalf("submit replacement payload: %v", err)
		}
	})

	t.Run("decline requires resolver", func(t *testing.T) {
		plan := newTestTrackerActionPlan(nil, nil)
		defer func() { _ = plan.Release() }()
		if _, err := plan.ResolveAction(context.Background(), api.RequiredActionResolveTrackerPreparation, false); !errors.Is(err, ErrPlanActionNotResolvable) {
			t.Fatalf("unresolvable action error = %v", err)
		}
	})

	t.Run("decline surfaces resolver failure", func(t *testing.T) {
		want := NewPreparationFailure("BTN", PreparationFailureCodeSkipped, "Tracker skipped.", nil)
		plan := newTestTrackerActionPlan(nil, func(context.Context) (TrackerPlan, *PreparationFailure) {
			return TrackerPlan{}, want
		})
		if _, err := plan.ResolveAction(context.Background(), api.RequiredActionResolveTrackerPreparation, false); !errors.Is(err, want) {
			t.Fatalf("resolver failure = %v, want %v", err, want)
		}
	})

	t.Run("decline wraps release failure", func(t *testing.T) {
		releaseErr := errors.New("release failed")
		plan := newTestTrackerActionPlan(func() error { return releaseErr }, func(context.Context) (TrackerPlan, *PreparationFailure) {
			return NewUploadPlan(
				"BTN",
				api.TrackerDryRunEntry{Tracker: "BTN"},
				func(context.Context) (api.UploadSummary, error) { return api.UploadSummary{}, nil },
				nil,
			), nil
		})
		_, err := plan.ResolveAction(context.Background(), api.RequiredActionResolveTrackerPreparation, false)
		var preparationFailure *PreparationFailure
		if !errors.As(err, &preparationFailure) || preparationFailure.Code() != "upload" || !errors.Is(err, releaseErr) {
			t.Fatalf("release failure = %v", err)
		}
	})
}

func TestTrackerPlanDecisionResolverUsesExplicitAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		confirmed bool
	}{
		{name: "confirm", confirmed: true},
		{name: "decline", confirmed: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var decided atomic.Bool
			var answer atomic.Bool
			var releases atomic.Int32
			plan, failure := PrepareAdapter(
				context.Background(),
				PreparationInput{Intent: PreparationIntentUpload, Tracker: "EXAMPLE"},
				nil,
				func(context.Context, PreparationInput) (PreparedOperation, error) {
					return NewPreparedOperationWithDecisionResolver(
						api.TrackerDryRunEntry{
							Tracker: "EXAMPLE",
							RequiredActions: []api.RequiredAction{{
								Kind: api.RequiredActionResolveTrackerPreparation,
							}},
						},
						func(context.Context) (api.UploadSummary, error) {
							return api.UploadSummary{}, errors.New("unresolved operation submitted")
						},
						func() error {
							releases.Add(1)
							return nil
						},
						func(_ context.Context, confirmed bool) (PreparedOperation, error) {
							answer.Store(confirmed)
							decided.Store(true)
							return NewPreparedOperation(
								api.TrackerDryRunEntry{Tracker: "EXAMPLE", Payload: map[string]string{"resolved": "true"}},
								func(context.Context) (api.UploadSummary, error) {
									return api.UploadSummary{Uploaded: 1}, nil
								},
								nil,
							), nil
						},
					), nil
				},
			)
			if failure != nil {
				t.Fatalf("prepare decision plan: %v", failure)
			}

			resolved, err := plan.ResolveAction(context.Background(), api.RequiredActionResolveTrackerPreparation, test.confirmed)
			if err != nil {
				t.Fatalf("resolve decision: %v", err)
			}
			if !decided.Load() || answer.Load() != test.confirmed || releases.Load() != 1 {
				t.Fatalf("decision called=%t answer=%t releases=%d", decided.Load(), answer.Load(), releases.Load())
			}
			if resolved.DryRun().Payload["resolved"] != "true" {
				t.Fatalf("resolved preview = %#v", resolved.DryRun())
			}
			if _, err := plan.Submit(context.Background()); !errors.Is(err, ErrPlanReleased) {
				t.Fatalf("replaced plan submit error = %v", err)
			}
			if _, err := resolved.Submit(context.Background()); err != nil {
				t.Fatalf("submit replacement: %v", err)
			}
		})
	}

	t.Run("preserves preparation failure", func(t *testing.T) {
		t.Parallel()

		want := NewPreparationFailure("EXAMPLE", PreparationFailureCodeSkipped, "Tracker skipped.", nil)
		plan, failure := PrepareAdapter(
			context.Background(),
			PreparationInput{Intent: PreparationIntentUpload, Tracker: "EXAMPLE"},
			nil,
			func(context.Context, PreparationInput) (PreparedOperation, error) {
				return NewPreparedOperationWithDecisionResolver(
					api.TrackerDryRunEntry{
						Tracker: "EXAMPLE",
						RequiredActions: []api.RequiredAction{{
							Kind: api.RequiredActionResolveTrackerPreparation,
						}},
					},
					func(context.Context) (api.UploadSummary, error) {
						return api.UploadSummary{}, errors.New("unresolved operation submitted")
					},
					nil,
					func(context.Context, bool) (PreparedOperation, error) {
						return PreparedOperation{}, want
					},
				), nil
			},
		)
		if failure != nil {
			t.Fatalf("prepare decision plan: %v", failure)
		}

		_, err := plan.ResolveAction(context.Background(), api.RequiredActionResolveTrackerPreparation, true)
		var got *PreparationFailure
		if !errors.Is(err, want) || !errors.As(err, &got) || got.Code() != PreparationFailureCodeSkipped {
			t.Fatalf("resolver failure = %v", err)
		}
	})
}

func TestPrepareAdapterPreservesPreparationFailure(t *testing.T) {
	t.Parallel()

	want := NewPreparationFailure("BTN", PreparationFailureCodeSkipped, "Tracker skipped.", nil)
	_, got := PrepareAdapter(
		context.Background(),
		PreparationInput{Intent: PreparationIntentUpload, Tracker: "BTN"},
		nil,
		func(context.Context, PreparationInput) (PreparedOperation, error) {
			return PreparedOperation{}, errors.Join(errors.New("wrapped"), want)
		},
	)
	if got != want {
		t.Fatalf("preparation failure = %#v, want original %#v", got, want)
	}
}

func TestPrepareAdapterKeepsPreviewIntentsNonSubmittable(t *testing.T) {
	t.Parallel()

	for _, intent := range []PreparationIntent{PreparationIntentDryRun} {
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
