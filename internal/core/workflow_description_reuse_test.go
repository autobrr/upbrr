// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type workflowDescriptionReuseRepositoryFake struct {
	sourcePath string
	value      api.ReusableDescription
	saves      int
	loads      int
}

func (r *workflowDescriptionReuseRepositoryFake) store(sourcePath string, value api.ReusableDescription) {
	r.sourcePath = sourcePath
	r.value = value
	r.saves++
}

func (r *workflowDescriptionReuseRepositoryFake) LoadReusableDescription(
	_ context.Context,
	sourcePath string,
) (api.ReusableDescription, bool, error) {
	r.loads++
	if sourcePath != r.sourcePath || !r.value.Valid() {
		return api.ReusableDescription{}, false, nil
	}
	return r.value, true, nil
}

type workflowDescriptionReuseResolverFake struct {
	subject        api.UploadSubject
	inputs         []api.UploadSubjectInput
	useInputGroups bool
}

func (r *workflowDescriptionReuseResolverFake) ResolveUploadSubject(
	_ context.Context,
	input api.UploadSubjectInput,
) (api.UploadSubject, error) {
	r.inputs = append(r.inputs, input)
	subject := r.subject
	if r.useInputGroups {
		subject.DescriptionGroups = input.DescriptionGroups
	}
	return subject, nil
}

type workflowDescriptionReuseFixture struct {
	builder        workflowDescriptionBuilder
	repository     *workflowDescriptionReuseRepositoryFake
	resolver       *workflowDescriptionReuseResolverFake
	service        *workflowDescriptionServiceFake
	oldRelease     api.ReleaseRef
	currentRelease api.ReleaseRef
	projections    api.TrackerReleaseProjectionSet
	oldMedia       api.MediaArtifactSet
	currentMedia   api.MediaArtifactSet
	oldPrivate     workflowMediaPrivateArtifacts
	currentPrivate workflowMediaPrivateArtifacts
	instructions   api.DescriptionInstructions
	snapshot       api.DescriptionSet
	currentScreen  string
}

// recordReusableDescriptions simulates the repository commit after preparation;
// preparation itself must never publish cache state ahead of the workflow save.
func (f *workflowDescriptionReuseFixture) recordReusableDescriptions(t *testing.T, release api.ReleaseRef,
	media api.MediaArtifactSet, private any, snapshot api.DescriptionSet,
) error {
	t.Helper()
	saves := f.repository.saves
	record, err := f.builder.PrepareReusableDescriptions(t.Context(), release, f.projections, media, private, f.instructions, snapshot)
	if f.repository.saves != saves {
		t.Fatal("reuse preparation wrote cache before workflow commit")
	}
	if err != nil || record == nil {
		return err
	}
	f.repository.store(record.SourcePath, record.Description)
	return nil
}

func TestWorkflowDescriptionReuseRestoresCurrentGenerationWithoutBuilding(t *testing.T) {
	t.Parallel()

	fixture := newWorkflowDescriptionReuseFixture(t)
	ctx := t.Context()
	if err := fixture.recordReusableDescriptions(t, fixture.oldRelease, fixture.oldMedia, fixture.oldPrivate, fixture.snapshot); err != nil {
		t.Fatalf("record reusable descriptions: %v", err)
	}
	if fixture.repository.saves != 1 {
		t.Fatalf("reusable description saves = %d, want 1", fixture.repository.saves)
	}

	fixture.resolver.subject = reusableDescriptionSubjectForFixture(fixture.currentRelease, 2)
	currentInstructions := fixture.instructions
	currentInstructions.Overrides = nil
	restored, restoredInstructions, err := fixture.builder.RestoreCompatibleDescriptions(
		ctx,
		fixture.currentRelease,
		fixture.projections,
		fixture.currentMedia,
		fixture.currentPrivate,
		currentInstructions,
	)
	if err != nil {
		t.Fatalf("restore reusable descriptions: %v", err)
	}
	if fixture.service.builds != 0 {
		t.Fatalf("description preparation builds = %d, want 0", fixture.service.builds)
	}
	if !reflect.DeepEqual(restored.Descriptions, fixture.snapshot.Descriptions) ||
		!reflect.DeepEqual(restored.TrackerResults, fixture.snapshot.TrackerResults) {
		t.Fatalf("restored descriptions = %#v", restored)
	}
	if !reflect.DeepEqual(restoredInstructions.Overrides, fixture.instructions.Overrides) {
		t.Fatalf("restored overrides = %#v, want %#v", restoredInstructions.Overrides, fixture.instructions.Overrides)
	}
	if restoredInstructions.Options != currentInstructions.Options || restoredInstructions.TemplateVersion != currentInstructions.TemplateVersion ||
		!reflect.DeepEqual(restoredInstructions.QuestionnaireAnswers, currentInstructions.QuestionnaireAnswers) {
		t.Fatalf("restored instructions changed current settings = %#v", restoredInstructions)
	}
	wantInput, wantTemplate, err := fixture.builder.Fingerprints(
		ctx,
		fixture.currentRelease,
		fixture.projections,
		fixture.currentMedia,
		fixture.currentPrivate,
		restoredInstructions,
	)
	if err != nil {
		t.Fatalf("current fingerprints: %v", err)
	}
	if restored.InputFingerprint != wantInput || restored.TemplateFingerprint != wantTemplate {
		t.Fatalf("restored fingerprints input=%s template=%s, want input=%s template=%s",
			restored.InputFingerprint, restored.TemplateFingerprint, wantInput, wantTemplate)
	}
	if restored.InputFingerprint == fixture.snapshot.InputFingerprint {
		t.Fatal("restored description retained prior workflow fingerprint")
	}
}

func TestWorkflowDescriptionReuseRejectsChangedDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*workflowDescriptionReuseFixture)
	}{
		{
			name: "facts",
			mutate: func(fixture *workflowDescriptionReuseFixture) {
				fixture.resolver.subject.EffectiveMetadata.Title = "Changed title"
			},
		},
		{
			name: "template",
			mutate: func(fixture *workflowDescriptionReuseFixture) {
				fixture.resolver.subject.DescriptionTemplate = "Changed template"
			},
		},
		{
			name: "config",
			mutate: func(fixture *workflowDescriptionReuseFixture) {
				fixture.builder.config.Description.CustomSignature = "Changed signature"
			},
		},
		{
			name: "image bytes",
			mutate: func(fixture *workflowDescriptionReuseFixture) {
				if err := os.WriteFile(fixture.currentScreen, []byte("changed screenshot"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "image order",
			mutate: func(fixture *workflowDescriptionReuseFixture) {
				fixture.currentMedia.Artifacts[0].Order, fixture.currentMedia.Artifacts[1].Order =
					fixture.currentMedia.Artifacts[1].Order, fixture.currentMedia.Artifacts[0].Order
			},
		},
		{
			name: "hosted processing evidence",
			mutate: func(fixture *workflowDescriptionReuseFixture) {
				link := fixture.currentPrivate.HostedImages["host-screen-one"]
				link.Tonemapping = api.ScreenshotTonemapped
				fixture.currentPrivate.HostedImages["host-screen-one"] = link
			},
		},
		{
			name: "hosted account binding",
			mutate: func(fixture *workflowDescriptionReuseFixture) {
				link := fixture.currentPrivate.HostedImages["host-screen-one"]
				link.AccountScope = "different-account"
				fixture.currentPrivate.HostedImages["host-screen-one"] = link
			},
		},
		{
			name: "hosted URL",
			mutate: func(fixture *workflowDescriptionReuseFixture) {
				fixture.currentPrivate.HostedImages["host-screen-one"] = api.UploadedImageLink{
					ImagePath:    fixture.currentPrivate.Screenshots[0].Path,
					Host:         "images.example",
					UsageScope:   "global",
					AccountScope: "account-one",
					RawURL:       "https://images.example/changed-screen-one.png",
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowDescriptionReuseFixture(t)
			ctx := t.Context()
			if err := fixture.recordReusableDescriptions(t, fixture.oldRelease, fixture.oldMedia, fixture.oldPrivate, fixture.snapshot); err != nil {
				t.Fatalf("record reusable descriptions: %v", err)
			}
			fixture.resolver.subject = reusableDescriptionSubjectForFixture(fixture.currentRelease, 2)
			test.mutate(&fixture)
			instructions := fixture.instructions
			instructions.Overrides = nil
			restored, _, err := fixture.builder.RestoreCompatibleDescriptions(
				ctx,
				fixture.currentRelease,
				fixture.projections,
				fixture.currentMedia,
				fixture.currentPrivate,
				instructions,
			)
			if err != nil {
				t.Fatalf("restore reusable descriptions: %v", err)
			}
			if len(restored.Descriptions) != 0 {
				t.Fatalf("restored changed dependency = %#v", restored)
			}
			if fixture.service.builds != 0 {
				t.Fatalf("cache rejection built descriptions = %d", fixture.service.builds)
			}
		})
	}
}

func TestWorkflowDescriptionReuseSkipsIncompleteSnapshotsAndFreshOverrides(t *testing.T) {
	t.Parallel()

	fixture := newWorkflowDescriptionReuseFixture(t)
	ctx := t.Context()
	incomplete := fixture.snapshot
	incomplete.Failures = []api.WorkflowFailure{{}}
	if err := fixture.recordReusableDescriptions(t, fixture.oldRelease, fixture.oldMedia, fixture.oldPrivate, incomplete); err != nil {
		t.Fatalf("record incomplete reusable descriptions: %v", err)
	}
	if fixture.repository.saves != 0 {
		t.Fatalf("incomplete snapshot saved %d reusable records", fixture.repository.saves)
	}

	if err := fixture.recordReusableDescriptions(t, fixture.oldRelease, fixture.oldMedia, fixture.oldPrivate, fixture.snapshot); err != nil {
		t.Fatalf("record reusable descriptions: %v", err)
	}
	fixture.resolver.subject = reusableDescriptionSubjectForFixture(fixture.currentRelease, 2)
	fresh := fixture.instructions
	fresh.Overrides = []api.DescriptionOverrideInput{{GroupKey: "alpha", Source: "fresh edit"}}
	restored, returnedInstructions, err := fixture.builder.RestoreCompatibleDescriptions(
		ctx,
		fixture.currentRelease,
		fixture.projections,
		fixture.currentMedia,
		fixture.currentPrivate,
		fresh,
	)
	if err != nil {
		t.Fatalf("restore with fresh override: %v", err)
	}
	if len(restored.Descriptions) != 0 || !reflect.DeepEqual(returnedInstructions.Overrides, fresh.Overrides) {
		t.Fatalf("fresh override cache result descriptions=%#v instructions=%#v", restored, returnedInstructions)
	}

	fixture.resolver.subject.MediaBinding.CompatibilityKey = ""
	if err := fixture.recordReusableDescriptions(t, fixture.currentRelease, fixture.currentMedia, fixture.currentPrivate, fixture.snapshot); err != nil {
		t.Fatalf("record legacy binding: %v", err)
	}
	if fixture.repository.saves != 1 {
		t.Fatalf("legacy binding unexpectedly saved reusable record count=%d", fixture.repository.saves)
	}
}

func newWorkflowDescriptionReuseFixture(t *testing.T) workflowDescriptionReuseFixture {
	t.Helper()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Example.Release.2026.mkv")
	compatibilityKey := api.MediaCompatibilityKey(strings.Repeat("a", 64))
	oldRelease := api.ReleaseRef{SourcePath: sourcePath, Generation: 1}
	currentRelease := api.ReleaseRef{SourcePath: sourcePath, Generation: 2}
	oldPrivate := reusableDescriptionPrivateMedia(t, root, "old")
	currentPrivate := reusableDescriptionPrivateMedia(t, root, "current")
	oldMedia := reusableDescriptionMediaSet("old", 1)
	currentMedia := reusableDescriptionMediaSet("current", 2)
	resolver := &workflowDescriptionReuseResolverFake{
		subject: reusableDescriptionSubjectForFixture(oldRelease, 1),
	}
	resolver.subject.MediaBinding.CompatibilityKey = compatibilityKey
	repository := &workflowDescriptionReuseRepositoryFake{}
	service := &workflowDescriptionServiceFake{}
	builder := workflowDescriptionBuilder{
		config: config.Config{Description: config.DescriptionSettingsConfig{
			CustomSignature: "Signature",
			ThumbnailSize:   350,
		}},
		resolver: resolver,
		trackers: service,
		reuse:    repository,
	}
	instructions := api.DescriptionInstructions{
		Overrides: []api.DescriptionOverrideInput{{GroupKey: "alpha", Source: "saved edit"}},
		QuestionnaireAnswers: map[api.TrackerID]map[string]string{
			"ALPHA": {"edition": "theatrical"},
		},
		Options:         api.UploadOptions{Screens: 2, InteractionMode: api.InteractionModeInteractive},
		TemplateVersion: "workflow-v1",
	}
	snapshot := api.DescriptionSet{
		InputFingerprint:    workflowTestFingerprint(t, "old-description-input"),
		TemplateFingerprint: workflowTestFingerprint(t, "old-description-template"),
		Descriptions: []api.RenderedDescription{
			{
				GroupKey:           "beta",
				TrackerIDs:         []api.TrackerID{"BETA"},
				Source:             "saved beta source",
				Rendered:           "<p>saved beta source</p>",
				ContentFingerprint: workflowTestFingerprint(t, "beta-description"),
			},
			{
				GroupKey:           "alpha",
				TrackerIDs:         []api.TrackerID{"ALPHA"},
				Source:             "saved alpha source",
				Rendered:           "<p>saved alpha source</p>",
				ContentFingerprint: workflowTestFingerprint(t, "alpha-description"),
			},
		},
		TrackerResults: []api.DescriptionTrackerResult{
			{TrackerID: "BETA", Status: api.StageStatusCompleted},
			{TrackerID: "ALPHA", Status: api.StageStatusCompleted},
		},
		Status: api.StageStatusCompleted,
	}
	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
		{
			TrackerID:        "ALPHA",
			DisplayName:      "Alpha",
			DescriptionGroup: "alpha",
			Artifacts:        api.TrackerArtifactRequirements{Description: true},
			NamingPolicyID:   "alpha-v1",
		},
		{
			TrackerID:        "BETA",
			DisplayName:      "Beta",
			DescriptionGroup: "beta",
			Artifacts:        api.TrackerArtifactRequirements{Description: true},
			NamingPolicyID:   "beta-v1",
		},
	}}
	return workflowDescriptionReuseFixture{
		builder:        builder,
		repository:     repository,
		resolver:       resolver,
		service:        service,
		oldRelease:     oldRelease,
		currentRelease: currentRelease,
		projections:    projections,
		oldMedia:       oldMedia,
		currentMedia:   currentMedia,
		oldPrivate:     oldPrivate,
		currentPrivate: currentPrivate,
		instructions:   instructions,
		snapshot:       snapshot,
		currentScreen:  currentPrivate.Screenshots[0].Path,
	}
}

func reusableDescriptionSubjectForFixture(release api.ReleaseRef, generation api.PreparedGeneration) api.UploadSubject {
	return api.UploadSubject{
		SourcePath:          release.SourcePath,
		DescriptionTemplate: "Template v1",
		MediaBinding: api.PreparedMediaBinding{
			SourcePath:               release.SourcePath,
			PreparedMediaFingerprint: "prepared-media-" + string(rune('0'+generation)),
			PreparedGeneration:       generation,
			CompatibilityKey:         api.MediaCompatibilityKey(strings.Repeat("a", 64)),
		},
		EffectiveMetadata: api.EffectiveMetadata{Title: "Example Title", Year: 2026},
		Identity: api.ExternalIdentity{
			SourcePath: release.SourcePath,
			Generation: generation,
			TMDBID:     42,
			ResolvedAt: time.Date(2026, time.January, int(generation), 0, 0, 0, 0, time.UTC),
			Resolution: api.IdentityResolutionKey{SourceFingerprint: "old-generation"},
			Dependencies: api.IdentityDependencySet{
				TMDB: api.IdentityDependency{ID: 42, IMDBID: 7},
			},
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: release.SourcePath,
			Generation: generation,
			UpdatedAt:  time.Date(2026, time.January, int(generation), 1, 0, 0, 0, time.UTC),
		},
		TrackerData: []api.TrackerMetadata{{
			Tracker:   "ALPHA",
			TMDBID:    42,
			UpdatedAt: time.Date(2026, time.January, int(generation), 2, 0, 0, 0, time.UTC),
		}},
	}
}

func reusableDescriptionMediaSet(prefix string, revision api.WorkflowRevision) api.MediaArtifactSet {
	return api.MediaArtifactSet{
		ID:       api.MediaArtifactSetID(prefix + "-media"),
		Revision: revision,
		Artifacts: []api.MediaArtifact{
			{
				ID:       "screen-one",
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
				Order:    0,
			},
			{
				ID:       "screen-two",
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
				Order:    1,
			},
			{
				ID:       "menu-one",
				Kind:     api.MediaArtifactDVDMenu,
				Purpose:  api.ScreenshotPurposeMenu,
				Selected: true,
				Order:    0,
			},
			{
				ID:       "host-screen-one",
				Kind:     api.MediaArtifactHostedImage,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
				Source:   "screen-one",
				Order:    0,
			},
			{
				ID:       "host-screen-two",
				Kind:     api.MediaArtifactHostedImage,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
				Source:   "screen-two",
				Order:    1,
			},
			{
				ID:       "host-menu-one",
				Kind:     api.MediaArtifactHostedImage,
				Purpose:  api.ScreenshotPurposeMenu,
				Selected: true,
				Source:   "menu-one",
				Order:    0,
			},
		},
	}
}

func reusableDescriptionPrivateMedia(t *testing.T, root, prefix string) workflowMediaPrivateArtifacts {
	t.Helper()
	write := func(name, contents string) string {
		t.Helper()
		pathValue := filepath.Join(root, prefix+"-"+name)
		if err := os.WriteFile(pathValue, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		return pathValue
	}
	screenOne := write("screen-one.png", "screen one")
	screenTwo := write("screen-two.png", "screen two")
	menu := write("menu-one.png", "menu one")
	return workflowMediaPrivateArtifacts{
		Screenshots: []api.ScreenshotImage{
			{
				Path:             screenOne,
				Purpose:          api.ScreenshotPurposeFinal,
				Index:            1,
				TimestampSeconds: 1.5,
				Width:            1920,
				Height:           1080,
			},
			{
				Path:             screenTwo,
				Purpose:          api.ScreenshotPurposeFinal,
				Index:            2,
				TimestampSeconds: 3.5,
				Width:            1920,
				Height:           1080,
			},
		},
		DVDMenus: []api.DVDMenuCaptureImage{{
			Path:    menu,
			Purpose: api.ScreenshotPurposeMenu,
			Index:   1,
			Width:   720,
			Height:  480,
		}},
		HostedImages: map[api.PublicResourceID]api.UploadedImageLink{
			"host-screen-one": {
				ImagePath:    screenOne,
				Host:         "images.example",
				UsageScope:   "global",
				AccountScope: "account-one",
				RawURL:       "https://images.example/screen-one.png",
				UploadedAt:   time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
			},
			"host-screen-two": {
				ImagePath:    screenTwo,
				Host:         "images.example",
				UsageScope:   "global",
				AccountScope: "account-one",
				RawURL:       "https://images.example/screen-two.png",
				UploadedAt:   time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
			},
			"host-menu-one": {
				ImagePath:    menu,
				Host:         "images.example",
				UsageScope:   "global",
				AccountScope: "account-one",
				RawURL:       "https://images.example/menu-one.png",
				UploadedAt:   time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
			},
		},
		HostedSources: map[api.PublicResourceID]api.PublicResourceID{
			"host-screen-one": "screen-one",
			"host-screen-two": "screen-two",
			"host-menu-one":   "menu-one",
		},
	}
}

func TestWorkflowDescriptionReuseRefreshesLegacyRendererAndKeepsFinalEdits(t *testing.T) {
	for _, renderer := range []string{"", "description-renderer-v1"} {
		for _, mode := range []string{"generated", "mixed", "final"} {
			t.Run(renderer+"/"+mode, func(t *testing.T) {
				f := newWorkflowDescriptionReuseFixture(t)
				for i, name := range []api.TrackerID{"AITHER", "STC"} {
					f.projections.Projections[i].TrackerID = name
					f.projections.Projections[i].DescriptionGroup = "unit3d"
					f.snapshot.Descriptions[i].TrackerIDs = []api.TrackerID{name}
					f.snapshot.Descriptions[i].GroupKey = "unit3d|" + strings.ToLower(string(name))
					f.snapshot.TrackerResults[i].TrackerID = name
				}
				f.instructions.Overrides = nil
				if mode != "generated" {
					f.snapshot.Descriptions[0].Source = "Final AITHER text"
					f.snapshot.Descriptions[0].Rendered = "<p>Final AITHER text</p>"
					f.instructions.Overrides = append(f.instructions.Overrides, api.DescriptionOverrideInput{
						GroupKey:   "unit3d|aither",
						TrackerIDs: []api.TrackerID{"AITHER"},
						Source:     "Final AITHER text",
						Final:      true,
					})
				}
				if mode == "final" {
					f.snapshot.Descriptions[1].Source = "Final STC text"
					f.snapshot.Descriptions[1].Rendered = "<p>Final STC text</p>"
					f.instructions.Overrides = append(f.instructions.Overrides, api.DescriptionOverrideInput{
						GroupKey:   "unit3d|stc",
						TrackerIDs: []api.TrackerID{"STC"},
						Source:     "Final STC text",
						Final:      true,
					})
				}
				if err := f.recordReusableDescriptions(t, f.oldRelease, f.oldMedia, f.oldPrivate, f.snapshot); err != nil {
					t.Fatal(err)
				}
				// Older output must rebuild without discarding final edits.
				f.repository.value.RendererVersion = renderer
				f.resolver.subject = reusableDescriptionSubjectForFixture(f.currentRelease, 2)
				f.resolver.useInputGroups = true
				f.builder.trackers = trackers.NewServiceWithRegistry(f.builder.config, api.NopLogger{}, nil, trackerimpl.MustNewRegistry())
				current := f.instructions
				current.Overrides = nil
				got, restored, err := f.builder.RestoreCompatibleDescriptions(t.Context(), f.currentRelease, f.projections, f.currentMedia, f.currentPrivate, current)
				if err != nil || got.Status != api.StageStatusCompleted || !reflect.DeepEqual(restored.Overrides, f.instructions.Overrides) {
					t.Fatalf("refresh=%+v instructions=%+v err=%v", got, restored, err)
				}
				for _, output := range got.Descriptions {
					for _, tracker := range output.TrackerIDs {
						final := mode == "final" || mode == "mixed" && tracker == "AITHER"
						if final && output.Source != "Final "+string(tracker)+" text" {
							t.Fatalf("final edit changed: %+v", output)
						}
						if !final && (!strings.Contains(output.Source, "Template v1") || strings.Contains(output.Source, "saved beta source") || strings.Contains(output.Source, "saved alpha source")) {
							t.Fatalf("generated text not refreshed: %+v", output)
						}
					}
				}
				record, err := f.builder.PrepareReusableDescriptions(t.Context(), f.currentRelease, f.projections, f.currentMedia, f.currentPrivate, restored, got)
				if err != nil || record == nil || record.Description.RendererVersion == "" {
					t.Fatalf("refreshed record=%+v err=%v", record, err)
				}
			})
		}
	}
}

func TestWorkflowDescriptionFingerprintsInvalidateLegacyRenderer(t *testing.T) {
	f := newWorkflowDescriptionReuseFixture(t)
	_, current, err := f.builder.Fingerprints(t.Context(), f.oldRelease, f.projections, f.oldMedia, f.oldPrivate, f.instructions)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := api.CanonicalWorkflowFingerprint(struct {
		Template string
		Version  string
	}{f.resolver.subject.DescriptionTemplate, strings.TrimSpace(f.instructions.TemplateVersion)})
	if err != nil {
		t.Fatal(err)
	}
	if current == legacy {
		t.Fatal("active snapshot from old renderer remains compatible")
	}
}

func TestWorkflowDescriptionReusePreservesEditsWhenUnknownImageDigestIsRecorded(t *testing.T) {
	for _, mode := range []string{"local", "hosted", "both"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newWorkflowDescriptionReuseFixture(t)
			fixture.instructions.Overrides[0].Final = true
			if err := fixture.recordReusableDescriptions(t, fixture.oldRelease, fixture.oldMedia, fixture.oldPrivate, fixture.snapshot); err != nil {
				t.Fatal(err)
			}
			// Reusable-media persistence records identity without inventing processing.
			for index := range fixture.currentPrivate.Screenshots {
				image := &fixture.currentPrivate.Screenshots[index]
				digest, err := workflowMediaContentSHA256(t.Context(), image.Path)
				if err != nil {
					t.Fatal(err)
				}
				if mode != "hosted" {
					image.ContentSHA256 = digest
				}
				if image.Tonemapping != "" {
					t.Fatal("legacy fixture must remain unknown")
				}
			}
			if mode != "hosted" {
				for index := range fixture.currentPrivate.DVDMenus {
					menu := &fixture.currentPrivate.DVDMenus[index]
					digest, err := workflowMediaContentSHA256(t.Context(), menu.Path)
					if err != nil {
						t.Fatal(err)
					}
					menu.ContentSHA256 = digest
				}
			}
			if mode != "local" {
				for id, link := range fixture.currentPrivate.HostedImages {
					digest, err := workflowMediaContentSHA256(t.Context(), link.ImagePath)
					if err != nil {
						t.Fatal(err)
					}
					link.ContentSHA256 = digest
					fixture.currentPrivate.HostedImages[id] = link
				}
			}
			fixture.resolver.subject = reusableDescriptionSubjectForFixture(fixture.currentRelease, 2)
			current := fixture.instructions
			current.Overrides = nil
			restored, instructions, err := fixture.builder.RestoreCompatibleDescriptions(t.Context(), fixture.currentRelease,
				fixture.projections, fixture.currentMedia, fixture.currentPrivate, current)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(restored.Descriptions, fixture.snapshot.Descriptions) ||
				!reflect.DeepEqual(instructions.Overrides, fixture.instructions.Overrides) || fixture.service.builds != 0 {
				t.Fatalf("unchanged image bytes lost saved edits: descriptions=%+v overrides=%+v builds=%d", restored.Descriptions, instructions.Overrides, fixture.service.builds)
			}
		})
	}
}

func TestUnknownHostedProvenanceKeepsLegacyFingerprintShape(t *testing.T) {
	link := api.UploadedImageLink{
		ImagePath:    "image.png",
		Host:         "images.example",
		UsageScope:   "global",
		AccountScope: "account-one",
		RawURL:       "https://images.example/image.png",
	}
	normalized, err := normalizeReusableDescriptionHostedImages([]api.UploadedImageLink{link}, map[string]string{"image.png": "digest"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	// Exact normalized JSON produced before screenshot provenance was introduced.
	const legacy = `[{"ImageSHA256":"digest","Host":"images.example","UsageScope":"global","AccountScope":"account-one","ImgURL":"","RawURL":"https://images.example/image.png","WebURL":"","SizeBytes":0}]`
	if string(payload) != legacy {
		t.Fatalf("unknown evidence changed legacy compatibility: %s", payload)
	}
	link.Tonemapping = api.ScreenshotNotTonemapped
	normalized, err = normalizeReusableDescriptionHostedImages([]api.UploadedImageLink{link}, map[string]string{"image.png": "digest"})
	if err != nil {
		t.Fatal(err)
	}
	known, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if string(known) == legacy {
		t.Fatal("known processing status must change compatibility")
	}
}
