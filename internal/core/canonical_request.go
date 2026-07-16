// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

func uploadReviewInputFromRequest(request api.Request, ref api.ReleaseRef) api.UploadReviewInput {
	ignoreRules := append([]string(nil), request.IgnoreTrackerRuleFailuresFor...)
	if request.IgnoreTrackerRuleFailures {
		ignoreRules = append([]string(nil), request.Trackers...)
	}
	return api.UploadReviewInput{
		Release:                ref,
		Trackers:               append([]string(nil), request.Trackers...),
		IgnoreDupesFor:         append([]string(nil), request.IgnoreDupesFor...),
		IgnoreRuleFailuresFor:  ignoreRules,
		SkipDuplicateCheck:     request.SkipDupeCheck,
		SkipDuplicateAsActual:  request.SkipDupeAsActual,
		DoubleDuplicateCheck:   request.DoubleDupeCheck,
		QuestionnaireAnswers:   cloneOperationQuestionnaireAnswers(request.TrackerQuestionnaireAnswers),
		TrackerIDOverrides:     cloneStringMap(request.TrackerIDOverrides),
		DescriptionGroups:      api.CloneDescriptionBuilderGroups(request.DescriptionGroups),
		TrackerConfigOverrides: request.TrackerConfigOverrides,
		TrackerSiteOverrides:   request.TrackerSiteOverrides,
		ClientOverrides:        request.ClientOverrides,
		ImageHostOverrides:     request.ImageHostOverrides,
		ScreenshotOverrides:    request.ScreenshotOverrides,
		TorrentOverrides:       request.TorrentOverrides,
		Options:                request.Options,
	}
}

// duplicateCheckInputFromRequest isolates operation choices from broad legacy
// request state and binds them to one accepted prepared generation.
func duplicateCheckInputFromRequest(request api.Request, ref api.ReleaseRef) api.DuplicateCheckInput {
	return api.DuplicateCheckInput{
		Release:      ref,
		Trackers:     append([]string(nil), request.Trackers...),
		Interaction:  request.Options.InteractionMode,
		IgnoreFor:    append([]string(nil), request.IgnoreDupesFor...),
		Skip:         request.SkipDupeCheck,
		SkipAsActual: request.SkipDupeAsActual,
		DoubleCheck:  request.DoubleDupeCheck,
		TrackerIDs:   cloneStringMap(request.TrackerIDOverrides),
		ClientSearch: api.ClientSearchPolicy{
			Skip:   request.Options.SkipAutoTorrent,
			Client: cloneStringPointer(request.ClientOverrides.Client),
		},
		ForceRecheck: cloneBoolPointer(request.ClientOverrides.ForceRecheck),
		Debug:        request.Options.Debug,
	}
}

// cloneStringPointer detaches an optional string without normalization.
func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// cloneBoolPointer detaches an optional boolean.
func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func trackerDryRunInputFromRequest(request api.Request, ref api.ReleaseRef) api.TrackerDryRunInput {
	ignoreRules := append([]string(nil), request.IgnoreTrackerRuleFailuresFor...)
	if request.IgnoreTrackerRuleFailures {
		ignoreRules = append([]string(nil), request.Trackers...)
	}
	return api.TrackerDryRunInput{
		Release:                ref,
		Trackers:               append([]string(nil), request.Trackers...),
		IgnoreDupesFor:         append([]string(nil), request.IgnoreDupesFor...),
		IgnoreRuleFailuresFor:  ignoreRules,
		QuestionnaireAnswers:   cloneOperationQuestionnaireAnswers(request.TrackerQuestionnaireAnswers),
		DescriptionGroups:      api.CloneDescriptionBuilderGroups(request.DescriptionGroups),
		TrackerConfigOverrides: request.TrackerConfigOverrides,
		TrackerSiteOverrides:   request.TrackerSiteOverrides,
		ImageHostOverrides:     request.ImageHostOverrides,
		TorrentOverrides:       request.TorrentOverrides,
		Options:                request.Options,
	}
}

func (c *Core) canonicalPreparationEnabled() bool {
	return c != nil && c.preparedFacts != nil
}

func (c *Core) prepareRequestRef(ctx context.Context, request api.Request, intent api.PreparationIntent) (api.ReleaseRef, error) {
	if !c.canonicalPreparationEnabled() {
		return api.ReleaseRef{}, errors.New("core: canonical preparation is not configured")
	}
	input, err := api.MapPreparationRequest(request, intent)
	if err != nil {
		return api.ReleaseRef{}, fmt.Errorf("core: map preparation request: %w", err)
	}
	prepared, err := c.preparedFacts.Prepare(ctx, input)
	if err != nil {
		return api.ReleaseRef{}, fmt.Errorf("core: prepare request release: %w", err)
	}
	return api.ReleaseRef{SourcePath: prepared.Release.Source.SourcePath, Generation: prepared.Release.Generation}, nil
}
