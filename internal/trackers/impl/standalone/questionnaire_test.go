// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package standalone

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestQuestionnaireAnswersNormalizesRequestedTrackerID(t *testing.T) {
	want := map[string]string{"category": "movie"}
	meta := api.UploadSubject{TrackerQuestionnaireAnswers: map[string]map[string]string{"PTP": want}}

	if got := QuestionnaireAnswers(meta, " ptp "); got["category"] != want["category"] {
		t.Fatalf("expected canonical tracker answers, got %#v", got)
	}
}

func TestQuestionnaireAnswersRequiresCanonicalStoredTrackerID(t *testing.T) {
	meta := api.UploadSubject{TrackerQuestionnaireAnswers: map[string]map[string]string{"ptp": {"category": "movie"}}}
	if got := QuestionnaireAnswers(meta, "PTP"); got != nil {
		t.Fatalf("expected non-canonical stored key to be ignored, got %#v", got)
	}
}
