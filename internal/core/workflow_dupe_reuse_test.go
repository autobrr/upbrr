// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type workflowDupeReuseService struct {
	calls     map[api.TrackerID]int
	checkedAt time.Time
}

func (s *workflowDupeReuseService) CheckProjectionSet(
	_ context.Context,
	subject api.DuplicateSubject,
	projections api.TrackerReleaseProjectionSet,
	_ api.ProjectionDupeCheckOptions,
) (api.DupeCheckSummary, api.DupeAssessmentEvidence, error) {
	var summary api.DupeCheckSummary
	evidence := make([]dupechecking.AssessmentEvidence, 0, len(projections.Projections))
	for _, projection := range projections.Projections {
		s.calls[projection.TrackerID]++
		tracker := string(projection.TrackerID)
		id := fmt.Sprintf("%s-%d", tracker, s.calls[projection.TrackerID])
		reason := "exact_identity"
		if slices.ContainsFunc(subject.MatchedTrackers, func(candidate string) bool {
			return strings.EqualFold(strings.TrimSpace(candidate), tracker)
		}) {
			reason = "in_client"
		}
		summary.Results = append(summary.Results, api.DupeCheckResult{
			Tracker:   tracker,
			Status:    "completed",
			HasDupes:  true,
			CheckedAt: s.checkedAt,
			Search:    api.DupeSearchEvidence{Complete: true, CandidateCount: 1},
			Evaluations: []api.DupeCandidateEvaluation{{
				ID:       id,
				Name:     projection.UploadReleaseName,
				Relation: api.DupeRelationExactDuplicate,
				Reasons:  []api.DupeReason{{Code: reason}},
			}},
		})
		evidence = append(evidence, dupechecking.AssessmentEvidence{
			Tracker:     tracker,
			Disposition: dupechecking.DispositionResolved,
			HasDupes:    true,
			Match: api.DupeMatch{
				MatchedID:       id,
				MatchedName:     projection.UploadReleaseName,
				MatchedReason:   reason,
				MatchedDownload: "https://tracker.invalid/download/" + id,
			},
			Raw: []api.DupeEntry{{ID: id, Download: "https://tracker.invalid/private/" + id}},
		})
	}
	return summary, dupechecking.NewAssessment(subject, config.Config{}, evidence), nil
}

type workflowDupeReuseFixture struct {
	builder     workflowDupeBuilder
	service     *workflowDupeReuseService
	subject     api.DuplicateSubject
	projections api.TrackerReleaseProjectionSet
	preflight   api.TrackerPreflightAssessment
	reuse       releaseworkflow.DuplicateAssessmentReuse
	now         time.Time
	skipRemote  bool
}

func newWorkflowDupeReuseFixture(t *testing.T) workflowDupeReuseFixture {
	t.Helper()
	checkedAt := time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC)
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	f := workflowDupeReuseFixture{
		service: &workflowDupeReuseService{calls: make(map[api.TrackerID]int), checkedAt: checkedAt},
		subject: api.DuplicateSubject{SourcePath: sourcePath},
		projections: api.TrackerReleaseProjectionSet{
			ID:         "projections-1",
			Revision:   1,
			ReleaseRef: api.ReleaseRef{SourcePath: sourcePath, Generation: 1},
		},
		preflight: api.TrackerPreflightAssessment{
			ID:        "preflight-1",
			Revision:  1,
			Status:    api.StageStatusReady,
			ExpiresAt: checkedAt.Add(time.Hour),
		},
		now: checkedAt.Add(5 * time.Minute),
	}
	for _, trackerID := range []api.TrackerID{"ALPHA", "BETA", "GAMMA", "DELTA"} {
		f.projections.Projections = append(f.projections.Projections, api.TrackerReleaseProjection{
			TrackerID:                   trackerID,
			UploadReleaseName:           "Example.Release.2026.1080p-GRP",
			DuplicateCriteria:           api.TrackerDuplicateCriteria{Name: "Example.Release.2026.1080p-GRP"},
			DuplicatePolicyID:           "example-policy/v1",
			InputFingerprint:            workflowTestFingerprint(t, "input-1"),
			ConfigFingerprint:           workflowTestFingerprint(t, "config-1"),
			CriteriaFingerprint:         workflowTestFingerprint(t, "criteria-1"),
			DuplicatePolicyFingerprint:  workflowTestFingerprint(t, "policy-1"),
			DuplicateTargetFingerprint:  workflowTestFingerprint(t, "target-1"),
			DuplicateSearchFingerprint:  workflowTestFingerprint(t, "search-1"),
			PreparedResourceFingerprint: workflowTestFingerprint(t, "resource-1"),
			Readiness:                   api.ReadinessStatusReady,
			DupeReady:                   true,
			UploadReady:                 true,
		})
		f.preflight.Results = append(f.preflight.Results, api.TrackerPreflightResult{
			TrackerID:  trackerID,
			State:      api.TrackerPreflightStateReady,
			FreshUntil: checkedAt.Add(time.Hour),
		})
	}
	f.projections.Projections[1].WaivableRuleFingerprint = workflowTestFingerprint(t, "warning-1")
	f.projections.Projections[1].RuleAuthorizationFingerprint = f.projections.Projections[1].WaivableRuleFingerprint
	f.projections.Projections[1].RequiredActions = []api.RequiredAction{{
		ID:               "action-1",
		Kind:             api.RequiredActionAuthorizeRules,
		Status:           api.RequiredActionStatusResolved,
		WorkflowRevision: 1,
		TrackerID:        "BETA",
		Prompt:           "Acknowledge this tracker warning.",
		CreatedAt:        checkedAt,
		ExpiresAt:        new(checkedAt.Add(time.Hour)),
	}}
	f.builder = workflowDupeBuilder{service: f.service}
	assessment, private, err := f.builder.Build(t.Context(), f.subject, f.projections, f.preflight, checkedAt, false)
	if err != nil {
		t.Fatalf("initial duplicate check: %v", err)
	}
	assessment.ReleaseRef = f.projections.ReleaseRef
	assessment.Results[1].FreshUntil = checkedAt.Add(20 * time.Minute)
	assessment.ExpiresAt = checkedAt.Add(18 * time.Minute)
	f.reuse = releaseworkflow.DuplicateAssessmentReuse{
		Assessment:          assessment,
		Projections:         f.projections,
		PrivateEvidence:     private,
		InvalidatedTrackers: []api.TrackerID{"ALPHA"},
	}
	// Reprojection republishes set and action identity without changing sibling semantics.
	f.projections.Projections = slices.Clone(f.projections.Projections)
	f.projections.ID = "projections-2"
	f.projections.Revision++
	for index := range f.projections.Projections {
		projection := &f.projections.Projections[index]
		projection.InputFingerprint = workflowTestFingerprint(t, "input-2")
		projection.RequiredActions = slices.Clone(projection.RequiredActions)
		for actionIndex := range projection.RequiredActions {
			action := &projection.RequiredActions[actionIndex]
			action.ID = "action-2"
			action.WorkflowRevision++
			action.CreatedAt = f.now
			action.ExpiresAt = new(f.now.Add(time.Hour))
		}
	}
	f.preflight.ID = "preflight-2"
	f.preflight.Revision++
	f.service.checkedAt = f.now
	return f
}

func (f workflowDupeReuseFixture) build(t *testing.T) (api.DupeAssessment, workflowDupePrivateEvidence) {
	t.Helper()
	assessment, private, err := f.builder.BuildWithReuse(
		t.Context(), f.subject, f.projections, f.preflight, f.now, f.skipRemote, f.reuse,
	)
	if err != nil {
		t.Fatalf("incremental duplicate check: %v", err)
	}
	evidence, ok := private.(workflowDupePrivateEvidence)
	if !ok {
		t.Fatalf("private evidence type = %T", private)
	}
	return assessment, evidence
}

func TestWorkflowDupeReusePreservesSiblingsAndCurrentPrivateAuthority(t *testing.T) {
	t.Parallel()
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprintf("revoked=%t", revoke), func(t *testing.T) {
			t.Parallel()
			f := newWorkflowDupeReuseFixture(t)
			prior, ok := f.reuse.PrivateEvidence.(workflowDupePrivateEvidence)
			if !ok {
				t.Fatal("missing initial private evidence")
			}
			bound, ok := prior.Assessment.(dupechecking.Assessment)
			if !ok {
				t.Fatal("missing concrete initial assessment")
			}
			authorized, err := bound.Authorize(f.subject, config.Config{}, []string{"GAMMA"})
			if err != nil {
				t.Fatal(err)
			}
			prior.Assessment = authorized
			f.reuse.PrivateEvidence = prior
			f.reuse.Assessment.Results[2].Decision = api.DupeDecisionIgnored
			f.projections.Projections = f.projections.Projections[:3]
			if revoke {
				f.projections.Projections[0].Readiness = api.ReadinessStatusBlocked
				f.projections.Projections[0].DupeReady = false
				f.preflight.Results[0].State = api.TrackerPreflightStateActionRequired
			}
			assessment, evidence := f.build(t)
			wantTargetCalls := 2
			if revoke {
				wantTargetCalls = 1
			}
			if want := (map[api.TrackerID]int{
				"ALPHA": wantTargetCalls,
				"BETA":  1,
				"GAMMA": 1,
				"DELTA": 1,
			}); !reflect.DeepEqual(f.service.calls, want) {
				t.Fatalf("per-tracker checks = %v, want %v", f.service.calls, want)
			}
			if len(assessment.Results) != 3 || !assessment.ExpiresAt.Equal(f.reuse.Assessment.ExpiresAt) {
				t.Fatalf("assessment tracker count or expiry changed: %#v", assessment)
			}
			for index := 1; index < len(assessment.Results); index++ {
				want := f.reuse.Assessment.Results[index]
				want.ProjectionFingerprint, err = api.CanonicalWorkflowFingerprint(f.projections.Projections[index])
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(assessment.Results[index], want) {
					t.Fatalf("retained %s decision, timestamps or evidence changed: got %#v, want %#v", want.TrackerID, assessment.Results[index], want)
				}
			}
			if !assessment.Results[0].CheckedAt.Equal(f.now) || (revoke && assessment.Results[0].Decision != api.DupeDecisionSkipped) {
				t.Fatalf("changed tracker outcome = %#v", assessment.Results[0])
			}
			merged, ok := evidence.Assessment.(dupechecking.Assessment)
			if !ok {
				t.Fatalf("merged evidence type = %T", evidence.Assessment)
			}
			for _, tracker := range []string{"BETA", "GAMMA"} {
				want, _ := authorized.Decision(tracker)
				got, exists := merged.Decision(tracker)
				if !exists || !reflect.DeepEqual(got, want) {
					t.Fatalf("retained private decision %s = %#v, want %#v", tracker, got, want)
				}
			}
			if _, exists := merged.Decision("DELTA"); exists {
				t.Fatal("removed tracker retained private authority")
			}
			target, exists := merged.Decision("ALPHA")
			if exists == revoke || (exists && target.Match.MatchedID != "ALPHA-2") {
				t.Fatalf("changed tracker private authority = %#v, exists=%t", target, exists)
			}
			wantSummary := []string{"BETA", "GAMMA"}
			if !revoke {
				wantSummary = append(wantSummary, "ALPHA")
			}
			summaryTrackers := make([]string, 0, len(evidence.Summary.Results))
			for _, result := range evidence.Summary.Results {
				summaryTrackers = append(summaryTrackers, result.Tracker)
			}
			slices.Sort(summaryTrackers)
			slices.Sort(wantSummary)
			if !slices.Equal(summaryTrackers, wantSummary) {
				t.Fatalf("private summary retained stale trackers: %v", summaryTrackers)
			}
			assertWorkflowDupeReuseVaultRoundTrip(t, evidence, assessment, f.now, revoke)
		})
	}
}

func assertWorkflowDupeReuseVaultRoundTrip(
	t *testing.T,
	evidence workflowDupePrivateEvidence,
	assessment api.DupeAssessment,
	now time.Time,
	revoked bool,
) {
	t.Helper()
	root := t.TempDir()
	codecs := workflowPrivateResourceCodecs(workflowMediaBuilder{}, workflowAudioAnalysisBuilder{})
	vault, err := releaseworkflow.NewPrivateArtifactVault(root, codecs...)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Put("owner", "workflow", "dupe:current", evidence, assessment.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	reopened, err := releaseworkflow.NewPrivateArtifactVault(root, codecs...)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := reopened.Get("owner", "workflow", "dupe:current", now)
	if err != nil {
		t.Fatal(err)
	}
	retained, ok := resource.(workflowDupePrivateEvidence)
	if !ok {
		t.Fatalf("reopened evidence type = %T", resource)
	}
	_, want, err := evidence.MarshalPrivateResource()
	if err != nil {
		t.Fatal(err)
	}
	_, got, err := retained.MarshalPrivateResource()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("private evidence, authorization, or skip policy changed across vault restart")
	}
	var upload api.UploadSubject
	applyWorkflowCrossSeeds(&upload, retained, assessment)
	wantIDs := []string{"BETA-1"}
	if !revoked {
		wantIDs = append(wantIDs, "ALPHA-2")
	}
	gotIDs := make([]string, 0, len(upload.CrossSeedTorrents))
	for _, torrent := range upload.CrossSeedTorrents {
		gotIDs = append(gotIDs, torrent.TorrentID)
		if torrent.DownloadURL != "https://tracker.invalid/download/"+torrent.TorrentID {
			t.Fatalf("cross-seed download authority = %#v", torrent)
		}
	}
	slices.Sort(gotIDs)
	slices.Sort(wantIDs)
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("cross-seed authority = %v, want %v", gotIDs, wantIDs)
	}
	public, err := json.Marshal(assessment)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "/download/") || strings.Contains(string(public), "/private/") {
		t.Fatal("public duplicate snapshot exposed private download authority")
	}
}

func TestWorkflowDupeReuseRejectsChangedOrStaleEvidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		change       func(*workflowDupeReuseFixture)
		wantCalls    int
		wantDecision api.DupeDecision
	}{
		{name: "source", change: func(f *workflowDupeReuseFixture) { f.projections.ReleaseRef.SourcePath += ".changed" }},
		{name: "generation", change: func(f *workflowDupeReuseFixture) { f.projections.ReleaseRef.Generation++ }},
		{name: "configuration", change: func(f *workflowDupeReuseFixture) { f.projections.Projections[1].ConfigFingerprint = "changed" }},
		{name: "criteria", change: func(f *workflowDupeReuseFixture) { f.projections.Projections[1].DuplicateCriteria.Name += ".changed" }},
		{name: "policy", change: func(f *workflowDupeReuseFixture) { f.projections.Projections[1].DuplicatePolicyFingerprint = "changed" }},
		{name: "warning fingerprint", change: func(f *workflowDupeReuseFixture) { f.projections.Projections[1].WaivableRuleFingerprint = "changed" }},
		{name: "action semantics", change: func(f *workflowDupeReuseFixture) {
			f.projections.Projections[1].RequiredActions[0].Prompt = "Acknowledge another rule."
		}},
		{name: "stale projection binding", change: func(f *workflowDupeReuseFixture) { f.reuse.Assessment.Results[1].ProjectionFingerprint = "stale" }},
		{name: "prepared resources", change: func(f *workflowDupeReuseFixture) {
			f.projections.Projections[1].PreparedResourceFingerprint = "changed"
		}},
		{name: "execution mode", change: func(f *workflowDupeReuseFixture) { f.projections.ExecutionMode = api.WorkflowExecutionModeDebug }},
		{name: "skip policy", change: func(f *workflowDupeReuseFixture) { f.skipRemote = true }},
		{name: "assessment expired", change: func(f *workflowDupeReuseFixture) { f.reuse.Assessment.ExpiresAt = f.now }},
		{name: "lane expired", change: func(f *workflowDupeReuseFixture) { f.reuse.Assessment.Results[1].FreshUntil = f.now }},
		{name: "private evidence missing", change: func(f *workflowDupeReuseFixture) { f.reuse.PrivateEvidence = nil }},
		{name: "private policy missing", change: func(f *workflowDupeReuseFixture) {
			f.reuse.PrivateEvidence = workflowDupePrivateEvidence{Assessment: dupechecking.EmptyAssessment()}
		}},
		{name: "new client match", change: func(f *workflowDupeReuseFixture) { f.subject.MatchedTrackers = []string{" beta "} }},
		{name: "old client match", change: func(f *workflowDupeReuseFixture) { f.reuse.Assessment.Results[1].Matches[0].Reason = "in_client" }},
		{
			name:         "projection blocked",
			wantCalls:    1,
			wantDecision: api.DupeDecisionSkipped,
			change: func(f *workflowDupeReuseFixture) {
				f.projections.Projections[1].Readiness = api.ReadinessStatusBlocked
				f.projections.Projections[1].DupeReady = false
			},
		},
		{
			name:         "preflight retryable",
			wantCalls:    1,
			wantDecision: api.DupeDecisionSkipped,
			change: func(f *workflowDupeReuseFixture) {
				f.preflight.Results[1].State = api.TrackerPreflightStateRetryable
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkflowDupeReuseFixture(t)
			tc.change(&f)
			assessment, _ := f.build(t)
			wantCalls := tc.wantCalls
			if wantCalls == 0 {
				wantCalls = 2
			}
			if f.service.calls["BETA"] != wantCalls {
				t.Fatalf("BETA checks = %d, want %d", f.service.calls["BETA"], wantCalls)
			}
			result := assessment.Results[1]
			if !result.CheckedAt.Equal(f.now) || (tc.wantDecision != "" && result.Decision != tc.wantDecision) {
				t.Fatalf("stale outcome retained: %#v", result)
			}
			if tc.name == "new client match" && (result.Decision != api.DupeDecisionAccepted || result.Matches[0].Reason != "in_client") {
				t.Fatalf("new local-client match did not block: %#v", result)
			}
		})
	}
}

func TestQuestionnaireRebindRejectsChangedDuplicateContextWithoutSearching(t *testing.T) {
	cases := []struct {
		name   string
		change func(*workflowDupeReuseFixture)
	}{
		{"answers only", func(*workflowDupeReuseFixture) {}},
		{"source", func(f *workflowDupeReuseFixture) { f.projections.ReleaseRef.SourcePath += ".changed" }},
		{"generation", func(f *workflowDupeReuseFixture) { f.projections.ReleaseRef.Generation++ }},
		{"name", func(f *workflowDupeReuseFixture) { f.projections.Projections[1].UploadReleaseName += ".changed" }},
		{"search", func(f *workflowDupeReuseFixture) { f.projections.Projections[1].DuplicateSearchFingerprint = "changed" }},
		{"target", func(f *workflowDupeReuseFixture) { f.projections.Projections[1].DuplicateTargetFingerprint = "changed" }},
		{"configuration", func(f *workflowDupeReuseFixture) { f.projections.Projections[1].ConfigFingerprint = "changed" }},
		{"policy", func(f *workflowDupeReuseFixture) { f.projections.Projections[1].DuplicatePolicyFingerprint = "changed" }},
		{"rules", func(f *workflowDupeReuseFixture) {
			f.projections.Projections[1].RuleAuthorizationFingerprint = "changed"
		}},
		{"resources", func(f *workflowDupeReuseFixture) {
			f.projections.Projections[1].PreparedResourceFingerprint = "changed"
		}},
		{"expiry", func(f *workflowDupeReuseFixture) { f.reuse.Assessment.Results[1].FreshUntil = f.now }},
		{"private evidence", func(f *workflowDupeReuseFixture) { f.reuse.PrivateEvidence = nil }},
		{"skip mode", func(f *workflowDupeReuseFixture) { f.skipRemote = true }},
		{"in client", func(f *workflowDupeReuseFixture) { f.subject.MatchedTrackers = []string{"BETA"} }},
		{"old in client", func(f *workflowDupeReuseFixture) { f.reuse.Assessment.Results[1].Matches[0].Reason = "in_client" }},
		{"readiness", func(f *workflowDupeReuseFixture) { f.projections.Projections[1].DupeReady = false }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f := newWorkflowDupeReuseFixture(t)
			f.reuse.QuestionnaireOnly = true
			f.reuse.InvalidatedTrackers = nil
			f.projections.Projections[1].QuestionnaireAnswers = map[string]string{"review": "yes"}
			f.projections.Projections[1].Questionnaire = []api.TrackerQuestionnaireRequirement{{Key: "review", Value: "yes"}}
			test.change(&f)
			assessment, _, err := f.builder.RebindQuestionnaire(t.Context(), f.subject, f.projections, f.preflight, f.now, f.skipRemote, f.reuse)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(assessment.Results) > 0; got != (test.name == "answers only") {
				t.Fatalf("rebind=%t", got)
			}
			for trackerID, calls := range f.service.calls {
				if calls != 1 {
					t.Fatalf("rebind remotely searched %s", trackerID)
				}
			}
			if test.name == "answers only" && !assessment.Results[1].FreshUntil.Equal(f.reuse.Assessment.Results[1].FreshUntil) {
				t.Fatal("freshness changed")
			}
		})
	}
}

func TestNamingQuestionnaireEditsRemainDuplicateRelevant(t *testing.T) {
	registry := trackerimpl.MustNewRegistry()
	generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       "Example Movie",
		Year:        2026,
		Resolution:  "1080p",
		Source:      "WEB",
		VideoEncode: "H.264",
		Tag:         "-GRP",
	}, nil)
	for tracker, key := range map[string]string{"FL": "name", "TVC": "name_override"} {
		t.Run(tracker, func(t *testing.T) {
			subject := api.UploadSubject{
				ReleaseName:   generated.Name,
				GeneratedName: generated.GeneratedName,
				Type:          "WEBDL",
				Source:        "WEB",
				Identity:      api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 123},
				Release: api.ReleaseInfo{
					Title:      "Example Movie",
					Year:       2026,
					Resolution: "1080p",
				},
			}
			input := trackers.PreparationInput{Tracker: tracker, Meta: subject}
			first, failure := registry.ProjectRelease(t.Context(), input, "", "", "")
			if failure != nil {
				t.Fatal(failure)
			}
			input.Meta.TrackerQuestionnaireAnswers = map[string]map[string]string{tracker: {key: "Changed Reviewed Name-GRP"}}
			second, failure := registry.ProjectRelease(t.Context(), input, "", "", "")
			if failure != nil {
				t.Fatal(failure)
			}
			a, err := reusableDupeProjectionFingerprint(first, true)
			if err != nil {
				t.Fatal(err)
			}
			b, err := reusableDupeProjectionFingerprint(second, true)
			if err != nil {
				t.Fatal(err)
			}
			if first.UploadReleaseName == second.UploadReleaseName || a == b {
				t.Fatal("naming questionnaire change retained duplicate fingerprint")
			}
		})
	}
}

func TestQuestionnaireRebindPreservesUnchangedSkippedSibling(t *testing.T) {
	f := newWorkflowDupeReuseFixture(t)
	f.reuse.QuestionnaireOnly = true
	f.reuse.InvalidatedTrackers = nil
	f.projections.Projections[0].QuestionnaireAnswers = map[string]string{"review": "yes"}
	for _, projections := range []*api.TrackerReleaseProjectionSet{&f.projections, &f.reuse.Projections} {
		projection := &projections.Projections[1]
		projection.Readiness = api.ReadinessStatusBlocked
		projection.DupeReady = false
		projection.UploadReady = false
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(f.reuse.Projections.Projections[1])
	if err != nil {
		t.Fatal(err)
	}
	f.reuse.Assessment.Results[1].ProjectionFingerprint = fingerprint
	f.reuse.Assessment.Results[1].Decision = api.DupeDecisionSkipped
	f.reuse.Assessment.Results[1].Status = api.StageStatusSkipped
	f.reuse.Assessment.Results[1].Matches = nil
	f.preflight.Results[1].State = api.TrackerPreflightStateActionRequired
	assessment, _, err := f.builder.RebindQuestionnaire(t.Context(), f.subject, f.projections, f.preflight, f.now, false, f.reuse)
	if err != nil {
		t.Fatal(err)
	}
	if len(assessment.Results) != len(f.projections.Projections) || assessment.Results[1].Decision != api.DupeDecisionSkipped {
		t.Fatal("unchanged blocked sibling forced a duplicate search")
	}
	for trackerID, calls := range f.service.calls {
		if calls != 1 {
			t.Fatalf("rebind searched %s", trackerID)
		}
	}
	// A newly blocked lane must retain its original private baseline, not republish authority.
	f.projections.Projections[0].Readiness = api.ReadinessStatusBlocked
	f.projections.Projections[0].DupeReady = false
	assessment, _, err = f.builder.RebindQuestionnaire(t.Context(), f.subject, f.projections, f.preflight, f.now, false, f.reuse)
	if err != nil || len(assessment.Results) != 0 {
		t.Fatalf("new incomplete answer rebound authority: %+v, %v", assessment, err)
	}
}
