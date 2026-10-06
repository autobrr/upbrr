// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type titleSearchTestDefinition struct {
	workflowImageHostPolicyDefinition
	result dupe.AdapterResult
	calls  *int
}

func (d *titleSearchTestDefinition) TitleSearchPolicy() trackers.TitleSearchPolicy {
	return trackers.TitleSearchPolicy{ID: "title-search-test-v1", Required: func(api.TrackerValidationSubject) bool { return true }}
}

func (d *titleSearchTestDefinition) NewDuplicateAdapter(dupe.Dependencies) dupe.Adapter {
	return titleSearchTestAdapter{AdapterFunc: dupe.AdapterFunc(func(context.Context, api.DuplicateSubject) dupe.AdapterResult {
		*d.calls++
		return d.result
	})}
}

type titleSearchTestAdapter struct{ dupe.AdapterFunc }

func (a titleSearchTestAdapter) SearchTitle(ctx context.Context, subject api.DuplicateSubject) dupe.AdapterResult {
	return a.Search(ctx, subject)
}

// The synthetic tracker uses the same three title-evidence outcomes as LST,
// without depending on unrelated site-specific eligibility requirements.
func titleSearchTestFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	outcome := trackers.LanguageUnresolved
	evidence := subject.TitleSearchEvidence
	if evidence.HasOtherTorrent(subject.Identity) {
		outcome = trackers.LanguageProhibited
	} else if evidence.Current(subject.Identity) {
		outcome = trackers.LanguageTrumpable
		if evidence.TorrentCount > 0 {
			outcome = trackers.LanguageProhibited
		}
	}
	failure := trackers.LanguageRuleFailure(subject, "subtitles_search", "missing English subtitles", outcome)
	failure.EvidenceFingerprint = evidence.ResultFingerprint
	return []api.RuleFailure{failure}
}

func TestWorkflowTitleSearchDecisionsAndChangedEvidence(t *testing.T) {
	t.Parallel()
	complete := dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID}
	for _, test := range []struct {
		name       string
		result     dupe.AdapterResult
		wantStatus api.MetadataEvidenceStatus
		waivable   bool
		inputBlock bool
	}{
		{
			name:       "zero",
			result:     dupe.ResolvedWithSearch(nil, nil, complete),
			wantStatus: api.MetadataEvidenceStatusComplete,
			waivable:   true,
		},
		{
			name:       "independent input block",
			result:     dupe.ResolvedWithSearch(nil, nil, complete),
			wantStatus: api.MetadataEvidenceStatusComplete,
			waivable:   true,
			inputBlock: true,
		},
		{"same slot", dupe.ResolvedWithSearch([]api.DupeEntry{{
			ID:   "1",
			Type: "WEBDL",
			Res:  "1080p",
		}}, nil, complete), api.MetadataEvidenceStatusComplete, false, false},
		{"another slot", dupe.ResolvedWithSearch([]api.DupeEntry{{
			ID:     "2",
			Type:   "REMUX",
			Res:    "2160p",
			Season: 1,
		}}, nil, complete), api.MetadataEvidenceStatusComplete, false, false},
		{"incomplete", dupe.ResolvedWithSearch(nil, nil, dupe.SearchEvidence{WorkScope: dupe.WorkScopeProviderID}), api.MetadataEvidenceStatusPartial, false, false},
		{"unbound", dupe.ResolvedWithSearch(nil, nil, dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeTitle}), api.MetadataEvidenceStatusPartial, false, false},
		{"wrong work", dupe.ResolvedWithSearch(nil, nil, dupe.SearchEvidence{
			Complete:       true,
			WorkScope:      dupe.WorkScopeProviderID,
			WrongWorkCount: 1,
		}), api.MetadataEvidenceStatusPartial, false, false},
		{"failure", dupe.Failed(dupe.FailureRequest, "search unavailable", errors.New("private failure")), api.MetadataEvidenceStatusUnavailable, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			definition := &titleSearchTestDefinition{
				name:   "TITLE",
				result: test.result,
				calls:  &calls,
			}
			registry := trackers.NewRegistry()
			validation := trackers.WithLanguageAssessment(trackers.NoExtraValidationPolicy("title-test-v1"), titleSearchTestFailures)
			if err := registry.RegisterDescriptor(trackers.Descriptor{
				Name:       "TITLE",
				Definition: definition,
				Validation: validation,
			}); err != nil {
				t.Fatal(err)
			}
			subject := api.UploadSubject{SourcePath: filepath.Join(t.TempDir(), "Example.mkv"), LanguageFacts: api.LanguageFacts{
				OriginalLanguages:  []string{"Japanese"},
				ProgrammeLanguages: []string{"Japanese"},
				AudioStatus:        api.MetadataEvidenceStatusComplete,
				SubtitleStatus:     api.MetadataEvidenceStatusComplete,
			}}
			subject.Identity = api.ExternalIdentity{
				SourcePath: subject.SourcePath,
				Generation: 1,
				Category:   api.CanonicalCategoryMovie,
				TMDBID:     123,
			}
			projection := api.TrackerReleaseProjection{
				TrackerID:         "TITLE",
				Readiness:         api.ReadinessStatusReady,
				DupeReady:         true,
				UploadReady:       true,
				ConfigFingerprint: "config",
			}
			failures := titleSearchTestFailures(api.NewTrackerValidationSubject(subject, "TITLE"))
			if test.inputBlock {
				failures = append(failures, trackers.NewRuleFailure("input.required", "required source input is missing", api.RuleDispositionStrict))
			}
			if err := trackers.ApplyProjectionRuleFailures(&projection, failures, api.WorkflowExecutionModeNormal, "", nil); err != nil {
				t.Fatal(err)
			}
			initial := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{projection}}
			builder := workflowPreflightBuilder{
				auth:     workflowPreflightAuthFake{},
				registry: registry,
				logger:   api.NopLogger{},
			}
			catalog := api.TrackerCatalogSnapshot{Trackers: []api.TrackerCatalogDescriptor{{TrackerID: "TITLE"}}}
			now := time.Now()
			assessment, finalized, err := builder.Build(t.Context(), subject, catalog, api.TrackerRuntimeSnapshot{}, initial, now)
			if err != nil {
				t.Fatal(err)
			}
			got := finalized[0]
			if calls != 1 || got.TitleSearchEvidence == nil || got.TitleSearchEvidence.Status != test.wantStatus || got.DupeReady {
				t.Fatalf("unacknowledged title search = %+v calls=%d", got, calls)
			}
			if (got.WaivableRuleFingerprint != "") != test.waivable {
				t.Fatalf("waiver availability = %+v", got)
			}
			before, _ := api.CanonicalWorkflowFingerprint(projection)
			if assessment.Results[0].ProjectionFingerprint != before {
				t.Fatal("preflight did not retain initial projection lineage")
			}
			if test.inputBlock && !slices.ContainsFunc(got.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
				return decision.Code == "input.required" && decision.Blocking
			}) {
				t.Fatal("title search cleared independent input blocker")
			}
			if !test.waivable || test.inputBlock {
				return
			}
			// An acknowledged empty result stays valid when rechecked unchanged.
			got.RuleAuthorizationFingerprint = got.WaivableRuleFingerprint
			initial.Projections[0] = got
			_, finalized, err = builder.Build(t.Context(), subject, catalog, api.TrackerRuntimeSnapshot{}, initial, now.Add(time.Second))
			if err != nil || !finalized[0].DupeReady || finalized[0].RuleAuthorizationFingerprint == "" {
				t.Fatalf("unchanged acknowledgement = %+v %v", finalized, err)
			}
			// A configuration change produces new evidence authority even when the
			// title remains empty, so the old acknowledgement cannot carry over.
			initial.Projections = finalized
			initial.Projections[0].ConfigFingerprint = "changed-config"
			_, finalized, err = builder.Build(t.Context(), subject, catalog, api.TrackerRuntimeSnapshot{}, initial, now.Add(2*time.Second))
			if err != nil || finalized[0].RuleAuthorizationFingerprint != "" || finalized[0].WaivableRuleFingerprint == got.WaivableRuleFingerprint {
				t.Fatalf("changed configuration retained waiver: %+v %v", finalized, err)
			}
			finalized[0].RuleAuthorizationFingerprint = finalized[0].WaivableRuleFingerprint
			// A new prepared generation also requires a new acknowledgement.
			initial.Projections = finalized
			subject.Identity.Generation++
			_, finalized, err = builder.Build(t.Context(), subject, catalog, api.TrackerRuntimeSnapshot{}, initial, now.Add(3*time.Second))
			if err != nil || finalized[0].RuleAuthorizationFingerprint != "" || finalized[0].DupeReady {
				t.Fatalf("new generation retained waiver: %+v %v", finalized, err)
			}
			finalized[0].RuleAuthorizationFingerprint = finalized[0].WaivableRuleFingerprint
			// A newly found release in any slot replaces the waiver with a strict block.
			definition.result = dupe.ResolvedWithSearch([]api.DupeEntry{{
				ID:   "new",
				Type: "REMUX",
				Res:  "2160p",
			}}, nil, complete)
			initial.Projections = finalized
			_, finalized, err = builder.Build(t.Context(), subject, catalog, api.TrackerRuntimeSnapshot{}, initial, now.Add(4*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if finalized[0].DupeReady || finalized[0].RuleAuthorizationFingerprint != "" || !slices.ContainsFunc(finalized[0].PolicyDecisions, func(d api.TrackerPolicyDecision) bool {
				return d.Disposition == api.RuleDispositionStrict && d.Blocking
			}) {
				t.Fatalf("changed evidence retained waiver: %+v", finalized[0])
			}
		})
	}
}

func TestWorkflowTitleSearchDoesNotExtendDownstreamFreshness(t *testing.T) {
	t.Parallel()
	now := time.Now()
	expires := now.Add(time.Minute)
	subject := api.UploadSubject{SourcePath: filepath.Join(t.TempDir(), "Example.mkv")}
	subject.Identity = api.ExternalIdentity{
		SourcePath: subject.SourcePath,
		Generation: 1,
		TMDBID:     123,
		Category:   api.CanonicalCategoryMovie,
	}
	title, err := api.TitleSearchIdentityFingerprint(subject.Identity)
	if err != nil {
		t.Fatal(err)
	}
	evidence := api.TrackerTitleSearchEvidence{
		Status:            api.MetadataEvidenceStatusComplete,
		TitleFingerprint:  title,
		ResultFingerprint: "result",
		ConfigFingerprint: "config",
		PolicyID:          "title-v1",
		CheckedAt:         now.Add(-time.Minute),
		FreshUntil:        expires,
	}
	projection := api.TrackerReleaseProjection{
		TrackerID:           "TITLE",
		Readiness:           api.ReadinessStatusReady,
		DupeReady:           true,
		UploadReady:         true,
		ConfigFingerprint:   "config",
		TitleSearchEvidence: &evidence,
	}
	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{projection}}
	search := &workflowDupeServiceFake{result: &api.DupeCheckResult{
		Tracker:   "TITLE",
		Status:    "completed",
		Search:    api.DupeSearchEvidence{Complete: true, Scope: "title_preflight"},
		CheckedAt: evidence.CheckedAt,
	}}
	dupes, _, err := (workflowDupeBuilder{service: search}).Build(t.Context(), api.DuplicateSubject{SourcePath: subject.SourcePath, Identity: subject.Identity}, projections, api.TrackerPreflightAssessment{
		Status:    api.StageStatusReady,
		ExpiresAt: expires,
		Results:   []api.TrackerPreflightResult{{TrackerID: "TITLE", State: api.TrackerPreflightStateReady}},
	}, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if !dupes.ExpiresAt.Equal(expires) || !dupes.Results[0].FreshUntil.Equal(expires) || !dupes.Results[0].CheckedAt.Equal(evidence.CheckedAt) {
		t.Fatalf("title search lifetime extended: %+v", dupes)
	}
	service := &workflowRetainedUploadServiceFake{}
	builder := workflowUploadPlanBuilder{resolver: workflowUploadResolverFixed{subject: subject}, trackers: service}
	plan, execution, err := builder.Build(t.Context(), projections, dupes, workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{}, api.DescriptionInstructions{}, releaseworkflow.UploadPlanBuildOptions{}, now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = execution.Release() }()
	if !plan.ExpiresAt.Equal(expires) || service.subject.TrackerTitleSearchEvidence["TITLE"].ResultFingerprint != "result" {
		t.Fatalf("upload lost evidence or extended lifetime: plan=%+v evidence=%+v", plan, service.subject.TrackerTitleSearchEvidence)
	}
	validated := api.NewTrackerValidationSubject(service.subject, "TITLE")
	if !validated.TitleSearchEvidence.Current(subject.Identity) {
		t.Fatal("late pure validation lost title evidence")
	}
}
