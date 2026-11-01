// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/preparedrelease"
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

type preferredHostEffectReporter struct{ effects []api.WorkflowFingerprint }

func (r *preferredHostEffectReporter) Begin(_ context.Context, effect api.WorkflowExternalEffect) (api.WorkflowExternalEffectReceipt, error) {
	r.effects = append(r.effects, effect.SemanticFingerprint)
	return api.WorkflowExternalEffectReceipt{EffectID: "image-effect"}, nil
}
func (*preferredHostEffectReporter) Complete(context.Context, api.WorkflowExternalEffectReceipt, bool) error {
	return nil
}
