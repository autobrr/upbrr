// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

type workflowPreflightAuthFake struct {
	capabilities    []api.TrackerAuthCapability
	statuses        []api.TrackerAuthStatus
	capabilityErr   error
	validationErr   error
	capabilityCalls *int
	validateCalls   *int
	validatedIDs    *[]string
}

type workflowAudioPolicyDefinition struct {
	name       string
	policy     trackerspkg.AudioPolicy
	banned     []string
	claimCalls *int
}

type workflowImageHostPolicyDefinition struct {
	name   string
	policy *trackerspkg.ImageHostPolicy
}

type workflowAuthDefinition struct {
	name       string
	capability api.TrackerAuthCapability
}

func (d workflowAuthDefinition) Name() string { return d.name }

func (workflowAuthDefinition) DefaultBaseURL() string { return "https://auth.invalid" }

func (workflowAuthDefinition) UploadContentMode() trackerspkg.UploadContentMode {
	return trackerspkg.UploadContentModeDescription
}

func (workflowAuthDefinition) Prepare(
	context.Context,
	trackerspkg.PreparationInput,
) (trackerspkg.TrackerPlan, *trackerspkg.PreparationFailure) {
	return trackerspkg.TrackerPlan{}, nil
}

func (d workflowAuthDefinition) AuthCapability() api.TrackerAuthCapability {
	return d.capability
}

func (d workflowImageHostPolicyDefinition) Name() string { return d.name }

func (d workflowImageHostPolicyDefinition) DefaultBaseURL() string {
	return "https://image-host.invalid"
}

func (d workflowImageHostPolicyDefinition) UploadContentMode() trackerspkg.UploadContentMode {
	return trackerspkg.UploadContentModeDescription
}

func (d workflowImageHostPolicyDefinition) Prepare(
	context.Context,
	trackerspkg.PreparationInput,
) (trackerspkg.TrackerPlan, *trackerspkg.PreparationFailure) {
	return trackerspkg.TrackerPlan{}, nil
}

func (d workflowImageHostPolicyDefinition) ImageHostPolicy() *trackerspkg.ImageHostPolicy {
	if d.policy == nil {
		return nil
	}
	policy := *d.policy
	policy.AllowedHosts = slices.Clone(d.policy.AllowedHosts)
	policy.OwnedHosts = slices.Clone(d.policy.OwnedHosts)
	return &policy
}

func (d workflowAudioPolicyDefinition) Name() string { return d.name }

func (d workflowAudioPolicyDefinition) DefaultBaseURL() string { return "https://alpha.invalid" }

func (d workflowAudioPolicyDefinition) UploadContentMode() trackerspkg.UploadContentMode {
	return trackerspkg.UploadContentModeDescription
}

func (d workflowAudioPolicyDefinition) Prepare(
	context.Context,
	trackerspkg.PreparationInput,
) (trackerspkg.TrackerPlan, *trackerspkg.PreparationFailure) {
	return trackerspkg.TrackerPlan{}, nil
}

func (d workflowAudioPolicyDefinition) AudioPolicy() *trackerspkg.AudioPolicy {
	policy := d.policy
	return &policy
}

func (d workflowAudioPolicyDefinition) BannedGroups() []string {
	return append([]string(nil), d.banned...)
}

func (d workflowAudioPolicyDefinition) NewClaimChecker(config.Config, api.Logger) trackerspkg.ClaimChecker {
	return workflowClaimChecker{calls: d.claimCalls}
}

type workflowClaimChecker struct {
	calls *int
}

type workflowPreparedResourceDefinition struct{}

func (workflowPreparedResourceDefinition) Name() string { return "RESOURCE" }

func (workflowPreparedResourceDefinition) DefaultBaseURL() string { return "https://resource.invalid" }

func (workflowPreparedResourceDefinition) UploadContentMode() trackerspkg.UploadContentMode {
	return trackerspkg.UploadContentModeDescription
}

func (workflowPreparedResourceDefinition) Prepare(
	context.Context,
	trackerspkg.PreparationInput,
) (trackerspkg.TrackerPlan, *trackerspkg.PreparationFailure) {
	return trackerspkg.TrackerPlan{}, nil
}

func (workflowPreparedResourceDefinition) ValidationPolicy() trackerspkg.ValidationPolicyBinding {
	return trackerspkg.ValidationPolicyBinding{
		ID: "resource-test-v1",
		Check: func(_ context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if subject.MediaInfoTextReady {
				return nil, nil
			}
			return []api.RuleFailure{trackerspkg.NewRuleFailure(
				"required_media_resource",
				"prepared MediaInfo text is unavailable",
				api.RuleDispositionStrict,
			)}, nil
		},
	}
}

func (c workflowClaimChecker) HasClaim(context.Context, api.UploadSubject) (bool, error) {
	if c.calls != nil {
		*c.calls++
	}
	return true, nil
}

func (workflowClaimChecker) FailureReason(api.UploadSubject) string { return "Synthetic claim." }

func (f workflowPreflightAuthFake) Capabilities(context.Context) ([]api.TrackerAuthCapability, error) {
	if f.capabilityCalls != nil {
		*f.capabilityCalls++
	}
	return append([]api.TrackerAuthCapability(nil), f.capabilities...), f.capabilityErr
}

func (f workflowPreflightAuthFake) ValidateMany(_ context.Context, trackerIDs []string) ([]api.TrackerAuthStatus, error) {
	if f.validateCalls != nil {
		*f.validateCalls++
	}
	if f.validatedIDs != nil {
		*f.validatedIDs = append([]string(nil), trackerIDs...)
	}
	return append([]api.TrackerAuthStatus(nil), f.statuses...), f.validationErr
}

func TestWorkflowPreflightBuilderSuccessActionRetryExpiryAndSecretExclusion(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	catalog, runtime, projections := workflowPreflightFixtures(t)
	registry := trackerspkg.NewRegistry()
	t.Run("success", func(t *testing.T) {
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{
			capabilities: []api.TrackerAuthCapability{{TrackerID: "ALPHA", SupportsLogin: true}},
			statuses:     []api.TrackerAuthStatus{{TrackerID: "ALPHA", State: trackerauth.StateConfigured}},
		}, registry: registry}
		assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatalf("build preflight: %v", err)
		}
		if len(assessment.Results) != 2 || len(finalized) != 2 || !assessment.ExpiresAt.Equal(now.Add(workflowPreflightFreshness)) {
			t.Fatalf("successful preflight = %#v/%#v", assessment, finalized)
		}
		for index, result := range assessment.Results {
			if result.State != api.TrackerPreflightStateReady || !result.AuthReady || !finalized[index].DupeReady {
				t.Fatalf("successful tracker result = %#v/%#v", result, finalized[index])
			}
		}
	})

	t.Run("two factor blocked lane", func(t *testing.T) {
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{
			capabilities: []api.TrackerAuthCapability{{TrackerID: "ALPHA", SupportsManual2FA: true}},
			statuses: []api.TrackerAuthStatus{{
				TrackerID: "ALPHA",
				State:     trackerauth.StateLoginRequired,
				Needs2FA:  true,
				LastError: "token=secret-value",
			}},
		}, registry: registry}
		assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatalf("build action preflight: %v", err)
		}
		result := assessment.Results[0]
		if result.State != api.TrackerPreflightStateRetryable ||
			len(result.RequiredActions) != 0 ||
			len(result.Failures) != 1 ||
			result.Failures[0].Failure.Code != api.OperationFailureTrackerAuthRequired ||
			result.Failures[0].Failure.Recovery != api.OperationRecoveryAuthenticateTrackers ||
			finalized[0].Readiness != api.ReadinessStatusBlocked ||
			finalized[0].DupeReady ||
			finalized[0].UploadReady {
			t.Fatalf("2FA preflight = %#v/%#v", result, finalized[0])
		}
		payload, err := json.Marshal(struct {
			Assessment api.TrackerPreflightAssessment
			Finalized  []api.TrackerReleaseProjection
		}{assessment, finalized})
		if err != nil {
			t.Fatalf("marshal action preflight: %v", err)
		}
		if strings.Contains(string(payload), "secret-value") {
			t.Fatalf("preflight exposed secret: %s", payload)
		}
	})

	t.Run("configured static api key skips remote validation", func(t *testing.T) {
		validateCalls := 0
		staticRuntime := runtime
		staticRuntime.Trackers = []api.TrackerRuntimeEntry{{TrackerID: "ALPHA", Configured: true}}
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{
			capabilities: []api.TrackerAuthCapability{{
				TrackerID:      "ALPHA",
				AuthKind:       "api_key",
				RequiresAPIKey: true,
			}},
			validateCalls: &validateCalls,
		}, registry: registry}
		assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, staticRuntime, projections, now)
		if err != nil {
			t.Fatalf("build static auth preflight: %v", err)
		}
		if validateCalls != 0 {
			t.Fatalf("static auth remote validation calls = %d, want 0", validateCalls)
		}
		if assessment.Results[0].State != api.TrackerPreflightStateReady || !finalized[0].DupeReady {
			t.Fatalf("static auth preflight = %#v/%#v", assessment.Results[0], finalized[0])
		}
	})

	t.Run("missing static auth config blocks only configured lane", func(t *testing.T) {
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{
			capabilities: []api.TrackerAuthCapability{{
				TrackerID:      "ALPHA",
				AuthKind:       "api_key",
				RequiresAPIKey: true,
			}},
		}, registry: registry}
		assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatalf("build missing static auth preflight: %v", err)
		}
		if assessment.Results[0].State != api.TrackerPreflightStateRetryable ||
			len(assessment.Results[0].RequiredActions) != 0 ||
			len(assessment.Results[0].Failures) != 1 ||
			assessment.Results[0].Failures[0].Failure.Code != api.OperationFailureTrackerAuthRequired ||
			finalized[0].Readiness != api.ReadinessStatusBlocked ||
			finalized[0].DupeReady ||
			finalized[0].UploadReady {
			t.Fatalf("missing static auth preflight = %#v/%#v", assessment.Results[0], finalized[0])
		}
		if assessment.Results[1].State != api.TrackerPreflightStateReady ||
			!finalized[1].DupeReady ||
			!finalized[1].UploadReady {
			t.Fatalf("missing static auth changed sibling = %#v/%#v", assessment.Results[1], finalized[1])
		}
	})

	t.Run("capability failure blocks known auth lane only", func(t *testing.T) {
		authRegistry := trackerspkg.NewRegistry()
		if err := authRegistry.Register(workflowAuthDefinition{
			name: "ALPHA",
			capability: api.TrackerAuthCapability{
				TrackerID:     "ALPHA",
				AuthKind:      "cookies_login",
				SupportsLogin: true,
			},
		}); err != nil {
			t.Fatalf("register auth definition: %v", err)
		}
		validateCalls := 0
		builder := workflowPreflightBuilder{
			auth: workflowPreflightAuthFake{
				capabilityErr: errors.New("capability service unavailable"),
				validateCalls: &validateCalls,
			},
			registry: authRegistry,
		}
		assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatalf("build capability failure preflight: %v", err)
		}
		if validateCalls != 0 {
			t.Fatalf("validation calls after capability failure = %d, want 0", validateCalls)
		}
		if assessment.Results[0].State != api.TrackerPreflightStateRetryable ||
			len(assessment.Results[0].RequiredActions) != 0 ||
			len(assessment.Results[0].Failures) != 1 ||
			assessment.Results[0].Failures[0].Failure.Code != api.OperationFailureTrackerAuthRequired ||
			finalized[0].Readiness != api.ReadinessStatusBlocked {
			t.Fatalf("capability failure auth lane = %#v/%#v", assessment.Results[0], finalized[0])
		}
		if assessment.Results[1].State != api.TrackerPreflightStateReady ||
			!finalized[1].DupeReady ||
			!finalized[1].UploadReady {
			t.Fatalf("capability failure changed sibling = %#v/%#v", assessment.Results[1], finalized[1])
		}
	})

	t.Run("retryable", func(t *testing.T) {
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{
			capabilities:  []api.TrackerAuthCapability{{TrackerID: "ALPHA", SupportsLogin: true}},
			validationErr: errors.New("token=secret-value remote timeout"),
		}, registry: registry}
		assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatalf("build retryable preflight: %v", err)
		}
		if assessment.Results[0].State != api.TrackerPreflightStateRetryable ||
			len(assessment.Results[0].Failures) != 1 ||
			assessment.Results[0].Failures[0].Failure.Code != api.OperationFailureTrackerAuthRequired ||
			assessment.Results[0].Failures[0].Failure.Recovery != api.OperationRecoveryAuthenticateTrackers ||
			finalized[0].DupeReady {
			t.Fatalf("retryable preflight = %#v/%#v", assessment.Results[0], finalized[0])
		}
		payload, err := json.Marshal(assessment)
		if err != nil {
			t.Fatalf("marshal retryable preflight: %v", err)
		}
		if strings.Contains(string(payload), "secret-value") {
			t.Fatalf("retryable preflight exposed cause: %s", payload)
		}
	})

	t.Run("configured remote validation failure skips without action", func(t *testing.T) {
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{
			capabilities: []api.TrackerAuthCapability{{TrackerID: "ALPHA", SupportsLogin: true}},
			statuses: []api.TrackerAuthStatus{{
				TrackerID: "ALPHA",
				State:     trackerauth.StateConfigured,
				Message:   "remote auth test failed",
				LastError: "remote validation unavailable",
			}},
		}, registry: registry}
		assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatalf("build unavailable auth preflight: %v", err)
		}
		result := assessment.Results[0]
		if result.State != api.TrackerPreflightStateRetryable ||
			len(result.RequiredActions) != 0 ||
			len(result.Failures) != 1 ||
			result.Failures[0].Failure.Code != api.OperationFailureTrackerAuthRequired ||
			result.Failures[0].Failure.Recovery != api.OperationRecoveryAuthenticateTrackers ||
			finalized[0].Readiness != api.ReadinessStatusBlocked ||
			finalized[0].DupeReady ||
			finalized[0].UploadReady {
			t.Fatalf("unavailable auth preflight = %#v/%#v", result, finalized[0])
		}
		if assessment.Results[1].State != api.TrackerPreflightStateReady || !finalized[1].DupeReady || !finalized[1].UploadReady {
			t.Fatalf("unavailable auth changed sibling = %#v/%#v", assessment.Results[1], finalized[1])
		}
	})

	t.Run("audio bloat policy", func(t *testing.T) {
		policyRegistry := trackerspkg.NewRegistry()
		if err := policyRegistry.Register(workflowAudioPolicyDefinition{
			name: "ALPHA",
			policy: trackerspkg.AudioPolicy{
				BlockEnglishOriginalWithForeign: true,
			},
		}); err != nil {
			t.Fatalf("register audio policy: %v", err)
		}
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: policyRegistry}
		assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{
			AudioLanguages: []string{"English", "French"},
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{OriginalLanguage: "en"},
			},
		}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatalf("build audio policy preflight: %v", err)
		}
		if assessment.Results[0].State != api.TrackerPreflightStateFailed || finalized[0].UploadReady {
			t.Fatalf("audio policy preflight = %#v/%#v", assessment.Results[0], finalized[0])
		}
		if len(assessment.Results[0].Failures) != 1 || !strings.Contains(assessment.Results[0].Failures[0].Failure.Message, "French") {
			t.Fatalf("audio policy failure = %#v", assessment.Results[0].Failures)
		}
	})

	t.Run("required image host policy", func(t *testing.T) {
		policyRegistry := trackerspkg.NewRegistry()
		for _, definition := range []workflowImageHostPolicyDefinition{
			{
				name: "ALPHA",
				policy: &trackerspkg.ImageHostPolicy{
					AllowedHosts: []string{"pixhost"},
				},
			},
			{name: "BETA"},
		} {
			if err := policyRegistry.Register(definition); err != nil {
				t.Fatalf("register image-host policy: %v", err)
			}
		}
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: policyRegistry}
		assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatalf("build image-host policy preflight: %v", err)
		}
		if assessment.Results[0].State != api.TrackerPreflightStateFailed ||
			finalized[0].Readiness != api.ReadinessStatusIneligible || finalized[0].DupeReady {
			t.Fatalf("missing image-host preflight = %#v/%#v", assessment.Results[0], finalized[0])
		}
		if len(assessment.Results[0].Failures) != 1 ||
			assessment.Results[0].Failures[0].Failure.Code != api.OperationFailureMissingPrerequisite ||
			assessment.Results[0].Failures[0].Failure.Operation != api.OperationKindImageHosting {
			t.Fatalf("missing image-host failure = %#v", assessment.Results[0].Failures)
		}
		if assessment.Results[1].State != api.TrackerPreflightStateReady || !finalized[1].DupeReady {
			t.Fatalf("unrestricted sibling preflight = %#v/%#v", assessment.Results[1], finalized[1])
		}

		builder.config.ImageHosting.Host1 = "pixhost"
		assessment, finalized, err = builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatalf("build configured image-host preflight: %v", err)
		}
		if assessment.Results[0].State != api.TrackerPreflightStateReady || !finalized[0].DupeReady {
			t.Fatalf("configured image-host preflight = %#v/%#v", assessment.Results[0], finalized[0])
		}
	})

	t.Run("debug bypasses runtime policy calls and records decisions", func(t *testing.T) {
		policyRegistry := trackerspkg.NewRegistry()
		claimCalls := 0
		if err := policyRegistry.Register(workflowAudioPolicyDefinition{
			name:       "ALPHA",
			banned:     []string{"GRP"},
			claimCalls: &claimCalls,
			policy: trackerspkg.AudioPolicy{
				BlockEnglishOriginalWithForeign: true,
			},
		}); err != nil {
			t.Fatalf("register debug policies: %v", err)
		}
		debugCatalog := catalog
		debugCatalog.Trackers = append([]api.TrackerCatalogDescriptor(nil), catalog.Trackers...)
		debugCatalog.Trackers[0].Capabilities.StaticBannedGroups = true
		debugCatalog.Trackers[0].Capabilities.Claims = true
		debugProjections := projections
		debugProjections.ExecutionMode = api.WorkflowExecutionModeDebug
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: policyRegistry}
		progress := make([]api.WorkflowProgressUpdate, 0, 1)
		ctx := api.WithWorkflowProgressReporter(context.Background(), func(update api.WorkflowProgressUpdate) {
			progress = append(progress, update)
		})
		assessment, finalized, err := builder.Build(ctx, api.UploadSubject{
			Tag:            "-GRP",
			AudioLanguages: []string{"English", "French"},
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{OriginalLanguage: "en"},
			},
		}, debugCatalog, runtime, debugProjections, now)
		if err != nil {
			t.Fatalf("build debug policy preflight: %v", err)
		}
		if claimCalls != 0 {
			t.Fatalf("debug claim calls = %d, want 0", claimCalls)
		}
		if assessment.ExecutionMode != api.WorkflowExecutionModeDebug || assessment.Results[0].State != api.TrackerPreflightStateReady ||
			!finalized[0].DupeReady {
			t.Fatalf("debug preflight = %#v/%#v", assessment, finalized[0])
		}
		for _, code := range []string{"banned_group", "claim_policy", "audio_policy"} {
			if !slices.ContainsFunc(finalized[0].PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
				return decision.Code == code && decision.Decision == "bypassed" && !decision.Blocking
			}) {
				t.Errorf("debug decision %s missing from %#v", code, finalized[0].PolicyDecisions)
			}
		}
		if !slices.ContainsFunc(progress, func(update api.WorkflowProgressUpdate) bool {
			return update.ItemID == "ALPHA" && update.Status == api.StageStatusCompleted &&
				strings.Contains(update.Message, "policy_code=banned_group decision=bypassed")
		}) {
			t.Fatalf("debug preflight progress = %#v", progress)
		}
	})

	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := (workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: registry}).Build(
			ctx,
			api.UploadSubject{},
			catalog,
			runtime,
			projections,
			now,
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled preflight error = %v", err)
		}
	})
}

func TestWorkflowPreflightRejectsMissingPreparedResourceBeforeAuth(t *testing.T) {
	t.Parallel()

	registry := trackerspkg.NewRegistry()
	if err := registry.Register(workflowPreparedResourceDefinition{}); err != nil {
		t.Fatalf("register resource tracker: %v", err)
	}
	missingPath := filepath.Join(t.TempDir(), "missing-mediainfo.txt")
	subject := api.UploadSubject{MediaInfoTextPath: missingPath}
	resourceFingerprint := api.NewTrackerValidationSubject(subject, "RESOURCE").PreparedResourceFingerprint
	fingerprint := func(value string) api.WorkflowFingerprint {
		result, err := api.CanonicalWorkflowFingerprint(value)
		if err != nil {
			t.Fatalf("fingerprint %s: %v", value, err)
		}
		return result
	}
	projection := api.TrackerReleaseProjection{
		TrackerID:                   "RESOURCE",
		DisplayName:                 "RESOURCE",
		CanonicalReleaseName:        "Example.Release.2026.1080p-GRP",
		UploadReleaseName:           "Example.Release.2026.1080p-GRP",
		DuplicateCriteria:           api.TrackerDuplicateCriteria{Name: "Example.Release.2026.1080p-GRP"},
		InputFingerprint:            fingerprint("resource-input"),
		CatalogFingerprint:          fingerprint("resource-catalog"),
		ConfigFingerprint:           fingerprint("resource-config"),
		ProjectorFingerprint:        fingerprint("resource-projector"),
		CriteriaFingerprint:         fingerprint("resource-criteria"),
		PreparedResourceFingerprint: api.WorkflowFingerprint(resourceFingerprint),
		Readiness:                   api.ReadinessStatusReady,
		DupeReady:                   true,
		UploadReady:                 true,
	}
	capabilityCalls := 0
	validateCalls := 0
	builder := workflowPreflightBuilder{
		auth: workflowPreflightAuthFake{
			capabilities:    []api.TrackerAuthCapability{{TrackerID: "RESOURCE", SupportsLogin: true}},
			statuses:        []api.TrackerAuthStatus{{TrackerID: "RESOURCE", State: trackerauth.StateConfigured}},
			capabilityCalls: &capabilityCalls,
			validateCalls:   &validateCalls,
		},
		registry: registry,
	}
	assessment, finalized, err := builder.Build(
		context.Background(),
		subject,
		api.TrackerCatalogSnapshot{Trackers: []api.TrackerCatalogDescriptor{{TrackerID: "RESOURCE"}}},
		api.TrackerRuntimeSnapshot{Fingerprint: fingerprint("runtime")},
		api.TrackerReleaseProjectionSet{
			ID:          "projection-set-resource",
			Revision:    1,
			Projections: []api.TrackerReleaseProjection{projection},
		},
		time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("build preflight: %v", err)
	}
	if capabilityCalls != 0 || validateCalls != 0 {
		t.Fatalf("auth called before local-resource rejection: capabilities=%d validate=%d", capabilityCalls, validateCalls)
	}
	if len(assessment.Results) != 1 || assessment.Results[0].State == api.TrackerPreflightStateReady ||
		len(finalized) != 1 || finalized[0].DupeReady {
		t.Fatalf("missing local resource remained eligible: assessment=%#v finalized=%#v", assessment.Results, finalized)
	}
	if !slices.ContainsFunc(finalized[0].PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == "required_media_resource" && decision.Blocking
	}) {
		t.Fatalf("missing local-resource decision: %#v", finalized[0].PolicyDecisions)
	}
}

func workflowPreflightFixtures(
	t *testing.T,
) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerReleaseProjectionSet) {
	t.Helper()
	fingerprint := func(value string) api.WorkflowFingerprint {
		result, err := api.CanonicalWorkflowFingerprint(value)
		if err != nil {
			t.Fatalf("fingerprint %s: %v", value, err)
		}
		return result
	}
	catalog := api.TrackerCatalogSnapshot{Trackers: []api.TrackerCatalogDescriptor{
		{TrackerID: "ALPHA"},
		{TrackerID: "BETA"},
	}}
	runtime := api.TrackerRuntimeSnapshot{Fingerprint: fingerprint("runtime")}
	projection := func(id api.TrackerID) api.TrackerReleaseProjection {
		return api.TrackerReleaseProjection{
			TrackerID:            id,
			DisplayName:          string(id),
			CanonicalReleaseName: "Example.Release.2026.1080p-GRP",
			UploadReleaseName:    "Example.Release.2026." + string(id) + "-GRP",
			DuplicateCriteria:    api.TrackerDuplicateCriteria{Name: "Example.Release.2026." + string(id) + "-GRP"},
			InputFingerprint:     fingerprint(string(id) + "-input"),
			CatalogFingerprint:   fingerprint(string(id) + "-catalog"),
			ConfigFingerprint:    fingerprint(string(id) + "-config"),
			ProjectorFingerprint: fingerprint(string(id) + "-projector"),
			CriteriaFingerprint:  fingerprint(string(id) + "-criteria"),
			Readiness:            api.ReadinessStatusReady,
			DupeReady:            true,
			UploadReady:          true,
		}
	}
	return catalog, runtime, api.TrackerReleaseProjectionSet{
		ID:          "projection-set-1",
		Revision:    4,
		Projections: []api.TrackerReleaseProjection{projection("ALPHA"), projection("BETA")},
	}
}
