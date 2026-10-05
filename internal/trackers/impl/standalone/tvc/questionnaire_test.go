// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProjectionQuestionnaireUsesResolvedNameWhenBlocked(t *testing.T) {
	t.Parallel()

	input := trackers.PreparationInput{Projection: &api.TrackerReleaseProjection{
		UploadReleaseName: "Reviewed Example Name",
	}}
	questionnaire := Profile().ProjectionQuestionnaire(input)
	if questionnaire == nil || len(questionnaire.Fields) != 1 {
		t.Fatalf("expected name field: %#v", questionnaire)
	}
	field := questionnaire.Fields[0]
	if field.Key != "name_override" || field.Value != input.Projection.UploadReleaseName || !field.Required {
		t.Fatalf("expected resolved name despite unrelated readiness failures: %#v", field)
	}
	input.Projection = nil
	if got := projectionQuestionnaire(input); got.Fields[0].Value != "" {
		t.Fatalf("missing projection should not invent a name: %#v", got.Fields[0])
	}
}
