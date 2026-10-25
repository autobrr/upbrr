// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowPreflightImportedReferencesDoNotNeedUploader(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		capability bool
		url        string
		stale      bool
		screens    int
		menus      int
		wantReady  bool
	}{
		{
			name:       "current allowed reference",
			capability: true,
			url:        "https://pixhost.to/unknown-format",
			wantReady:  true,
		},
		{name: "other tracker remains independent", url: "https://pixhost.to/unknown-format"},
		{
			name:       "stale reference",
			capability: true,
			url:        "https://pixhost.to/unknown-format",
			stale:      true,
		},
		{
			name:       "disallowed host",
			capability: true,
			url:        "https://imgbox.com/unknown-format",
		},
		{name: "missing reference", capability: true},
		{
			name:       "explicit screenshot count still needs uploader",
			capability: true,
			url:        "https://pixhost.to/unknown-format",
			screens:    1,
		},
		{
			name:       "DVD menus still need uploader",
			capability: true,
			url:        "https://pixhost.to/unknown-format",
			menus:      1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := trackers.NewRegistry()
			if err := registry.RegisterDescriptor(trackers.Descriptor{
				Name:          "IMPORTER",
				Definition:    workflowImageHostPolicyDefinition{name: "IMPORTER"},
				ImageHost:     &trackers.ImageHostPolicy{AllowedHosts: []string{"pixhost"}},
				WorkflowMedia: &trackers.WorkflowMediaRequirements{AllowImportedScreenshotReferences: test.capability},
			}); err != nil {
				t.Fatal(err)
			}
			subject := api.UploadSubject{
				SourcePath: "source",
				MediaBinding: api.PreparedMediaBinding{
					SourcePath:               "source",
					PreparedGeneration:       3,
					PreparedMediaFingerprint: "media",
				},
				Identity: api.ExternalIdentity{
					SourcePath: "source",
					Generation: 3,
					Resolution: api.IdentityResolutionKey{SourceFingerprint: "source-fingerprint"},
				},
				ProviderMetadata: api.SourceScopedMetadata{
					SourcePath: "source",
					Generation: 3,
					ImportedScreenshotEvidence: &api.ImportedScreenshotEvidence{
						Tracker:                "SOURCE",
						TrackerID:              "42",
						SourceFingerprint:      "source-fingerprint",
						PolicyVersion:          api.ImportedScreenshotEvidencePolicyVersion,
						DescriptionFingerprint: "description-images",
						References:             []api.ImportedScreenshotReference{{RawURL: test.url}},
					},
				},
			}
			if test.stale {
				subject.ProviderMetadata.Generation++
			}
			builder := workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: registry}
			assessment, finalized, err := builder.Build(t.Context(), subject,
				api.TrackerCatalogSnapshot{Trackers: []api.TrackerCatalogDescriptor{{TrackerID: "IMPORTER"}}},
				api.TrackerRuntimeSnapshot{},
				api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
					TrackerID:   "IMPORTER",
					Readiness:   api.ReadinessStatusReady,
					DupeReady:   true,
					UploadReady: true,
					Artifacts:   api.TrackerArtifactRequirements{ScreenshotCount: test.screens, DVDMenuCount: test.menus},
				}}}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(assessment.Results) != 1 || (assessment.Results[0].State == api.TrackerPreflightStateReady) != test.wantReady ||
				len(finalized) != 1 || finalized[0].DupeReady != test.wantReady {
				t.Fatalf("preflight = %+v, projections = %+v, want ready %t", assessment, finalized, test.wantReady)
			}
			if subject.ExactMedia != nil {
				t.Fatal("reference-only evidence created exact media")
			}
		})
	}
}

func TestWorkflowMediaReferenceOnlyProjectionPreservesExplicitCapture(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		instructions api.MediaCaptureInstructions
		wantCapture  bool
	}{
		{name: "reference-only projection skips capture"},
		{
			name:         "explicit count captures",
			instructions: api.MediaCaptureInstructions{ScreenshotCount: 1},
			wantCapture:  true,
		},
		{
			name:         "manual frame captures",
			instructions: api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal, ManualFrames: []int{30}},
			wantCapture:  true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			screenshots := &workflowScreenshotFake{root: t.TempDir()}
			builder := workflowMediaBuilder{resolver: workflowMediaResolverFake{}, screenshots: screenshots}
			snapshot, privateValue, err := builder.Build(t.Context(), api.ReleaseRef{SourcePath: "source", Generation: 3},
				api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "IMPORTER"}}},
				test.instructions, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if (screenshots.captures > 0) != test.wantCapture {
				t.Fatalf("capture calls = %d, want capture %t", screenshots.captures, test.wantCapture)
			}
			if !test.wantCapture {
				private, ok := privateValue.(workflowMediaPrivateArtifacts)
				if !ok || snapshot.Status != api.StageStatusSkipped || len(snapshot.Artifacts) != 0 || len(private.ArtifactImages) != 0 || len(private.HostedImages) != 0 {
					t.Fatalf("reference-only projection invented media: %+v, %+v", snapshot, privateValue)
				}
			}
		})
	}
}

func TestWorkflowDescriptionFingerprintBindsReferenceSelectionPolicy(t *testing.T) {
	subject := api.UploadSubject{}
	original, _, err := workflowDescriptionFingerprints(api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, api.MediaArtifactSet{}, api.DescriptionInstructions{}, subject, nil)
	if err != nil {
		t.Fatal(err)
	}
	retainWorkflowScreenshotSelection(&subject, releaseworkflow.DescriptionResources{SuppressImportedScreenshotReferences: map[string]bool{"LST": true}})
	changed, _, err := workflowDescriptionFingerprints(api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, api.MediaArtifactSet{}, api.DescriptionInstructions{}, subject, nil)
	if err != nil {
		t.Fatal(err)
	}
	if original == changed {
		t.Fatal("nonserialized selection policy was omitted from fingerprint")
	}
}

func TestReusableDescriptionBindsAndRestoresReferenceSelectionPolicy(t *testing.T) {
	fixture := newWorkflowDescriptionReuseFixture(t)
	policy := map[string]bool{"ALPHA": true}
	old := releaseworkflow.DescriptionResources{Media: fixture.oldPrivate, SuppressImportedScreenshotReferences: policy}
	if err := fixture.recordReusableDescriptions(t, fixture.oldRelease, fixture.oldMedia, old, fixture.snapshot); err != nil {
		t.Fatal(err)
	}
	fixture.resolver.subject = reusableDescriptionSubjectForFixture(fixture.currentRelease, 2)
	instructions := fixture.instructions
	instructions.Overrides = nil
	current := releaseworkflow.DescriptionResources{Media: fixture.currentPrivate, SuppressImportedScreenshotReferences: policy}
	restored, _, err := fixture.builder.RestoreCompatibleDescriptions(t.Context(), fixture.currentRelease, fixture.projections, fixture.currentMedia, current, instructions)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != api.StageStatusCompleted {
		t.Fatal("policy-only wrapper disabled matching reusable descriptions")
	}
	current.SuppressImportedScreenshotReferences = nil
	changed, _, err := fixture.builder.RestoreCompatibleDescriptions(t.Context(), fixture.currentRelease, fixture.projections, fixture.currentMedia, current, instructions)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Status != "" {
		t.Fatal("changed nonserialized policy reused old rendered content")
	}
}
