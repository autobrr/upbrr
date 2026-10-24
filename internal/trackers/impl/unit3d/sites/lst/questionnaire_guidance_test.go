// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLSTQuestionnaireRetainsOnlyGenuineInputs(t *testing.T) {
	for _, releaseType := range []string{"REMUX", "ENCODE", "WEBDL", "BDMV"} {
		for _, personal := range []bool{false, true} {
			for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
				t.Run(fmt.Sprintf("%s/personal=%t/%s", releaseType, personal, mode), func(t *testing.T) {
					meta := api.UploadSubject{Type: releaseType, PersonalRelease: personal}
					if releaseType == "BDMV" {
						meta.DiscType = "BDMV"
					}
					questionnaire := languageQuestionnaire(trackers.PreparationInput{Meta: meta, ExecutionMode: mode})
					if questionnaire != nil {
						for _, field := range questionnaire.Fields {
							if !strings.HasPrefix(field.Key, "compatibility_mix_") && !strings.HasPrefix(field.Key, "exception_note_") {
								t.Fatalf("reassurance control survived: %+v", field)
							}
						}
					}
				})
			}
		}
	}
}
