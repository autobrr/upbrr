// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const channelAnswerKey = "channel"

func resolveChannelInput(meta api.UploadSubject, configured string) string {
	answers := standalone.QuestionnaireAnswers(meta, "SPD")
	return metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers[channelAnswerKey]), strings.TrimSpace(configured))
}

func buildChannelQuestionnaire(input string) *api.TrackerQuestionnaire {
	return &api.TrackerQuestionnaire{
		Tracker: "SPD",
		Fields: []api.TrackerQuestionnaireField{{
			Key:         channelAnswerKey,
			Label:       "Channel",
			Kind:        "text",
			Value:       strings.TrimSpace(input),
			Placeholder: "1 or channel tag",
			Required:    true,
		}},
	}
}

func validChannelID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
