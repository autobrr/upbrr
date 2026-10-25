// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// importedReferences keeps accepted source URLs separate from inspected files.
// The caller supplies the operation-local, host-policy-filtered receipt.
func importedReferences(evidence *api.ImportedScreenshotEvidence) []api.ScreenshotReference {
	if evidence == nil {
		return nil
	}
	seen := make(map[string]bool)
	var references []api.ScreenshotReference
	for _, image := range evidence.References {
		raw := strings.TrimSpace(image.RawURL)
		if !validScreenshotFullURL(raw) || seen[raw] {
			continue
		}
		seen[raw] = true
		id, _ := api.CanonicalWorkflowFingerprint(struct {
			Source api.WorkflowFingerprint
			URL    string
		}{evidence.DescriptionFingerprint, raw})
		references = append(references, api.ScreenshotReference{ID: id, RawURL: raw})
	}
	return references
}

// withImportedReferenceFacts adds unknown-format references without fabricating
// local screenshots or downgrading an inspected image with the same full URL.
func withImportedReferenceFacts(subject api.TrackerValidationSubject) api.TrackerValidationSubject {
	additional := importedReferences(subject.ImportedScreenshotEvidence)
	if len(additional) == 0 {
		return subject
	}
	known := make(map[string]bool)
	for _, image := range subject.AssetFacts.ScreenshotReferences {
		known[strings.TrimSpace(image.RawURL)] = true
	}
	subject.AssetFacts.ScreenshotReferences = slices.Clone(subject.AssetFacts.ScreenshotReferences)
	added := 0
	for _, image := range additional {
		if known[image.RawURL] {
			continue
		}
		known[image.RawURL] = true
		added++
		subject.AssetFacts.ScreenshotReferences = append(subject.AssetFacts.ScreenshotReferences, image)
	}
	if added == 0 {
		return subject
	}
	images := &subject.AssetFacts.Screenshots
	images.Count += added
	images.Ready = true
	images.Status = api.MetadataEvidenceStatusComplete
	hosted := &subject.AssetFacts.HostedScreenshots
	hosted.Count += added
	hosted.Ready = true
	hosted.Status = api.MetadataEvidenceStatusComplete
	subject.AssetFacts.ScreenshotFingerprint, _ = api.CanonicalWorkflowFingerprint(struct {
		Selected api.WorkflowFingerprint
		Imported *api.ImportedScreenshotEvidence
	}{subject.AssetFacts.ScreenshotFingerprint, subject.ImportedScreenshotEvidence})
	return subject
}
