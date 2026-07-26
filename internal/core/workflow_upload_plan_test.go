// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type dryRunClientService struct {
	injections []api.TorrentResult
	searches   int
	injectErr  error
}

func (s *dryRunClientService) Inject(_ context.Context, _ api.ClientSubject, torrent api.TorrentResult) error {
	s.injections = append(s.injections, torrent)
	return s.injectErr
}

func (s *dryRunClientService) SearchPathedTorrents(context.Context, api.ClientSubject) (api.ClientSearchResult, error) {
	s.searches++
	return api.ClientSearchResult{}, nil
}

type workflowRetainedUploadPlanFake struct {
	preparations []trackers.RetainedTrackerPreparation
	results      []trackers.RetainedTrackerResult
}

func (f *workflowRetainedUploadPlanFake) Preparations() []trackers.RetainedTrackerPreparation {
	return append([]trackers.RetainedTrackerPreparation(nil), f.preparations...)
}

func (f *workflowRetainedUploadPlanFake) Execute(context.Context) ([]trackers.RetainedTrackerResult, error) {
	return append([]trackers.RetainedTrackerResult(nil), f.results...), nil
}

func (f *workflowRetainedUploadPlanFake) ExecuteSelected(
	_ context.Context,
	trackerIDs []string,
) ([]trackers.RetainedTrackerResult, error) {
	selected := make(map[string]struct{}, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		selected[strings.ToUpper(strings.TrimSpace(trackerID))] = struct{}{}
	}
	results := make([]trackers.RetainedTrackerResult, 0, len(trackerIDs))
	for _, result := range f.results {
		if _, exists := selected[strings.ToUpper(strings.TrimSpace(result.Tracker))]; exists {
			results = append(results, result)
		}
	}
	return results, nil
}

func (*workflowRetainedUploadPlanFake) Release() error { return nil }

type workflowRetainedUploadServiceFake struct {
	projections  []api.TrackerReleaseProjection
	failures     map[api.TrackerID]trackers.TrackerFailure
	torrentPaths map[api.TrackerID]string
	results      []trackers.RetainedTrackerResult
	subject      api.UploadSubject
}

func (f *workflowRetainedUploadServiceFake) PrepareRetainedUploadPlan(
	_ context.Context,
	subject api.UploadSubject,
	projections []api.TrackerReleaseProjection,
) (workflowRetainedUploadPlan, error) {
	f.subject = subject
	f.projections = append([]api.TrackerReleaseProjection(nil), projections...)
	preparations := make([]trackers.RetainedTrackerPreparation, 0, len(projections))
	for _, projection := range projections {
		preparation := trackers.RetainedTrackerPreparation{
			Tracker: string(projection.TrackerID),
			Preview: api.TrackerDryRunEntry{
				Tracker: string(projection.TrackerID),
				Status:  "ready",
				Files: []api.TrackerDryRunFile{{
					Field:   "file_input",
					Path:    filepath.Join("preview", "must-not-drive-injection.torrent"),
					Present: true,
				}},
			},
		}
		if torrentPath := f.torrentPaths[projection.TrackerID]; torrentPath != "" {
			preparation.TorrentPath = torrentPath
		}
		if failure, ok := f.failures[projection.TrackerID]; ok {
			preparation.Failure = &failure
		}
		preparations = append(preparations, preparation)
	}
	return &workflowRetainedUploadPlanFake{
		preparations: preparations,
		results:      append([]trackers.RetainedTrackerResult(nil), f.results...),
	}, nil
}

func TestSanitizeWorkflowUploadPreviewPreservesSemanticsWithoutSecretsOrPaths(t *testing.T) {
	t.Parallel()

	endpoint, fields, files := sanitizeWorkflowUploadPreview(api.TrackerDryRunEntry{
		Endpoint: "https://tracker.example/upload?api_token=private#fragment",
		Payload: map[string]string{
			"api_token": "private",
			"name":      "Example.Release.2026.REVIEWED-GRP",
			"poster":    "https://images.example/poster.jpg?signature=private",
			"torrent":   "C:\\private\\Example.Release.2026.torrent",
		},
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent",
			Path:    "C:\\private\\Example.Release.2026.torrent",
			Present: true,
		}},
	})
	if endpoint != "https://tracker.example/upload" {
		t.Fatalf("sanitized endpoint = %q", endpoint)
	}
	values := make(map[string]string, len(fields))
	for _, field := range fields {
		values[field.Key] = field.Value
	}
	if values["api_token"] != "[redacted]" || values["torrent"] != "[private path]" ||
		values["name"] != "Example.Release.2026.REVIEWED-GRP" || values["poster"] != "https://images.example/poster.jpg" {
		t.Fatalf("sanitized fields = %#v", fields)
	}
	if len(files) != 1 || files[0].Field != "torrent" || !files[0].Present {
		t.Fatalf("sanitized files = %#v", files)
	}
	for _, value := range []string{endpoint, values["api_token"], values["torrent"], values["poster"]} {
		if value == "private" || strings.Contains(value, "C:\\private") || strings.Contains(value, "signature=private") {
			t.Fatalf("sanitized preview exposed private material: %q", value)
		}
	}
}

func TestWorkflowUploadPlanFingerprintChangesWithReviewedDependency(t *testing.T) {
	t.Parallel()

	builder := workflowUploadPlanBuilder{}
	projections := api.TrackerReleaseProjectionSet{
		ID:               "projections-1",
		Revision:         1,
		InputFingerprint: workflowTestFingerprint(t, "projection"),
	}
	dupes := api.DupeAssessment{
		ID:               "dupes-1",
		Revision:         2,
		InputFingerprint: workflowTestFingerprint(t, "dupes"),
	}
	media := api.MediaArtifactSet{
		ID:                 "media-1",
		Revision:           3,
		CaptureFingerprint: workflowTestFingerprint(t, "media"),
	}
	descriptions := api.DescriptionSet{
		ID:               "descriptions-1",
		Revision:         4,
		InputFingerprint: workflowTestFingerprint(t, "descriptions"),
	}
	first, err := builder.Fingerprint(context.Background(), projections, dupes, media, descriptions, releaseworkflow.UploadPlanBuildOptions{})
	if err != nil {
		t.Fatalf("fingerprint upload plan: %v", err)
	}
	dupes.Revision++
	changed, err := builder.Fingerprint(context.Background(), projections, dupes, media, descriptions, releaseworkflow.UploadPlanBuildOptions{})
	if err != nil {
		t.Fatalf("fingerprint changed upload plan: %v", err)
	}
	if first == changed {
		t.Fatal("upload plan fingerprint ignored duplicate revision")
	}
	options := releaseworkflow.UploadPlanBuildOptions{
		TrackerIDs:           []api.TrackerID{"ALPHA"},
		TrackerApproval:      &api.TrackerApprovalSnapshotRef{ID: "approval-1", Revision: 5},
		AuthorityFingerprint: workflowTestFingerprint(t, "approval-authority"),
	}
	authorityFirst, err := builder.Fingerprint(context.Background(), projections, dupes, media, descriptions, options)
	if err != nil {
		t.Fatalf("fingerprint authorized upload plan: %v", err)
	}
	options.AuthorityFingerprint = workflowTestFingerprint(t, "changed-approval-authority")
	authorityChanged, err := builder.Fingerprint(context.Background(), projections, dupes, media, descriptions, options)
	if err != nil {
		t.Fatalf("fingerprint changed upload authority: %v", err)
	}
	if authorityFirst == authorityChanged {
		t.Fatal("upload plan fingerprint ignored tracker authority")
	}
}

func TestDryRunClientInjectionReportsAggregateTerminalProgress(t *testing.T) {
	t.Parallel()

	torrentPath := filepath.Join(t.TempDir(), "Example.Release.2026.ALPHA-GRP.torrent")
	if err := os.WriteFile(torrentPath, []byte("exact tracker torrent"), 0o600); err != nil {
		t.Fatalf("write exact tracker torrent: %v", err)
	}
	updates := make([]api.WorkflowProgressUpdate, 0, 2)
	ctx := api.WithWorkflowProgressReporter(context.Background(), func(update api.WorkflowProgressUpdate) {
		updates = append(updates, update)
	})
	clientService := &dryRunClientService{}
	projection := api.TrackerReleaseProjection{TrackerID: "ALPHA", DisplayName: "Alpha"}

	status, _, _, injected, err := injectWorkflowDryRunClient(
		ctx,
		clientService,
		api.ClientSubject{},
		projection,
		torrentPath,
		2,
		4,
	)
	if err != nil {
		t.Fatalf("inject dry-run client: %v", err)
	}
	if status != api.StageStatusCompleted || !injected {
		t.Fatalf("client injection status=%s injected=%t", status, injected)
	}
	if len(updates) != 2 {
		t.Fatalf("client injection progress = %#v", updates)
	}
	if updates[0].Status != api.StageStatusRunning || updates[0].Completed != 2 || updates[0].Total != 4 {
		t.Fatalf("running client injection progress = %#v", updates[0])
	}
	if updates[1].Status != api.StageStatusCompleted || updates[1].Completed != 3 || updates[1].Total != 4 {
		t.Fatalf("terminal client injection progress = %#v", updates[1])
	}
}

func TestWorkflowDryRunClientFailureRetainsReconciliationIdentity(t *testing.T) {
	t.Parallel()

	failure := workflowDryRunClientFailure(
		"ALPHA",
		api.OperationFailureUnknownOutcome,
		"Client injection outcome is unknown.",
	)
	if failure.Failure.Operation != api.OperationKindClientInjection ||
		failure.Failure.Recovery != api.OperationRecoveryConfirm ||
		failure.Resource != "dry-run:ALPHA" {
		t.Fatalf("unknown client effect failure = %#v", failure)
	}
}

func TestWorkflowUploadPlanOmitsSkippedTrackersAndKeepsPreparationFailuresLocal(t *testing.T) {
	t.Parallel()

	service := &workflowRetainedUploadServiceFake{failures: map[api.TrackerID]trackers.TrackerFailure{
		"GAMMA": {
			Tracker: "GAMMA",
			Code:    "prepare",
			Message: "fixture preparation failed",
		},
	}, torrentPaths: map[api.TrackerID]string{
		"ALPHA": filepath.Join(t.TempDir(), "Example.Release.2026.ALPHA-GRP.torrent"),
		"GAMMA": filepath.Join(t.TempDir(), "Example.Release.2026.GAMMA-GRP.torrent"),
	}}
	for tracker, path := range service.torrentPaths {
		if err := os.WriteFile(path, []byte("exact torrent "+tracker), 0o600); err != nil {
			t.Fatalf("write %s torrent: %v", tracker, err)
		}
	}
	clientService := &dryRunClientService{}
	builder := workflowUploadPlanBuilder{
		resolver: &workflowDescriptionResolverFake{},
		trackers: service,
		clients:  clientService,
	}
	projections := api.TrackerReleaseProjectionSet{
		ExecutionMode: api.WorkflowExecutionModeDebug,
		ReleaseRef:    api.ReleaseRef{SourcePath: "C:\\media\\Example.Release.2026", Generation: 1},
		Projections: []api.TrackerReleaseProjection{
			{
				TrackerID:         "ALPHA",
				DisplayName:       "Alpha",
				UploadReleaseName: "Example.Release.2026.ALPHA-GRP",
				Artifacts:         api.TrackerArtifactRequirements{Description: true},
				Readiness:         api.ReadinessStatusReady,
				UploadReady:       true,
			},
			{
				TrackerID:         "BETA",
				DisplayName:       "Beta",
				UploadReleaseName: "Example.Release.2026.BETA-GRP",
				Artifacts:         api.TrackerArtifactRequirements{Description: true},
				Readiness:         api.ReadinessStatusReady,
				UploadReady:       true,
			},
			{
				TrackerID:         "GAMMA",
				DisplayName:       "Gamma",
				UploadReleaseName: "Example.Release.2026.GAMMA-GRP",
				Artifacts:         api.TrackerArtifactRequirements{Description: true},
				Readiness:         api.ReadinessStatusReady,
				UploadReady:       true,
			},
		},
	}
	dupes := api.DupeAssessment{Results: []api.TrackerDupeAssessment{
		{
			TrackerID: "ALPHA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		},
		{
			TrackerID: "BETA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		},
		{
			TrackerID: "GAMMA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		},
	}}
	media := api.MediaArtifactSet{
		ID:                 "media-1",
		Revision:           1,
		CaptureFingerprint: workflowTestFingerprint(t, "media"),
		Artifacts: []api.MediaArtifact{
			{
				ID:       "screenshot-1",
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
			},
			{
				ID:       "menu-1",
				Kind:     api.MediaArtifactDVDMenu,
				Purpose:  api.ScreenshotPurposeMenu,
				Selected: true,
			},
			{
				ID:       "hosted-1",
				Kind:     api.MediaArtifactHostedImage,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
				Source:   "screenshot-1",
			},
			{
				ID:       "hosted-menu-1",
				Kind:     api.MediaArtifactHostedImage,
				Purpose:  api.ScreenshotPurposeMenu,
				Selected: true,
				Source:   "menu-1",
			},
		},
	}
	exactScreenshotPath := filepath.Join(t.TempDir(), "screen.png")
	exactMenuPath := filepath.Join(t.TempDir(), "menu.png")
	exactUpload := api.UploadedImageLink{
		ImagePath:  exactScreenshotPath,
		Host:       "imgbox",
		UsageScope: "global",
		RawURL:     "https://img.example/exact-screen.png",
	}
	exactMenuUpload := api.UploadedImageLink{
		ImagePath:  exactMenuPath,
		Host:       "imgbox",
		UsageScope: "global",
		RawURL:     "https://img.example/exact-menu.png",
	}
	privateMedia := workflowMediaPrivateArtifacts{
		Screenshots: []api.ScreenshotImage{{Path: exactScreenshotPath, Purpose: api.ScreenshotPurposeFinal}},
		DVDMenus: []api.DVDMenuCaptureImage{{ScreenshotImage: api.ScreenshotImage{
			Path: exactMenuPath, Purpose: api.ScreenshotPurposeMenu,
		}}},
		HostedImages: map[api.PublicResourceID]api.UploadedImageLink{
			"hosted-1":      exactUpload,
			"hosted-menu-1": exactMenuUpload,
		},
		HostedSources: map[api.PublicResourceID]api.PublicResourceID{
			"hosted-1":      "screenshot-1",
			"hosted-menu-1": "menu-1",
		},
	}
	descriptions := api.DescriptionSet{
		ID:               "descriptions-1",
		Revision:         1,
		InputFingerprint: workflowTestFingerprint(t, "descriptions"),
		Descriptions: []api.RenderedDescription{{
			GroupKey:   "alpha",
			TrackerIDs: []api.TrackerID{"ALPHA", "GAMMA"},
		}},
		TrackerResults: []api.DescriptionTrackerResult{
			{TrackerID: "ALPHA", Status: api.StageStatusCompleted},
			{
				TrackerID: "BETA",
				Status:    api.StageStatusSkipped,
				Message:   "BETA could not find an allowed screenshot host.",
			},
		},
	}

	plan, execution, err := builder.Build(
		context.Background(),
		projections,
		dupes,
		workflowDupePrivateEvidence{},
		media,
		privateMedia,
		descriptions,
		api.DescriptionInstructions{},
		releaseworkflow.UploadPlanBuildOptions{DryRun: true},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build workflow upload plan: %v", err)
	}
	defer func() { _ = execution.Release() }()
	if plan.Status != api.StageStatusReady || len(plan.Trackers) != 2 {
		t.Fatalf("upload plan = %#v", plan)
	}
	if plan.ProjectionSet.ID != projections.ID || plan.Dupes.ID != dupes.ID || plan.Media == nil || plan.Media.ID != media.ID ||
		plan.Descriptions == nil || plan.Descriptions.ID != descriptions.ID {
		t.Fatalf("upload plan exact refs = %#v", plan)
	}
	if len(service.projections) != 2 || service.projections[0].TrackerID != "ALPHA" || service.projections[1].TrackerID != "GAMMA" {
		t.Fatalf("retained upload projections = %#v", service.projections)
	}
	if service.subject.ImageHostOverrides.SkipUpload == nil || !*service.subject.ImageHostOverrides.SkipUpload {
		t.Fatalf("retained preparation allowed hidden image upload: %#v", service.subject.ImageHostOverrides)
	}
	if service.subject.ExactMedia == nil || len(service.subject.ExactMedia.Screenshots) != 1 ||
		service.subject.ExactMedia.Screenshots[0].Path != exactScreenshotPath ||
		len(service.subject.ExactMedia.ScreenshotUploads) != 1 ||
		service.subject.ExactMedia.ScreenshotUploads[0].RawURL != exactUpload.RawURL ||
		len(service.subject.ExactMedia.DVDMenus) != 1 ||
		service.subject.ExactMedia.DVDMenus[0].Path != exactMenuPath ||
		len(service.subject.ExactMedia.DVDMenuUploads) != 1 ||
		service.subject.ExactMedia.DVDMenuUploads[0].RawURL != exactMenuUpload.RawURL {
		t.Fatalf(
			"retained preparation exact media screenshots=%#v uploads=%#v",
			service.subject.ExactMedia,
			service.subject.ExactMedia,
		)
	}
	if !plan.Trackers[0].Eligible || plan.Trackers[0].Status != api.StageStatusReady {
		t.Fatalf("ready tracker = %#v", plan.Trackers[0])
	}
	if plan.Trackers[0].PreparedOperationID == "" || plan.Trackers[0].TorrentArtifactID == "" ||
		plan.Trackers[0].TorrentFingerprint == "" {
		t.Fatalf("ready tracker exact private identities = %#v", plan.Trackers[0])
	}
	publicPlan, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal public upload plan: %v", err)
	}
	if strings.Contains(string(publicPlan), service.torrentPaths["ALPHA"]) || strings.Contains(string(publicPlan), "must-not-drive-injection") {
		t.Fatalf("public upload plan exposed private torrent authority: %s", publicPlan)
	}
	if plan.Trackers[0].ClientInjectionStatus != api.StageStatusCompleted || len(clientService.injections) != 1 ||
		clientService.injections[0].Tracker != "ALPHA" || clientService.injections[0].Path != service.torrentPaths["ALPHA"] {
		t.Fatalf("dry-run client injection: tracker=%#v injections=%#v", plan.Trackers[0], clientService.injections)
	}
	if plan.Trackers[1].TrackerID != "GAMMA" || plan.Trackers[1].Eligible || plan.Trackers[1].Status != api.StageStatusFailed ||
		plan.Trackers[1].ClientInjectionStatus != api.StageStatusSkipped || len(plan.Trackers[1].Warnings) != 1 ||
		plan.Trackers[1].Warnings[0] != "fixture preparation failed" {
		t.Fatalf("failed tracker = %#v", plan.Trackers[1])
	}

	noSeedPlan, noSeedExecution, err := builder.Build(
		context.Background(),
		projections,
		dupes,
		workflowDupePrivateEvidence{},
		media,
		privateMedia,
		descriptions,
		api.DescriptionInstructions{},
		releaseworkflow.UploadPlanBuildOptions{DryRun: true, NoSeed: true},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build no-seed workflow upload plan: %v", err)
	}
	defer func() { _ = noSeedExecution.Release() }()
	if len(clientService.injections) != 1 || noSeedPlan.Trackers[0].ClientInjectionStatus != api.StageStatusSkipped {
		t.Fatalf("no-seed dry-run client injection: tracker=%#v injections=%#v", noSeedPlan.Trackers[0], clientService.injections)
	}

	clientService.injectErr = errors.New("client unavailable")
	failedPlan, failedExecution, err := builder.Build(
		context.Background(),
		projections,
		dupes,
		workflowDupePrivateEvidence{},
		media,
		privateMedia,
		descriptions,
		api.DescriptionInstructions{},
		releaseworkflow.UploadPlanBuildOptions{DryRun: true},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build workflow upload plan with failed client injection: %v", err)
	}
	defer func() { _ = failedExecution.Release() }()
	if failedPlan.Status != api.StageStatusBlocked || failedPlan.Trackers[0].Status != api.StageStatusFailed ||
		failedPlan.Trackers[0].Eligible ||
		failedPlan.Trackers[0].ClientInjectionStatus != api.StageStatusFailed || len(clientService.injections) != 2 {
		t.Fatalf("failed dry-run client injection: plan=%#v injections=%#v", failedPlan, clientService.injections)
	}

	retryPlan, retryExecution, err := builder.Build(
		context.Background(),
		projections,
		dupes,
		workflowDupePrivateEvidence{},
		media,
		privateMedia,
		descriptions,
		api.DescriptionInstructions{},
		releaseworkflow.UploadPlanBuildOptions{TrackerIDs: []api.TrackerID{"GAMMA"}},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build failed-tracker retry upload plan: %v", err)
	}
	defer func() { _ = retryExecution.Release() }()
	if len(retryPlan.Trackers) != 1 || retryPlan.Trackers[0].TrackerID != "GAMMA" ||
		len(service.projections) != 1 || service.projections[0].TrackerID != "GAMMA" {
		t.Fatalf("retry preparation was not target-scoped: plan=%#v projections=%#v", retryPlan.Trackers, service.projections)
	}
}

func TestWorkflowUploadPlanFailsTerminalImageHostTrackerAndRestoresAfterRetry(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := &workflowRetainedUploadServiceFake{torrentPaths: map[api.TrackerID]string{
		"ALPHA": filepath.Join(tempDir, "Example.Release.2026.ALPHA-GRP.torrent"),
		"BETA":  filepath.Join(tempDir, "Example.Release.2026.BETA-GRP.torrent"),
	}}
	for tracker, torrentPath := range service.torrentPaths {
		if err := os.WriteFile(torrentPath, []byte("exact torrent "+tracker), 0o600); err != nil {
			t.Fatalf("write %s torrent: %v", tracker, err)
		}
	}
	builder := workflowUploadPlanBuilder{
		resolver: &workflowDescriptionResolverFake{},
		trackers: service,
	}
	projections := api.TrackerReleaseProjectionSet{
		ReleaseRef: api.ReleaseRef{SourcePath: "C:\\media\\Example.Release.2026", Generation: 1},
		Projections: []api.TrackerReleaseProjection{
			{
				TrackerID:         "ALPHA",
				DisplayName:       "Alpha",
				UploadReleaseName: "Example.Release.2026.ALPHA-GRP",
				Readiness:         api.ReadinessStatusReady,
				UploadReady:       true,
			},
			{
				TrackerID:         "BETA",
				DisplayName:       "Beta",
				UploadReleaseName: "Example.Release.2026.BETA-GRP",
				Readiness:         api.ReadinessStatusReady,
				UploadReady:       true,
			},
		},
	}
	dupes := api.DupeAssessment{Results: []api.TrackerDupeAssessment{
		{
			TrackerID: "ALPHA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		},
		{
			TrackerID: "BETA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		},
	}}
	media := api.MediaArtifactSet{
		ID:                 "media-1",
		Revision:           1,
		CaptureFingerprint: workflowTestFingerprint(t, "media-with-image-host-failure"),
		Failures: []api.WorkflowFailure{{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureImageHostUnavailable,
				Operation: api.OperationKindImageHosting,
				Message:   "Required image host failed.",
				Recovery:  api.OperationRecoveryRetry,
			},
			TrackerID: "BETA",
			Resource:  "pixhost",
		}},
	}
	descriptions := api.DescriptionSet{
		ID:               "descriptions-1",
		Revision:         1,
		InputFingerprint: workflowTestFingerprint(t, "descriptions-after-image-host-failure"),
	}

	plan, execution, err := builder.Build(
		context.Background(),
		projections,
		dupes,
		workflowDupePrivateEvidence{},
		media,
		workflowMediaPrivateArtifacts{},
		descriptions,
		api.DescriptionInstructions{Options: api.UploadOptions{SkipAutoTorrent: true}},
		releaseworkflow.UploadPlanBuildOptions{},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build image-host-failed upload plan: %v", err)
	}
	defer func() { _ = execution.Release() }()
	if plan.Status != api.StageStatusReady || len(plan.Trackers) != 2 ||
		len(service.projections) != 1 || service.projections[0].TrackerID != "ALPHA" {
		t.Fatalf("image-host-failed upload plan=%#v retained=%#v", plan, service.projections)
	}
	if !plan.Trackers[0].Eligible || plan.Trackers[0].Status != api.StageStatusReady {
		t.Fatalf("successful sibling upload plan = %#v", plan.Trackers[0])
	}
	failed := plan.Trackers[1]
	if failed.TrackerID != "BETA" || failed.Eligible || failed.Status != api.StageStatusFailed ||
		len(failed.Failures) != 1 ||
		failed.Failures[0].Failure.Code != api.OperationFailureImageHostUnavailable {
		t.Fatalf("failed image-host upload plan = %#v", failed)
	}

	media.Failures = nil
	retryPlan, retryExecution, err := builder.Build(
		context.Background(),
		projections,
		dupes,
		workflowDupePrivateEvidence{},
		media,
		workflowMediaPrivateArtifacts{},
		descriptions,
		api.DescriptionInstructions{Options: api.UploadOptions{SkipAutoTorrent: true}},
		releaseworkflow.UploadPlanBuildOptions{},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build upload plan after image-host retry: %v", err)
	}
	defer func() { _ = retryExecution.Release() }()
	if retryPlan.Status != api.StageStatusReady || len(retryPlan.Trackers) != 2 ||
		!retryPlan.Trackers[0].Eligible || !retryPlan.Trackers[1].Eligible ||
		len(service.projections) != 2 {
		t.Fatalf("restored image-host upload plan=%#v retained=%#v", retryPlan, service.projections)
	}
}

func TestWorkflowUploadPlanStopsBeforePreparationWhenEveryTrackerWasSkipped(t *testing.T) {
	t.Parallel()

	resolver := &workflowDescriptionResolverFake{}
	service := &workflowRetainedUploadServiceFake{}
	clientService := &dryRunClientService{}
	builder := workflowUploadPlanBuilder{
		resolver: resolver,
		trackers: service,
		clients:  clientService,
	}
	projections := api.TrackerReleaseProjectionSet{
		ReleaseRef: api.ReleaseRef{SourcePath: "C:\\media\\Example.Release.2026", Generation: 1},
		Projections: []api.TrackerReleaseProjection{{
			TrackerID:         "AITHER",
			DisplayName:       "Aither",
			UploadReleaseName: "Example.Release.2026.AITHER-GRP",
			Readiness:         api.ReadinessStatusReady,
			UploadReady:       true,
		}},
	}
	dupes := api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
		TrackerID: "AITHER",
		Decision:  api.DupeDecisionAccepted,
		Status:    api.StageStatusCompleted,
	}}}
	media := api.MediaArtifactSet{
		CaptureFingerprint: workflowTestFingerprint(t, "all-skipped-media-capture"),
	}
	descriptions := api.DescriptionSet{
		InputFingerprint:    workflowTestFingerprint(t, "all-skipped-descriptions"),
		TemplateFingerprint: workflowTestFingerprint(t, "all-skipped-template"),
	}

	plan, execution, err := builder.Build(
		context.Background(),
		projections,
		dupes,
		workflowDupePrivateEvidence{},
		media,
		workflowMediaPrivateArtifacts{},
		descriptions,
		api.DescriptionInstructions{},
		releaseworkflow.UploadPlanBuildOptions{DryRun: true},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build all-skipped workflow upload plan: %v", err)
	}
	defer func() { _ = execution.Release() }()
	if plan.Status != api.StageStatusSkipped || len(plan.Trackers) != 0 {
		t.Fatalf("all-skipped upload plan = %#v", plan)
	}
	if len(service.projections) != 0 || len(clientService.injections) != 0 || resolver.input.Release.SourcePath != "" {
		t.Fatalf(
			"skipped tracker reached preparation: projections=%#v injections=%#v resolver=%#v",
			service.projections,
			clientService.injections,
			resolver.input,
		)
	}
}

func TestWorkflowUploadExecutionDoesNotRepeatSuccessfulDryRunInjection(t *testing.T) {
	t.Parallel()

	clientService := &dryRunClientService{}
	execution := &workflowUploadExecution{
		plan: &workflowRetainedUploadPlanFake{results: []trackers.RetainedTrackerResult{{
			Tracker: "ALPHA",
			Summary: api.UploadSummary{UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "ALPHA",
				TorrentPath: filepath.Join(t.TempDir(), "Example.Release.2026.ALPHA-GRP.torrent"),
			}}},
		}}},
		clients:        clientService,
		dryRunInjected: map[api.TrackerID]struct{}{"ALPHA": {}},
	}
	results, err := execution.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute workflow upload: %v", err)
	}
	if len(results) != 1 || !results[0].ClientInjected || len(clientService.injections) != 0 {
		t.Fatalf("upload execution results=%#v injections=%#v", results, clientService.injections)
	}
}

func TestWorkflowUploadExecutionInjectsExactRegisteredTrackerTorrent(t *testing.T) {
	t.Parallel()

	clientService := &dryRunClientService{}
	exactPath := filepath.Join(t.TempDir(), "prepared", "Example.Release.2026.ALPHA-GRP.torrent")
	remotePath := filepath.Join(t.TempDir(), "downloaded", "must-not-drive-injection.torrent")
	writeWorkflowRegisteredTorrent(t, exactPath, "prepared")
	writeWorkflowRegisteredTorrent(t, remotePath, "registered")
	execution := &workflowUploadExecution{
		plan: &workflowRetainedUploadPlanFake{results: []trackers.RetainedTrackerResult{{
			Tracker: "ALPHA",
			Summary: api.UploadSummary{
				UploadedTorrents: []api.UploadedTorrent{{
					Tracker:     "ALPHA",
					TorrentPath: remotePath,
				}},
			},
		}}},
		clients: clientService,
	}
	results, err := execution.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute workflow upload: %v", err)
	}
	if len(results) != 1 || !results[0].ClientInjected || len(clientService.injections) != 1 ||
		clientService.injections[0].Path != remotePath || clientService.injections[0].Path == exactPath {
		t.Fatalf("registered tracker injection results=%#v injections=%#v", results, clientService.injections)
	}
	authority := execution.RegisteredArtifactAuthority()
	if authority.Torrents["ALPHA"].Path != remotePath {
		t.Fatalf("registered artifact authority = %#v", authority)
	}
}

func TestWorkflowLiveReviewDefersInjectionUntilRegisteredTorrentExists(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	preparedPath := filepath.Join(tempDir, "prepared", "Example.Release.2026.ALPHA-GRP.torrent")
	registeredPath := filepath.Join(tempDir, "registered", "[alpha].Example.Release.2026.ALPHA-GRP.torrent")
	writeWorkflowRegisteredTorrent(t, preparedPath, "prepared")
	writeWorkflowRegisteredTorrent(t, registeredPath, "registered")

	clientService := &dryRunClientService{}
	service := &workflowRetainedUploadServiceFake{
		torrentPaths: map[api.TrackerID]string{"ALPHA": preparedPath},
		results: []trackers.RetainedTrackerResult{{
			Tracker: "ALPHA",
			Summary: api.UploadSummary{
				Uploaded: 1,
				UploadedTorrents: []api.UploadedTorrent{{
					Tracker:     "ALPHA",
					TorrentPath: registeredPath,
				}},
			},
		}},
	}
	builder := workflowUploadPlanBuilder{
		resolver: &workflowDescriptionResolverFake{},
		trackers: service,
		clients:  clientService,
	}
	projections := api.TrackerReleaseProjectionSet{
		ExecutionMode: api.WorkflowExecutionModeNormal,
		ReleaseRef: api.ReleaseRef{
			SourcePath: filepath.Join(tempDir, "Example.Release.2026"),
			Generation: 1,
		},
		Projections: []api.TrackerReleaseProjection{{
			TrackerID:         "ALPHA",
			DisplayName:       "Alpha",
			UploadReleaseName: "Example.Release.2026.ALPHA-GRP",
			Readiness:         api.ReadinessStatusReady,
			UploadReady:       true,
		}},
	}
	dupes := api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
		TrackerID: "ALPHA",
		Decision:  api.DupeDecisionNoMatch,
		Status:    api.StageStatusCompleted,
	}}}
	media := api.MediaArtifactSet{
		CaptureFingerprint: workflowTestFingerprint(t, "live-review-media"),
	}
	descriptions := api.DescriptionSet{
		InputFingerprint: workflowTestFingerprint(t, "live-review-descriptions"),
	}

	plan, execution, err := builder.Build(
		context.Background(),
		projections,
		dupes,
		workflowDupePrivateEvidence{},
		media,
		workflowMediaPrivateArtifacts{},
		descriptions,
		api.DescriptionInstructions{Options: api.UploadOptions{SkipAutoTorrent: true}},
		releaseworkflow.UploadPlanBuildOptions{DryRun: true},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build live upload review: %v", err)
	}
	defer func() { _ = execution.Release() }()
	if len(plan.Trackers) != 1 ||
		plan.Trackers[0].ClientInjectionStatus != api.StageStatusSkipped ||
		!strings.Contains(plan.Trackers[0].ClientInjectionMessage, "deferred") ||
		len(clientService.injections) != 0 {
		t.Fatalf("live review tracker=%#v injections=%#v", plan.Trackers, clientService.injections)
	}

	results, err := execution.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute live upload: %v", err)
	}
	if len(results) != 1 || !results[0].ClientInjected || len(clientService.injections) != 1 ||
		clientService.injections[0].Path != registeredPath ||
		clientService.injections[0].Path == preparedPath {
		t.Fatalf("live upload results=%#v injections=%#v", results, clientService.injections)
	}
}

func TestWorkflowUploadExecutionUsesRegisteredTorrentURLFallback(t *testing.T) {
	t.Parallel()

	clientService := &dryRunClientService{}
	execution := &workflowUploadExecution{
		plan: &workflowRetainedUploadPlanFake{results: []trackers.RetainedTrackerResult{{
			Tracker: "ALPHA",
			Summary: api.UploadSummary{UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "ALPHA",
				TorrentID:   "123",
				DownloadURL: "https://tracker.invalid/torrent/download/123",
			}}},
		}}},
		clients: clientService,
	}
	results, err := execution.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute workflow upload: %v", err)
	}
	if len(results) != 1 || !results[0].ClientInjected || len(clientService.injections) != 1 ||
		clientService.injections[0].Path != "" ||
		clientService.injections[0].URL != "https://tracker.invalid/torrent/download/123" {
		t.Fatalf("registered URL injection results=%#v injections=%#v", results, clientService.injections)
	}
}

func TestWorkflowUploadExecutionKeepsSubmissionSuccessWithoutExactArtifact(t *testing.T) {
	t.Parallel()

	clientService := &dryRunClientService{}
	execution := &workflowUploadExecution{
		plan: &workflowRetainedUploadPlanFake{results: []trackers.RetainedTrackerResult{{
			Tracker: "ALPHA",
			Summary: api.UploadSummary{UploadedTorrents: []api.UploadedTorrent{{
				Tracker:   "BETA",
				TorrentID: "123",
			}}},
		}}},
		clients: clientService,
	}
	results, err := execution.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute workflow upload: %v", err)
	}
	if len(results) != 1 || results[0].SubmissionStatus != api.StageStatusCompleted ||
		results[0].ClientInjectionStatus != api.StageStatusFailed ||
		results[0].ClientFailureCode != api.OperationFailureMissingExactTorrent ||
		results[0].Status != api.StageStatusPartial || len(clientService.injections) != 0 {
		t.Fatalf("artifact-less upload results=%#v injections=%#v", results, clientService.injections)
	}
}

func TestWorkflowUploadExecutionNoSeedSkipsRegisteredTorrentInjection(t *testing.T) {
	t.Parallel()

	clientService := &dryRunClientService{}
	execution := &workflowUploadExecution{
		plan: &workflowRetainedUploadPlanFake{results: []trackers.RetainedTrackerResult{{
			Tracker: "ALPHA",
			Summary: api.UploadSummary{UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "ALPHA",
				DownloadURL: "https://tracker.invalid/torrent/download/123",
			}}},
		}}},
		clients: clientService,
		noSeed:  true,
	}
	results, err := execution.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute workflow upload: %v", err)
	}
	if len(results) != 1 || results[0].SubmissionStatus != api.StageStatusCompleted ||
		results[0].ClientInjectionStatus != api.StageStatusSkipped ||
		results[0].Status != api.StageStatusCompleted || len(clientService.injections) != 0 {
		t.Fatalf("no-seed upload results=%#v injections=%#v", results, clientService.injections)
	}
}

func writeWorkflowRegisteredTorrent(t *testing.T, path string, source string) {
	t.Helper()
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name:        "Example.Release.2026",
		PieceLength: 16 * 1024,
		Pieces:      make([]byte, 20),
		Length:      1,
		Source:      source,
	})
	if err != nil {
		t.Fatalf("marshal test torrent info: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create test torrent directory: %v", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create test torrent: %v", err)
	}
	if err := (&metainfo.MetaInfo{InfoBytes: infoBytes}).Write(file); err != nil {
		_ = file.Close()
		t.Fatalf("write test torrent: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close test torrent: %v", err)
	}
}

func TestWorkflowTrackerArtifactIdentityIsTrackerScoped(t *testing.T) {
	t.Parallel()

	sharedPath := filepath.Join(t.TempDir(), "Example.Release.2026.shared.torrent")
	if err := os.WriteFile(sharedPath, []byte("shared exact torrent"), 0o600); err != nil {
		t.Fatalf("write shared torrent: %v", err)
	}
	planFingerprint := workflowTestFingerprint(t, "shared-plan")
	alphaOperation, alphaTorrent, alphaFingerprint, err := workflowTrackerArtifactIdentity("ALPHA", sharedPath, planFingerprint)
	if err != nil {
		t.Fatalf("identify alpha torrent: %v", err)
	}
	betaOperation, betaTorrent, betaFingerprint, err := workflowTrackerArtifactIdentity("BETA", sharedPath, planFingerprint)
	if err != nil {
		t.Fatalf("identify beta torrent: %v", err)
	}
	if alphaOperation == betaOperation || alphaTorrent == betaTorrent || alphaFingerprint != betaFingerprint {
		t.Fatalf(
			"tracker-scoped identities collided: alpha=%s/%s/%s beta=%s/%s/%s",
			alphaOperation,
			alphaTorrent,
			alphaFingerprint,
			betaOperation,
			betaTorrent,
			betaFingerprint,
		)
	}
}
