// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type descriptionCollectionHostFake struct {
	descriptionAudioHostFake
	inputs []api.ImageCollectionInput
	skips  []bool
}

func (f *descriptionCollectionHostFake) UploadCollection(_ context.Context, _ api.ImageHostingSubject, input api.ImageCollectionInput, skip bool) (*api.ImageCollection, error) {
	f.inputs = append(f.inputs, input)
	f.skips = append(f.skips, skip)
	if skip {
		return nil, nil
	}
	record := &api.ImageCollection{
		Fingerprint:        strconv.Itoa(len(f.inputs)),
		ContentFingerprint: "content",
		Host:               "slowpics",
		URL:                "https://slow.pics/c/synthetic",
		Purpose:            input.Purpose,
		Category:           input.Category,
		Label:              input.Label,
		AccountScope:       "anonymous",
		Completed:          true,
	}
	for index := range input.Images {
		record.Members = append(record.Members, api.ImageCollectionMember{Identity: strconv.Itoa(index), Label: input.Labels[index]})
	}
	return record, nil
}

func collectionDescriptionBuilder(t *testing.T) (workflowDescriptionBuilder, *descriptionCollectionHostFake) {
	t.Helper()
	registry := trackers.NewRegistry()
	for _, name := range []string{"OPEN", "RESTRICTED"} {
		descriptor := trackers.Descriptor{
			Name:              name,
			Definition:        mediaImageHostDefinition(name),
			Family:            trackers.FamilyStandalone,
			BaseURL:           "https://synthetic.example.invalid",
			UploadContentMode: trackers.UploadContentModeScreenshots,
		}
		if name == "RESTRICTED" {
			descriptor.ImageHost = &trackers.ImageHostPolicy{AllowedHosts: []string{"imgbb"}}
		}
		if err := registry.RegisterDescriptor(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{ImageHosting: config.ImageHostingConfig{AnalysisHost: "slowpics", Host1: "imgbb"}}
	host := &descriptionCollectionHostFake{}
	return workflowDescriptionBuilder{config: cfg, media: &mediaModule{
		cfg:      cfg,
		registry: registry,
		images:   host,
		logger:   api.NopLogger{},
	}}, host
}

func TestComparisonCollectionsKeepWholeSetsAndLegacyRoute(t *testing.T) {
	builder, host := collectionDescriptionBuilder(t)
	root := t.TempDir()
	groupImage := func(name string) api.ScreenshotImage {
		return api.ScreenshotImage{
			Path:                  filepath.Join(root, name),
			Purpose:               api.ScreenshotPurposeFinal,
			ComparisonGroup:       "group-1",
			ComparisonCount:       2,
			ComparisonLabel:       "Comparison set",
			ComparisonMemberLabel: name,
		}
	}
	first, second := groupImage("A.png"), groupImage("B.png")
	legacy := api.ScreenshotImage{Path: filepath.Join(root, "legacy.png"), Purpose: api.ScreenshotPurposeFinal}
	subject := api.UploadSubject{ExactMedia: &api.ExactMediaAssets{CollectionHost: "slowpics", Screenshots: []api.ScreenshotImage{first, second, legacy}}}
	if err := builder.uploadComparisonCollections(t.Context(), subject, []string{"OPEN"}); err != nil {
		t.Fatal(err)
	}
	if len(host.inputs) != 1 || len(host.inputs[0].Images) != 2 || host.inputs[0].Images[0].Path != first.Path || len(subject.ExactMedia.Collections) != 1 || len(subject.ExactMedia.Screenshots) != 1 || subject.ExactMedia.Screenshots[0].Path != legacy.Path {
		t.Fatalf("collection route = %#v %#v", host.inputs, subject.ExactMedia)
	}
	subject.ExactMedia = &api.ExactMediaAssets{CollectionHost: "slowpics", Screenshots: []api.ScreenshotImage{first}}
	if err := builder.uploadComparisonCollections(t.Context(), subject, []string{"OPEN"}); err == nil || len(host.inputs) != 1 {
		t.Fatal("partial group was uploaded")
	}
	subject.ExactMedia = &api.ExactMediaAssets{CollectionHost: "slowpics", Screenshots: []api.ScreenshotImage{first, second}}
	if err := builder.uploadComparisonCollections(t.Context(), subject, []string{"RESTRICTED"}); err != nil || len(host.inputs) != 2 || len(subject.ExactMedia.Collections) != 1 {
		t.Fatalf("restricted tracker collection = %#v, %v", host.inputs, err)
	}
}

func TestCollectionSkipPreservesStatsAndNeverOrdinaryUploads(t *testing.T) {
	builder, host := collectionDescriptionBuilder(t)
	image := api.ScreenshotImage{Path: filepath.Join(t.TempDir(), "waveform.png"), Purpose: api.ScreenshotPurposeAudioAnalysis}
	subject := api.UploadSubject{ExactMedia: &api.ExactMediaAssets{
		CollectionHost:         "slowpics",
		ImageHostUploadSkipped: true,
		AudioAnalysis:          &api.AudioAnalysisRef{ID: "audio", Revision: 1},
		AudioTracks: []api.AudioDescriptionTrack{{
			Ordinal: 1,
			Images:  []api.ScreenshotImage{image},
			Stats:   "Synthetic statistics",
		}},
	}}
	if err := builder.uploadAnalysisDescriptionImages(t.Context(), subject, []string{"OPEN"}); err != nil {
		t.Fatal(err)
	}
	if len(host.inputs) != 1 || !host.skips[0] || len(host.images) != 0 || len(subject.ExactMedia.Collections) != 0 || subject.ExactMedia.AudioTracks[0].Stats != "Synthetic statistics" {
		t.Fatal("skip or statistics changed")
	}
	image.Purpose = api.ScreenshotPurposeFinal
	image.ComparisonGroup = "comparison"
	image.ComparisonCount = 1
	result, err := builder.media.uploadImagesToTargetsWithFallback(t.Context(), api.UploadSubject{}, "", nil, []trackers.ImageUploadTarget{{Host: "imgbb", Trackers: []string{"OPEN"}}}, []api.ScreenshotImage{image}, nil, nil)
	if err != nil || len(result.Links) != 0 || len(host.images) != 0 {
		t.Fatalf("collection image escaped ordinary fallback: %#v %v", result, err)
	}
	if _, err := builder.media.uploadImagesToTarget(t.Context(), api.UploadSubject{}, trackers.ImageUploadTarget{Host: "slowpics"}, []api.ScreenshotImage{image}, nil, nil, false); err == nil {
		t.Fatal("ordinary target accepted slowpics")
	}
}

func TestCollectionDescriptionOutcomeRequiresExactConfirmation(t *testing.T) {
	cause := api.NewOperationError(api.OperationFailure{
		Code:      api.OperationFailureUnknownOutcome,
		Operation: api.OperationKindImageHosting,
		Recovery:  api.OperationRecoveryConfirm,
		Message:   "Uncertain collection",
	}, nil)
	outcome := &api.ImageCollectionOutcomeError{AttemptID: "exact-attempt", Cause: cause}
	descriptions, err := collectionDescriptionOutcome(outcome, "input", "template")
	if err != nil || descriptions.Status != api.StageStatusBlocked || len(descriptions.RequiredActions) != 1 {
		t.Fatalf("blocked description = %#v, %v", descriptions, err)
	}
	action := descriptions.RequiredActions[0]
	if action.Kind != api.RequiredActionReconcileSubmission || action.EffectKind != api.WorkflowExternalEffectImageHosting || action.EffectScopeID != api.ImageCollectionEffectScope("exact-attempt") || len(action.Options) != 1 || action.Options[0].Value != api.RequiredActionReconcileNotCompleted || action.Options[0].Label != "I verified no collection was created" {
		t.Fatalf("confirmation = %#v", action)
	}
	if descriptions.InputFingerprint != "input" || descriptions.TemplateFingerprint != "template" || len(descriptions.Failures) != 1 || descriptions.Failures[0].Resource != action.EffectScopeID {
		t.Fatalf("lost description authority: %#v", descriptions)
	}
	_, err = collectionDescriptionOutcome(cause, "input", "template")
	if !errors.Is(err, cause) {
		t.Fatalf("unscoped error became actionable: %v", err)
	}
}

func TestDescriptionCollectionsIgnoreScreenshotHostPolicy(t *testing.T) {
	for _, purpose := range []api.ScreenshotPurpose{api.ScreenshotPurposeComparison, api.ScreenshotPurposeHDRAnalysis, api.ScreenshotPurposeAudioAnalysis} {
		for _, failed := range []bool{false, true} {
			t.Run(string(purpose)+"/failed="+strconv.FormatBool(failed), func(t *testing.T) {
				builder, host := collectionDescriptionBuilder(t)
				image := api.ScreenshotImage{Path: filepath.Join(t.TempDir(), "plot.png"), Purpose: purpose}
				subject := api.UploadSubject{ExactMedia: &api.ExactMediaAssets{CollectionHost: "slowpics"}}
				if failed {
					subject.ImageHostOverrides.FailedHosts = []string{"slowpics", "imgbb"}
				}
				var err error
				switch purpose {
				case api.ScreenshotPurposeComparison:
					image.Purpose = api.ScreenshotPurposeFinal
					image.ComparisonGroup, image.ComparisonCount = "comparison", 1
					image.ComparisonLabel, image.ComparisonMemberLabel = "Comparison", "Source"
					subject.ExactMedia.Screenshots = []api.ScreenshotImage{image}
					err = builder.uploadComparisonCollections(t.Context(), subject, []string{"OPEN", "RESTRICTED"})
				case api.ScreenshotPurposeHDRAnalysis:
					subject.ExactMedia.HDRPlots = []api.HDRDescriptionPlot{{TargetID: "source", Image: image}}
					err = builder.uploadAnalysisDescriptionImages(t.Context(), subject, []string{"OPEN", "RESTRICTED"})
				case api.ScreenshotPurposeAudioAnalysis:
					subject.ExactMedia.AudioTracks = []api.AudioDescriptionTrack{{Ordinal: 1, Images: []api.ScreenshotImage{image}}}
					err = builder.uploadAnalysisDescriptionImages(t.Context(), subject, []string{"OPEN", "RESTRICTED"})
				case api.ScreenshotPurposePreview, api.ScreenshotPurposeFinal, api.ScreenshotPurposeMenu:
					t.Fatalf("unexpected purpose %q", purpose)
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(host.inputs) != 1 || len(host.images) != 0 || len(subject.ExactMedia.Collections) != 1 || subject.ExactMedia.Collections[0].URL != "https://slow.pics/c/synthetic" {
					t.Fatalf("description collection = %#v, ordinary uploads = %#v, media = %#v", host.inputs, host.images, subject.ExactMedia)
				}
				for _, test := range []struct {
					host string
					want bool
				}{{"imgbb", !failed}, {"imgbox", false}, {"slowpics", false}} {
					allowed, policyErr := trackers.ReusableImageHostAllowedWithRegistry(builder.media.registry, builder.config, "RESTRICTED", test.host, subject.ImageHostOverrides)
					if policyErr != nil || allowed != test.want {
						t.Fatalf("ordinary host %s allowed = %t, err = %v, want %t", test.host, allowed, policyErr, test.want)
					}
				}
			})
		}
	}
}

func TestAnalysisImagesWithoutCollectionsKeepHostRestrictions(t *testing.T) {
	for _, audio := range []bool{false, true} {
		t.Run("audio="+strconv.FormatBool(audio), func(t *testing.T) {
			builder, host := collectionDescriptionBuilder(t)
			image := api.ScreenshotImage{Path: filepath.Join(t.TempDir(), "plot.png"), Purpose: api.ScreenshotPurposeHDRAnalysis}
			subject := api.UploadSubject{
				ImageHostOverrides: api.ImageHostOverrides{FailedHosts: []string{"imgbb"}},
				ExactMedia:         &api.ExactMediaAssets{},
			}
			var err error
			if audio {
				image.Purpose = api.ScreenshotPurposeAudioAnalysis
				subject.ExactMedia.AudioTracks = []api.AudioDescriptionTrack{{Ordinal: 1, Images: []api.ScreenshotImage{image}}}
				err = builder.uploadAudioDescriptionImages(t.Context(), subject, []string{"RESTRICTED"})
			} else {
				subject.ExactMedia.HDRPlots = []api.HDRDescriptionPlot{{TargetID: "source", Image: image}}
				err = builder.uploadHDRDescriptionImages(t.Context(), subject, []string{"RESTRICTED"})
			}
			if err == nil || len(host.inputs) != 0 || len(host.images) != 0 {
				t.Fatalf("restricted ordinary route: err = %v, collections = %#v, images = %#v", err, host.inputs, host.images)
			}
		})
	}
}

func TestAnalysisCollectionsCombineAllTargetsAndTracks(t *testing.T) {
	t.Parallel()
	builder, host := collectionDescriptionBuilder(t)
	root := t.TempDir()
	exact := &api.ExactMediaAssets{
		CollectionHost: "slowpics",
		HDRAnalysis:    &api.HDRAnalysisRef{ID: "hdr", Revision: 2},
		AudioAnalysis:  &api.AudioAnalysisRef{ID: "audio", Revision: 3},
	}
	for index, kind := range []api.HDRArtifactKind{api.HDRArtifactGradePlot, api.HDRArtifactSceneContactSheet} {
		exact.HDRPlots = append(exact.HDRPlots, api.HDRDescriptionPlot{
			TargetID: "target-" + strconv.Itoa(index),
			Kind:     kind,
			Label:    "HDR " + strconv.Itoa(index),
			Image:    api.ScreenshotImage{Path: filepath.Join(root, "hdr-"+strconv.Itoa(index)+".png"), Purpose: api.ScreenshotPurposeHDRAnalysis},
		})
	}
	for ordinal := 1; ordinal <= 2; ordinal++ {
		exact.AudioTracks = append(exact.AudioTracks, api.AudioDescriptionTrack{
			Ordinal: ordinal,
			Stats:   "Synthetic amplitude statistics",
			Images:  []api.ScreenshotImage{{Path: filepath.Join(root, "audio-"+strconv.Itoa(ordinal)+".png"), Purpose: api.ScreenshotPurposeAudioAnalysis}},
		})
	}
	subject := api.UploadSubject{ExactMedia: exact}
	if err := builder.uploadAnalysisDescriptionImages(t.Context(), subject, []string{"OPEN", "RESTRICTED"}); err != nil {
		t.Fatal(err)
	}
	if len(host.inputs) != 1 || len(exact.Collections) != 1 || len(host.images) != 0 {
		t.Fatalf("collections=%+v ordinary=%+v", host.inputs, host.images)
	}
	set := host.inputs[0]
	if set.Category != api.ImageCollectionCategoryAnalysis || set.Label != api.ImageCollectionCategoryAnalysis || set.Columns != 1 || len(set.Images) != 4 ||
		set.Images[0].Path != exact.HDRPlots[0].Image.Path || set.Images[2].Path != exact.AudioTracks[0].Images[0].Path ||
		set.Images[2].Purpose != api.ScreenshotPurposeAudioAnalysis || !reflect.DeepEqual(set.Labels, []string{"HDR 0", "HDR 1", "Audio track 1 plot 1", "Audio track 2 plot 1"}) {
		t.Fatalf("combined set=%+v", set)
	}
	if exact.AudioTracks[1].Stats != "Synthetic amplitude statistics" {
		t.Fatal("statistics lost")
	}
	exact.AudioAnalysis.Revision++
	if err := builder.uploadAnalysisDescriptionImages(t.Context(), subject, []string{"OPEN"}); err != nil {
		t.Fatal(err)
	}
	if host.inputs[1].Generation == set.Generation {
		t.Fatal("audio revision did not invalidate combined generation")
	}
}

type collectionAudioPaths string

func (p collectionAudioPaths) LocalArtifactPath(_ api.AudioAnalysisResult, id api.PublicResourceID) (string, error) {
	return filepath.Join(string(p), string(id)+".png"), nil
}

func TestDescriptionBuildUploadsAnalysisCollectionOnce(t *testing.T) {
	t.Parallel()
	builder, host := collectionDescriptionBuilder(t)
	preview := &workflowDescriptionServiceFake{}
	builder.resolver = &workflowDescriptionResolverFake{}
	builder.trackers = preview
	analysis := api.AudioAnalysisResult{ID: "audio", Revision: 1}
	for ordinal := 1; ordinal <= 2; ordinal++ {
		analysis.Tracks = append(analysis.Tracks, api.AudioAnalysisTrackResult{
			Ordinal: ordinal,
			Artifacts: []api.AudioAnalysisArtifact{{
				ID:      api.PublicResourceID("waveform-" + strconv.Itoa(ordinal)),
				Variant: api.AudioAnalysisWaveform,
				Status:  api.StageStatusCompleted,
				Width:   1200,
				Height:  400,
			}},
		})
	}
	resources := releaseworkflow.DescriptionResources{
		Media:         workflowMediaPrivateArtifacts{},
		AudioAnalysis: analysis,
		AudioPaths:    collectionAudioPaths(t.TempDir()),
	}
	release := api.ReleaseRef{SourcePath: filepath.Join(t.TempDir(), "Synthetic.Release.2026-GRP.mkv"), Generation: 1}
	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "OPEN", Artifacts: api.TrackerArtifactRequirements{Description: true}}}}
	_, err := builder.Build(t.Context(), release, projections, api.MediaArtifactSet{}, resources, api.DescriptionInstructions{}, time.Now())
	if err != nil || len(host.inputs) != 1 || len(host.inputs[0].Images) != 2 || len(host.images) != 0 ||
		preview.subject.ExactMedia == nil || len(preview.subject.ExactMedia.Collections) != 1 || preview.builds != 1 {
		t.Fatalf("build error=%v inputs=%+v ordinary=%+v preview=%+v", err, host.inputs, host.images, preview.subject.ExactMedia)
	}
}

func TestDescriptionBuildUploadsSourceComparisonBeforeAnalysis(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		builder, host := collectionDescriptionBuilder(t)
		root := t.TempDir()
		preview := &workflowDescriptionServiceFake{
			comparisons: []api.DescriptionComparison{{
				BBCode:    "[comparison=Source A,Source B]synthetic images[/comparison]",
				Sources:   []string{"Source A", "Source B"},
				ImageURLs: []string{"https://images.example.test/a.png", "https://images.example.test/b.png"},
				Trackers:  []string{"OPEN"},
			}},
			comparisonInput: api.ImageCollectionInput{
				Generation: "imported-comparison",
				Purpose:    api.ScreenshotPurposeComparison,
				Category:   "Comps",
				Label:      "Source A vs Source B",
				Columns:    2,
				Images:     []api.ScreenshotImage{{Path: filepath.Join(root, "a.png")}, {Path: filepath.Join(root, "b.png")}},
				Labels:     []string{"Source A", "Source B"},
			},
		}
		builder.resolver, builder.trackers = &workflowDescriptionResolverFake{}, preview
		analysis := api.AudioAnalysisResult{
			ID:       "audio",
			Revision: 1,
			Tracks: []api.AudioAnalysisTrackResult{{
				Ordinal: 1,
				Artifacts: []api.AudioAnalysisArtifact{{
					ID:      "waveform",
					Variant: api.AudioAnalysisWaveform,
					Status:  api.StageStatusCompleted,
					Width:   1200,
					Height:  400,
				}},
			}},
		}
		resources := releaseworkflow.DescriptionResources{
			Media:         workflowMediaPrivateArtifacts{},
			AudioAnalysis: analysis,
			AudioPaths:    collectionAudioPaths(root),
		}
		release := api.ReleaseRef{SourcePath: filepath.Join(root, "Synthetic.Release.2026-GRP.mkv"), Generation: 1}
		projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "OPEN", Artifacts: api.TrackerArtifactRequirements{Description: true}}}}
		initial, _, err := builder.Fingerprints(t.Context(), release, projections, api.MediaArtifactSet{}, resources, api.DescriptionInstructions{})
		if err != nil || preview.materializations != 0 || len(host.inputs) != 0 {
			t.Fatalf("fingerprinting caused external work: %v", err)
		}
		preview.comparisons[0].ImageURLs[0] = "https://images.example.test/changed.png"
		changed, _, err := builder.Fingerprints(t.Context(), release, projections, api.MediaArtifactSet{}, resources, api.DescriptionInstructions{})
		if err != nil || changed == initial {
			t.Fatalf("source comparison changes did not invalidate descriptions: %v", err)
		}
		started := time.Now()
		_, err = builder.Build(t.Context(), release, projections, api.MediaArtifactSet{}, resources, api.DescriptionInstructions{}, started)
		if err != nil || len(host.inputs) != 2 || host.inputs[0].Purpose != api.ScreenshotPurposeComparison || host.inputs[0].Columns != 2 ||
			host.inputs[1].Category != api.ImageCollectionCategoryAnalysis || host.inputs[1].Columns != 1 || time.Since(started) != 5*time.Second ||
			preview.subject.ExactMedia.DescriptionComparisons[0].Collection == nil || preview.materializations != 1 {
			t.Fatalf("source comparison build err=%v inputs=%+v wait=%v preview=%+v", err, host.inputs, time.Since(started), preview.subject.ExactMedia)
		}
	})
}

func TestComparisonAnalysisCollectionSpacing(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"both", "no comparison", "no analysis", "skip", "final override", "canceled"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				builder, host := collectionDescriptionBuilder(t)
				exact := &api.ExactMediaAssets{CollectionHost: "slowpics"}
				if name != "no comparison" {
					exact.Screenshots = []api.ScreenshotImage{{
						Path:                  filepath.Join(t.TempDir(), "comparison.png"),
						Purpose:               api.ScreenshotPurposeFinal,
						ComparisonGroup:       "group",
						ComparisonCount:       1,
						ComparisonLabel:       "Comparison",
						ComparisonMemberLabel: "Source",
					}}
				}
				if name != "no analysis" {
					exact.HDRPlots = []api.HDRDescriptionPlot{{Label: "HDR plot", Image: api.ScreenshotImage{Path: "hdr.png", Purpose: api.ScreenshotPurposeHDRAnalysis}}}
				}
				subject := api.UploadSubject{ExactMedia: exact}
				if name == "skip" {
					exact.ImageHostUploadSkipped = true
				}
				if name == "final override" {
					subject.DescriptionGroupsFinal = true
					subject.DescriptionGroups = []api.DescriptionBuilderGroup{{Trackers: []string{"OPEN"}, RawDescription: "Final text"}}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if err := builder.uploadComparisonCollections(ctx, subject, []string{"OPEN"}); err != nil {
					t.Fatal(err)
				}
				started := time.Now()
				if name == "canceled" {
					time.AfterFunc(time.Second, cancel)
				}
				err := builder.uploadAnalysisDescriptionImages(ctx, subject, []string{"OPEN"})
				wantWait, wantCalls := time.Duration(0), 2
				switch name {
				case "both":
					wantWait = 5 * time.Second
				case "canceled":
					wantWait, wantCalls = time.Second, 1
				case "no comparison", "no analysis":
					wantCalls = 1
				case "final override":
					wantCalls = 0
				}
				if errors.Is(err, context.Canceled) != (name == "canceled") || err != nil && name != "canceled" || time.Since(started) != wantWait || len(host.inputs) != wantCalls {
					t.Fatalf("error=%v wait=%s calls=%d", err, time.Since(started), len(host.inputs))
				}
			})
		})
	}
}

func TestAnalysisCollectionAudioLabelsRetainTrack(t *testing.T) {
	t.Parallel()
	builder, host := collectionDescriptionBuilder(t)
	exact := &api.ExactMediaAssets{CollectionHost: "slowpics"}
	for ordinal := 1; ordinal <= 2; ordinal++ {
		exact.AudioTracks = append(exact.AudioTracks, api.AudioDescriptionTrack{
			Ordinal:     ordinal,
			Images:      []api.ScreenshotImage{{Path: "synthetic.png", Purpose: api.ScreenshotPurposeAudioAnalysis}},
			ImageLabels: []string{"waveform"},
		})
	}
	if err := builder.uploadAnalysisDescriptionImages(t.Context(), api.UploadSubject{ExactMedia: exact}, []string{"OPEN"}); err != nil {
		t.Fatal(err)
	}
	if len(host.inputs) != 1 || !reflect.DeepEqual(host.inputs[0].Labels, []string{"Audio track 1 waveform", "Audio track 2 waveform"}) {
		t.Fatalf("track context lost: %+v", host.inputs)
	}
}
