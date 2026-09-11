// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLiveTestTrackerPlanGuardsDirectAndResolvedSubmission(t *testing.T) {
	t.Parallel()
	for _, resolution := range []string{"direct", "confirm", "decline", "decision-confirm", "decision-decline"} {
		t.Run(resolution, func(t *testing.T) {
			policy, err := api.NewLiveTestPolicy("tracker-run", filepath.Join(t.TempDir(), "images.jsonl"), 0)
			if err != nil {
				t.Fatal(err)
			}
			remoteCalls, resolutions, releases := 0, 0, 0
			makePlan := func() TrackerPlan {
				return NewUploadPlan("ALPHA", api.TrackerDryRunEntry{}, func(context.Context) (api.UploadSummary, error) {
					remoteCalls++
					return api.UploadSummary{Uploaded: 1}, nil
				}, func() error { releases++; return nil })
			}
			plan := makePlan().withLiveTestPolicy(policy)
			if resolution != "direct" {
				plan.dryRun.RequiredActions = []api.RequiredAction{{Kind: api.RequiredActionResolveTrackerPreparation}}
				if resolution == "decline" {
					plan.state.resolve = func(context.Context) (TrackerPlan, *PreparationFailure) {
						resolutions++
						return makePlan(), nil
					}
				}
				if resolution == "decision-confirm" || resolution == "decision-decline" {
					plan.state.decide = func(_ context.Context, confirmed bool) (TrackerPlan, *PreparationFailure) {
						if confirmed != (resolution == "decision-confirm") {
							t.Errorf("decision = %t", confirmed)
						}
						resolutions++
						return makePlan(), nil
					}
				}
				plan, err = plan.ResolveAction(t.Context(), api.RequiredActionResolveTrackerPreparation,
					resolution == "confirm" || resolution == "decision-confirm")
				if err != nil {
					t.Fatal(err)
				}
			}
			// Neither this fresh context nor a nil rebinding may remove the process guard.
			plan = plan.withLiveTestPolicy(nil)
			_, err = plan.Submit(t.Context())
			if !errors.Is(err, api.ErrLiveTestMutationDisabled) || remoteCalls != 0 {
				t.Fatalf("submit = %v, remote calls = %d", err, remoteCalls)
			}
			if got := policy.Snapshot().TrackerSubmission; got != (api.LiveTestEffectCounts{MutationCallsDenied: 1}) {
				t.Fatalf("boundary receipt = %#v", got)
			}
			if err := plan.Release(); err != nil {
				t.Fatal(err)
			}
			if releases != resolutions+1 {
				t.Fatalf("cleanup calls = %d, replacements = %d", releases, resolutions)
			}
		})
	}
}

func TestLiveTestTrackerServiceBlocksBeforePreparation(t *testing.T) {
	t.Parallel()
	policy, err := api.NewLiveTestPolicy("tracker-run", filepath.Join(t.TempDir(), "images.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	started, submitted := make(chan string, 1), make(chan string, 1)
	ready := make(chan struct{})
	close(ready)
	registry := NewRegistry()
	if err := registry.Register(barrierPlanDefinition{
		name:        "ALPHA",
		started:     started,
		releasePrep: ready,
		submitted:   submitted,
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithRegistryAndImages(config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"ALPHA"}}},
		nil, nil, registry, nil, policy)
	_, err = svc.Upload(t.Context(), api.UploadSubject{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026-GRP")})
	if !errors.Is(err, api.ErrLiveTestMutationDisabled) || len(started) != 0 || len(submitted) != 0 {
		t.Fatalf("upload = %v, preparations = %d, submissions = %d", err, len(started), len(submitted))
	}
	// Retained callers may prepare upload-capable adapters directly. Their returned
	// plans must acquire the same process policy before escaping the service.
	slots := svc.prepareUploadPlans(t.Context(), api.UploadSubject{}, []string{"ALPHA"}, nil, nil, nil)
	if len(slots) != 1 || slots[0].failure != nil || len(started) != 1 {
		t.Fatalf("retained preparation = %#v, calls=%d", slots, len(started))
	}
	_, err = slots[0].plan.Submit(t.Context())
	if !errors.Is(err, api.ErrLiveTestMutationDisabled) || len(submitted) != 0 {
		t.Fatalf("retained submission = %v, calls = %d", err, len(submitted))
	}
	if err := slots[0].plan.Release(); err != nil {
		t.Fatal(err)
	}
}
