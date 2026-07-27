// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	workflowPrivateResourceKindDupes = "upbrr/workflow-dupe-evidence/v1"
	workflowPrivateResourceKindMedia = "upbrr/workflow-media-artifacts/v1"
)

type persistedWorkflowDupeEvidence struct {
	Summary    api.DupeCheckSummary `json:"summary"`
	Assessment json.RawMessage      `json:"assessment"`
}

func (e workflowDupePrivateEvidence) MarshalPrivateResource() (string, []byte, error) {
	if e.Assessment == nil {
		return "", nil, errors.New("marshal workflow duplicate assessment: evidence is unavailable")
	}
	assessment, err := e.Assessment.MarshalBinary()
	if err != nil {
		return "", nil, fmt.Errorf("marshal workflow duplicate assessment: %w", err)
	}
	payload, err := json.Marshal(persistedWorkflowDupeEvidence{
		Summary:    e.Summary,
		Assessment: assessment,
	})
	if err != nil {
		return "", nil, fmt.Errorf("marshal workflow duplicate evidence: %w", err)
	}
	return workflowPrivateResourceKindDupes, payload, nil
}

func decodeWorkflowDupePrivateEvidence(payload []byte) (any, error) {
	var persisted persistedWorkflowDupeEvidence
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode workflow duplicate evidence: %w", err)
	}
	assessment, err := dupechecking.UnmarshalAssessment(persisted.Assessment)
	if err != nil {
		return nil, fmt.Errorf("decode workflow duplicate assessment: %w", err)
	}
	return workflowDupePrivateEvidence{
		Summary:    persisted.Summary,
		Assessment: assessment,
	}, nil
}

type persistedWorkflowMediaArtifacts struct {
	Screenshots       []api.ScreenshotImage                          `json:"screenshots,omitempty"`
	DVDMenus          []api.DVDMenuCaptureImage                      `json:"dvdMenus,omitempty"`
	ArtifactImages    map[api.PublicResourceID]api.ScreenshotImage   `json:"artifactImages,omitempty"`
	HostedImages      map[api.PublicResourceID]api.UploadedImageLink `json:"hostedImages,omitempty"`
	HostedSources     map[api.PublicResourceID]api.PublicResourceID  `json:"hostedSources,omitempty"`
	ScreenshotSubject api.ScreenshotSubject                          `json:"screenshotSubject"`
	DVDMenuSubject    api.DVDMenuSubject                             `json:"dvdMenuSubject"`
	PendingDeletes    []persistedWorkflowMediaPendingDelete          `json:"pendingDeletes,omitempty"`
}

type persistedWorkflowMediaPendingDelete struct {
	Kind       api.MediaArtifactKind `json:"kind"`
	Path       string                `json:"path"`
	SourcePath string                `json:"sourcePath,omitempty"`
	Host       string                `json:"host,omitempty"`
}

func (a workflowMediaPrivateArtifacts) MarshalPrivateResource() (string, []byte, error) {
	pending := a.pendingDeletes()
	persistedPending := make([]persistedWorkflowMediaPendingDelete, 0, len(pending))
	for _, deletion := range pending {
		persistedPending = append(persistedPending, persistedWorkflowMediaPendingDelete{
			Kind:       deletion.kind,
			Path:       deletion.path,
			SourcePath: deletion.sourcePath,
			Host:       deletion.host,
		})
	}
	payload, err := json.Marshal(persistedWorkflowMediaArtifacts{
		Screenshots:       append([]api.ScreenshotImage(nil), a.Screenshots...),
		DVDMenus:          append([]api.DVDMenuCaptureImage(nil), a.DVDMenus...),
		ArtifactImages:    cloneScreenshotImageMap(a.ArtifactImages),
		HostedImages:      cloneUploadedImageMap(a.HostedImages),
		HostedSources:     cloneHostedSourceMap(a.HostedSources),
		ScreenshotSubject: a.screenshotSubject,
		DVDMenuSubject:    a.dvdMenuSubject,
		PendingDeletes:    persistedPending,
	})
	if err != nil {
		return "", nil, fmt.Errorf("marshal workflow media artifacts: %w", err)
	}
	return workflowPrivateResourceKindMedia, payload, nil
}

func decodeWorkflowMediaPrivateArtifacts(builder workflowMediaBuilder, payload []byte) (any, error) {
	var persisted persistedWorkflowMediaArtifacts
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode workflow media artifacts: %w", err)
	}
	pending := make([]workflowMediaPendingDelete, 0, len(persisted.PendingDeletes))
	for _, deletion := range persisted.PendingDeletes {
		if deletion.Kind == "" || deletion.Path == "" {
			return nil, errors.New("workflow private media contains invalid pending deletion")
		}
		pending = append(pending, workflowMediaPendingDelete{
			kind:       deletion.Kind,
			path:       deletion.Path,
			sourcePath: deletion.SourcePath,
			host:       deletion.Host,
		})
	}
	var commitState *workflowMediaCommitState
	if len(pending) > 0 {
		commitState = &workflowMediaCommitState{pending: pending}
	}
	return workflowMediaPrivateArtifacts{
		Screenshots:       append([]api.ScreenshotImage(nil), persisted.Screenshots...),
		DVDMenus:          append([]api.DVDMenuCaptureImage(nil), persisted.DVDMenus...),
		ArtifactImages:    cloneScreenshotImageMap(persisted.ArtifactImages),
		HostedImages:      cloneUploadedImageMap(persisted.HostedImages),
		HostedSources:     cloneHostedSourceMap(persisted.HostedSources),
		screenshotService: builder.screenshots,
		screenshotSubject: persisted.ScreenshotSubject,
		dvdMenuService:    builder.dvdMenus,
		dvdMenuSubject:    persisted.DVDMenuSubject,
		hostedRepository:  builder.media.repo,
		commitState:       commitState,
	}, nil
}

func workflowPrivateResourceCodecs(builder workflowMediaBuilder) []releaseworkflow.PrivateResourceCodec {
	return []releaseworkflow.PrivateResourceCodec{
		{
			Kind:   workflowPrivateResourceKindDupes,
			Decode: decodeWorkflowDupePrivateEvidence,
		},
		{
			Kind: workflowPrivateResourceKindMedia,
			Decode: func(payload []byte) (any, error) {
				return decodeWorkflowMediaPrivateArtifacts(builder, payload)
			},
		},
	}
}
