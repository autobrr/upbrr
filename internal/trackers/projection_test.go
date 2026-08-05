// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"fmt"
	"slices"
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
			Filename:    "Example.Source.2026.1080p-GRP.mkv",
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
	if projection.NamingElementPolicyVersion != api.ReleaseNameElementPolicyVersionV1 ||
		projection.EpisodeTitleMode != api.EpisodeTitleModeInclude {
		t.Fatalf("projection element policy = version %q mode %q", projection.NamingElementPolicyVersion, projection.EpisodeTitleMode)
	}
	if prepareCalls != 0 {
		t.Fatalf("projection invoked tracker preparation %d times", prepareCalls)
	}
	if projection.DuplicateCriteria.Name != "Example.Show.S01E01.1080p.WEB-DL.x265-GRP" {
		t.Fatalf("duplicate query name = %q", projection.DuplicateCriteria.Name)
	}
	if !slices.Contains(projection.DuplicateTarget.Names, "Example.Source.2026.1080p-GRP") {
		t.Fatalf("duplicate target names = %#v", projection.DuplicateTarget.Names)
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

func TestRegistryProjectionAppliesAndFingerprintsEpisodeTitleOmitPolicy(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:             "EXAMPLE",
		DisplayName:      "Example Tracker",
		ProjectorVersion: "example-v2",
		Definition:       projectionStubDefinition{stubDefinition: stubDefinition{name: "EXAMPLE"}},
		Family:           FamilyStandalone,
		ReleaseNamePolicy: WithEpisodeTitleMode(
			CanonicalReleaseNamePolicy(),
			api.EpisodeTitleModeOmit,
		),
	}); err != nil {
		t.Fatalf("register descriptor: %v", err)
	}

	const included = "Example.Show.S01E02.Example.Episode.1080p-GRP"
	const omitted = "Example.Show.S01E02.1080p-GRP"
	fingerprint := mustProjectionFingerprint(t, "projection")
	projection, failure := registry.ProjectRelease(context.Background(), PreparationInput{
		Tracker: "EXAMPLE",
		Meta: api.UploadSubject{
			ReleaseName: included,
			GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
				IncludeEpisodeTitle: api.ReleaseNameVariant{Name: included},
				OmitEpisodeTitle:    api.ReleaseNameVariant{Name: omitted},
			},
			Release: api.ReleaseInfo{Category: "TV"},
		},
	}, fingerprint, fingerprint, fingerprint)
	if failure != nil {
		t.Fatalf("project release: %v", failure)
	}
	if projection.UploadReleaseName != omitted || projection.DuplicateCriteria.Name != omitted {
		t.Fatalf("projected names = upload %q duplicate %q", projection.UploadReleaseName, projection.DuplicateCriteria.Name)
	}
	if projection.NamingElementPolicyVersion != api.ReleaseNameElementPolicyVersionV1 ||
		projection.EpisodeTitleMode != api.EpisodeTitleModeOmit || projection.NamingFingerprint == "" {
		t.Fatalf("projection element policy = %#v", projection)
	}
}

func TestRegistryProjectionRequiresNonSceneUploadNameConfirmationWithoutBlockingDupes(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	policy := WithNonSceneReleaseNameConfirmation(
		SimpleSubjectReleaseNamePolicy("standalone/example/v2", func(subject api.UploadSubject) string {
			if subject.Scene {
				return subject.SceneName
			}
			return subject.ReleaseName
		}),
	)
	if err := registry.RegisterDescriptor(Descriptor{
		Name:              "EXAMPLE",
		DisplayName:       "Example Tracker",
		ProjectorVersion:  "example-v2",
		Definition:        projectionStubDefinition{stubDefinition: stubDefinition{name: "EXAMPLE"}},
		Family:            FamilyStandalone,
		ReleaseNamePolicy: policy,
	}); err != nil {
		t.Fatalf("register descriptor: %v", err)
	}
	fingerprint := mustProjectionFingerprint(t, "projection")
	project := func(meta api.UploadSubject, requested *string) api.TrackerReleaseProjection {
		t.Helper()
		projection, failure := registry.ProjectRelease(context.Background(), PreparationInput{
			Tracker:             "EXAMPLE",
			Meta:                meta,
			RequestedUploadName: requested,
		}, fingerprint, fingerprint, fingerprint)
		if failure != nil {
			t.Fatalf("project release: %v", failure)
		}
		return projection
	}

	const proposed = "Example.Release.2026-GRP"
	blocked := project(api.UploadSubject{ReleaseName: proposed}, nil)
	if blocked.Readiness != api.ReadinessStatusReady || !blocked.DupeReady || blocked.UploadReady ||
		len(blocked.RequiredActions) != 1 {
		t.Fatalf("upload-pending projection = %#v", blocked)
	}
	action := blocked.RequiredActions[0]
	if action.Kind != api.RequiredActionProvideTrackerInput || action.TrackerID != "EXAMPLE" ||
		!action.AllowsFreeText || len(action.Options) != 1 || action.Options[0].Value != proposed {
		t.Fatalf("confirmation action = %#v", action)
	}
	if !slices.ContainsFunc(blocked.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == releaseNameConfirmationCode &&
			decision.Decision == "confirmation_required" &&
			!decision.Blocking
	}) {
		t.Fatalf("confirmation policy decision missing: %#v", blocked.PolicyDecisions)
	}

	const reviewed = "Example.Release.2026.REVIEWED-GRP"
	confirmedName := reviewed
	confirmed := project(api.UploadSubject{ReleaseName: proposed}, &confirmedName)
	if confirmed.Readiness != api.ReadinessStatusReady || !confirmed.DupeReady || !confirmed.UploadReady ||
		len(confirmed.RequiredActions) != 0 || confirmed.UploadReleaseName != reviewed {
		t.Fatalf("confirmed projection = %#v", confirmed)
	}
	if confirmed.CriteriaFingerprint != blocked.CriteriaFingerprint ||
		confirmed.DuplicateTargetFingerprint != blocked.DuplicateTargetFingerprint ||
		confirmed.DuplicateSearchFingerprint != blocked.DuplicateSearchFingerprint ||
		confirmed.DuplicatePolicyFingerprint != blocked.DuplicatePolicyFingerprint ||
		confirmed.DuplicateCriteria.Name != blocked.DuplicateCriteria.Name ||
		!slices.Equal(confirmed.DuplicateTarget.Names, blocked.DuplicateTarget.Names) {
		t.Fatalf("reviewed upload name changed duplicate semantics: pending=%#v confirmed=%#v", blocked, confirmed)
	}
	if !slices.ContainsFunc(confirmed.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == releaseNameConfirmationCode &&
			decision.Decision == "confirmed" &&
			!decision.Blocking
	}) {
		t.Fatalf("confirmed policy decision missing: %#v", confirmed.PolicyDecisions)
	}

	const sceneName = "Example Release [SCENE].2026-GRP"
	scene := project(api.UploadSubject{
		Scene:       true,
		SceneName:   sceneName,
		ReleaseName: proposed,
	}, nil)
	if scene.Readiness != api.ReadinessStatusReady || scene.UploadReleaseName != sceneName ||
		len(scene.RequiredActions) != 0 {
		t.Fatalf("scene projection = %#v", scene)
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

func TestDuplicateTargetFingerprintIncludesHDRProvenance(t *testing.T) {
	t.Parallel()

	subject := api.UploadSubject{
		HDRFacts: api.HDRFacts{
			Formats: []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
			Origin:  api.HDREvidenceMediaInfo,
			Status:  api.HDREvidenceComplete,
		},
	}
	first, err := api.CanonicalWorkflowFingerprint(duplicateTarget(subject))
	if err != nil {
		t.Fatalf("first target fingerprint: %v", err)
	}
	subject.HDRFacts.Origin = api.HDREvidenceContentFilename
	subject.HDRFacts.Status = api.HDREvidencePartial
	second, err := api.CanonicalWorkflowFingerprint(duplicateTarget(subject))
	if err != nil {
		t.Fatalf("second target fingerprint: %v", err)
	}
	if first == second {
		t.Fatal("target fingerprint ignored HDR evidence provenance")
	}
}

func TestDuplicateTargetPrefersProviderCode(t *testing.T) {
	t.Parallel()

	target := duplicateTarget(api.UploadSubject{
		Service:         "AMZN",
		ServiceLongName: "Amazon Prime Video",
		Distributor:     "Example Distributor",
	})
	if target.Provider != "AMZN" {
		t.Fatalf("duplicate provider = %q, want AMZN", target.Provider)
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
	waivable := []api.RuleFailure{NewEvidenceRuleFailure(
		"runtime_gate",
		"waivable gate",
		api.RuleDispositionWaivable,
		api.MetadataEvidenceStatusPartial,
	)}
	strict := []api.RuleFailure{NewEvidenceRuleFailure(
		"constructibility",
		"hard prerequisite",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)}

	normal := readyProjection()
	ApplyProjectionRuleFailures(&normal, waivable, api.WorkflowExecutionModeNormal)
	if normal.Readiness != api.ReadinessStatusIneligible || normal.DupeReady || !normal.PolicyDecisions[0].Blocking {
		t.Fatalf("normal waivable outcome = %#v", normal)
	}
	if normal.PolicyDecisions[0].Disposition != api.RuleDispositionWaivable ||
		normal.PolicyDecisions[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("normal public policy evidence = %#v", normal.PolicyDecisions[0])
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
