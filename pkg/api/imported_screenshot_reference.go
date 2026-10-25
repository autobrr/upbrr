// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"slices"
	"strings"
)

// ImportedScreenshotEvidencePolicyVersion invalidates receipts when their
// acceptance boundary changes. Legacy image metadata is not a receipt.
const ImportedScreenshotEvidencePolicyVersion = "imported-screenshot-reference-v1"

// ImportedScreenshotReference records a current policy-accepted image URL whose
// bounded byte inspection or local retention was unavailable. It does not establish a valid image,
// complete-body size, local artifact, format, dimensions, or preservation.
// Preview and page URLs cannot inherit the full URL's acceptance.
type ImportedScreenshotReference struct {
	RawURL string
}

// ImportedScreenshotEvidence captures one selected source tracker's accepted
// references. The containing SourceScopedMetadata owns source and generation;
// the source and original-description fingerprints bind the collected receipt.
type ImportedScreenshotEvidence struct {
	Tracker                string
	TrackerID              string
	InfoHash               string
	SourceFingerprint      string
	PolicyVersion          string
	DescriptionFingerprint WorkflowFingerprint
	References             []ImportedScreenshotReference
}

// Clone detaches the reference slice without creating authority for absent evidence.
func (e *ImportedScreenshotEvidence) Clone() *ImportedScreenshotEvidence {
	if e == nil {
		return nil
	}
	cloned := *e
	cloned.References = slices.Clone(e.References)
	return &cloned
}

// CurrentImportedScreenshotEvidence returns detached receipts only from the
// exact prepared source and generation. Source-level caches and legacy blank
// scopes cannot establish reference-only screenshot authority.
func (s UploadSubject) CurrentImportedScreenshotEvidence() *ImportedScreenshotEvidence {
	m := s.ProviderMetadata
	e := m.ImportedScreenshotEvidence
	if e == nil || !s.MediaBinding.Valid() || s.Identity.Generation == 0 || m.Generation != s.Identity.Generation ||
		s.MediaBinding.PreparedGeneration != m.Generation || strings.TrimSpace(s.SourcePath) == "" ||
		m.SourcePath != s.SourcePath || s.Identity.SourcePath != s.SourcePath || s.MediaBinding.SourcePath != s.SourcePath ||
		e.SourceFingerprint == "" || e.SourceFingerprint != s.Identity.Resolution.SourceFingerprint ||
		e.PolicyVersion != ImportedScreenshotEvidencePolicyVersion || e.DescriptionFingerprint == "" ||
		strings.TrimSpace(e.Tracker) == "" || strings.TrimSpace(e.TrackerID) == "" && strings.TrimSpace(e.InfoHash) == "" || len(e.References) == 0 {
		return nil
	}
	for _, ref := range e.References {
		if strings.TrimSpace(ref.RawURL) == "" || ref.RawURL != strings.TrimSpace(ref.RawURL) {
			return nil
		}
	}
	return e.Clone()
}
