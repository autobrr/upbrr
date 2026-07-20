// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type dryRunPreparedStore struct {
	release api.PreparedRelease
}

func (s *dryRunPreparedStore) LoadPreparedRelease(_ context.Context, sourcePath string) (api.PreparedRelease, error) {
	if s.release.Generation == 0 || !strings.EqualFold(filepath.Clean(s.release.Source.SourcePath), filepath.Clean(sourcePath)) {
		return api.PreparedRelease{}, internalerrors.ErrNotFound
	}
	cloned, err := s.release.Clone()
	if err != nil {
		return api.PreparedRelease{}, fmt.Errorf("clone loaded release: %w", err)
	}
	return cloned, nil
}

func (s *dryRunPreparedStore) CommitPreparedRelease(_ context.Context, release api.PreparedRelease) error {
	cloned, err := release.Clone()
	if err != nil {
		return fmt.Errorf("clone committed release: %w", err)
	}
	s.release = cloned
	return nil
}

func (s *dryRunPreparedStore) PurgePreparedRelease(context.Context, string) error {
	s.release = api.PreparedRelease{}
	return nil
}

type dryRunIdentityResolver struct{}

func (dryRunIdentityResolver) Resolve(_ context.Context, request externalidentity.Request) (externalidentity.Result, error) {
	now := time.Now().UTC()
	return externalidentity.Result{
		Identity: api.ExternalIdentity{
			SourcePath: request.SourcePath,
			Generation: request.Generation,
			TMDBID:     1234567,
			Category:   api.CanonicalCategoryMovie,
			Provenance: api.IdentityProvenanceSet{
				TMDB:     api.IdentityProvenanceProvider,
				Category: api.IdentityProvenanceProvider,
			},
			Conflict: api.IdentityConflictNone,
			Resolution: api.IdentityResolutionKey{
				SourceFingerprint: request.SourceFingerprint,
				IntentFingerprint: "dry-run-test",
				ContractVersion:   externalidentity.ContractVersion,
			},
			ResolvedAt: now,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: request.SourcePath,
			Generation: request.Generation,
			UpdatedAt:  now,
		},
	}, nil
}

type dryRunFactCollector struct{}

func (dryRunFactCollector) Collect(_ context.Context, request preparationstate.Request) (preparedrelease.CollectedFacts, error) {
	return preparedrelease.CollectedFacts{
		Naming: api.NamingFacts{
			Filename:    filepath.Base(request.Manifest.SourcePath),
			ReleaseName: "Example.Release.2026.1080p-GRP",
		},
		Assessments: api.ReleaseAssessments{
			MediaInfoUniqueID:       api.UniqueIDStatusPresent,
			MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent,
			Naming:                  api.NamingAssessment{Status: api.NamingStatusComplete},
		},
		Resources: preparedrelease.CollectedResources{
			SourcePath: request.Manifest.SourcePath,
			FileList:   []string{request.Manifest.SourcePath},
		},
	}, nil
}

type dryRunTrackerService struct {
	uploadCalls int
	buildCalls  int
	reviewCalls int
	status      string
}

func (s *dryRunTrackerService) Upload(context.Context, api.UploadSubject) (api.UploadSummary, error) {
	s.uploadCalls++
	return api.UploadSummary{Uploaded: 1}, nil
}

func (*dryRunTrackerService) BuildPreparation(context.Context, api.DescriptionSubject, []string) (api.PreparationPreview, error) {
	return api.PreparationPreview{}, nil
}

func (s *dryRunTrackerService) BuildUploadReview(context.Context, api.UploadSubject, []string) ([]api.TrackerDryRunEntry, error) {
	s.reviewCalls++
	return nil, nil
}

func (s *dryRunTrackerService) BuildUploadDryRun(context.Context, api.UploadSubject, []string) ([]api.TrackerDryRunEntry, error) {
	s.buildCalls++
	status := s.status
	if status == "" {
		status = "ready"
	}
	return []api.TrackerDryRunEntry{{
		Tracker:  "BLU",
		Status:   status,
		Endpoint: "https://user:super-secret@example.invalid/upload?api_key=super-secret",
		Payload:  map[string]string{"api_key": "super-secret", "name": "Example.Release.2026.1080p-GRP"},
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent",
			Path:    "prepared-blu.torrent",
			Present: true,
		}},
	}}, nil
}

type dryRunTorrentService struct{}

func (dryRunTorrentService) Create(context.Context, api.TorrentSubject) (api.TorrentResult, error) {
	return api.TorrentResult{Path: "base.torrent", InfoHash: "0123456789abcdef"}, nil
}

type dryRunClientService struct {
	injections []api.TorrentResult
	searches   int
}

func (s *dryRunClientService) Inject(_ context.Context, _ api.ClientSubject, torrent api.TorrentResult) error {
	s.injections = append(s.injections, torrent)
	return nil
}

func (s *dryRunClientService) SearchPathedTorrents(context.Context, api.ClientSubject) (api.ClientSearchResult, error) {
	s.searches++
	return api.ClientSearchResult{}, nil
}

type dryRunDupeService struct {
	hasDupes bool
	calls    int
}

func (s *dryRunDupeService) Check(ctx context.Context, subject api.DuplicateSubject, trackerNames []string) (api.DupeCheckSummary, error) {
	summary, _, err := s.CheckWithAssessment(ctx, subject, trackerNames, dupechecking.CheckOptions{})
	return summary, err
}

func (s *dryRunDupeService) CheckWithAssessment(
	_ context.Context,
	subject api.DuplicateSubject,
	trackerNames []string,
	_ dupechecking.CheckOptions,
) (api.DupeCheckSummary, dupechecking.Assessment, error) {
	s.calls++
	results := make([]api.DupeCheckResult, 0, len(trackerNames))
	evidence := make([]dupechecking.AssessmentEvidence, 0, len(trackerNames))
	for _, trackerName := range trackerNames {
		results = append(results, api.DupeCheckResult{
			Tracker:  trackerName,
			Status:   "completed",
			HasDupes: s.hasDupes,
		})
		evidence = append(evidence, dupechecking.AssessmentEvidence{
			Tracker:     trackerName,
			Disposition: dupechecking.DispositionResolved,
			HasDupes:    s.hasDupes,
			Match:       api.DupeMatch{MatchedReason: "name"},
		})
	}
	return api.DupeCheckSummary{SourcePath: subject.SourcePath, Results: results}, dupechecking.NewAssessment(subject, config.Config{}, evidence), nil
}

type dryRunPolicy struct {
	blocked     bool
	ruleFailure bool
}

func (p dryRunPolicy) EvaluateUploadPolicy(context.Context, api.UploadSubject, []string) (api.UploadReviewOutcome, error) {
	outcome := api.UploadReviewOutcome{}
	if p.ruleFailure {
		outcome.TrackerRuleFailures = map[string][]api.RuleFailure{
			"BLU": {
				{
					Rule:     "source",
					Reason:   "source is not permitted",
					Disposition: api.RuleDispositionStrict,
				},
			},
		}
	}
	if p.blocked {
		outcome.BlockedTrackers = map[string][]api.TrackerBlockReason{
			"BLU": {api.TrackerBlockReasonClaim},
		}
	}
	return outcome, nil
}

func prepareDryRunCore(t *testing.T) (*Core, api.ReleaseRef, *dryRunTrackerService, *dryRunClientService, *uploadModule) {
	t.Helper()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	if err := os.WriteFile(sourcePath, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	preparedFacts, err := preparedrelease.New(&dryRunPreparedStore{}, dryRunIdentityResolver{}, dryRunFactCollector{})
	if err != nil {
		t.Fatalf("create prepared-release module: %v", err)
	}
	prepared, err := preparedFacts.Prepare(context.Background(), api.PrepareInput{SourcePath: sourcePath, Intent: api.PreparationIntentDryRun})
	if err != nil {
		t.Fatalf("prepare release: %v", err)
	}
	trackerService := &dryRunTrackerService{}
	clientService := &dryRunClientService{}
	dupeService := &dryRunDupeService{}
	module := &uploadModule{
		cfg:           config.Config{MainSettings: config.MainSettingsConfig{DBPath: t.TempDir()}},
		logger:        api.NopLogger{},
		policy:        dryRunPolicy{},
		trackers:      trackerService,
		torrents:      dryRunTorrentService{},
		clients:       clientService,
		dupes:         dupeService,
		registry:      trackerimpl.MustNewRegistry(),
		preparedFacts: preparedFacts,
		resolveSubjectGroups: func(context.Context, api.UploadSubject, api.UploadReviewInput) ([]api.DescriptionBuilderGroup, error) {
			return nil, nil
		},
	}
	return &Core{upload: module}, api.ReleaseRef{SourcePath: sourcePath, Generation: prepared.Release.Generation}, trackerService, clientService, module
}

func TestRunAcceptedTrackerDryRunOwnsSubmissionAndInjectionPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		noSeed      bool
		duplicate   bool
		blocked     bool
		ruleFailure bool
		ignoreDupes bool
		payloadStatus string
		wantInject  int
		wantStatus  string
	}{
		{
			name:       "ready entry injects once",
			wantInject: 1,
			wantStatus: "ready",
		},
		{
			name:       "no-seed suppresses injection",
			noSeed:     true,
			wantStatus: "ready",
		},
		{
			name:       "duplicate remains visible and permits injection",
			duplicate:  true,
			wantInject: 1,
			wantStatus: "ready",
		},
		{
			name:        "explicit duplicate override permits injection",
			duplicate:   true,
			ignoreDupes: true,
			wantInject:  1,
			wantStatus:  "ready",
		},
		{
			name:        "strict rule remains diagnostic and permits injection",
			ruleFailure: true,
			wantInject:  1,
			wantStatus:  "ready",
		},
		{
			name:       "policy block remains diagnostic and permits injection",
			blocked:    true,
			wantInject: 1,
			wantStatus: "ready",
		},
		{
			name:          "operationally failed payload does not inject",
			payloadStatus: "error",
			wantStatus:    "error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coreSvc, release, trackerService, clientService, module := prepareDryRunCore(t)
			trackerService.status = test.payloadStatus
			module.policy = dryRunPolicy{blocked: test.blocked, ruleFailure: test.ruleFailure}
			var ignoreDupesFor []string
			if test.ignoreDupes {
				ignoreDupesFor = []string{"BLU"}
			}
			var progress []api.UploadProgressUpdate
			ctx := api.WithUploadProgressReporter(context.Background(), func(update api.UploadProgressUpdate) {
				progress = append(progress, update)
			})
			preview, err := coreSvc.RunAcceptedTrackerDryRun(ctx, api.TrackerDryRunPlan{
				Input: api.TrackerDryRunInput{
					Release:               release,
					Trackers:              []string{"blu", "BLU"},
					IgnoreDupesFor: ignoreDupesFor,
					Options:               api.UploadOptions{NoSeed: test.noSeed},
				},
				Duplicate: api.AcceptedDuplicateEvidence{
					Release:  release,
					Trackers: []string{"BLU"},
					Results: []api.DupeCheckResult{{
						Tracker:  "BLU",
						Status:   "completed",
						HasDupes: test.duplicate,
					}},
				},
			})
			if err != nil {
				t.Fatalf("run accepted tracker dry run: %v", err)
			}
			if len(preview.Trackers) != 1 || preview.Trackers[0].Status != test.wantStatus {
				t.Fatalf("preview = %#v", preview)
			}
			if trackerService.uploadCalls != 0 || trackerService.buildCalls != 1 || trackerService.reviewCalls != 0 {
				t.Fatalf("tracker calls: upload=%d dry-run=%d review=%d", trackerService.uploadCalls, trackerService.buildCalls, trackerService.reviewCalls)
			}
			if len(clientService.injections) != test.wantInject {
				t.Fatalf("client injections = %d, want %d", len(clientService.injections), test.wantInject)
			}
			if clientService.searches != 0 {
				t.Fatalf("dry run client searches = %d, want 0", clientService.searches)
			}
			dupeService, ok := module.dupes.(*dryRunDupeService)
			if !ok {
				t.Fatalf("duplicate service type = %T", module.dupes)
			}
			if dupeService.calls != 0 {
				t.Fatalf("dry run duplicate checks = %d, want 0", dupeService.calls)
			}
			if len(progress) < 2 || progress[0].Task != "dry_run" || progress[0].Status != "running" ||
				progress[len(progress)-1].Task != "dry_run" || progress[len(progress)-1].Status != "completed" {
				t.Fatalf("dry-run progress = %#v", progress)
			}
			serialized, marshalErr := json.Marshal(preview)
			if marshalErr != nil {
				t.Fatalf("marshal preview: %v", marshalErr)
			}
			if strings.Contains(string(serialized), "super-secret") {
				t.Fatalf("preview leaked sensitive payload: %s", serialized)
			}
		})
	}
}

func TestReviewAcceptedUploadRefreshesClientAndDuplicateAuthorityOnce(t *testing.T) {
	t.Parallel()

	coreSvc, release, trackerService, clientService, module := prepareDryRunCore(t)
	reviewed, err := coreSvc.ReviewAcceptedUpload(context.Background(), api.UploadReviewInput{
		Release:  release,
		Trackers: []string{"BLU"},
		Options:  api.UploadOptions{NoSeed: true},
	})
	if err != nil {
		t.Fatalf("review accepted upload: %v", err)
	}
	if len(reviewed.Outcome.Eligibility.EligibleTrackers) != 1 || reviewed.Outcome.Eligibility.EligibleTrackers[0] != "BLU" {
		t.Fatalf("review eligibility = %#v", reviewed.Outcome.Eligibility)
	}
	if clientService.searches != 0 {
		t.Fatalf("review client searches = %d, want 0", clientService.searches)
	}
	dupeService, ok := module.dupes.(*dryRunDupeService)
	if !ok {
		t.Fatalf("duplicate service type = %T", module.dupes)
	}
	if dupeService.calls != 1 {
		t.Fatalf("review duplicate checks = %d, want 1", dupeService.calls)
	}
	if trackerService.reviewCalls != 1 || trackerService.buildCalls != 0 || trackerService.uploadCalls != 0 {
		t.Fatalf(
			"tracker calls: review=%d dry-run=%d upload=%d",
			trackerService.reviewCalls,
			trackerService.buildCalls,
			trackerService.uploadCalls,
		)
	}
}

func TestRunAcceptedTrackerDryRunRequiresExactGenerationAndTrackers(t *testing.T) {
	t.Parallel()

	coreSvc, release, _, _, _ := prepareDryRunCore(t)
	_, err := coreSvc.RunAcceptedTrackerDryRun(context.Background(), api.TrackerDryRunPlan{Input: api.TrackerDryRunInput{Release: release}})
	if err == nil {
		t.Fatal("expected empty tracker selection to fail")
	}
	release.Generation++
	_, err = coreSvc.RunAcceptedTrackerDryRun(context.Background(), api.TrackerDryRunPlan{
		Input: api.TrackerDryRunInput{Release: release, Trackers: []string{"BLU"}},
		Duplicate: api.AcceptedDuplicateEvidence{
			Release:  api.ReleaseRef{SourcePath: release.SourcePath, Generation: release.Generation - 1},
			Trackers: []string{"BLU"},
			Results:  []api.DupeCheckResult{{Tracker: "BLU", Status: "completed"}},
		},
	})
	failure, ok := api.AsOperationFailure(err)
	if !ok || failure.Code != api.OperationFailureStaleGeneration {
		t.Fatalf("stale generation error = %v", err)
	}
	release.Generation--

	tests := []struct {
		name     string
		evidence api.AcceptedDuplicateEvidence
	}{
		{
			name: "mismatched tracker selection",
			evidence: api.AcceptedDuplicateEvidence{
				Release:  release,
				Trackers: []string{"AITHER"},
				Results:  []api.DupeCheckResult{{Tracker: "AITHER", Status: "completed"}},
			},
		},
		{
			name: "missing tracker result",
			evidence: api.AcceptedDuplicateEvidence{
				Release:  release,
				Trackers: []string{"BLU"},
			},
		},
		{
			name: "nonterminal tracker result",
			evidence: api.AcceptedDuplicateEvidence{
				Release:  release,
				Trackers: []string{"BLU"},
				Results:  []api.DupeCheckResult{{Tracker: "BLU", Status: "running"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, testErr := coreSvc.RunAcceptedTrackerDryRun(context.Background(), api.TrackerDryRunPlan{
				Input:     api.TrackerDryRunInput{Release: release, Trackers: []string{"BLU"}},
				Duplicate: test.evidence,
			})
			testFailure, testOK := api.AsOperationFailure(testErr)
			if !testOK || testFailure.Code != api.OperationFailureMissingPrerequisite {
				t.Fatalf("missing prerequisite error = %v", testErr)
			}
		})
	}
}

func TestRunAcceptedTrackerDryRunHonorsPreCanceledContext(t *testing.T) {
	t.Parallel()

	coreSvc, release, trackerService, clientService, _ := prepareDryRunCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := coreSvc.RunAcceptedTrackerDryRun(ctx, api.TrackerDryRunPlan{
		Input: api.TrackerDryRunInput{Release: release, Trackers: []string{"BLU"}},
		Duplicate: api.AcceptedDuplicateEvidence{
			Release:  release,
			Trackers: []string{"BLU"},
			Results:  []api.DupeCheckResult{{Tracker: "BLU", Status: "completed"}},
		},
	})
	if err == nil {
		t.Fatal("expected canceled dry run to fail")
	}
	if trackerService.uploadCalls != 0 || trackerService.buildCalls != 0 || len(clientService.injections) != 0 {
		t.Fatalf("canceled dry run performed side effects: tracker=%#v clients=%#v", trackerService, clientService.injections)
	}
}

func TestRunAcceptedUploadWithDebugLogLevelStillSubmits(t *testing.T) {
	t.Parallel()

	coreSvc, release, trackerService, clientService, _ := prepareDryRunCore(t)
	result, err := coreSvc.RunAcceptedUpload(context.Background(), api.UploadExecutionPlan{
		Input: api.UploadReviewInput{
			Release:  release,
			Trackers: []string{"BLU"},
			Options:  api.UploadOptions{NoSeed: true, RunLogLevel: "debug"},
		},
		Outcome: api.UploadReviewOutcome{
			ResolvedTrackers: []string{"BLU"},
			Eligibility: api.TrackerEligibility{
				Release:          release,
				EligibleTrackers: []string{"BLU"},
			},
		},
	})
	if err != nil {
		t.Fatalf("run accepted upload: %v", err)
	}
	if result.UploadedCount != 1 || trackerService.uploadCalls != 1 || trackerService.buildCalls != 0 {
		t.Fatalf("upload result=%#v calls: upload=%d dry-run=%d", result, trackerService.uploadCalls, trackerService.buildCalls)
	}
	if len(clientService.injections) != 0 {
		t.Fatalf("no-seed upload injected clients: %#v", clientService.injections)
	}
}

var _ api.TrackerService = (*dryRunTrackerService)(nil)
var _ api.TorrentService = dryRunTorrentService{}
var _ api.ClientService = (*dryRunClientService)(nil)
var _ api.DupeService = (*dryRunDupeService)(nil)
var _ uploadPolicyEvaluator = dryRunPolicy{}
var _ preparedrelease.Store = (*dryRunPreparedStore)(nil)
