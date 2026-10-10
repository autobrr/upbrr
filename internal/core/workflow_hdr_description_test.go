// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowFinalDescriptionDoesNotResolveOrHostUnusedHDR(t *testing.T) {
	registry := trackerimpl.MustNewRegistry()
	descriptor, _ := registry.LookupDescriptor("AITHER")
	builder := workflowDescriptionBuilder{
		resolver: &workflowDescriptionResolverFake{},
		trackers: trackers.NewServiceWithRegistry(config.Config{}, api.NopLogger{}, nil, registry),
	}
	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
		TrackerID:        "AITHER",
		DescriptionGroup: descriptor.DescriptionGroup,
		Artifacts:        api.TrackerArtifactRequirements{Description: true},
	}}}
	resources := releaseworkflow.DescriptionResources{
		Media: workflowMediaPrivateArtifacts{},
		HDR: func(context.Context) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
			t.Error("final description resolved unused missing HDR resource")
			return api.HDRAnalysisResult{}, nil, releaseworkflow.ErrPrivateResourceUnavailable
		},
	}
	source := "[b]Owner's final description[/b]"
	instructions := api.DescriptionInstructions{Overrides: []api.DescriptionOverrideInput{{
		GroupKey:   descriptor.DescriptionGroup,
		Source:     source,
		Final:      true,
		TrackerIDs: []api.TrackerID{"AITHER"},
	}}}
	release := api.ReleaseRef{SourcePath: "Synthetic.mkv", Generation: 1}
	if _, _, err := builder.Fingerprints(t.Context(), release, projections, api.MediaArtifactSet{}, resources, instructions); err != nil {
		t.Fatal(err)
	}
	result, err := builder.Build(t.Context(), release, projections, api.MediaArtifactSet{}, resources, instructions, time.Now().UTC())
	if err != nil || len(result.Descriptions) != 1 || result.Descriptions[0].Source != source {
		t.Fatalf("final text depended on HDR hosting: %#v %v", result, err)
	}
}

func TestWorkflowMixedDescriptionsHostHDRForGeneratedLanesOnce(t *testing.T) {
	extractor, resolver, sidecar := hdrFixture(t)
	release := resolver.subject.Release
	record, retained, err := extractor.retainExtraction(t.Context(), release, sidecar, "description-metadata")
	if err != nil {
		t.Fatal(err)
	}
	analysis, paths, err := extractor.Build(t.Context(), release, api.HDRAnalysisInstructions{Release: release, TargetIDs: []string{sidecar.Identity.TargetID}},
		"description-plot", time.Now().UTC(), nil, nil,
		map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord{record.ID: record},
		map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource{record.ID: retained}, nil)
	if err != nil {
		t.Fatal(err)
	}
	analysis.ID, analysis.Revision = "description-hdr", 1
	host := &descriptionAudioHostFake{}
	preview := &workflowDescriptionServiceFake{}
	builder := workflowDescriptionBuilder{
		resolver: &workflowDescriptionResolverFake{},
		trackers: preview,
		media: &mediaModule{
			cfg:      config.Config{ImageHosting: config.ImageHostingConfig{Host1: "onlyimage"}},
			logger:   api.NopLogger{},
			registry: mediaImageHostRegistry(t),
			images:   host,
		},
	}
	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
		{TrackerID: "ONE", Artifacts: api.TrackerArtifactRequirements{Description: true}},
		{TrackerID: "TWO", Artifacts: api.TrackerArtifactRequirements{Description: true}},
	}}
	instructions := api.DescriptionInstructions{Overrides: []api.DescriptionOverrideInput{{
		GroupKey:   "default",
		Source:     "Owner's final ONE description",
		Final:      true,
		TrackerIDs: []api.TrackerID{"ONE"},
	}}}
	resources := releaseworkflow.DescriptionResources{Media: workflowMediaPrivateArtifacts{}, HDR: func(context.Context) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
		return analysis, paths, nil
	}}
	_, err = builder.Build(t.Context(), release, projections, api.MediaArtifactSet{}, resources, instructions, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	exact := preview.subject.ExactMedia
	if len(host.images) != 1 || host.images[0].Purpose != api.ScreenshotPurposeHDRAnalysis || len(exact.HDRUploadHosts) != 1 || exact.HDRUploadHosts["TWO"] != "onlyimage" {
		t.Fatalf("HDR uploaded twice or for final lane: images=%#v assets=%#v", host.images, exact)
	}
	resources.HDR = func(context.Context) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
		return api.HDRAnalysisResult{}, nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	_, err = builder.Build(t.Context(), release, projections, api.MediaArtifactSet{}, resources, instructions, time.Now().UTC())
	if !errors.Is(err, releaseworkflow.ErrPrivateResourceUnavailable) {
		t.Fatalf("generated lane skipped HDR authority: %v", err)
	}
}

func TestFinalDescriptionReuseDoesNotResolveUnusedHDR(t *testing.T) {
	fixture := newWorkflowDescriptionReuseFixture(t)
	fixture.resolver.subject.DescriptionGroupsFinal = true
	fixture.resolver.subject.DescriptionGroups = []api.DescriptionBuilderGroup{{
		GroupKey:       "default",
		Trackers:       []string{"ALPHA", "BETA"},
		RawDescription: "Owner's final text",
		HasOverride:    true,
	}}
	resources := releaseworkflow.DescriptionResources{Media: fixture.oldPrivate, HDR: func(context.Context) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
		t.Error("final description reuse resolved missing automatic HDR")
		return api.HDRAnalysisResult{}, nil, releaseworkflow.ErrPrivateResourceUnavailable
	}}
	if err := fixture.recordReusableDescriptions(t, fixture.oldRelease, fixture.oldMedia, resources, fixture.snapshot); err != nil {
		t.Fatal(err)
	}
	if fixture.repository.saves != 1 {
		t.Fatal("final description stopped preserving reusable text")
	}
}
