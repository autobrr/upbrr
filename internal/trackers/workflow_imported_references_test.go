// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func importedReferenceProjectionSubject() api.UploadSubject {
	return api.UploadSubject{
		SourcePath:  "source",
		ReleaseName: "Example.Release.2026.1080p-GRP",
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
				References:             []api.ImportedScreenshotReference{{RawURL: "https://pixhost.to/unknown-format"}},
			},
		},
	}
}

func TestWorkflowProjectorImportedReferencesAvoidOnlyDefaultCapture(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name              string
		trackerImageCount int
		override          *int
		defaultCount      bool
		withoutReferences bool
		stale             bool
		url               string
		want              int
	}{
		{name: "global fallback is not forced"},
		{name: "tracker minimum is not forced", trackerImageCount: 3},
		{
			name:              "CLI default count is not forced",
			trackerImageCount: 3,
			override:          new(7),
			defaultCount:      true,
		},
		{
			name:              "CLI default without receipt remains unchanged",
			trackerImageCount: 3,
			override:          new(7),
			defaultCount:      true,
			withoutReferences: true,
			want:              7,
		},
		{
			name:              "CLI default with stale receipt remains unchanged",
			trackerImageCount: 3,
			override:          new(7),
			defaultCount:      true,
			stale:             true,
			want:              7,
		},
		{
			name:              "CLI default with rejected host remains unchanged",
			trackerImageCount: 3,
			override:          new(7),
			defaultCount:      true,
			url:               "https://imgbox.com/unknown-format",
			want:              7,
		},
		{
			name:     "explicit count remains independent",
			override: new(2),
			want:     2,
		},
		{
			name:              "explicit count preserves tracker minimum",
			trackerImageCount: 3,
			override:          new(2),
			want:              3,
		},
		{
			name:              "explicit zero retains existing minimum semantics",
			trackerImageCount: 3,
			override:          new(0),
			want:              3,
		},
		{
			name:  "stale evidence does not suppress capture",
			stale: true,
			want:  4,
		},
		{
			name: "disallowed reference does not suppress capture",
			url:  "https://imgbox.com/unknown-format",
			want: 4,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := NewRegistry()
			for _, name := range []string{"IMPORTER", "OTHER"} {
				descriptor := Descriptor{
					Name:              name,
					Definition:        stubDefinition{name: name},
					UploadContentMode: UploadContentModeDescription,
					ImageHost:         &ImageHostPolicy{AllowedHosts: []string{"pixhost"}},
				}
				if name == "IMPORTER" {
					descriptor.WorkflowMedia = &WorkflowMediaRequirements{AllowImportedScreenshotReferences: true}
				}
				if err := registry.RegisterDescriptor(descriptor); err != nil {
					t.Fatal(err)
				}
			}
			cfg := config.Config{
				ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 4},
				Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
					"IMPORTER": {ImageCount: test.trackerImageCount},
				}},
			}
			projector, err := NewWorkflowProjector(registry, cfg, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			subject := importedReferenceProjectionSubject()
			if test.stale {
				subject.ProviderMetadata.Generation++
			}
			if test.url != "" {
				subject.ProviderMetadata.ImportedScreenshotEvidence.References[0].RawURL = test.url
			}
			if test.withoutReferences {
				subject.ProviderMetadata.ImportedScreenshotEvidence = nil
			}
			instructions := map[api.TrackerID]api.TrackerProjectionInstructions{
				"IMPORTER": {ScreenshotCount: test.override, ScreenshotCountIsDefault: test.defaultCount},
			}
			wantOther := 4
			if test.defaultCount {
				instructions["OTHER"] = api.TrackerProjectionInstructions{ScreenshotCount: test.override, ScreenshotCountIsDefault: true}
				wantOther = *test.override
			}
			_, _, _, projections, err := projector.Build(t.Context(), api.ReleaseSnapshot{}, subject,
				[]api.TrackerID{"IMPORTER", "OTHER"},
				instructions,
				nil, api.WorkflowExecutionModeNormal)
			if err != nil {
				t.Fatal(err)
			}
			if len(projections.Projections) != 2 || projections.Projections[0].Artifacts.ScreenshotCount != test.want ||
				projections.Projections[1].Artifacts.ScreenshotCount != wantOther {
				t.Fatalf("imported reference requirements = %+v, want %d and unchanged sibling %d", projections.Projections, test.want, wantOther)
			}
			if subject.ExactMedia != nil || !test.withoutReferences && len(subject.ProviderMetadata.ImportedScreenshotEvidence.References) != 1 {
				t.Fatal("projection changed canonical evidence or created local media")
			}
		})
	}
}

func TestFilterImportedScreenshotEvidencePreservesHostPolicyAndSource(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	for _, descriptor := range []Descriptor{
		{
			Name:          "IMPORTER",
			Definition:    stubDefinition{name: "IMPORTER"},
			ImageHost:     &ImageHostPolicy{AllowedHosts: []string{"pixhost", "imgbb"}},
			WorkflowMedia: &WorkflowMediaRequirements{AllowImportedScreenshotReferences: true},
		},
		{
			Name:          "OTHER",
			Definition:    stubDefinition{name: "OTHER"},
			WorkflowMedia: &WorkflowMediaRequirements{AllowImportedScreenshotReferences: true},
		},
		{
			Name:       "OWNER",
			Definition: stubDefinition{name: "OWNER"},
			ImageHost:  &ImageHostPolicy{OwnedHosts: []string{"private.example"}},
		},
		{Name: "UNDECLARED", Definition: stubDefinition{name: "UNDECLARED"}},
	} {
		if err := registry.RegisterDescriptor(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name      string
		tracker   string
		overrides api.ImageHostOverrides
		stale     bool
		want      int
	}{
		{
			name:    "only allowed host retained",
			tracker: "IMPORTER",
			want:    1,
		},
		{
			name:      "explicit preferred host keeps allowed fallback",
			tracker:   "IMPORTER",
			overrides: api.ImageHostOverrides{PreferredHost: new("imgbb")},
			want:      1,
		},
		{
			name:      "failed host excluded",
			tracker:   "IMPORTER",
			overrides: api.ImageHostOverrides{FailedHosts: []string{"pixhost"}},
		},
		{
			name:      "invalid override fails closed",
			tracker:   "IMPORTER",
			overrides: api.ImageHostOverrides{PreferredHost: new("unsupported")},
		},
		{
			name:    "owned host excluded from unrestricted target",
			tracker: "OTHER",
			want:    2,
		},
		{name: "undeclared tracker excluded", tracker: "UNDECLARED"},
		{
			name:    "stale canonical evidence excluded",
			tracker: "IMPORTER",
			stale:   true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := importedReferenceProjectionSubject()
			subject.ProviderMetadata.ImportedScreenshotEvidence.References = append(subject.ProviderMetadata.ImportedScreenshotEvidence.References,
				api.ImportedScreenshotReference{RawURL: "https://imgbox.com/not-allowed"},
				api.ImportedScreenshotReference{RawURL: "https://private.example/owner-only"},
				api.ImportedScreenshotReference{RawURL: "https://passthepopcorn.me/source-only.png"},
				api.ImportedScreenshotReference{RawURL: "https://wsrv.nl/?url=https%3A%2F%2Fi.ibb.co%2Ffull.png"})
			subject.ImageHostOverrides = test.overrides
			if test.stale {
				subject.ProviderMetadata.Generation++
			}
			filtered := FilterImportedScreenshotEvidenceForTrackerWithRegistry(registry, config.Config{}, test.tracker, subject)
			evidence := filtered.CurrentImportedScreenshotEvidence()
			if test.want == 0 {
				if evidence != nil {
					t.Fatalf("unexpected reusable evidence: %+v", evidence)
				}
			} else {
				if evidence == nil || len(evidence.References) != test.want || evidence.TrackerID != "42" || evidence.DescriptionFingerprint != "description-images" {
					t.Fatalf("filtered evidence = %+v, want %d", evidence, test.want)
				}
				filtered.ProviderMetadata.ImportedScreenshotEvidence.References[0].RawURL = "changed"
			}
			original := subject.ProviderMetadata.ImportedScreenshotEvidence
			if len(original.References) != 5 || original.References[0].RawURL != "https://pixhost.to/unknown-format" || filtered.ExactMedia != nil {
				t.Fatalf("filter mutated canonical receipt or invented media: %+v", subject)
			}
		})
	}
}

type importedReferenceNoIORepository struct{ UploadPersistence }

func TestImportedReferencesDoNotForceDescriptionRehosting(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:          "IMPORTER",
		Definition:    stubDefinition{name: "IMPORTER"},
		ImageHost:     &ImageHostPolicy{AllowedHosts: []string{"pixhost"}},
		WorkflowMedia: &WorkflowMediaRequirements{AllowImportedScreenshotReferences: true},
	}); err != nil {
		t.Fatal(err)
	}
	subject := importedReferenceProjectionSubject()
	subject.Options.KeepImages = true
	subject.ExactMedia = &api.ExactMediaAssets{}
	preloaded := &preloadedDescriptionAssetData{
		exactMedia: subject.ExactMedia.Clone(), screenshotSlotsLoaded: true,
	}
	repo := importedReferenceNoIORepository{}
	resolution, err := ensureDescriptionImageHostWithDataAndRegistry(t.Context(), "IMPORTER", subject,
		trackerTestConfig(t, config.Config{}), config.TrackerConfig{}, repo, nil, api.NopLogger{}, registry, preloaded)
	if err != nil || resolution.blocking || resolution.feedback.Reuploaded || len(resolution.screenshots) != 0 {
		t.Fatalf("reference-only description host resolution = %+v, err = %v", resolution, err)
	}
	assets := DescriptionAssets{Description: "Source release notes"}
	service := &Service{
		registry: registry,
		repo:     repo,
		logger:   api.NopLogger{},
	}
	if err := service.rehostSourceOnlyDescriptionImages(t.Context(), "IMPORTER", subject, config.TrackerConfig{}, &assets, preloaded); err != nil {
		t.Fatalf("reference-only source description forced rehosting: %v", err)
	}
	if len(preloaded.screenshotSlots) != 0 || len(subject.ExactMedia.Screenshots) != 0 || len(subject.ExactMedia.ScreenshotUploads) != 0 {
		t.Fatal("reference-only description host resolution fabricated local images")
	}
}

func TestImportedReferencePolicyChangesCatalogFingerprint(t *testing.T) {
	t.Parallel()
	descriptor := Descriptor{Name: "IMPORTER", WorkflowMedia: &WorkflowMediaRequirements{}}
	before, err := descriptorPolicyFingerprint(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	descriptor.WorkflowMedia.AllowImportedScreenshotReferences = true
	after, err := descriptorPolicyFingerprint(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("imported reference policy change reused the previous catalog fingerprint")
	}
}

func TestExplicitScreenshotCountExcludesImportedReferenceFacts(t *testing.T) {
	for _, test := range []struct {
		name           string
		count          *int
		automatic      bool
		wantReferences bool
	}{
		{
			name:           "automatic default",
			count:          new(3),
			automatic:      true,
			wantReferences: true,
		},
		{name: "explicit three", count: new(3)},
		{name: "explicit zero", count: new(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			checked := false
			err := registry.RegisterDescriptor(Descriptor{
				Name:              "IMPORTER",
				Definition:        stubDefinition{name: "IMPORTER"},
				UploadContentMode: UploadContentModeDescription,
				WorkflowMedia:     &WorkflowMediaRequirements{AllowImportedScreenshotReferences: true},
				Validation: ValidationPolicyBinding{ID: "reference-selection-v1", Check: func(_ context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
					checked = true
					if got := subject.ImportedScreenshotEvidence != nil; got != test.wantReferences {
						t.Errorf("reference facts present=%v, want %v", got, test.wantReferences)
					}
					return nil, nil
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			projector, err := NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, _, err = projector.Build(t.Context(), api.ReleaseSnapshot{}, importedReferenceProjectionSubject(), []api.TrackerID{"IMPORTER"}, map[api.TrackerID]api.TrackerProjectionInstructions{"IMPORTER": {ScreenshotCount: test.count, ScreenshotCountIsDefault: test.automatic}}, nil, api.WorkflowExecutionModeNormal)
			if err != nil {
				t.Fatal(err)
			}
			if !checked {
				t.Fatal("validation did not run")
			}
		})
	}
}

func TestDescriptionScreenshotSelectionPolicySurvivesTrackerProjection(t *testing.T) {
	source := importedReferenceProjectionSubject()
	source.SuppressImportedScreenshotReferences = map[string]bool{"IMPORTER": true}
	description := api.NewDescriptionSubject(source)
	projected := uploadSubjectForDescription(description)
	projected.SuppressImportedScreenshotReferences["IMPORTER"] = false
	if !description.SuppressImportedScreenshotReferences["IMPORTER"] || !source.SuppressImportedScreenshotReferences["IMPORTER"] {
		t.Fatal("description policy was not detached")
	}
}
