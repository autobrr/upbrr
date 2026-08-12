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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
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
	plan     *api.ScreenshotPlan
	plans    int
	captures int
	deleted  []string
	err      error
}

func (f *workflowScreenshotFake) Plan(
	context.Context,
	api.ScreenshotSubject,
	int,
) (api.ScreenshotPlan, error) {
	f.plans++
	if f.plan != nil {
		return *f.plan, nil
	}
	return api.ScreenshotPlan{SuggestedSelections: []api.ScreenshotSelection{{Index: 1, TimestampSeconds: 60}}}, nil
}

func (f *workflowScreenshotFake) Capture(
	_ context.Context,
	_ api.ScreenshotSubject,
	selections []api.ScreenshotSelection,
	purpose api.ScreenshotPurpose,
) (api.ScreenshotResult, error) {
	f.captures++
	if f.err != nil {
		return api.ScreenshotResult{}, f.err
	}
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
		Images: []api.DVDMenuCaptureImage{{ScreenshotImage: api.ScreenshotImage{
			Path:    "new-auto-menu.png",
			Purpose: api.ScreenshotPurposeMenu,
		}}},
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
			{ScreenshotImage: api.ScreenshotImage{Path: "manual-menu.png", Purpose: api.ScreenshotPurposeMenu}},
			{ScreenshotImage: api.ScreenshotImage{Path: "old-auto-menu.png", Purpose: api.ScreenshotPurposeMenu}},
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
			ScreenshotImage: api.ScreenshotImage{Path: menuPath},
			Discovery:       api.DVDMenuDiscoveryReachable,
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
