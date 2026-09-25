// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

// PrepareReusableDescriptions returns fully successful public output for the
// workflow repository to commit atomically with its owning snapshot.
func (b workflowDescriptionBuilder) PrepareReusableDescriptions(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	media api.MediaArtifactSet,
	privateMedia any,
	instructions api.DescriptionInstructions,
	snapshot api.DescriptionSet,
) (*api.ReusableDescriptionRecord, error) {
	if b.reuse == nil || !workflowDescriptionSnapshotFullyCompleted(projections, snapshot) {
		return nil, nil
	}
	sourcePath, compatibilityFingerprint, available, err := b.reusableDescriptionInputs(
		ctx, release, projections, media, privateMedia, instructions,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow reusable descriptions: fingerprint: %w", err)
	}
	if !available {
		return nil, nil
	}
	reusable := api.ReusableDescription{
		CompatibilityFingerprint: compatibilityFingerprint,
		Descriptions:             cloneWorkflowRenderedDescriptions(snapshot.Descriptions),
		TrackerResults:           slices.Clone(snapshot.TrackerResults),
		Overrides:                slices.Clone(instructions.Overrides),
	}
	if !reusable.Valid() {
		return nil, nil
	}
	return &api.ReusableDescriptionRecord{SourcePath: sourcePath, Description: reusable}, nil
}

// RestoreCompatibleDescriptions materializes a fresh description snapshot
// only from current dependencies. A current explicit override must rebuild so
// its text cannot be replaced by cached rendered output.
func (b workflowDescriptionBuilder) RestoreCompatibleDescriptions(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	media api.MediaArtifactSet,
	privateMedia any,
	instructions api.DescriptionInstructions,
) (api.DescriptionSet, api.DescriptionInstructions, error) {
	if _, hasAudio := privateMedia.(releaseworkflow.DescriptionResources); hasAudio {
		return api.DescriptionSet{}, instructions, nil
	}
	if err := ctx.Err(); err != nil {
		return api.DescriptionSet{}, instructions, fmt.Errorf("workflow reusable descriptions: %w", err)
	}
	if b.reuse == nil || len(instructions.Overrides) > 0 {
		return api.DescriptionSet{}, instructions, nil
	}
	sourcePath, compatibilityFingerprint, available, err := b.reusableDescriptionInputs(
		ctx, release, projections, media, privateMedia, instructions,
	)
	if err != nil {
		return api.DescriptionSet{}, instructions, fmt.Errorf("workflow reusable descriptions: fingerprint: %w", err)
	}
	if !available {
		return api.DescriptionSet{}, instructions, nil
	}
	reusable, found, err := b.reuse.LoadReusableDescription(ctx, sourcePath)
	if err != nil {
		return api.DescriptionSet{}, instructions, fmt.Errorf("workflow reusable descriptions: load: %w", err)
	}
	if !found || !reusable.Valid() || reusable.CompatibilityFingerprint != compatibilityFingerprint {
		return api.DescriptionSet{}, instructions, nil
	}

	restoredInstructions := instructions
	restoredInstructions.Overrides = slices.Clone(reusable.Overrides)
	inputFingerprint, templateFingerprint, err := b.Fingerprints(
		ctx, release, projections, media, privateMedia, restoredInstructions,
	)
	if err != nil {
		return api.DescriptionSet{}, instructions, fmt.Errorf("workflow reusable descriptions: current fingerprints: %w", err)
	}
	snapshot := api.DescriptionSet{
		InputFingerprint:    inputFingerprint,
		TemplateFingerprint: templateFingerprint,
		Descriptions:        cloneWorkflowRenderedDescriptions(reusable.Descriptions),
		TrackerResults:      slices.Clone(reusable.TrackerResults),
		Status:              api.StageStatusCompleted,
	}
	if !workflowDescriptionSnapshotFullyCompleted(projections, snapshot) {
		return api.DescriptionSet{}, instructions, nil
	}
	return snapshot, restoredInstructions, nil
}

func (b workflowDescriptionBuilder) reusableDescriptionInputs(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	media api.MediaArtifactSet,
	privateMedia any,
	instructions api.DescriptionInstructions,
) (string, api.WorkflowFingerprint, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", "", false, fmt.Errorf("workflow reusable descriptions: %w", err)
	}
	compatibilityInstructions := instructions
	compatibilityInstructions.Overrides = nil
	subject, err := b.resolveSubject(ctx, release, projections, compatibilityInstructions)
	if err != nil {
		return "", "", false, err
	}
	binding := subject.MediaBinding
	if !binding.Valid() || !binding.CompatibilityKey.Valid() {
		return "", "", false, nil
	}
	exactMedia, err := resolveWorkflowExactMedia(privateMedia, media)
	if err != nil {
		return "", "", false, err
	}
	descriptionSubject := api.NewDescriptionSubject(subject)
	descriptionSubject.ExactMedia = exactMedia
	fingerprint, available, err := workflowReusableDescriptionFingerprint(
		ctx, b.config, binding.CompatibilityKey, projections, compatibilityInstructions, descriptionSubject,
	)
	if err != nil {
		return "", "", false, err
	}
	return binding.SourcePath, fingerprint, available, nil
}

type workflowReusableDescriptionImage struct {
	ContentSHA256 string
	Image         api.ScreenshotImage
}

type workflowReusableDescriptionDVDMenu struct {
	ContentSHA256 string
	Image         api.DVDMenuCaptureImage
}

type workflowReusableDescriptionHostedImage struct {
	ImageSHA256  string
	Host         string
	UsageScope   string
	AccountScope string
	ImgURL       string
	RawURL       string
	WebURL       string
	SizeBytes    int64
}

type workflowReusableDescriptionMedia struct {
	Screenshots       []workflowReusableDescriptionImage
	DVDMenus          []workflowReusableDescriptionDVDMenu
	ScreenshotUploads []workflowReusableDescriptionHostedImage
	DVDMenuUploads    []workflowReusableDescriptionHostedImage
}

type workflowReusableDescriptionReportResources struct {
	SummarySHA256     string
	ExtSummarySHA256  string
	FullSummarySHA256 string
}

type workflowReusableDescriptionDiscResources struct {
	MediaInfoTextSHA256 string
	Reports             []workflowReusableDescriptionReportResources
}

type workflowReusableDescriptionLocalResources struct {
	MediaInfoTextSHA256 string
	Discs               []workflowReusableDescriptionDiscResources
}

func workflowReusableDescriptionFingerprint(
	ctx context.Context,
	cfg config.Config,
	compatibilityKey api.MediaCompatibilityKey,
	projections api.TrackerReleaseProjectionSet,
	instructions api.DescriptionInstructions,
	subject api.DescriptionSubject,
) (api.WorkflowFingerprint, bool, error) {
	normalizedSubject, localResources, available, err := normalizeReusableDescriptionSubject(ctx, subject)
	if err != nil || !available {
		return "", available, err
	}
	normalizedMedia, available, err := normalizeReusableDescriptionMedia(ctx, subject.ExactMedia)
	if err != nil || !available {
		return "", available, err
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Version          string
		Config           config.Config
		CompatibilityKey api.MediaCompatibilityKey
		Projections      []api.TrackerReleaseProjection
		Instructions     api.DescriptionInstructions
		Subject          api.DescriptionSubject
		LocalResources   workflowReusableDescriptionLocalResources
		ExactMedia       workflowReusableDescriptionMedia
	}{
		Version:          "description-reuse-v1",
		Config:           cfg,
		CompatibilityKey: compatibilityKey,
		Projections:      reusableDescriptionProjections(projections.Projections),
		Instructions:     instructions,
		Subject:          normalizedSubject,
		LocalResources:   localResources,
		ExactMedia:       normalizedMedia,
	})
	if err != nil {
		return "", false, fmt.Errorf("workflow reusable descriptions: canonical fingerprint: %w", err)
	}
	return fingerprint, true, nil
}

func reusableDescriptionProjections(projections []api.TrackerReleaseProjection) []api.TrackerReleaseProjection {
	targets := workflowDescriptionTargets(projections)
	result := slices.Clone(targets)
	for index := range result {
		projection := &result[index]
		projection.DuplicatePolicyFingerprint = ""
		projection.DuplicateTargetFingerprint = ""
		projection.DuplicateSearchFingerprint = ""
		projection.NamingFingerprint = ""
		projection.WaivableRuleFingerprint = ""
		projection.RuleAuthorizationFingerprint = ""
		projection.InputFingerprint = ""
		projection.CatalogFingerprint = ""
		projection.ConfigFingerprint = ""
		projection.ProjectorFingerprint = ""
		projection.CriteriaFingerprint = ""
		projection.PreparedResourceFingerprint = ""
	}
	return result
}

func normalizeReusableDescriptionSubject(
	ctx context.Context,
	subject api.DescriptionSubject,
) (api.DescriptionSubject, workflowReusableDescriptionLocalResources, bool, error) {
	resources := workflowReusableDescriptionLocalResources{
		Discs: make([]workflowReusableDescriptionDiscResources, 0, len(subject.Discs)),
	}
	var available bool
	var err error
	resources.MediaInfoTextSHA256, available, err = reusableDescriptionFileDigest(ctx, subject.MediaInfoTextPath)
	if err != nil || !available {
		return api.DescriptionSubject{}, workflowReusableDescriptionLocalResources{}, available, err
	}
	subject.MediaInfoTextPath = ""
	subject.MediaBinding.PreparedMediaFingerprint = ""
	subject.MediaBinding.PreparedGeneration = 0
	subject.Identity.Generation = 0
	subject.Identity.ResolvedAt = time.Time{}
	subject.Identity.Resolution = api.IdentityResolutionKey{}
	subject.Identity.Dependencies = api.IdentityDependencySet{}
	subject.ProviderMetadata.Generation = 0
	subject.ProviderMetadata.UpdatedAt = time.Time{}
	for trackerIndex := range subject.TrackerData {
		subject.TrackerData[trackerIndex].UpdatedAt = time.Time{}
	}
	for discIndex := range subject.Discs {
		disc := &subject.Discs[discIndex]
		discResources := workflowReusableDescriptionDiscResources{
			Reports: make([]workflowReusableDescriptionReportResources, 0, len(disc.Reports)),
		}
		discResources.MediaInfoTextSHA256, available, err = reusableDescriptionFileDigest(ctx, disc.MediaInfoTextPath)
		if err != nil || !available {
			return api.DescriptionSubject{}, workflowReusableDescriptionLocalResources{}, available, err
		}
		disc.Root = ""
		disc.VideoPath = ""
		disc.FileList = nil
		disc.MediaInfoJSONPath = ""
		disc.MediaInfoTextPath = ""
		disc.DVDIFOPath = ""
		disc.DVDVOBPath = ""
		for reportIndex := range disc.Reports {
			report := &disc.Reports[reportIndex]
			resourcesForReport := workflowReusableDescriptionReportResources{}
			resourcesForReport.SummarySHA256, available, err = reusableDescriptionFileDigest(ctx, report.SummaryPath)
			if err != nil || !available {
				return api.DescriptionSubject{}, workflowReusableDescriptionLocalResources{}, available, err
			}
			resourcesForReport.ExtSummarySHA256, available, err = reusableDescriptionFileDigest(ctx, report.ExtSummaryPath)
			if err != nil || !available {
				return api.DescriptionSubject{}, workflowReusableDescriptionLocalResources{}, available, err
			}
			resourcesForReport.FullSummarySHA256, available, err = reusableDescriptionFileDigest(ctx, report.FullSummaryPath)
			if err != nil || !available {
				return api.DescriptionSubject{}, workflowReusableDescriptionLocalResources{}, available, err
			}
			report.SummaryPath = ""
			report.ExtSummaryPath = ""
			report.FullSummaryPath = ""
			discResources.Reports = append(discResources.Reports, resourcesForReport)
		}
		resources.Discs = append(resources.Discs, discResources)
	}
	subject.ExactMedia = nil
	return subject, resources, true, nil
}

func normalizeReusableDescriptionMedia(
	ctx context.Context,
	exact *api.ExactMediaAssets,
) (workflowReusableDescriptionMedia, bool, error) {
	if exact == nil {
		return workflowReusableDescriptionMedia{}, true, nil
	}
	normalized := workflowReusableDescriptionMedia{
		Screenshots: make([]workflowReusableDescriptionImage, 0, len(exact.Screenshots)),
		DVDMenus:    make([]workflowReusableDescriptionDVDMenu, 0, len(exact.DVDMenus)),
	}
	digestsByPath := make(map[string]string, len(exact.Screenshots)+len(exact.DVDMenus))
	for _, image := range exact.Screenshots {
		if strings.TrimSpace(image.Path) == "" {
			return workflowReusableDescriptionMedia{}, false, nil
		}
		digest, available, err := reusableDescriptionFileDigest(ctx, image.Path)
		if err != nil || !available {
			return workflowReusableDescriptionMedia{}, available, err
		}
		digestsByPath[image.Path] = digest
		image.Path = ""
		image.UploadedAt = time.Time{}
		normalized.Screenshots = append(normalized.Screenshots, workflowReusableDescriptionImage{
			ContentSHA256: digest,
			Image:         image,
		})
	}
	for _, menu := range exact.DVDMenus {
		if strings.TrimSpace(menu.Path) == "" {
			return workflowReusableDescriptionMedia{}, false, nil
		}
		digest, available, err := reusableDescriptionFileDigest(ctx, menu.Path)
		if err != nil || !available {
			return workflowReusableDescriptionMedia{}, available, err
		}
		digestsByPath[menu.Path] = digest
		menu.Path = ""
		menu.UploadedAt = time.Time{}
		normalized.DVDMenus = append(normalized.DVDMenus, workflowReusableDescriptionDVDMenu{
			ContentSHA256: digest,
			Image:         menu,
		})
	}
	var err error
	normalized.ScreenshotUploads, err = normalizeReusableDescriptionHostedImages(exact.ScreenshotUploads, digestsByPath)
	if err != nil {
		return workflowReusableDescriptionMedia{}, false, err
	}
	normalized.DVDMenuUploads, err = normalizeReusableDescriptionHostedImages(exact.DVDMenuUploads, digestsByPath)
	if err != nil {
		return workflowReusableDescriptionMedia{}, false, err
	}
	return normalized, true, nil
}

func normalizeReusableDescriptionHostedImages(
	links []api.UploadedImageLink,
	digestsByPath map[string]string,
) ([]workflowReusableDescriptionHostedImage, error) {
	if links == nil {
		return nil, nil
	}
	result := make([]workflowReusableDescriptionHostedImage, 0, len(links))
	for _, link := range links {
		digest, exists := digestsByPath[link.ImagePath]
		if !exists {
			return nil, errors.New("workflow reusable descriptions: hosted image source is unavailable")
		}
		result = append(result, workflowReusableDescriptionHostedImage{
			ImageSHA256:  digest,
			Host:         link.Host,
			UsageScope:   link.UsageScope,
			AccountScope: link.AccountScope,
			ImgURL:       link.ImgURL,
			RawURL:       link.RawURL,
			WebURL:       link.WebURL,
			SizeBytes:    link.SizeBytes,
		})
	}
	return result, nil
}

func reusableDescriptionFileDigest(ctx context.Context, pathValue string) (string, bool, error) {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return "", true, nil
	}
	digest, err := workflowMediaContentSHA256(ctx, pathValue)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("workflow reusable descriptions: hash resource: %w", err)
	}
	return digest, true, nil
}

func workflowDescriptionSnapshotFullyCompleted(
	projections api.TrackerReleaseProjectionSet,
	snapshot api.DescriptionSet,
) bool {
	targets := workflowDescriptionTargets(projections.Projections)
	if snapshot.Status != api.StageStatusCompleted || len(targets) == 0 || len(snapshot.Descriptions) == 0 ||
		len(snapshot.Failures) > 0 || len(snapshot.RequiredActions) > 0 || len(snapshot.TrackerResults) != len(targets) {
		return false
	}
	covered := make(map[api.TrackerID]struct{}, len(targets))
	for _, description := range snapshot.Descriptions {
		baseGroup := strings.TrimSpace(strings.SplitN(description.GroupKey, "|", 2)[0])
		if baseGroup == "" || strings.TrimSpace(description.Source) == "" || strings.TrimSpace(description.Rendered) == "" {
			return false
		}
		for _, trackerID := range description.TrackerIDs {
			if _, exists := covered[trackerID]; exists {
				return false
			}
			matched := false
			for _, projection := range targets {
				if projection.TrackerID == trackerID &&
					(projection.DescriptionGroup == "" || strings.EqualFold(strings.TrimSpace(projection.DescriptionGroup), baseGroup)) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
			covered[trackerID] = struct{}{}
		}
	}
	completed := make(map[api.TrackerID]struct{}, len(snapshot.TrackerResults))
	for _, result := range snapshot.TrackerResults {
		if result.Status != api.StageStatusCompleted {
			return false
		}
		if _, exists := completed[result.TrackerID]; exists {
			return false
		}
		completed[result.TrackerID] = struct{}{}
	}
	for _, projection := range targets {
		if _, covered := covered[projection.TrackerID]; !covered {
			return false
		}
		if _, completed := completed[projection.TrackerID]; !completed {
			return false
		}
	}
	return true
}

func cloneWorkflowRenderedDescriptions(values []api.RenderedDescription) []api.RenderedDescription {
	if values == nil {
		return nil
	}
	cloned := make([]api.RenderedDescription, len(values))
	for index := range values {
		cloned[index] = values[index]
		cloned[index].TrackerIDs = slices.Clone(values[index].TrackerIDs)
	}
	return cloned
}
