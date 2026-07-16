// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// FetchAcceptedMetadataPreview projects one exact prepared generation and
// current source-scoped tracker records into the transport preview model.
func (c *Core) FetchAcceptedMetadataPreview(ctx context.Context, ref api.ReleaseRef) (preview api.MetadataPreview, err error) {
	defer func() { err = classifyOperationError(api.OperationKindPreparation, err) }()
	finishProgress := api.BeginPreparationProgress(ctx, api.PreparationPhasePreviewProjection, "Building metadata preview.")
	defer func() { finishProgress(err) }()
	if c == nil || c.preparedFacts == nil {
		return api.MetadataPreview{}, errors.New("core: canonical preparation is not configured")
	}
	prepared, err := c.preparedFacts.ResolveResult(ctx, ref)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("core: resolve canonical metadata preview: %w", err)
	}
	display, err := c.preparedFacts.ResolveDisplay(ctx, ref)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("core: project canonical metadata preview: %w", err)
	}
	preview = api.MetadataPreview{
		SourcePath:  prepared.Release.Source.SourcePath,
		ReleaseName: prepared.Release.Naming.ReleaseName,
		Release: api.ReleaseRef{
			SourcePath: prepared.Release.Source.SourcePath,
			Generation: prepared.Release.Generation,
		},
		Identity:    prepared.Release.Identity,
		Display:     display,
		Bluray:      prepared.Release.ProviderMetadata.Bluray,
		Diagnostics: append([]api.PreparationDiagnostic(nil), prepared.Diagnostics...),
	}
	if c.upload != nil && c.upload.trackerRepo != nil {
		records, loadErr := c.upload.trackerRepo.ListTrackerMetadataByPath(ctx, prepared.Release.Source.SourcePath)
		if loadErr != nil && !errors.Is(loadErr, internalerrors.ErrNotFound) {
			return api.MetadataPreview{}, fmt.Errorf("core: load tracker metadata preview: %w", loadErr)
		}
		preview.TrackerData = buildTrackerPreview(records, c.upload.cfg)
	}
	return preview, nil
}

// FetchAcceptedPreparationPreview builds description preparation for one exact
// prepared generation without consulting mutable cache state.
func (c *Core) FetchAcceptedPreparationPreview(ctx context.Context, input api.DescriptionInput) (api.PreparationPreview, error) {
	preview, err := c.description.fetchAcceptedPreview(ctx, input)
	if err != nil {
		return api.PreparationPreview{}, classifyOperationError(api.OperationKindDescription, err)
	}
	result := api.PreparationPreview{SourcePath: preview.SourcePath}
	for _, group := range preview.Groups {
		result.Descriptions = append(result.Descriptions, api.PreparationDescription{
			GroupKey:           group.GroupKey,
			Trackers:           append([]string(nil), group.Trackers...),
			RawDescription:     group.RawDescription,
			RawDescriptionHTML: group.RawDescriptionHTML,
			Description:        group.Description,
			DescriptionHTML:    group.DescriptionHTML,
			HasOverride:        group.HasOverride,
			ImageHost:          group.ImageHost,
		})
	}
	return result, nil
}
