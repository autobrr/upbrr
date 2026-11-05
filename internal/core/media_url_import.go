// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// reusableImportedImageOrigins recovers source associations from the same
// validated local tracker inventory used by screenshot planning. Hosted outputs
// are not import authority, and ambiguous or changed bytes remain unassociated.
func (b workflowMediaBuilder) reusableImportedImageOrigins(ctx context.Context, subject api.ScreenshotSubject) map[string]api.ScreenshotLinkedImage {
	inventory, ok := b.screenshots.(workflowReusableTrackerImageInventory)
	if !ok {
		return nil
	}
	links, err := inventory.ReusableTrackerImageLinks(ctx, subject.SourcePath, subject.DiscType, subject.Release)
	if err != nil {
		return nil
	}
	origins := make(map[string]api.ScreenshotLinkedImage)
	ambiguous := make(map[string]bool)
	for _, link := range links {
		if strings.TrimSpace(link.Host) == "" || strings.TrimSpace(link.URL) == "" {
			continue
		}
		digest, err := workflowMediaContentSHA256(ctx, link.Path)
		if err != nil {
			continue
		}
		if prior, exists := origins[digest]; exists && prior.URL != link.URL {
			ambiguous[digest] = true
		}
		origins[digest] = link
	}
	for digest := range ambiguous {
		delete(origins, digest)
	}
	return origins
}

func (b workflowMediaBuilder) mediaRegistry() *trackers.Registry {
	if b.media == nil {
		return nil
	}
	return b.media.registry
}

// restoredNativeImageAttempts restores combined native and ordinary coverage.
// The ordinary single-host path remains authoritative without native results.
func (b workflowMediaBuilder) restoredNativeImageAttempts(
	snapshot api.MediaArtifactSet,
	retained workflowMediaPrivateArtifacts,
	projection api.TrackerReleaseProjection,
	subject api.UploadSubject,
	selected, eligible map[api.PublicResourceID]struct{},
	ordered []api.PublicResourceID,
) ([]api.HostedImageAttempt, bool) {
	tracker := strings.ToUpper(strings.TrimSpace(string(projection.TrackerID)))
	policy, ok := b.media.registry.LookupImageHostPolicy(tracker)
	if !ok || policy.URLImport == nil || subject.ImageHostOverrides.PreferredHost != nil && strings.TrimSpace(*subject.ImageHostOverrides.PreferredHost) != "" {
		return nil, false
	}
	capability := policy.URLImport
	isNative := func(link api.UploadedImageLink) bool {
		return link.Host == capability.Host && link.UsageScope == capability.UsageScope
	}
	bySource := make(map[api.PublicResourceID]retainedHostedImageCandidate, len(ordered))
	for _, candidate := range b.retainedHostedImageCandidates(snapshot, retained, selected) {
		link := candidate.link
		if slices.Contains(snapshot.FailedHosts, link.Host) || link.UsageScope != "global" && link.UsageScope != "tracker:"+tracker {
			continue
		}
		allowed, err := trackers.ReusableImageHostAllowedWithRegistry(b.media.registry, b.config, tracker, link.Host, subject.ImageHostOverrides)
		if err != nil || !allowed {
			continue
		}
		prior, exists := bySource[candidate.sourceID]
		if !exists || isNative(link) && !isNative(prior.link) {
			bySource[candidate.sourceID] = candidate
		}
	}
	for sourceID := range eligible {
		if _, covered := bySource[sourceID]; !covered {
			return nil, false
		}
	}
	type group struct {
		host, scope string
		sources     map[api.PublicResourceID]struct{}
		ids         []api.PublicResourceID
	}
	var groups []group
	groupByTarget := make(map[string]int)
	kinds := make(map[api.PublicResourceID]api.MediaArtifactKind, len(ordered))
	selectedMenus := 0
	for _, artifact := range snapshot.Artifacts {
		if _, chosen := selected[artifact.ID]; !chosen {
			continue
		}
		kinds[artifact.ID] = artifact.Kind
		if artifact.Kind == api.MediaArtifactDVDMenu {
			selectedMenus++
		}
	}
	nativePresent := false
	coveredScreenshots, coveredMenus := 0, 0
	for _, sourceID := range ordered {
		candidate, covered := bySource[sourceID]
		if !covered {
			continue
		}
		link := candidate.link
		nativePresent = nativePresent || isNative(link)
		switch kinds[sourceID] {
		case api.MediaArtifactScreenshot:
			coveredScreenshots++
		case api.MediaArtifactDVDMenu:
			coveredMenus++
		case api.MediaArtifactHostedImage:
			continue
		}
		key := link.Host + "\x00" + link.UsageScope
		index, exists := groupByTarget[key]
		if !exists {
			index = len(groups)
			groupByTarget[key] = index
			groups = append(groups, group{
				host:    link.Host,
				scope:   link.UsageScope,
				sources: make(map[api.PublicResourceID]struct{}),
			})
		}
		groups[index].sources[sourceID] = struct{}{}
		groups[index].ids = append(groups[index].ids, sourceID)
	}
	if !nativePresent || coveredScreenshots < projection.Artifacts.ScreenshotCount || coveredMenus < selectedMenus {
		return nil, false
	}
	attempts := make([]api.HostedImageAttempt, 0, len(groups))
	for _, grouped := range groups {
		attempt, covered := b.restoredHostedImageAttempt(
			snapshot,
			retained,
			grouped.sources,
			grouped.sources,
			grouped.ids,
			0,
			grouped.host,
			grouped.scope,
			[]string{tracker},
		)
		if !covered {
			return nil, false
		}
		attempts = append(attempts, attempt)
	}
	return attempts, true
}

// imageURLImportTargets records authority from selected imported artifacts, never
// from an incidental hosted URL on a local capture. Explicit host controls win.
func (b workflowMediaBuilder) imageURLImportTargets(
	snapshot api.MediaArtifactSet,
	retained workflowMediaPrivateArtifacts,
	selected map[api.PublicResourceID]struct{},
	subject api.UploadSubject,
	trackersList []string,
	host string,
	preferred *string,
	excluded []string,
) ([]trackers.ImageUploadTarget, map[string]string) {
	var targets []trackers.ImageUploadTarget
	sources := make(map[string]string)
	if host != "" || preferred != nil && strings.TrimSpace(*preferred) != "" || subject.ExactMedia != nil && subject.ExactMedia.ImageHostUploadSkipped {
		return nil, sources
	}
	for _, artifact := range snapshot.Artifacts {
		if _, ok := selected[artifact.ID]; !ok || artifact.Kind != api.MediaArtifactScreenshot || artifact.Source != "tracker" ||
			!isImportedWorkflowScreenshot(artifact, retained) {
			continue
		}
		image, ok := retained.ArtifactImages[artifact.ID]
		if ok && image.RawURL != "" {
			sources[normalizedUploadImagePath(image.Path)] = image.RawURL
		}
	}
	if len(sources) == 0 {
		return nil, sources
	}
	for _, name := range trackersList {
		capability := trackers.DefaultImageURLImport(b.media.registry, b.config, name, subject.ImageHostOverrides)
		if capability == nil {
			continue
		}
		if slices.Contains(excluded, capability.Host) {
			continue
		}
		targets = append(targets, trackers.ImageUploadTarget{
			Host:       capability.Host,
			UsageScope: capability.UsageScope,
			Trackers:   []string{name},
			ImportID:   capability.ID,
		})
	}
	return targets, sources
}

// uploadImagesWithURLImports partitions tracker coverage per selected artifact.
// The ordinary scheduler retains its existing semantics for each residual set.
func (m *mediaModule) uploadImagesWithURLImports(
	ctx context.Context,
	meta api.UploadSubject,
	host string,
	excluded []string,
	targets, native []trackers.ImageUploadTarget,
	sources map[string]string,
	images []api.ScreenshotImage,
	retained, blocked []api.UploadedImageLink,
) (api.UploadImagesResult, error) {
	if len(native) == 0 {
		return m.uploadImagesToTargetsWithFallback(ctx, meta, host, excluded, targets, images, retained, blocked)
	}
	var result api.UploadImagesResult
	reuseOverrides := meta.ImageHostOverrides
	reuseOverrides.FailedHosts = slices.Concat(reuseOverrides.FailedHosts, excluded)
	covered := make(map[string]map[string]bool)
	for _, target := range native {
		tracker := target.Trackers[0]
		covered[tracker] = make(map[string]bool)
		for _, image := range images {
			key := normalizedUploadImagePath(image.Path)
			if sources[key] == "" {
				continue
			}
			// Reuse any approved current-account target before importing.
			resolvedTarget := target
			for _, link := range retained {
				if normalizedUploadImagePath(link.ImagePath) == key && trackers.NativeImageLinkCurrent(m.cfg, m.registry, link) &&
					(link.UsageScope == "global" || link.UsageScope == target.UsageScope) {
					allowed, err := trackers.ReusableImageHostAllowedWithRegistry(m.registry, m.cfg, tracker, link.Host, reuseOverrides)
					if err == nil && allowed {
						resolvedTarget.Host, resolvedTarget.UsageScope = link.Host, link.UsageScope
						resolvedTarget.ImportID, resolvedTarget.ReuseOnly = "", true
						break
					}
				}
			}
			image.RawURL = sources[key]
			links, err := m.uploadImagesToTarget(ctx, meta, resolvedTarget, []api.ScreenshotImage{image}, retained, blocked, false)
			if failure, ok := api.AsOperationFailure(err); ok && failure.Code == api.OperationFailureUnknownOutcome {
				return result, err
			}
			attempt := api.UploadImageHostAttemptResult{
				ImagePaths: []string{image.Path},
				Host:       resolvedTarget.Host,
				UsageScope: resolvedTarget.UsageScope,
				Trackers:   slices.Clone(target.Trackers),
				Links:      links,
			}
			if err != nil {
				attempt.Failure = &api.UploadImageHostFailure{
					Host:       target.Host,
					UsageScope: target.UsageScope,
					Trackers:   slices.Clone(target.Trackers),
					Message:    "Native image import unavailable; using ordinary hosting.",
				}
			}
			result.Attempts = append(result.Attempts, attempt)
			result.Links = append(result.Links, links...)
			if uploadedImageLinksCoverTarget(links, resolvedTarget, []api.ScreenshotImage{image}) {
				covered[tracker][key] = true
			}
		}
	}
	appendResult := func(residual api.UploadImagesResult) {
		result.Links = append(result.Links, residual.Links...)
		result.Attempts = append(result.Attempts, residual.Attempts...)
		result.Failures = append(result.Failures, residual.Failures...)
		for _, failed := range residual.FailedHosts {
			if !slices.Contains(result.FailedHosts, failed) {
				result.FailedHosts = append(result.FailedHosts, failed)
			}
		}
	}
	// Unrelated trackers keep their original complete batch and partial-success policy.
	ordinary := make([]trackers.ImageUploadTarget, 0, len(targets))
	for _, target := range targets {
		target.Trackers = slices.DeleteFunc(slices.Clone(target.Trackers), func(tracker string) bool { _, isNative := covered[tracker]; return isNative })
		if len(target.Trackers) > 0 {
			ordinary = append(ordinary, target)
		}
	}
	if len(ordinary) > 0 {
		residual, err := m.uploadImagesToTargetsWithFallback(ctx, meta, host, excluded, ordinary, images, retained, blocked)
		if err != nil {
			return result, err
		}
		appendResult(residual)
	}
	for _, nativeTarget := range native {
		tracker := nativeTarget.Trackers[0]
		missing := slices.DeleteFunc(
			slices.Clone(images),
			func(image api.ScreenshotImage) bool { return covered[tracker][normalizedUploadImagePath(image.Path)] },
		)
		if len(missing) == 0 {
			continue
		}
		remaining := make([]trackers.ImageUploadTarget, 0, len(targets))
		for _, target := range targets {
			if slices.Contains(target.Trackers, tracker) {
				target.Trackers = []string{tracker}
				remaining = append(remaining, target)
			}
		}
		if len(remaining) == 0 {
			result.Failures = append(result.Failures, api.UploadImageHostFailure{
				Host:       nativeTarget.Host,
				UsageScope: nativeTarget.UsageScope,
				Trackers:   []string{tracker},
				Message:    "Selected images require an available ordinary image host.",
			})
			continue
		}
		residual, err := m.uploadImagesToTargetsWithFallback(ctx, meta, host, excluded, remaining, missing, retained, blocked)
		if err != nil {
			return result, err
		}
		paths := make([]string, 0, len(missing))
		for _, image := range missing {
			paths = append(paths, image.Path)
		}
		for index := range residual.Attempts {
			residual.Attempts[index].ImagePaths = slices.Clone(paths)
		}
		appendResult(residual)
	}
	return result, nil
}
