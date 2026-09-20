// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type workflowMediaResolverFake struct {
	screenshotSubject *api.ScreenshotSubject
}

type workflowMediaRepositoryFake struct{ mediaRepository }

type workflowMediaReuseLookupGuard struct {
	api.MediaReuseRepository
	calls int
}

func (r *workflowMediaReuseLookupGuard) LoadReusableMediaAssets(
	context.Context,
	api.MediaCompatibilityKey,
) ([]api.ReusableMediaAsset, error) {
	r.calls++
	return nil, errors.New("rebind must not load reusable media")
}

type reusableWorkflowMediaRepository struct {
	mediaRepository
	assets           []api.ReusableMediaAsset
	saved            []api.ReusableMediaAsset
	commits          map[api.ReusableMediaCommit]bool
	persistStarted   chan<- struct{}
	allowPersistence <-chan struct{}
}

func (r *reusableWorkflowMediaRepository) CommitReusableMedia(
	_ context.Context,
	_ api.PreparedMediaBinding,
	_ api.MediaCompatibilityKey,
	assets []api.ReusableMediaAsset,
	commit api.ReusableMediaCommit,
) error {
	if !commit.Valid() {
		return internalerrors.ErrInvalidInput
	}
	r.saved = append([]api.ReusableMediaAsset(nil), assets...)
	if r.commits == nil {
		r.commits = make(map[api.ReusableMediaCommit]bool)
	}
	r.commits[commit] = true
	if r.persistStarted != nil {
		r.persistStarted <- struct{}{}
		<-r.allowPersistence
	}
	return nil
}

func (r *reusableWorkflowMediaRepository) HasReusableMediaCommit(
	_ context.Context,
	commit api.ReusableMediaCommit,
) (bool, error) {
	return r.commits[commit], nil
}

func (r *reusableWorkflowMediaRepository) LoadReusableMediaAssets(
	context.Context,
	api.MediaCompatibilityKey,
) ([]api.ReusableMediaAsset, error) {
	return append([]api.ReusableMediaAsset(nil), r.assets...), nil
}

func (*reusableWorkflowMediaRepository) DeleteReusableMediaAssets(context.Context, api.PreparedMediaBinding, []string) error {
	return nil
}

func (f workflowMediaResolverFake) ResolveScreenshotSubject(
	_ context.Context,
	input api.MediaPlanInput,
) (api.ScreenshotSubject, error) {
	if f.screenshotSubject != nil {
		subject := *f.screenshotSubject
		subject.ManualFrames = append([]int(nil), input.Options.ManualFrames...)
		return subject, nil
	}
	return api.ScreenshotSubject{
		SourcePath:   input.Release.SourcePath,
		DiscType:     "DVD",
		ManualFrames: append([]int(nil), input.Options.ManualFrames...),
	}, nil
}

func TestWorkflowMediaRestoreCompatibleWithoutReuseCapabilityReturnsNoMedia(t *testing.T) {
	t.Parallel()

	builder := workflowMediaBuilder{
		resolver: workflowMediaResolverFake{},
		media:    &mediaModule{repo: workflowMediaRepositoryFake{}},
	}
	snapshot, retained, err := builder.RestoreCompatible(
		t.Context(),
		api.ReleaseRef{SourcePath: "C:\\releases\\Example.Release.2026.mkv"},
		api.TrackerReleaseProjectionSet{},
		nil,
		nil,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("restore without media-reuse capability: %v", err)
	}
	if len(snapshot.Artifacts) != 0 || snapshot.ID != "" || retained != nil {
		t.Fatalf("restore without media-reuse capability = %#v, %#v", snapshot, retained)
	}
}

func TestWorkflowMediaModuleKeepsReusableMediaCapabilityOutsideRepositoryView(t *testing.T) {
	t.Parallel()

	repository, err := db.Open(filepath.Join(t.TempDir(), "workflow-media.sqlite"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	capabilities := api.RepositoryCapabilitiesFrom(repository)
	module := newMediaModule(
		config.Config{},
		api.NopLogger{},
		api.ServiceSet{},
		mediaRepositoryView{TrackerStateRepository: capabilities.Trackers(), MediaAssetRepository: capabilities.Media()},
		capabilities.MediaReuse(),
		nil,
		nil,
	)
	if module.mediaReuse == nil {
		t.Fatal("workflow media module lost reusable-media repository capability")
	}
}

func TestWorkflowMediaRestoreCompatibleRebuildsCurrentArtifacts(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	imagePath := filepath.Join(baseDir, "screen.png")
	if err := os.WriteFile(imagePath, []byte("restorable image"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	compatibilityKey := api.MediaCompatibilityKey(workflowMediaTestSHA256("verified source"))
	currentBinding := api.PreparedMediaBinding{
		SourcePath:               "C:\\releases\\Example.Release.2026.mkv",
		PreparedMediaFingerprint: "current-prepared-media",
		PreparedGeneration:       7,
		CompatibilityKey:         compatibilityKey,
	}
	image := api.ScreenshotImage{
		DiscID:           "disc-a",
		Index:            1,
		TimestampSeconds: 90,
		Path:             imagePath,
		Purpose:          api.ScreenshotPurposeFinal,
		Width:            1920,
		Height:           1080,
		SizeBytes:        int64(len("restorable image")),
	}
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(baseDir, "workflow.db")},
		ImageHosting: config.ImageHostingConfig{Host1: "pixhost"},
	}
	captureFingerprint, err := workflowMediaReuseCaptureFingerprint(cfg, compatibilityKey, api.MediaArtifactScreenshot, image)
	if err != nil {
		t.Fatalf("capture fingerprint: %v", err)
	}
	accountScope, err := workflowMediaHostAccountScope(cfg, "pixhost")
	if err != nil {
		t.Fatalf("host account scope: %v", err)
	}
	repository := &reusableWorkflowMediaRepository{assets: []api.ReusableMediaAsset{{
		Binding: api.PreparedMediaBinding{
			SourcePath:               currentBinding.SourcePath,
			PreparedMediaFingerprint: "old-prepared-media",
			PreparedGeneration:       2,
		},
		CompatibilityKey:   compatibilityKey,
		CaptureFingerprint: captureFingerprint,
		ContentSHA256:      workflowMediaTestSHA256("restorable image"),
		Kind:               api.MediaArtifactScreenshot,
		Image:              image,
		Selected:           true,
		Order:              3,
		HostedLinks: []api.UploadedImageLink{{
			Host:         "pixhost",
			UsageScope:   "global",
			AccountScope: accountScope,
			ImgURL:       "https://images.invalid/screen.png",
			SizeBytes:    image.SizeBytes,
			UploadedAt:   time.Now().UTC(),
		}},
	}}}
	builder := workflowMediaBuilder{
		config: cfg,
		resolver: workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{
			MediaBinding: currentBinding,
			SourcePath:   currentBinding.SourcePath,
			Discs:        []api.ScreenshotDiscSubject{{ID: "disc-a", Name: "Disc A"}},
		}},
		media: &mediaModule{
			cfg:        cfg,
			repo:       repository,
			mediaReuse: repository,
			registry:   mediaImageHostRegistry(t),
		},
	}
	snapshot, retainedResource, err := builder.RestoreCompatible(t.Context(), api.ReleaseRef{SourcePath: currentBinding.SourcePath, Generation: 7},
		api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "ONE", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1}}}}, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("restore compatible media: %v", err)
	}
	if len(snapshot.Artifacts) != 2 || snapshot.Artifacts[0].Kind != api.MediaArtifactScreenshot ||
		snapshot.Artifacts[0].Order != 3 || snapshot.Artifacts[1].Kind != api.MediaArtifactHostedImage ||
		snapshot.Artifacts[1].Source != string(snapshot.Artifacts[0].ID) {
		t.Fatalf("restored artifacts = %#v", snapshot.Artifacts)
	}
	if len(repository.saved) != 0 {
		t.Fatalf("restored uncommitted association = %#v", repository.saved)
	}
	snapshot.WorkflowID = "workflow-restored"
	snapshot.ID = "media-restored"
	snapshot.Revision = 1
	if err := builder.RecordReusableMedia(t.Context(), snapshot, retainedResource); err != nil {
		t.Fatalf("record restored media: %v", err)
	}
	if len(repository.saved) != 1 || repository.saved[0].Binding.PreparedGeneration != currentBinding.PreparedGeneration {
		t.Fatalf("recorded restored association = %#v", repository.saved)
	}
	retained, ok := retainedResource.(workflowMediaPrivateArtifacts)
	if !ok || retained.screenshotSubject.MediaBinding.PreparedGeneration != currentBinding.PreparedGeneration {
		t.Fatalf("restored private media = %#v", retainedResource)
	}
	if !strings.HasPrefix(filepath.Base(filepath.Dir(repository.saved[0].Image.Path)), "@reused-") {
		t.Fatalf("restored image path = %q, want dedicated reusable root", repository.saved[0].Image.Path)
	}
	hosted := retained.HostedImages[snapshot.Artifacts[1].ID]
	if hosted.ImagePath != retained.Screenshots[0].Path {
		t.Fatalf("restored hosted image path = %q, want materialized %q", hosted.ImagePath, retained.Screenshots[0].Path)
	}
}

func TestWorkflowMediaRestoreCompatibleRebindsRetainedSnapshotWithoutCacheLookup(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	if err := os.WriteFile(sourcePath, []byte("verified source bytes"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	repository, err := db.Open(filepath.Join(t.TempDir(), "workflow-media.sqlite"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	prepared, err := preparedrelease.New(repository, workflowMediaIdentityResolver{}, workflowMediaCollector{})
	if err != nil {
		t.Fatalf("create prepared release module: %v", err)
	}
	verified, err := preparedrelease.VerifyInputSource(ctx, api.PrepareInput{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("verify source: %v", err)
	}
	preparedResult, err := prepared.Prepare(ctx, api.PrepareInput{SourcePath: sourcePath, VerifiedSource: &verified})
	if err != nil {
		t.Fatalf("prepare source: %v", err)
	}
	release := api.ReleaseRef{SourcePath: sourcePath, Generation: preparedResult.Release.Generation}

	registry := mediaImageHostRegistry(t)
	if err := registry.RegisterDescriptor(trackers.Descriptor{
		Name:              "THREE",
		Definition:        mediaImageHostDefinition("THREE"),
		Family:            trackers.FamilyStandalone,
		BaseURL:           "https://three.example.invalid",
		UploadContentMode: trackers.UploadContentModeScreenshots,
		ImageHost:         &trackers.ImageHostPolicy{AllowedHosts: []string{"pixhost"}},
	}); err != nil {
		t.Fatalf("register third tracker: %v", err)
	}
	cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "pixhost"}}
	accountScope, err := workflowMediaHostAccountScope(cfg, "pixhost")
	if err != nil {
		t.Fatalf("host account scope: %v", err)
	}
	guard := &workflowMediaReuseLookupGuard{}
	builder := workflowMediaBuilder{
		config: cfg,
		media: &mediaModule{
			cfg:           cfg,
			mediaReuse:    guard,
			registry:      registry,
			preparedFacts: prepared,
		},
	}
	missingPath := filepath.Join(t.TempDir(), "missing-retained-screen.png")
	selected := api.MediaArtifact{
		ID:       "screen-selected",
		Kind:     api.MediaArtifactScreenshot,
		Purpose:  api.ScreenshotPurposeFinal,
		Selected: true,
		Order:    8,
	}
	hosted := api.MediaArtifact{
		ID:       "hosted-selected",
		Kind:     api.MediaArtifactHostedImage,
		Purpose:  api.ScreenshotPurposeFinal,
		Selected: true,
		Order:    8,
		Source:   string(selected.ID),
		Host:     "pixhost",
		URL:      "https://images.invalid/retained.png",
	}
	deselected := api.MediaArtifact{
		ID:       "screen-deselected",
		Kind:     api.MediaArtifactScreenshot,
		Purpose:  api.ScreenshotPurposeFinal,
		Selected: false,
		Order:    1,
	}
	completedCoverage := api.HostedImageAttempt{
		ID:          "historic-host",
		Media:       api.MediaArtifactSetRef{ID: "prior-media", Revision: 4},
		Host:        "pixhost",
		UsageScope:  "global",
		TrackerIDs:  []api.TrackerID{"ONE"},
		Status:      api.StageStatusCompleted,
		ArtifactIDs: []api.PublicResourceID{selected.ID},
		Results:     []api.MediaArtifact{hosted},
	}
	failure := hostedImageFailure("ONE", "old-host", "previous host failure")
	historicalAttempt := api.HostedImageAttempt{
		ID:         "historic-failure",
		Media:      api.MediaArtifactSetRef{ID: "prior-media", Revision: 4},
		Host:       "old-host",
		UsageScope: "tracker:ONE",
		TrackerIDs: []api.TrackerID{"ONE"},
		Status:     api.StageStatusFailed,
		Failures:   []api.WorkflowFailure{failure},
	}
	reconcile := api.RequiredAction{Kind: api.RequiredActionReconcileSubmission, EffectKind: api.WorkflowExternalEffectImageHosting}
	existing := api.MediaArtifactSet{
		CaptureFingerprint:        workflowTestFingerprint(t, "retained-rebind"),
		RequirementsFingerprint:   workflowTestFingerprint(t, "prior-requirements"),
		Artifacts:                 []api.MediaArtifact{selected, hosted, deselected},
		HostAttempts:              []api.HostedImageAttempt{completedCoverage, historicalAttempt},
		FailedHosts:               []string{"old-host"},
		ImageRequirementsPrepared: true,
		Status:                    api.StageStatusBlocked,
		RequiredActions:           []api.RequiredAction{reconcile},
		Failures:                  []api.WorkflowFailure{failure},
	}
	retained := workflowMediaPrivateArtifacts{
		Screenshots: []api.ScreenshotImage{
			{Path: missingPath, Purpose: api.ScreenshotPurposeFinal},
			{Path: filepath.Join(t.TempDir(), "also-missing-retained-screen.png"), Purpose: api.ScreenshotPurposeFinal},
		},
		ArtifactImages: map[api.PublicResourceID]api.ScreenshotImage{
			selected.ID:   {Path: missingPath, Purpose: api.ScreenshotPurposeFinal},
			deselected.ID: {Path: filepath.Join(t.TempDir(), "also-missing-retained-screen.png"), Purpose: api.ScreenshotPurposeFinal},
		},
		HostedImages: map[api.PublicResourceID]api.UploadedImageLink{
			hosted.ID: {
				Host:         "pixhost",
				UsageScope:   "global",
				AccountScope: accountScope,
				RawURL:       hosted.URL,
			},
		},
		HostedSources: map[api.PublicResourceID]api.PublicResourceID{hosted.ID: selected.ID},
	}
	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
		{TrackerID: "ONE", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1}},
		{TrackerID: "THREE", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1}},
	}}

	for _, testCase := range []struct {
		name                    string
		usageScope              string
		wantPreservedAttempts   int
		wantSynthesizedAttempts int
	}{
		{
name: "global link covers added tracker",
 usageScope: "global",
 wantPreservedAttempts: 1,
 wantSynthesizedAttempts: 2,
},
		{
name: "tracker scoped link does not widen to added tracker",
 usageScope: "tracker:ONE",
 wantPreservedAttempts: 2,
},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			private := cloneWorkflowMediaPrivateArtifacts(retained)
			link := private.HostedImages[hosted.ID]
			link.UsageScope = testCase.usageScope
			private.HostedImages[hosted.ID] = link

			rebound, reboundResource, restoreErr := builder.RestoreCompatible(ctx, release, projections, &existing, private, time.Now())
			if restoreErr != nil {
				t.Fatalf("rebind retained media: %v", restoreErr)
			}
			if !reflect.DeepEqual(rebound.Artifacts, existing.Artifacts) || rebound.Artifacts[2].Selected ||
				rebound.Artifacts[0].Order != 8 || rebound.Artifacts[2].Order != 1 {
				t.Fatalf("rebind changed retained selection/order/artifacts: %#v", rebound.Artifacts)
			}
			requirements, fingerprintErr := workflowMediaRequirementsFingerprint(projections.Projections)
			if fingerprintErr != nil || rebound.RequirementsFingerprint != requirements {
				t.Fatalf("rebound requirements fingerprint = %q, %v; want %q", rebound.RequirementsFingerprint, fingerprintErr, requirements)
			}
			if !rebound.ImageRequirementsPrepared || !reflect.DeepEqual(rebound.FailedHosts, existing.FailedHosts) ||
				!reflect.DeepEqual(rebound.Failures, existing.Failures) || !reflect.DeepEqual(rebound.RequiredActions, existing.RequiredActions) {
				t.Fatalf("rebind changed retained host readiness/failures/actions: %#v", rebound)
			}
			if len(rebound.HostAttempts) != testCase.wantPreservedAttempts+testCase.wantSynthesizedAttempts {
				t.Fatalf("rebind host attempts = %#v", rebound.HostAttempts)
			}
			if testCase.wantSynthesizedAttempts == 0 {
				if !reflect.DeepEqual(rebound.HostAttempts, existing.HostAttempts) {
					t.Fatalf("tracker-scoped retained link widened into current coverage: %#v", rebound.HostAttempts)
				}
			} else if !reflect.DeepEqual(rebound.HostAttempts[0], historicalAttempt) {
				t.Fatalf("rebind discarded failed historical attempt: %#v", rebound.HostAttempts)
			}
			attemptIDs := make(map[api.PublicResourceID]struct{}, len(rebound.HostAttempts))
			for _, attempt := range rebound.HostAttempts {
				if _, exists := attemptIDs[attempt.ID]; exists {
					t.Fatalf("rebind retained duplicate host attempt %q: %#v", attempt.ID, rebound.HostAttempts)
				}
				attemptIDs[attempt.ID] = struct{}{}
			}
			for _, attempt := range rebound.HostAttempts[testCase.wantPreservedAttempts:] {
				if attempt.Media != (api.MediaArtifactSetRef{}) || attempt.UsageScope != "global" ||
					len(attempt.TrackerIDs) != 1 || len(attempt.Results) != 1 || attempt.Results[0].ID != hosted.ID {
					t.Fatalf("rebound synthetic host coverage = %#v", attempt)
				}
			}
			if testCase.wantSynthesizedAttempts > 0 {
				repeated, _, repeatErr := builder.RestoreCompatible(ctx, release, projections, &existing, private, time.Now())
				if repeatErr != nil || !reflect.DeepEqual(repeated.HostAttempts, rebound.HostAttempts) {
					t.Fatalf("repeated rebind host coverage = %#v, %v; want %#v", repeated.HostAttempts, repeatErr, rebound.HostAttempts)
				}
			}
			updatedPrivate, ok := reboundResource.(workflowMediaPrivateArtifacts)
			if !ok || updatedPrivate.ArtifactImages[selected.ID].Path != missingPath {
				t.Fatalf("rebound retained resource = %#v", reboundResource)
			}
			updatedPrivate.ArtifactImages[selected.ID] = api.ScreenshotImage{Path: "changed"}
			if private.ArtifactImages[selected.ID].Path != missingPath {
				t.Fatal("rebind shared retained private media")
			}
		})
	}
	if guard.calls != 0 {
		t.Fatalf("rebind loaded reusable media %d times", guard.calls)
	}
	if _, err := os.Stat(missingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retained local media unexpectedly available: %v", err)
	}
}

func TestWorkflowMediaRestoreCompatibleHostedLinksRequireCurrentCompleteCoverage(t *testing.T) {
	t.Parallel()

	cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "pixhost", Host2: "imgbb"}}
	accountScope, err := workflowMediaHostAccountScope(cfg, "pixhost")
	if err != nil {
		t.Fatalf("host account scope: %v", err)
	}
	fallbackAccountScope, err := workflowMediaHostAccountScope(cfg, "imgbb")
	if err != nil {
		t.Fatalf("fallback host account scope: %v", err)
	}
	network := &countingWorkflowImageHost{}
	builder := workflowMediaBuilder{
		config: cfg,
		media: &mediaModule{
			cfg:      cfg,
			images:   network,
			registry: mediaImageHostRegistry(t),
		},
	}
	snapshot := api.MediaArtifactSet{
		CaptureFingerprint: workflowTestFingerprint(t, "restored-hosted-coverage"),
		Artifacts: []api.MediaArtifact{
			{
				ID:       "screen-selected",
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
				Order:    4,
			},
			{
				ID:       "screen-deselected",
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: false,
				Order:    1,
			},
			{
				ID:       "hosted-selected",
				Kind:     api.MediaArtifactHostedImage,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
				Order:    4,
				Source:   "screen-selected",
				Host:     "pixhost",
				URL:      "https://images.invalid/screen-selected.png",
			},
		},
	}
	retained := workflowMediaPrivateArtifacts{
		HostedImages: map[api.PublicResourceID]api.UploadedImageLink{
			"hosted-selected": {
				Host:         "pixhost",
				UsageScope:   "global",
				AccountScope: accountScope,
				RawURL:       "https://images.invalid/screen-selected.png",
			},
		},
		HostedSources: map[api.PublicResourceID]api.PublicResourceID{"hosted-selected": "screen-selected"},
	}
	projections := []api.TrackerReleaseProjection{{
		TrackerID: "ONE",
		Artifacts: api.TrackerArtifactRequirements{
			ScreenshotCount: 1,
		},
	}}
	attempts, prepared := builder.restoredHostedImageAttemptsForSubject(snapshot, retained, projections, api.UploadSubject{})
	if !prepared || len(attempts) != 1 || attempts[0].Host != "pixhost" || attempts[0].UsageScope != "global" ||
		!slices.Equal(attempts[0].ArtifactIDs, []api.PublicResourceID{"screen-selected"}) ||
		len(attempts[0].Results) != 1 || attempts[0].Results[0].ID != "hosted-selected" {
		t.Fatalf("restored hosted attempts = %#v, prepared=%t", attempts, prepared)
	}
	if network.uploads != 0 {
		t.Fatalf("restored coverage called image host %d times", network.uploads)
	}
	if snapshot.Artifacts[1].Selected || snapshot.Artifacts[1].Order != 1 || snapshot.Artifacts[0].Order != 4 {
		t.Fatalf("restored coverage changed selections or order: %#v", snapshot.Artifacts)
	}
	fallbackSnapshot := snapshot
	fallbackSnapshot.Artifacts = slices.Clone(snapshot.Artifacts)
	fallbackSnapshot.Artifacts[2].Host = "imgbb"
	fallbackSnapshot.Artifacts[2].URL = "https://images.invalid/fallback-screen-selected.png"
	fallbackRetained := workflowMediaPrivateArtifacts{
		HostedImages:  maps.Clone(retained.HostedImages),
		HostedSources: maps.Clone(retained.HostedSources),
	}
	fallbackLink := fallbackRetained.HostedImages["hosted-selected"]
	fallbackLink.Host = "imgbb"
	fallbackLink.AccountScope = fallbackAccountScope
	fallbackLink.RawURL = fallbackSnapshot.Artifacts[2].URL
	fallbackRetained.HostedImages["hosted-selected"] = fallbackLink
	attempts, prepared = builder.restoredHostedImageAttemptsForSubject(fallbackSnapshot, fallbackRetained, projections, api.UploadSubject{})
	if !prepared || len(attempts) != 1 || attempts[0].Host != "imgbb" || len(attempts[0].Results) != 1 ||
		attempts[0].Results[0].ID != "hosted-selected" {
		t.Fatalf("current fallback host did not restore cached coverage: %#v, prepared=%t", attempts, prepared)
	}
	if network.uploads != 0 {
		t.Fatalf("fallback coverage called image host %d times", network.uploads)
	}

	removedFallback := cfg
	removedFallback.ImageHosting.Host2 = ""
	builder.config = removedFallback
	builder.media.cfg = removedFallback
	if attempts, prepared := builder.restoredHostedImageAttemptsForSubject(fallbackSnapshot, fallbackRetained, projections, api.UploadSubject{}); prepared || len(attempts) != 0 {
		t.Fatalf("removed fallback host accepted cached link: %#v, prepared=%t", attempts, prepared)
	}

	builder.config = cfg
	builder.media.cfg = cfg
	scopeMismatchRetained := workflowMediaPrivateArtifacts{
		HostedImages:  maps.Clone(fallbackRetained.HostedImages),
		HostedSources: maps.Clone(fallbackRetained.HostedSources),
	}
	scopeMismatchLink := scopeMismatchRetained.HostedImages["hosted-selected"]
	scopeMismatchLink.UsageScope = "tracker:ONE"
	scopeMismatchRetained.HostedImages["hosted-selected"] = scopeMismatchLink
	if attempts, prepared := builder.restoredHostedImageAttemptsForSubject(fallbackSnapshot, scopeMismatchRetained, projections, api.UploadSubject{}); prepared || len(attempts) != 0 {
		t.Fatalf("changed fallback scope accepted cached link: %#v, prepared=%t", attempts, prepared)
	}

	changedFallbackAccount := cfg
	changedFallbackAccount.ImageHosting.Host3 = "sharex"
	builder.config = changedFallbackAccount
	builder.media.cfg = changedFallbackAccount
	if attempts, prepared := builder.restoredHostedImageAttemptsForSubject(fallbackSnapshot, fallbackRetained, projections, api.UploadSubject{}); prepared || len(attempts) != 0 {
		t.Fatalf("changed fallback account accepted cached link: %#v, prepared=%t", attempts, prepared)
	}
	builder.config = cfg
	builder.media.cfg = cfg
	missingMenu := snapshot
	missingMenu.Artifacts = append(slices.Clone(snapshot.Artifacts), api.MediaArtifact{
		ID:       "menu-selected",
		Kind:     api.MediaArtifactDVDMenu,
		Purpose:  api.ScreenshotPurposeMenu,
		Selected: true,
		Order:    5,
	})
	if attempts, prepared := builder.restoredHostedImageAttemptsForSubject(missingMenu, retained, projections, api.UploadSubject{}); prepared || len(attempts) != 0 {
		t.Fatalf("selected DVD menu without a hosted link accepted: %#v, prepared=%t", attempts, prepared)
	}
	coveredMenu := missingMenu
	coveredMenu.Artifacts = append(append([]api.MediaArtifact(nil), missingMenu.Artifacts...), api.MediaArtifact{
		ID:       "hosted-menu",
		Kind:     api.MediaArtifactHostedImage,
		Purpose:  api.ScreenshotPurposeMenu,
		Selected: true,
		Order:    5,
		Source:   "menu-selected",
		Host:     "pixhost",
		URL:      "https://images.invalid/menu-selected.png",
	})
	coveredRetained := workflowMediaPrivateArtifacts{
		HostedImages:  maps.Clone(retained.HostedImages),
		HostedSources: maps.Clone(retained.HostedSources),
	}
	coveredRetained.HostedImages["hosted-menu"] = api.UploadedImageLink{
		Host:         "pixhost",
		UsageScope:   "global",
		AccountScope: accountScope,
		RawURL:       "https://images.invalid/menu-selected.png",
	}
	coveredRetained.HostedSources["hosted-menu"] = "menu-selected"
	attempts, prepared = builder.restoredHostedImageAttemptsForSubject(coveredMenu, coveredRetained, projections, api.UploadSubject{})
	if !prepared || len(attempts) != 1 ||
		!slices.Equal(attempts[0].ArtifactIDs, []api.PublicResourceID{"screen-selected", "menu-selected"}) ||
		len(attempts[0].Results) != 2 || attempts[0].Results[0].ID != "hosted-selected" || attempts[0].Results[1].ID != "hosted-menu" {
		t.Fatalf("covered DVD menu attempt = %#v, prepared=%t", attempts, prepared)
	}

	changedHost := cfg
	changedHost.ImageHosting.Host1 = "imgbb"
	builder.config = changedHost
	builder.media.cfg = changedHost
	if attempts, prepared := builder.restoredHostedImageAttemptsForSubject(snapshot, retained, projections, api.UploadSubject{}); prepared || len(attempts) != 0 {
		t.Fatalf("changed current host accepted cached link: %#v, prepared=%t", attempts, prepared)
	}

	builder.config = cfg
	builder.media.cfg = cfg
	partial := snapshot
	partial.Artifacts = append([]api.MediaArtifact(nil), snapshot.Artifacts...)
	partial.Artifacts[1].Selected = true
	partialProjections := append([]api.TrackerReleaseProjection(nil), projections...)
	partialProjections[0].Artifacts.ScreenshotCount = 2
	if attempts, prepared := builder.restoredHostedImageAttemptsForSubject(partial, retained, partialProjections, api.UploadSubject{}); prepared || len(attempts) != 0 {
		t.Fatalf("partial cached links accepted: %#v, prepared=%t", attempts, prepared)
	}

	changedAccount := cfg
	changedAccount.ImageHosting.Host3 = "sharex"
	builder.config = changedAccount
	builder.media.cfg = changedAccount
	if attempts, prepared := builder.restoredHostedImageAttemptsForSubject(snapshot, retained, projections, api.UploadSubject{}); prepared || len(attempts) != 0 {
		t.Fatalf("changed host account accepted cached link: %#v, prepared=%t", attempts, prepared)
	}
}

func TestWorkflowMediaRestoreCompatibleMaterializesBeforeSourceCleanup(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "workflow.db")
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("create tmp root: %v", err)
	}
	firstSource := filepath.Join(baseDir, "first", "Same.Release.mkv")
	secondSource := filepath.Join(baseDir, "second", "Same.Release.mkv")
	if err := os.MkdirAll(filepath.Dir(firstSource), 0o700); err != nil {
		t.Fatalf("create first source root: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(secondSource), 0o700); err != nil {
		t.Fatalf("create second source root: %v", err)
	}
	if err := os.WriteFile(firstSource, []byte("first source"), 0o600); err != nil {
		t.Fatalf("write first source: %v", err)
	}
	if err := os.WriteFile(secondSource, []byte("second source"), 0o600); err != nil {
		t.Fatalf("write second source: %v", err)
	}
	firstTmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, firstSource, api.ReleaseInfo{})
	if err != nil {
		t.Fatalf("create first release tmp dir: %v", err)
	}
	secondLegacyTmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, secondSource, api.ReleaseInfo{})
	if err != nil {
		t.Fatalf("create second legacy tmp dir: %v", err)
	}
	if !pathing.SamePath(firstTmpDir, secondLegacyTmpDir) {
		t.Fatalf("same-basename sources did not share legacy tmp dir: %q, %q", firstTmpDir, secondLegacyTmpDir)
	}
	secondReuseRoot, err := reusableMediaTempRoot(tmpRoot, secondSource)
	if err != nil {
		t.Fatalf("resolve second reusable root: %v", err)
	}
	if pathing.SamePath(firstTmpDir, secondReuseRoot) {
		t.Fatalf("reusable root collides with first source legacy tmp dir: %q", secondReuseRoot)
	}
	imagePath := filepath.Join(firstTmpDir, "screen.png")
	imageBytes := []byte("reusable image")
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatalf("write reusable image: %v", err)
	}

	compatibilityKey := api.MediaCompatibilityKey(workflowMediaTestSHA256("compatible source"))
	currentBinding := api.PreparedMediaBinding{
		SourcePath:               secondSource,
		PreparedMediaFingerprint: "second-prepared-media",
		PreparedGeneration:       2,
		CompatibilityKey:         compatibilityKey,
	}
	image := api.ScreenshotImage{Path: imagePath, Purpose: api.ScreenshotPurposeFinal}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}
	captureFingerprint, err := workflowMediaReuseCaptureFingerprint(cfg, compatibilityKey, api.MediaArtifactScreenshot, image)
	if err != nil {
		t.Fatalf("capture fingerprint: %v", err)
	}
	persistStarted := make(chan struct{})
	allowPersistence := make(chan struct{})
	repository := &reusableWorkflowMediaRepository{
		assets: []api.ReusableMediaAsset{{
			Binding: api.PreparedMediaBinding{
				SourcePath:               firstSource,
				PreparedMediaFingerprint: "first-prepared-media",
				PreparedGeneration:       1,
			},
			CompatibilityKey:   compatibilityKey,
			CaptureFingerprint: captureFingerprint,
			ContentSHA256:      workflowMediaTestSHA256(string(imageBytes)),
			Kind:               api.MediaArtifactScreenshot,
			Image:              image,
			Selected:           true,
		}},
		persistStarted:   persistStarted,
		allowPersistence: allowPersistence,
	}
	builder := workflowMediaBuilder{
		config: cfg,
		resolver: workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{
			MediaBinding: currentBinding,
			SourcePath:   secondSource,
		}},
		media: &mediaModule{repo: repository, mediaReuse: repository},
	}
	type restoreResult struct {
		snapshot api.MediaArtifactSet
		retained releaseworkflow.RetainedMediaResource
		err      error
	}
	result := make(chan restoreResult, 1)
	go func() {
		snapshot, retained, restoreErr := builder.RestoreCompatible(t.Context(), api.ReleaseRef{SourcePath: secondSource, Generation: 2},
			api.TrackerReleaseProjectionSet{}, nil, nil, time.Now())
		if restoreErr == nil {
			snapshot.WorkflowID = "workflow-media-reuse"
			snapshot.ID = "restored-media"
			snapshot.Revision = 2
			restoreErr = builder.RecordReusableMedia(t.Context(), snapshot, retained)
		}
		result <- restoreResult{
			snapshot: snapshot,
			retained: retained,
			err:      restoreErr,
		}
	}()
	select {
	case <-persistStarted:
	case restored := <-result:
		t.Fatalf("record reusable media completed before persistence pause: %#v", restored)
	}
	if err := os.RemoveAll(firstTmpDir); err != nil {
		t.Fatalf("remove first source tmp dir: %v", err)
	}
	close(allowPersistence)
	restored := <-result
	if restored.err != nil {
		t.Fatalf("restore compatible media: %v", restored.err)
	}
	if len(repository.saved) != 1 {
		t.Fatalf("persisted reusable assets = %#v", repository.saved)
	}
	materializedPath := repository.saved[0].Image.Path
	if pathing.IsWithinRoot(firstTmpDir, materializedPath) {
		t.Fatalf("persisted image remains under deleted first source root: %q", materializedPath)
	}
	if !pathing.IsWithinRoot(secondReuseRoot, materializedPath) {
		t.Fatalf("persisted image path = %q, want under %q", materializedPath, secondReuseRoot)
	}
	if !strings.HasPrefix(filepath.Base(filepath.Dir(materializedPath)), "@reused-") {
		t.Fatalf("persisted image path = %q, want dedicated reusable root", materializedPath)
	}
	if got, readErr := os.ReadFile(materializedPath); readErr != nil || !bytes.Equal(got, imageBytes) {
		t.Fatalf("materialized image = %q, %v; want %q", got, readErr, imageBytes)
	}
	if len(restored.snapshot.Artifacts) != 1 {
		t.Fatalf("restored artifacts = %#v", restored.snapshot.Artifacts)
	}
	if _, ok := restored.retained.(workflowMediaPrivateArtifacts); !ok {
		t.Fatalf("restored private media = %T", restored.retained)
	}
}

func TestWorkflowMediaRestoreCompatiblePersistsDistinctPathsForEqualBytes(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "workflow.db")
	repository, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	firstSource := filepath.Join(baseDir, "first", "Example.Release.mkv")
	secondSource := filepath.Join(baseDir, "second", "Example.Release.mkv")
	for _, sourcePath := range []string{firstSource, secondSource} {
		if err := os.MkdirAll(filepath.Dir(sourcePath), 0o700); err != nil {
			t.Fatalf("create source directory: %v", err)
		}
		if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
			t.Fatalf("write source: %v", err)
		}
	}
	imageBytes := []byte("identical capture bytes")
	images := []api.ScreenshotImage{
		{
			Path:             filepath.Join(baseDir, "first", "capture-one.png"),
			Purpose:          api.ScreenshotPurposeFinal,
			Index:            1,
			TimestampSeconds: 60,
		},
		{
			Path:             filepath.Join(baseDir, "first", "capture-two.png"),
			Purpose:          api.ScreenshotPurposeFinal,
			Index:            2,
			TimestampSeconds: 120,
		},
	}
	for index := range images {
		images[index].SizeBytes = int64(len(imageBytes))
		if err := os.WriteFile(images[index].Path, imageBytes, 0o600); err != nil {
			t.Fatalf("write reusable image: %v", err)
		}
	}

	compatibilityKey := api.MediaCompatibilityKey(workflowMediaTestSHA256("compatible source"))
	previousBinding := api.PreparedMediaBinding{
		SourcePath:               firstSource,
		PreparedMediaFingerprint: "first-prepared-media",
		PreparedGeneration:       1,
		CompatibilityKey:         compatibilityKey,
	}
	currentBinding := api.PreparedMediaBinding{
		SourcePath:               secondSource,
		PreparedMediaFingerprint: "second-prepared-media",
		PreparedGeneration:       2,
		CompatibilityKey:         compatibilityKey,
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}
	assets := make([]api.ReusableMediaAsset, 0, len(images))
	for _, image := range images {
		captureFingerprint, fingerprintErr := workflowMediaReuseCaptureFingerprint(cfg, compatibilityKey, api.MediaArtifactScreenshot, image)
		if fingerprintErr != nil {
			t.Fatalf("capture fingerprint: %v", fingerprintErr)
		}
		assets = append(assets, api.ReusableMediaAsset{
			Binding:            previousBinding,
			CompatibilityKey:   compatibilityKey,
			CaptureFingerprint: captureFingerprint,
			ContentSHA256:      workflowMediaTestSHA256(string(imageBytes)),
			Kind:               api.MediaArtifactScreenshot,
			Image:              image,
			Selected:           true,
		})
	}
	if assets[0].CaptureFingerprint == assets[1].CaptureFingerprint {
		t.Fatalf("distinct captures share fingerprint %q", assets[0].CaptureFingerprint)
	}
	if err := repository.CommitReusableMedia(ctx, previousBinding, compatibilityKey, assets, api.ReusableMediaCommit{
		WorkflowID: "workflow-media-reuse",
		MediaID:    "seed-media",
		Revision:   1,
	}); err != nil {
		t.Fatalf("seed reusable media: %v", err)
	}

	builder := workflowMediaBuilder{
		config: cfg,
		resolver: workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{
			MediaBinding: currentBinding,
			SourcePath:   secondSource,
		}},
		media: &mediaModule{repo: repository, mediaReuse: repository},
	}
	projections := api.TrackerReleaseProjectionSet{}
	firstSnapshot, firstRetained, err := builder.RestoreCompatible(ctx,
		api.ReleaseRef{SourcePath: secondSource, Generation: currentBinding.PreparedGeneration}, projections, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("restore reusable media: %v", err)
	}
	if len(firstSnapshot.Artifacts) != len(images) {
		t.Fatalf("first restored artifacts = %#v", firstSnapshot.Artifacts)
	}
	firstSnapshot.WorkflowID = "workflow-media-reuse"
	firstSnapshot.ID = "restored-media"
	firstSnapshot.Revision = 2
	if err := builder.RecordReusableMedia(ctx, firstSnapshot, firstRetained); err != nil {
		t.Fatalf("record restored reusable media: %v", err)
	}
	firstPaths := reusableMediaPathsForBinding(ctx, t, repository, compatibilityKey, currentBinding)
	if len(firstPaths) != len(images) {
		t.Fatalf("first restored reusable paths = %v", firstPaths)
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("resolve tmp root: %v", err)
	}
	reuseRoot, err := reusableMediaTempRoot(tmpRoot, secondSource)
	if err != nil {
		t.Fatalf("resolve reuse root: %v", err)
	}
	for pathValue := range firstPaths {
		if !pathing.IsWithinRoot(reuseRoot, pathValue) {
			t.Fatalf("restored reusable path %q outside %q", pathValue, reuseRoot)
		}
		if got, readErr := os.ReadFile(pathValue); readErr != nil || !bytes.Equal(got, imageBytes) {
			t.Fatalf("restored reusable image = %q, %v; want %q", got, readErr, imageBytes)
		}
	}

	secondSnapshot, _, err := builder.RestoreCompatible(ctx,
		api.ReleaseRef{SourcePath: secondSource, Generation: currentBinding.PreparedGeneration}, projections, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("repeat restore reusable media: %v", err)
	}
	if len(secondSnapshot.Artifacts) != len(images) {
		t.Fatalf("second restored artifacts = %#v", secondSnapshot.Artifacts)
	}
	secondPaths := reusableMediaPathsForBinding(ctx, t, repository, compatibilityKey, currentBinding)
	if !maps.Equal(firstPaths, secondPaths) {
		t.Fatalf("repeat restored reusable paths = %v, want %v", secondPaths, firstPaths)
	}
}

func reusableMediaPathsForBinding(
	ctx context.Context,
	t *testing.T,
	repository *db.SQLiteRepository,
	compatibilityKey api.MediaCompatibilityKey,
	binding api.PreparedMediaBinding,
) map[string]struct{} {
	t.Helper()
	assets, err := repository.LoadReusableMediaAssets(ctx, compatibilityKey)
	if err != nil {
		t.Fatalf("load reusable media: %v", err)
	}
	paths := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		if !asset.Binding.Equal(binding) {
			continue
		}
		if _, exists := paths[asset.Image.Path]; exists {
			t.Fatalf("duplicate persisted reusable path %q", asset.Image.Path)
		}
		paths[asset.Image.Path] = struct{}{}
	}
	return paths
}

func TestCopyReusableMediaImageRejectsChangedSourceWithoutPublishing(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	sourcePath := filepath.Join(baseDir, "source.png")
	destination := filepath.Join(baseDir, "destination.png")
	if err := os.WriteFile(sourcePath, []byte("changed image"), 0o600); err != nil {
		t.Fatalf("write source image: %v", err)
	}
	_, err := copyReusableMediaImage(t.Context(), sourcePath, destination, workflowMediaTestSHA256("expected image"))
	if !errors.Is(err, errReusableMediaUnavailable) {
		t.Fatalf("copy changed reusable image error = %v, want unavailable", err)
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination after changed source = %v, want absent", statErr)
	}
	partials, globErr := filepath.Glob(filepath.Join(baseDir, ".reusable-media-*.partial"))
	if globErr != nil || len(partials) != 0 {
		t.Fatalf("partial reusable files = %v, %v", partials, globErr)
	}
}

func TestCopyReusableMediaImagePreservesDifferentDestination(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	sourcePath := filepath.Join(baseDir, "source.png")
	destination := filepath.Join(baseDir, "destination.png")
	if err := os.WriteFile(sourcePath, []byte("expected image"), 0o600); err != nil {
		t.Fatalf("write source image: %v", err)
	}
	if err := os.WriteFile(destination, []byte("different image"), 0o600); err != nil {
		t.Fatalf("write destination image: %v", err)
	}
	_, err := copyReusableMediaImage(t.Context(), sourcePath, destination, workflowMediaTestSHA256("expected image"))
	if err == nil {
		t.Fatal("copy into different destination succeeded")
	}
	if got, readErr := os.ReadFile(destination); readErr != nil || string(got) != "different image" {
		t.Fatalf("destination after conflicting copy = %q, %v", got, readErr)
	}
}

func TestWorkflowMediaRestoreCompatiblePreservesHostedLinkWhenLocalImageIsMissing(t *testing.T) {
	t.Parallel()

	compatibilityKey := api.MediaCompatibilityKey(workflowMediaTestSHA256("verified source"))
	binding := api.PreparedMediaBinding{
		SourcePath:               "C:\\releases\\Example.Release.2026.mkv",
		PreparedMediaFingerprint: "current-prepared-media",
		PreparedGeneration:       7,
		CompatibilityKey:         compatibilityKey,
	}
	image := api.ScreenshotImage{Path: filepath.Join(t.TempDir(), "missing.png"), Purpose: api.ScreenshotPurposeFinal}
	captureFingerprint, err := workflowMediaReuseCaptureFingerprint(config.Config{}, compatibilityKey, api.MediaArtifactScreenshot, image)
	if err != nil {
		t.Fatalf("capture fingerprint: %v", err)
	}
	accountScope, err := workflowMediaHostAccountScope(config.Config{}, "pixhost")
	if err != nil {
		t.Fatalf("host account scope: %v", err)
	}
	repository := &reusableWorkflowMediaRepository{assets: []api.ReusableMediaAsset{{
		Binding:            binding,
		CompatibilityKey:   compatibilityKey,
		CaptureFingerprint: captureFingerprint,
		ContentSHA256:      workflowMediaTestSHA256("missing"),
		Kind:               api.MediaArtifactScreenshot,
		Image:              image,
		Selected:           true,
		HostedLinks: []api.UploadedImageLink{{
			Host:         "pixhost",
			UsageScope:   "global",
			AccountScope: accountScope,
			ImgURL:       "https://images.invalid/missing.png",
			UploadedAt:   time.Now().UTC(),
		}},
	}}}
	builder := workflowMediaBuilder{
		resolver: workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{MediaBinding: binding, SourcePath: binding.SourcePath}},
		media:    &mediaModule{repo: repository, mediaReuse: repository},
	}
	snapshot, _, err := builder.RestoreCompatible(t.Context(), api.ReleaseRef{SourcePath: binding.SourcePath, Generation: binding.PreparedGeneration}, api.TrackerReleaseProjectionSet{}, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("restore compatible hosted link: %v", err)
	}
	if len(snapshot.Artifacts) != 1 || snapshot.Artifacts[0].Kind != api.MediaArtifactHostedImage || snapshot.Artifacts[0].URL == "" {
		t.Fatalf("hosted link was not retained independently: %#v", snapshot.Artifacts)
	}
}

func TestWorkflowMediaReuseCapturesHostsThenRestoresFreshGenerationWithoutRepeatingEffects(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	if err := os.WriteFile(sourcePath, []byte("verified source bytes"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	repository, err := db.Open(filepath.Join(t.TempDir(), "workflow.db"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	prepared, err := preparedrelease.New(repository, workflowMediaIdentityResolver{}, workflowMediaCollector{})
	if err != nil {
		t.Fatalf("create prepared release module: %v", err)
	}
	verified, err := preparedrelease.VerifyInputSource(ctx, api.PrepareInput{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("verify first source: %v", err)
	}
	firstPrepared, err := prepared.Prepare(ctx, api.PrepareInput{SourcePath: sourcePath, VerifiedSource: &verified})
	if err != nil {
		t.Fatalf("prepare first generation: %v", err)
	}
	compatibilityKey, err := firstPrepared.Release.MediaCompatibilityKey()
	if err != nil {
		t.Fatalf("derive first compatibility key: %v", err)
	}
	firstRelease := api.ReleaseRef{SourcePath: sourcePath, Generation: firstPrepared.Release.Generation}
	capture := &durableWorkflowScreenshotFake{root: t.TempDir()}
	host := &countingWorkflowImageHost{}
	cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "pixhost", Host2: "imgbb"}}
	builder := workflowMediaBuilder{
		config:      cfg,
		resolver:    prepared,
		screenshots: capture,
		media: &mediaModule{
			cfg:           cfg,
			images:        host,
			repo:          repository,
			mediaReuse:    repository,
			registry:      mediaImageHostRegistry(t),
			preparedFacts: prepared,
			logger:        api.NopLogger{},
		},
	}
	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
		TrackerID: "ONE", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1},
	}}}
	snapshot, retainedAny, err := builder.Build(ctx, firstRelease, projections,
		api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal, ScreenshotCount: 2}, time.Now())
	if err != nil {
		t.Fatalf("capture first generation: %v", err)
	}
	if capture.captures != 1 || len(snapshot.Artifacts) != 2 {
		t.Fatalf("initial capture calls=%d artifacts=%#v", capture.captures, snapshot.Artifacts)
	}
	snapshot.WorkflowID = "workflow-media-reuse"
	snapshot.ID = "persisted-media"
	snapshot.Revision = 1
	if err := builder.RecordReusableMedia(ctx, snapshot, retainedAny); err != nil {
		t.Fatalf("record existing media state: %v", err)
	}
	existingSnapshot := snapshot
	existingAssets, err := repository.LoadReusableMediaAssets(ctx, compatibilityKey)
	if err != nil {
		t.Fatalf("load existing reusable media: %v", err)
	}
	firstArtifactID, deletedArtifactID := snapshot.Artifacts[0].ID, snapshot.Artifacts[1].ID
	snapshot, retainedResource, _, err := builder.UploadImages(ctx, firstRelease, projections, snapshot, retainedAny,
		[]api.PublicResourceID{firstArtifactID}, "imgbb", false, time.Now())
	if err != nil {
		t.Fatalf("host first generation: %v", err)
	}
	if host.uploads != 1 || countMediaArtifacts(snapshot.Artifacts, api.MediaArtifactHostedImage) != 1 {
		t.Fatalf("initial host uploads=%d artifacts=%#v", host.uploads, snapshot.Artifacts)
	}
	existingCommitted, err := builder.HasReusableMediaCommit(ctx, existingSnapshot)
	if err != nil || !existingCommitted {
		t.Fatalf("existing reusable media receipt = %v, %v", existingCommitted, err)
	}
	assetsBeforePublication, err := repository.LoadReusableMediaAssets(ctx, compatibilityKey)
	if err != nil {
		t.Fatalf("load reusable media before publication: %v", err)
	}
	if !reflect.DeepEqual(assetsBeforePublication, existingAssets) {
		t.Fatalf("upload mutated reusable associations before publication: got %#v, want %#v", assetsBeforePublication, existingAssets)
	}
	snapshot, retainedResource, reusedAttempts, err := builder.UploadImages(ctx, firstRelease, projections, snapshot, retainedResource,
		[]api.PublicResourceID{firstArtifactID}, "imgbb", false, time.Now())
	if err != nil {
		t.Fatalf("reuse first generation host: %v", err)
	}
	if host.uploads != 2 || countMediaArtifacts(snapshot.Artifacts, api.MediaArtifactHostedImage) != 1 ||
		len(reusedAttempts) != 1 || len(reusedAttempts[0].Results) != 1 ||
		reusedAttempts[0].Results[0].Kind != api.MediaArtifactHostedImage {
		t.Fatalf("reused host result uploads=%d attempts=%#v artifacts=%#v", host.uploads, reusedAttempts, snapshot.Artifacts)
	}
	currentAccountScope, err := workflowMediaHostAccountScope(cfg, "imgbb")
	if err != nil {
		t.Fatalf("current account scope: %v", err)
	}
	initialRetained, ok := retainedResource.(workflowMediaPrivateArtifacts)
	if !ok || len(initialRetained.HostedImages) != 1 {
		t.Fatalf("initial retained hosted images = %#v", retainedResource)
	}
	for _, link := range initialRetained.HostedImages {
		if link.AccountScope != currentAccountScope {
			t.Fatalf("initial hosted account scope = %q, want %q", link.AccountScope, currentAccountScope)
		}
	}
	for index := range snapshot.Artifacts {
		if snapshot.Artifacts[index].ID == firstArtifactID {
			snapshot.Artifacts[index].Order = 7
		}
	}
	retainedResource, err = retainedResource.DeleteArtifacts(ctx, snapshot, []api.PublicResourceID{deletedArtifactID})
	if err != nil {
		t.Fatalf("delete second local artifact: %v", err)
	}
	snapshot.Artifacts = slices.DeleteFunc(snapshot.Artifacts, func(artifact api.MediaArtifact) bool {
		return artifact.ID == deletedArtifactID
	})
	committer, ok := retainedResource.(releaseworkflow.RetainedMediaCommitter)
	if !ok {
		t.Fatalf("deleted media did not retain a committer: %T", retainedResource)
	}
	if err := committer.Commit(ctx); err != nil {
		t.Fatalf("commit local deletion: %v", err)
	}
	snapshot.ID = "published-media"
	snapshot.Revision = 2
	if err := builder.RecordReusableMedia(ctx, snapshot, retainedResource); err != nil {
		t.Fatalf("record final media state: %v", err)
	}
	committed, err := builder.HasReusableMediaCommit(ctx, snapshot)
	if err != nil || !committed {
		t.Fatalf("recorded media commit = %v, %v", committed, err)
	}
	existingCommitted, err = builder.HasReusableMediaCommit(ctx, existingSnapshot)
	if err != nil || !existingCommitted {
		t.Fatalf("existing reusable media receipt after publication = %v, %v", existingCommitted, err)
	}
	publishedAssets, err := repository.LoadReusableMediaAssets(ctx, compatibilityKey)
	if err != nil {
		t.Fatalf("load published reusable media: %v", err)
	}
	if len(publishedAssets) != 1 || len(publishedAssets[0].HostedLinks) != 1 {
		t.Fatalf("published reusable media = %#v", publishedAssets)
	}
	uploadsBeforeRestore := host.uploads
	verified, err = preparedrelease.VerifyInputSource(ctx, api.PrepareInput{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("verify refreshed source: %v", err)
	}
	secondPrepared, err := prepared.Prepare(ctx, api.PrepareInput{
		SourcePath:     sourcePath,
		VerifiedSource: &verified,
		Force:          true,
	})
	if err != nil {
		t.Fatalf("prepare refreshed generation: %v", err)
	}
	if secondPrepared.Release.Generation <= firstPrepared.Release.Generation {
		t.Fatalf("prepared generation did not advance: %d -> %d", firstPrepared.Release.Generation, secondPrepared.Release.Generation)
	}
	restored, restoredResource, err := builder.RestoreCompatible(ctx,
		api.ReleaseRef{SourcePath: sourcePath, Generation: secondPrepared.Release.Generation}, projections, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("restore refreshed generation: %v", err)
	}
	if capture.captures != 1 || host.uploads != uploadsBeforeRestore {
		t.Fatalf("restore repeated effects: captures=%d uploads=%d before=%d", capture.captures, host.uploads, uploadsBeforeRestore)
	}
	if len(restored.Artifacts) != 2 || restored.Artifacts[0].Kind != api.MediaArtifactScreenshot || restored.Artifacts[0].Order != 7 ||
		restored.Artifacts[1].Kind != api.MediaArtifactHostedImage || restored.Artifacts[1].Order != 7 || restored.Artifacts[1].URL == "" ||
		!restored.ImageRequirementsPrepared || len(restored.HostAttempts) != 1 || restored.HostAttempts[0].Host != "imgbb" || len(restored.HostAttempts[0].Results) != 1 ||
		restored.HostAttempts[0].Results[0].ID != restored.Artifacts[1].ID {
		t.Fatalf("restored media did not preserve ready current hosted state: %#v", restored)
	}
	restoredPrivate, ok := restoredResource.(workflowMediaPrivateArtifacts)
	if !ok || len(restoredPrivate.HostedImages) != 1 {
		t.Fatalf("restored private media = %#v", restoredResource)
	}
	for _, link := range restoredPrivate.HostedImages {
		if link.AccountScope != currentAccountScope {
			t.Fatalf("restored hosted account scope = %q, want %q", link.AccountScope, currentAccountScope)
		}
	}
}

func TestWorkflowMediaAttachDefersReusableMediaPersistenceAndPreservesDuplicateAttachmentIdentity(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	baseDir := t.TempDir()
	sourcePath := filepath.Join(baseDir, "Example.Release.2026.mkv")
	if err := os.WriteFile(sourcePath, []byte("verified source bytes"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dbPath := filepath.Join(baseDir, "workflow.db")
	repository, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	prepared, err := preparedrelease.New(repository, workflowMediaIdentityResolver{}, workflowMediaCollector{})
	if err != nil {
		t.Fatalf("create prepared release module: %v", err)
	}
	verified, err := preparedrelease.VerifyInputSource(ctx, api.PrepareInput{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("verify source: %v", err)
	}
	preparedResult, err := prepared.Prepare(ctx, api.PrepareInput{SourcePath: sourcePath, VerifiedSource: &verified})
	if err != nil {
		t.Fatalf("prepare source: %v", err)
	}
	compatibilityKey, err := preparedResult.Release.MediaCompatibilityKey()
	if err != nil {
		t.Fatalf("derive compatibility key: %v", err)
	}
	release := api.ReleaseRef{SourcePath: sourcePath, Generation: preparedResult.Release.Generation}
	subject, err := prepared.ResolveScreenshotSubject(ctx, api.MediaPlanInput{Release: release})
	if err != nil {
		t.Fatalf("resolve screenshot subject: %v", err)
	}
	imagePath := filepath.Join(baseDir, "existing.png")
	if err := os.WriteFile(imagePath, []byte("existing image"), 0o600); err != nil {
		t.Fatalf("write existing image: %v", err)
	}
	image := api.ScreenshotImage{
		Path:      imagePath,
		Purpose:   api.ScreenshotPurposeFinal,
		SizeBytes: int64(len("existing image")),
	}
	existing := api.MediaArtifactSet{
		WorkflowID:         "workflow-media-attach",
		ID:                 "persisted-media",
		Revision:           1,
		CaptureFingerprint: workflowTestFingerprint(t, "persisted-media-attach"),
		Status:             api.StageStatusCompleted,
		Artifacts: []api.MediaArtifact{{
			ID:       "existing-screen",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
		}},
	}
	retained := workflowMediaPrivateArtifacts{
		Screenshots:       []api.ScreenshotImage{image},
		ArtifactImages:    map[api.PublicResourceID]api.ScreenshotImage{"existing-screen": image},
		DVDMenuImages:     make(map[api.PublicResourceID]api.DVDMenuCaptureImage),
		HostedImages:      make(map[api.PublicResourceID]api.UploadedImageLink),
		HostedSources:     make(map[api.PublicResourceID]api.PublicResourceID),
		screenshotSubject: subject,
		mediaReuse:        repository,
		commitState:       &workflowMediaCommitState{},
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}
	builder := workflowMediaBuilder{
		config: cfg,
		media: &mediaModule{
			cfg:           cfg,
			repo:          repository,
			mediaReuse:    repository,
			preparedFacts: prepared,
			logger:        api.NopLogger{},
		},
	}
	if err := builder.RecordReusableMedia(ctx, existing, retained); err != nil {
		t.Fatalf("record existing media state: %v", err)
	}
	existingAssets, err := repository.LoadReusableMediaAssets(ctx, compatibilityKey)
	if err != nil {
		t.Fatalf("load existing reusable media: %v", err)
	}
	updated, updatedRetained, err := builder.Attach(ctx, release, api.TrackerReleaseProjectionSet{}, &existing, retained,
		[]releaseworkflow.StagedMediaAttachment{
			{
				Attachment: api.MediaAttachment{
					Kind:    api.MediaArtifactScreenshot,
					Purpose: api.ScreenshotPurposeFinal,
					Order:   1,
				},
				Content: releaseworkflow.StagedMediaContent{ContentType: "image/png", Bytes: []byte("duplicate image")},
			},
			{
				Attachment: api.MediaAttachment{
					Kind:    api.MediaArtifactDVDMenu,
					Purpose: api.ScreenshotPurposeMenu,
					Order:   2,
				},
				Content: releaseworkflow.StagedMediaContent{ContentType: "image/png", Bytes: []byte("duplicate image")},
			},
			{
				Attachment: api.MediaAttachment{
					Kind:    api.MediaArtifactScreenshot,
					Purpose: api.ScreenshotPurposeFinal,
					Order:   3,
				},
				Content: releaseworkflow.StagedMediaContent{ContentType: "image/png", Bytes: []byte("following image")},
			},
		}, time.Now())
	if err != nil {
		t.Fatalf("attach media: %v", err)
	}
	if len(updated.Artifacts) != 3 || updated.Artifacts[1].Kind != api.MediaArtifactScreenshot ||
		updated.Artifacts[1].Purpose != api.ScreenshotPurposeFinal || updated.Artifacts[1].Order != 1 || updated.Artifacts[1].Index != 0 ||
		updated.Artifacts[2].Kind != api.MediaArtifactScreenshot || updated.Artifacts[2].Purpose != api.ScreenshotPurposeFinal ||
		updated.Artifacts[2].Order != 3 || updated.Artifacts[2].Index != 1 {
		t.Fatalf("attached artifacts = %#v", updated.Artifacts)
	}
	existingCommitted, err := builder.HasReusableMediaCommit(ctx, existing)
	if err != nil || !existingCommitted {
		t.Fatalf("existing reusable media receipt = %v, %v", existingCommitted, err)
	}
	assetsBeforePublication, err := repository.LoadReusableMediaAssets(ctx, compatibilityKey)
	if err != nil {
		t.Fatalf("load reusable media before publication: %v", err)
	}
	if !reflect.DeepEqual(assetsBeforePublication, existingAssets) {
		t.Fatalf("attach mutated reusable associations before publication: got %#v, want %#v", assetsBeforePublication, existingAssets)
	}
	updated.ID = "published-media"
	updated.Revision = 2
	if err := builder.RecordReusableMedia(ctx, updated, updatedRetained); err != nil {
		t.Fatalf("record published media state: %v", err)
	}
	publishedCommitted, err := builder.HasReusableMediaCommit(ctx, updated)
	if err != nil || !publishedCommitted {
		t.Fatalf("published reusable media receipt = %v, %v", publishedCommitted, err)
	}
	existingCommitted, err = builder.HasReusableMediaCommit(ctx, existing)
	if err != nil || !existingCommitted {
		t.Fatalf("existing reusable media receipt after publication = %v, %v", existingCommitted, err)
	}
	publishedAssets, err := repository.LoadReusableMediaAssets(ctx, compatibilityKey)
	if err != nil {
		t.Fatalf("load published reusable media: %v", err)
	}
	if len(publishedAssets) != 3 || reflect.DeepEqual(publishedAssets, existingAssets) {
		t.Fatalf("published reusable media = %#v", publishedAssets)
	}
}

type workflowMediaIdentityResolver struct{}

func (workflowMediaIdentityResolver) Resolve(_ context.Context, request externalidentity.Request) (externalidentity.Result, error) {
	now := time.Now().UTC()
	return externalidentity.Result{
		Identity: api.ExternalIdentity{
			SourcePath: request.SourcePath,
			Generation: request.Generation,
			TMDBID:     1234567,
			Category:   api.CanonicalCategoryMovie,
			Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceProvider, Category: api.IdentityProvenanceProvider},
			Conflict:   api.IdentityConflictNone,
			Resolution: api.IdentityResolutionKey{
				SourceFingerprint: request.SourceFingerprint,
				IntentFingerprint: "workflow-media-test",
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

type workflowMediaCollector struct{}

func (workflowMediaCollector) Collect(_ context.Context, request preparationstate.Request) (preparedrelease.CollectedFacts, error) {
	return preparedrelease.CollectedFacts{
		Naming: api.NamingFacts{Filename: filepath.Base(request.Manifest.SourcePath), ReleaseName: "Example.Release.2026.1080p-GRP"},
		Resources: preparedrelease.CollectedResources{
			SourcePath: request.Manifest.SourcePath,
			VideoPath:  request.Manifest.SourcePath,
			FileList:   []string{request.Manifest.SourcePath},
		},
	}, nil
}

type durableWorkflowScreenshotFake struct {
	root     string
	captures int
}

func (*durableWorkflowScreenshotFake) Plan(context.Context, api.ScreenshotSubject, int) (api.ScreenshotPlan, error) {
	return api.ScreenshotPlan{SuggestedSelections: []api.ScreenshotSelection{
		{Index: 1, TimestampSeconds: 60},
		{Index: 2, TimestampSeconds: 120},
	}}, nil
}

func (f *durableWorkflowScreenshotFake) Capture(
	_ context.Context,
	_ api.ScreenshotSubject,
	selections []api.ScreenshotSelection,
	purpose api.ScreenshotPurpose,
) (api.ScreenshotResult, error) {
	f.captures++
	images := make([]api.ScreenshotImage, 0, len(selections))
	for _, selection := range selections {
		pathValue := filepath.Join(f.root, fmt.Sprintf("capture-%d.png", selection.Index))
		content := []byte(fmt.Sprintf("capture-%d", selection.Index))
		if err := os.WriteFile(pathValue, content, 0o600); err != nil {
			return api.ScreenshotResult{}, fmt.Errorf("write captured screenshot: %w", err)
		}
		images = append(images, api.ScreenshotImage{
			Path:             pathValue,
			Purpose:          purpose,
			Index:            selection.Index,
			TimestampSeconds: selection.TimestampSeconds,
			Width:            1920,
			Height:           1080,
			SizeBytes:        int64(len(content)),
		})
	}
	return api.ScreenshotResult{Purpose: purpose, Images: images}, nil
}

func (*durableWorkflowScreenshotFake) PreviewFrame(context.Context, api.ScreenshotSubject, string, float64) (api.ScreenshotPreview, error) {
	return api.ScreenshotPreview{}, nil
}

func (f *durableWorkflowScreenshotFake) Delete(_ context.Context, _ api.ScreenshotSubject, pathValue string) error {
	if err := os.Remove(pathValue); err != nil {
		return fmt.Errorf("delete captured screenshot: %w", err)
	}
	return nil
}

func (*durableWorkflowScreenshotFake) SaveFinalSelections(context.Context, api.ScreenshotSubject, []api.ScreenshotImage) error {
	return nil
}

type countingWorkflowImageHost struct{ uploads int }

func (*countingWorkflowImageHost) ListCandidates(context.Context, api.ImageHostingSubject) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (h *countingWorkflowImageHost) Upload(
	_ context.Context,
	_ api.ImageHostingSubject,
	host string,
	usageScope string,
	images []api.ScreenshotImage,
) ([]api.UploadedImageLink, error) {
	h.uploads++
	links := make([]api.UploadedImageLink, 0, len(images))
	for _, image := range images {
		links = append(links, api.UploadedImageLink{
			ImagePath:  image.Path,
			Host:       host,
			UsageScope: usageScope,
			RawURL:     "https://images.invalid/" + filepath.Base(image.Path),
			SizeBytes:  image.SizeBytes,
			UploadedAt: time.Now().UTC(),
		})
	}
	return links, nil
}

func workflowMediaTestSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (workflowMediaResolverFake) ResolveDVDMenuSubject(
	_ context.Context,
	input api.MediaPlanInput,
) (api.DVDMenuSubject, error) {
	return api.DVDMenuSubject{SourcePath: input.Release.SourcePath, DiscType: "DVD"}, nil
}

type workflowMediaPlanResolverFake struct {
	screenshotInputs []api.MediaPlanInput
	err              error
}

func (f *workflowMediaPlanResolverFake) ResolveScreenshotSubject(
	_ context.Context,
	input api.MediaPlanInput,
) (api.ScreenshotSubject, error) {
	f.screenshotInputs = append(f.screenshotInputs, input)
	if f.err != nil {
		return api.ScreenshotSubject{}, f.err
	}
	return api.ScreenshotSubject{SourcePath: input.Release.SourcePath}, nil
}

func (*workflowMediaPlanResolverFake) ResolveDVDMenuSubject(
	context.Context,
	api.MediaPlanInput,
) (api.DVDMenuSubject, error) {
	return api.DVDMenuSubject{}, nil
}

type workflowScreenshotFake struct {
	root       string
	plan       *api.ScreenshotPlan
	result     *api.ScreenshotResult
	plans      int
	captures   int
	selections []api.ScreenshotSelection
	planCounts []int
	deleted    []string
	err        error
}

func (f *workflowScreenshotFake) Plan(
	_ context.Context,
	_ api.ScreenshotSubject,
	count int,
) (api.ScreenshotPlan, error) {
	f.plans++
	f.planCounts = append(f.planCounts, count)
	if f.plan != nil {
		return *f.plan, nil
	}
	return api.ScreenshotPlan{SuggestedSelections: []api.ScreenshotSelection{{Index: 1, TimestampSeconds: 60}}}, nil
}

func TestWorkflowMediaPlanHonorsProjectedContentRequirements(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		artifacts     api.TrackerArtifactRequirements
		wantPlanCount int
	}{
		{name: "none"},
		{name: "dvd-menu-only", artifacts: api.TrackerArtifactRequirements{DVDMenuCount: 1}},
		{
			name:          "images",
			artifacts:     api.TrackerArtifactRequirements{ScreenshotCount: 2},
			wantPlanCount: 2,
		},
		{
			name:          "full",
			artifacts:     api.TrackerArtifactRequirements{ScreenshotCount: 3, Description: true},
			wantPlanCount: 3,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			resolver := &workflowMediaPlanResolverFake{}
			if testCase.wantPlanCount == 0 {
				resolver.err = errors.New("source artifact is unavailable")
			}
			screenshots := &workflowScreenshotFake{}
			builder := workflowMediaBuilder{
				config:      config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 7}},
				resolver:    resolver,
				screenshots: screenshots,
			}
			plan, err := builder.Plan(
				t.Context(),
				api.ReleaseRef{SourcePath: "missing-source.mkv", Generation: 1},
				api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
					TrackerID: "SYNTHETIC",
					Artifacts: testCase.artifacts,
				}}},
				time.Now(),
			)
			if err != nil {
				t.Fatalf("plan workflow media: %v", err)
			}
			if len(plan.Requirements) != 1 || plan.Requirements[0].TrackerID != "SYNTHETIC" ||
				plan.Requirements[0].ScreenshotCount != testCase.artifacts.ScreenshotCount ||
				plan.Requirements[0].DVDMenuCount != testCase.artifacts.DVDMenuCount {
				t.Fatalf("media requirements = %#v", plan.Requirements)
			}
			if testCase.wantPlanCount == 0 {
				if len(resolver.screenshotInputs) != 0 || screenshots.plans != 0 || len(plan.SuggestedSelections) != 0 {
					t.Fatalf("empty media plan resolved source: plan=%#v inputs=%#v calls=%d", plan, resolver.screenshotInputs, screenshots.plans)
				}
				return
			}
			if len(resolver.screenshotInputs) != 1 || resolver.screenshotInputs[0].Count != testCase.wantPlanCount ||
				!slices.Equal(screenshots.planCounts, []int{testCase.wantPlanCount}) || len(plan.SuggestedSelections) != 1 {
				t.Fatalf("media plan = %#v inputs=%#v counts=%v", plan, resolver.screenshotInputs, screenshots.planCounts)
			}
		})
	}
}

func TestWorkflowMediaBuilderPreservesExplicitCaptureWithoutProjectedRequirement(t *testing.T) {
	t.Parallel()

	screenshots := &workflowScreenshotFake{root: t.TempDir()}
	snapshot, _, err := (workflowMediaBuilder{
		resolver:    workflowMediaResolverFake{},
		screenshots: screenshots,
	}).Build(
		t.Context(),
		api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1},
		api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "SYNTHETIC"}}},
		api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal, ScreenshotCount: 1},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build explicit workflow media: %v", err)
	}
	if snapshot.Status != api.StageStatusCompleted || len(snapshot.Artifacts) != 1 || screenshots.plans != 1 || screenshots.captures != 1 {
		t.Fatalf("explicit media capture = %#v plans=%d captures=%d", snapshot, screenshots.plans, screenshots.captures)
	}
	if !slices.Equal(screenshots.planCounts, []int{1}) || snapshot.Artifacts[0].Index != 1 || snapshot.Artifacts[0].TimestampSeconds != 60 {
		t.Fatalf("explicit media artifact = %#v", snapshot.Artifacts[0])
	}
}

func (f *workflowScreenshotFake) Capture(
	_ context.Context,
	subject api.ScreenshotSubject,
	selections []api.ScreenshotSelection,
	purpose api.ScreenshotPurpose,
) (api.ScreenshotResult, error) {
	f.captures++
	f.selections = append(f.selections, selections...)
	if f.err != nil {
		return api.ScreenshotResult{}, f.err
	}
	if f.result != nil {
		result := *f.result
		result.Purpose = purpose
		return result, nil
	}
	images := make([]api.ScreenshotImage, len(selections))
	discNames := make(map[string]string, len(subject.Discs))
	for _, disc := range subject.Discs {
		discNames[disc.ID] = disc.Name
	}
	for index, selection := range selections {
		name := fmt.Sprintf("screen-%d.png", selection.Index)
		if selection.DiscID != "" {
			name = fmt.Sprintf("%s-screen-%d.png", selection.DiscID, selection.Index)
		}
		images[index] = api.ScreenshotImage{
			DiscID:           selection.DiscID,
			DiscName:         discNames[selection.DiscID],
			Path:             filepath.Join(f.root, name),
			Purpose:          purpose,
			Index:            selection.Index,
			TimestampSeconds: selection.TimestampSeconds,
			Width:            1920,
			Height:           1080,
			SizeBytes:        1234,
		}
	}
	return api.ScreenshotResult{Purpose: purpose, Images: images}, nil
}

func TestWorkflowMediaBuilderKeepsAutomaticSuggestionsForOmittedDiscs(t *testing.T) {
	t.Parallel()

	subject := api.ScreenshotSubject{SourcePath: "C:\\releases\\Example.Release.2026", Discs: []api.ScreenshotDiscSubject{
		{ID: "disc-a", Name: "Disc 1"},
		{ID: "disc-b", Name: "Disc 2"},
	}}
	screenshots := &workflowScreenshotFake{root: t.TempDir(), plan: &api.ScreenshotPlan{
		Discs: []api.ScreenshotDiscPlan{
			{
				DiscID:   "disc-a",
				DiscName: "Disc 1",
				SuggestedSelections: []api.ScreenshotSelection{{
					DiscID:           "disc-a",
					Index:            0,
					TimestampSeconds: 10,
				}},
			},
			{
				DiscID:   "disc-b",
				DiscName: "Disc 2",
				SuggestedSelections: []api.ScreenshotSelection{{
					DiscID:           "disc-b",
					Index:            0,
					TimestampSeconds: 20,
				}},
			},
		},
	}}
	builder := workflowMediaBuilder{
		resolver:    workflowMediaResolverFake{screenshotSubject: &subject},
		screenshots: screenshots,
	}
	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
		TrackerID: "ALPHA", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 2},
	}}}

	result, _, err := builder.Build(context.Background(), api.ReleaseRef{SourcePath: subject.SourcePath, Generation: 1}, projections,
		api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal, Selections: []api.ScreenshotSelection{{
			DiscID:           "disc-a",
			Index:            4,
			TimestampSeconds: 40,
		}}}, time.Now())
	if err != nil {
		t.Fatalf("build media: %v", err)
	}
	if len(result.Artifacts) != 2 || len(screenshots.selections) != 2 || screenshots.selections[0].DiscID != "disc-a" ||
		screenshots.selections[1].DiscID != "disc-b" {
		t.Fatalf("artifacts=%#v selections=%#v", result.Artifacts, screenshots.selections)
	}
}

func TestMergeManualSelectionsPreservesUnreplacedExistingScreenshots(t *testing.T) {
	t.Parallel()

	manual := []api.ScreenshotSelection{{
		DiscID:           "disc-a",
		Index:            4,
		TimestampSeconds: 40,
	}}
	plan := api.ScreenshotPlan{
		Discs: []api.ScreenshotDiscPlan{
			{DiscID: "disc-a"},
			{DiscID: "disc-b", SuggestedSelections: []api.ScreenshotSelection{{
				DiscID:           "disc-b",
				Index:            0,
				TimestampSeconds: 20,
			}}},
		},
		ExistingScreenshots: []api.ScreenshotImage{
			{
				DiscID: "disc-a",
				Index:  1,
				Path:   "disc-a-existing.png",
			},
			{
				DiscID: "disc-a",
				Index:  4,
				Path:   "disc-a-replaced.png",
			},
			{
				DiscID: "disc-b",
				Index:  2,
				Path:   "disc-b-existing.png",
			},
		},
	}

	selections, existing, err := mergeManualSelectionsWithDiscPlan(manual, plan)
	if err != nil {
		t.Fatalf("merge selections: %v", err)
	}
	if len(selections) != 2 || selections[0].DiscID != "disc-a" || selections[1].DiscID != "disc-b" {
		t.Fatalf("merged selections = %#v", selections)
	}
	if len(existing) != 2 || existing[0].Path != "disc-a-existing.png" || existing[1].Path != "disc-b-existing.png" {
		t.Fatalf("retained screenshots = %#v", existing)
	}
}

func TestDecodeWorkflowMediaPrivateArtifactsHandlesLegacyHostedDeleteBinding(t *testing.T) {
	t.Parallel()

	binding := api.PreparedMediaBinding{
		SourcePath:               "C:\\releases\\Example.Release.2026",
		PreparedMediaFingerprint: "prepared-media",
		PreparedGeneration:       2,
	}
	for _, test := range []struct {
		name       string
		subject    api.ScreenshotSubject
		wantDelete bool
	}{
		{
			name:       "subject fallback",
			subject:    api.ScreenshotSubject{MediaBinding: binding},
			wantDelete: true,
		},
		{name: "unbound legacy entry", wantDelete: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger := &recordingMediaLogger{}
			payload, err := json.Marshal(persistedWorkflowMediaArtifacts{
				ScreenshotSubject: test.subject,
				PendingDeletes: []persistedWorkflowMediaPendingDelete{{
					Kind: api.MediaArtifactHostedImage,
					Path: "C:\\private\\screen.png",
					Host: "example-host",
				}},
			})
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}

			decoded, err := decodeWorkflowMediaPrivateArtifacts(workflowMediaBuilder{media: &mediaModule{logger: logger}}, payload)
			if err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			artifacts, ok := decoded.(workflowMediaPrivateArtifacts)
			if !ok {
				t.Fatalf("decoded fixture type = %T", decoded)
			}
			if !test.wantDelete {
				if artifacts.commitState != nil {
					t.Fatalf("unsafe legacy delete remained queued: %#v", artifacts.commitState.pending)
				}
				if got := logger.countLevelContaining("WARN", "kind=hosted_image"); got != 1 {
					t.Fatalf("legacy deletion warning count = %d, want 1", got)
				}
				for _, entry := range logger.entries {
					if strings.Contains(entry.message, "screen.png") || strings.Contains(entry.message, "example-host") {
						t.Fatalf("legacy deletion warning exposed private details: %q", entry.message)
					}
				}
				return
			}
			if logger.countLevelContaining("WARN", "legacy pending media deletion") != 0 {
				t.Fatalf("unexpected legacy deletion warning: %#v", logger.entries)
			}
			if artifacts.commitState == nil || len(artifacts.commitState.pending) != 1 ||
				!artifacts.commitState.pending[0].binding.Equal(binding) {
				t.Fatalf("repaired pending delete = %#v", artifacts.commitState)
			}
		})
	}
}

func TestWorkflowMediaBuilderRetainsExpandedRawFramesAcrossDiscs(t *testing.T) {
	t.Parallel()

	subject := api.ScreenshotSubject{SourcePath: "C:\\releases\\Example.Release.2026", Discs: []api.ScreenshotDiscSubject{
		{ID: "disc-a", Name: "Disc 1"},
		{ID: "disc-b", Name: "Disc 2"},
	}}
	expanded := []api.ScreenshotSelection{
		{
			DiscID:           "disc-a",
			Index:            0,
			Frame:            24,
			TimestampSeconds: 1,
			Source:           "manual",
		},
		{
			DiscID:           "disc-a",
			Index:            1,
			Frame:            48,
			TimestampSeconds: 2,
			Source:           "manual",
		},
		{
			DiscID:           "disc-b",
			Index:            0,
			Frame:            24,
			TimestampSeconds: 1,
			Source:           "manual",
		},
		{
			DiscID:           "disc-b",
			Index:            1,
			Frame:            48,
			TimestampSeconds: 2,
			Source:           "manual",
		},
	}
	screenshots := &workflowScreenshotFake{root: t.TempDir(), plan: &api.ScreenshotPlan{
		SuggestedSelections: expanded,
		Discs: []api.ScreenshotDiscPlan{
			{
				DiscID:              "disc-a",
				DiscName:            "Disc 1",
				SuggestedSelections: expanded[:2],
			},
			{
				DiscID:              "disc-b",
				DiscName:            "Disc 2",
				SuggestedSelections: expanded[2:],
			},
		},
	}}
	builder := workflowMediaBuilder{
		resolver:    workflowMediaResolverFake{screenshotSubject: &subject},
		screenshots: screenshots,
	}

	result, _, err := builder.Build(context.Background(), api.ReleaseRef{SourcePath: subject.SourcePath, Generation: 1}, api.TrackerReleaseProjectionSet{},
		api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal, ManualFrames: []int{24, 48}}, time.Now())
	if err != nil {
		t.Fatalf("build media: %v", err)
	}
	if len(result.Artifacts) != 4 || len(screenshots.selections) != 4 {
		t.Fatalf("artifacts=%#v selections=%#v", result.Artifacts, screenshots.selections)
	}
	for index, artifact := range result.Artifacts {
		if artifact.DiscID != expanded[index].DiscID || artifact.Index != expanded[index].Index {
			t.Fatalf("artifact[%d] = %#v", index, artifact)
		}
	}
}

func (*workflowScreenshotFake) PreviewFrame(
	context.Context,
	api.ScreenshotSubject,
	string,
	float64,
) (api.ScreenshotPreview, error) {
	return api.ScreenshotPreview{}, nil
}

func (f *workflowScreenshotFake) Delete(_ context.Context, _ api.ScreenshotSubject, path string) error {
	f.deleted = append(f.deleted, path)
	return nil
}

func (*workflowScreenshotFake) SaveFinalSelections(context.Context, api.ScreenshotSubject, []api.ScreenshotImage) error {
	return nil
}

type workflowDVDMenuFake struct {
	captures  int
	deleted   []string
	maxItems  []int
	result    *api.DVDMenuCaptureResult
	err       error
	deleteErr error
}

func (f *workflowDVDMenuFake) Capture(
	_ context.Context,
	_ api.DVDMenuSubject,
	maxItems int,
) (api.DVDMenuCaptureResult, error) {
	f.captures++
	f.maxItems = append(f.maxItems, maxItems)
	if f.err != nil {
		return api.DVDMenuCaptureResult{}, f.err
	}
	if f.result != nil {
		return *f.result, nil
	}
	return api.DVDMenuCaptureResult{
		Images: []api.DVDMenuCaptureImage{{
			Path:      "C:\\private\\menu.png",
			Purpose:   api.ScreenshotPurposeMenu,
			Width:     720,
			Height:    480,
			SizeBytes: 4321,
			Discovery: api.DVDMenuDiscoveryReachable,
		}},
		MaxItems: maxItems,
		Complete: true,
	}, nil
}

func (*workflowDVDMenuFake) List(context.Context, api.DVDMenuSubject) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (f *workflowDVDMenuFake) Delete(_ context.Context, _ api.DVDMenuSubject, path string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, path)
	return nil
}

func (*workflowDVDMenuFake) Capability(context.Context) (api.DVDMenuEngineInfo, error) {
	return api.DVDMenuEngineInfo{}, nil
}

func TestWorkflowMediaBuilderCapturesOnlyProjectedNormalScreenshots(t *testing.T) {
	t.Parallel()

	screenshots := &workflowScreenshotFake{root: t.TempDir()}
	dvdMenus := &workflowDVDMenuFake{}
	builder := workflowMediaBuilder{
		resolver:    workflowMediaResolverFake{},
		screenshots: screenshots,
		dvdMenus:    dvdMenus,
	}
	projection := api.TrackerReleaseProjection{
		TrackerID: "ALPHA",
		Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1},
	}
	projections := api.TrackerReleaseProjectionSet{
		ID:          "projections-1",
		Revision:    4,
		Projections: []api.TrackerReleaseProjection{projection},
	}
	release := api.ReleaseRef{SourcePath: "C:\\releases\\Example.Release.2026", Generation: 1}
	snapshot, privateArtifacts, err := builder.Build(
		context.Background(),
		release,
		projections,
		api.MediaCaptureInstructions{},
		time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("build workflow media: %v", err)
	}
	if snapshot.Status != api.StageStatusCompleted || len(snapshot.Artifacts) != 1 ||
		screenshots.plans != 1 || screenshots.captures != 1 || dvdMenus.captures != 0 {
		t.Fatalf("media capture = %#v plans=%d screenshots=%d menus=%d", snapshot, screenshots.plans, screenshots.captures, dvdMenus.captures)
	}
	private, ok := privateArtifacts.(workflowMediaPrivateArtifacts)
	if !ok || len(private.Screenshots) != 1 || len(private.DVDMenus) != 0 {
		t.Fatalf("private media artifacts = %#v", privateArtifacts)
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal workflow media: %v", err)
	}
	if strings.Contains(string(payload), "C:\\\\private") || strings.Contains(string(payload), "screen.png") || strings.Contains(string(payload), "menu.png") {
		t.Fatalf("public media exposed private path: %s", payload)
	}

	changedRelease := release
	changedRelease.Generation++
	changed, _, err := builder.Build(context.Background(), changedRelease, projections, api.MediaCaptureInstructions{}, time.Now())
	if err != nil {
		t.Fatalf("build changed-generation media: %v", err)
	}
	if changed.CaptureFingerprint == snapshot.CaptureFingerprint {
		t.Fatal("media capture fingerprint ignored release generation")
	}
}

func TestWorkflowMediaCaptureUsesProjectedScreenshotOverride(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		count         *int
		minimum       int
		want          int
		withoutImages bool
	}{
		{name: "configured default", want: 7},
		{
			name:  "lower override",
			count: new(2),
			want:  2,
		},
		{name: "zero override", count: new(0)},
		{name: "no-image tracker configured default", withoutImages: true},
		{
			name:          "no-image tracker explicit count",
			count:         new(7),
			withoutImages: true,
		},
		{
			name:    "tracker minimum",
			count:   new(0),
			minimum: 3,
			want:    3,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{
				ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 7},
				Trackers:           config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ONE": {ImageCount: test.minimum}}},
			}
			registry := mediaImageHostRegistry(t)
			trackerID := api.TrackerID("ONE")
			if test.withoutImages {
				trackerID = "NONE"
				if err := registry.RegisterDescriptor(trackers.Descriptor{
					Name:              "NONE",
					Definition:        mediaImageHostDefinition("NONE"),
					UploadContentMode: trackers.UploadContentModeNone,
					Family:            trackers.FamilyStandalone,
					BaseURL:           "https://none.example.invalid",
				}); err != nil {
					t.Fatal(err)
				}
			}
			projector, err := trackers.NewWorkflowProjector(registry, cfg, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, projections, err := projector.Build(t.Context(), api.ReleaseSnapshot{}, api.UploadSubject{},
				[]api.TrackerID{trackerID}, map[api.TrackerID]api.TrackerProjectionInstructions{trackerID: {ScreenshotCount: test.count}},
				nil, api.WorkflowExecutionModeNormal)
			if err != nil {
				t.Fatal(err)
			}
			plan := api.ScreenshotPlan{}
			for index := range test.want {
				plan.SuggestedSelections = append(plan.SuggestedSelections, api.ScreenshotSelection{Index: index + 1, TimestampSeconds: float64(index+1) * 60})
			}
			screenshots := &workflowScreenshotFake{root: t.TempDir(), plan: &plan}
			builder := workflowMediaBuilder{
				config:      cfg,
				resolver:    workflowMediaResolverFake{},
				screenshots: screenshots,
			}
			instructions := api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal}
			snapshot, _, err := builder.Build(t.Context(), api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1},
				projections, instructions, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Artifacts) != test.want {
				t.Fatalf("captured %d screenshots, want %d", len(snapshot.Artifacts), test.want)
			}
			if test.want == 0 {
				if screenshots.plans != 0 || screenshots.captures != 0 {
					t.Fatal("zero override invoked screenshot capture")
				}
			} else if !slices.Equal(screenshots.planCounts, []int{test.want}) {
				t.Fatalf("capture plan counts = %v, want %d", screenshots.planCounts, test.want)
			}
		})
	}
}

func TestWorkflowMediaBuilderReportsFrameCorruption(t *testing.T) {
	t.Parallel()

	screenshots := &workflowScreenshotFake{root: t.TempDir(), err: fmt.Errorf("synthetic: %w", internalerrors.ErrFrameCorruption)}
	builder := workflowMediaBuilder{resolver: workflowMediaResolverFake{}, screenshots: screenshots}
	var progress []api.WorkflowProgressUpdate
	ctx := api.WithWorkflowProgressReporter(context.Background(), func(update api.WorkflowProgressUpdate) {
		progress = append(progress, update)
	})
	snapshot, _, err := builder.Build(
		ctx,
		api.ReleaseRef{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026"), Generation: 1},
		api.TrackerReleaseProjectionSet{
			ID:       "projections-corruption",
			Revision: 1,
			Projections: []api.TrackerReleaseProjection{{
				TrackerID: "SYNTHETIC",
				Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1},
			}},
		},
		api.MediaCaptureInstructions{},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build workflow media: %v", err)
	}
	if snapshot.Status != api.StageStatusFailed || len(snapshot.Failures) != 1 {
		t.Fatalf("frame corruption snapshot = %#v", snapshot)
	}
	if snapshot.Artifacts == nil {
		t.Fatal("frame corruption artifacts = nil, want empty array")
	}
	if got := snapshot.Failures[0].Failure.Message; got != "Screenshot frame corruption detected. Repair source media before retrying." {
		t.Fatalf("frame corruption failure = %q", got)
	}
	if !slices.ContainsFunc(progress, func(update api.WorkflowProgressUpdate) bool {
		return update.ItemID == "screenshots" && update.Status == api.StageStatusFailed
	}) {
		t.Fatalf("frame corruption progress was not emitted: %#v", progress)
	}
}

func TestWorkflowMediaBuilderPartialScreenshotCaptureUsesSurvivingMinimum(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		required         int
		wantStatus       api.StageStatus
		wantRequiredTask bool
	}{
		{
			name:       "minimum met",
			required:   1,
			wantStatus: api.StageStatusCompleted,
		},
		{
			name:             "minimum not met",
			required:         2,
			wantStatus:       api.StageStatusBlocked,
			wantRequiredTask: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			capture := api.ScreenshotResult{
				Images: []api.ScreenshotImage{{
					Index:            0,
					TimestampSeconds: 10,
					Path:             filepath.Join(t.TempDir(), "screen-0.png"),
					Purpose:          api.ScreenshotPurposeFinal,
				}},
				Errors: []api.ScreenshotError{{Index: 1, Message: "synthetic capture failure"}},
			}
			screenshots := &workflowScreenshotFake{root: t.TempDir(), result: &capture}
			builder := workflowMediaBuilder{resolver: workflowMediaResolverFake{}, screenshots: screenshots}
			var progress []api.WorkflowProgressUpdate
			ctx := api.WithWorkflowProgressReporter(context.Background(), func(update api.WorkflowProgressUpdate) {
				progress = append(progress, update)
			})
			snapshot, privateArtifacts, err := builder.Build(
				ctx,
				api.ReleaseRef{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026"), Generation: 1},
				api.TrackerReleaseProjectionSet{
					ID:       api.TrackerReleaseProjectionSetID("projections-partial-" + testCase.name),
					Revision: 1,
					Projections: []api.TrackerReleaseProjection{{
						TrackerID: "SYNTHETIC",
						Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: testCase.required},
					}},
				},
				api.MediaCaptureInstructions{
					Purpose: api.ScreenshotPurposeFinal,
					Selections: []api.ScreenshotSelection{
						{Index: 0, TimestampSeconds: 10},
						{Index: 1, TimestampSeconds: 20},
					},
				},
				time.Now(),
			)
			if err != nil {
				t.Fatalf("build workflow media: %v", err)
			}
			if snapshot.Status != testCase.wantStatus || len(snapshot.Artifacts) != 1 || len(snapshot.Failures) != 0 {
				t.Fatalf("partial capture snapshot = %#v", snapshot)
			}
			if got := len(snapshot.RequiredActions) > 0; got != testCase.wantRequiredTask {
				t.Fatalf("required action present = %t, want %t: %#v", got, testCase.wantRequiredTask, snapshot.RequiredActions)
			}
			private, ok := privateArtifacts.(workflowMediaPrivateArtifacts)
			if !ok || len(private.Screenshots) != 1 {
				t.Fatalf("partial private artifacts = %#v", privateArtifacts)
			}
			if !slices.ContainsFunc(progress, func(update api.WorkflowProgressUpdate) bool {
				return update.ItemID == "screenshots" && update.Status == api.StageStatusPartial &&
					update.Completed == 1 && update.Total == 2 && strings.Contains(update.Message, "partial coverage")
			}) {
				t.Fatalf("partial screenshot diagnostics were not emitted: %#v", progress)
			}
		})
	}
}

func TestReconcileRetriedImageHostFailuresClearsOnlySatisfiedTracker(t *testing.T) {
	t.Parallel()

	const (
		alpha api.TrackerID = "ALPHA"
		beta  api.TrackerID = "BETA"
	)
	artifacts := []api.MediaArtifact{
		{
			ID:       "screen-0",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
		},
		{
			ID:       "hosted-0",
			Kind:     api.MediaArtifactHostedImage,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
			Source:   "screen-0",
		},
		{
			ID:       "screen-1",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
		},
		{
			ID:       "hosted-1",
			Kind:     api.MediaArtifactHostedImage,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
			Source:   "screen-1",
		},
	}
	priorAttempts := []api.HostedImageAttempt{
		{
			Host:       "primary",
			UsageScope: "tracker:ALPHA",
			TrackerIDs: []api.TrackerID{alpha},
			Status:     api.StageStatusFailed,
			Results:    []api.MediaArtifact{artifacts[1]},
		},
		{
			Host:       "fallback",
			UsageScope: "tracker:ALPHA",
			TrackerIDs: []api.TrackerID{alpha},
			Status:     api.StageStatusFailed,
		},
	}
	priorFailures := []api.WorkflowFailure{
		hostedImageFailure(alpha, "primary", "old primary failure"),
		hostedImageFailure(alpha, "fallback", "old fallback failure"),
		hostedImageFailure(beta, "primary", "old sibling failure"),
		hostedImageFailure("", "primary", "unscoped failure"),
	}
	projections := []api.TrackerReleaseProjection{
		{TrackerID: alpha, Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 2}},
		{TrackerID: beta, Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1}},
	}
	currentFailures := []api.WorkflowFailure{hostedImageFailure(beta, "primary", "current sibling failure")}

	for _, test := range []struct {
		name              string
		required          int
		results           []api.MediaArtifact
		wantAlphaFailures int
	}{
		{
			name:     "successful retry restores satisfied tracker",
			required: 2,
			results:  []api.MediaArtifact{artifacts[3]},
		},
		{
			name:              "completed attempt without required sources retains failures",
			required:          2,
			wantAlphaFailures: 2,
		},
		{name: "successful retry satisfies zero screenshot requirement"},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := api.MediaArtifactSet{
				Artifacts:    append([]api.MediaArtifact(nil), artifacts...),
				HostAttempts: append([]api.HostedImageAttempt(nil), priorAttempts...),
				FailedHosts:  []string{"fallback", "primary"},
				Failures:     append([]api.WorkflowFailure(nil), priorFailures...),
			}
			attempts := []api.HostedImageAttempt{
				{
					Host:       "primary",
					UsageScope: "tracker:ALPHA",
					TrackerIDs: []api.TrackerID{alpha},
					Status:     api.StageStatusCompleted,
					Results:    test.results,
				},
				{
					Host:       "primary",
					UsageScope: "tracker:BETA",
					TrackerIDs: []api.TrackerID{beta},
					Status:     api.StageStatusFailed,
				},
			}

			testProjections := append([]api.TrackerReleaseProjection(nil), projections...)
			testProjections[0].Artifacts.ScreenshotCount = test.required
			reconcileRetriedImageHostFailures(&snapshot, testProjections, attempts, currentFailures)
			alphaFailures, betaFailures, unscopedFailures := 0, 0, 0
			for _, failure := range snapshot.Failures {
				switch failure.TrackerID {
				case alpha:
					alphaFailures++
				case beta:
					betaFailures++
					if failure.Failure.Message != "current sibling failure" {
						t.Fatalf("sibling retry failure was not replaced: %#v", failure)
					}
				case "":
					unscopedFailures++
				}
			}
			if alphaFailures != test.wantAlphaFailures || betaFailures != 1 || unscopedFailures != 1 {
				t.Fatalf(
					"retained image-host failures alpha=%d beta=%d unscoped=%d: %#v",
					alphaFailures,
					betaFailures,
					unscopedFailures,
					snapshot.Failures,
				)
			}
			if len(snapshot.HostAttempts) != len(priorAttempts) || !slices.Equal(snapshot.FailedHosts, []string{"fallback", "primary"}) {
				t.Fatalf("historical diagnostics changed: attempts=%#v failedHosts=%v", snapshot.HostAttempts, snapshot.FailedHosts)
			}
		})
	}
}

func TestWorkflowMediaBuilderExplicitDVDMenuCaptureUsesCap(t *testing.T) {
	t.Parallel()

	dvdMenus := &workflowDVDMenuFake{}
	builder := workflowMediaBuilder{
		config:   config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{MaxMenuItems: 6}},
		resolver: workflowMediaResolverFake{},
		dvdMenus: dvdMenus,
	}
	snapshot, privateArtifacts, err := builder.Build(
		context.Background(),
		api.ReleaseRef{SourcePath: "C:\\releases\\Example.Release.2026", Generation: 1},
		api.TrackerReleaseProjectionSet{ID: "projections-menu", Revision: 1},
		api.MediaCaptureInstructions{
			Purpose:         api.ScreenshotPurposeMenu,
			CaptureDVDMenus: true,
			MaxDVDMenuItems: 4,
		},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build explicit DVD menus: %v", err)
	}
	private, ok := privateArtifacts.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatalf("private artifacts type = %T", privateArtifacts)
	}
	if snapshot.Status != api.StageStatusCompleted || len(snapshot.Artifacts) != 1 || len(private.DVDMenus) != 1 {
		t.Fatalf("explicit menu capture = %#v private=%#v", snapshot, private)
	}
	if dvdMenus.captures != 1 || len(dvdMenus.maxItems) != 1 || dvdMenus.maxItems[0] != 4 {
		t.Fatalf("menu calls=%d caps=%v", dvdMenus.captures, dvdMenus.maxItems)
	}
	if snapshot.Artifacts[0].Source != api.ScreenshotSelectionSourceDVDMenu {
		t.Fatalf("automatic menu source = %q", snapshot.Artifacts[0].Source)
	}
}

func TestWorkflowMediaBuilderDoesNotHideCaptureRequiredDVDMenus(t *testing.T) {
	t.Parallel()

	dvdMenus := &workflowDVDMenuFake{}
	builder := workflowMediaBuilder{
		resolver: workflowMediaResolverFake{},
		dvdMenus: dvdMenus,
	}
	snapshot, _, err := builder.Build(
		context.Background(),
		api.ReleaseRef{SourcePath: "C:\\releases\\Example.Release.2026", Generation: 1},
		api.TrackerReleaseProjectionSet{
			ID:       "projections-required-menu",
			Revision: 1,
			Projections: []api.TrackerReleaseProjection{{
				TrackerID: "SYNTHETIC",
				Artifacts: api.TrackerArtifactRequirements{DVDMenuCount: 2},
			}},
		},
		api.MediaCaptureInstructions{},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build required DVD menus: %v", err)
	}
	if snapshot.Status != api.StageStatusBlocked || len(snapshot.RequiredActions) != 1 || dvdMenus.captures != 0 {
		t.Fatalf("required menu result = %#v captures=%d", snapshot, dvdMenus.captures)
	}
}

func TestWorkflowMediaBuilderMenuRecaptureReplacesAutomaticAndPreservesManual(t *testing.T) {
	t.Parallel()

	dvdMenus := &workflowDVDMenuFake{result: &api.DVDMenuCaptureResult{
		Images: []api.DVDMenuCaptureImage{{
			Path:    "new-auto-menu.png",
			Purpose: api.ScreenshotPurposeMenu}},
		Partial: true,
		Warnings: []api.DVDMenuCaptureWarning{{
			Code: "partial_coverage", Message: "Synthetic partial coverage.",
		}},
	}}
	builder := workflowMediaBuilder{resolver: workflowMediaResolverFake{}, dvdMenus: dvdMenus}
	existing := api.MediaArtifactSet{
		CaptureFingerprint:      workflowTestFingerprint(t, "existing-media"),
		RequirementsFingerprint: workflowTestFingerprint(t, "requirements"),
		Status:                  api.StageStatusCompleted,
		Artifacts: []api.MediaArtifact{
			{
				ID:       "screen",
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
				Order:    0,
			},
			{
				ID:       "manual-menu",
				Kind:     api.MediaArtifactDVDMenu,
				Purpose:  api.ScreenshotPurposeMenu,
				Selected: true,
				Order:    1,
				Source:   api.ScreenshotSelectionSourceMenu,
			},
			{
				ID:       "auto-menu",
				Kind:     api.MediaArtifactDVDMenu,
				Purpose:  api.ScreenshotPurposeMenu,
				Selected: true,
				Order:    2,
				Source:   api.ScreenshotSelectionSourceDVDMenu,
			},
			{
				ID:       "hosted-auto",
				Kind:     api.MediaArtifactHostedImage,
				Purpose:  api.ScreenshotPurposeMenu,
				Selected: true,
				Order:    3,
				Source:   "auto-menu",
			},
		},
	}
	retained := workflowMediaPrivateArtifacts{
		Screenshots: []api.ScreenshotImage{{Path: "screen.png", Purpose: api.ScreenshotPurposeFinal}},
		DVDMenus: []api.DVDMenuCaptureImage{
			{Path: "manual-menu.png", Purpose: api.ScreenshotPurposeMenu},
			{Path: "old-auto-menu.png", Purpose: api.ScreenshotPurposeMenu},
		},
		ArtifactImages: map[api.PublicResourceID]api.ScreenshotImage{
			"screen":      {Path: "screen.png", Purpose: api.ScreenshotPurposeFinal},
			"manual-menu": {Path: "manual-menu.png", Purpose: api.ScreenshotPurposeMenu},
			"auto-menu":   {Path: "old-auto-menu.png", Purpose: api.ScreenshotPurposeMenu},
		},
		DVDMenuImages: map[api.PublicResourceID]api.DVDMenuCaptureImage{
			"manual-menu": {ScreenshotImage: api.ScreenshotImage{Path: "manual-menu.png", Purpose: api.ScreenshotPurposeMenu}},
			"auto-menu":   {ScreenshotImage: api.ScreenshotImage{Path: "old-auto-menu.png", Purpose: api.ScreenshotPurposeMenu}},
		},
		HostedImages: map[api.PublicResourceID]api.UploadedImageLink{
			"hosted-auto": {ImagePath: "old-auto-menu.png", RawURL: "https://img.example/old-auto.png"},
		},
		HostedSources: map[api.PublicResourceID]api.PublicResourceID{"hosted-auto": "auto-menu"},
		commitState:   &workflowMediaCommitState{},
	}
	var progress []api.WorkflowProgressUpdate
	ctx := api.WithWorkflowProgressReporter(context.Background(), func(update api.WorkflowProgressUpdate) {
		progress = append(progress, update)
	})

	combined, privateResult, err := builder.BuildIncremental(
		ctx,
		api.ReleaseRef{SourcePath: "C:\\releases\\Example.Release.2026", Generation: 1},
		api.TrackerReleaseProjectionSet{ID: "projections-menu-refresh", Revision: 1},
		api.MediaCaptureInstructions{
			Purpose:         api.ScreenshotPurposeMenu,
			CaptureDVDMenus: true,
			MaxDVDMenuItems: 4,
		},
		&existing,
		retained,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("refresh automatic menus: %v", err)
	}
	if combined.Status != api.StageStatusCompleted {
		t.Fatalf("partial optional menu capture blocked media: %#v", combined)
	}
	if !slices.ContainsFunc(progress, func(update api.WorkflowProgressUpdate) bool {
		return update.ItemID == "dvd_menus" && update.Status == api.StageStatusPartial
	}) {
		t.Fatalf("partial menu diagnostics were not emitted: %#v", progress)
	}
	if countMediaArtifacts(combined.Artifacts, api.MediaArtifactScreenshot) != 1 ||
		countMediaArtifacts(combined.Artifacts, api.MediaArtifactDVDMenu) != 2 ||
		countMediaArtifacts(combined.Artifacts, api.MediaArtifactHostedImage) != 0 {
		t.Fatalf("refreshed media = %#v", combined.Artifacts)
	}
	if !slices.ContainsFunc(combined.Artifacts, func(artifact api.MediaArtifact) bool {
		return artifact.ID == "manual-menu" && artifact.Source == api.ScreenshotSelectionSourceMenu
	}) {
		t.Fatalf("manual menu was not preserved: %#v", combined.Artifacts)
	}
	if slices.ContainsFunc(combined.Artifacts, func(artifact api.MediaArtifact) bool {
		return artifact.ID == "auto-menu" || artifact.ID == "hosted-auto"
	}) {
		t.Fatalf("old automatic menu revision remains: %#v", combined.Artifacts)
	}
	private, ok := privateResult.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatalf("private result type = %T", privateResult)
	}
	if len(private.Screenshots) != 1 || private.Screenshots[0].Path != "screen.png" || len(private.DVDMenus) != 2 {
		t.Fatalf("private refreshed media = %#v", private)
	}
}

func TestWorkflowMediaBuilderOptionalMenuFailurePreservesReadyNormalMedia(t *testing.T) {
	t.Parallel()

	builder := workflowMediaBuilder{
		resolver: workflowMediaResolverFake{},
		dvdMenus: &workflowDVDMenuFake{err: errors.New("synthetic menu engine unavailable")},
	}
	existing := api.MediaArtifactSet{
		CaptureFingerprint:      workflowTestFingerprint(t, "ready-normal-media"),
		RequirementsFingerprint: workflowTestFingerprint(t, "requirements"),
		Status:                  api.StageStatusCompleted,
		Artifacts: []api.MediaArtifact{{
			ID:       "screen",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
		}},
	}
	retained := workflowMediaPrivateArtifacts{
		Screenshots:    []api.ScreenshotImage{{Path: "screen.png", Purpose: api.ScreenshotPurposeFinal}},
		ArtifactImages: map[api.PublicResourceID]api.ScreenshotImage{"screen": {Path: "screen.png", Purpose: api.ScreenshotPurposeFinal}},
		commitState:    &workflowMediaCommitState{},
	}
	var progress []api.WorkflowProgressUpdate
	ctx := api.WithWorkflowProgressReporter(context.Background(), func(update api.WorkflowProgressUpdate) {
		progress = append(progress, update)
	})

	combined, privateResult, err := builder.BuildIncremental(
		ctx,
		api.ReleaseRef{SourcePath: "C:\\releases\\Example.Release.2026", Generation: 1},
		api.TrackerReleaseProjectionSet{ID: "projections-optional-menu", Revision: 1},
		api.MediaCaptureInstructions{
			Purpose:         api.ScreenshotPurposeMenu,
			CaptureDVDMenus: true,
			MaxDVDMenuItems: 4,
		},
		&existing,
		retained,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("optional menu failure: %v", err)
	}
	if combined.Status != api.StageStatusCompleted || len(combined.Artifacts) != 1 || len(combined.Failures) != 1 {
		t.Fatalf("optional menu failure poisoned ready media: %#v", combined)
	}
	if !slices.ContainsFunc(progress, func(update api.WorkflowProgressUpdate) bool {
		return update.ItemID == "dvd_menus" && update.Status == api.StageStatusFailed
	}) {
		t.Fatalf("optional menu failure diagnostic was not emitted: %#v", progress)
	}
	private, ok := privateResult.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatalf("private result type = %T", privateResult)
	}
	if len(private.Screenshots) != 1 || private.Screenshots[0].Path != "screen.png" {
		t.Fatalf("ready normal media was not retained: %#v", private)
	}
}

func TestWorkflowMediaBuilderRepeatedCaptureIsNoOp(t *testing.T) {
	t.Parallel()

	screenshots := &workflowScreenshotFake{root: t.TempDir()}
	builder := workflowMediaBuilder{
		resolver:    workflowMediaResolverFake{},
		screenshots: screenshots,
	}
	projections := api.TrackerReleaseProjectionSet{
		ID:       "projections-1",
		Revision: 4,
		Projections: []api.TrackerReleaseProjection{{
			TrackerID: "ALPHA",
			Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 2},
		}},
	}
	instructions := api.MediaCaptureInstructions{
		Purpose: api.ScreenshotPurposeFinal,
		Selections: []api.ScreenshotSelection{
			{Index: 1, TimestampSeconds: 60},
			{Index: 2, TimestampSeconds: 120},
		},
	}
	release := api.ReleaseRef{SourcePath: "C:\\releases\\Example.Release.2026", Generation: 1}
	first, retained, err := builder.BuildIncremental(
		context.Background(),
		release,
		projections,
		instructions,
		nil,
		nil,
		time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("first incremental capture: %v", err)
	}
	if len(first.Artifacts) != 2 || screenshots.captures != 1 {
		t.Fatalf("first media = %#v captures=%d", first, screenshots.captures)
	}

	second, _, err := builder.BuildIncremental(
		context.Background(),
		release,
		projections,
		instructions,
		&first,
		retained,
		time.Date(2026, time.July, 20, 12, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("repeated incremental capture: %v", err)
	}
	if second.Status != first.Status || len(second.Artifacts) != len(first.Artifacts) || screenshots.captures != 1 {
		t.Fatalf("repeated media = %#v captures=%d", second, screenshots.captures)
	}
	for index := range first.Artifacts {
		if second.Artifacts[index].ID != first.Artifacts[index].ID {
			t.Fatalf("artifact %d changed across no-op capture: before=%q after=%q", index, first.Artifacts[index].ID, second.Artifacts[index].ID)
		}
	}

	// The live lifecycle probe adds a spare slot without changing retained choices.
	first.Artifacts[0].Selected = false
	first.Artifacts[0].Order, first.Artifacts[1].Order = first.Artifacts[1].Order, first.Artifacts[0].Order
	instructions.Selections = append(instructions.Selections, api.ScreenshotSelection{Index: 3, TimestampSeconds: 180})
	third, _, err := builder.BuildIncremental(t.Context(), release, projections, instructions, &first, retained, time.Now())
	if err != nil {
		t.Fatalf("additional incremental capture: %v", err)
	}
	if len(third.Artifacts) != 3 || screenshots.captures != 2 {
		t.Fatalf("extended media = %#v captures=%d", third, screenshots.captures)
	}
	for index := range first.Artifacts {
		before, after := first.Artifacts[index], third.Artifacts[index]
		if after.ID != before.ID || after.Selected != before.Selected || after.Order != before.Order || after.TimestampSeconds != before.TimestampSeconds {
			t.Fatalf("retained artifact changed during spare capture: before=%#v after=%#v", before, after)
		}
	}
	if third.Artifacts[2].Index != 3 {
		t.Fatalf("spare frame used the wrong slot: %#v", third.Artifacts[2])
	}
}

func TestWorkflowMediaBuilderRetainsMatchingExistingScreenshots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	existing := api.ScreenshotImage{
		Index:            1,
		TimestampSeconds: 600,
		Path:             filepath.Join(root, "existing.png"),
		Purpose:          api.ScreenshotPurposeFinal,
		Width:            1920,
		Height:           1080,
		SizeBytes:        1234,
	}
	screenshots := &workflowScreenshotFake{
		root: root,
		plan: &api.ScreenshotPlan{
			SuggestedSelections: []api.ScreenshotSelection{
				{Index: 2, TimestampSeconds: 1200},
				{Index: 3, TimestampSeconds: 1800},
				{Index: 4, TimestampSeconds: 2400},
			},
			ExistingScreenshots: []api.ScreenshotImage{existing},
		},
	}
	builder := workflowMediaBuilder{
		resolver:    workflowMediaResolverFake{},
		screenshots: screenshots,
	}
	projections := api.TrackerReleaseProjectionSet{
		ID:       "projections-existing",
		Revision: 4,
		Projections: []api.TrackerReleaseProjection{{
			TrackerID: "ALPHA",
			Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 4},
		}},
	}
	snapshot, retained, err := builder.Build(
		context.Background(),
		api.ReleaseRef{SourcePath: filepath.Join(root, "Example.Release.2026.1080p-GRP.mkv"), Generation: 1},
		projections,
		api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal, ScreenshotCount: 4},
		time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("build workflow media with existing screenshot: %v", err)
	}
	privateArtifacts, ok := retained.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatalf("private media artifacts = %#v", retained)
	}
	if snapshot.Status != api.StageStatusCompleted || len(snapshot.Artifacts) != 4 || len(privateArtifacts.Screenshots) != 4 ||
		screenshots.captures != 1 {
		t.Fatalf(
			"media capture = %#v private_screenshots=%d captures=%d",
			snapshot,
			len(privateArtifacts.Screenshots),
			screenshots.captures,
		)
	}
	if privateArtifacts.Screenshots[0].Path != existing.Path {
		t.Fatalf("existing screenshot was not retained: %#v", privateArtifacts.Screenshots)
	}
}

func TestWorkflowMediaBuilderUsesOnlyExistingScreenshotsWithoutCapture(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	existing := make([]api.ScreenshotImage, 4)
	for index := range existing {
		existing[index] = api.ScreenshotImage{
			Index:            index + 1,
			TimestampSeconds: float64((index + 1) * 600),
			Path:             filepath.Join(root, fmt.Sprintf("existing-%d.png", index+1)),
			Purpose:          api.ScreenshotPurposeFinal,
			Width:            1920,
			Height:           1080,
			SizeBytes:        1234,
		}
	}
	screenshots := &workflowScreenshotFake{
		root: root,
		plan: &api.ScreenshotPlan{ExistingScreenshots: existing},
	}
	builder := workflowMediaBuilder{
		resolver:    workflowMediaResolverFake{},
		screenshots: screenshots,
	}
	snapshot, retained, err := builder.Build(
		context.Background(),
		api.ReleaseRef{SourcePath: filepath.Join(root, "Example.Release.2026.1080p-GRP.mkv"), Generation: 1},
		api.TrackerReleaseProjectionSet{
			ID:       "projections-existing-only",
			Revision: 4,
			Projections: []api.TrackerReleaseProjection{{
				TrackerID: "ALPHA",
				Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 4},
			}},
		},
		api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal, ScreenshotCount: 4},
		time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("build workflow media from existing screenshots: %v", err)
	}
	privateArtifacts, ok := retained.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatalf("private media artifacts = %#v", retained)
	}
	if snapshot.Status != api.StageStatusCompleted || len(snapshot.Artifacts) != 4 || len(privateArtifacts.Screenshots) != 4 ||
		screenshots.captures != 0 {
		t.Fatalf(
			"existing media = %#v private_screenshots=%d captures=%d",
			snapshot,
			len(privateArtifacts.Screenshots),
			screenshots.captures,
		)
	}
}

func TestWorkflowMediaBuilderHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := (workflowMediaBuilder{resolver: workflowMediaResolverFake{}}).Build(
		ctx,
		api.ReleaseRef{},
		api.TrackerReleaseProjectionSet{},
		api.MediaCaptureInstructions{},
		time.Now(),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled media build error = %v", err)
	}
}

func TestWorkflowMediaPrivateResourceReadsOpaqueArtifact(t *testing.T) {
	t.Parallel()

	imagePath := filepath.Join(t.TempDir(), "screenshot.png")
	if err := os.WriteFile(imagePath, []byte("synthetic image"), 0o600); err != nil {
		t.Fatalf("write screenshot: %v", err)
	}
	resource := workflowMediaPrivateArtifacts{
		Screenshots: []api.ScreenshotImage{{Path: imagePath}},
	}
	snapshot := api.MediaArtifactSet{Artifacts: []api.MediaArtifact{{
		ID:   "artifact-1",
		Kind: api.MediaArtifactScreenshot,
	}}}
	content, err := resource.OpenArtifact(context.Background(), snapshot, "artifact-1")
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	payload, err := io.ReadAll(content.Body)
	_ = content.Body.Close()
	if err != nil {
		t.Fatalf("read artifact content: %v", err)
	}
	if string(payload) != "synthetic image" || content.ContentType != "image/png" {
		t.Fatal("artifact content did not match the synthetic fixture")
	}
	if _, err := resource.OpenArtifact(context.Background(), snapshot, "missing"); err == nil {
		t.Fatal("expected unknown opaque artifact rejection")
	}
}

func TestWorkflowMediaPrivateResourceDeletesThroughManagedServices(t *testing.T) {
	t.Parallel()

	screenshots := &workflowScreenshotFake{root: t.TempDir()}
	dvdMenus := &workflowDVDMenuFake{}
	resource := workflowMediaPrivateArtifacts{
		Screenshots: []api.ScreenshotImage{{Path: "C:\\private\\screen.png"}},
		DVDMenus: []api.DVDMenuCaptureImage{{
			Path:      "C:\\private\\menu.png",
			Discovery: api.DVDMenuDiscoveryReachable,
		}},
		screenshotService: screenshots,
		dvdMenuService:    dvdMenus,
	}
	snapshot := api.MediaArtifactSet{Artifacts: []api.MediaArtifact{
		{ID: "screen-1", Kind: api.MediaArtifactScreenshot},
		{ID: "menu-1", Kind: api.MediaArtifactDVDMenu},
	}}
	retained, err := resource.DeleteArtifacts(context.Background(), snapshot, []api.PublicResourceID{"screen-1"})
	if err != nil {
		t.Fatalf("delete screenshot: %v", err)
	}
	updated, ok := retained.(workflowMediaPrivateArtifacts)
	if !ok || len(updated.Screenshots) != 0 || len(updated.DVDMenus) != 1 ||
		updated.DVDMenus[0].Discovery != api.DVDMenuDiscoveryReachable {
		t.Fatalf("retained media = %#v", retained)
	}
	if len(screenshots.deleted) != 0 || len(dvdMenus.deleted) != 0 {
		t.Fatalf("media deletion ran before commit screenshots=%v menus=%v", screenshots.deleted, dvdMenus.deleted)
	}
	if err := updated.Commit(context.Background()); err != nil {
		t.Fatalf("commit screenshot deletion: %v", err)
	}
	if len(screenshots.deleted) != 1 || screenshots.deleted[0] != "C:\\private\\screen.png" || len(dvdMenus.deleted) != 0 {
		t.Fatalf("service deletions screenshots=%v menus=%v", screenshots.deleted, dvdMenus.deleted)
	}
}

// TestWorkflowMediaCommitPropagatesMenuDeletionFailure keeps menu-deletion
// idempotency inside the menu service. The workflow cannot tell an already-absent
// image from one whose cleanup failed and left local state behind, so it treats
// every error as unfinished work and keeps the deletion pending for a retry.
func TestWorkflowMediaCommitPropagatesMenuDeletionFailure(t *testing.T) {
	t.Parallel()

	menuPath := "C:\\private\\menu.png"
	dvdMenus := &workflowDVDMenuFake{
		deleteErr: fmt.Errorf("DVD menus: delete records: %w", internalerrors.ErrNotFound),
	}
	resource := workflowMediaPrivateArtifacts{
		DVDMenus: []api.DVDMenuCaptureImage{{
			Path:      menuPath,
			Discovery: api.DVDMenuDiscoveryReachable,
		}},
		dvdMenuService: dvdMenus,
	}
	snapshot := api.MediaArtifactSet{Artifacts: []api.MediaArtifact{{
		ID:   "menu-1",
		Kind: api.MediaArtifactDVDMenu,
	}}}
	retained, err := resource.DeleteArtifacts(context.Background(), snapshot, []api.PublicResourceID{"menu-1"})
	if err != nil {
		t.Fatalf("delete menu artifact: %v", err)
	}
	updated, ok := retained.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatalf("retained media = %#v", retained)
	}
	if err := updated.Commit(context.Background()); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("commit error = %v, want the menu service failure", err)
	}
	if pending := updated.pendingDeletes(); len(pending) != 1 || pending[0].path != menuPath {
		t.Fatalf("failed menu deletion did not stay pending: %#v", pending)
	}
}

func TestRemoveHostedImagesUsesDVDMenuBindingForLegacyLink(t *testing.T) {
	t.Parallel()

	binding := api.PreparedMediaBinding{
		SourcePath:               "C:\\source\\release",
		PreparedMediaFingerprint: "prepared-fingerprint",
		PreparedGeneration:       1,
	}
	artifactID := api.PublicResourceID("hosted-menu")
	retained := workflowMediaPrivateArtifacts{
		HostedImages: map[api.PublicResourceID]api.UploadedImageLink{
			artifactID: {
				ImagePath: "C:\\private\\menu.png",
				Host:      "example-host",
			},
		},
		HostedSources: map[api.PublicResourceID]api.PublicResourceID{artifactID: "menu"},
		dvdMenuSubject: api.DVDMenuSubject{
			MediaBinding: binding,
		},
		commitState: &workflowMediaCommitState{},
	}
	snapshot := api.MediaArtifactSet{Artifacts: []api.MediaArtifact{{
		ID:   artifactID,
		Kind: api.MediaArtifactHostedImage,
	}}}

	_, privateResult, err := (workflowMediaBuilder{}).RemoveHostedImages(
		context.Background(),
		api.ReleaseRef{},
		snapshot,
		retained,
		[]api.PublicResourceID{artifactID},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("remove hosted menu image: %v", err)
	}
	updated, ok := privateResult.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatalf("retained media = %#v", privateResult)
	}
	if pending := updated.pendingDeletes(); len(pending) != 1 || !pending[0].binding.Equal(binding) {
		t.Fatalf("pending hosted menu deletion = %#v, want binding %#v", pending, binding)
	}
}
