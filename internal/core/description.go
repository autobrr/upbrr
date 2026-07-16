// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/description"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// descriptionModule owns description preparation, rendering, override
// persistence, and canonical group resolution. It borrows all dependencies;
// Core retains resource lifetime.
type descriptionModule struct {
	cfg            config.Config
	logger         api.Logger
	trackerService api.TrackerService
	repo           api.ReleaseSelectionRepository
	registry       *trackers.Registry
	preparedFacts  *preparedrelease.Module
}

func newDescriptionModule(
	cfg config.Config,
	logger api.Logger,
	services api.ServiceSet,
	repo api.ReleaseSelectionRepository,
	registry *trackers.Registry,
	preparedFacts *preparedrelease.Module,
) *descriptionModule {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &descriptionModule{
		cfg:            cfg,
		logger:         logger,
		trackerService: services.Trackers,
		repo:           repo,
		registry:       registry,
		preparedFacts:  preparedFacts,
	}
}

// fetchAcceptedPreview builds editable description groups from one exact
// prepared generation and operation-owned choices.
func (m *descriptionModule) fetchAcceptedPreview(ctx context.Context, input api.DescriptionInput) (api.DescriptionBuilderPreview, error) {
	if m == nil || m.preparedFacts == nil {
		return api.DescriptionBuilderPreview{}, errors.New("core: canonical preparation is not configured")
	}
	if m.trackerService == nil {
		return api.DescriptionBuilderPreview{}, errors.New("core: tracker service not configured")
	}
	subject, err := m.preparedFacts.ResolveUploadSubject(ctx, api.UploadReviewInput{
		Release:              input.Release,
		Trackers:             append([]string(nil), input.Trackers...),
		DescriptionGroups:    api.CloneDescriptionBuilderGroups(input.Groups),
		ImageHostOverrides:   input.ImageHost,
		QuestionnaireAnswers: cloneOperationQuestionnaireAnswers(input.QuestionnaireData),
		Options:              input.Options,
	})
	if err != nil {
		return api.DescriptionBuilderPreview{}, fmt.Errorf("core: resolve upload subject for description preview: %w", err)
	}
	resolved, explicitEmpty := resolveTrackersPreservingExplicitEmpty(m.cfg, input.Trackers, nil, m.logger, m.registry, false, false)
	if explicitEmpty {
		return api.DescriptionBuilderPreview{SourcePath: subject.SourcePath}, nil
	}
	subject.Trackers = resolved
	prepared, err := m.trackerService.BuildPreparation(ctx, api.NewDescriptionSubject(subject), resolved)
	if err != nil {
		return api.DescriptionBuilderPreview{}, fmt.Errorf("core: %w", err)
	}
	overrideByGroup, err := m.descriptionOverrides(ctx, subject.SourcePath)
	if err != nil {
		return api.DescriptionBuilderPreview{}, err
	}
	preview := api.DescriptionBuilderPreview{SourcePath: subject.SourcePath}
	for _, entry := range prepared.Descriptions {
		preview.Groups = append(preview.Groups, buildDescriptionBuilderGroup(entry, overrideByGroup))
	}
	return preview, nil
}

func (m *descriptionModule) fetchAcceptedGroupPreview(ctx context.Context, input api.DescriptionInput) (api.DescriptionBuilderGroup, error) {
	preview, err := m.fetchAcceptedPreview(ctx, input)
	if err != nil {
		return api.DescriptionBuilderGroup{}, fmt.Errorf("core: resolve description subject: %w", err)
	}
	target := normalizeDescriptionBuilderGroupKey(input.GroupKey, input.Trackers)
	if target == "" {
		return api.DescriptionBuilderGroup{}, internalerrors.ErrInvalidInput
	}
	for _, group := range preview.Groups {
		if normalizeDescriptionBuilderGroupKey(group.GroupKey, group.Trackers) == target {
			return group, nil
		}
	}
	return api.DescriptionBuilderGroup{}, internalerrors.ErrNotFound
}

func (m *descriptionModule) saveAcceptedOverride(
	ctx context.Context,
	input api.DescriptionInput,
	raw string,
) (api.DescriptionBuilderGroup, error) {
	if m == nil || m.preparedFacts == nil {
		return api.DescriptionBuilderGroup{}, errors.New("core: canonical preparation is not configured")
	}
	if m.repo == nil {
		return api.DescriptionBuilderGroup{}, errors.New("core: repository not configured")
	}
	subject, err := m.preparedFacts.ResolveDescriptionSubject(ctx, input)
	if err != nil {
		return api.DescriptionBuilderGroup{}, fmt.Errorf("core: resolve description override subject: %w", err)
	}
	groupKey := normalizeDescriptionBuilderGroupKey(input.GroupKey, input.Trackers)
	if groupKey == "" {
		return api.DescriptionBuilderGroup{}, internalerrors.ErrInvalidInput
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		if err := m.repo.DeleteDescriptionOverride(ctx, subject.SourcePath, groupKey); err != nil {
			return api.DescriptionBuilderGroup{}, fmt.Errorf("core: %w", err)
		}
		group, err := m.fetchAcceptedGroupPreview(ctx, input)
		if err == nil {
			return group, nil
		}
		if errors.Is(err, internalerrors.ErrNotFound) {
			return api.DescriptionBuilderGroup{GroupKey: groupKey, Trackers: append([]string(nil), input.Trackers...)}, nil
		}
		return api.DescriptionBuilderGroup{}, err
	}
	if err := m.repo.SaveDescriptionOverride(ctx, api.DescriptionOverride{
		SourcePath:  subject.SourcePath,
		GroupKey:    groupKey,
		Description: trimmed,
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		return api.DescriptionBuilderGroup{}, fmt.Errorf("core: %w", err)
	}
	return api.DescriptionBuilderGroup{
		GroupKey:           groupKey,
		Trackers:           append([]string(nil), input.Trackers...),
		RawDescription:     trimmed,
		RawDescriptionHTML: description.Render(trimmed),
		HasOverride:        true,
	}, nil
}

func (m *descriptionModule) descriptionOverrides(ctx context.Context, sourcePath string) (map[string]api.DescriptionOverride, error) {
	result := make(map[string]api.DescriptionOverride)
	if m.repo == nil {
		return result, nil
	}
	overrides, err := m.repo.ListDescriptionOverridesByPath(ctx, sourcePath)
	if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
		return nil, fmt.Errorf("core: description override: %w", err)
	}
	for _, override := range overrides {
		result[normalizeDescriptionBuilderGroupKey(override.GroupKey, nil)] = override
	}
	return result, nil
}

// FetchDescriptionBuilderPreview builds editable description groups from cached
// or freshly prepared metadata. When request trackers are provided, only that
// selected set contributes groups; selections that resolve empty return an empty
// preview instead of falling back to configured defaults.

func buildDescriptionBuilderGroup(
	entry api.PreparationDescription,
	overrideByGroup map[string]api.DescriptionOverride,
) api.DescriptionBuilderGroup {
	groupKey := normalizeDescriptionBuilderGroupKey(entry.GroupKey, entry.Trackers)
	descriptionText := strings.TrimSpace(entry.Description)
	if descriptionText == "" {
		descriptionText = strings.TrimSpace(entry.RawDescription)
	}
	descriptionHTML := entry.DescriptionHTML
	if strings.TrimSpace(descriptionHTML) == "" {
		descriptionHTML = description.Render(descriptionText)
	}
	rawDescription := descriptionText
	rawDescriptionHTML := descriptionHTML
	if strings.TrimSpace(rawDescription) == "" {
		rawDescription = strings.TrimSpace(entry.RawDescription)
		if strings.TrimSpace(rawDescription) != "" {
			rawDescriptionHTML = entry.RawDescriptionHTML
		} else {
			rawDescriptionHTML = ""
		}
	}
	hasOverride := entry.HasOverride
	if strings.TrimSpace(rawDescription) == "" {
		if override, ok := overrideByGroup[groupKey]; ok && strings.TrimSpace(override.Description) != "" {
			rawDescription = strings.TrimSpace(override.Description)
			rawDescriptionHTML = description.Render(rawDescription)
		}
	}
	if _, ok := overrideByGroup[groupKey]; ok && strings.TrimSpace(rawDescription) != "" {
		hasOverride = true
	}
	if strings.TrimSpace(rawDescriptionHTML) == "" {
		rawDescriptionHTML = description.Render(rawDescription)
	}
	return api.DescriptionBuilderGroup{
		GroupKey:           groupKey,
		Trackers:           append([]string{}, entry.Trackers...),
		Description:        rawDescription,
		DescriptionHTML:    rawDescriptionHTML,
		RawDescription:     rawDescription,
		RawDescriptionHTML: rawDescriptionHTML,
		HasOverride:        hasOverride,
		ImageHost:          entry.ImageHost,
	}
}

// ensureDescriptionBuilderMetadata refreshes missing external metadata before
// tracker description preparation, using the resolved tracker set for localized
// pt-BR refreshes while preserving the original tracker list on returned
// metadata. Cacheable WebUI refreshes are stored as request-refreshed entries.

// descriptionBuilderNeedsExternalMetadata reports whether tracker description
// preparation needs metadata not present on the current prepared metadata.

// descriptionBuilderNeedsPTBRMetadata reports whether localized tracker
// descriptions need a missing pt-BR TMDB metadata entry.

// descriptionBuilderTrackersNeedPTBR reports whether any tracker consumes pt-BR localized metadata.

// descriptionBuilderEpisodeLike reports whether description generation should
// require episode-scoped metadata when episode overview support is enabled.

func normalizeDescriptionBuilderGroupKey(groupKey string, trackersList []string) string {
	normalized := strings.ToLower(strings.TrimSpace(groupKey))
	if normalized == "" && len(trackersList) > 0 {
		normalized = strings.ToLower(strings.TrimSpace(trackersList[0]))
	}
	return normalized
}

// FetchDescriptionBuilderGroupPreview rebuilds one description group from cached
// or freshly prepared metadata. Request trackers limit the rebuild to the
// selected set while tracker removals still suppress removed selections.

func (m *descriptionModule) render(ctx context.Context, raw string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("core: render description canceled: %w", err)
	}
	return description.Render(raw), nil
}

func (m *descriptionModule) resolveOverrideRequest(ctx context.Context, req api.Request) (api.Request, error) {
	if strings.TrimSpace(req.DescriptionOverrideRaw) != "" || strings.TrimSpace(req.DescriptionOverrideURL) == "" {
		return req, nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(req.DescriptionOverrideURL), nil)
	if err != nil {
		return api.Request{}, fmt.Errorf("core: description override url: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return api.Request{}, fmt.Errorf("core: description override fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return api.Request{}, fmt.Errorf("core: description override fetch: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return api.Request{}, fmt.Errorf("core: description override read: %w", err)
	}
	if strings.TrimSpace(string(body)) == "" {
		return api.Request{}, errors.New("core: description override fetch returned empty body")
	}

	req.DescriptionOverrideRaw = string(body)
	return req, nil
}

// resolveCanonicalDescriptionGroups returns request or cached description groups
// before rebuilding groups from tracker preparation for the selected tracker set.
// Explicit tracker selections constrain the rebuild and do not fall back to
// configured defaults when they resolve empty.

// resolveSubjectGroups builds description groups directly from an exact
// operation subject. It never imports mutable preparation state.
func (m *descriptionModule) resolveSubjectGroups(
	ctx context.Context,
	subject api.UploadSubject,
	input api.UploadReviewInput,
) ([]api.DescriptionBuilderGroup, error) {
	if len(input.DescriptionGroups) > 0 {
		return api.CloneDescriptionBuilderGroups(input.DescriptionGroups), nil
	}
	if len(subject.DescriptionGroups) > 0 {
		return api.CloneDescriptionBuilderGroups(subject.DescriptionGroups), nil
	}
	if m == nil || m.trackerService == nil {
		return nil, errors.New("core: tracker service not configured")
	}
	if len(subject.Trackers) == 0 {
		return nil, nil
	}

	preparation, err := m.trackerService.BuildPreparation(ctx, api.NewDescriptionSubject(subject), subject.Trackers)
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}
	if len(preparation.Descriptions) == 0 {
		return nil, nil
	}

	overrideByGroup := make(map[string]api.DescriptionOverride)
	if m.repo != nil && strings.TrimSpace(subject.SourcePath) != "" {
		overrides, err := m.repo.ListDescriptionOverridesByPath(ctx, subject.SourcePath)
		if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
			return nil, fmt.Errorf("core: description override: %w", err)
		}
		for _, override := range overrides {
			overrideByGroup[normalizeDescriptionBuilderGroupKey(override.GroupKey, nil)] = override
		}
	}

	groups := make([]api.DescriptionBuilderGroup, 0, len(preparation.Descriptions))
	for _, entry := range preparation.Descriptions {
		groups = append(groups, buildDescriptionBuilderGroup(entry, overrideByGroup))
	}
	return groups, nil
}
