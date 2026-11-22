// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowAutomaticImageHostingUsesPreferredPolicy(t *testing.T) {
	for _, kind := range []api.MediaArtifactKind{api.MediaArtifactScreenshot, api.MediaArtifactDVDMenu} {
		t.Run(string(kind), func(t *testing.T) {
			for _, test := range []struct{ name, configured, explicit, want string }{
				{name: "preferred", want: "imgbb"},
				{
					name:       "tracker precedence",
					configured: "pixhost",
					want:       "pixhost",
				},
				{
					name:     "explicit host",
					explicit: "pixhost",
					want:     "pixhost",
				},
			} {
				t.Run(test.name, func(t *testing.T) {
					reporter := &preferredHostEffectReporter{}
					ctx := api.WithWorkflowExternalEffectReporter(t.Context(), reporter)
					source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
					if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
						t.Fatal(err)
					}
					repository, err := db.Open(filepath.Join(t.TempDir(), "workflow.db"))
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
					result, err := prepared.Prepare(ctx, api.PrepareInput{SourcePath: source, VerifiedSource: &verified})
					if err != nil {
						t.Fatal(err)
					}
					release := api.ReleaseRef{SourcePath: source, Generation: result.Release.Generation}
					path := filepath.Join(t.TempDir(), "image.png")
					if err := os.WriteFile(path, []byte("synthetic image"), 0o600); err != nil {
						t.Fatal(err)
					}
					cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "pixhost", Host2: "imgbb"}, Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ONE": {ImageHost: test.configured}}}}
					uploader := &countingWorkflowImageHost{}
					builder := workflowMediaBuilder{config: cfg, media: &mediaModule{
						cfg:           cfg,
						images:        uploader,
						preparedFacts: prepared,
						registry:      mediaImageHostRegistry(t),
						logger:        api.NopLogger{},
					}}
					snapshot := api.MediaArtifactSet{
						ID:       "media",
						Revision: 1,
						Status:   api.StageStatusCompleted,
						Artifacts: []api.MediaArtifact{{
							ID:       "local",
							Kind:     kind,
							Purpose:  api.ScreenshotPurposeFinal,
							Selected: true,
						}},
					}
					retained := workflowMediaPrivateArtifacts{ArtifactImages: map[api.PublicResourceID]api.ScreenshotImage{"local": {Path: path, Purpose: api.ScreenshotPurposeFinal}}}
					projections := api.TrackerReleaseProjectionSet{
						ID:          "projections",
						Revision:    1,
						Projections: []api.TrackerReleaseProjection{{TrackerID: "ONE", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1}}},
					}
					uploaded, resource, attempts, err := builder.UploadImages(ctx, release, projections, snapshot, retained, []api.PublicResourceID{"local"}, test.explicit, false, new("imgbb"), time.Now())
					if err != nil {
						t.Fatal(err)
					}
					if len(uploader.calls) != 1 || uploader.calls[0].host != test.want || len(attempts) != 1 || len(attempts[0].Results) != 1 {
						t.Fatalf("calls=%#v attempts=%#v", uploader.calls, attempts)
					}
					// Replaying compatible saved media does not force another upload.
					_, _, _, err = builder.UploadImages(ctx, release, projections, uploaded, resource, []api.PublicResourceID{"local"}, test.explicit, false, new("imgbb"), time.Now())
					if err != nil || len(uploader.calls) != 1 {
						t.Fatalf("reuse calls=%#v error=%v", uploader.calls, err)
					}
					if len(reporter.effects) != 2 || reporter.effects[0] != reporter.effects[1] {
						t.Fatalf("same-policy effect identity changed: %v", reporter.effects)
					}
					_, _, _, err = builder.UploadImages(ctx, release, projections, uploaded, resource, []api.PublicResourceID{"local"}, test.explicit, false, new("pixhost"), time.Now())
					if err != nil {
						t.Fatal(err)
					}
					if len(reporter.effects) != 3 || reporter.effects[2] == reporter.effects[1] {
						t.Fatalf("changed policy reused effect identity: %v", reporter.effects)
					}
				})
			}
		})
	}
}

func TestWorkflowExplicitHostUploadCanCompleteDiscCoverageOneFrameAtATime(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "Example.Release.2026.mkv")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := db.Open(filepath.Join(root, "workflow.db"))
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
	ctx := api.WithWorkflowExternalEffectReporter(t.Context(), &preferredHostEffectReporter{})
	verified, err := preparedrelease.VerifyInputSource(ctx, api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	result, err := prepared.Prepare(ctx, api.PrepareInput{SourcePath: source, VerifiedSource: &verified})
	if err != nil {
		t.Fatal(err)
	}
	release := api.ReleaseRef{SourcePath: source, Generation: result.Release.Generation}
	cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "imgbb"}}
	builder := workflowMediaBuilder{config: cfg, media: &mediaModule{
		cfg:           cfg,
		images:        &countingWorkflowImageHost{},
		preparedFacts: prepared,
		registry:      mediaImageHostRegistry(t),
		logger:        api.NopLogger{},
	}}
	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
		TrackerID: "ONE",
		Artifacts: api.TrackerArtifactRequirements{
			ScreenshotCount: 1,
			DiscScreenshots: &api.DiscScreenshotRequirement{DiscIDs: []string{"first", "second"}, AdditionalCount: 2},
		},
	}}}
	snapshot := api.MediaArtifactSet{ID: "media", Revision: 1}
	images := make(map[api.PublicResourceID]api.ScreenshotImage, 3)
	var retained releaseworkflow.RetainedMediaResource = workflowMediaPrivateArtifacts{ArtifactImages: images}
	ids := make([]api.PublicResourceID, 0, 3)
	for index, disc := range []string{"first", "second", "second"} {
		name := fmt.Sprintf("disc-frame-%d", index)
		id := api.PublicResourceID(name)
		ids = append(ids, id)
		snapshot.Artifacts = append(snapshot.Artifacts, api.MediaArtifact{
			ID: id,
 DiscID: disc,
 Kind: api.MediaArtifactScreenshot,
 Purpose: api.ScreenshotPurposeFinal,
 Selected: true,
		})
		images[id] = api.ScreenshotImage{
Path: filepath.Join(root, name+".png"),
 DiscID: disc,
 Purpose: api.ScreenshotPurposeFinal,
}
	}
	for index, id := range ids {
		var attempts []api.HostedImageAttempt
		snapshot, retained, attempts, err = builder.UploadImages(ctx, release, projections, snapshot, retained, []api.PublicResourceID{id},
			"imgbb", false, nil, time.Now())
		if err != nil || len(attempts) != 1 || attempts[0].Status != api.StageStatusCompleted || len(snapshot.FailedHosts) != 0 {
			t.Fatalf("single frame upload failed at %d: attempts=%+v, err=%v", index, attempts, err)
		}
		snapshot.HostAttempts = append(snapshot.HostAttempts, attempts...)
		ready := releaseworkflow.HostedScreenshotRequirementsMet(snapshot, projections.Projections)
		if ready != (index == len(ids)-1) {
			t.Fatalf("disc gallery readiness after frame %d = %t", index, ready)
		}
	}
}

type preferredHostEffectReporter struct{ effects []api.WorkflowFingerprint }

func (r *preferredHostEffectReporter) Begin(_ context.Context, effect api.WorkflowExternalEffect) (api.WorkflowExternalEffectReceipt, error) {
	r.effects = append(r.effects, effect.SemanticFingerprint)
	return api.WorkflowExternalEffectReceipt{EffectID: "image-effect"}, nil
}
func (*preferredHostEffectReporter) Complete(context.Context, api.WorkflowExternalEffectReceipt, bool) error {
	return nil
}
