// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func checkTVEvidenceRules(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 8)
	failures = append(failures, trackers.ValidatePackageExtensions(
		subject.PackageFacts,
		trackers.PackageExtensionPolicy{
			Evidence:      tvEvidencePolicy("ant_tv_archive"),
			BlockArchives: true,
		},
	)...)
	if !trackers.IsDiscType(subject.DiscType) {
		failures = append(failures, trackers.ValidateMediaOnlyPackage(
			subject.PackageFacts,
			tvEvidencePolicy("ant_tv_media_only"),
		)...)
	}
	if !subject.TVPack && !subject.MultipleEpisodes {
		failures = append(failures, trackers.ValidateSingleFileFolder(
			subject.PackageFacts,
			tvEvidencePolicy("ant_tv_episode_layout"),
		)...)
	}
	failures = append(failures, trackers.ValidateMultiSeasonPackage(
		subject.PackageFacts,
		tvEvidencePolicy("ant_tv_multi_season"),
	)...)

	return failures, nil
}

func tvEvidencePolicy(rule string) trackers.EvidencePredicatePolicy {
	return trackers.EvidencePredicatePolicy{
		Rule:                       rule,
		ViolationDisposition:       api.RuleDispositionStrict,
		MissingEvidenceDisposition: api.RuleDispositionAdvisory,
	}
}
