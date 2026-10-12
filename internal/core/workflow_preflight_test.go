// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
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

type workflowPreflightImagesFake struct{ links []api.ScreenshotLinkedImage }

func (f workflowPreflightImagesFake) ReusableTrackerImageLinks(context.Context, string, api.ReleaseInfo) ([]api.ScreenshotLinkedImage, error) {
	return f.links, nil
}

type workflowClaimPolicyDefinition struct {
	name       string
	banned     []string
	claimCalls *int
	claimErr   error
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

func (d workflowClaimPolicyDefinition) Name() string { return d.name }

func (d workflowClaimPolicyDefinition) DefaultBaseURL() string { return "https://alpha.invalid" }

func (d workflowClaimPolicyDefinition) UploadContentMode() trackerspkg.UploadContentMode {
	return trackerspkg.UploadContentModeDescription
}

func (d workflowClaimPolicyDefinition) Prepare(
	context.Context,
	trackerspkg.PreparationInput,
) (trackerspkg.TrackerPlan, *trackerspkg.PreparationFailure) {
	return trackerspkg.TrackerPlan{}, nil
}

func (d workflowClaimPolicyDefinition) BannedGroups() []string {
	return append([]string(nil), d.banned...)
}

func (d workflowClaimPolicyDefinition) NewClaimChecker(config.Config, api.Logger) trackerspkg.ClaimChecker {
	return workflowClaimChecker{calls: d.claimCalls, err: d.claimErr}
}

type workflowClaimChecker struct {
	calls *int
	err   error
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
	return c.err == nil, c.err
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

	t.Run("resolved rules do not request further acknowledgement", func(t *testing.T) {
		for _, ready := range []bool{true, false} {
			acknowledged := projections
			acknowledged.Projections = append([]api.TrackerReleaseProjection(nil), projections.Projections...)
			acknowledged.Projections[0].RequiredActions = []api.RequiredAction{{
				Kind:      api.RequiredActionAuthorizeRules,
				Status:    api.RequiredActionStatusResolved,
				TrackerID: "ALPHA",
				Prompt:    "Acknowledge these tracker warnings?",
			}}
			if !ready {
				acknowledged.Projections[0].Readiness = api.ReadinessStatusBlocked
				acknowledged.Projections[0].DupeReady = false
			}
			builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: registry}
			assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, acknowledged, now)
			if err != nil {
				t.Fatalf("build acknowledged preflight: %v", err)
			}
			wantState := api.TrackerPreflightStateReady
			if !ready {
				wantState = api.TrackerPreflightStateFailed
			}
			if assessment.Results[0].State != wantState || len(finalized[0].RequiredActions) != 1 ||
				finalized[0].RequiredActions[0].Status != api.RequiredActionStatusResolved || (!ready && len(assessment.Results[0].Failures) == 0) {
				t.Fatalf("acknowledged preflight ready=%t: %#v/%#v", ready, assessment.Results[0], finalized[0])
			}
		}
	})

	t.Run("auth failures preserve resolved rule acknowledgement", func(t *testing.T) {
		for _, unavailable := range []bool{false, true} {
			acknowledged := projections
			acknowledged.Projections = append([]api.TrackerReleaseProjection(nil), projections.Projections...)
			ruleAction := api.RequiredAction{
				Kind:      api.RequiredActionAuthorizeRules,
				Status:    api.RequiredActionStatusResolved,
				TrackerID: "ALPHA",
				Prompt:    "Acknowledge these tracker warnings?",
			}
			acknowledged.Projections[0].RequiredActions = []api.RequiredAction{
				ruleAction,
				{Kind: api.RequiredActionProvideTrackerInput, Status: api.RequiredActionStatusPending},
			}
			status := api.TrackerAuthStatus{TrackerID: "ALPHA", State: trackerauth.StateLoginRequired}
			wantCode := api.OperationFailureTrackerAuthRequired
			if unavailable {
				status.State = trackerauth.StateConfigured
				status.LastError = "remote validation unavailable"
				wantCode = api.OperationFailureTrackerAuthUnavailable
			}
			builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{
				capabilities: []api.TrackerAuthCapability{{TrackerID: "ALPHA", SupportsLogin: true}},
				statuses:     []api.TrackerAuthStatus{status},
			}, registry: registry}
			assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, acknowledged, now)
			if err != nil {
				t.Fatalf("build auth-blocked acknowledged preflight: %v", err)
			}
			result := assessment.Results[0]
			if result.State != api.TrackerPreflightStateRetryable || len(result.Failures) != 1 || result.Failures[0].Failure.Code != wantCode ||
				finalized[0].DupeReady || finalized[0].UploadReady || len(finalized[0].RequiredActions) != 1 ||
				finalized[0].RequiredActions[0].Kind != ruleAction.Kind || finalized[0].RequiredActions[0].Status != ruleAction.Status ||
				finalized[0].RequiredActions[0].Prompt != ruleAction.Prompt {
				t.Fatalf("auth-blocked rule acknowledgement unavailable=%t: %#v/%#v", unavailable, result, finalized[0])
			}
		}
	})

	t.Run("upload name review does not block duplicate preflight", func(t *testing.T) {
		uploadPending := projections
		uploadPending.Projections = append([]api.TrackerReleaseProjection(nil), projections.Projections...)
		uploadPending.Projections[0].UploadReady = false
		uploadPending.Projections[0].RequiredActions = []api.RequiredAction{{
			Kind:      api.RequiredActionProvideTrackerInput,
			TrackerID: "ALPHA",
			Prompt:    "Confirm the tracker release name.",
		}}
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: registry}
		assessment, finalized, err := builder.Build(
			context.Background(),
			api.UploadSubject{},
			catalog,
			runtime,
			uploadPending,
			now,
		)
		if err != nil {
			t.Fatalf("build upload-pending preflight: %v", err)
		}
		if assessment.Results[0].State != api.TrackerPreflightStateReady ||
			len(assessment.Results[0].RequiredActions) != 1 ||
			finalized[0].Readiness != api.ReadinessStatusReady ||
			!finalized[0].DupeReady ||
			finalized[0].UploadReady ||
			len(finalized[0].RequiredActions) != 1 {
			t.Fatalf("upload-pending preflight = %#v/%#v", assessment.Results[0], finalized[0])
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
			!strings.Contains(result.Failures[0].Failure.Message, "two-factor") ||
			!strings.Contains(result.Failures[0].Failure.Message, "Tracker Auth") ||
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

	t.Run("encrypted storage blocker preserves repair guidance", func(t *testing.T) {
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{
			capabilities: []api.TrackerAuthCapability{{TrackerID: "ALPHA", SupportsLogin: true}},
			statuses: []api.TrackerAuthStatus{{
				TrackerID:        "ALPHA",
				State:            trackerauth.StateEncryptedStorageUnavailable,
				EncryptedStorage: false,
				Message:          "encrypted cookie storage unavailable; create web-auth.json before importing cookies",
			}},
		}, registry: registry}
		assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatalf("build encrypted storage preflight: %v", err)
		}
		result := assessment.Results[0]
		if result.State != api.TrackerPreflightStateRetryable ||
			len(result.RequiredActions) != 0 ||
			len(result.Failures) != 1 ||
			result.Failures[0].Failure.Code != api.OperationFailureTrackerAuthRequired ||
			result.Failures[0].Failure.Recovery != api.OperationRecoveryCompletePrerequisite ||
			!strings.Contains(result.Failures[0].Failure.Message, "Encrypted cookie storage") ||
			!strings.Contains(result.Failures[0].Failure.Message, "web-auth.json") ||
			finalized[0].Readiness != api.ReadinessStatusBlocked ||
			finalized[0].DupeReady ||
			finalized[0].UploadReady {
			t.Fatalf("encrypted storage preflight = %#v/%#v", result, finalized[0])
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
			assessment.Results[0].Failures[0].Failure.Message != authBlockedPreflightMessage ||
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
			assessment.Results[0].Failures[0].Failure.Code != api.OperationFailureTrackerAuthUnavailable ||
			assessment.Results[0].Failures[0].Failure.Recovery != api.OperationRecoveryRetry ||
			assessment.Results[0].Failures[0].Failure.Message != authUnavailablePreflightMessage ||
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
			assessment.Results[0].Failures[0].Failure.Code != api.OperationFailureTrackerAuthUnavailable ||
			assessment.Results[0].Failures[0].Failure.Recovery != api.OperationRecoveryRetry ||
			assessment.Results[0].Failures[0].Failure.Message != authUnavailablePreflightMessage ||
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
		logger := &recordingMediaLogger{}
		builder := workflowPreflightBuilder{
			auth: workflowPreflightAuthFake{
				capabilities: []api.TrackerAuthCapability{{TrackerID: "ALPHA", SupportsLogin: true}},
				statuses: []api.TrackerAuthStatus{{
					TrackerID: "ALPHA",
					State:     trackerauth.StateConfigured,
					Message:   "remote auth test failed",
					LastError: "remote validation unavailable",
				}},
			},
			registry: registry,
			logger:   logger,
		}
		assessment, finalized, err := builder.Build(context.Background(), api.UploadSubject{}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatalf("build unavailable auth preflight: %v", err)
		}
		result := assessment.Results[0]
		if result.State != api.TrackerPreflightStateRetryable ||
			len(result.RequiredActions) != 0 ||
			len(result.Failures) != 1 ||
			result.Failures[0].Failure.Code != api.OperationFailureTrackerAuthUnavailable ||
			result.Failures[0].Failure.Recovery != api.OperationRecoveryRetry ||
			result.Failures[0].Failure.Message != authUnavailablePreflightMessage ||
			finalized[0].Readiness != api.ReadinessStatusBlocked ||
			finalized[0].DupeReady ||
			finalized[0].UploadReady {
			t.Fatalf("unavailable auth preflight = %#v/%#v", result, finalized[0])
		}
		if assessment.Results[1].State != api.TrackerPreflightStateReady || !finalized[1].DupeReady || !finalized[1].UploadReady {
			t.Fatalf("unavailable auth changed sibling = %#v/%#v", assessment.Results[1], finalized[1])
		}
		if logger.countLevelContaining(
			"WARN",
			"tracker auth unavailable tracker=ALPHA state=configured reason=remote_validation_unavailable decision=retry recovery=retry",
		) != 1 {
			t.Fatalf("auth failure log = %#v", logger.entries)
		}
		if logger.countLevelContaining("WARN", "remote validation unavailable") != 0 {
			t.Fatalf("auth failure log exposed raw validation detail: %#v", logger.entries)
		}
	})

	t.Run("structured language decisions remain tracker scoped", func(t *testing.T) {
		policyRegistry := trackerspkg.NewRegistry()
		if err := policyRegistry.RegisterDescriptor(trackerspkg.Descriptor{
			Name:       "ALPHA",
			Definition: workflowImageHostPolicyDefinition{name: "ALPHA"},
			Validation: trackerspkg.WithLanguagePolicy(trackerspkg.NoExtraValidationPolicy("language-test-v1"), trackerspkg.LanguagePolicy{ExtraDubs: trackerspkg.LanguageProhibited}),
		}); err != nil {
			t.Fatal(err)
		}
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: policyRegistry}
		subject := api.UploadSubject{
			LanguageFacts: api.LanguageFacts{
				OriginalLanguages:  []string{"Japanese"},
				ProgrammeLanguages: []string{"Japanese", "English", "French"},
				ProgrammeStatus:    api.MetadataEvidenceStatusComplete,
				AudioStatus:        api.MetadataEvidenceStatusComplete,
				SubtitleStatus:     api.MetadataEvidenceStatusComplete,
			},
			ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "en"}},
		}
		for _, disc := range []string{"", "DVD"} {
			subject.DiscType = disc
			checked := projections
			checked.Projections = slices.Clone(projections.Projections)
			failures, err := trackerspkg.EvaluateTrackerValidationWithRegistry(t.Context(), policyRegistry, "ALPHA", api.NewTrackerValidationSubject(subject, "ALPHA"), api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			if err := trackerspkg.ApplyProjectionRuleFailures(&checked.Projections[0], failures, api.WorkflowExecutionModeNormal, "", nil); err != nil {
				t.Fatal(err)
			}
			assessment, finalized, err := builder.Build(t.Context(), subject, catalog, runtime, checked, now)
			if err != nil {
				t.Fatal(err)
			}
			wantReady := disc != ""
			if finalized[0].UploadReady != wantReady || (assessment.Results[0].State == api.TrackerPreflightStateReady) != wantReady {
				t.Fatalf("structured language preflight disc=%q: %+v / %+v", disc, assessment.Results[0], finalized[0])
			}
			if !finalized[1].UploadReady || assessment.Results[1].State != api.TrackerPreflightStateReady {
				t.Fatal("language block affected undeclared sibling")
			}
			if !wantReady && !slices.ContainsFunc(finalized[0].PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
				return decision.Code == "language_extra_dub" && decision.Disposition == api.RuleDispositionStrict && decision.Blocking && strings.Contains(decision.Message, "Original: Japanese") && strings.Contains(decision.Message, "French")
			}) {
				t.Fatalf("lost canonical language review: %+v", finalized[0])
			}
		}
	})

	t.Run("undeclared trackers do not inherit universal audio warnings", func(t *testing.T) {
		policyRegistry := trackerspkg.NewRegistry()
		for _, name := range []string{"ALPHA", "BETA"} {
			if err := policyRegistry.Register(workflowImageHostPolicyDefinition{name: name}); err != nil {
				t.Fatal(err)
			}
		}
		logger := &recordingMediaLogger{}
		builder := workflowPreflightBuilder{
			auth:     workflowPreflightAuthFake{},
			registry: policyRegistry,
			logger:   logger,
		}
		assessment, finalized, err := builder.Build(t.Context(), api.UploadSubject{
			AudioLanguages: []string{"Japanese", "French"},
			LanguageFacts: api.LanguageFacts{
				OriginalLanguages:  []string{"Japanese"},
				ProgrammeLanguages: []string{"Japanese", "French"},
				ProgrammeStatus:    api.MetadataEvidenceStatusComplete,
				AudioStatus:        api.MetadataEvidenceStatusComplete,
			},
			ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"}},
		}, catalog, runtime, projections, now)
		if err != nil {
			t.Fatal(err)
		}
		for index, result := range assessment.Results {
			if result.State != api.TrackerPreflightStateReady || !finalized[index].UploadReady || len(finalized[index].PolicyDecisions) != 0 {
				t.Fatalf("invented language policy: %+v", finalized[index])
			}
		}
		if logger.countLevelContaining("WARN", "audio bloat") != 0 {
			t.Fatalf("legacy universal warning remains: %#v", logger.entries)
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

		withImages := projections
		withImages.Projections = append([]api.TrackerReleaseProjection(nil), projections.Projections...)
		withImages.Projections[0].Artifacts.ScreenshotCount = 2
		subject := api.UploadSubject{SourcePath: `C:\releases\Example.Release.2026-GRP.mkv`}
		builder.images = workflowPreflightImagesFake{links: []api.ScreenshotLinkedImage{
			{
				Tracker: "AITHER",
				URL:     "https://pixhost.cc/one.png",
				Path:    "image-one.png",
				Host:    "pixhost",
			},
			{
				Tracker: "AITHER",
				URL:     "https://pixhost.cc/two.png",
				Path:    "image-two.png",
				Host:    "pixhost",
			},
		}}
		assessment, finalized, err = builder.Build(context.Background(), subject, catalog, runtime, withImages, now)
		if err != nil || assessment.Results[0].State != api.TrackerPreflightStateReady || !finalized[0].DupeReady {
			t.Fatalf("reusable source host preflight = %#v/%#v err=%v", assessment.Results[0], finalized[0], err)
		}
		builder.images = workflowPreflightImagesFake{links: []api.ScreenshotLinkedImage{{
			Tracker: "AITHER",
			URL:     "https://pixhost.cc/one.png",
			Path:    "image-one.png",
			Host:    "pixhost",
		}}}
		assessment, finalized, err = builder.Build(context.Background(), subject, catalog, runtime, withImages, now)
		if err != nil || assessment.Results[0].State != api.TrackerPreflightStateFailed || finalized[0].DupeReady {
			t.Fatalf("insufficient source images preflight = %#v/%#v err=%v", assessment.Results[0], finalized[0], err)
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
		if err := policyRegistry.RegisterDescriptor(trackerspkg.Descriptor{
			Name:         "ALPHA",
			BannedGroups: []string{"GRP"},
			Definition: workflowClaimPolicyDefinition{
				name:       "ALPHA",
				banned:     []string{"GRP"},
				claimCalls: &claimCalls,
			},
			ClaimFactory: workflowClaimPolicyDefinition{
				name:       "ALPHA",
				banned:     []string{"GRP"},
				claimCalls: &claimCalls,
			},
			Validation: trackerspkg.WithLanguagePolicy(trackerspkg.NoExtraValidationPolicy("debug-language-v1"), trackerspkg.LanguagePolicy{ExtraDubs: trackerspkg.LanguageProhibited}),
		}); err != nil {
			t.Fatalf("register debug policies: %v", err)
		}
		debugCatalog := catalog
		debugCatalog.Trackers = append([]api.TrackerCatalogDescriptor(nil), catalog.Trackers...)
		debugCatalog.Trackers[0].Capabilities.StaticBannedGroups = true
		debugCatalog.Trackers[0].Capabilities.Claims = true
		debugProjections := projections
		debugProjections.ExecutionMode = api.WorkflowExecutionModeDebug
		debugProjections.Projections = slices.Clone(projections.Projections)
		debugSubject := api.UploadSubject{Tag: "-GRP", LanguageFacts: api.LanguageFacts{
			OriginalLanguages:  []string{"English"},
			ProgrammeLanguages: []string{"English", "French"},
			ProgrammeStatus:    api.MetadataEvidenceStatusComplete,
			AudioStatus:        api.MetadataEvidenceStatusComplete,
			SubtitleStatus:     api.MetadataEvidenceStatusComplete,
		}}
		languageFailures, err := trackerspkg.EvaluateTrackerValidationWithRegistry(t.Context(), policyRegistry, "ALPHA", api.NewTrackerValidationSubject(debugSubject, "ALPHA"), api.NopLogger{})
		if err != nil {
			t.Fatal(err)
		}
		if err := trackerspkg.ApplyProjectionRuleFailures(&debugProjections.Projections[0], languageFailures, api.WorkflowExecutionModeDebug, "", nil); err != nil {
			t.Fatal(err)
		}
		builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: policyRegistry}
		progress := make([]api.WorkflowProgressUpdate, 0, 1)
		ctx := api.WithWorkflowProgressReporter(context.Background(), func(update api.WorkflowProgressUpdate) {
			progress = append(progress, update)
		})
		assessment, finalized, err := builder.Build(ctx, debugSubject, debugCatalog, runtime, debugProjections, now)
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
		for _, code := range []string{"banned_group", "claim_policy", "language_extra_dub"} {
			if !slices.ContainsFunc(finalized[0].PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
				return decision.Code == code && decision.Decision == "bypassed" && !decision.Blocking
			}) {
				t.Errorf("debug decision %s missing from %#v", code, finalized[0].PolicyDecisions)
			}
		}
		if !slices.ContainsFunc(progress, func(update api.WorkflowProgressUpdate) bool {
			return update.ItemID == "ALPHA" && update.Status == api.StageStatusCompleted &&
				strings.Contains(update.Message, "policy_code=language_extra_dub decision=bypassed")
		}) {
			t.Fatalf("debug preflight progress = %#v", progress)
		}
	})

	t.Run("claim cancellation terminates preflight", func(t *testing.T) {
		policyRegistry := trackerspkg.NewRegistry()
		if err := policyRegistry.Register(workflowClaimPolicyDefinition{name: "ALPHA", claimErr: context.Canceled}); err != nil {
			t.Fatalf("register claim policy: %v", err)
		}
		claimCatalog := catalog
		claimCatalog.Trackers = append([]api.TrackerCatalogDescriptor(nil), catalog.Trackers...)
		claimCatalog.Trackers[0].Capabilities.Claims = true
		_, _, err := (workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: policyRegistry}).Build(
			context.Background(),
			api.UploadSubject{},
			claimCatalog,
			runtime,
			projections,
			now,
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("claim cancellation error = %v", err)
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
		PolicyDecisions: []api.TrackerPolicyDecision{
			{Code: "previous_rule", Disposition: api.RuleDispositionWaivable},
			{Code: "retained_decision", Decision: "allowed"},
		},
		RequiredActions: []api.RequiredAction{
			{Kind: api.RequiredActionAuthorizeRules, TrackerID: "RESOURCE"},
			{Kind: api.RequiredActionReviewDuplicates, TrackerID: "RESOURCE"},
		},
		Failures: []api.WorkflowFailure{
			{
				Failure:   api.OperationFailure{Code: api.OperationFailureNoEligibleTrackers, Operation: api.OperationKindDuplicateCheck},
				TrackerID: "RESOURCE",
			},
			{
				Failure:   api.OperationFailure{Code: api.OperationFailureMissingPrerequisite, Operation: api.OperationKindPreparation},
				TrackerID: "RESOURCE",
			},
		},
	}
	projectionSet := api.TrackerReleaseProjectionSet{
		ID:          "projection-set-resource",
		Revision:    1,
		Projections: []api.TrackerReleaseProjection{projection},
	}
	originalProjectionSet, err := projectionSet.Clone()
	if err != nil {
		t.Fatalf("clone input projection set: %v", err)
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
		projectionSet,
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
	if !reflect.DeepEqual(projectionSet, originalProjectionSet) {
		t.Fatalf("input projection set mutated: got=%#v want=%#v", projectionSet, originalProjectionSet)
	}
}

func TestSubjectWithAvailablePreparedResourcesPreservesStatError(t *testing.T) {
	t.Parallel()

	subject := api.UploadSubject{MediaInfoTextPath: "invalid\x00path"}
	checked, changed, err := subjectWithAvailablePreparedResources(subject)
	if !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("prepared resource stat error = %v, want EINVAL", err)
	}
	if changed || checked.MediaInfoTextPath != subject.MediaInfoTextPath {
		t.Fatalf("prepared resource changed after stat error: changed=%t checked=%#v", changed, checked)
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

type workflowQuestionnaireResourceDefinition struct {
	workflowPreparedResourceDefinition
}

func (workflowQuestionnaireResourceDefinition) ValidationPolicy() trackerspkg.ValidationPolicyBinding {
	return trackerspkg.ValidationPolicyBinding{ID: "resource-questionnaire-test-v1", Check: func(_ context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
		if subject.MediaInfoTextReady && subject.QuestionnaireAnswers["override"] == "yes" {
			return nil, nil
		}
		return []api.RuleFailure{trackerspkg.NewRuleFailure("required_answer_or_media", "prepared media and reviewed answer are required", api.RuleDispositionStrict)}, nil
	}}
}

func TestWorkflowPreflightResourceRevalidationUsesExactQuestionnaireAnswers(t *testing.T) {
	registry := trackerspkg.NewRegistry()
	if err := registry.Register(workflowQuestionnaireResourceDefinition{}); err != nil {
		t.Fatal(err)
	}
	mediaInfo := filepath.Join(t.TempDir(), "mediainfo.txt")
	if err := os.WriteFile(mediaInfo, []byte("prepared media"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		answers   map[string]string
		staged    string
		wantReady bool
	}{
		{"reviewed answer overrides staged value", map[string]string{"override": "yes"}, "no", true},
		{"empty authority clears staged answer", map[string]string{}, "yes", false},
		{"legacy authority preserves staging", nil, "yes", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := api.UploadSubject{
				MediaInfoTextPath:           mediaInfo,
				SceneNFOPath:                filepath.Join(t.TempDir(), "missing-optional.nfo"),
				TrackerQuestionnaireAnswers: map[string]map[string]string{"RESOURCE": {"override": test.staged}},
			}
			resource := api.NewTrackerValidationSubject(subject, "RESOURCE").PreparedResourceFingerprint
			initial := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
				TrackerID:                   "RESOURCE",
				QuestionnaireAnswers:        test.answers,
				Readiness:                   api.ReadinessStatusReady,
				DupeReady:                   true,
				UploadReady:                 true,
				PreparedResourceFingerprint: api.WorkflowFingerprint(resource),
			}}}
			builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: registry}
			assessment, finalized, err := builder.Build(t.Context(), subject, api.TrackerCatalogSnapshot{Trackers: []api.TrackerCatalogDescriptor{{TrackerID: "RESOURCE"}}}, api.TrackerRuntimeSnapshot{}, initial, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(finalized) != 1 || finalized[0].DupeReady != test.wantReady || (assessment.Results[0].State == api.TrackerPreflightStateReady) != test.wantReady {
				t.Fatalf("reviewed answers lost after optional NFO disappeared: %+v / %+v", assessment.Results, finalized)
			}
			if finalized[0].PreparedResourceFingerprint == api.WorkflowFingerprint(resource) {
				t.Fatal("fixture did not revalidate changed resources")
			}
			if subject.TrackerQuestionnaireAnswers["RESOURCE"]["override"] != test.staged {
				t.Fatal("mutated source answers")
			}
		})
	}
}
