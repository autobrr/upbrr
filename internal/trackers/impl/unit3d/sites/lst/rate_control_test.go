// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
	"slices"
	"strings"
	"testing"
)

func TestLSTRateControlUsesEachFinalizedResource(t *testing.T) {
	subject := reviewedLSTEncode(t)
	subject.MediaFileFacts = api.MediaFileFacts{Files: []api.MediaFileFact{
		{
			ResourceID:          "first",
			Container:           "mkv",
			VideoEncoder:        "x264",
			Resolution:          "1080p",
			RateControl:         api.VideoRateControlCRF,
			EncodeSettingsKnown: true,
			HasEncodeSettings:   true,
		},
		{
			ResourceID:          "second",
			Container:           "mkv",
			VideoEncoder:        "x264",
			Resolution:          "1080p",
			RateControl:         api.VideoRateControlCBR,
			EncodeSettingsKnown: true,
			HasEncodeSettings:   true,
		},
	}}
	failures := sourceValidationFailures(subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "lst_resource_encode_rate_control" && f.Disposition == api.RuleDispositionWaivable && strings.Contains(f.Reason, "second") && strings.Contains(f.Reason, "cbr")
	}) {
		t.Fatalf("missing direct rate-control warning: %+v", failures)
	}
	before, err := trackers.WaivableRuleFailureFingerprint("LST", failures)
	if err != nil {
		t.Fatal(err)
	}
	subject.MediaFileFacts.Files[1].RateControl = api.VideoRateControlABR
	after, err := trackers.WaivableRuleFailureFingerprint("LST", sourceValidationFailures(subject))
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("changed measured rate control retained warning authority")
	}
	for _, mode := range []api.VideoRateControl{"", api.VideoRateControlCRF, api.VideoRateControlTwoPassVBR, api.VideoRateControlMultiPassVBR} {
		subject.MediaFileFacts.Files[1].RateControl = mode
		for _, f := range sourceValidationFailures(subject) {
			if f.Rule == "lst_resource_encode_rate_control" {
				t.Fatalf("unknown/accepted mode %q rejected: %+v", mode, f)
			}
		}
	}
	subject.MediaFileFacts.Files[1].RateControl = api.VideoRateControlCBR
	subject.MediaFileFacts.TechnicalStatus = api.MetadataEvidenceStatusContradictory
	for _, f := range sourceValidationFailures(subject) {
		if f.Rule == "lst_resource_encode_rate_control" {
			t.Fatalf("contradictory evidence rejected: %+v", f)
		}
	}
}
