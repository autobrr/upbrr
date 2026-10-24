// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type firstAttachmentMediaMutator struct {
	hostSpecificMediaMutator
	testing *testing.T
}

func (m *firstAttachmentMediaMutator) Attach(
	_ context.Context,
	_ api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	prior *api.MediaArtifactSet,
	_ any,
	attachments []StagedMediaAttachment,
	_ time.Time,
) (api.MediaArtifactSet, RetainedMediaResource, error) {
	if prior != nil {
		m.testing.Fatal("first attachment unexpectedly received prior media")
	}
	requirements, err := mediaRequirementsFingerprint(projections.Projections)
	return api.MediaArtifactSet{
		AutomaticScreenshotSelection: true,
		CaptureFingerprint:           testFingerprint(m.testing, "first-attachment"),
		RequirementsFingerprint:      requirements,
		Status:                       api.StageStatusCompleted,
		Artifacts: []api.MediaArtifact{{
			ID:       "attached",
			Kind:     attachments[0].Attachment.Kind,
			Purpose:  attachments[0].Attachment.Purpose,
			Selected: true,
		}},
	}, &retainedMediaResourceFake{stats: &retainedMediaResourceStats{}}, err
}

func TestFirstMediaAttachmentPublishesWithoutPriorSnapshot(t *testing.T) {
	for _, kind := range []api.MediaArtifactKind{api.MediaArtifactScreenshot, api.MediaArtifactDVDMenu} {
		t.Run(string(kind), func(t *testing.T) {
			module, repository, _, current := newDuplicateReuseWorkflow(t)
			if current.Workflow.Media != nil {
				t.Fatal("fixture already has media")
			}
			module.mediaBuilder = &firstAttachmentMediaMutator{testing: t}
			resource, err := module.StageMediaResource(t.Context(), testOwnerID, current.Workflow.ID, current.Workflow.Revision, StagedMediaContent{
				Name:        "attached.png",
				Bytes:       []byte("synthetic staged image"),
				ContentType: "image/png",
			})
			if err != nil {
				t.Fatal(err)
			}
			purpose := api.ScreenshotPurposeFinal
			if kind == api.MediaArtifactDVDMenu {
				purpose = api.ScreenshotPurposeMenu
			}
			attached := executeCommand(t, module, AttachMediaArtifactsCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Attachments: []api.MediaAttachment{{
					Resource: resource,
					Kind:     kind,
					Purpose:  purpose,
				}},
			})
			if attached.Media == nil || len(attached.Media.Artifacts) != 1 || attached.Media.AutomaticScreenshotSelection {
				t.Fatalf("first attachment did not retain conservative selection intent: %+v", attached.Media)
			}
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Workflow.Media == nil || state.Media[state.Workflow.Media.ID].AutomaticScreenshotSelection {
				t.Fatal("first attachment was not durably published with explicit selection intent")
			}
		})
	}
}
