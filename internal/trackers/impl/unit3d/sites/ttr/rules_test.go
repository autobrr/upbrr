// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ttr

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSubtitleRuleUsesResolvedTrackLanguages(t *testing.T) {
	t.Parallel()

	resolved := api.TrackerValidationSubject{SubtitleLanguages: []string{"Spanish"}, Release: api.ReleaseInfo{Language: []string{"English"}}}
	if failures, err := checkSubtitleOnly(context.Background(), resolved, api.NopLogger{}); err != nil || len(failures) != 0 {
		t.Fatalf("resolved Spanish track failures = %#v, err = %v", failures, err)
	}
	rawOnly := api.TrackerValidationSubject{Release: api.ReleaseInfo{Language: []string{"Spanish"}}}
	if failures, err := checkSubtitleOnly(context.Background(), rawOnly, api.NopLogger{}); err != nil || len(failures) != 1 {
		t.Fatalf("raw-only Spanish track failures = %#v, err = %v", failures, err)
	}
}
