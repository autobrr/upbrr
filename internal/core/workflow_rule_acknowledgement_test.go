// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/oe"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/otw"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestContinueReleaseWorkflowAcknowledgesOTWRulesAfterSiblingDuplicateCheck(t *testing.T) {
	testContinueRuleAcknowledgementDuplicateScope(t, "OTW", otw.ValidationPolicy(), "genre")
}

func TestContinueReleaseWorkflowAcknowledgesOtherRulesAfterSiblingDuplicateCheck(t *testing.T) {
	testContinueRuleAcknowledgementDuplicateScope(t, "WARN", trackerspkg.ValidationPolicyBinding{
		ID: "warning-scope-test-v1",
		Check: func(context.Context, api.TrackerValidationSubject, api.Logger) ([]api.RuleFailure, error) {
			return []api.RuleFailure{
				trackerspkg.NewRuleFailure("group", "Confirm this release group", api.RuleDispositionWaivable),
				trackerspkg.NewRuleFailure("language", "Confirm this release language", api.RuleDispositionWaivable),
			}, nil
		},
	}, "group")
}

func testContinueRuleAcknowledgementDuplicateScope(t *testing.T, tracker api.TrackerID, validation trackerspkg.ValidationPolicyBinding, rule string) {
	t.Parallel()

	registry := trackerspkg.NewRegistry()
	for _, descriptor := range []trackerspkg.Descriptor{
		{
			Name:       string(tracker),
			Definition: workflowImageHostPolicyDefinition{name: string(tracker)},
			Validation: validation,
		},
		{Name: "OE", Definition: workflowImageHostPolicyDefinition{name: "OE", policy: oe.Profile().ImageHost}},
	} {
		if err := registry.RegisterDescriptor(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{
		ImageHosting:       config.ImageHostingConfig{Host1: "imgbb", Host2: "onlyimage"},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"OE": {ImageHost: "onlyimage"},
		}},
	}
	projector, err := trackerspkg.NewWorkflowProjector(registry, cfg, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	const releaseName = "Example.Release.2026.1080p.WEB-DL-GRP"
	var generation atomic.Uint64
	generation.Store(1)
	preparer := releaseworkflow.ReleasePreparerFunc{
		PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			if input.Force {
				generation.Add(1)
			}
			return api.PrepareResult{Release: api.PreparedRelease{
				Generation: api.PreparedGeneration(generation.Load()),
				Source:     api.SourceManifest{SourcePath: input.SourcePath},
				Naming:     api.NamingFacts{ReleaseName: releaseName},
			}}, nil
		},
		DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
			return api.PreparedReleaseDisplay{ReleaseName: releaseName}, nil
		},
		SubjectFunc: func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
			return api.UploadSubject{
				SourcePath:  input.Release.SourcePath,
				FileList:    []string{input.Release.SourcePath},
				ReleaseName: releaseName,
				Source:      "Web",
				Type:        "WEBDL",
				LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
					OriginalLanguage:      "English",
					TrackCoverageComplete: true,
					PrimaryAudioTrackID:   "main",
					Tracks: []api.MediaTrackFacts{{
						ID:        "main",
						Kind:      api.MediaTrackAudio,
						Role:      api.AudioRoleProgramme,
						Languages: []string{"English"},
					}},
				}),
				Identity: api.ExternalIdentity{
					SourcePath: input.Release.SourcePath,
					Generation: input.Release.Generation,
					Category:   api.CanonicalCategoryMovie,
					TMDBID:     1234567,
				},
				ProviderMetadata: api.SourceScopedMetadata{
					SourcePath: input.Release.SourcePath,
					Generation: input.Release.Generation,
					TMDB: &api.TMDBMetadata{
						TMDBID: 1234567,
						Title:  "Example Release",
						Year:   2026,
						Genres: "Drama",
					},
				},
				Release: api.ReleaseInfo{
					Title:      "Example Release",
					Year:       2026,
					Resolution: "1080p",
				},
			}, nil
		},
		DuplicateFunc: func(_ context.Context, input api.DuplicateCheckInput) (api.DuplicateSubject, error) {
			return api.DuplicateSubject{SourcePath: input.Release.SourcePath, ReleaseName: releaseName}, nil
		},
	}
	dupes := &workflowDupeServiceFake{results: []api.DupeCheckResult{
		{
			Tracker: string(tracker),
			Status:  "completed",
			Search:  api.DupeSearchEvidence{Complete: true},
		},
		{
			Tracker:     "OE",
			Status:      "completed",
			Search:      api.DupeSearchEvidence{Complete: true},
			HasDupes:    true,
			Evaluations: []api.DupeCandidateEvaluation{{Name: "Example.Release.2026.1080p.WEB-DL-GRP", Relation: api.DupeRelationSameSlot}},
		},
	}}
	module, err := releaseworkflow.New(
		releaseworkflow.NewMemoryRepository(),
		releaseworkflow.NewMemoryPrivateResourceStore(),
		preparer,
		releaseworkflow.WithTrackerProjectionBuilder(projector),
		releaseworkflow.WithTrackerPreflightBuilder(workflowPreflightBuilder{
			auth:     workflowPreflightAuthFake{},
			registry: registry,
			config:   cfg,
		}),
		releaseworkflow.WithDupeAssessmentBuilder(workflowDupeBuilder{service: dupes}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Shutdown(context.Background()) })
	core := &Core{workflow: module, logger: api.NopLogger{}}
	ctx := releaseworkflow.WithTrackerDecisionMode(t.Context(), releaseworkflow.TrackerDecisionModeWebUIControls)
	const owner = "rule-acknowledgement-owner"
	intent := api.WorkflowIntent{
		Preparation: &api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), releaseName+".mkv")},
		TrackerIDs:  []api.TrackerID{tracker, "OE"},
	}
	current, err := core.ContinueReleaseWorkflow(ctx, owner, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "open-rule-workflow",
		Goal:           api.WorkflowGoalDuplicatesDecided,
		Intent:         intent,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(key string, answers ...api.RequiredActionAnswer) api.ContinueReleaseWorkflowRequest {
		return api.ContinueReleaseWorkflowRequest{
			Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
			IdempotencyKey: key,
			Goal:           api.WorkflowGoalDuplicatesDecided,
			Intent:         intent,
			Answers:        answers,
		}
	}
	settle := func(key string, answers ...api.RequiredActionAnswer) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			previousRevision := current.Workflow.Revision
			current, err = core.ContinueReleaseWorkflow(ctx, owner, request(key, answers...))
			if err != nil {
				t.Fatalf("continue %s: %v (cause: %v)", key, err, errors.Unwrap(err))
			}
			for current.Operation != nil && (current.Operation.Status == api.StageStatusQueued ||
				current.Operation.Status == api.StageStatusRunning || current.Operation.Status == api.StageStatusPending ||
				current.Operation.Status == api.StageStatusReady) {
				if !time.Now().Before(deadline) {
					t.Fatalf("%s operation did not finish: %#v", key, current.Operation)
				}
				time.Sleep(25 * time.Millisecond)
				current, err = core.CurrentReleaseWorkflow(ctx, owner, current.Workflow.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			current, err = core.CurrentReleaseWorkflow(ctx, owner, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Operation != nil && current.Operation.Status == api.StageStatusFailed {
				t.Fatalf("%s operation failed: %#v", key, current.Operation)
			}
			if current.Workflow.Revision == previousRevision {
				return
			}
		}
		t.Fatalf("%s continuation did not settle: revision=%d operation=%#v", key, current.Workflow.Revision, current.Operation)
	}
	projection := func() api.TrackerReleaseProjection {
		t.Helper()
		if current.Projections != nil {
			for _, candidate := range current.Projections.Projections {
				if candidate.TrackerID == tracker {
					return candidate
				}
			}
		}
		t.Fatalf("missing OTW projection: %#v", current)
		return api.TrackerReleaseProjection{}
	}
	action := func(confirmed bool) api.RequiredAction {
		t.Helper()
		actions := current.Workflow.RequiredActions
		status := api.RequiredActionStatusPending
		if !confirmed {
			actions = projection().RequiredActions
			status = api.RequiredActionStatusResolved
		}
		for _, candidate := range actions {
			if candidate.Kind == api.RequiredActionAuthorizeRules && candidate.TrackerID == tracker && candidate.Status == status {
				return candidate
			}
		}
		t.Fatalf("missing OTW acknowledgement confirmed=%t: %#v", confirmed, actions)
		return api.RequiredAction{}
	}

	settle("check-sibling-duplicates")
	pending := action(true)
	if pending.WorkflowRevision >= current.Workflow.Revision {
		t.Fatalf("fixture did not retain an earlier action: action=%d workflow=%d", pending.WorkflowRevision, current.Workflow.Revision)
	}
	if current.Dupes == nil || !slices.ContainsFunc(current.Dupes.Results, func(result api.TrackerDupeAssessment) bool {
		return result.TrackerID == "OE" && result.Decision == api.DupeDecisionPending
	}) || len(dupes.projections) != 1 || dupes.projections[0].TrackerID != "OE" {
		t.Fatalf("eligible sibling did not finish its duplicate check: dupes=%#v checked=%#v", current.Dupes, dupes.projections)
	}
	if !slices.ContainsFunc(projection().PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == rule && decision.Disposition == api.RuleDispositionWaivable && decision.Blocking
	}) {
		t.Fatalf("OTW genre warning missing: %#v", projection())
	}
	checkImageHost := func(stage string) {
		t.Helper()
		t.Run(stage, func(t *testing.T) {
			checkRuleAcknowledgementImageHost(t, cfg, registry, current.Projections.Projections)
		})
	}
	checkImageHost("before-acknowledgement")
	intent = api.WorkflowIntent{Interaction: api.InteractionModeInteractive}
	stale := request("stale-acknowledgement", api.RequiredActionAnswer{
		ActionID:         pending.ID,
		WorkflowRevision: current.Workflow.Revision,
		Confirmed:        new(true),
	})
	stale.Authority.ExpectedRevision = pending.WorkflowRevision
	if _, err := core.ContinueReleaseWorkflow(ctx, owner, stale); !errors.Is(err, releaseworkflow.ErrRevisionConflict) {
		t.Fatalf("stale client revision error = %v", err)
	}

	staleAnswer := request("stale-answer-revision", api.RequiredActionAnswer{
		ActionID:         pending.ID,
		WorkflowRevision: pending.WorkflowRevision,
		Confirmed:        new(true),
	})
	if _, err := core.ContinueReleaseWorkflow(ctx, owner, staleAnswer); !errors.Is(err, releaseworkflow.ErrRevisionConflict) {
		t.Fatalf("stale answer revision error = %v", err)
	}

	// Existing duplicate-risk controls change retained decisions without searching.
	for _, decision := range []api.DupeDecision{api.DupeDecisionIgnored, api.DupeDecisionAccepted, api.DupeDecisionIgnored} {
		intent.DuplicateDecisions = map[api.TrackerID]api.DupeDecision{"OE": decision}
		settle("sibling-decision-" + string(decision))
		if len(dupes.projections) != 1 {
			t.Fatalf("duplicate decision reran searches: %v", dupes.projections)
		}
	}
	intent.DuplicateDecisions = nil
	var siblingEvidence api.TrackerDupeAssessment
	for _, result := range current.Dupes.Results {
		if result.TrackerID == "OE" {
			siblingEvidence = result
		}
	}
	otwChecks := 0
	checkDuplicateCalls := func() {
		t.Helper()
		counts := make(map[api.TrackerID]int)
		for _, checked := range dupes.projections {
			counts[checked.TrackerID]++
		}
		if counts["OE"] != 1 || counts[tracker] != otwChecks {
			t.Fatalf("duplicate service calls = %v, want OE=1 OTW=%d", counts, otwChecks)
		}
	}
	for _, confirmed := range []bool{true, false, true} {
		selectedAction := action(confirmed)
		key := "acknowledge-" + string(selectedAction.ID)
		settle(key, api.RequiredActionAnswer{
			ActionID:         selectedAction.ID,
			WorkflowRevision: current.Workflow.Revision,
			Confirmed:        &confirmed,
		})
		if confirmed {
			otwChecks++
		}
		checkDuplicateCalls()
		for _, result := range current.Dupes.Results {
			if result.TrackerID == "OE" && (result.Decision != api.DupeDecisionIgnored ||
				!result.CheckedAt.Equal(siblingEvidence.CheckedAt) || !result.FreshUntil.Equal(siblingEvidence.FreshUntil) ||
				result.EvidenceFingerprint != siblingEvidence.EvidenceFingerprint) {
				t.Fatalf("unaffected sibling evidence changed: before=%#v after=%#v", siblingEvidence, result)
			}
		}
		otwProjection := projection()
		if (otwProjection.RuleAuthorizationFingerprint != "") != confirmed || otwProjection.DupeReady != confirmed {
			t.Fatalf("OTW acknowledgement confirmed=%t: %#v", confirmed, otwProjection)
		}
		if confirmed && otwProjection.RuleAuthorizationFingerprint != otwProjection.WaivableRuleFingerprint {
			t.Fatalf("OTW acknowledgement lost exact warning authority: %#v", otwProjection)
		}
		if current.Dupes == nil || !slices.ContainsFunc(current.Dupes.Results, func(result api.TrackerDupeAssessment) bool {
			return result.TrackerID == tracker && ((confirmed && result.Decision == api.DupeDecisionNoMatch) ||
				(!confirmed && result.Decision == api.DupeDecisionSkipped))
		}) {
			t.Fatalf("OTW duplicate state after acknowledgement confirmed=%t: %#v", confirmed, current.Dupes)
		}
		checkImageHost(key)
		settle("repeat-" + key)
		checkDuplicateCalls()
		checkImageHost("repeat-" + key)
		if !confirmed {
			settle("obsolete-grant-after-revocation", api.RequiredActionAnswer{
				ActionID:         pending.ID,
				WorkflowRevision: current.Workflow.Revision,
				Confirmed:        new(true),
			})
			if projection().RuleAuthorizationFingerprint != "" {
				t.Fatal("obsolete action restored revoked rule authority")
			}
		}
	}

	previousGeneration := current.Release.Release.Generation
	obsoleteAction := action(false)
	intent.Preparation = &api.PrepareInput{SourcePath: current.Release.Release.Source.SourcePath, Force: true}
	intent.TrackerIDs = []api.TrackerID{tracker, "OE"}
	settle("new-prepared-generation")
	siblingChecks := 0
	for _, checked := range dupes.projections {
		if checked.TrackerID == "OE" {
			siblingChecks++
		}
	}
	if siblingChecks != 2 {
		t.Fatalf("new preparation did not refresh sibling evidence: calls=%d", siblingChecks)
	}
	if current.Release.Release.Generation <= previousGeneration || projection().RuleAuthorizationFingerprint != "" {
		t.Fatalf("new preparation retained old authority: generation=%d projection=%#v", current.Release.Release.Generation, projection())
	}
	settle("obsolete-grant-after-repreparation", api.RequiredActionAnswer{
		ActionID:         obsoleteAction.ID,
		WorkflowRevision: current.Workflow.Revision,
		Confirmed:        new(true),
	})
	if projection().RuleAuthorizationFingerprint != "" || action(true).ID == obsoleteAction.ID {
		t.Fatalf("obsolete generation action authorized current warnings: %#v", projection())
	}
}

func checkRuleAcknowledgementImageHost(
	t *testing.T,
	cfg config.Config,
	registry *trackerspkg.Registry,
	projections []api.TrackerReleaseProjection,
) {
	t.Helper()
	index := slices.IndexFunc(projections, func(projection api.TrackerReleaseProjection) bool { return projection.TrackerID == "OE" })
	if index < 0 || !projections[index].DupeReady || projections[index].Artifacts.ScreenshotCount != 1 {
		t.Fatalf("OE sibling lost readiness or screenshot requirements: %#v", projections)
	}
	projections = projections[index : index+1]
	builder := workflowMediaBuilder{config: cfg, media: &mediaModule{
		cfg:      cfg,
		registry: registry,
		logger:   api.NopLogger{},
	}}
	for _, test := range []struct {
		name                string
		cachedHosts         []string
		extraDefaultImage   bool
		failPreferredUpload bool
		failedHosts         []string
		wantHost            string
		wantPrepared        bool
	}{
		{
			name:        "default-only",
			cachedHosts: []string{"imgbb"},
			wantHost:    "onlyimage",
		},
		{
			name:         "both-hosts-default-first",
			cachedHosts:  []string{"imgbb", "onlyimage"},
			wantHost:     "onlyimage",
			wantPrepared: true,
		},
		{
			name:              "preferred-cache-misses-selected-image",
			cachedHosts:       []string{"onlyimage", "imgbb"},
			extraDefaultImage: true,
			wantHost:          "onlyimage",
		},
		{
			name:                "preferred-upload-fails",
			cachedHosts:         []string{"imgbb"},
			failPreferredUpload: true,
			wantHost:            "onlyimage",
		},
		{
			name:         "recorded-preferred-host-failure",
			cachedHosts:  []string{"imgbb"},
			failedHosts:  []string{"onlyimage"},
			wantHost:     "imgbb",
			wantPrepared: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			const sourceID api.PublicResourceID = "selected-screenshot"
			pathValue := filepath.Join(t.TempDir(), "selected.png")
			snapshot := api.MediaArtifactSet{
				FailedHosts: test.failedHosts,
				Artifacts: []api.MediaArtifact{{
					ID:       sourceID,
					Kind:     api.MediaArtifactScreenshot,
					Selected: true,
				}},
			}
			retained := workflowMediaPrivateArtifacts{
				ArtifactImages: map[api.PublicResourceID]api.ScreenshotImage{sourceID: {Path: pathValue, Purpose: api.ScreenshotPurposeFinal}},
				HostedImages:   make(map[api.PublicResourceID]api.UploadedImageLink),
				HostedSources:  make(map[api.PublicResourceID]api.PublicResourceID),
			}
			selected := map[api.PublicResourceID]struct{}{sourceID: {}}
			for _, host := range test.cachedHosts {
				accountScope, err := workflowMediaHostAccountScope(cfg, host)
				if err != nil {
					t.Fatal(err)
				}
				hostedID := api.PublicResourceID("hosted-" + host)
				url := "https://" + host + ".example.invalid/selected.png"
				snapshot.Artifacts = append(snapshot.Artifacts, api.MediaArtifact{
					ID:       hostedID,
					Kind:     api.MediaArtifactHostedImage,
					Selected: true,
					Source:   string(sourceID),
					Host:     host,
					URL:      url,
				})
				retained.HostedSources[hostedID] = sourceID
				retained.HostedImages[hostedID] = api.UploadedImageLink{
					ImagePath:    pathValue,
					Host:         host,
					UsageScope:   "global",
					AccountScope: accountScope,
					RawURL:       url,
				}
			}
			if test.extraDefaultImage {
				const extraID api.PublicResourceID = "extra-screenshot"
				const hostedID api.PublicResourceID = "extra-imgbb"
				extraPath := filepath.Join(t.TempDir(), "extra.png")
				link := retained.HostedImages["hosted-imgbb"]
				link.ImagePath = extraPath
				link.RawURL = "https://imgbb.example.invalid/extra.png"
				retained.ArtifactImages[extraID] = api.ScreenshotImage{Path: extraPath, Purpose: api.ScreenshotPurposeFinal}
				retained.HostedSources[hostedID] = extraID
				retained.HostedImages[hostedID] = link
				snapshot.Artifacts = append(snapshot.Artifacts,
					api.MediaArtifact{
						ID:       extraID,
						Kind:     api.MediaArtifactScreenshot,
						Selected: true,
					},
					api.MediaArtifact{
						ID:       hostedID,
						Kind:     api.MediaArtifactHostedImage,
						Selected: true,
						Source:   string(extraID),
						Host:     "imgbb",
						URL:      link.RawURL,
					},
				)
				selected[extraID] = struct{}{}
			}
			subject := api.UploadSubject{}
			targets, err := builder.media.resolveImageUploadTargets(t.Context(), []string{"OE"}, subject, "", test.failedHosts)
			if err != nil || len(targets) != 1 || targets[0].Host != test.wantHost {
				t.Fatalf("configured upload target = %#v, error=%v", targets, err)
			}
			targets, err = builder.preferReusableImageTargets(snapshot, retained, selected,
				projections, subject, test.failedHosts, targets)
			if err != nil || len(targets) != 1 || targets[0].Host != test.wantHost || targets[0].ReuseOnly != test.wantPrepared {
				t.Errorf("cached image selection changed OE configured target: targets=%#v error=%v", targets, err)
			}
			attempts, prepared := builder.restoredHostedImageAttemptsForSubject(t.Context(), snapshot, retained, projections, subject)
			if prepared != test.wantPrepared || (prepared && (len(attempts) != 1 || attempts[0].Host != test.wantHost)) {
				t.Errorf("restored image coverage changed OE configured target: attempts=%#v prepared=%t", attempts, prepared)
			}
			images := make([]api.ScreenshotImage, 0, len(selected))
			sourceByPath := make(map[string]api.PublicResourceID)
			sourcePaths := make(map[api.PublicResourceID]string)
			for id := range selected {
				image := retained.ArtifactImages[id]
				images = append(images, image)
				sourceByPath[strings.ToLower(normalizedUploadImagePath(image.Path))] = id
				sourcePaths[id] = image.Path
			}
			links, blocked, _ := builder.retainedHostedImageLinks(snapshot, retained, selected, sourceByPath, sourcePaths, targets)
			builder.media.images = &countingWorkflowImageHost{}
			wantUploadedHost, wantAttempts := test.wantHost, 1
			if test.failPreferredUpload {
				builder.media.images = &partialImageHostingService{failHost: "onlyimage"}
				wantUploadedHost, wantAttempts = "imgbb", 2
			}
			result, err := builder.media.uploadImagesToTargetsWithFallback(t.Context(), subject, "", test.failedHosts, targets, images, links, blocked)
			if err != nil || len(result.Attempts) != wantAttempts {
				t.Fatalf("image upload attempts: result=%#v error=%v", result, err)
			}
			if test.failPreferredUpload && (result.Attempts[0].Host != "onlyimage" || result.Attempts[0].Failure == nil) {
				t.Errorf("configured host was not attempted before fallback: %#v", result.Attempts)
			}
			last := result.Attempts[len(result.Attempts)-1]
			if last.Host != wantUploadedHost || len(last.Links) != len(images) || len(result.Failures) != 0 {
				t.Errorf("image upload changed OE configured target or dropped selected images: result=%#v error=%v", result, err)
			}
		})
	}
}
