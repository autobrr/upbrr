// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func discReviewFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if !trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	guidance := trackers.NewEvidenceRuleFailure(
		"guidance_lst_disc_source",
		"Check retail provenance, original menus/extras, physical-disc integrity, protection/region restrictions and modification history against the exact source. Disc inventory alone does not establish those facts or same-disc availability; multi-disc uploads should preserve a cohesive retail set without splitting physical discs.",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusPartial,
	)
	failures := []api.RuleFailure{guidance}

	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) && slices.Contains(subject.PackageFacts.Extensions, ".iso") &&
		(!strings.EqualFold(subject.Is3D, "3D") || !strings.EqualFold(subject.DiscType, "BDMV")) {
		failure := trackers.NewEvidenceRuleFailure(
			"lst_disc_iso",
			"ISO requires established 3D Blu-ray evidence",
			api.RuleDispositionStrict,
			api.MetadataEvidenceStatusPartial,
		)
		failure.DebugBypass = true
		failures = append(failures, failure)
	}
	return failures
}
