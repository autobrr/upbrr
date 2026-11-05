// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type nativeWorkflowHost struct {
	mu sync.Mutex
	countingWorkflowImageHost
	imports    int
	importErr  error
	failSource string
	failHost   string
	active     *nativeEffectReporter
}

func (h *nativeWorkflowHost) Upload(ctx context.Context, meta api.ImageHostingSubject, host, scope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	links, err := h.countingWorkflowImageHost.Upload(ctx, meta, host, scope, images)
	if host == h.failHost {
		return nil, errors.New("known host rejection")
	}
	return links, err
}

func (h *nativeWorkflowHost) ImportURLs(_ context.Context, meta api.ImageHostingSubject, host, scope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	if h.active == nil || !h.active.started || h.active.completed {
		return nil, errors.New("native import escaped image effect authority")
	}
	var links []api.UploadedImageLink
	for _, image := range images {
		h.imports++
		if h.importErr != nil {
			return links, h.importErr
		}
		if image.RawURL == h.failSource {
			return links, errors.New("known source rejection")
		}
		if !slices.Contains([]string{"https://unapproved.example/source.png", "https://unapproved.example/second.png"}, image.RawURL) {
			return links, errors.New("original remote association lost")
		}
		links = append(links, api.UploadedImageLink{
			SourcePath:               meta.MediaBinding.SourcePath,
			PreparedMediaFingerprint: meta.MediaBinding.PreparedMediaFingerprint,
			PreparedGeneration:       meta.MediaBinding.PreparedGeneration,
			Host:                     host,
			UsageScope:               scope,
			ImagePath:                image.Path,
			Purpose:                  image.Purpose,
			DiscID:                   image.DiscID,
			ImgURL:                   "https://kshare.club/" + filepath.Base(image.Path),
			RawURL:                   "https://kshare.club/" + filepath.Base(image.Path),
			WebURL:                   "https://kshare.club/" + filepath.Base(image.Path),
			ContentSHA256:            image.ContentSHA256,
			UploadedAt:               time.Now().UTC(),
		})
	}
	return links, nil
}

type nativeEffectReporter struct {
	started, completed, unknown, succeeded bool
	fingerprints                           []api.WorkflowFingerprint
}

type nativeInventoryScreenshots struct {
	mediaScreenshotService
	workflowPreflightImagesFake
}

type nativeDescriptionDefinition struct{ screenshots *[]api.ScreenshotImage }

func (nativeDescriptionDefinition) Name() string { return "GPW" }
func (d nativeDescriptionDefinition) Prepare(_ context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	if input.Assets != nil {
		*d.screenshots = slices.Clone(input.Assets.Screenshots)
	}
	return trackers.NewDescriptionPlan("GPW", trackers.DescriptionResult{Group: "gpw", Description: "Synthetic description"}), nil
}

func (r *nativeEffectReporter) Begin(_ context.Context, effect api.WorkflowExternalEffect) (api.WorkflowExternalEffectReceipt, error) {
	r.fingerprints = append(r.fingerprints, effect.SemanticFingerprint)
	if r.unknown || r.started && !r.completed {
		return api.WorkflowExternalEffectReceipt{}, api.ErrReleaseWorkflowEffectOutcomeUnknown
	}
	if r.succeeded {
		return api.WorkflowExternalEffectReceipt{EffectID: "effect", AlreadySucceeded: true}, nil
	}
	r.started, r.completed = true, false
	return api.WorkflowExternalEffectReceipt{EffectID: "effect"}, nil
}
func (r *nativeEffectReporter) Complete(_ context.Context, _ api.WorkflowExternalEffectReceipt, succeeded bool) error {
	r.completed = true
	r.succeeded = succeeded
	return nil
}

func TestWorkflowNativeImportRunsBeforeGenericAndRetainsExactResults(t *testing.T) {
	for _, scenario := range []string{"mixed", "shared failed host", "protected source", "native only", "saved only", "approved", "excluded retained", "partial native", "known failure", "unknown", "explicit host", "credential change"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			source := filepath.Join(t.TempDir(), "Example.Release.mkv")
			if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
				t.Fatal(err)
			}
			dbPath := filepath.Join(t.TempDir(), "workflow.db")
			repository, err := db.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repository.Close() })
			if err := repository.Migrate(); err != nil {
				t.Fatal(err)
			}
			prepared, err := preparedrelease.New(repository, workflowMediaIdentityResolver{}, workflowMediaCollector{})
			if err != nil {
				t.Fatal(err)
			}
			verified, err := preparedrelease.VerifyInputSource(ctx, api.PrepareInput{SourcePath: source})
			if err != nil {
				t.Fatal(err)
			}
			facts, err := prepared.Prepare(ctx, api.PrepareInput{SourcePath: source, VerifiedSource: &verified})
			if err != nil {
				t.Fatal(err)
			}
			release := api.ReleaseRef{SourcePath: source, Generation: facts.Release.Generation}
			registry := mediaImageHostRegistry(t)
			if err := registry.RegisterDescriptor(trackers.Descriptor{
				Name:              "GPW",
				Definition:        mediaImageHostDefinition("GPW"),
				Family:            trackers.FamilyStandalone,
				BaseURL:           "https://gpw.example",
				UploadContentMode: trackers.UploadContentModeScreenshots,
				ImageHost: &trackers.ImageHostPolicy{AllowedHosts: []string{"pixhost", "imgbb", "kshare"}, URLImport: &trackers.ImageURLImportCapability{
					ID:         "test-import-v1",
					Host:       "kshare",
					UsageScope: "tracker:GPW",
					Import:     func(context.Context, *http.Client, string, string) (string, error) { return "", nil },
				}},
			}); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "pixhost", Host2: "imgbb"}, Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"GPW": {APIKey: "synthetic-key"}}}}
			cfg.MainSettings.DBPath = dbPath
			if scenario == "native only" || scenario == "saved only" {
				cfg.ImageHosting = config.ImageHostingConfig{}
			}
			reporter := &nativeEffectReporter{}
			hosting := &nativeWorkflowHost{active: reporter}
			if scenario == "shared failed host" {
				hosting.failHost = "pixhost"
			}
			if scenario == "known failure" {
				hosting.importErr = errors.New("known rejection")
			}
			if scenario == "partial native" {
				hosting.failSource = "https://unapproved.example/second.png"
			}
			if scenario == "unknown" {
				hosting.importErr = api.NewOperationError(api.OperationFailure{
					Code:      api.OperationFailureUnknownOutcome,
					Operation: api.OperationKindImageHosting,
					Recovery:  api.OperationRecoveryConfirm,
				}, errors.New("interrupted import"))
			}
			builder := workflowMediaBuilder{
				config:      cfg,
				resolver:    prepared,
				screenshots: &durableWorkflowScreenshotFake{root: t.TempDir()},
				media: &mediaModule{
					cfg:           cfg,
					images:        hosting,
					repo:          repository,
					mediaReuse:    repository,
					registry:      registry,
					preparedFacts: prepared,
					logger:        api.NopLogger{},
				},
			}
			projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "GPW", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 2}}, {TrackerID: "ONE", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 2}}}}
			trackerIDs := []api.TrackerID{"GPW", "ONE"}
			if scenario == "native only" || scenario == "saved only" {
				projections.Projections = projections.Projections[:1]
				trackerIDs = trackerIDs[:1]
			}
			snapshot, resource, err := builder.Build(ctx, release, projections, api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal, ScreenshotCount: 2}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			retained, ok := resource.(workflowMediaPrivateArtifacts)
			if !ok {
				t.Fatal("unexpected retained media resource")
			}
			remoteID := snapshot.Artifacts[0].ID
			snapshot.Artifacts[0].Source = "tracker"
			image := retained.ArtifactImages[remoteID]
			image.RawURL = "https://unapproved.example/source.png"
			image.Host = "unapproved"
			if scenario == "protected source" {
				image.RawURL = "https://passthepopcorn.me/static/image/source.png"
			}
			retained.ArtifactImages[remoteID] = image
			if scenario == "saved only" {
				image.RawURL, image.Host = "https://pixhost.to/approved.png", "pixhost"
				retained.ArtifactImages[remoteID] = image
				for _, artifact := range slices.Clone(snapshot.Artifacts) {
					selectedImage := retained.ArtifactImages[artifact.ID]
					if err := builder.retainImportedScreenshot(&retained, &snapshot, artifact, selectedImage, api.ScreenshotLinkedImage{
						Host:          "pixhost",
						URL:           "https://pixhost.to/approved.png",
						ContentSHA256: selectedImage.ContentSHA256,
					}, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
			}
			if scenario == "approved" || scenario == "excluded retained" {
				if scenario == "approved" {
					image.RawURL, image.Host = "https://pixhost.to/approved.png", "pixhost"
				}
				retained.ArtifactImages[remoteID] = image
				if err := builder.retainImportedScreenshot(&retained, &snapshot, snapshot.Artifacts[0], image, api.ScreenshotLinkedImage{
					Host:          "pixhost",
					URL:           "https://pixhost.to/approved.png",
					ContentSHA256: image.ContentSHA256,
				}, time.Now()); err != nil {
					t.Fatal(err)
				}
				if scenario == "excluded retained" {
					snapshot.FailedHosts = []string{"pixhost"}
				}
			}
			if scenario == "partial native" || scenario == "native only" {
				snapshot.Artifacts[1].Source = "tracker"
				second := retained.ArtifactImages[snapshot.Artifacts[1].ID]
				second.RawURL, second.Host = "https://unapproved.example/second.png", "unapproved"
				retained.ArtifactImages[snapshot.Artifacts[1].ID] = second
			}
			ids := []api.PublicResourceID{remoteID, snapshot.Artifacts[1].ID}
			if scenario == "native only" {
				inventory := &nativeInventoryScreenshots{mediaScreenshotService: builder.screenshots}
				for _, id := range ids {
					origin := retained.ArtifactImages[id]
					inventory.links = append(inventory.links, api.ScreenshotLinkedImage{
						Host: origin.Host,
						URL:  origin.RawURL,
						Path: origin.Path,
					})
				}
				builder.screenshots = inventory
				snapshot.WorkflowID, snapshot.ID, snapshot.Revision = "workflow-native-sql", "media-native-sql", 1
				if err := builder.RecordReusableMedia(ctx, snapshot, retained); err != nil {
					t.Fatal(err)
				}
				restored, restoredPrivate, restoreErr := builder.RestoreCompatible(ctx, release, projections, nil, nil, time.Now())
				if restoreErr != nil {
					t.Fatal(restoreErr)
				}
				result, ok := restoredPrivate.(workflowMediaPrivateArtifacts)
				if !ok {
					t.Fatal("SQL restore lost private media")
				}
				for _, origin := range result.ArtifactImages {
					if !slices.Contains([]string{"https://unapproved.example/source.png", "https://unapproved.example/second.png"}, origin.RawURL) {
						t.Fatal("SQL restore lost original import authority")
					}
				}
				snapshot, retained = restored, result
				remoteID = snapshot.Artifacts[0].ID
				ids = []api.PublicResourceID{remoteID, snapshot.Artifacts[1].ID}
				image = retained.ArtifactImages[remoteID]
			}
			explicit := ""
			if scenario == "explicit host" {
				explicit = "imgbb"
			}
			var progress []api.ImageUploadProgressUpdate
			var progressMu sync.Mutex
			ctx = api.WithImageUploadProgressReporter(ctx, func(update api.ImageUploadProgressUpdate) {
				progressMu.Lock()
				defer progressMu.Unlock()
				progress = append(progress, update)
			})
			ctx = api.WithWorkflowExternalEffectReporter(ctx, reporter)
			published, publishedPrivate, attempts, err := builder.UploadImages(ctx, release, projections, snapshot, retained, ids, explicit, false, nil, time.Now())
			if scenario == "unknown" {
				if failure, ok := api.AsOperationFailure(err); !ok || failure.Code != api.OperationFailureUnknownOutcome || reporter.completed || hosting.uploads != 0 {
					t.Fatal("uncertain import authorized fallback or completion")
				}
				_, _, _, err = builder.UploadImages(ctx, release, projections, snapshot, retained, ids, "", false, nil, time.Now())
				if err == nil || hosting.imports != 1 {
					t.Fatal("reopened unknown effect replayed import")
				}
				return
			}
			if err != nil || !reporter.completed || len(attempts) == 0 {
				t.Fatalf("image operation failed: %v", err)
			}
			wantImports := 1
			if scenario == "explicit host" || scenario == "approved" || scenario == "protected source" || scenario == "saved only" {
				wantImports = 0
			}
			if scenario == "partial native" || scenario == "native only" {
				wantImports = 2
			}
			if hosting.imports != wantImports {
				t.Fatalf("native calls=%d", hosting.imports)
			}
			assertHostedSourceCoverage(t, attempts, trackerIDs, ids)
			if scenario == "saved only" && hosting.uploads != 0 {
				t.Fatal("saved image coverage initiated an upload without a configured uploader")
			}
			if scenario == "shared failed host" && (hosting.uploads != 2 || !slices.Contains(published.FailedHosts, "pixhost")) {
				t.Fatal("residual stage retried a failed host or failed to reuse successful fallback links")
			}
			if scenario == "native only" {
				completed := 0
				for _, update := range progress {
					if update.Host == "kshare" {
						if update.Total != 2 || update.Completed < completed {
							t.Fatal("native progress reset within one host attempt")
						}
						completed = update.Completed
					}
				}
				if completed != 2 {
					t.Fatal("native progress omitted completed images")
				}
			}
			if scenario == "native only" && hosting.uploads != 0 {
				t.Fatal("native-only coverage initiated generic uploads")
			}
			if scenario == "excluded retained" {
				for _, attempt := range attempts {
					if attempt.Host == "pixhost" {
						t.Fatal("image stage reused an excluded host")
					}
				}
			}
			if scenario == "known failure" || scenario == "explicit host" || scenario == "protected source" {
				if hosting.uploads == 0 {
					t.Fatal("ordinary fallback was lost")
				}
				return
			}
			private, ok := publishedPrivate.(workflowMediaPrivateArtifacts)
			if !ok {
				t.Fatal("unexpected published media resource")
			}
			_, encoded, encodeErr := private.MarshalPrivateResource()
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			private, err = decodeWorkflowMediaPrivateArtifacts(builder, encoded)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "mixed" || scenario == "excluded retained" || scenario == "partial native" || scenario == "native only" || scenario == "saved only" {
				meta, resolveErr := prepared.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: release, Trackers: []string{"GPW"}})
				if resolveErr != nil {
					t.Fatal(resolveErr)
				}
				meta.ExactMedia, err = resolveWorkflowExactMedia(private, published)
				if err != nil {
					t.Fatal(err)
				}
				skip := true
				meta.ImageHostOverrides.SkipUpload = &skip
				meta.ImageHostOverrides.FailedHosts = slices.Clone(published.FailedHosts)
				var consumed []api.ScreenshotImage
				descriptionRegistry := trackers.NewRegistry()
				descriptor, _ := registry.LookupDescriptor("GPW")
				descriptor.Definition = nativeDescriptionDefinition{screenshots: &consumed}
				descriptor.UploadContentMode = trackers.UploadContentModeDescription
				if err := descriptionRegistry.RegisterDescriptor(descriptor); err != nil {
					t.Fatal(err)
				}
				service := trackers.NewServiceWithRegistryAndImages(cfg, api.NopLogger{}, repository, descriptionRegistry, hosting)
				beforeImports, beforeUploads := hosting.imports, hosting.uploads
				preview, descriptionErr := service.BuildPreparation(t.Context(), api.NewDescriptionSubject(meta), []string{"GPW"})
				if descriptionErr != nil || len(preview.ContentFailures) != 0 || len(consumed) != len(ids) {
					t.Fatalf("description lost selected hosted screenshots: count=%d err=%v", len(consumed), descriptionErr)
				}
				for _, screenshot := range consumed {
					if slices.Contains(published.FailedHosts, screenshot.Host) || !slices.Contains([]string{"kshare", "pixhost", "imgbb"}, screenshot.Host) {
						t.Fatal("description consumed an excluded or unapproved host")
					}
				}
				if hosting.imports != beforeImports || hosting.uploads != beforeUploads {
					t.Fatalf("description preparation initiated an image write: imports=%d/%d uploads=%d/%d", hosting.imports, beforeImports, hosting.uploads, beforeUploads)
				}
			}
			if scenario == "approved" || scenario == "saved only" {
				if hosting.imports != 0 {
					t.Fatal("approved source was imported")
				}
				return
			}
			var native api.UploadedImageLink
			for _, link := range private.HostedImages {
				if link.Host == "kshare" && link.ImagePath == image.Path {
					native = link
				}
			}
			if native.ImagePath != image.Path || native.UsageScope != "tracker:GPW" || !trackers.NativeImageLinkCurrent(cfg, registry, native) || private.ArtifactImages[remoteID].RawURL != image.RawURL {
				t.Fatal("native scope, publication or original source association lost")
			}
			if scenario != "native only" && !slices.ContainsFunc(hosting.calls, func(call workflowImageHostCall) bool {
				return slices.Contains(call.paths, retained.ArtifactImages[ids[1]].Path)
			}) {
				t.Fatal("mixed local image was treated as native-covered")
			}
			if scenario == "credential change" {
				cfg.Trackers.Trackers["GPW"] = config.TrackerConfig{APIKey: "replacement-key"}
				builder.config, builder.media.cfg = cfg, cfg
				if trackers.NativeImageLinkCurrent(cfg, registry, native) {
					t.Fatal("credential replacement reused native account")
				}
				if candidates := builder.retainedHostedImageCandidates(published, private, map[api.PublicResourceID]struct{}{remoteID: {}}); slices.ContainsFunc(candidates, func(c retainedHostedImageCandidate) bool { return c.link.Host == "kshare" }) {
					t.Fatal("retained artifacts bypassed credential check")
				}
			}
			if scenario == "mixed" || scenario == "partial native" || scenario == "native only" {
				// RestoreCompatible must rebuild combined host attempts directly,
				// with no image action required to regain workflow coverage.
				published.HostAttempts = nil
				restored, restoredPrivate, restoreErr := builder.RestoreCompatible(t.Context(), release, projections, &published, private, time.Now())
				if restoreErr != nil {
					t.Fatal(restoreErr)
				}
				assertHostedSourceCoverage(t, restored.HostAttempts, trackerIDs, ids)
				// A new authorized image action after publication reuses both native
				// and ordinary results, including after private-resource reload.
				fresh := &nativeEffectReporter{}
				hosting.active = fresh
				_, _, reusedAttempts, reuseErr := builder.UploadImages(api.WithWorkflowExternalEffectReporter(t.Context(), fresh), release, projections, restored, restoredPrivate, ids, "", false, nil, time.Now())
				if reuseErr != nil || hosting.imports != wantImports {
					t.Fatalf("published native result was reimported after reload: %v", reuseErr)
				}
				assertHostedSourceCoverage(t, reusedAttempts, trackerIDs, ids)
			}
			if scenario == "native only" {
				// SQL must restore original source authority even when native output
				// belongs to a replaced account and cannot itself be reused.
				published.WorkflowID, published.ID, published.Revision = "workflow-native-sql", "media-native-sql", 2
				if err := builder.RecordReusableMedia(ctx, published, private); err != nil {
					t.Fatal(err)
				}
				cfg.Trackers.Trackers["GPW"] = config.TrackerConfig{APIKey: "replacement-key"}
				builder.config, builder.media.cfg = cfg, cfg
				// Reload the persisted old-account result without rewriting it under
				// the new account, then authorize a fresh image action.
				restored, restoredResource, restoreErr := builder.RestoreCompatible(ctx, release, projections, nil, nil, time.Now())
				if restoreErr != nil {
					t.Fatal(restoreErr)
				}
				fresh := &nativeEffectReporter{}
				hosting.active = fresh
				newIDs := []api.PublicResourceID{restored.Artifacts[0].ID, restored.Artifacts[1].ID}
				_, _, newAttempts, importErr := builder.UploadImages(api.WithWorkflowExternalEffectReporter(t.Context(), fresh), release, projections, restored, restoredResource, newIDs, "", false, nil, time.Now())
				wantImports += 2
				if importErr != nil || hosting.imports != wantImports || hosting.uploads != 0 {
					t.Fatalf("SQL account replacement did not reimport original sources: %v", importErr)
				}
				assertHostedSourceCoverage(t, newAttempts, trackerIDs, newIDs)
			}
			// Completed but unpublished work must not replay even if the result is unavailable.
			_, _, _, err = builder.UploadImages(ctx, release, projections, snapshot, retained, ids, "", false, nil, time.Now())
			if err == nil || hosting.imports != wantImports {
				t.Fatal("completed effect with unavailable publication was replayed")
			}
		})
	}
}

func TestReusableImportedImageOriginsRequireUnambiguousMatchingBytes(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first.png")
	other := filepath.Join(t.TempDir(), "other.png")
	if err := os.WriteFile(first, []byte("original bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("unrelated bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := workflowMediaContentSHA256(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"different bytes", "ambiguous source"} {
		t.Run(scenario, func(t *testing.T) {
			links := []api.ScreenshotLinkedImage{{
				Host: "unapproved",
				URL:  "https://unapproved.example/source.png",
				Path: other,
			}}
			if scenario == "ambiguous source" {
				links[0].Path = first
				links = append(links, api.ScreenshotLinkedImage{
					Host: "kshare",
					URL:  "https://kshare.club/output.png",
					Path: first,
				})
			}
			builder := workflowMediaBuilder{screenshots: &nativeInventoryScreenshots{links: links}}
			if _, associated := builder.reusableImportedImageOrigins(t.Context(), api.ScreenshotSubject{})[digest]; associated {
				t.Fatal("unproven URL became cached-image import authority")
			}
		})
	}
}

func assertHostedSourceCoverage(t *testing.T, attempts []api.HostedImageAttempt, trackerIDs []api.TrackerID, sourceIDs []api.PublicResourceID) {
	t.Helper()
	for _, tracker := range trackerIDs {
		covered := make(map[api.PublicResourceID]bool)
		for _, attempt := range attempts {
			if attempt.Status != api.StageStatusCompleted || !slices.Contains(attempt.TrackerIDs, tracker) || attempt.UsageScope != "global" && attempt.UsageScope != "tracker:"+string(tracker) {
				continue
			}
			for _, result := range attempt.Results {
				if result.Selected {
					if tracker == "GPW" && len(attempt.Results) == 1 && (len(attempt.ArtifactIDs) != 1 || attempt.ArtifactIDs[0] != api.PublicResourceID(result.Source)) {
						t.Fatal("single-image GPW attempt claimed another artifact subset")
					}
					covered[api.PublicResourceID(result.Source)] = true
				}
			}
		}
		for _, sourceID := range sourceIDs {
			if !covered[sourceID] {
				t.Fatalf("tracker %s lost hosted workflow coverage for a selected source", tracker)
			}
		}
	}
}
