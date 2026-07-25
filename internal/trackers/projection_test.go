// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type projectionStubDefinition struct {
	stubDefinition
	prepareCalls *int
}

func (d projectionStubDefinition) Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure) {
	if d.prepareCalls != nil {
		*d.prepareCalls++
	}
	return prepareTestDefinition(ctx, input, d)
}

func (projectionStubDefinition) prepareDryRun(ctx context.Context, input PreparationInput) (api.TrackerDryRunEntry, error) {
	if err := ctx.Err(); err != nil {
		return api.TrackerDryRunEntry{}, fmt.Errorf("prepare projection preview: %w", err)
	}
	return api.TrackerDryRunEntry{
		Tracker:          input.Tracker,
		Status:           "ready",
		ReleaseName:      "Example.Show.S01E01.1080p.WEB-DL.H.265-GRP",
		DescriptionGroup: "example",
		Payload: map[string]string{
			"category_id":   "2",
			"type_id":       "episode",
			"resolution_id": "1080p",
			"source":        "WEB-DL",
			"container":     "MKV",
			"codec":         "H.265",
		},
	}, nil
}

func TestRegistryCatalogAndProjectionUseStableIdentityAndFinalPreviewSemantics(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	prepareCalls := 0
	definition := projectionStubDefinition{stubDefinition: stubDefinition{name: "EXAMPLE"}, prepareCalls: &prepareCalls}
	if err := registry.RegisterDescriptor(Descriptor{
		Name:              "EXAMPLE",
		DisplayName:       "Example Tracker",
		Aliases:           []string{"EXAMPLE-LEGACY"},
		ProjectorVersion:  "example-v2",
		Definition:        definition,
		Family:            FamilyStandalone,
		BaseURL:           "https://tracker.example.invalid",
		UploadContentMode: UploadContentModeDescription,
	}); err != nil {
		t.Fatalf("register descriptor: %v", err)
	}

	descriptors, err := registry.CatalogDescriptors()
	if err != nil {
		t.Fatalf("catalog descriptors: %v", err)
	}
	if len(descriptors) != 1 {
		t.Fatalf("catalog descriptor count = %d, want 1", len(descriptors))
	}
	descriptor := descriptors[0]
	if descriptor.TrackerID != "EXAMPLE" || descriptor.DisplayName != "Example Tracker" || descriptor.ProjectorVersion != "example-v2" {
		t.Fatalf("catalog identity = %#v", descriptor)
	}
	if resolved, ok := registry.LookupDescriptor("example-legacy"); !ok || resolved.Name != "EXAMPLE" {
		t.Fatalf("legacy alias resolved to %#v, %t", resolved, ok)
	}

	inputFingerprint := mustProjectionFingerprint(t, "projection-input")
	catalogFingerprint := mustProjectionFingerprint(t, "catalog")
	configFingerprint := mustProjectionFingerprint(t, "config")
	projection, failure := registry.ProjectRelease(context.Background(), PreparationInput{
		Tracker: "EXAMPLE-LEGACY",
		Meta: api.UploadSubject{
			ReleaseName: "Example.Show.S01E01.1080p.WEB-DL.x265-GRP",
			SeasonInt:   1,
			EpisodeInt:  1,
			Type:        "WEB-DL",
			VideoCodec:  "H.265",
			Release: api.ReleaseInfo{
				Category:   "TV",
				Resolution: "1080p",
			},
			Identity: api.ExternalIdentity{
				TMDBID: 1234567,
			},
		},
	}, inputFingerprint, catalogFingerprint, configFingerprint)
	if failure != nil {
		t.Fatalf("project release: %v", failure)
	}
	if projection.TrackerID != "EXAMPLE" || projection.DisplayName != "Example Tracker" {
		t.Fatalf("projection identity = %#v", projection)
	}
	if projection.UploadReleaseName != "Example.Show.S01E01.1080p.WEB-DL.x265-GRP" {
		t.Fatalf("upload release name = %q", projection.UploadReleaseName)
	}
	if prepareCalls != 0 {
		t.Fatalf("projection invoked tracker preparation %d times", prepareCalls)
	}
	if projection.DuplicateCriteria.Name != "Example.Show.S01E01.1080p.WEB-DL.x265-GRP" {
		t.Fatalf("duplicate query name = %q", projection.DuplicateCriteria.Name)
	}
	if projection.Taxonomy.Category.Label != "TV" || projection.Taxonomy.Codec.Label != "H.265" {
		t.Fatalf("projection taxonomy = %#v", projection.Taxonomy)
	}
	if projection.InputFingerprint != inputFingerprint || projection.CatalogFingerprint != catalogFingerprint ||
		projection.ConfigFingerprint != configFingerprint || projection.ProjectorFingerprint == "" || projection.CriteriaFingerprint == "" {
		t.Fatalf("projection fingerprints = %#v", projection)
	}
	if !projection.DupeReady || !projection.UploadReady || projection.Readiness != api.ReadinessStatusReady {
		t.Fatalf("projection readiness = %#v", projection)
	}
}

func TestPrepareAdapterRejectsPayloadSemanticsThatDifferFromReviewedProjection(t *testing.T) {
	t.Parallel()

	definition := projectionStubDefinition{stubDefinition: stubDefinition{name: "EXAMPLE"}}
	input := PreparationInput{
		Intent:  PreparationIntentDryRun,
		Tracker: "EXAMPLE",
		Meta: api.UploadSubject{
			ReleaseName: "Example.Show.S01E01.1080p.WEB-DL.x265-GRP",
		},
		Projection: &api.TrackerReleaseProjection{
			UploadReleaseName: "Different.Reviewed.Name-GRP",
		},
	}
	_, failure := definition.Prepare(context.Background(), input)
	if failure == nil || failure.Code() != "projection" {
		t.Fatalf("projection mismatch failure = %#v", failure)
	}

	input.Projection.UploadReleaseName = "Example.Show.S01E01.1080p.WEB-DL.H.265-GRP"
	plan, failure := definition.Prepare(context.Background(), input)
	if failure != nil {
		t.Fatalf("matching reviewed projection: %v", failure)
	}
	if got := plan.DryRun().ReleaseName; got != input.Projection.UploadReleaseName {
		t.Fatalf("prepared release name = %q, want %q", got, input.Projection.UploadReleaseName)
	}
}

func TestApplyProjectionRuleFailuresHonorsExplicitDebugWaivers(t *testing.T) {
	t.Parallel()

	readyProjection := func() api.TrackerReleaseProjection {
		return api.TrackerReleaseProjection{
			TrackerID:   "EXAMPLE",
			Readiness:   api.ReadinessStatusReady,
			DupeReady:   true,
			UploadReady: true,
		}
	}
	waivable := []api.RuleFailure{NewRuleFailure("runtime_gate", "waivable gate", api.RuleDispositionWaivable)}
	strict := []api.RuleFailure{NewRuleFailure("constructibility", "hard prerequisite", api.RuleDispositionStrict)}

	normal := readyProjection()
	ApplyProjectionRuleFailures(&normal, waivable, api.WorkflowExecutionModeNormal)
	if normal.Readiness != api.ReadinessStatusIneligible || normal.DupeReady || !normal.PolicyDecisions[0].Blocking {
		t.Fatalf("normal waivable outcome = %#v", normal)
	}

	debug := readyProjection()
	ApplyProjectionRuleFailures(&debug, waivable, api.WorkflowExecutionModeDebug)
	if debug.Readiness != api.ReadinessStatusReady || !debug.DupeReady || debug.PolicyDecisions[0].Decision != "bypassed" ||
		debug.PolicyDecisions[0].Blocking {
		t.Fatalf("debug waivable outcome = %#v", debug)
	}

	debugStrict := readyProjection()
	ApplyProjectionRuleFailures(&debugStrict, strict, api.WorkflowExecutionModeDebug)
	if debugStrict.Readiness != api.ReadinessStatusIneligible || debugStrict.DupeReady ||
		debugStrict.PolicyDecisions[0].Decision != "ineligible" {
		t.Fatalf("debug strict outcome = %#v", debugStrict)
	}
}

func mustProjectionFingerprint(t *testing.T, value string) api.WorkflowFingerprint {
	t.Helper()
	fingerprint, err := api.CanonicalWorkflowFingerprint(value)
	if err != nil {
		t.Fatalf("fingerprint %q: %v", value, err)
	}
	return fingerprint
}

func TestProjectionIneligibleProgressMessageIncludesStablePolicyDetails(t *testing.T) {
	t.Parallel()

	message := projectionIneligibleProgressMessage(api.TrackerReleaseProjection{
		PolicyDecisions: []api.TrackerPolicyDecision{{
			Code:     "unsupported_source",
			Decision: "ineligible",
			Blocking: true,
			Message:  "Tracker does not support the release source.",
		}},
	})
	for _, expected := range []string{"code=unsupported_source", "reason=Tracker does not support the release source."} {
		if !strings.Contains(message, expected) {
			t.Fatalf("projection progress message missing %q: %q", expected, message)
		}
	}
}
