// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type workflowMediaResolverFake struct{}

func (workflowMediaResolverFake) ResolveScreenshotSubject(
	_ context.Context,
	input api.MediaPlanInput,
) (api.ScreenshotSubject, error) {
	return api.ScreenshotSubject{SourcePath: input.Release.SourcePath, DiscType: "DVD"}, nil
}

func (workflowMediaResolverFake) ResolveDVDMenuSubject(
	_ context.Context,
	input api.MediaPlanInput,
) (api.DVDMenuSubject, error) {
	return api.DVDMenuSubject{SourcePath: input.Release.SourcePath, DiscType: "DVD"}, nil
}

type workflowScreenshotFake struct {
	root     string
	plans    int
	captures int
	deleted  []string
}

func (f *workflowScreenshotFake) Plan(
	context.Context,
	api.ScreenshotSubject,
	int,
) (api.ScreenshotPlan, error) {
	f.plans++
	return api.ScreenshotPlan{SuggestedSelections: []api.ScreenshotSelection{{Index: 1, TimestampSeconds: 60}}}, nil
}

func (f *workflowScreenshotFake) Capture(
	_ context.Context,
	_ api.ScreenshotSubject,
	selections []api.ScreenshotSelection,
	purpose api.ScreenshotPurpose,
) (api.ScreenshotResult, error) {
	f.captures++
	images := make([]api.ScreenshotImage, len(selections))
	for index, selection := range selections {
		images[index] = api.ScreenshotImage{
			Path:             filepath.Join(f.root, fmt.Sprintf("screen-%d.png", selection.Index)),
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

func (*workflowScreenshotFake) PreviewFrame(
	context.Context,
	api.ScreenshotSubject,
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
	captures int
	deleted  []string
}

func (f *workflowDVDMenuFake) Capture(
	_ context.Context,
	_ api.DVDMenuSubject,
	maxItems int,
) (api.DVDMenuCaptureResult, error) {
	f.captures++
	return api.DVDMenuCaptureResult{
		Images: []api.DVDMenuCaptureImage{{ScreenshotImage: api.ScreenshotImage{
			Path:      "C:\\private\\menu.png",
			Purpose:   api.ScreenshotPurposeMenu,
			Width:     720,
			Height:    480,
			SizeBytes: 4321,
		}, Discovery: api.DVDMenuDiscoveryReachable}},
		MaxItems: maxItems,
		Complete: true,
	}, nil
}

func (*workflowDVDMenuFake) List(context.Context, api.DVDMenuSubject) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (f *workflowDVDMenuFake) Delete(_ context.Context, _ api.DVDMenuSubject, path string) error {
	f.deleted = append(f.deleted, path)
	return nil
}

func (*workflowDVDMenuFake) Capability(context.Context) (api.DVDMenuEngineInfo, error) {
	return api.DVDMenuEngineInfo{}, nil
}

func TestWorkflowMediaBuilderAutomaticallyCapturesProjectionRequirements(t *testing.T) {
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
		Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1, DVDMenuCount: 2},
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
	if snapshot.Status != api.StageStatusCompleted || len(snapshot.Artifacts) != 2 ||
		screenshots.plans != 1 || screenshots.captures != 1 || dvdMenus.captures != 1 {
		t.Fatalf("media capture = %#v plans=%d screenshots=%d menus=%d", snapshot, screenshots.plans, screenshots.captures, dvdMenus.captures)
	}
	private, ok := privateArtifacts.(workflowMediaPrivateArtifacts)
	if !ok || len(private.Screenshots) != 1 || len(private.DVDMenus) != 1 {
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
			ScreenshotImage: api.ScreenshotImage{Path: "C:\\private\\menu.png"},
			Discovery:       api.DVDMenuDiscoveryReachable,
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
