// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	oeEncodingSettingsKey = "encoding_settings"
	oeSourceNotesKey      = "source_notes"
)

// inputSchema declares OE's conditional description evidence fields.
func inputSchema(meta api.UploadSubject) *api.TrackerQuestionnaire {
	answers := oeQuestionnaireAnswers(meta)
	fields := make([]api.TrackerQuestionnaireField, 0, 2)
	if oeRequiresEncodingSettings(meta.VideoCodec, meta.Type, meta.HasEncodeSettings) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         oeEncodingSettingsKey,
			Label:       oeEncodingSettingsLabel,
			Kind:        "textarea",
			Value:       strings.TrimSpace(answers[oeEncodingSettingsKey]),
			Placeholder: "AV1 encoder settings omitted from MediaInfo",
			Help:        "OE requires AV1 encoder settings in the description when MediaInfo does not include them.",
			Required:    true,
		})
	}
	if oeRequiresSourceNotes(meta.Tag) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         oeSourceNotesKey,
			Label:       oeSourceNotesLabel,
			Kind:        "textarea",
			Value:       strings.TrimSpace(answers[oeSourceNotesKey]),
			Placeholder: "Source and any relevant source notes",
			Help:        "OE requires source information for SM737 releases.",
			Required:    true,
		})
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "OE", Fields: fields}
}

// inputReadiness reports missing OE description evidence before upload work.
func inputReadiness(meta api.UploadSubject) []api.InputReadinessFieldOutcome {
	schema := inputSchema(meta)
	if schema == nil {
		return nil
	}
	fields := make([]api.InputReadinessFieldOutcome, 0, len(schema.Fields))
	for _, field := range schema.Fields {
		outcome := api.InputReadinessFieldOutcome{
			Key:         "tracker_input." + field.Key,
			Status:      api.InputReadinessFieldReady,
			Disposition: api.RuleDispositionStrict,
		}
		if field.Value == "" {
			outcome.Status = api.InputReadinessFieldMissing
			outcome.Message = field.Help
		}
		fields = append(fields, outcome)
	}
	return fields
}

func oeQuestionnaireAnswers(meta api.UploadSubject) map[string]string {
	return meta.TrackerQuestionnaireAnswers["OE"]
}

func oeRequiresEncodingSettings(videoCodec string, releaseType string, hasEncodeSettings bool) bool {
	if hasEncodeSettings || normalizeCodec(videoCodec) != "AV1" {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(releaseType)) {
	case "ENCODE", "WEBRIP", "DVDRIP":
		return true
	default:
		return false
	}
}

func oeRequiresSourceNotes(tag string) bool {
	return strings.EqualFold(trackers.NormalizeTrackerReleaseGroup(tag), "SM737")
}
