// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// validationPolicy requires a supported HD resolution as a strict block and
// reports a missing IMDb identifier as waivable.
func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "standalone-hds-constructibility-v1", Check: checkRules}
}

func checkRules(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 2)
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if !supportsHDSResolution(resolution) {
		failures = append(failures, trackers.NewRuleFailure(
			"min_resolution",
			"resolution must be at least 720p",
			api.RuleDispositionStrict,
		))
	}
	hasIMDb := meta.Identity.IMDBID > 0
	if !hasIMDb && meta.ProviderMetadata.IMDB != nil {
		hasIMDb = meta.ProviderMetadata.IMDB.IMDBID > 0 || strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL) != ""
	}
	if !hasIMDb {
		failures = append(failures, trackers.NewRuleFailure("require_imdb", "missing IMDb ID", api.RuleDispositionWaivable))
	}
	return failures, nil
}
