// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDescriptionCacheFailureAllowsNewKeyRetryAtOriginalRevision(t *testing.T) {
	t.Parallel()
	module, memory, _ := newCompositeUploadTestModule(t)
	repository := &descriptionAtomicRepositoryFake{MemoryRepository: memory}
	module.repository = repository
	module.descriptionBuilder = descriptionAtomicBuilderFake{&descriptionBuilderFake{testing: t}}
	started, err := module.StartUpload(t.Context(), testOwnerID,
		compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeDebug, "description-atomic"))
	if err != nil {
		t.Fatal(err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	current := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA"}, "description-approval")
	if current.Descriptions == nil || repository.reusable == nil {
		t.Fatalf("initial description/cache unavailable: %#v", current.Descriptions)
	}
	priorSource := repository.reusable.Description.Descriptions[0].Source
	command := SaveDescriptionOverrideCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Descriptions:     *current.Workflow.Descriptions,
		GroupKey:         current.Descriptions.Descriptions[0].GroupKey,
		Source:           "Retained custom notes",
		IdempotencyKey:   "first-save",
	}
	repository.failCache = true
	if _, err := module.Execute(t.Context(), testOwnerID, command); err == nil {
		t.Fatal("description cache failure was not returned")
	}
	afterFailure, err := module.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.Workflow.Revision != current.Workflow.Revision || afterFailure.Descriptions.ID != current.Descriptions.ID ||
		repository.reusable.Description.Descriptions[0].Source != priorSource {
		t.Fatal("failed cache write committed the edit, receipt, or cache")
	}
	// The WebUI retries with a new key and the revision it knew before the failure.
	repository.failCache = false
	command.IdempotencyKey = "ui-retry-new-key"
	retried, err := module.Execute(t.Context(), testOwnerID, command)
	if err != nil {
		t.Fatalf("retry at original revision: %v", err)
	}
	if retried.Descriptions.Descriptions[0].Source != command.Source ||
		repository.reusable.Description.Descriptions[0].Source != command.Source {
		t.Fatal("successful retry did not publish both edit and reusable description")
	}
	if len(repository.reusable.Description.Overrides) != 1 || !repository.reusable.Description.Overrides[0].Final ||
		!slices.Equal(repository.reusable.Description.Overrides[0].TrackerIDs, current.Descriptions.Descriptions[0].TrackerIDs) {
		t.Fatal("saved editor output lost its final-source marker in reusable instructions")
	}
}

func TestSaveMainDescriptionPublishesOneCompleteRevision(t *testing.T) {
	t.Parallel()
	module, memory, _ := newCompositeUploadTestModule(t)
	repository := &descriptionAtomicRepositoryFake{MemoryRepository: memory}
	module.repository = repository
	module.descriptionBuilder = descriptionAtomicBuilderFake{&descriptionBuilderFake{testing: t}}
	started, err := module.StartUpload(t.Context(), testOwnerID,
		compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeDebug, "main-description-atomic"))
	if err != nil {
		t.Fatal(err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	current := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA"}, "approve-main-description")
	if current.Descriptions == nil || repository.reusable == nil || len(current.Descriptions.Descriptions) == 0 {
		t.Fatalf("fixture lacks selected description groups: %#v", current.Descriptions)
	}
	saveGroup := executeCommand(t, module, SaveDescriptionOverrideCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Descriptions:     *current.Workflow.Descriptions,
		GroupKey:         current.Descriptions.Descriptions[0].GroupKey,
		Source:           "Old group edit",
		IdempotencyKey:   "before-main-edit",
	})
	request := SaveMainDescriptionCommand{
		WorkflowID:       saveGroup.Workflow.ID,
		ExpectedRevision: saveGroup.Workflow.Revision,
		Descriptions:     *saveGroup.Workflow.Descriptions,
		Source:           "  [b]New shared text[/b]\n",
		IdempotencyKey:   "main-save-fails",
	}
	repository.failCache = true
	if _, err := module.Execute(t.Context(), testOwnerID, request); err == nil {
		t.Fatal("cache failure allowed publishing the main editor source")
	}
	before, err := module.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil || before.Descriptions.MainSource != nil || before.Workflow.Revision != saveGroup.Workflow.Revision {
		t.Fatalf("failed main save changed current workflow: %#v, %v", before, err)
	}
	repository.failCache = false
	request.IdempotencyKey = "main-save"
	saved := executeCommand(t, module, request)
	if saved.Descriptions.MainSource == nil || *saved.Descriptions.MainSource != request.Source ||
		len(saved.Descriptions.Descriptions) != len(current.Descriptions.Descriptions) || saved.Workflow.DryRun != nil {
		t.Fatalf("main save did not atomically supersede the current descriptions: %#v", saved.Descriptions)
	}
	for _, output := range saved.Descriptions.Descriptions {
		want := "Example description for " + output.GroupKey + ".\n" + request.Source
		if output.Source != want {
			t.Fatalf("group output not regenerated from shared input: %q, want %q", output.Source, want)
		}
	}
	repeated := executeCommand(t, module, request)
	if repeated.Descriptions.ID != saved.Descriptions.ID {
		t.Fatal("idempotent save published another revision")
	}
	regenerated := executeCommand(t, module, GenerateDescriptionsCommand{
		WorkflowID:       saved.Workflow.ID,
		ExpectedRevision: saved.Workflow.Revision,
		Instructions:     api.DescriptionInstructions{TemplateVersion: "regenerated"},
		IdempotencyKey:   "main-retained-regenerate",
	})
	if regenerated.Descriptions.MainSource == nil || *regenerated.Descriptions.MainSource != request.Source {
		t.Fatal("generating descriptions without a new source lost the saved main input")
	}
	request.ExpectedRevision = regenerated.Workflow.Revision
	request.Descriptions = *regenerated.Workflow.Descriptions
	request.Source, request.IdempotencyKey = "", "main-save-empty"
	cleared := executeCommand(t, module, request)
	if cleared.Descriptions.MainSource == nil || *cleared.Descriptions.MainSource != "" {
		t.Fatal("clearing source was not explicit in the saved description snapshot")
	}
}

func TestMainDescriptionOnlySupersedesSelectedTrackerOverrides(t *testing.T) {
	projections := []api.TrackerReleaseProjection{testProjection(t, "ALPHA", "Example")}
	projections[0].Artifacts.Description = true
	projections[0].DescriptionGroup = "specific"
	groups := []api.RenderedDescription{{GroupKey: "specific|selected-image-host", TrackerIDs: []api.TrackerID{"ALPHA"}}}
	overrides := []api.DescriptionOverrideInput{
		{
			GroupKey:   "specific|selected-image-host",
			Source:     "split this",
			Final:      true,
			TrackerIDs: []api.TrackerID{"ALPHA", "BETA"},
		},
		{GroupKey: "specific", Source: "old group-wide draft"},
		{
			GroupKey:   "unrelated",
			Source:     "keep this",
			Final:      true,
			TrackerIDs: []api.TrackerID{"GAMMA"},
		},
	}
	want := []api.DescriptionOverrideInput{
		{
			GroupKey:   "specific|selected-image-host",
			Source:     "split this",
			Final:      true,
			TrackerIDs: []api.TrackerID{"BETA"},
		},
		overrides[2],
	}
	got := descriptionOverridesOutsideMainTargets(overrides, projections, groups)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("main save replaced unrelated overrides: got %#v, want %#v", got, want)
	}
}

type descriptionAtomicRepositoryFake struct {
	*MemoryRepository
	failCache bool
	reusable  *api.ReusableDescriptionRecord
}

func (r *descriptionAtomicRepositoryFake) Save(ctx context.Context, owner string, revision api.WorkflowRevision, state State) error {
	if state.descriptionReuse != nil && r.failCache {
		return errors.New("injected atomic description cache failure")
	}
	if err := r.MemoryRepository.Save(ctx, owner, revision, state); err != nil {
		return err
	}
	if state.descriptionReuse != nil {
		r.reusable = state.descriptionReuse
	}
	return nil
}

type descriptionAtomicBuilderFake struct{ *descriptionBuilderFake }

func (b descriptionAtomicBuilderFake) PrepareReusableDescriptions(_ context.Context, release api.ReleaseRef,
	_ api.TrackerReleaseProjectionSet, _ api.MediaArtifactSet, _ any, instructions api.DescriptionInstructions, snapshot api.DescriptionSet,
) (*api.ReusableDescriptionRecord, error) {
	return &api.ReusableDescriptionRecord{
		SourcePath: release.SourcePath,
		Description: api.ReusableDescription{
			CompatibilityFingerprint: testFingerprint(b.testing, "atomic-description"),
			Descriptions:             snapshot.Descriptions,
			TrackerResults:           snapshot.TrackerResults,
			Overrides:                instructions.Overrides,
		},
	}, nil
}

func (descriptionAtomicBuilderFake) RestoreCompatibleDescriptions(_ context.Context, _ api.ReleaseRef,
	_ api.TrackerReleaseProjectionSet, _ api.MediaArtifactSet, _ any, instructions api.DescriptionInstructions,
) (api.DescriptionSet, api.DescriptionInstructions, error) {
	return api.DescriptionSet{}, instructions, nil
}

func TestDescriptionFailedCacheRefreshRetainsFinalEdits(t *testing.T) {
	module, _, _ := newCompositeUploadTestModule(t)
	started, err := module.StartUpload(t.Context(), testOwnerID,
		compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeDebug, "description-refresh"))
	if err != nil {
		t.Fatal(err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	current := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA"}, "refresh-approval")
	builder := &descriptionFailedRefreshFake{
		descriptionAtomicBuilderFake: descriptionAtomicBuilderFake{&descriptionBuilderFake{testing: t}},
		override: api.DescriptionOverrideInput{
			GroupKey:   current.Descriptions.Descriptions[0].GroupKey,
			TrackerIDs: []api.TrackerID{"ALPHA"},
			Source:     "Owner final text",
			Final:      true,
		},
	}
	module.descriptionBuilder = builder
	result := executeCommand(t, module, GenerateDescriptionsCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Instructions:     api.DescriptionInstructions{TemplateVersion: "refresh-renderer"},
		IdempotencyKey:   "refresh-failed",
	})
	if result.Descriptions == nil || result.Descriptions.Status != api.StageStatusFailed || builder.builds != 0 {
		t.Fatalf("failed refresh was discarded or rebuilt: descriptions=%+v builds=%d", result.Descriptions, builder.builds)
	}
	retained, err := module.private.Get(testOwnerID, result.Workflow.ID, descriptionPrivateResourceID(result.Descriptions.ID), module.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	instructions, ok := retained.(api.DescriptionInstructions)
	if !ok || !reflect.DeepEqual(instructions.Overrides, []api.DescriptionOverrideInput{builder.override}) {
		t.Fatalf("final edits not retained for retry: %+v", retained)
	}
}

type descriptionFailedRefreshFake struct {
	descriptionAtomicBuilderFake
	override api.DescriptionOverrideInput
}

func (b *descriptionFailedRefreshFake) RestoreCompatibleDescriptions(ctx context.Context, release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet, media api.MediaArtifactSet, private any, instructions api.DescriptionInstructions,
) (api.DescriptionSet, api.DescriptionInstructions, error) {
	instructions.Overrides = []api.DescriptionOverrideInput{b.override}
	input, template, err := b.Fingerprints(ctx, release, projections, media, private, instructions)
	return api.DescriptionSet{
		InputFingerprint:    input,
		TemplateFingerprint: template,
		Status:              api.StageStatusFailed,
		Failures: []api.WorkflowFailure{{
			TrackerID: "ALPHA",
			Failure: api.OperationFailure{
				Code:      api.OperationFailureInvalidInput,
				Operation: api.OperationKindDescription,
				Message:   "Injected renderer failure",
			},
		}},
	}, instructions, err
}
