// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/mediafacts"
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
	failures []api.RuleFailure
}

func (v *workflowHybridUploadValidator) PrepareRetainedUploadPlan(ctx context.Context, subject api.UploadSubject, projections []api.TrackerReleaseProjection) (workflowRetainedUploadPlan, error) {
	v.subject = subject
	body, _ := subject.TrackerDescriptionOverride("LST")
	if err := lst.Profile().Site.ValidateFinalDescription(subject, body, "MediaInfo", ""); err != nil {
		return nil, fmt.Errorf("validate final Hybrid description: %w", err)
	}
	failures, err := lst.Profile().ValidationPolicy.Check(ctx, api.NewTrackerValidationSubject(subject, "LST"), api.NopLogger{})
	if err != nil {
		return nil, fmt.Errorf("validate Hybrid evidence: %w", err)
	}
	v.failures = failures
	for _, projection := range projections {
		failure, err := trackers.FirstBlockingRuleFailure("LST", failures, "", &projection)
		if err != nil {
			return nil, fmt.Errorf("validate Hybrid warning authority: %w", err)
		}
		if failure != nil {
			return nil, fmt.Errorf("unacknowledged LST rule %s: %s", failure.Rule, failure.Reason)
		}
	}
	return v.workflowRetainedUploadServiceFake.PrepareRetainedUploadPlan(ctx, subject, projections)
}

func requireWorkflowHybridFinding(t *testing.T, failures []api.RuleFailure, disposition api.RuleDisposition) {
	t.Helper()
	var hybrid []api.RuleFailure
	for _, failure := range failures {
		if strings.Contains(failure.Rule, "lst_hybrid_hdr_evidence") {
			hybrid = append(hybrid, failure)
		}
	}
	if disposition == "" {
		if len(hybrid) != 0 {
			t.Fatalf("included complete evidence still needs review: %+v", hybrid)
		}
		return
	}
	rule, status := "guidance_lst_hybrid_hdr_evidence", api.MetadataEvidenceStatusPartial
	if disposition == api.RuleDispositionWaivable {
		rule, status = "lst_hybrid_hdr_evidence", api.MetadataEvidenceStatusComplete
	}
	if len(hybrid) != 1 || hybrid[0].Rule != rule || hybrid[0].Disposition != disposition || hybrid[0].EvidenceStatus != status ||
		hybrid[0].QuestionnaireInput || hybrid[0].EvidenceFingerprint == "" {
		t.Fatalf("Hybrid finding must be %s/%s, not reassurance: %+v", rule, status, hybrid)
	}
}

func TestWorkflowHybridEvidenceSurvivesDescriptionAndFinalUpload(t *testing.T) {
	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: filepath.Join(root, "Synthetic.Hybrid.mkv"), Generation: 1}
	mediaInfo := filepath.Join(root, "mediainfo.txt")
	if err := os.WriteFile(mediaInfo, []byte("General\nFormat : Matroska\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	subject := api.UploadSubject{
		SourcePath:        release.SourcePath,
		Identity:          api.ExternalIdentity{SourcePath: release.SourcePath, Generation: release.Generation},
		Container:         "mkv",
		Type:              "WEBDL",
		MediaInfoTextPath: mediaInfo,
		ReleaseFeatures:   []api.ReleaseFeature{api.ReleaseFeatureHybrid},
		HDRFacts:          api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatHDR10Plus}},
		LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "English",
			TrackCoverageComplete: true,
			Tracks: []api.MediaTrackFacts{{
				ID:           "audio-0",
				Kind:         api.MediaTrackAudio,
				Role:         api.AudioRoleProgramme,
				Languages:    []string{"English"},
				Codec:        "AAC",
				DefaultKnown: true,
				Default:      true,
			}},
		}),
	}
	questionnaire := lst.Profile().Site.ProjectionQuestionnaire(trackers.PreparationInput{Meta: subject})
	if questionnaire != nil {
		t.Fatalf("source-only projection restored retired reassurance questions: %+v", questionnaire)
	}
	failures, err := lst.Profile().ValidationPolicy.Check(t.Context(), api.NewTrackerValidationSubject(subject, "LST"), api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	requireWorkflowHybridFinding(t, failures, api.RuleDispositionAdvisory)
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
		artifact := slices.IndexFunc(target.Artifacts, func(artifact api.HDRTypedArtifact) bool { return artifact.ID == evidence.ArtifactID })
		if artifact < 0 || evidence.Analysis != (api.HDRAnalysisRef{ID: analysis.ID, Revision: analysis.Revision}) ||
			evidence.TargetID != target.TargetID || evidence.Kind != target.Artifacts[artifact].Kind || !evidence.GenerationComplete ||
			len(evidence.ImageURLs) != 1 || !strings.Contains(body, evidence.ImageURLs[0]) {
			t.Fatalf("generated evidence was not embedded: %+v body=%s", evidence, body)
		}
	}
	analysis.Targets[0].Decoded[0].State = api.HDRDecodedInconclusive
	inconclusive, err := builder.Build(t.Context(), release, projections, media, resources, api.DescriptionInstructions{}, time.Now())
	if err != nil || len(inconclusive.Descriptions) != 1 || len(inconclusive.HDRArtifacts["LST"]) != 2 {
		t.Fatalf("inconclusive selected plot must remain renderable: %+v err=%v", inconclusive, err)
	}
	if !slices.ContainsFunc(inconclusive.HDRArtifacts["LST"], func(evidence api.HDRArtifactEvidence) bool {
		return evidence.ArtifactID == "grade" && !evidence.GenerationComplete
	}) {
		t.Fatal("inconclusive grade plot manufactured completed evidence")
	}
	analysis.Targets[0].Decoded[0].State = api.HDRDecodedComplete
	resources.HDRArtifacts = generated.HDRArtifacts
	resources.HDR = func(context.Context) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
		t.Fatal("saved final description reopened HDR resources")
		return api.HDRAnalysisResult{}, nil, nil
	}
	for _, test := range []struct {
		name, body  string
		disposition api.RuleDisposition
	}{
		{"generated", body, ""},
		{"inconclusive generation", inconclusive.Descriptions[0].Source, api.RuleDispositionAdvisory},
		{"saved final", body + "\nReviewed source notes", ""},
		{"removed grade", strings.ReplaceAll(body, "[img=350]https://images.example/grade.png[/img]", ""), api.RuleDispositionWaivable},
		{"literal grade", strings.ReplaceAll(body, "[img=350]https://images.example/grade.png[/img]", "[code][img=350]https://images.example/grade.png[/img][/code]"), api.RuleDispositionWaivable},
		{"stale grade URL", strings.ReplaceAll(body, "https://images.example/grade.png", "https://images.example/stale-grade.png"), api.RuleDispositionWaivable},
		{"unbound URLs", body, api.RuleDispositionAdvisory},
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
				if err != nil || !reflect.DeepEqual(descriptions.HDRArtifacts["LST"], generated.HDRArtifacts["LST"]) {
					t.Fatalf("final edit lost retained image associations: %+v err=%v", descriptions, err)
				}
			}
			if test.name == "unbound URLs" {
				descriptions.HDRArtifacts = nil
			}
			validation := &workflowHybridUploadValidator{}
			upload := workflowUploadPlanBuilder{
				resolver: resolver,
				trackers: validation,
				torrents: &workflowTorrentServiceCapture{},
			}
			currentProjections := projections
			currentProjections.Projections = slices.Clone(projections.Projections)
			build := func() error {
				_, execution, buildErr := upload.Build(t.Context(), currentProjections,
					api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
						TrackerID: "LST",
						Decision:  api.DupeDecisionNoMatch,
						Status:    api.StageStatusCompleted,
					}}},
					workflowDupePrivateEvidence{}, media, private, descriptions, instructions, releaseworkflow.UploadPlanBuildOptions{}, time.Now())
				if execution != nil {
					_ = execution.Release()
				}
				return buildErr
			}
			buildErr := build()
			warning := test.disposition == api.RuleDispositionWaivable
			if (buildErr != nil) != warning || warning && !strings.Contains(buildErr.Error(), "lst_hybrid_hdr_evidence") {
				t.Fatalf("final upload warning=%v err=%v body=%s", warning, buildErr, test.body)
			}
			requireWorkflowHybridFinding(t, validation.failures, test.disposition)
			if validation.subject.ExactMedia == nil || validation.subject.ExactMedia.HDRAnalysis != nil ||
				!reflect.DeepEqual(validation.subject.HDRArtifactEvidence("LST"), descriptions.HDRArtifacts["LST"]) {
				t.Fatal("final upload did not consume retained path-free evidence")
			}
			questionnaire := lst.Profile().Site.ProjectionQuestionnaire(trackers.PreparationInput{Meta: validation.subject})
			if questionnaire != nil {
				t.Fatalf("final description restored retired reassurance questions: %+v", questionnaire)
			}
			fingerprint, err := trackers.WaivableRuleFailureFingerprint("LST", validation.failures)
			if err != nil || (fingerprint != "") != warning {
				t.Fatalf("unexpected warning authority %q: %v failures=%+v", fingerprint, err, validation.failures)
			}
			if warning {
				projection := &currentProjections.Projections[0]
				projection.WaivableRuleFingerprint = fingerprint
				if err := build(); err == nil {
					t.Fatal("warning fingerprint alone authorized upload")
				}
				projection.RuleAuthorizationFingerprint = fingerprint
				if err := build(); err != nil {
					t.Fatalf("exact warning acknowledgement did not authorize upload: %v", err)
				}
				for _, changed := range []string{"description", "evidence"} {
					t.Run("stale acknowledgement after "+changed, func(t *testing.T) {
						updated, err := descriptions.Clone()
						if err != nil {
							t.Fatal(err)
						}
						if changed == "description" {
							updated.Descriptions[0].Source += "\nChanged source notes"
						} else {
							updated.HDRArtifacts["LST"][0].Analysis.Revision++
						}
						original := descriptions
						descriptions = updated
						err = build()
						descriptions = original
						if err == nil || !strings.Contains(err.Error(), "lst_hybrid_hdr_evidence") {
							t.Fatalf("stale warning authority accepted changed %s: %v", changed, err)
						}
						changedFingerprint, err := trackers.WaivableRuleFailureFingerprint("LST", validation.failures)
						if err != nil || changedFingerprint == "" || changedFingerprint == fingerprint {
							t.Fatalf("changed %s retained warning fingerprint %q: %v", changed, changedFingerprint, err)
						}
					})
				}
			}
		})
	}
}
