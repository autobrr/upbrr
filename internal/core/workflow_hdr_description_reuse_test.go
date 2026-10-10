// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRDescriptionReuseRejectsConsumedPlots(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		name := "generated"
		if mixed {
			name = "mixed"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newWorkflowDescriptionReuseFixture(t)
			fixture.builder.resolver = hdrDescriptionReuseResolver{fixture.resolver.subject}
			fixture.instructions.Overrides = nil
			if mixed {
				fixture.instructions.Overrides = []api.DescriptionOverrideInput{{
					GroupKey:   "alpha",
					Source:     "Owner's final text",
					Final:      true,
					TrackerIDs: []api.TrackerID{"ALPHA"},
				}}
			}
			extractor, resolver, sidecar := hdrFixture(t)
			record, retained, err := extractor.retainExtraction(t.Context(), resolver.subject.Release, sidecar, "cache-metadata")
			if err != nil {
				t.Fatal(err)
			}
			analysis, paths, err := extractor.Build(t.Context(), resolver.subject.Release,
				api.HDRAnalysisInstructions{Release: resolver.subject.Release, TargetIDs: []string{sidecar.Identity.TargetID}},
				"cache-plot", time.Now().UTC(), nil, nil,
				map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord{record.ID: record},
				map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource{record.ID: retained}, nil)
			if err != nil {
				t.Fatal(err)
			}
			analysis.ID, analysis.Revision = "cache-hdr", 1
			calls := 0
			resources := releaseworkflow.DescriptionResources{Media: fixture.oldPrivate, HDR: func(context.Context) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
				calls++
				return analysis, paths, nil
			}}
			// The rendered text is supplied here; real hosting/block generation is
			// covered separately. This test exercises the actual reuse consumer.
			fixture.snapshot.Descriptions[0].Source += "\n[spoiler=source_hdr]prior HDR plot[/spoiler]"
			if err := fixture.recordReusableDescriptions(t, fixture.oldRelease, fixture.oldMedia, resources, fixture.snapshot); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || fixture.repository.saves != 0 {
				t.Fatalf("consumed HDR entered reusable cache: resource calls=%d saves=%d", calls, fixture.repository.saves)
			}
			plainInstructions := fixture.instructions
			plainInstructions.Overrides = nil
			restored, _, err := fixture.builder.RestoreCompatibleDescriptions(t.Context(), fixture.oldRelease, fixture.projections,
				fixture.oldMedia, fixture.oldPrivate, plainInstructions)
			if err != nil || len(restored.Descriptions) != 0 {
				t.Fatalf("disabled HDR restored generated block: %#v %v", restored, err)
			}
			fresh, err := fixture.builder.Build(t.Context(), fixture.oldRelease, fixture.projections, fixture.oldMedia,
				fixture.oldPrivate, plainInstructions, time.Now().UTC())
			if err != nil || fixture.service.builds != 1 || len(fresh.Descriptions) == 0 || strings.Contains(fresh.Descriptions[0].Source, "source_hdr") {
				t.Fatalf("disabled HDR did not build clean text: %#v %v", fresh, err)
			}
			if _, _, err := fixture.builder.RestoreCompatibleDescriptions(t.Context(), fixture.currentRelease, fixture.projections,
				fixture.currentMedia, resources, plainInstructions); err != nil || calls != 1 || fixture.repository.loads != 1 {
				t.Fatalf("re-enabled HDR used plain cache or resolved unused resources: calls=%d loads=%d error=%v", calls, fixture.repository.loads, err)
			}
		})
	}
}

func TestHDRDescriptionReusePreservesAllFinalTextAfterDisable(t *testing.T) {
	fixture := newWorkflowDescriptionReuseFixture(t)
	fixture.builder.resolver = hdrDescriptionReuseResolver{fixture.resolver.subject}
	source := "Owner's final text [spoiler=source_hdr]intentional notes[/spoiler]"
	fixture.instructions.Overrides = []api.DescriptionOverrideInput{{
		GroupKey:   "default",
		Source:     source,
		Final:      true,
		TrackerIDs: []api.TrackerID{"ALPHA", "BETA"},
	}}
	for index := range fixture.snapshot.Descriptions {
		fixture.snapshot.Descriptions[index].Source = source
	}
	resources := releaseworkflow.DescriptionResources{Media: fixture.oldPrivate, HDR: func(context.Context) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
		t.Error("all-final cache save resolved unused missing HDR")
		return api.HDRAnalysisResult{}, nil, releaseworkflow.ErrPrivateResourceUnavailable
	}}
	if err := fixture.recordReusableDescriptions(t, fixture.oldRelease, fixture.oldMedia, resources, fixture.snapshot); err != nil || fixture.repository.saves != 1 {
		t.Fatalf("all-final text was not reusable: saves=%d error=%v", fixture.repository.saves, err)
	}
	fixture.builder.resolver = hdrDescriptionReuseResolver{reusableDescriptionSubjectForFixture(fixture.currentRelease, 2)}
	currentInstructions := fixture.instructions
	currentInstructions.Overrides = nil
	restored, restoredInstructions, err := fixture.builder.RestoreCompatibleDescriptions(t.Context(), fixture.currentRelease, fixture.projections,
		fixture.currentMedia, fixture.currentPrivate, currentInstructions)
	if err != nil || len(restored.Descriptions) != 2 || restored.Descriptions[0].Source != source ||
		len(restoredInstructions.Overrides) != 1 || !restoredInstructions.Overrides[0].Final || restoredInstructions.Overrides[0].Source != source {
		t.Fatalf("disabled HDR stripped final editor text: %#v %#v %v", restored, restoredInstructions, err)
	}
}

type hdrDescriptionReuseResolver struct{ subject api.UploadSubject }

func (r hdrDescriptionReuseResolver) ResolveUploadSubject(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
	subject := r.subject
	subject.DescriptionGroups = input.DescriptionGroups
	return subject, nil
}

func TestHDRDescriptionReuseRejectsLegacyV1Records(t *testing.T) {
	fixture := newWorkflowDescriptionReuseFixture(t)
	fixture.instructions.Overrides = nil
	subject, err := fixture.builder.resolveSubject(t.Context(), fixture.oldRelease, fixture.projections, fixture.instructions)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := resolveWorkflowExactMedia(fixture.oldPrivate, fixture.oldMedia)
	if err != nil {
		t.Fatal(err)
	}
	descriptionSubject := api.NewDescriptionSubject(subject)
	descriptionSubject.ExactMedia = exact
	normalized, local, available, err := normalizeReusableDescriptionSubject(t.Context(), descriptionSubject)
	if err != nil || !available {
		t.Fatalf("legacy subject: %v available=%t", err, available)
	}
	media, available, err := normalizeReusableDescriptionMedia(t.Context(), exact)
	if err != nil || !available {
		t.Fatalf("legacy media: %v available=%t", err, available)
	}
	// Preserve the historical v1 schema exactly, independently of production's
	// current version. V1 omitted consumed HDR assets from its compatibility key.
	legacy, err := api.CanonicalWorkflowFingerprint(struct {
		Version          string
		Config           config.Config
		CompatibilityKey api.MediaCompatibilityKey
		Projections      []api.TrackerReleaseProjection
		Instructions     api.DescriptionInstructions
		Subject          api.DescriptionSubject
		LocalResources   workflowReusableDescriptionLocalResources
		ExactMedia       workflowReusableDescriptionMedia
	}{
		Version:          "description-reuse-v1",
		Config:           fixture.builder.config,
		CompatibilityKey: subject.MediaBinding.CompatibilityKey,
		Projections:      reusableDescriptionProjections(fixture.projections.Projections),
		Instructions:     fixture.instructions,
		Subject:          normalized,
		LocalResources:   local,
		ExactMedia:       media,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.snapshot.Descriptions[0].Source += "\n[spoiler=source_hdr]legacy HDR plot[/spoiler]"
	fixture.repository.store(fixture.oldRelease.SourcePath, api.ReusableDescription{
		CompatibilityFingerprint: legacy,
		Descriptions:             fixture.snapshot.Descriptions,
		TrackerResults:           fixture.snapshot.TrackerResults,
	})
	fixture.resolver.subject = reusableDescriptionSubjectForFixture(fixture.currentRelease, 2)
	restored, _, err := fixture.builder.RestoreCompatibleDescriptions(t.Context(), fixture.currentRelease, fixture.projections,
		fixture.currentMedia, fixture.currentPrivate, fixture.instructions)
	if err != nil || len(restored.Descriptions) != 0 || fixture.repository.loads != 1 {
		t.Fatalf("legacy cache survived invalidation: %#v loads=%d error=%v", restored, fixture.repository.loads, err)
	}
}
