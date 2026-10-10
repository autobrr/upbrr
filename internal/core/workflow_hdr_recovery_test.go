// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/imagehosting"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRPlaylistPreparationRendersCapturedSidecarWithoutReadingDisc(t *testing.T) {
	builder, resolver, sidecar := hdrFixture(t)
	release := api.ReleaseRef{SourcePath: filepath.Join(t.TempDir(), "Synthetic.Disc.2026"), Generation: 1}
	targetID := api.HDRTargetID(release.SourcePath, "00001.MPLS")
	resolver.subject.Release = release
	resolver.subject.Targets[0] = api.HDRAnalysisTargetSubject{
		Target: api.HDRAnalysisTarget{
			ID:              targetID,
			Label:           "Disc 1",
			Playlist:        "00001.MPLS",
			Supported:       true,
			SelectionPolicy: "primary_hevc_angle_zero",
		},
		DiscRoot: release.SourcePath,
	}
	sidecar.Identity.TargetID, sidecar.Identity.SelectionPolicy = targetID, "primary_hevc_angle_zero"
	sidecar.Timeline = []hdranalysis.TimelineItem{{
		Index:      0,
		Out45:      45000,
		Duration45: 45000,
		Mapping: video.Mapping{
			PID:       1,
			Codec:     video.HEVC,
			Role:      video.Primary,
			EntryType: 1,
		},
		Source: video.Source{Kind: "m2ts", Path: filepath.Join(release.SourcePath, "BDMV", "STREAM", "00001.M2TS")},
	}}
	record, captured, err := builder.retainExtraction(t.Context(), release, sidecar, "playlist-capture")
	if err != nil {
		t.Fatal(err)
	}
	target := &resolver.subject.Targets[0]
	target.CapturedPath, target.CapturedSize, target.CapturedSHA256, target.CapturedTrackID = captured.Files["metadata"].Path, record.Size, record.SHA256, record.ResolvedTrackID
	preparer := releaseworkflow.ReleasePreparerFunc{
		PrepareFunc: func(context.Context, api.PrepareInput) (api.PrepareResult, error) {
			return api.PrepareResult{Release: api.PreparedRelease{
				Generation: release.Generation,
				Source:     api.SourceManifest{SourcePath: release.SourcePath},
				Naming:     api.NamingFacts{ReleaseName: resolver.subject.Title},
			}}, nil
		},
		DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
			return api.PreparedReleaseDisplay{HDRTargets: []api.HDRAnalysisTarget{target.Target}}, nil
		},
	}
	repository := releaseworkflow.NewMemoryRepository()
	vault, err := releaseworkflow.NewPrivateArtifactVault(t.TempDir(), hdrResourceCodec(builder.root, "analysis"), hdrResourceCodec(builder.root, "extraction"))
	if err != nil {
		t.Fatal(err)
	}
	module, err := releaseworkflow.New(repository, vault, preparer, releaseworkflow.WithHDRAnalysisBuilder(builder))
	if err != nil {
		t.Fatal(err)
	}
	created, err := module.Execute(t.Context(), "owner", releaseworkflow.CreateWorkflowCommand{WorkflowID: "captured-playlist"})
	if err != nil {
		t.Fatal(err)
	}
	phases := make(map[string]bool)
	ctx := hdranalysis.WithProgress(t.Context(), func(update hdranalysis.Progress) { phases[update.Phase] = true })
	prepared, err := module.Execute(ctx, "owner", releaseworkflow.PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: release.SourcePath, Controls: api.PreparationControls{CaptureHDRMetadata: true}},
		IdempotencyKey:   "confirm-playlist",
	})
	if err != nil || prepared.HDRAnalysis == nil || prepared.HDRAnalysis.Status != api.StageStatusCompleted || !prepared.Workflow.HDRAnalysisEnabled ||
		!phases["rendering"] || phases["reading_metadata"] {
		t.Fatalf("captured metadata not automatically plotted: result=%#v phases=%v err=%v", prepared, phases, err)
	}
	// The disc does not exist. A successful image proves that preparation reused the saved capture.
	content, err := module.HDRAnalysisArtifact(
		t.Context(),
		"owner",
		prepared.Workflow.ID,
		*prepared.Workflow.HDRAnalysis,
		prepared.HDRAnalysis.Targets[0].Artifact.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer content.Body.Close()
	image, err := png.DecodeConfig(content.Body)
	if err != nil || image.Width != 3000 || image.Height != 1200 {
		t.Fatalf("prepared image=%#v err=%v", image, err)
	}
}

func TestHDRStoredPlotRestoresWithoutMetadataAndReusesHostedLinks(t *testing.T) {
	builder, resolver, sidecar := hdrFixture(t)
	release := resolver.subject.Release
	repo, err := db.Open(filepath.Join(t.TempDir(), "uploads.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	builder.uploads = repo
	binding := api.PreparedMediaBinding{
		SourcePath:               release.SourcePath,
		PreparedGeneration:       release.Generation,
		PreparedMediaFingerprint: "first",
	}
	resolver.subject.MediaBinding = binding
	record, metadata, err := builder.retainExtraction(t.Context(), release, sidecar, "original-metadata")
	if err != nil {
		t.Fatal(err)
	}
	request := api.HDRAnalysisInstructions{Release: release, TargetIDs: []string{sidecar.Identity.TargetID}}
	original, plots, err := builder.Build(t.Context(), release, request, "original", time.Now().UTC(), nil, nil,
		map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord{record.ID: record},
		map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource{record.ID: metadata}, nil)
	if err != nil || original.Status != api.StageStatusCompleted {
		t.Fatalf("original=%#v err=%v", original, err)
	}
	from, err := plots.LocalArtifactPath(original, original.Targets[0].Artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := hdrFileIntegrity(from)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "onlyimage"}}
	registry := mediaImageHostRegistry(t)
	account, err := imagehosting.AnalysisAccountScope(cfg, registry, "onlyimage", api.ScreenshotPurposeHDRAnalysis)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"global", "tracker:ONE"} {
		if err := repo.SaveUploadedImages(t.Context(), binding, "onlyimage", []api.UploadedImageLink{{
			ImagePath:    from,
			Purpose:      api.ScreenshotPurposeHDRAnalysis,
			AccountScope: account,
			UsageScope:   scope,
			ImgURL:       "https://images.example.invalid/preview.png",
			RawURL:       "https://images.example.invalid/full.png",
			WebURL:       "https://images.example.invalid/page",
			SizeBytes:    123,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	// Reopen the real durable vault, with only the PNG still present in tmp.
	vaultRoot := t.TempDir()
	vault, err := releaseworkflow.NewPrivateArtifactVault(vaultRoot, hdrResourceCodec(builder.root, "analysis"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.PutWithoutExpiry("owner", "old-workflow", "plot", plots); err != nil {
		t.Fatal(err)
	}
	vault, err = releaseworkflow.NewPrivateArtifactVault(vaultRoot, hdrResourceCodec(builder.root, "analysis"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := vault.Get("owner", "old-workflow", "plot", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	retained, ok := value.(releaseworkflow.RetainedHDRAnalysisResource)
	if !ok {
		t.Fatal("vault did not restore PNG authority")
	}
	if err := metadata.Release(); err != nil {
		t.Fatal(err)
	}
	request.Release.Generation++
	resolver.subject.MediaBinding.PreparedGeneration = request.Release.Generation
	resolver.subject.MediaBinding.PreparedMediaFingerprint = "second"
	phases := make(map[string]bool)
	ctx := hdranalysis.WithProgress(t.Context(), func(update hdranalysis.Progress) { phases[update.Phase] = true })
	restored, resource, err := builder.Build(ctx, request.Release, request, "restored", time.Now().UTC(), &original, retained, nil, nil, nil)
	if err != nil || restored.Status != api.StageStatusCompleted || resource == nil || phases["reading_metadata"] || phases["rendering"] {
		t.Fatalf("PNG restoration reread or rerendered: result=%#v phases=%v err=%v", restored, phases, err)
	}
	to, err := resource.LocalArtifactPath(restored, restored.Targets[0].Artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := hdrFileIntegrity(to)
	if err != nil || to == from || before.SHA256 != after.SHA256 || restored.Targets[0].Frames != original.Targets[0].Frames {
		t.Fatal("restored PNG changed or shares the old owner's path")
	}
	links, err := repo.ListUploadedImagesByPath(t.Context(), resolver.subject.MediaBinding)
	if err != nil || len(links) != 2 {
		t.Fatalf("restored links=%#v err=%v", links, err)
	}
	for _, link := range links {
		if link.ImagePath != to || link.AccountScope != account || link.Purpose != api.ScreenshotPurposeHDRAnalysis ||
			link.ImgURL != "https://images.example.invalid/preview.png" || link.RawURL != "https://images.example.invalid/full.png" ||
			link.WebURL != "https://images.example.invalid/page" || link.SizeBytes != 123 {
			t.Fatalf("upload provenance changed: %#v", link)
		}
	}
	host := &descriptionAudioHostFake{}
	media := &mediaModule{
		cfg:      cfg,
		registry: registry,
		logger:   api.NopLogger{},
		repo:     repo,
		images:   host,
	}
	subject := api.UploadSubject{SourcePath: release.SourcePath, MediaBinding: resolver.subject.MediaBinding}
	images := []api.ScreenshotImage{{
		Path:    to,
		Purpose: api.ScreenshotPurposeHDRAnalysis,
		Width:   api.HDRAnalysisWidth,
		Height:  api.HDRAnalysisHeight,
	}}
	for _, scope := range []string{"global", "tracker:ONE"} {
		reused, err := media.uploadImagesToTarget(
			t.Context(),
			subject,
			trackers.ImageUploadTarget{Host: "onlyimage", UsageScope: scope},
			images,
			nil,
			nil,
			false,
		)
		if err != nil || len(reused) != 1 || len(host.images) != 0 || reused[0].RawURL != links[0].RawURL {
			t.Fatalf("existing upload not reused: %#v err=%v", reused, err)
		}
	}
	media.cfg.ImageHosting.Host2 = "changed-account-configuration"
	if _, err := media.uploadImagesToTarget(
		t.Context(),
		subject,
		trackers.ImageUploadTarget{Host: "onlyimage", UsageScope: "global"},
		images,
		nil,
		nil,
		false,
	); err != nil ||
		len(host.images) != 1 {
		t.Fatalf("changed account reused old upload: calls=%d err=%v", len(host.images), err)
	}
	// Source, title/estimator, and PNG integrity remain prerequisites for reuse.
	for _, test := range []string{"source", "title", "estimator", "damaged"} {
		t.Run(test, func(t *testing.T) {
			copyResolver := *resolver
			candidate := builder
			candidate.resolver = &copyResolver
			instructions := request
			switch test {
			case "source":
				copyResolver.subject.ManifestFingerprint = "changed-source"
			case "title":
				copyResolver.subject.Title = "Changed title"
			case "estimator":
				instructions.PeakSource = api.HDRPeakMaxSCL
			case "damaged":
				if err := os.WriteFile(from, []byte("damaged"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			result, _, err := candidate.Build(
				t.Context(),
				instructions.Release,
				instructions,
				"rejected-"+test,
				time.Now().UTC(),
				&original,
				retained,
				nil,
				nil,
				nil,
			)
			if err != nil || result.Status == api.StageStatusCompleted {
				t.Fatalf("incompatible plot reused: %#v err=%v", result, err)
			}
		})
	}
}
