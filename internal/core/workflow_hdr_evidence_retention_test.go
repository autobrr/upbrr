// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lst"
	"github.com/autobrr/upbrr/pkg/api"
)

type workflowHybridResolver struct{ subject api.UploadSubject }

func (r workflowHybridResolver) ResolveUploadSubject(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
	subject := r.subject
	subject.Trackers = input.Trackers
	subject.DescriptionGroups = input.DescriptionGroups
	subject.DescriptionGroupsFinal = input.DescriptionGroupsFinal
	subject.TrackerQuestionnaireAnswers = input.QuestionnaireAnswers
	subject.ImageHostOverrides = input.ImageHostOverrides
	return subject, nil
}

type workflowHybridPaths struct{ root string }

func (p workflowHybridPaths) LocalArtifactPath(_ api.HDRAnalysisResult, id api.PublicResourceID) (string, error) {
	return filepath.Join(p.root, string(id)+".png"), nil
}

func (workflowHybridPaths) OpenArtifact(context.Context, api.HDRAnalysisResult, api.PublicResourceID) (releaseworkflow.MediaArtifactContent, error) {
	return releaseworkflow.MediaArtifactContent{}, errors.New("fixture has no textual artifacts")
}

type workflowHybridHost struct{ descriptionAudioHostFake }

func (h *workflowHybridHost) Upload(ctx context.Context, subject api.ImageHostingSubject, host, scope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	links, err := h.descriptionAudioHostFake.Upload(ctx, subject, host, scope, images)
	for i := range links {
		links[i].RawURL = "https://images.example/" + filepath.Base(links[i].ImagePath)
		links[i].ImgURL = links[i].RawURL
	}
	return links, err
}

type workflowHybridUploadValidator struct {
	workflowRetainedUploadServiceFake
}

func (v *workflowHybridUploadValidator) PrepareRetainedUploadPlan(ctx context.Context, subject api.UploadSubject, projections []api.TrackerReleaseProjection) (workflowRetainedUploadPlan, error) {
	v.subject = subject
	body, _ := subject.TrackerDescriptionOverride("LST")
	if err := lst.Profile().Site.ValidateFinalDescription(subject, body, "MediaInfo", ""); err != nil {
		return nil, fmt.Errorf("validate final Hybrid description: %w", err)
	}
	return v.workflowRetainedUploadServiceFake.PrepareRetainedUploadPlan(ctx, subject, projections)
}

func TestWorkflowHybridEvidenceSurvivesDescriptionAndFinalUpload(t *testing.T) {
	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: filepath.Join(root, "Synthetic.Hybrid.mkv"), Generation: 1}
	subject := api.UploadSubject{
		SourcePath:      release.SourcePath,
		Identity:        api.ExternalIdentity{Generation: release.Generation},
		ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureHybrid},
		HDRFacts:        api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatHDR10Plus}},
	}
	questionnaire := lst.Profile().Site.ProjectionQuestionnaire(trackers.PreparationInput{Meta: subject})
	if !slices.ContainsFunc(questionnaire.Fields, func(field api.TrackerQuestionnaireField) bool { return field.HDRGradeComparison }) {
		t.Fatal("source-only projection silently claimed automatic evidence")
	}
	resolver := workflowHybridResolver{subject: subject}
	registry := trackerimpl.MustNewRegistry()
	descriptor, _ := registry.LookupDescriptor("LST")
	projections := api.TrackerReleaseProjectionSet{
		ID:         "projections",
		Revision:   1,
		ReleaseRef: release,
		Projections: []api.TrackerReleaseProjection{{
			TrackerID:        "LST",
			DescriptionGroup: descriptor.DescriptionGroup,
			Readiness:        api.ReadinessStatusReady,
			UploadReady:      true,
			Artifacts:        api.TrackerArtifactRequirements{Description: true},
		}},
	}
	media := api.MediaArtifactSet{ID: "media", Revision: 1}
	private := workflowMediaPrivateArtifacts{
		HostedImages:  make(map[api.PublicResourceID]api.UploadedImageLink),
		HostedSources: make(map[api.PublicResourceID]api.PublicResourceID),
	}
	for i := range 3 {
		id, hosted := api.PublicResourceID(fmt.Sprintf("screen-%d", i)), api.PublicResourceID(fmt.Sprintf("hosted-%d", i))
		image := api.ScreenshotImage{
			Path:    filepath.Join(root, string(id)+".png"),
			Purpose: api.ScreenshotPurposeFinal,
			Format:  "png",
		}
		private.Screenshots = append(private.Screenshots, image)
		private.HostedImages[hosted] = api.UploadedImageLink{
			ImagePath:  image.Path,
			Purpose:    image.Purpose,
			Host:       "onlyimage",
			UsageScope: "global",
			RawURL:     "https://images.example/" + string(id) + ".png",
			ImgURL:     "https://images.example/" + string(id) + ".png",
			WebURL:     "https://images.example/" + string(id) + ".png",
		}
		private.HostedSources[hosted] = id
		media.Artifacts = append(media.Artifacts,
			api.MediaArtifact{
				ID:       id,
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  image.Purpose,
				Selected: true,
				Order:    i,
			},
			api.MediaArtifact{
				ID:       hosted,
				Kind:     api.MediaArtifactHostedImage,
				Purpose:  image.Purpose,
				Selected: true,
				Order:    i,
			},
		)
	}
	target := api.HDRAnalysisTargetResult{
		TargetID: api.HDRTargetID("synthetic", ""),
		Label:    "Primary source",
		Status:   api.StageStatusCompleted,
		Formats:  []api.HDRFormatResult{{Format: api.HDRNativeHDR10Plus, State: api.HDRExtractionComplete}},
		Decoded: []api.HDRDecodedResult{{
			Mode:      api.HDRDecodedGrade,
			State:     api.HDRDecodedComplete,
			Measured:  1,
			Artifacts: []api.PublicResourceID{"grade"},
		}},
	}
	for _, item := range []struct {
		id     api.PublicResourceID
		kind   api.HDRArtifactKind
		format api.HDRNativeFormat
	}{
		{"hdr10plus", api.HDRArtifactHDR10PlusPlot, api.HDRNativeHDR10Plus},
		{"grade", api.HDRArtifactGradePlot, ""},
	} {
		width, height := item.kind.ImageDimensions()
		target.Artifacts = append(target.Artifacts, api.HDRTypedArtifact{
			ID:      item.id,
			Kind:    item.kind,
			Format:  item.format,
			Width:   width,
			Height:  height,
			MIME:    "image/png",
			Profile: api.HDRAnalysisProfileV2,
		})
	}
	analysis := api.HDRAnalysisResult{
		ID:             "hdr",
		Revision:       2,
		Release:        release,
		ProfileVersion: api.HDRAnalysisProfileV2,
		Status:         api.StageStatusCompleted,
		Targets:        []api.HDRAnalysisTargetResult{target},
	}
	resources := releaseworkflow.DescriptionResources{Media: private, HDR: func(context.Context) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
		return analysis, workflowHybridPaths{root}, nil
	}}
	cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "onlyimage"}}
	for i := range 3 {
		cfg.Description.CustomDescriptionHeader += fmt.Sprintf("[img]https://images.example/screen-%d.png[/img]\n", i)
	}
	host := &workflowHybridHost{}
	builder := workflowDescriptionBuilder{
		resolver: resolver,
		trackers: trackers.NewServiceWithRegistry(cfg, api.NopLogger{}, nil, registry),
		media: &mediaModule{
			cfg:      cfg,
			logger:   api.NopLogger{},
			registry: registry,
			images:   host,
		},
	}
	generated, err := builder.Build(t.Context(), release, projections, media, resources, api.DescriptionInstructions{}, time.Now())
	if err != nil || len(generated.Descriptions) != 1 || len(generated.HDRArtifacts["LST"]) != 2 {
		t.Fatalf("generated description lost selected HDR evidence: %+v err=%v", generated, err)
	}
	body := generated.Descriptions[0].Source
	for _, evidence := range generated.HDRArtifacts["LST"] {
		if len(evidence.ImageURLs) != 1 || !strings.Contains(body, evidence.ImageURLs[0]) {
			t.Fatalf("generated evidence was not embedded: %+v body=%s", evidence, body)
		}
	}
	analysis.Targets[0].Decoded[0].State = api.HDRDecodedInconclusive
	inconclusive, err := builder.Build(t.Context(), release, projections, media, resources, api.DescriptionInstructions{}, time.Now())
	if err != nil || len(inconclusive.Descriptions) != 1 || len(inconclusive.HDRArtifacts["LST"]) != 2 {
		t.Fatalf("inconclusive selected plot must remain renderable: %+v err=%v", inconclusive, err)
	}
	analysis.Targets[0].Decoded[0].State = api.HDRDecodedComplete
	resources.HDRArtifacts = generated.HDRArtifacts
	resources.HDR = func(context.Context) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
		t.Fatal("saved final description reopened HDR resources")
		return api.HDRAnalysisResult{}, nil, nil
	}
	for _, test := range []struct {
		name, body string
		valid      bool
	}{
		{"generated", body, true},
		{"inconclusive generation", inconclusive.Descriptions[0].Source, false},
		{"saved final", body + "\nReviewed source notes", true},
		{"removed grade", strings.ReplaceAll(body, "[img=350]https://images.example/grade.png[/img]", ""), false},
		{"literal grade", strings.ReplaceAll(body, "[img=350]https://images.example/grade.png[/img]", "[code][img=350]https://images.example/grade.png[/img][/code]"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			descriptions := generated
			instructions := api.DescriptionInstructions{}
			if test.name == "inconclusive generation" {
				descriptions = inconclusive
			} else if test.name != "generated" {
				instructions.Overrides = []api.DescriptionOverrideInput{{
					GroupKey:   generated.Descriptions[0].GroupKey,
					TrackerIDs: []api.TrackerID{"LST"},
					Source:     test.body,
					Final:      true,
				}}
				descriptions, err = builder.Build(t.Context(), release, projections, media, resources, instructions, time.Now())
				if err != nil || len(descriptions.HDRArtifacts["LST"]) != 2 {
					t.Fatalf("final edit lost retained image associations: %+v err=%v", descriptions, err)
				}
			}
			validation := &workflowHybridUploadValidator{}
			upload := workflowUploadPlanBuilder{
				resolver: resolver,
				trackers: validation,
				torrents: &workflowTorrentServiceCapture{},
			}
			_, execution, buildErr := upload.Build(t.Context(), projections,
				api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
					TrackerID: "LST",
					Decision:  api.DupeDecisionNoMatch,
					Status:    api.StageStatusCompleted,
				}}},
				workflowDupePrivateEvidence{}, media, private, descriptions, instructions, releaseworkflow.UploadPlanBuildOptions{}, time.Now())
			if execution != nil {
				_ = execution.Release()
			}
			if (buildErr == nil) != test.valid || !test.valid && !strings.Contains(buildErr.Error(), "source-linked grade comparison") {
				t.Fatalf("final upload valid=%v err=%v body=%s", test.valid, buildErr, test.body)
			}
			if validation.subject.ExactMedia == nil || validation.subject.ExactMedia.HDRAnalysis != nil || len(validation.subject.HDRArtifactEvidence("LST")) != 2 {
				t.Fatal("final upload did not consume retained path-free evidence")
			}
			questionnaire := lst.Profile().Site.ProjectionQuestionnaire(trackers.PreparationInput{Meta: validation.subject})
			if needs := slices.ContainsFunc(questionnaire.Fields, func(field api.TrackerQuestionnaireField) bool { return field.HDRGradeComparison }); needs == test.valid {
				t.Fatalf("current final description questionnaire needs evidence=%v", needs)
			}
			if test.valid {
				withoutEvidence := validation.subject
				withoutEvidence.TrackerHDRArtifacts = nil
				if err := lst.Profile().Site.ValidateFinalDescription(withoutEvidence, test.body, "MediaInfo", ""); err == nil {
					t.Fatal("unbound image URLs alone satisfied Hybrid evidence")
				}
			}
		})
	}
}
