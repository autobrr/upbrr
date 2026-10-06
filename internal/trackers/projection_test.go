// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type projectionStubDefinition struct {
	stubDefinition
	prepareCalls *int
}

func TestSafeTrackerConfigFingerprintTracksGroupPolicies(t *testing.T) {
	t.Parallel()

	base, err := safeTrackerConfigFingerprint(config.TrackerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for name, trackerConfig := range map[string]config.TrackerConfig{
		"duplicate bypass": {DupeBypassGroups: config.CSVList{"GRP"}},
		"personal release": {PersonalReleaseGroups: config.CSVList{"GRP"}},
		"internal":         {InternalGroups: config.CSVList{"GRP"}},
	} {
		t.Run(name, func(t *testing.T) {
			fingerprint, fingerprintErr := safeTrackerConfigFingerprint(trackerConfig)
			if fingerprintErr != nil {
				t.Fatal(fingerprintErr)
			}
			if fingerprint == base {
				t.Fatalf("group policy did not change safe config fingerprint")
			}
		})
	}

	legacy, err := safeTrackerConfigFingerprint(config.TrackerConfig{Internal: true})
	if err != nil {
		t.Fatal(err)
	}
	if legacy != base {
		t.Fatalf("deprecated Internal changed safe config fingerprint")
	}
}

func TestProjectionsUseResolvedMediaInsteadOfParserFallbacks(t *testing.T) {
	for _, codec := range []string{"H.264", ""} {
		t.Run(codec, func(t *testing.T) {
			input := PreparationInput{
				Tracker: "EXAMPLE",
				Meta: api.UploadSubject{
					ReleaseName: "Example Release 2026 1080p-GRP",
					VideoCodec:  codec,
					Release:     api.ReleaseInfo{Codec: []string{"HEVC"}, Ext: "mkv"},
				},
			}
			var wantCodecs []string
			if codec != "" {
				wantCodecs = []string{codec}
			}
			pure := pureReleaseProjection(input)
			if pure.Taxonomy.Codec.Label != codec || pure.Taxonomy.Container.Label != "" ||
				!slices.Equal(pure.DuplicateCriteria.Codecs, wantCodecs) {
				t.Fatalf("pure media projection = %#v, codecs = %v", pure.Taxonomy, pure.DuplicateCriteria.Codecs)
			}
			preview, err := projectDryRunEntry(input, api.TrackerDryRunEntry{Status: "ready"})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(preview.DuplicateCriteria.Codecs, wantCodecs) {
				t.Fatalf("preview codecs = %v, want %v", preview.DuplicateCriteria.Codecs, wantCodecs)
			}
		})
	}
}

func TestHEVCRuleUsesResolvedCodec(t *testing.T) {
	for _, codec := range []string{"HEVC", "H.265", "H.264", ""} {
		t.Run(codec, func(t *testing.T) {
			subject := api.RuleSubject{VideoCodec: codec, Release: api.ReleaseInfo{Codec: []string{"HEVC"}}}
			if got, want := isHEVC(subject), codec == "HEVC" || codec == "H.265"; got != want {
				t.Fatalf("isHEVC = %t, want %t", got, want)
			}
		})
	}
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
	definition := projectionStubDefinition{name: "EXAMPLE", prepareCalls: &prepareCalls}
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

func TestNilPolicyProjectionInvalidatesPreTitleInferenceContract(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "EXAMPLE",
		Family:     FamilyUnit3D,
		Definition: projectionStubDefinition{stubDefinition: stubDefinition{name: "EXAMPLE"}},
	}); err != nil {
		t.Fatalf("register nil-policy descriptor: %v", err)
	}
	fingerprint := mustProjectionFingerprint(t, "projection")
	projection, failure := registry.ProjectRelease(context.Background(), PreparationInput{
		Tracker: "EXAMPLE",
		Meta: api.UploadSubject{
			ReleaseName: "Example.Movie.2026.1080p.WEB-DL.H264-GRP",
			Release:     api.ReleaseInfo{Category: "MOVIE", Resolution: "1080p"},
		},
	}, fingerprint, fingerprint, fingerprint)
	if failure != nil {
		t.Fatalf("project nil-policy tracker: %v", failure)
	}
	legacy, err := api.CanonicalWorkflowFingerprint(struct {
		GeneralPolicyID string
		ID              string
		Policy          *DupePolicy
	}{
		GeneralPolicyID: "general/duplicate/v4",
		ID:              projection.DuplicatePolicyID,
		Policy:          nil,
	})
	if err != nil {
		t.Fatalf("fingerprint legacy contract: %v", err)
	}
	if projection.DuplicatePolicyFingerprint == legacy || projection.DuplicatePolicyFingerprint == "" {
		t.Fatalf("nil-policy projection retained the pre-title-inference fingerprint: %q", projection.DuplicatePolicyFingerprint)
	}
}

func TestDuplicatePolicyFingerprintTracksSameGroupRestriction(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "EXAMPLE",
		Family:     FamilyStandalone,
		Definition: projectionStubDefinition{stubDefinition: stubDefinition{name: "EXAMPLE"}},
	}); err != nil {
		t.Fatal(err)
	}
	fingerprint := mustProjectionFingerprint(t, "projection")
	project := func(trackerCfg config.TrackerConfig) api.WorkflowFingerprint {
		t.Helper()
		projection, failure := registry.ProjectRelease(context.Background(), PreparationInput{
			Tracker:       "EXAMPLE",
			TrackerConfig: trackerCfg,
			Meta: api.UploadSubject{
				ReleaseName: "Example.Movie.2026.1080p.WEB-DL-NTb",
				Tag:         "-NTb",
				Release:     api.ReleaseInfo{Category: "MOVIE", Resolution: "1080p"},
			},
		}, fingerprint, fingerprint, fingerprint)
		if failure != nil {
			t.Fatalf("project release: %v", failure)
		}
		return projection.DuplicatePolicyFingerprint
	}

	baseline := project(config.TrackerConfig{})
	dupeBypass := project(config.TrackerConfig{DupeBypassGroups: config.CSVList{"NTb"}})
	internal := project(config.TrackerConfig{InternalGroups: config.CSVList{"NTb"}})
	personal := project(config.TrackerConfig{PersonalReleaseGroups: config.CSVList{"NTb"}})
	if baseline == dupeBypass {
		t.Fatal("same-group duplicate restriction did not change duplicate policy fingerprint")
	}
	if dupeBypass != internal {
		t.Fatalf("equivalent duplicate restrictions produced different fingerprints: %q and %q", dupeBypass, internal)
	}
	if baseline != personal {
		t.Fatalf("personal-only policy changed duplicate policy fingerprint: %q and %q", baseline, personal)
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
	policy := WithNonSceneReleaseNameConfirmation(StructuredReleaseNamePolicy("test/confirmation-rebuild/v1", StructuredNamePolicy{
		Opaque:    OpaqueNameRebuild,
		Authority: []NameAuthority{{Role: api.NameRoleEdition, Aspect: NamePresence}},
		Mandatory: func(editor *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
			return editor.Omit(api.NameRoleEdition)
		},
	}))
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
	project := func(meta api.UploadSubject, requested *string, confirmed api.WorkflowFingerprint) api.TrackerReleaseProjection {
		t.Helper()
		projection, failure := registry.ProjectRelease(context.Background(), PreparationInput{
			Tracker:                  "EXAMPLE",
			Meta:                     meta,
			RequestedUploadName:      requested,
			ConfirmedNameFingerprint: confirmed,
		}, fingerprint, fingerprint, fingerprint)
		if failure != nil {
			t.Fatalf("project release: %v", failure)
		}
		return projection
	}

	subject := structuredSubject()
	blocked := project(subject, nil, "")
	if blocked.Readiness != api.ReadinessStatusReady || !blocked.DupeReady || blocked.UploadReady ||
		len(blocked.RequiredActions) != 1 {
		t.Fatalf("upload-pending projection = %#v", blocked)
	}
	action := blocked.RequiredActions[0]
	if action.Kind != api.RequiredActionProvideTrackerInput || action.TrackerID != "EXAMPLE" ||
		!action.AllowsFreeText || len(action.Options) != 1 || action.Options[0].Value != blocked.UploadReleaseName {
		t.Fatalf("confirmation action = %#v", action)
	}
	if !slices.ContainsFunc(blocked.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == releaseNameConfirmationCode &&
			decision.Decision == "confirmation_required" &&
			!decision.Blocking
	}) {
		t.Fatalf("confirmation policy decision missing: %#v", blocked.PolicyDecisions)
	}

	opaque := "Opaque Uncut Name-GRP"
	rebuilt := project(subject, &opaque, blocked.NamingFingerprint)
	if rebuilt.Readiness != api.ReadinessStatusReady || !rebuilt.DupeReady || rebuilt.UploadReady ||
		len(rebuilt.RequiredActions) != 1 || rebuilt.UploadReleaseName != blocked.UploadReleaseName ||
		rebuilt.NamingFingerprint == blocked.NamingFingerprint {
		t.Fatalf("opaque rebuilt projection bypassed confirmation = %#v", rebuilt)
	}

	exact := blocked.UploadReleaseName
	confirmed := project(subject, &exact, "")
	if confirmed.Readiness != api.ReadinessStatusReady || !confirmed.DupeReady || !confirmed.UploadReady ||
		len(confirmed.RequiredActions) != 0 || confirmed.UploadReleaseName != exact {
		t.Fatalf("exact rebuilt name was not accepted = %#v", confirmed)
	}
	if !slices.ContainsFunc(confirmed.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == releaseNameConfirmationCode &&
			decision.Decision == "confirmed" &&
			!decision.Blocking
	}) {
		t.Fatalf("confirmed policy decision missing: %#v", confirmed.PolicyDecisions)
	}

	marked := project(subject, nil, blocked.NamingFingerprint)
	if marked.Readiness != api.ReadinessStatusReady || !marked.DupeReady || !marked.UploadReady ||
		len(marked.RequiredActions) != 0 || marked.UploadReleaseName != blocked.UploadReleaseName {
		t.Fatalf("server-confirmed generated projection = %#v", marked)
	}
}

func TestPrepareAdapterRejectsPayloadSemanticsThatDifferFromReviewedProjection(t *testing.T) {
	t.Parallel()

	definition := projectionStubDefinition{name: "EXAMPLE"}
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

func TestRegistryProjectionExplainsUnsatisfiedNamingRule(t *testing.T) {
	registry := NewRegistry()
	policy := StructuredReleaseNamePolicy("test/required-edition/v1", StructuredNamePolicy{
		Authority: []NameAuthority{{Role: api.NameRoleEdition, Aspect: NamePresence}},
		Mandatory: func(editor *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
			return editor.Omit(api.NameRoleEdition)
		},
	})
	if err := registry.RegisterDescriptor(Descriptor{
		Name:              "EXAMPLE",
		Definition:        projectionStubDefinition{stubDefinition: stubDefinition{name: "EXAMPLE"}},
		Family:            FamilyStandalone,
		ReleaseNamePolicy: policy,
	}); err != nil {
		t.Fatal(err)
	}
	fingerprint := mustProjectionFingerprint(t, "blocked-naming")
	projection, failure := registry.ProjectRelease(t.Context(), PreparationInput{
		Tracker:             "EXAMPLE",
		Meta:                structuredSubject(),
		RequestedUploadName: new("Opaque Uncut Name-GRP"),
	}, fingerprint, fingerprint, fingerprint)
	if failure == nil || failure.Code() != "name_rule_unsatisfied" || projection.DupeReady || projection.UploadReady || projection.Readiness != api.ReadinessStatusBlocked {
		t.Fatalf("unsatisfied naming outcome = %+v, %v", projection, failure)
	}
	if !strings.Contains(failure.Message(), "clear the name override and reprepare") {
		t.Fatalf("missing actionable naming detail: %q", failure.Message())
	}
	if !slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == "name_rule_unsatisfied" && decision.Blocking && decision.Message == failure.Message()
	}) {
		t.Fatalf("projection lost naming recovery detail: %+v", projection.PolicyDecisions)
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

func TestApplyProjectionRuleFailuresRequiresExactNormalAuthorization(t *testing.T) {
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
		"waivable gate.",
		api.RuleDispositionWaivable,
		api.MetadataEvidenceStatusPartial,
	)}
	strict := []api.RuleFailure{NewEvidenceRuleFailure(
		"constructibility",
		"hard prerequisite",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)}
	waivableFingerprint, err := WaivableRuleFailureFingerprint("EXAMPLE", waivable)
	if err != nil {
		t.Fatalf("fingerprint waivable rules: %v", err)
	}
	apply := func(projection *api.TrackerReleaseProjection, failures []api.RuleFailure, mode api.WorkflowExecutionMode, authorization api.WorkflowFingerprint, logger api.Logger) {
		t.Helper()
		if err := ApplyProjectionRuleFailures(projection, failures, mode, authorization, logger); err != nil {
			t.Fatalf("apply projection rule failures: %v", err)
		}
	}

	normal := readyProjection()
	apply(&normal, waivable, api.WorkflowExecutionModeNormal, "", nil)
	if normal.Readiness != api.ReadinessStatusBlocked || normal.DupeReady || normal.UploadReady ||
		len(normal.RequiredActions) != 1 || normal.RequiredActions[0].Kind != api.RequiredActionAuthorizeRules ||
		normal.PolicyDecisions[0].Decision != "authorization_required" || !normal.PolicyDecisions[0].Blocking {
		t.Fatalf("normal waivable outcome = %#v", normal)
	}
	if normal.PolicyDecisions[0].Disposition != api.RuleDispositionWaivable ||
		normal.PolicyDecisions[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("normal public policy evidence = %#v", normal.PolicyDecisions[0])
	}
	if normal.WaivableRuleFingerprint != waivableFingerprint || normal.RuleAuthorizationFingerprint != "" {
		t.Fatalf("normal rule authority = %#v", normal)
	}
	if prompt := normal.RequiredActions[0].Prompt; prompt != "EXAMPLE rule warning: runtime_gate: waivable gate. Acknowledge these tracker warnings?" {
		t.Fatalf("normal rule prompt = %q", prompt)
	}

	authorized := readyProjection()
	apply(&authorized, waivable, api.WorkflowExecutionModeNormal, waivableFingerprint, nil)
	if authorized.Readiness != api.ReadinessStatusReady || !authorized.DupeReady || !authorized.UploadReady ||
		len(authorized.RequiredActions) != 1 || authorized.RequiredActions[0].Status != api.RequiredActionStatusResolved ||
		authorized.RequiredActions[0].Kind != api.RequiredActionAuthorizeRules || authorized.PolicyDecisions[0].Decision != "authorized" ||
		authorized.PolicyDecisions[0].Blocking || authorized.RuleAuthorizationFingerprint != waivableFingerprint {
		t.Fatalf("authorized waivable outcome = %#v", authorized)
	}

	strictWithAuthorization := readyProjection()
	combinedFailures := append(append([]api.RuleFailure(nil), waivable...), strict...)
	apply(
		&strictWithAuthorization,
		combinedFailures,
		api.WorkflowExecutionModeNormal,
		waivableFingerprint,
		nil,
	)
	if strictWithAuthorization.Readiness != api.ReadinessStatusIneligible || strictWithAuthorization.DupeReady ||
		strictWithAuthorization.UploadReady || len(strictWithAuthorization.RequiredActions) != 0 ||
		!strictWithAuthorization.PolicyDecisions[1].Blocking ||
		strictWithAuthorization.PolicyDecisions[1].Disposition != api.RuleDispositionStrict {
		t.Fatalf("strict failure with waivable authorization = %#v", strictWithAuthorization)
	}

	changed := readyProjection()
	changedFailures := append(append([]api.RuleFailure(nil), waivable...), NewRuleFailure(
		"second_gate",
		"another waiver is required",
		api.RuleDispositionWaivable,
	))
	apply(&changed, changedFailures, api.WorkflowExecutionModeNormal, waivableFingerprint, nil)
	if changed.Readiness != api.ReadinessStatusBlocked || changed.RuleAuthorizationFingerprint != "" || len(changed.RequiredActions) != 1 {
		t.Fatalf("changed waivable outcome = %#v", changed)
	}

	debug := readyProjection()
	apply(&debug, waivable, api.WorkflowExecutionModeDebug, "", nil)
	if debug.Readiness != api.ReadinessStatusReady || !debug.DupeReady || debug.PolicyDecisions[0].Decision != "bypassed" ||
		debug.PolicyDecisions[0].Blocking || len(debug.RequiredActions) != 0 {
		t.Fatalf("debug waivable outcome = %#v", debug)
	}

	debugStrict := readyProjection()
	logger := &warningLogger{}
	apply(&debugStrict, strict, api.WorkflowExecutionModeDebug, "", logger)
	if debugStrict.Readiness != api.ReadinessStatusIneligible || debugStrict.DupeReady ||
		debugStrict.PolicyDecisions[0].Decision != "ineligible" || len(debugStrict.RequiredActions) != 0 {
		t.Fatalf("debug strict outcome = %#v", debugStrict)
	}
	if len(logger.warnings) != 1 || logger.warnings[0] != "trackers: projection validation blocked tracker=EXAMPLE rule=constructibility decision=ineligible" {
		t.Fatalf("blocking validation warnings = %#v", logger.warnings)
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

func TestDuplicateTargetRetainsSeparatedEditionParts(t *testing.T) {
	t.Parallel()
	legacy := duplicateTarget(api.UploadSubject{Edition: "Extended Collector's Open Matte"})
	structured := duplicateTarget(api.UploadSubject{
		Cut:          "Extended",
		Edition:      "Collector's",
		Presentation: "Open Matte",
	})
	if structured.Edition != legacy.Edition || structured.Edition == "" {
		t.Fatalf("structured target edition = %q, legacy = %q", structured.Edition, legacy.Edition)
	}
}

type reviewQuestionnaireDefinition struct{ stubDefinition }

func (d reviewQuestionnaireDefinition) ProjectionQuestionnaire(input PreparationInput) *api.TrackerQuestionnaire {
	return &api.TrackerQuestionnaire{Tracker: d.name, Fields: []api.TrackerQuestionnaireField{{
		Key:      "review",
		Label:    "Review",
		Kind:     "select",
		Options:  []string{"yes", "no"},
		Required: true,
		Value:    input.Meta.TrackerQuestionnaireAnswers[d.name]["review"],
	}}}
}

func TestProjectionQuestionnaireIsTrackerScopedAndRetained(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(reviewQuestionnaireDefinition{stubDefinition{name: "ONE"}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(stubDefinition{name: "TWO"}); err != nil {
		t.Fatal(err)
	}
	for _, answer := range []string{"", "yes"} {
		input := PreparationInput{Tracker: "ONE", Meta: api.UploadSubject{ReleaseName: "Example.Movie.2026.1080p-GRP", TrackerQuestionnaireAnswers: map[string]map[string]string{"ONE": {"review": answer}}}}
		projection, failure := registry.ProjectRelease(t.Context(), input, "input", "catalog", "config")
		if failure != nil {
			t.Fatal(failure)
		}
		if len(projection.Questionnaire) != 1 || projection.Questionnaire[0].Value != answer {
			t.Fatalf("questionnaire=%#v", projection.Questionnaire)
		}
		if answer == "" {
			if len(projection.RequiredActions) != 1 || projection.RequiredActions[0].TrackerID != "ONE" || projection.RequiredActions[0].Kind != api.RequiredActionAnswerQuestionnaire || projection.UploadReady {
				t.Fatalf("missing review=%#v", projection)
			}
		} else {
			if len(projection.RequiredActions) != 0 || !projection.UploadReady {
				t.Fatalf("reviewed=%#v", projection)
			}
			payloadInput := PreparationInput{Meta: api.UploadSubject{}, Projection: &projection}
			applied := applyReviewedProjection(payloadInput)
			if applied.Meta.TrackerQuestionnaireAnswers["ONE"]["review"] != "yes" {
				t.Fatal("reviewed answer lost before payload")
			}
			if payloadInput.Meta.TrackerQuestionnaireAnswers != nil {
				t.Fatal("mutated caller answers")
			}
		}
		input.Tracker = "TWO"
		other, failure := registry.ProjectRelease(t.Context(), input, "input", "catalog", "config")
		if failure != nil || !other.UploadReady || len(other.RequiredActions) > 0 {
			t.Fatalf("other tracker affected: %#v %v", other, failure)
		}
	}
}

type blockedQuestionnaireDefinition struct{ reviewQuestionnaireDefinition }

func (blockedQuestionnaireDefinition) ValidationPolicy() ValidationPolicyBinding {
	return ValidationPolicyBinding{ID: "questionnaire-strict-test-v1", Check: func(context.Context, api.TrackerValidationSubject, api.Logger) ([]api.RuleFailure, error) {
		return []api.RuleFailure{NewRuleFailure("unrelated_strict", "unsupported source", api.RuleDispositionStrict)}, nil
	}}
}

func TestProjectionQuestionnaireRemainsActionableWithStrictFailure(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(blockedQuestionnaireDefinition{reviewQuestionnaireDefinition{stubDefinition{name: "ONE"}}}); err != nil {
		t.Fatal(err)
	}
	projection, failure := registry.ProjectRelease(t.Context(), PreparationInput{
		Tracker: "ONE", Meta: api.UploadSubject{ReleaseName: "Example.Movie.2026.1080p-GRP"},
	}, "input", "catalog", "config")
	if failure != nil {
		t.Fatal(failure)
	}
	if !slices.ContainsFunc(projection.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionAnswerQuestionnaire && action.TrackerID == "ONE"
	}) {
		t.Fatalf("missing questionnaire action with strict failure: %+v", projection)
	}
	if projection.Readiness != api.ReadinessStatusIneligible || projection.DupeReady || projection.UploadReady || len(projection.Failures) == 0 {
		t.Fatalf("questionnaire waived strict failure: %+v", projection)
	}
}

func TestPreparedProjectionPreservesAndValidatesEditionFeatures(t *testing.T) {
	t.Parallel()
	catalogue := []api.TrackerEditionFeature{
		{
			Label:    "With Commentary",
			Category: "Feature",
			Selected: true,
			Evidence: "Effective commentary",
		},
		{Label: "Remastered", Category: "Edition"},
	}
	input := PreparationInput{Tracker: "EXAMPLE", Meta: api.UploadSubject{ReleaseName: "Example.Release.2026-GRP"}}
	preview := api.TrackerDryRunEntry{
		Status:          "ready",
		EditionFeatures: catalogue,
		Payload:         map[string]string{"remaster_title": "With Commentary"},
	}
	projection, err := projectDryRunEntry(input, preview)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(projection.EditionFeatures, catalogue) || preview.Payload["remaster_title"] != "With Commentary" {
		t.Fatal("projection did not preserve review options separately from wire payload")
	}
	input.Projection = &projection
	if err := validatePreparedProjection(input, preview); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"selected", "label", "category", "evidence", "missing"} {
		t.Run(change, func(t *testing.T) {
			changed := preview
			changed.EditionFeatures = slices.Clone(catalogue)
			switch change {
			case "selected":
				changed.EditionFeatures[0].Selected = false
			case "label":
				changed.EditionFeatures[0].Label = "Other"
			case "category":
				changed.EditionFeatures[0].Category = "Edition"
			case "evidence":
				changed.EditionFeatures[0].Evidence = "Changed evidence"
			case "missing":
				changed.EditionFeatures = nil
			}
			if err := validatePreparedProjection(input, changed); err == nil {
				t.Fatal("accepted review catalogue drift")
			}
		})
	}
	preview.EditionFeatures[0].Selected = false
	if !projection.EditionFeatures[0].Selected {
		t.Fatal("projection aliases preview catalogue")
	}
	input.Projection.EditionFeatures = nil
	if err := validatePreparedProjection(input, preview); err != nil {
		t.Fatalf("legacy projection without catalogue failed: %v", err)
	}
}
