// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

type collectionImageHostingService interface {
	UploadCollection(context.Context, api.ImageHostingSubject, api.ImageCollectionInput, bool) (*api.ImageCollection, error)
}

func (b workflowDescriptionBuilder) uploadDescriptionCollection(
	ctx context.Context,
	subject api.UploadSubject,
	trackerNames []string,
	input api.ImageCollectionInput,
) (*api.ImageCollection, error) {
	if len(input.Images) == 0 || len(trackerNames) == 0 {
		return nil, nil
	}
	if b.media == nil {
		return nil, errors.New("collection image hosting is unavailable")
	}
	// Collection URLs are description links, so screenshot host restrictions do not apply.
	service, ok := b.media.images.(collectionImageHostingService)
	if !ok {
		return nil, errors.New("collection image hosting is unavailable")
	}
	result, err := service.UploadCollection(
		ctx,
		api.ImageHostingSubject{MediaBinding: subject.MediaBinding, SourcePath: subject.SourcePath},
		input,
		subject.ExactMedia.ImageHostUploadSkipped,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow collection hosting: %w", err)
	}
	if result != nil {
		subject.ExactMedia.Collections = append(subject.ExactMedia.Collections, *result)
	}
	return result, nil
}

func (b workflowDescriptionBuilder) uploadComparisonCollections(ctx context.Context, subject api.UploadSubject, trackerNames []string) error {
	exact := subject.ExactMedia
	if exact == nil || exact.CollectionHost != "slowpics" {
		return nil
	}
	trackerNames = hdrDescriptionTrackers(subject, trackerNames)
	if len(trackerNames) == 0 {
		return nil
	}
	groups := make(map[string]int)
	for _, image := range exact.Screenshots {
		if image.ComparisonGroup == "" {
			continue
		}
		index, exists := groups[image.ComparisonGroup]
		if !exists {
			index = len(exact.ComparisonSets)
			groups[image.ComparisonGroup] = index
			exact.ComparisonSets = append(exact.ComparisonSets, api.ImageCollectionInput{
				Generation: image.ComparisonGroup,
				Purpose:    api.ScreenshotPurposeComparison,
				Category:   "Comps",
				Label:      image.ComparisonLabel,
			})
		}
		set := &exact.ComparisonSets[index]
		if set.Label != image.ComparisonLabel || image.ComparisonCount < 1 {
			return errors.New("comparison grouping is incomplete; reattach the comparison input")
		}
		set.Images = append(set.Images, image)
		set.Labels = append(set.Labels, image.ComparisonMemberLabel)
	}
	for _, set := range exact.ComparisonSets {
		for _, image := range set.Images {
			if len(set.Images) != image.ComparisonCount {
				return errors.New("comparison set selection is incomplete; select every member or reattach the intended set")
			}
		}
	}
	for _, set := range exact.ComparisonSets {
		if _, err := b.uploadDescriptionCollection(ctx, subject, trackerNames, set); err != nil {
			return fmt.Errorf("workflow collection hosting: %w", err)
		}
	}
	for index := range exact.DescriptionComparisons {
		item := &exact.DescriptionComparisons[index]
		service, ok := b.trackers.(workflowDescriptionComparisonService)
		if !ok {
			return errors.New("description comparison resources are unavailable")
		}
		set, err := service.MaterializeDescriptionComparison(ctx, api.NewDescriptionSubject(subject), *item)
		if err != nil {
			return fmt.Errorf("workflow descriptions: materialize comparison: %w", err)
		}
		item.Collection, err = b.uploadDescriptionCollection(ctx, subject, item.Trackers, set)
		if err != nil {
			return err
		}
	}
	// Grouped comparisons have only set links in collection mode, including explicit skip.
	paths := make(map[string]bool)
	for _, set := range exact.ComparisonSets {
		for _, image := range set.Images {
			paths[image.Path] = true
		}
	}
	exact.Screenshots = slices.DeleteFunc(exact.Screenshots, func(image api.ScreenshotImage) bool { return paths[image.Path] })
	exact.ScreenshotUploads = slices.DeleteFunc(exact.ScreenshotUploads, func(image api.UploadedImageLink) bool { return paths[image.ImagePath] })
	return nil
}

func (b workflowDescriptionBuilder) uploadAnalysisDescriptionImages(ctx context.Context, subject api.UploadSubject, trackerNames []string) error {
	exact := subject.ExactMedia
	if exact == nil {
		return nil
	}
	if exact.CollectionHost != "slowpics" {
		if err := b.uploadAudioDescriptionImages(ctx, subject, trackerNames); err != nil {
			return err
		}
		return b.uploadHDRDescriptionImages(ctx, subject, trackerNames)
	}
	trackerNames = hdrDescriptionTrackers(subject, trackerNames)
	if len(trackerNames) == 0 {
		return nil
	}
	set := api.ImageCollectionInput{
		Purpose:  api.ScreenshotPurposeHDRAnalysis,
		Category: api.ImageCollectionCategoryAnalysis,
		Label:    api.ImageCollectionCategoryAnalysis,
		Columns:  1,
	}
	for _, plot := range exact.HDRPlots {
		set.Images = append(set.Images, plot.Image)
		set.Labels = append(set.Labels, api.CollectionLabel(plot.Label, fmt.Sprintf("HDR plot %d", len(set.Images))))
	}
	if len(set.Images) == 0 {
		set.Purpose = api.ScreenshotPurposeAudioAnalysis
	}
	for _, track := range exact.AudioTracks {
		for index, image := range track.Images {
			label := fmt.Sprintf("Audio track %d plot %d", track.Ordinal, index+1)
			if len(track.ImageLabels) == len(track.Images) {
				plotLabel := api.CollectionLabel(track.ImageLabels[index], fmt.Sprintf("plot %d", index+1))
				label = api.CollectionLabel(fmt.Sprintf("Audio track %d %s", track.Ordinal, plotLabel), label)
			}
			set.Images = append(set.Images, image)
			set.Labels = append(set.Labels, label)
		}
	}
	if len(set.Images) == 0 {
		return nil
	}
	generation, err := api.CanonicalWorkflowFingerprint(struct {
		HDR   *api.HDRAnalysisRef
		Audio *api.AudioAnalysisRef
	}{exact.HDRAnalysis, exact.AudioAnalysis})
	if err != nil {
		return fmt.Errorf("workflow collection hosting: %w", err)
	}
	set.Generation = string(generation)
	if (len(exact.ComparisonSets) > 0 || len(exact.DescriptionComparisons) > 0) && !exact.ImageHostUploadSkipped {
		var fallback api.Logger
		if b.media != nil {
			fallback = b.media.logger
		}
		logging.FromContext(ctx, fallback).Infof("slow.pics: analysis collection state=waiting reason=comparison_spacing delay_ms=5000")
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return fmt.Errorf("workflow collection hosting: %w", ctx.Err())
		case <-timer.C:
		}
	}
	_, err = b.uploadDescriptionCollection(ctx, subject, trackerNames, set)
	return err
}

func collectionDescriptionOutcome(err error, input, template api.WorkflowFingerprint) (api.DescriptionSet, error) {
	var outcome *api.ImageCollectionOutcomeError
	if !errors.As(err, &outcome) || outcome.AttemptID == "" {
		return api.DescriptionSet{}, err
	}
	failure, ok := api.AsOperationFailure(err)
	if !ok {
		return api.DescriptionSet{}, err
	}
	scope := api.ImageCollectionEffectScope(outcome.AttemptID)
	return api.DescriptionSet{
		InputFingerprint:    input,
		TemplateFingerprint: template,
		Status:              api.StageStatusBlocked,
		Failures:            []api.WorkflowFailure{{Failure: failure, Resource: scope}},
		RequiredActions: []api.RequiredAction{
			{
				Kind:          api.RequiredActionReconcileSubmission,
				Status:        api.RequiredActionStatusPending,
				EffectKind:    api.WorkflowExternalEffectImageHosting,
				EffectScopeID: scope,
				Prompt:        "Check slow.pics for this uncertain upload. Confirm only after verifying that no collection was created. This clears the block without uploading; retry description generation separately.",
				Options:       []api.RequiredActionOption{{Value: api.RequiredActionReconcileNotCompleted, Label: "I verified no collection was created"}},
			},
		},
	}, nil
}
