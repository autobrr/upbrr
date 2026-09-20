// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
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
