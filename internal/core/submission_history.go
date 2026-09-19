// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type submissionFenceTrackerRegistry interface {
	LookupDescriptor(string) (trackers.Descriptor, bool)
}

// workflowSubmissionHistoryFilter removes only confirmed global submissions
// from a tracker selection. Started and unknown attempts remain fail-closed.
type workflowSubmissionHistoryFilter struct {
	fences   api.SubmissionFenceRepository
	registry submissionFenceTrackerRegistry
}

func (f workflowSubmissionHistoryFilter) FilterConfirmedSubmissions(
	ctx context.Context,
	subject api.UploadSubject,
	requested []api.TrackerID,
) ([]api.TrackerID, []api.SubmissionExclusion, error) {
	if len(requested) == 0 {
		return nil, nil, nil
	}
	if f.fences == nil || f.registry == nil {
		return nil, nil, errors.New("workflow submission history: fence lookup is unavailable")
	}
	identity, err := workflowSubmissionContentIdentity(workflowSubmissionTorrentSubject(subject), subject.SourceIdentity)
	if err != nil {
		return nil, nil, err
	}
	remaining := make([]api.TrackerID, 0, len(requested))
	exclusions := make([]api.SubmissionExclusion, 0, len(requested))
	seen := make(map[api.TrackerID]struct{}, len(requested))
	for _, requestedID := range requested {
		descriptor, ok := f.registry.LookupDescriptor(string(requestedID))
		if !ok || strings.TrimSpace(descriptor.Name) == "" {
			return nil, nil, fmt.Errorf("workflow submission history: registered tracker identity is unavailable for %q", requestedID)
		}
		trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(descriptor.Name)))
		if _, duplicate := seen[trackerID]; duplicate {
			continue
		}
		seen[trackerID] = struct{}{}
		site, err := trackers.CanonicalSubmissionTrackerSite(descriptor.Name, descriptor.BaseURL)
		if err != nil {
			return nil, nil, fmt.Errorf("workflow submission history: %w", err)
		}
		fence, err := f.fences.LoadSubmissionFence(ctx, identity, site)
		if errors.Is(err, api.ErrSubmissionFenceNotFound) {
			remaining = append(remaining, trackerID)
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("workflow submission history: load %s: %w", trackerID, err)
		}
		switch fence.Status {
		case api.WorkflowEffectStatusSucceeded:
			if fence.ConfirmedAt == nil {
				return nil, nil, fmt.Errorf("workflow submission history: confirmed fence for %s has no confirmation time", trackerID)
			}
			exclusions = append(exclusions, api.SubmissionExclusion{
				TrackerID:   trackerID,
				Reason:      "already_uploaded",
				ConfirmedAt: *fence.ConfirmedAt,
			})
		case api.WorkflowEffectStatusStarted, api.WorkflowEffectStatusUnknown:
			return nil, nil, fmt.Errorf("workflow submission history: %s: %w", trackerID, api.ErrReleaseWorkflowEffectOutcomeUnknown)
		case api.WorkflowEffectStatusFailed:
			remaining = append(remaining, trackerID)
		default:
			return nil, nil, fmt.Errorf("workflow submission history: fence for %s has invalid status %q", trackerID, fence.Status)
		}
	}
	return remaining, exclusions, nil
}

func workflowSubmissionTorrentSubject(subject api.UploadSubject) api.TorrentSubject {
	return api.TorrentSubject{
		SourcePath:                subject.SourcePath,
		SourceSize:                subject.SourceSize,
		FileList:                  append([]string(nil), subject.FileList...),
		DiscType:                  subject.DiscType,
		ClientTorrentPath:         subject.ClientTorrentPath,
		ClientTorrentInfoHash:     subject.InfoHash,
		ClientTorrentDataVerified: subject.ClientTorrentDataVerified,
	}
}
