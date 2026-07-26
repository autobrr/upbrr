// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestReleaseWorkflowUploadRequestRoundTripPreservesPresence(t *testing.T) {
	t.Parallel()

	zero := 0
	empty := ""
	disabled := false
	request := CreateReleaseWorkflowUploadRequest{
		Source:     ReleaseWorkflowUploadSource{Path: `D:\Example Release 2026`},
		Unattended: &ReleaseWorkflowUploadUnattended{},
		Execution: ReleaseWorkflowUploadExecution{
			Mode:            ReleaseWorkflowUploadModeDebug,
			PreparedRelease: ReleaseWorkflowPreparedReleaseRequire,
		},
		Preparation: ReleaseWorkflowUploadPreparation{
			Facts: ReleaseWorkflowUploadFacts{
				ExternalIDs: ReleaseWorkflowUploadExternalIDs{
					TMDB: &ReleaseWorkflowUploadNumericID{Value: &zero},
					IMDB: &ReleaseWorkflowUploadStringID{Value: &empty},
				},
				ReleaseName: ReleaseWorkflowUploadReleaseName{
					Tag:    &empty,
					NoYear: &disabled,
				},
			},
			ClientSearch: ReleaseWorkflowUploadClientSearch{Skip: &disabled},
		},
		Media: ReleaseWorkflowUploadMedia{
			Screenshots: ReleaseWorkflowUploadScreenshots{Count: &zero},
		},
		Client:         ReleaseWorkflowUploadClient{NoSeed: &disabled},
		IdempotencyKey: "transport-only",
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal upload request: %v", err)
	}
	var decoded CreateReleaseWorkflowUploadRequest
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal upload request: %v", err)
	}
	if decoded.IdempotencyKey != "" {
		t.Fatalf("transport idempotency key entered JSON: %q", decoded.IdempotencyKey)
	}
	request.IdempotencyKey = ""
	if !reflect.DeepEqual(decoded, request) {
		t.Fatalf("round trip changed presence:\n got: %#v\nwant: %#v", decoded, request)
	}
}

func TestReleaseWorkflowUploadRequestValidation(t *testing.T) {
	t.Parallel()

	valid := CreateReleaseWorkflowUploadRequest{
		Source:         ReleaseWorkflowUploadSource{Path: `D:\Example Release 2026`},
		Unattended:     &ReleaseWorkflowUploadUnattended{},
		IdempotencyKey: "upload-1",
	}
	tests := []struct {
		name   string
		mutate func(*CreateReleaseWorkflowUploadRequest)
	}{
		{"missing source", func(request *CreateReleaseWorkflowUploadRequest) { request.Source.Path = "" }},
		{"missing unattended", func(request *CreateReleaseWorkflowUploadRequest) { request.Unattended = nil }},
		{"bad mode", func(request *CreateReleaseWorkflowUploadRequest) { request.Execution.Mode = "unsafe" }},
		{"tracker overlap", func(request *CreateReleaseWorkflowUploadRequest) {
			request.Trackers.Include = []TrackerID{"alpha"}
			request.Trackers.Remove = []TrackerID{"ALPHA"}
		}},
		{"bad comparison index", func(request *CreateReleaseWorkflowUploadRequest) {
			index := 2
			request.Media.Screenshots.ComparisonPaths = []string{`D:\comparisons`}
			request.Media.Screenshots.ComparisonPrimaryIndex = &index
		}},
		{"description source conflict", func(request *CreateReleaseWorkflowUploadRequest) {
			inline, file := "example", `D:\description.txt`
			request.Descriptions.Overrides = []ReleaseWorkflowUploadDescriptionOverride{{
				GroupKey: "example",
				Inline:   &inline,
				File:     &file,
			}}
		}},
		{"torrent conflict", func(request *CreateReleaseWorkflowUploadRequest) {
			enabled := true
			request.Torrent.NoHash = &enabled
			request.Torrent.Rehash = &enabled
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatalf("Validate accepted %s", test.name)
			}
		})
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid upload request: %v", err)
	}
}

func TestReleaseWorkflowUploadFeedbackRequiresMatchingDiscriminatedMember(t *testing.T) {
	t.Parallel()

	valid := ReleaseWorkflowUploadFeedback{
		Action: ReleaseWorkflowUploadActionIdentity{
			ID:               "action-example",
			WorkflowRevision: 4,
		},
		Response: ReleaseWorkflowUploadFeedbackResponse{
			Kind: ReleaseWorkflowUploadFeedbackDuplicateReview,
			DuplicateReview: &ReleaseWorkflowUploadDuplicateReview{
				TrackerID: "EXAMPLE",
				Decision:  DupeDecisionIgnored,
			},
		},
		IdempotencyKey: "feedback-1",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid upload feedback: %v", err)
	}
	mismatched := valid
	mismatched.Response.Kind = ReleaseWorkflowUploadFeedbackUploadApproval
	if err := mismatched.Validate(); err == nil {
		t.Fatal("feedback accepted a mismatched discriminator")
	}
	ambiguous := valid
	ambiguous.Response.UploadApproval = &ReleaseWorkflowUploadApproval{Confirmed: true}
	if err := ambiguous.Validate(); err == nil {
		t.Fatal("feedback accepted multiple response members")
	}
}

func TestReleaseWorkflowUploadFeedbackRejectsDuplicateApprovalTrackers(t *testing.T) {
	t.Parallel()

	feedback := ReleaseWorkflowUploadFeedback{
		Action: ReleaseWorkflowUploadActionIdentity{
			ID:               "action-approval",
			WorkflowRevision: 2,
		},
		Response: ReleaseWorkflowUploadFeedbackResponse{
			Kind: ReleaseWorkflowUploadFeedbackTrackerApproval,
			TrackerApproval: &ReleaseWorkflowUploadTrackerApproval{
				Confirmed:  true,
				TrackerIDs: []TrackerID{"alpha", "ALPHA"},
			},
		},
		IdempotencyKey: "approval-duplicate-trackers",
	}
	if err := feedback.Validate(); err == nil {
		t.Fatal("tracker approval accepted duplicate normalized tracker IDs")
	}
}
