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

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

// PrepareReusableDescriptions provides successful output and verified shared source
// for the workflow repository to commit atomically with its owning snapshot.
func (b workflowDescriptionBuilder) PrepareReusableDescriptions(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	media api.MediaArtifactSet,
	privateMedia any,
	instructions api.DescriptionInstructions,
	snapshot api.DescriptionSet,
) (*api.ReusableDescriptionRecord, error) {
	fullyCompleted := workflowDescriptionSnapshotFullyCompleted(projections, snapshot)
	if b.reuse == nil || (!fullyCompleted && instructions.MainSource == nil) {
		return nil, nil
	}
	inputs, err := b.reusableDescriptionInputs(ctx, release, projections, media, privateMedia, instructions,
		instructions.MainSource == nil || descriptionOutputReuseAllowed(privateMedia))
	if err != nil {
		return nil, fmt.Errorf("workflow reusable descriptions: fingerprint: %w", err)
	}
	var reusable api.ReusableDescription
	if fullyCompleted && inputs.available {
		reusable = api.ReusableDescription{
			RendererVersion:          workflowDescriptionRendererVersion,
			CompatibilityFingerprint: inputs.fingerprint,
			Descriptions:             cloneWorkflowRenderedDescriptions(snapshot.Descriptions),
			TrackerResults:           slices.Clone(snapshot.TrackerResults),
			Overrides:                slices.Clone(instructions.Overrides),
		}
	}
	if instructions.MainSource != nil && inputs.sourceIdentity != "" {
		resources, _ := privateMedia.(releaseworkflow.DescriptionResources)
		preservePrior := resources.PreservePriorDescriptionOverrides && len(instructions.Overrides) == 0
		if preservePrior || (len(reusable.Descriptions) == 0 && len(instructions.Overrides) > 0) {
			previous, found, err := b.reuse.LoadReusableDescription(ctx, inputs.sourcePath)
			if err != nil {
				return nil, fmt.Errorf("workflow reusable descriptions: retain prior override evidence: %w", err)
			}
			if found && previous.Valid() && (previous.MainSourceIdentity == "" || previous.MainSourceIdentity == inputs.sourceIdentity) {
				if preservePrior && len(previous.Overrides) > 0 && previous.MainSource != nil &&
					previous.MainSourceIdentity == inputs.sourceIdentity && *previous.MainSource == *instructions.MainSource &&
					previous.CompatibilityFingerprint != reusable.CompatibilityFingerprint {
					// This build did not load those final edits because they were
					// incompatible, not because an editor superseded them.
					return nil, nil
				}
				previous.Overrides = compatibleRetainedDescriptionOverrides(previous.Overrides, instructions.Overrides)
				if len(previous.Overrides) > 0 {
					// Its original full dependency fingerprint remains the only authority
					// to use these inactive final edits; the rendered output is not reused.
					reusable = previous
				}
			}
		}
		reusable.MainSource = new(*instructions.MainSource)
		reusable.MainSourceIdentity = inputs.sourceIdentity
	}
	if !reusable.Valid() {
		return nil, nil
	}
	return &api.ReusableDescriptionRecord{SourcePath: inputs.sourcePath, Description: reusable}, nil
}

// compatibleRetainedDescriptionOverrides keeps only unchanged text and exact
// tracker memberships proved by the prior compatible record.
func compatibleRetainedDescriptionOverrides(prior, current []api.DescriptionOverrideInput) []api.DescriptionOverrideInput {
	var retained []api.DescriptionOverrideInput
	for _, old := range prior {
		for _, remaining := range current {
			if !strings.EqualFold(old.GroupKey, remaining.GroupKey) || old.Source != remaining.Source || old.Final != remaining.Final {
				continue
			}
			if len(old.TrackerIDs) > 0 {
				old.TrackerIDs = slices.DeleteFunc(slices.Clone(old.TrackerIDs), func(id api.TrackerID) bool {
					return !slices.ContainsFunc(remaining.TrackerIDs, func(candidate api.TrackerID) bool {
						return strings.EqualFold(string(candidate), string(id))
					})
				})
				if len(old.TrackerIDs) == 0 {
					break
				}
			} else if len(remaining.TrackerIDs) > 0 {
				break
			}
			retained = append(retained, old)
			break
		}
	}
	return retained
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
	if b.reuse == nil {
		return api.DescriptionSet{}, instructions, nil
	}
	inputs, err := b.reusableDescriptionInputs(ctx, release, projections, media, privateMedia, instructions, descriptionOutputReuseAllowed(privateMedia))
	if err != nil {
		return api.DescriptionSet{}, instructions, err
	}
	if inputs.sourcePath == "" || (!inputs.available && inputs.sourceIdentity == "") {
		return api.DescriptionSet{}, instructions, nil
	}
	reusable, found, err := b.reuse.LoadReusableDescription(ctx, inputs.sourcePath)
	if err != nil {
		return api.DescriptionSet{}, instructions, fmt.Errorf("workflow reusable descriptions: load: %w", err)
	}
	if !found || !reusable.Valid() {
		return api.DescriptionSet{}, instructions, nil
	}

	restoredInstructions := instructions
	sameSource := reusable.MainSource != nil && reusable.MainSourceIdentity == inputs.sourceIdentity
	if restoredInstructions.MainSource == nil && sameSource {
		restoredInstructions.MainSource = new(*reusable.MainSource)
	}
	if !inputs.available || len(instructions.Overrides) > 0 || instructions.MainSource != nil ||
		(reusable.MainSource != nil && !sameSource) ||
		(reusable.CompatibilityFingerprint != inputs.fingerprint && reusable.CompatibilityFingerprint != inputs.legacyFingerprint) {
		return api.DescriptionSet{}, restoredInstructions, nil
	}
	restoredInstructions.Overrides = slices.Clone(reusable.Overrides)
	if restoredInstructions.MainSource != nil || reusable.RendererVersion != workflowDescriptionRendererVersion {
		// Retained output can predate the shared edit or use an older renderer.
		// Rebuild from the compatible input, never from preexisting output.
		snapshot, buildErr := b.Build(ctx, release, projections, media, privateMedia, restoredInstructions, time.Time{})
		return snapshot, restoredInstructions, buildErr
	}
	inputFingerprint, templateFingerprint, err := b.Fingerprints(
		ctx, release, projections, media, privateMedia, restoredInstructions,
	)
	if err != nil {
		return api.DescriptionSet{}, instructions, fmt.Errorf("workflow reusable descriptions: current fingerprints: %w", err)
	}
	snapshot := api.DescriptionSet{
		MainSource:          restoredInstructions.MainSource,
		InputFingerprint:    inputFingerprint,
		TemplateFingerprint: templateFingerprint,
		Descriptions:        cloneWorkflowRenderedDescriptions(reusable.Descriptions),
		TrackerResults:      slices.Clone(reusable.TrackerResults),
		Status:              api.StageStatusCompleted,
	}
	if !workflowDescriptionSnapshotFullyCompleted(projections, snapshot) {
		return api.DescriptionSet{}, restoredInstructions, nil
	}
	return snapshot, restoredInstructions, nil
}

func descriptionOutputReuseAllowed(privateMedia any) bool {
	resources, wrapped := privateMedia.(releaseworkflow.DescriptionResources)
	return !wrapped || (resources.AudioAnalysis.ID == "" && resources.AudioPaths == nil && resources.HDR == nil && len(resources.HDRArtifacts) == 0)
}

type descriptionReuseInputs struct {
	sourcePath        string
	sourceIdentity    string
	fingerprint       api.WorkflowFingerprint
	legacyFingerprint api.WorkflowFingerprint
	available         bool
}

func (b workflowDescriptionBuilder) reusableDescriptionInputs(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	media api.MediaArtifactSet,
	privateMedia any,
	instructions api.DescriptionInstructions,
	includeOutput bool,
) (descriptionReuseInputs, error) {
	if err := ctx.Err(); err != nil {
		return descriptionReuseInputs{}, fmt.Errorf("workflow reusable descriptions: %w", err)
	}
	compatibilityInstructions := instructions
	compatibilityInstructions.Overrides = nil
	compatibilityInstructions.MainSource = nil
	subject, err := b.resolveSubject(ctx, release, projections, compatibilityInstructions)
	if err != nil {
		return descriptionReuseInputs{}, err
	}
	binding := subject.MediaBinding
	if !binding.Valid() {
		return descriptionReuseInputs{}, nil
	}
	inputs := descriptionReuseInputs{sourcePath: binding.SourcePath}
	if subject.SourceIdentity.Version == api.SourceContentIdentityVersion {
		inputs.sourceIdentity = strings.ToLower(subject.SourceIdentity.Digest)
	}
	if !includeOutput || !binding.CompatibilityKey.Valid() {
		return inputs, nil
	}
	retainWorkflowScreenshotSelection(&subject, privateMedia)
	resourceSubject := subject
	if len(instructions.Overrides) > 0 {
		resourceSubject, err = b.resolveSubject(ctx, release, projections, instructions)
		if err != nil {
			return descriptionReuseInputs{}, err
		}
	}
	exactMedia, err := b.resolveWorkflowDescriptionMedia(ctx, resourceSubject, privateMedia, media)
	if err != nil {
		return descriptionReuseInputs{}, err
	}
	descriptionSubject := api.NewDescriptionSubject(subject)
	descriptionSubject.ExactMedia = exactMedia
	if resources, wrapped := privateMedia.(releaseworkflow.DescriptionResources); wrapped {
		privateMedia = resources.Media
	}
	retained, _ := privateMedia.(workflowMediaPrivateArtifacts)
	fingerprint, legacyFingerprint, available, err := workflowReusableDescriptionFingerprint(
		ctx, b.config, binding.CompatibilityKey, projections, compatibilityInstructions, descriptionSubject, retained.legacyImageFormats,
	)
	if err != nil {
		return descriptionReuseInputs{}, err
	}
	inputs.fingerprint, inputs.legacyFingerprint, inputs.available = fingerprint, legacyFingerprint, available
	return inputs, nil
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
	Tonemapping  api.ScreenshotTonemapping `json:",omitempty"`
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
	ComparisonFingerprint api.WorkflowFingerprint
	ComparisonUploads     [][]workflowReusableDescriptionHostedImage
	Screenshots           []workflowReusableDescriptionImage
	DVDMenus              []workflowReusableDescriptionDVDMenu
	ScreenshotUploads     []workflowReusableDescriptionHostedImage
	DVDMenuUploads        []workflowReusableDescriptionHostedImage
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
	legacyFormats map[string]workflowLegacyImageFormat,
) (api.WorkflowFingerprint, api.WorkflowFingerprint, bool, error) {
	normalizedSubject, localResources, available, err := normalizeReusableDescriptionSubject(ctx, subject)
	if err != nil || !available {
		return "", "", available, err
	}
	normalizedMedia, available, err := normalizeReusableDescriptionMedia(ctx, subject.ExactMedia)
	if err != nil || !available {
		return "", "", available, err
	}
	payload := struct {
		Version                              string
		Config                               config.Config
		CompatibilityKey                     api.MediaCompatibilityKey
		Projections                          []api.TrackerReleaseProjection
		Instructions                         api.DescriptionInstructions
		Subject                              api.DescriptionSubject
		SuppressImportedScreenshotReferences map[string]bool
		LocalResources                       workflowReusableDescriptionLocalResources
		ExactMedia                           workflowReusableDescriptionMedia
	}{
		Version:                              "description-reuse-v4",
		Config:                               cfg,
		CompatibilityKey:                     compatibilityKey,
		Projections:                          reusableDescriptionProjections(projections.Projections),
		Instructions:                         instructions,
		Subject:                              normalizedSubject,
		SuppressImportedScreenshotReferences: subject.SuppressImportedScreenshotReferences,
		LocalResources:                       localResources,
		ExactMedia:                           normalizedMedia,
	}
	legacyAvailable := true
	for index := range payload.ExactMedia.Screenshots {
		if original, exists := legacyFormats[subject.ExactMedia.Screenshots[index].Path]; exists {
			legacyAvailable = legacyAvailable && !original.Conflicting
			payload.ExactMedia.Screenshots[index].Image.Format = original.Format
		}
	}
	for index := range payload.ExactMedia.DVDMenus {
		if original, exists := legacyFormats[subject.ExactMedia.DVDMenus[index].Path]; exists {
			legacyAvailable = legacyAvailable && !original.Conflicting
			payload.ExactMedia.DVDMenus[index].Image.Format = original.Format
		}
	}
	var legacyFingerprint api.WorkflowFingerprint
	if legacyAvailable {
		legacyFingerprint, err = api.CanonicalWorkflowFingerprint(payload)
		if err != nil {
			return "", "", false, fmt.Errorf("workflow reusable descriptions: canonical fingerprint: %w", err)
		}
	}
	// Older records included the format label. Accept that exact dependency hash,
	// but store byte-bound compatibility so later format inspection preserves edits.
	for index := range payload.ExactMedia.Screenshots {
		payload.ExactMedia.Screenshots[index].Image.Format = ""
	}
	for index := range payload.ExactMedia.DVDMenus {
		payload.ExactMedia.DVDMenus[index].Image.Format = ""
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(payload)
	if err != nil {
		return "", "", false, fmt.Errorf("workflow reusable descriptions: canonical fingerprint: %w", err)
	}
	return fingerprint, legacyFingerprint, true, nil
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
	// Automatic HDR output must rebuild against its current exact authority.
	// All-final editor text resolves no HDR assets and remains reusable.
	if exact.HDRAnalysis != nil || len(exact.HDRPlots) > 0 {
		return workflowReusableDescriptionMedia{}, false, nil
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
		// Actual bytes are already bound below; optional provenance enrichment is not a content change.
		image.ContentSHA256 = ""
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
		menu.ContentSHA256 = ""
		menu.UploadedAt = time.Time{}
		normalized.DVDMenus = append(normalized.DVDMenus, workflowReusableDescriptionDVDMenu{
			ContentSHA256: digest,
			Image:         menu,
		})
	}
	var err error
	if exact.Comparisons != nil {
		normalized.ComparisonFingerprint, err = comparisons.ReviewFingerprint(*exact.Comparisons)
		if err != nil {
			return workflowReusableDescriptionMedia{}, false, fmt.Errorf("workflow reusable descriptions: fingerprint comparisons: %w", err)
		}
		for _, row := range exact.Comparisons.Rows {
			if row.Excluded {
				continue
			}
			for _, cell := range row.Cells {
				image := exact.ComparisonImages[cell.ArtifactID]
				digest, available, err := reusableDescriptionFileDigest(ctx, image.Path)
				if err != nil || !available || digest == "" || digest != cell.ContentSHA256 {
					return workflowReusableDescriptionMedia{}, false, err
				}
				digestsByPath[image.Path] = digest
				links, err := normalizeReusableDescriptionHostedImages(exact.ComparisonUploads[cell.ArtifactID], digestsByPath)
				if err != nil {
					return workflowReusableDescriptionMedia{}, false, err
				}
				// The review fingerprint binds cell order. Retain one list per cell,
				// including empty lists, so equal-content cells keep separate links.
				normalized.ComparisonUploads = append(normalized.ComparisonUploads, links)
			}
		}
	}
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
			Tonemapping:  link.Tonemapping,
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
		empty := strings.TrimSpace(description.Source) == ""
		if baseGroup == "" || empty != (strings.TrimSpace(description.Rendered) == "") {
			return false
		}
		for _, trackerID := range description.TrackerIDs {
			if _, exists := covered[trackerID]; exists {
				return false
			}
			matched := false
			for _, projection := range targets {
				if projection.TrackerID == trackerID &&
					(!empty || projection.Artifacts.AllowEmptyDescription) &&
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
