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
