// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type workflowDescriptionResolverFake struct {
	input api.UploadSubjectInput
}

func (f *workflowDescriptionResolverFake) ResolveUploadSubject(
	_ context.Context,
	input api.UploadSubjectInput,
) (api.UploadSubject, error) {
	f.input = input
	return api.UploadSubject{
		SourcePath:          input.Release.SourcePath,
		DescriptionTemplate: "Template v1",
		DescriptionGroups:   input.DescriptionGroups,
		Trackers:            input.Trackers,
		Options:             input.Options,
		ImageHostOverrides:  input.ImageHostOverrides,
	}, nil
}

type workflowDescriptionServiceFake struct {
	builds   int
	subject  api.DescriptionSubject
	trackers []string
	preview  *api.PreparationPreview
}

func (f *workflowDescriptionServiceFake) BuildPreparation(
	_ context.Context,
	subject api.DescriptionSubject,
	trackers []string,
) (api.PreparationPreview, error) {
	f.builds++
	f.subject = subject
	f.trackers = append([]string(nil), trackers...)
	if f.preview != nil {
		return *f.preview, nil
	}
	return api.PreparationPreview{Descriptions: []api.PreparationDescription{{
		GroupKey:           "unit3d|img.example|global",
		Trackers:           append([]string(nil), trackers...),
		RawDescription:     "Example description.",
		RawDescriptionHTML: "<p>Example description.</p>",
		ImageHost: api.ImageHostFeedback{
			Status:       "ready",
			SelectedHost: "img.example",
		},
	}}}, nil
}

func TestWorkflowDescriptionBuilderBindsProjectionMediaInputsAndImageFeedback(t *testing.T) {
	t.Parallel()

	resolver := &workflowDescriptionResolverFake{}
	service := &workflowDescriptionServiceFake{}
	builder := workflowDescriptionBuilder{
		resolver: resolver,
		trackers: service,
	}
	release := api.ReleaseRef{SourcePath: "C:\\releases\\Example.Release.2026", Generation: 2}
	projections := api.TrackerReleaseProjectionSet{
		ID:                "projections-1",
		Revision:          4,
		InputFingerprint:  workflowTestFingerprint(t, "projection-input"),
		PolicyFingerprint: workflowTestFingerprint(t, "projection-policy"),
		Projections: []api.TrackerReleaseProjection{
			{
				TrackerID:        "ALPHA",
				DisplayName:      "Alpha",
				DescriptionGroup: "unit3d",
				Artifacts:        api.TrackerArtifactRequirements{Description: true},
			},
			{
				TrackerID:        "BETA",
				DisplayName:      "Beta",
				DescriptionGroup: "unit3d",
				Artifacts:        api.TrackerArtifactRequirements{Description: true},
			},
			{
				TrackerID:        "SCREENSHOTS",
				DisplayName:      "Screenshots",
				DescriptionGroup: "unit3d",
				Artifacts:        api.TrackerArtifactRequirements{ScreenshotCount: 2},
			},
		},
	}
	media := api.MediaArtifactSet{
		ID:                      "media-1",
		Revision:                5,
		CaptureFingerprint:      workflowTestFingerprint(t, "media-capture"),
		RequirementsFingerprint: workflowTestFingerprint(t, "media-requirements"),
		Artifacts: []api.MediaArtifact{
			{
				ID:       "screen-1",
				Kind:     api.MediaArtifactScreenshot,
				Selected: true,
			},
			{
				ID:       "menu-1",
				Kind:     api.MediaArtifactDVDMenu,
				Selected: false,
			},
			{
				ID:       "hosted-1",
				Kind:     api.MediaArtifactHostedImage,
				Selected: true,
				Source:   "screen-1",
				Host:     "img.example",
				URL:      "https://img.example/screen.png",
			},
		},
	}
	instructions := api.DescriptionInstructions{
		Overrides: []api.DescriptionOverrideInput{{GroupKey: "unit3d", Source: "User description."}},
		QuestionnaireAnswers: map[api.TrackerID]map[string]string{
			"ALPHA": {"edition": "theatrical"},
		},
		TemplateVersion: "v1",
	}
	privateMedia := workflowMediaPrivateArtifacts{
		Screenshots: []api.ScreenshotImage{{Path: "C:\\private\\screen.png"}},
		DVDMenus:    []api.DVDMenuCaptureImage{{ScreenshotImage: api.ScreenshotImage{Path: "C:\\private\\menu.png"}}},
		HostedImages: map[api.PublicResourceID]api.UploadedImageLink{
			"hosted-1": {
				ImagePath: "C:\\private\\screen.png",
				Host:      "img.example",
				RawURL:    "https://img.example/screen.png",
			},
		},
		HostedSources: map[api.PublicResourceID]api.PublicResourceID{"hosted-1": "screen-1"},
	}
	snapshot, err := builder.Build(context.Background(), release, projections, media, privateMedia, instructions, time.Now())
	if err != nil {
		t.Fatalf("build workflow descriptions: %v", err)
	}
	if service.builds != 1 || snapshot.Status != api.StageStatusCompleted || len(snapshot.Descriptions) != 1 {
		t.Fatalf("description build = %#v builds=%d", snapshot, service.builds)
	}
	if len(snapshot.TrackerResults) != 2 || snapshot.TrackerResults[0].Status != api.StageStatusCompleted ||
		snapshot.TrackerResults[1].Status != api.StageStatusCompleted {
		t.Fatalf("description tracker results = %#v", snapshot.TrackerResults)
	}
	if len(service.trackers) != 2 || service.trackers[0] != "ALPHA" || service.trackers[1] != "BETA" {
		t.Fatalf("description targets = %v", service.trackers)
	}
	if len(service.subject.ExactScreenshots) != 1 || service.subject.ExactScreenshots[0].Path != "C:\\private\\screen.png" {
		t.Fatalf("exact description screenshots = %#v", service.subject.ExactScreenshots)
	}
	if len(service.subject.ExactUploadedImages) != 1 || service.subject.ExactUploadedImages[0].RawURL != "https://img.example/screen.png" {
		t.Fatalf("exact hosted images = %#v", service.subject.ExactUploadedImages)
	}
	if service.subject.ImageHost.SkipUpload == nil || !*service.subject.ImageHost.SkipUpload {
		t.Fatalf("description subject allowed hidden image upload: %#v", service.subject.ImageHost)
	}
	description := snapshot.Descriptions[0]
	if len(description.TrackerIDs) != 2 || description.TrackerIDs[0] != "ALPHA" || description.TrackerIDs[1] != "BETA" ||
		description.ContentFingerprint == "" {
		t.Fatalf("description group = %#v", description)
	}
	if len(resolver.input.DescriptionGroups) != 1 || !resolver.input.DescriptionGroups[0].HasOverride ||
		resolver.input.QuestionnaireAnswers["ALPHA"]["edition"] != "theatrical" {
		t.Fatalf("description subject input = %#v", resolver.input)
	}
	changed := instructions
	changed.TemplateVersion = "v2"
	changedInput, changedTemplate, err := builder.Fingerprints(context.Background(), release, projections, media, privateMedia, changed)
	if err != nil {
		t.Fatalf("changed description fingerprints: %v", err)
	}
	if changedInput == snapshot.InputFingerprint || changedTemplate == snapshot.TemplateFingerprint {
		t.Fatal("description fingerprints ignored template version")
	}
	privateMedia.HostedImages["hosted-1"] = api.UploadedImageLink{
		ImagePath: "C:\\private\\screen.png",
		Host:      "img.example",
		RawURL:    "https://img.example/replaced.png",
	}
	hostedInput, _, err := builder.Fingerprints(context.Background(), release, projections, media, privateMedia, instructions)
	if err != nil {
		t.Fatalf("changed hosted-image fingerprint: %v", err)
	}
	if hostedInput == snapshot.InputFingerprint {
		t.Fatal("description fingerprint ignored exact hosted-image lineage")
	}
}

func TestWorkflowDescriptionBuilderFailsOnlyTrackerWithoutSuitableImageHost(t *testing.T) {
	t.Parallel()

	resolver := &workflowDescriptionResolverFake{}
	service := &workflowDescriptionServiceFake{preview: &api.PreparationPreview{
		Descriptions: []api.PreparationDescription{{
			GroupKey:           "alpha",
			Trackers:           []string{"ALPHA"},
			RawDescription:     "Example description.",
			RawDescriptionHTML: "<p>Example description.</p>",
		}},
		ContentFailures: []api.TrackerContentFailure{{
			Tracker: "BETA",
			Code:    api.TrackerContentFailureImageHostUnavailable,
			Message: "BETA could not find an allowed screenshot host.",
		}},
	}}
	builder := workflowDescriptionBuilder{resolver: resolver, trackers: service}
	projections := api.TrackerReleaseProjectionSet{
		ID:                "projections-1",
		Revision:          1,
		InputFingerprint:  workflowTestFingerprint(t, "projection-input"),
		PolicyFingerprint: workflowTestFingerprint(t, "projection-policy"),
		Projections: []api.TrackerReleaseProjection{
			{
				TrackerID:        "ALPHA",
				DescriptionGroup: "alpha",
				Artifacts:        api.TrackerArtifactRequirements{Description: true},
			},
			{
				TrackerID:        "BETA",
				DescriptionGroup: "beta",
				Artifacts:        api.TrackerArtifactRequirements{Description: true},
			},
		},
	}
	media := api.MediaArtifactSet{
		ID:                      "media-1",
		Revision:                1,
		CaptureFingerprint:      workflowTestFingerprint(t, "media-capture"),
		RequirementsFingerprint: workflowTestFingerprint(t, "media-requirements"),
	}
	progress := make([]api.WorkflowProgressUpdate, 0)
	ctx := api.WithWorkflowProgressReporter(context.Background(), func(update api.WorkflowProgressUpdate) {
		progress = append(progress, update)
	})

	snapshot, err := builder.Build(
		ctx,
		api.ReleaseRef{SourcePath: "C:\\releases\\Example.Release.2026", Generation: 1},
		projections,
		media,
		workflowMediaPrivateArtifacts{},
		api.DescriptionInstructions{},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("build workflow descriptions: %v", err)
	}
	if snapshot.Status != api.StageStatusCompleted || len(snapshot.Descriptions) != 1 || len(snapshot.Failures) != 1 {
		t.Fatalf("description snapshot = %#v", snapshot)
	}
	if len(snapshot.TrackerResults) != 2 || snapshot.TrackerResults[0].TrackerID != "ALPHA" ||
		snapshot.TrackerResults[0].Status != api.StageStatusCompleted || snapshot.TrackerResults[1].TrackerID != "BETA" ||
		snapshot.TrackerResults[1].Status != api.StageStatusFailed {
		t.Fatalf("description tracker results = %#v", snapshot.TrackerResults)
	}
	if progress[len(progress)-1].ItemID != "BETA" || progress[len(progress)-1].Status != api.StageStatusFailed ||
		progress[len(progress)-1].Completed != 2 || progress[len(progress)-1].Total != 2 {
		t.Fatalf("description progress = %#v", progress)
	}
}

func TestWorkflowDescriptionBuilderHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (workflowDescriptionBuilder{}).Build(
		ctx,
		api.ReleaseRef{},
		api.TrackerReleaseProjectionSet{},
		api.MediaArtifactSet{},
		workflowMediaPrivateArtifacts{},
		api.DescriptionInstructions{},
		time.Now(),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled description build error = %v", err)
	}
}
