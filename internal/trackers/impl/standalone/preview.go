// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package standalone owns shared lifecycle and definition behavior for tracker
// implementations that do not belong to a protocol family.
package standalone

import (
	"maps"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// PreviewSpec contains tracker-local facts projected into an upload preview.
type PreviewSpec struct {
	Tracker          string
	ReadyMessage     string
	BlockedReason    string
	ReleaseName      string
	DescriptionGroup string
	Description      string
	Endpoint         string
	Payload          map[string]string
	Files            []api.TrackerDryRunFile
	Questionnaire    *api.TrackerQuestionnaire
	DebugSections    []api.TrackerDryRunDebugSection
	ImageHost        api.ImageHostFeedback
}

// BuildPreview returns a defensively copied ready or blocked preview from
// tracker-local prepared facts.
func BuildPreview(spec PreviewSpec) api.TrackerDryRunEntry {
	status := "ready"
	message := strings.TrimSpace(spec.ReadyMessage)
	if message == "" {
		message = "dry-run payload generated"
	}
	if reason := strings.TrimSpace(spec.BlockedReason); reason != "" {
		status = "blocked"
		message = reason
	}
	entry := api.TrackerDryRunEntry{
		Tracker:          strings.ToUpper(strings.TrimSpace(spec.Tracker)),
		Status:           status,
		Message:          message,
		ReleaseName:      strings.TrimSpace(spec.ReleaseName),
		DescriptionGroup: strings.ToLower(strings.TrimSpace(spec.DescriptionGroup)),
		Description:      spec.Description,
		Endpoint:         strings.TrimSpace(spec.Endpoint),
		Payload:          maps.Clone(spec.Payload),
		Files:            append([]api.TrackerDryRunFile(nil), spec.Files...),
		DebugSections:    append([]api.TrackerDryRunDebugSection(nil), spec.DebugSections...),
		ImageHost:        spec.ImageHost,
	}
	for idx := range entry.DebugSections {
		entry.DebugSections[idx].Payload = maps.Clone(entry.DebugSections[idx].Payload)
		entry.DebugSections[idx].Files = append([]api.TrackerDryRunFile(nil), entry.DebugSections[idx].Files...)
	}
	if spec.Questionnaire != nil {
		questionnaire := *spec.Questionnaire
		questionnaire.Fields = append([]api.TrackerQuestionnaireField(nil), questionnaire.Fields...)
		for idx := range questionnaire.Fields {
			questionnaire.Fields[idx].Options = append([]string(nil), questionnaire.Fields[idx].Options...)
		}
		entry.Questionnaire = &questionnaire
	}
	entry.ImageHost.AllowedHosts = append([]string(nil), spec.ImageHost.AllowedHosts...)
	entry.ImageHost.Warnings = append([]api.ImageHostWarning(nil), spec.ImageHost.Warnings...)
	return entry
}
