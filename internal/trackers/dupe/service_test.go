// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type bannedGroupDefinition struct{}

func (bannedGroupDefinition) Name() string { return "DP" }

func (bannedGroupDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }

func (bannedGroupDefinition) UploadContentMode() trackerspkg.UploadContentMode {
	return trackerspkg.UploadContentModeNone
}

func (bannedGroupDefinition) BannedGroups() []string { return []string{"SUBSPLEASE"} }

func (bannedGroupDefinition) Prepare(
	context.Context,
	trackerspkg.PreparationInput,
) (trackerspkg.TrackerPlan, *trackerspkg.PreparationFailure) {
	return trackerspkg.TrackerPlan{}, nil
}

func testService(adapters map[string]Adapter) *Service {
	return &Service{
		cfg:                    adaptersConfig(adapters),
		logger:                 api.NopLogger{},
		adapters:               adapters,
		cancelWarningThreshold: time.Second,
	}
}

type recordingDupeLogger struct {
	debug []string
	info  []string
	trace []string
}

func (l *recordingDupeLogger) Tracef(format string, args ...any) {
	l.trace = append(l.trace, fmt.Sprintf(format, args...))
}

func (l *recordingDupeLogger) Debugf(format string, args ...any) {
	l.debug = append(l.debug, fmt.Sprintf(format, args...))
}

func (l *recordingDupeLogger) Infof(format string, args ...any) {
	l.info = append(l.info, fmt.Sprintf(format, args...))
}

func (*recordingDupeLogger) Warnf(string, ...any) {}

func (*recordingDupeLogger) Errorf(string, ...any) {}

func TestProjectAdapterResultDebugLogsEveryCandidateEvaluation(t *testing.T) {
	logger := &recordingDupeLogger{}
	service := testService(nil)
	service.logger = logger
	candidateHDR := api.HDRFacts{
		Origin: api.HDREvidenceUnknown,
		Status: api.HDREvidenceMissing,
	}
	result, _ := service.projectAdapterResult("BHD", api.DuplicateSubject{
		HDRFacts: api.HDRFacts{
			Formats: []api.HDRFormat{api.HDRFormatHDR10},
			Origin:  api.HDREvidenceMediaInfo,
			Status:  api.HDREvidenceComplete,
		},
	}, ResolvedWithSearch([]api.DupeEntry{
		{
			ID:       "candidate-1",
			Name:     "Example.Release.2026.1080p.WEB-DL-GRP",
			Type:     "WEB-DL",
			Res:      "1080p",
			HDR:      candidateHDR,
			Provider: "EXAMPLE",
		},
		{
			ID:   "candidate-2",
			Name: "Example.Release.2026.1080p.WEB-DL-OTHER",
			Type: "WEB-DL",
			Res:  "1080p",
			HDR:  candidateHDR,
		},
	}, nil, SearchEvidence{Complete: true, Pages: 1}), time.Now().UTC())

	if len(result.Evaluations) != 2 {
		t.Fatalf("candidate evaluations = %d, want 2", len(result.Evaluations))
	}
	var candidateLogs []string
	for _, entry := range logger.debug {
		if strings.Contains(entry, "dupechecking: candidate ") {
			candidateLogs = append(candidateLogs, entry)
		}
	}
	if len(candidateLogs) != 2 {
		t.Fatalf("candidate debug logs = %d, want 2", len(candidateLogs))
	}
	if !strings.Contains(candidateLogs[0], `tracker=BHD candidate_id="candidate-1" relation=same_slot`) ||
		!strings.Contains(candidateLogs[0], `winning_rule=bhd/duplicate/v1/same_slot`) ||
		!strings.Contains(candidateLogs[0], `kind=web_dl class=web source_family=web`) ||
		!strings.Contains(candidateLogs[0], `name="Example.Release.2026.1080p.WEB-DL-GRP"`) ||
		!strings.Contains(candidateLogs[0], `facts="WEB-DL · EXAMPLE · 1080p"`) ||
		!strings.Contains(candidateLogs[0], `hdr_status=missing`) ||
		!strings.Contains(candidateLogs[0], `hdr="unknown" hdr_origin=unknown`) ||
		!strings.Contains(candidateLogs[0], `reasons="same_tracker_slot — Candidate occupies the same tracker slot and requires review."`) {
		t.Fatalf("first candidate debug log missing evaluation evidence: %q", candidateLogs[0])
	}
	if !strings.Contains(candidateLogs[1], `candidate_id="candidate-2"`) ||
		!strings.Contains(candidateLogs[1], `name="Example.Release.2026.1080p.WEB-DL-OTHER"`) {
		t.Fatalf("second candidate debug log missing candidate identity: %q", candidateLogs[1])
	}
	if len(logger.trace) != 0 {
		t.Fatalf("candidate evaluations unexpectedly logged at trace: %#v", logger.trace)
	}
}

func TestProjectAdapterResultIncompleteEmptySearchIsNotCandidateEvidence(t *testing.T) {
	t.Parallel()

	logger := &recordingDupeLogger{}
	service := testService(nil)
	service.logger = logger
	result, entry := service.projectAdapterResult(
		"AR",
		api.DuplicateSubject{},
		ResolvedWithSearch(nil, nil, SearchEvidence{
			Complete: false,
			Pages:    1,
			Warnings: []string{"adapter search completeness is not evidenced"},
		}),
		time.Now().UTC(),
	)
	if result.HasDupes {
		t.Fatalf("incomplete empty search reported dupes: %#v", result)
	}
	if entry.verdict != VerdictBlocked || entry.match.MatchedReason != "incomplete_search" {
		t.Fatalf("incomplete empty assessment = %#v", entry)
	}
	if got := dupeProgressMessage(result); got != "search incomplete; review required" {
		t.Fatalf("incomplete search progress = %q", got)
	}
	if len(logger.info) != 1 ||
		!strings.Contains(logger.info[0], "candidates=0 complete=false candidate_action=false review_required=true") {
		t.Fatalf("incomplete search outcome log = %#v", logger.info)
	}
}

func TestCheckTrackerLogsLocalClientOutcome(t *testing.T) {
	t.Parallel()

	logger := &recordingDupeLogger{}
	service := testService(nil)
	service.logger = logger
	result, _, canceled := service.checkTracker(
		context.Background(),
		api.DuplicateSubject{
			ReleaseName:     "Example.Release.2026.1080p.WEB-DL-GRP",
			MatchedTrackers: []string{"HDB"},
		},
		"HDB",
		CheckOptions{},
	)
	if canceled || !result.HasDupes || len(logger.info) != 1 {
		t.Fatalf("local-client result=%#v canceled=%t logs=%#v", result, canceled, logger.info)
	}
	if !strings.Contains(
		logger.info[0],
		"tracker=HDB state=completed source=local_client candidates=1 complete=true candidate_action=true review_required=true",
	) {
		t.Fatalf("local-client log = %q", logger.info[0])
	}
}

func TestCandidateLogIncludesOnlyDecisiveDeduplicatedEvidence(t *testing.T) {
	t.Parallel()

	logger := &recordingDupeLogger{}
	service := testService(nil)
	service.logger = logger
	service.logCandidateEvaluation("OTW", CandidateEvaluation{
		Candidate:   TrackerCandidate{ID: "candidate-1", Name: "Example.Release.2026.1080p.WEB-DL-GRP"},
		Relation:    api.DupeRelationCoexists,
		WinningRule: GeneralPolicyID + "/media_class",
		Findings: []RuleFinding{
			{
				RuleID:      GeneralPolicyID + "/media_class",
				Status:      RuleFindingMatched,
				Compared:    []string{"media_class", "resolution", "media_class"},
				Priority:    findingPriorityGeneral,
				Specificity: 1,
			},
			{
				RuleID:   "otw/duplicate/v2/slot",
				Status:   RuleFindingDisproved,
				Compared: []string{"pack=equal", "pack=equal"},
				Missing:  []string{"target_season", "target_season"},
				Priority: findingPriorityTrackerMatched,
			},
			{
				RuleID:   "policy_evidence_unavailable",
				Status:   RuleFindingMatched,
				Priority: findingPriorityFallback,
			},
		},
	})
	if len(logger.debug) != 1 {
		t.Fatalf("candidate logs = %#v", logger.debug)
	}
	logLine := logger.debug[0]
	for _, value := range []string{
		`compared="media_class | resolution"`,
		`missing=""`,
		`matched="general/duplicate/v2/media_class"`,
	} {
		if !strings.Contains(logLine, value) {
			t.Fatalf("candidate log missing %q: %q", value, logLine)
		}
	}
	for _, value := range []string{"pack=equal", "target_season", "policy_evidence_unavailable"} {
		if strings.Contains(logLine, value) {
			t.Fatalf("candidate log includes non-decisive %q: %q", value, logLine)
		}
	}
}

func TestSetFindingLogIncludesBoundedOccupancyEvidence(t *testing.T) {
	t.Parallel()

	logger := &recordingDupeLogger{}
	service := testService(nil)
	service.logger = logger
	service.logSetFinding("PTP", SetFinding{
		RuleID:                           "ptp/duplicate/v2/1080p_x264_capacity",
		Status:                           RuleFindingMatched,
		Relation:                         api.DupeRelationCoexists,
		ReasonCode:                       "set_capacity_available",
		ExistingOccupancy:                1,
		Capacity:                         2,
		MinimumSizeSeparationPercent:     20,
		ObservedMinimumSeparationPercent: 20,
		SeparationKnown:                  true,
		CandidateIDs:                     []string{"candidate-1"},
		FactSummaries: []string{
			"candidate[id=candidate-1,size=80,kind=disc_encode,resolution=1080p,codec=h264,hdr=sdr,edition=]",
		},
	})
	if len(logger.debug) != 1 {
		t.Fatalf("set logs = %#v", logger.debug)
	}
	for _, value := range []string{
		"relation=coexists",
		"occupancy=1 capacity=2",
		"minimum_size_separation=20.00 observed_size_separation=20.00",
		`candidates="candidate-1"`,
		"kind=disc_encode",
	} {
		if !strings.Contains(logger.debug[0], value) {
			t.Fatalf("set log missing %q: %q", value, logger.debug[0])
		}
	}
}

func TestCandidateLogIncludesStructuredComparisonOperands(t *testing.T) {
	t.Parallel()

	logger := &recordingDupeLogger{}
	service := testService(nil)
	service.logger = logger
	service.logCandidateEvaluation("SP", CandidateEvaluation{
		Candidate: TrackerCandidate{
			ID:            "candidate-1",
			Name:          "Example.Show.S01E12.1080p.WEB-DL-GRP",
			Type:          "WEB-DL",
			CanonicalType: "WEBDL",
			Resolution:    "1080p",
		},
		Facts: normalizedFacts{
			Type: Fact{
				Value:        "WEBDL",
				Status:       FactComplete,
				Origin:       FactOriginTrackerAPI,
				SourceFields: []string{"canonicalType"},
			},
			Resolution: completeFact("1080p", FactOriginTrackerAPI, "resolution"),
			MediaKind:  mediaKindWEBDL,
			MediaClass: mediaClassWEB,
			HDR: api.HDRFacts{
				Origin: api.HDREvidenceUnknown,
				Status: api.HDREvidenceMissing,
			},
		},
		Relation:    api.DupeRelationInsufficientEvidence,
		WinningRule: "sp/duplicate/v2/slot",
		Findings: []RuleFinding{{
			RuleID:      "sp/duplicate/v2/slot",
			Status:      RuleFindingIndeterminate,
			Missing:     []string{"candidate_hdr"},
			Priority:    findingPrioritySlotMissing,
			Specificity: 3,
			comparisons: []factComparison{{
				Dimension: trackerspkg.DupeDimensionType,
				Target: Fact{
					Value:  "WEBDL",
					Status: FactComplete,
					Origin: FactOriginTargetMedia,
				},
				Candidate: Fact{
					Value:  "WEBDL",
					Status: FactComplete,
					Origin: FactOriginTrackerAPI,
				},
				Result: DimensionEqual,
			}},
		}},
	})

	if len(logger.debug) != 1 {
		t.Fatalf("candidate logs = %#v", logger.debug)
	}
	logLine := logger.debug[0]
	for _, value := range []string{
		`raw_type="WEB-DL" comparison_type="WEBDL"`,
		`compared="type[target={WEBDL},target_status=complete,target_origin=target_media,candidate={WEBDL},candidate_status=complete,candidate_origin=tracker_api,result=equal]"`,
	} {
		if !strings.Contains(logLine, value) {
			t.Fatalf("candidate log missing %q: %q", value, logLine)
		}
	}
}

func TestCandidateLogShowsDifferentTargetAndCandidateOperands(t *testing.T) {
	t.Parallel()

	result := Evaluate(
		api.TrackerDuplicateTarget{
			Source:      "BluRay",
			Resolution:  "1080p",
			VideoEncode: "x264",
			Group:       "GRP",
		},
		[]TrackerCandidate{NormalizeCandidate(api.DupeEntry{
			Name: "Example.Release.2026.1080p.WEB-DL.H.264-GRP",
		}, "AR")},
		trackerspkg.DupePolicy{
			ID:                             "ar/duplicate/v2",
			EvidenceID:                     "ar-uploading-guidelines",
			SlotDifferencesOverrideGeneral: true,
			SlotDimensions: []trackerspkg.DupeDimension{
				trackerspkg.DupeDimensionSource,
				trackerspkg.DupeDimensionResolution,
				trackerspkg.DupeDimensionCodec,
				trackerspkg.DupeDimensionGroup,
			},
		},
		SearchEvidence{Complete: true},
	).Candidates[0]
	logger := &recordingDupeLogger{}
	service := testService(nil)
	service.logger = logger
	service.logCandidateEvaluation("AR", result)
	if len(logger.debug) != 1 || !strings.Contains(
		logger.debug[0],
		"source[target={bluray},target_status=complete,target_origin=target_media,candidate={web},candidate_status=partial,"+
			"candidate_origin=tracker_title,result=different]",
	) {
		t.Fatalf("different comparison operands missing: %#v", logger.debug)
	}
}

func TestCheckWithAssessmentReportsDebugBannedGroupAsBypassed(t *testing.T) {
	registry := trackerspkg.NewRegistry()
	if err := registry.Register(bannedGroupDefinition{}); err != nil {
		t.Fatalf("register banned-group definition: %v", err)
	}
	tempDir := t.TempDir()
	var adapterCalled atomic.Bool
	service := testService(map[string]Adapter{
		"DP": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			adapterCalled.Store(true)
			return Resolved(nil, nil)
		}),
	})
	service.banned = trackerspkg.NewBannedGroupCheckerWithRegistry(filepath.Join(tempDir, "upbrr.db"), registry)
	var (
		progressMu sync.Mutex
		progress   []api.DupeProgressUpdate
	)
	ctx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		progressMu.Lock()
		progress = append(progress, update)
		progressMu.Unlock()
	})
	summary, assessment, err := service.CheckWithAssessment(ctx, api.DuplicateSubject{
		SourcePath: filepath.Join(tempDir, "Example.Release.2026.1080p-GRP.mkv"),
		Tag:        "SubsPlease",
	}, []string{"DP"}, CheckOptions{BypassBannedGroups: true})
	if err != nil {
		t.Fatalf("check bypassed banned group: %v", err)
	}
	if adapterCalled.Load() {
		t.Fatal("bypassed banned-group result invoked duplicate adapter")
	}
	if len(summary.Results) != 1 || summary.Results[0].Status != "bypassed" ||
		summary.Results[0].SkipCode != NotRunBannedGroup ||
		!strings.Contains(summary.Results[0].SkipReason, "debug mode bypassed policy") {
		t.Fatalf("bypassed duplicate result = %#v", summary.Results)
	}
	decision, ok := assessment.Decision("DP")
	if !ok || decision.Verdict != VerdictWaived || decision.Authorization != AuthorizationWaiver {
		t.Fatalf("bypassed assessment decision = %#v found=%t", decision, ok)
	}
	progressMu.Lock()
	defer progressMu.Unlock()
	if len(progress) == 0 || progress[len(progress)-1].Status != "bypassed" ||
		!strings.Contains(progress[len(progress)-1].Message, "debug mode bypassed policy") {
		t.Fatalf("bypassed duplicate progress = %#v", progress)
	}
}

func adaptersConfig(adapters map[string]Adapter) config.Config {
	trackers := make(map[string]config.TrackerConfig, len(adapters))
	for name := range adapters {
		trackers[name] = config.TrackerConfig{}
	}
	return config.Config{Trackers: config.TrackersConfig{Trackers: trackers}}
}

func TestAdapterResultDefensiveCopies(t *testing.T) {
	entries := []api.DupeEntry{{Name: "Example.Release.2026.1080p-GRP", Files: []string{"one.mkv"}}}
	notes := []string{"display only"}
	result := Resolved(entries, notes)
	entries[0].Name = "mutated"
	entries[0].Files[0] = "mutated"
	notes[0] = "mutated"

	gotEntries := result.Entries()
	gotNotes := result.Notes()
	if gotEntries[0].Name != "Example.Release.2026.1080p-GRP" || gotEntries[0].Files[0] != "one.mkv" || gotNotes[0] != "display only" {
		t.Fatalf("result changed through caller mutation: %#v %#v", gotEntries, gotNotes)
	}
	gotEntries[0].Files[0] = "again"
	if result.Entries()[0].Files[0] != "one.mkv" {
		t.Fatal("result accessor exposed mutable state")
	}
}

func TestCheckProjectionSetUsesExactCriteriaAndRetainsBlockedClientMatch(t *testing.T) {
	t.Parallel()

	var received api.DuplicateSubject
	var blockedAdapterCalls atomic.Int32
	service := testService(map[string]Adapter{
		"A": AdapterFunc(func(_ context.Context, subject api.DuplicateSubject) AdapterResult {
			received = subject
			return Resolved(nil, nil)
		}),
		"B": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			blockedAdapterCalls.Add(1)
			return Resolved(nil, nil)
		}),
	})
	projectionFingerprint := mustDupeFingerprint(t, "projection")
	criteriaFingerprint := mustDupeFingerprint(t, "criteria")
	inputFingerprint := mustDupeFingerprint(t, "input")
	catalogFingerprint := mustDupeFingerprint(t, "catalog")
	configFingerprint := mustDupeFingerprint(t, "config")
	policyFingerprint := mustDupeFingerprint(t, "policy")
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	projectionSet := api.TrackerReleaseProjectionSet{
		ID:         "projection-set-1",
		WorkflowID: "workflow-1",
		Revision:   1,
		Release: api.ReleaseSnapshotRef{
			ID:       "release-1",
			Revision: 1,
		},
		ReleaseRef: api.ReleaseRef{
			SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
			Generation: 1,
		},
		Catalog: api.TrackerCatalogSnapshotRef{
			ID:       "catalog-1",
			Revision: 1,
		},
		Runtime: api.TrackerRuntimeSnapshotRef{
			ID:       "runtime-1",
			Revision: 1,
		},
		Selection: api.TrackerSelectionRef{
			ID:       "selection-1",
			Revision: 1,
		},
		InputFingerprint:  inputFingerprint,
		PolicyFingerprint: policyFingerprint,
		Projections: []api.TrackerReleaseProjection{{
			TrackerID:            "A",
			DisplayName:          "Tracker A",
			CanonicalReleaseName: "Example.Release.2026.1080p-GRP",
			UploadReleaseName:    "Example.Release.2026.TrackerA-GRP",
			AdditionalNames: []api.TrackerReleaseName{{
				Role:  api.TrackerReleaseNameRoleSearch,
				Value: "Example Release 2026",
			}},
			DuplicateCriteria: api.TrackerDuplicateCriteria{
				Name:   "Example Release 2026",
				Season: 2,
			},
			InputFingerprint:     inputFingerprint,
			CatalogFingerprint:   catalogFingerprint,
			ConfigFingerprint:    configFingerprint,
			ProjectorFingerprint: projectionFingerprint,
			CriteriaFingerprint:  criteriaFingerprint,
			Readiness:            api.ReadinessStatusReady,
			DupeReady:            true,
			UploadReady:          true,
		}},
		Status:    api.StageStatusReady,
		CreatedAt: now,
	}
	blockedProjection := projectionSet.Projections[0]
	blockedProjection.TrackerID = "B"
	blockedProjection.DisplayName = "Tracker B"
	blockedProjection.CanonicalReleaseName = "Example.Release.2026.BLOCKED-GRP"
	blockedProjection.UploadReleaseName = ""
	blockedProjection.DuplicateCriteria = api.TrackerDuplicateCriteria{}
	blockedProjection.ProjectorFingerprint = mustDupeFingerprint(t, "blocked-projection")
	blockedProjection.CriteriaFingerprint = mustDupeFingerprint(t, "blocked-criteria")
	blockedProjection.Readiness = api.ReadinessStatusIneligible
	blockedProjection.DupeReady = false
	blockedProjection.UploadReady = false
	blockedProjection.PolicyDecisions = []api.TrackerPolicyDecision{{
		Code:        "unsupported_source",
		Decision:    "ineligible",
		Blocking:    true,
		Disposition: api.RuleDispositionStrict,
	}}
	projectionSet.Projections = append(projectionSet.Projections, blockedProjection)
	summary, _, err := service.CheckProjectionSet(context.Background(), api.DuplicateSubject{
		SourcePath:      projectionSet.ReleaseRef.SourcePath,
		ReleaseName:     projectionSet.Projections[0].CanonicalReleaseName,
		MatchedTrackers: []string{" b "},
	}, projectionSet, api.ProjectionDupeCheckOptions{})
	if err != nil {
		t.Fatalf("check projection set: %v", err)
	}
	if received.Projection == nil || received.ReleaseName != "Example Release 2026" || received.SeasonInt != 2 {
		t.Fatalf("adapter subject = %#v", received)
	}
	if len(summary.Results) != 2 {
		t.Fatalf("duplicate results = %#v", summary.Results)
	}
	result := summary.Results[0]
	if result.CanonicalReleaseName != projectionSet.Projections[0].CanonicalReleaseName ||
		result.UploadReleaseName != projectionSet.Projections[0].UploadReleaseName ||
		result.ProjectionFingerprint != projectionFingerprint || result.CriteriaFingerprint != criteriaFingerprint ||
		result.ProjectionStatus != api.ReadinessStatusReady {
		t.Fatalf("projected duplicate result = %#v", result)
	}
	clientResult := summary.Results[1]
	if blockedAdapterCalls.Load() != 0 || clientResult.Tracker != "B" || !clientResult.HasDupes ||
		len(clientResult.Evaluations) != 1 || clientResult.Evaluations[0].Name != blockedProjection.CanonicalReleaseName ||
		len(clientResult.Evaluations[0].Reasons) != 1 || clientResult.Evaluations[0].Reasons[0].Code != "in_client" ||
		clientResult.Search.Scope != "local_client" {
		t.Fatalf("blocked local-client result = %#v adapter_calls=%d", clientResult, blockedAdapterCalls.Load())
	}
}

func mustDupeFingerprint(t *testing.T, value string) api.WorkflowFingerprint {
	t.Helper()
	fingerprint, err := api.CanonicalWorkflowFingerprint(value)
	if err != nil {
		t.Fatalf("fingerprint %q: %v", value, err)
	}
	return fingerprint
}

func TestCheckReturnsResolvedOrderAndActualCompletionProgress(t *testing.T) {
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	service := testService(map[string]Adapter{
		"A": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			close(startedA)
			<-releaseA
			return Resolved(nil, nil)
		}),
		"B": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			close(startedB)
			<-releaseB
			return Resolved(nil, nil)
		}),
	})

	var mu sync.Mutex
	completed := make([]string, 0, 2)
	completedB := make(chan struct{})
	ctx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		if update.Status == "completed" {
			mu.Lock()
			completed = append(completed, update.Tracker)
			mu.Unlock()
			if update.Tracker == "B" {
				close(completedB)
			}
		}
	})
	done := make(chan api.DupeCheckSummary, 1)
	go func() {
		summary, _ := service.Check(ctx, api.DuplicateSubject{SourcePath: "C:/media/example.mkv"}, []string{"A", "B"})
		done <- summary
	}()
	waitForSignal := func(label string, signal <-chan struct{}) {
		t.Helper()
		select {
		case <-signal:
		case <-time.After(10 * time.Second):
			t.Fatalf("timeout waiting for %s", label)
		}
	}
	waitForSignal("adapter A to start", startedA)
	waitForSignal("adapter B to start", startedB)
	close(releaseB)
	waitForSignal("adapter B completion progress", completedB)
	close(releaseA)
	var summary api.DupeCheckSummary
	select {
	case summary = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for duplicate check summary")
	}
	if len(summary.Results) != 2 || summary.Results[0].Tracker != "A" || summary.Results[1].Tracker != "B" {
		t.Fatalf("resolved order not preserved: %#v", summary.Results)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(completed) != 2 || completed[0] != "B" || completed[1] != "A" {
		t.Fatalf("completion progress not actual order: %v", completed)
	}
}

func TestCheckLimitsConcurrencyToFour(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	release := make(chan struct{})
	adapters := make(map[string]Adapter)
	for _, name := range []string{"A", "B", "C", "D", "E", "F"} {
		adapters[name] = AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			current := active.Add(1)
			for {
				previous := maximum.Load()
				if current <= previous || maximum.CompareAndSwap(previous, current) {
					break
				}
			}
			<-release
			active.Add(-1)
			return Resolved(nil, nil)
		})
	}
	done := make(chan struct{})
	go func() {
		_, _ = testService(adapters).Check(context.Background(), api.DuplicateSubject{SourcePath: "C:/media/example.mkv"}, []string{"A", "B", "C", "D", "E", "F"})
		close(done)
	}()
	deadline := time.After(10 * time.Second)
	for maximum.Load() < 4 {
		select {
		case <-deadline:
			t.Fatal("workers did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if maximum.Load() > maxDupeWorkers {
		t.Fatalf("maximum concurrency = %d", maximum.Load())
	}
	close(release)
	<-done
}

func TestCheckCancellationWaitsForStartedAdapterAndReturnsCompletedEvidence(t *testing.T) {
	started := make(chan struct{})
	completed := make(chan struct{})
	release := make(chan struct{})
	service := testService(map[string]Adapter{
		"A": AdapterFunc(func(ctx context.Context, _ api.DuplicateSubject) AdapterResult {
			close(started)
			<-ctx.Done()
			<-release
			return Failed(FailureRequest, "canceled", ctx.Err())
		}),
		"B": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			close(completed)
			return Resolved(nil, nil)
		}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		summary api.DupeCheckSummary
		err     error
	}, 1)
	go func() {
		summary, err := service.Check(ctx, api.DuplicateSubject{SourcePath: "C:/media/example.mkv"}, []string{"B", "A"})
		done <- struct {
			summary api.DupeCheckSummary
			err     error
		}{summary: summary, err: err}
	}()
	<-started
	<-completed
	cancel()
	select {
	case <-done:
		t.Fatal("cancellation detached a started adapter")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	got := <-done
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("error = %v", got.err)
	}
	if len(got.summary.Results) != 1 || got.summary.Results[0].Tracker != "B" {
		t.Fatalf("completed evidence lost: %#v", got.summary.Results)
	}
}

func TestPublicProjectionBlanksPrivateDownloadsAndSanitizesURLQueries(t *testing.T) {
	service := testService(map[string]Adapter{
		"A": AdapterFunc(func(context.Context, api.DuplicateSubject) AdapterResult {
			return Resolved([]api.DupeEntry{{
				Name:        "Example.Release.2026.1080p-GRP",
				Link:        "https://user@tracker.example/torrents.php?id=44&torrentid=55&token=secret#private",
				Download:    "https://tracker.example/download/1?passkey=secret",
				Attributes:  map[string]string{"authkey": "secret"},
				BDInfo:      "private tracker payload",
				Description: "private description",
			}}, nil)
		}),
	})
	summary, err := service.Check(context.Background(), api.DuplicateSubject{SourcePath: "C:/media/example.mkv"}, []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Results[0].Evaluations) != 1 {
		t.Fatalf("public evaluations = %#v", summary.Results[0].Evaluations)
	}
	entry := summary.Results[0].Evaluations[0]
	if entry.Link != "https://tracker.example/torrents.php?id=44&torrentid=55" {
		t.Fatalf("private URL leaked: %#v", entry)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	for _, secret := range []string{"passkey=secret", "authkey", "private tracker payload", "private description"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("private protocol payload leaked: %s", encoded)
		}
	}
}
