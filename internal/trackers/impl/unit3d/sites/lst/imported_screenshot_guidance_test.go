// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestImportedScreenshotGuidanceUsesActualFormatCounts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		inspected   int
		lossless    int
		rule        string
		disposition api.RuleDisposition
		counts      string
	}{
		{
			name:      "three lossless among eight suffice",
			inspected: 8,
			lossless:  3,
		},
		{
			name:      "three lossless with unknown extras suffice",
			inspected: 3,
			lossless:  3,
		},
		{
			name:        "partial inspection remains passive",
			inspected:   7,
			lossless:    2,
			rule:        "guidance_lst_screenshot_format",
			disposition: api.RuleDispositionAdvisory,
			counts:      "5 confirmed JPEG, 1 format unknown",
		},
		{
			name:        "fully inspected shortfall is waivable",
			inspected:   8,
			lossless:    2,
			rule:        "lst_screenshot_format",
			disposition: api.RuleDispositionWaivable,
			counts:      "6 confirmed JPEG, 0 format unknown",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject := lstValidationSubject()
			subject.AssetFacts.Screenshots.Count = 8
			subject.AssetFacts.Screenshots.InspectedFormatCount = tc.inspected
			subject.AssetFacts.Screenshots.LosslessCount = tc.lossless
			failures := assetReviewFailures(subject)
			if tc.rule == "" {
				if len(failures) != 0 {
					t.Fatalf("sufficient lossless images generated warnings: %+v", failures)
				}
				return
			}
			if len(failures) != 1 || failures[0].Rule != tc.rule || failures[0].Disposition != tc.disposition {
				t.Fatalf("unexpected screenshot guidance: %+v", failures)
			}
			if !strings.Contains(failures[0].Reason, "2 of 8") || !strings.Contains(failures[0].Reason, tc.counts) {
				t.Fatalf("guidance does not explain actual counts: %s", failures[0].Reason)
			}
			if failures[0].QuestionnaireInput || strings.Contains(failures[0].Reason, "replace the remaining") {
				t.Fatalf("guidance forces replacement or reassurance: %+v", failures[0])
			}
		})
	}
}

func TestFinalScreenshotShortfallUsesBoundWarning(t *testing.T) {
	meta := api.UploadSubject{}
	description := lstDescriptionScreenshots(&meta)
	meta.DescriptionOverride = strings.Join(strings.Split(description, "\n")[:2], "\n")
	meta.DescriptionGroupsFinal = true
	failures := assetReviewFailures(api.NewTrackerValidationSubject(meta, "LST"))
	requireLSTValidationFailure(t, failures, "lst_screenshot_description", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	fingerprint, err := trackers.WaivableRuleFailureFingerprint("LST", failures)
	if err != nil {
		t.Fatal(err)
	}
	projection := api.TrackerReleaseProjection{WaivableRuleFingerprint: fingerprint, RuleAuthorizationFingerprint: fingerprint}
	if failure, err := trackers.FirstBlockingRuleFailure("LST", failures, api.WorkflowExecutionModeNormal, &projection); err != nil || failure != nil {
		t.Fatalf("exact warning acknowledgement did not authorize the deficiency: failure=%+v, err=%v", failure, err)
	}
	if err := validateFinalDescription(meta, meta.DescriptionOverride, "MediaInfo", ""); err != nil {
		t.Fatalf("authorized description warning hit a second strict gate: %v", err)
	}
	meta.DescriptionOverride += "\nEdited release note"
	changed := assetReviewFailures(api.NewTrackerValidationSubject(meta, "LST"))
	if failure, err := trackers.FirstBlockingRuleFailure("LST", changed, api.WorkflowExecutionModeNormal, &projection); err != nil || failure == nil {
		t.Fatalf("changed description reused old warning acknowledgement: failure=%+v, err=%v", failure, err)
	}
	meta.DescriptionGroupsFinal = false
	for _, failure := range assetReviewFailures(api.NewTrackerValidationSubject(meta, "LST")) {
		if failure.Rule == "lst_screenshot_description" {
			t.Fatalf("pending description emitted final-content warning: %+v", failure)
		}
	}
}

func TestDescriptionImageCountDeduplicatesFullURLAliases(t *testing.T) {
	meta := api.UploadSubject{}
	lstDescriptionScreenshots(&meta)
	for i := range meta.ExactMedia.ScreenshotUploads {
		meta.ExactMedia.ScreenshotUploads[i].RawURL = meta.ExactMedia.ScreenshotUploads[0].RawURL
	}
	description := "[img]" + meta.ExactMedia.ScreenshotUploads[0].RawURL + "[/img]"
	if got := countPreparedDescriptionImages(meta, description); got != 1 {
		t.Fatalf("one full image counted as %d distinct screenshots", got)
	}
}

func TestDescriptionImageCountMergesOverlappingHostedAliases(t *testing.T) {
	references := []api.ScreenshotReference{
		{ID: "first", RawURL: "https://images.example.invalid/first.png"},
		{ID: "first", RawURL: "https://images.example.invalid/shared.png"},
		{ID: "second", RawURL: "https://images.example.invalid/shared.png"},
		{ID: "second", RawURL: "https://images.example.invalid/second.png"},
		{ID: "third", RawURL: "https://images.example.invalid/third.png"},
	}
	description := "[img]https://images.example.invalid/first.png[/img]\n" +
		"[img]https://images.example.invalid/second.png[/img]\n" +
		"[img]https://images.example.invalid/third.png[/img]"
	if got := countDescriptionScreenshotReferences(references, description); got != 2 {
		t.Fatalf("overlapping aliases counted as %d distinct screenshots", got)
	}
}

func TestFinalScreenshotGuidanceDoesNotTreatUnknownReferencesAsInspected(t *testing.T) {
	subject := lstValidationSubject()
	subject.DescriptionGroupsFinal = true
	subject.DescriptionOverride = "[img]https://images.example.invalid/uninspected.png[/img]"
	failures := assetReviewFailures(subject)
	requireLSTValidationFailure(t, failures, "guidance_lst_screenshot_description", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	for _, failure := range failures {
		if failure.QuestionnaireInput || api.IsBlockingRuleFailure(failure) {
			t.Fatalf("unknown reference evidence blocks or asks for reassurance: %+v", failure)
		}
	}
}

func TestFinalScreenshotGuidanceKeepsMissingFullImageURLsPassive(t *testing.T) {
	meta := api.UploadSubject{}
	meta.DescriptionOverride = lstDescriptionScreenshots(&meta)
	meta.DescriptionGroupsFinal = true
	meta.ExactMedia.ScreenshotUploads[0].RawURL = ""
	failures := assetReviewFailures(api.NewTrackerValidationSubject(meta, "LST"))
	requireLSTValidationFailure(t, failures, "guidance_lst_screenshot_description", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	for _, failure := range failures {
		if failure.QuestionnaireInput || api.IsBlockingRuleFailure(failure) {
			t.Fatalf("unknown full-image association became a measured violation: %+v", failure)
		}
	}
}

func TestFinalScreenshotWarningIgnoresRedundantThumbnailOnlyVariants(t *testing.T) {
	for _, raw := range []string{"", "not-a-url"} {
		meta := api.UploadSubject{}
		description := lstDescriptionScreenshots(&meta)
		meta.DescriptionOverride = strings.Join(strings.Split(description, "\n")[:2], "\n")
		meta.DescriptionGroupsFinal = true
		thumbnail := meta.ExactMedia.ScreenshotUploads[0]
		thumbnail.RawURL = raw
		meta.ExactMedia.ScreenshotUploads = append(meta.ExactMedia.ScreenshotUploads, thumbnail)
		failures := assetReviewFailures(api.NewTrackerValidationSubject(meta, "LST"))
		requireLSTValidationFailure(t, failures, "lst_screenshot_description", api.RuleDispositionWaivable, api.MetadataEvidenceStatusComplete)
	}
}

func TestFinalScreenshotGuidanceKeepsUnknownImageIdentityPassive(t *testing.T) {
	for _, id := range []api.WorkflowFingerprint{"", " "} {
		meta := api.UploadSubject{}
		meta.DescriptionOverride = lstDescriptionScreenshots(&meta)
		meta.DescriptionGroupsFinal = true
		subject := api.NewTrackerValidationSubject(meta, "LST")
		subject.AssetFacts.ScreenshotReferences[0].ID = id
		failures := assetReviewFailures(subject)
		requireLSTValidationFailure(t, failures, "guidance_lst_screenshot_description", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	}
}

func TestFinalScreenshotGuidanceKeepsMalformedFullImageURLsPassive(t *testing.T) {
	for _, raw := range []string{"not-a-url", "https://", "ftp://images.example.invalid/screen.png"} {
		t.Run(raw, func(t *testing.T) {
			meta := api.UploadSubject{}
			meta.DescriptionOverride = lstDescriptionScreenshots(&meta)
			meta.DescriptionGroupsFinal = true
			meta.ExactMedia.ScreenshotUploads[0].RawURL = raw
			failures := assetReviewFailures(api.NewTrackerValidationSubject(meta, "LST"))
			requireLSTValidationFailure(t, failures, "guidance_lst_screenshot_description", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
		})
	}
}
