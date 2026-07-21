// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package standalone

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// QuestionnaireAnswers returns the answers stored under a normalized tracker ID.
// Tracker answer maps use canonical uppercase IDs; missing or non-canonical keys return nil.
func QuestionnaireAnswers(meta api.UploadSubject, tracker string) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers[strings.ToUpper(strings.TrimSpace(tracker))]
}
