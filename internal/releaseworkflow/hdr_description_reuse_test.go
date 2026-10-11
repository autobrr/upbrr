// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRDescriptionRecorderUsesAuthorityAtAtomicSave(t *testing.T) {
	module, memory, _ := newCompositeUploadTestModule(t)
	repository := &descriptionAtomicRepositoryFake{MemoryRepository: memory}
	module.repository = repository
	builder := &hdrDescriptionRecorderSpy{descriptionAtomicBuilderFake: descriptionAtomicBuilderFake{&descriptionBuilderFake{testing: t}}}
	module.descriptionBuilder = builder
	module.hdrAnalysisBuilder = &hdrBuilderFixture{}
	started, err := module.StartUpload(t.Context(), testOwnerID,
		compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeDebug, "hdr-description-atomic"))
	if err != nil {
		t.Fatal(err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	current := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA"}, "hdr-description-approval")
	if repository.reusable == nil {
		t.Fatal("plain description was not atomically cached")
	}
	generate := func(key string) {
		t.Helper()
		current = executeCommand(t, module, GenerateDescriptionsCommand{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
			Instructions:     api.DescriptionInstructions{TemplateVersion: "hdr-cache-test"},
			IdempotencyKey:   key,
		})
		stored, err := memory.Load(t.Context(), testOwnerID, current.Workflow.ID)
		if err != nil || stored.Workflow.Descriptions == nil || *stored.Workflow.Descriptions != *current.Workflow.Descriptions {
			t.Fatalf("description/cache save lost owning snapshot: %#v %v", stored.Workflow.Descriptions, err)
		}
	}
	plainRecord := repository.reusable
	release := api.ReleaseRef{SourcePath: current.Release.Release.Source.SourcePath, Generation: current.Release.Release.Generation}
	var attempt string
	for index, peak := range []api.HDRPeakSource{api.HDRPeakHistogram, api.HDRPeakMaxSCL} {
		builder.wantHDR = true
		current = executeCommand(t, module, AnalyzeHDRCommand{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
			Instructions: api.HDRAnalysisInstructions{
				Release:    release,
				TargetIDs:  []string{api.HDRTargetID("source", string(rune('a'+index)))},
				PeakSource: peak,
			},
			IdempotencyKey: "analyze-" + string(peak),
		})
		attempt = current.HDRAnalysis.AttemptID
		generate("generate-" + string(peak))
		if !strings.Contains(current.Descriptions.Descriptions[0].Source, "source_hdr") || repository.reusable != plainRecord {
			t.Fatal("enabled HDR output overwrote the plain reusable record")
		}
		current = executeCommand(t, module, SetHDRAnalysisEnabledCommand{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
			Enabled:          false,
		})
		builder.wantHDR = false
		generate("disabled-" + string(peak))
		if strings.Contains(current.Descriptions.Descriptions[0].Source, "source_hdr") || repository.reusable == plainRecord {
			t.Fatal("disabled HDR did not publish clean generated text and cache")
		}
		plainRecord = repository.reusable
	}
	current = executeCommand(t, module, SetHDRAnalysisEnabledCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Enabled:          true,
	})
	builder.wantHDR = true
	generate("re-enabled")
	// Saving all-final editor text must not ask for unused HDR authority.
	module.private.Delete(testOwnerID, current.Workflow.ID, hdrAnalysisPrivateResourceID(attempt))
	finalText := "Owner's final text [spoiler=source_hdr]intentional notes[/spoiler]"
	current = executeCommand(t, module, SaveDescriptionOverrideCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Descriptions:     *current.Workflow.Descriptions,
		GroupKey:         current.Descriptions.Descriptions[0].GroupKey,
		Source:           finalText,
		IdempotencyKey:   "final-editor-save",
	})
	if current.Descriptions.Descriptions[0].Source != finalText || repository.reusable.Description.Descriptions[0].Source != finalText ||
		!repository.reusable.Description.Overrides[0].Final {
		t.Fatal("all-final text was stripped or lost its atomic reusable save")
	}
	// Retain the pre-existing audio exclusion, including combined audio/HDR.
	state, err := memory.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	state.Workflow.AudioAnalysisEnabled = true
	state.Workflow.AudioAnalysis = &api.AudioAnalysisRef{ID: "audio-result", Revision: 1}
	calls := builder.records
	if err := module.prepareReusableDescriptions(t.Context(), testOwnerID, &state, current.Descriptions, module.clock.Now().UTC()); err != nil ||
		state.descriptionReuse != nil || builder.records != calls {
		t.Fatalf("audio cache exclusion was lost: calls=%d error=%v", builder.records, err)
	}
}

// This spy verifies the actual Module producer and repository transaction seam.
// Core tests separately exercise the real cache classification and restoration.
type hdrDescriptionRecorderSpy struct {
	descriptionAtomicBuilderFake
	wantHDR bool
	records int
}

func (b *hdrDescriptionRecorderSpy) Build(ctx context.Context, release api.ReleaseRef, projections api.TrackerReleaseProjectionSet,
	media api.MediaArtifactSet, private any, instructions api.DescriptionInstructions, now time.Time,
) (api.DescriptionSet, error) {
	snapshot, err := b.descriptionBuilderFake.Build(ctx, release, projections, media, private, instructions, now)
	if err != nil {
		return snapshot, err
	}
	if resources, ok := private.(DescriptionResources); ok && resources.HDR != nil && len(instructions.Overrides) == 0 {
		if _, _, err := resources.HDR(ctx); err != nil {
			return api.DescriptionSet{}, err
		}
		for index := range snapshot.Descriptions {
			snapshot.Descriptions[index].Source += "\n[spoiler=source_hdr]fixture HDR plot[/spoiler]"
		}
	}
	return snapshot, nil
}

func (b *hdrDescriptionRecorderSpy) PrepareReusableDescriptions(ctx context.Context, release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet, media api.MediaArtifactSet, private any, instructions api.DescriptionInstructions, snapshot api.DescriptionSet,
) (*api.ReusableDescriptionRecord, error) {
	b.records++
	resources, wrapped := private.(DescriptionResources)
	if wrapped != b.wantHDR || wrapped && (resources.HDR == nil || resources.Media == nil) {
		b.testing.Errorf("recorder lost HDR resource contract: wanted HDR=%t received=%#v", b.wantHDR, private)
	}
	final := len(instructions.Overrides) > 0 && instructions.Overrides[0].Final
	if wrapped && !final {
		analysis, retained, err := resources.HDR(ctx)
		if err != nil || analysis.Release != release || analysis.Status != api.StageStatusCompleted || retained == nil {
			b.testing.Fatalf("recorder lost exact HDR authority: %#v %v", analysis, err)
		}
		return nil, nil
	}
	return b.descriptionAtomicBuilderFake.PrepareReusableDescriptions(ctx, release, projections, media, private, instructions, snapshot)
}
