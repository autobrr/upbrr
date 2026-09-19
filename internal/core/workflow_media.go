// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type workflowMediaSubjectResolver interface {
	ResolveScreenshotSubject(context.Context, api.MediaPlanInput) (api.ScreenshotSubject, error)
	ResolveDVDMenuSubject(context.Context, api.MediaPlanInput) (api.DVDMenuSubject, error)
}

type workflowMediaBuilder struct {
	config      config.Config
	resolver    workflowMediaSubjectResolver
	screenshots mediaScreenshotService
	dvdMenus    mediaDVDMenuService
	media       *mediaModule
}

var errReusableMediaUnavailable = errors.New("workflow media reusable artifact is unavailable")

type discFrameKey struct {
	discID string
	index  int
}

// Plan resolves screenshot suggestions only when projections require screenshots.
// It retains DVD-menu requirements for Build without resolving a screenshot source.
func (b workflowMediaBuilder) Plan(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	_ time.Time,
) (api.MediaPlan, error) {
	requirements := make([]api.MediaCaptureRequirement, 0, len(projections.Projections))
	for _, projection := range projections.Projections {
		requirements = append(requirements, api.MediaCaptureRequirement{
			TrackerID:       projection.TrackerID,
			ScreenshotCount: projection.Artifacts.ScreenshotCount,
			DVDMenuCount:    projection.Artifacts.DVDMenuCount,
			Purpose:         api.ScreenshotPurposeFinal,
		})
	}
	screenshotCount, _ := projectedMediaRequirements(projections.Projections)
	if screenshotCount <= 0 {
		return api.MediaPlan{Requirements: requirements}, nil
	}
	if b.resolver == nil || b.screenshots == nil {
		return api.MediaPlan{}, errors.New("workflow media plan service is unavailable")
	}
	subject, err := b.resolver.ResolveScreenshotSubject(ctx, api.MediaPlanInput{
		Release: release,
		Count:   screenshotCount,
		Purpose: api.ScreenshotPurposeFinal,
	})
	if err != nil {
		return api.MediaPlan{}, fmt.Errorf("workflow media resolve plan subject: %w", err)
	}
	plan, err := b.screenshots.Plan(ctx, subject, screenshotCount)
	if err != nil {
		return api.MediaPlan{}, fmt.Errorf("workflow media plan: %w", err)
	}
	discs := make([]api.MediaDiscPlan, 0, len(plan.Discs))
	for _, disc := range plan.Discs {
		discs = append(discs, api.MediaDiscPlan{
			DiscID:              disc.DiscID,
			DiscName:            disc.DiscName,
			DurationSeconds:     disc.DurationSeconds,
			FrameRate:           disc.FrameRate,
			SuggestedSelections: append([]api.ScreenshotSelection(nil), disc.SuggestedSelections...),
		})
	}
	return api.MediaPlan{
		DurationSeconds:     plan.DurationSeconds,
		FrameRate:           plan.FrameRate,
		DiscType:            plan.DiscType,
		SuggestedSelections: append([]api.ScreenshotSelection(nil), plan.SuggestedSelections...),
		Discs:               discs,
		Requirements:        requirements,
	}, nil
}

func (b workflowMediaBuilder) PreviewFrame(
	ctx context.Context,
	release api.ReleaseRef,
	discID string,
	timestampSeconds float64,
) (releaseworkflow.MediaPreviewContent, error) {
	if b.resolver == nil || b.screenshots == nil {
		return releaseworkflow.MediaPreviewContent{}, errors.New("workflow frame preview service is unavailable")
	}
	subject, err := b.resolver.ResolveScreenshotSubject(ctx, api.MediaPlanInput{
		Release: release,
		Purpose: api.ScreenshotPurposePreview,
	})
	if err != nil {
		return releaseworkflow.MediaPreviewContent{}, fmt.Errorf("workflow media resolve preview subject: %w", err)
	}
	preview, err := b.screenshots.PreviewFrame(ctx, subject, discID, timestampSeconds)
	if err != nil {
		return releaseworkflow.MediaPreviewContent{}, fmt.Errorf("workflow media preview frame: %w", err)
	}
	contentType := http.DetectContentType(preview.ImageBytes)
	if !strings.HasPrefix(contentType, "image/") {
		contentType = "application/octet-stream"
	}
	return releaseworkflow.MediaPreviewContent{
		DiscID:      preview.DiscID,
		DiscName:    preview.DiscName,
		Bytes:       append([]byte(nil), preview.ImageBytes...),
		ContentType: contentType,
		Width:       preview.Width,
		Height:      preview.Height,
	}, nil
}

type workflowMediaPrivateArtifacts struct {
	Screenshots       []api.ScreenshotImage
	DVDMenus          []api.DVDMenuCaptureImage
	ArtifactImages    map[api.PublicResourceID]api.ScreenshotImage
	DVDMenuImages     map[api.PublicResourceID]api.DVDMenuCaptureImage
	HostedImages      map[api.PublicResourceID]api.UploadedImageLink
	HostedSources     map[api.PublicResourceID]api.PublicResourceID
	screenshotService mediaScreenshotService
	screenshotSubject api.ScreenshotSubject
	dvdMenuService    mediaDVDMenuService
	dvdMenuSubject    api.DVDMenuSubject
	hostedRepository  mediaRepository
	mediaReuse        api.MediaReuseRepository
	commitState       *workflowMediaCommitState
}

type workflowMediaPendingDelete struct {
	kind    api.MediaArtifactKind
	path    string
	binding api.PreparedMediaBinding
	host    string
}

type workflowMediaCommitState struct {
	mu      sync.Mutex
	pending []workflowMediaPendingDelete
}

func (a workflowMediaPrivateArtifacts) OpenArtifact(
	ctx context.Context,
	snapshot api.MediaArtifactSet,
	artifactID api.PublicResourceID,
) (releaseworkflow.MediaArtifactContent, error) {
	if err := ctx.Err(); err != nil {
		return releaseworkflow.MediaArtifactContent{}, fmt.Errorf("workflow media read canceled: %w", err)
	}
	image, err := a.imageForArtifact(snapshot, artifactID)
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, err
	}
	data, err := os.ReadFile(image.Path)
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, fmt.Errorf("workflow media read artifact: %w", err)
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(image.Path)))
	if !strings.HasPrefix(contentType, "image/") {
		contentType = "application/octet-stream"
	}
	return releaseworkflow.MediaArtifactContent{
		Body:        io.NopCloser(bytes.NewReader(data)),
		ContentType: contentType,
	}, nil
}

func (a workflowMediaPrivateArtifacts) DeleteArtifacts(
	ctx context.Context,
	snapshot api.MediaArtifactSet,
	artifactIDs []api.PublicResourceID,
) (releaseworkflow.RetainedMediaResource, error) {
	deleted := make(map[api.PublicResourceID]struct{}, len(artifactIDs))
	for _, artifactID := range artifactIDs {
		deleted[artifactID] = struct{}{}
	}
	result := workflowMediaPrivateArtifacts{
		screenshotService: a.screenshotService,
		screenshotSubject: a.screenshotSubject,
		dvdMenuService:    a.dvdMenuService,
		dvdMenuSubject:    a.dvdMenuSubject,
		hostedRepository:  a.hostedRepository,
		mediaReuse:        a.mediaReuse,
		ArtifactImages:    cloneScreenshotImageMap(a.ArtifactImages),
		DVDMenuImages:     cloneDVDMenuImageMap(a.DVDMenuImages),
		HostedImages:      cloneUploadedImageMap(a.HostedImages),
		HostedSources:     cloneHostedSourceMap(a.HostedSources),
	}
	pending := a.pendingDeletes()
	screenshotIndex, menuIndex := 0, 0
	for _, artifact := range snapshot.Artifacts {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("workflow media delete canceled: %w", err)
		}
		var image api.ScreenshotImage
		var dvdMenu api.DVDMenuCaptureImage
		switch artifact.Kind {
		case api.MediaArtifactScreenshot:
			if screenshotIndex >= len(a.Screenshots) {
				return nil, errors.New("workflow media screenshot resource is incomplete")
			}
			image = a.Screenshots[screenshotIndex]
			screenshotIndex++
		case api.MediaArtifactDVDMenu:
			if menuIndex >= len(a.DVDMenus) {
				return nil, errors.New("workflow media DVD menu resource is incomplete")
			}
			dvdMenu = a.DVDMenus[menuIndex]
			image = dvdMenu.ScreenshotImage
			menuIndex++
		case api.MediaArtifactHostedImage:
			if _, remove := deleted[artifact.ID]; remove {
				return nil, errors.New("workflow hosted image cannot be deleted as a local artifact")
			}
			continue
		}
		if _, remove := deleted[artifact.ID]; remove {
			pending = append(pending, workflowMediaPendingDelete{
				kind:    artifact.Kind,
				path:    image.Path,
				binding: workflowMediaBindingForKind(a, artifact.Kind),
			})
			delete(result.ArtifactImages, artifact.ID)
			delete(result.DVDMenuImages, artifact.ID)
			continue
		}
		switch artifact.Kind {
		case api.MediaArtifactScreenshot:
			result.Screenshots = append(result.Screenshots, image)
		case api.MediaArtifactDVDMenu:
			result.DVDMenus = append(result.DVDMenus, dvdMenu)
		case api.MediaArtifactHostedImage:
		}
	}
	result.commitState = &workflowMediaCommitState{pending: pending}
	return result, nil
}

func (a workflowMediaPrivateArtifacts) pendingDeletes() []workflowMediaPendingDelete {
	if a.commitState == nil {
		return nil
	}
	a.commitState.mu.Lock()
	defer a.commitState.mu.Unlock()
	return append([]workflowMediaPendingDelete(nil), a.commitState.pending...)
}

func (a workflowMediaPrivateArtifacts) Commit(ctx context.Context) error {
	if a.commitState == nil {
		return nil
	}
	a.commitState.mu.Lock()
	defer a.commitState.mu.Unlock()
	for len(a.commitState.pending) > 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("workflow media deletion canceled: %w", err)
		}
		deletion := a.commitState.pending[0]
		var err error
		switch deletion.kind {
		case api.MediaArtifactScreenshot:
			if a.screenshotService == nil {
				return errors.New("workflow screenshot deletion service is unavailable")
			}
			err = a.screenshotService.Delete(ctx, a.screenshotSubject, deletion.path)
		case api.MediaArtifactDVDMenu:
			if a.dvdMenuService == nil {
				return errors.New("workflow DVD menu deletion service is unavailable")
			}
			err = a.dvdMenuService.Delete(ctx, a.dvdMenuSubject, deletion.path)
		case api.MediaArtifactHostedImage:
			if a.hostedRepository == nil {
				return errors.New("workflow hosted-image repository is unavailable")
			}
			err = a.hostedRepository.DeleteUploadedImage(ctx, deletion.binding, deletion.path, deletion.host)
		}
		if err != nil {
			return fmt.Errorf("workflow media delete artifact: %w", err)
		}
		if deletion.kind == api.MediaArtifactScreenshot || deletion.kind == api.MediaArtifactDVDMenu {
			if a.mediaReuse != nil && deletion.binding.Valid() {
				if err := a.mediaReuse.DeleteReusableMediaAssets(ctx, deletion.binding, []string{deletion.path}); err != nil {
					return fmt.Errorf("workflow media tombstone deleted artifact: %w", err)
				}
			}
		}
		a.commitState.pending = a.commitState.pending[1:]
	}
	return nil
}

func workflowMediaBindingForKind(artifacts workflowMediaPrivateArtifacts, kind api.MediaArtifactKind) api.PreparedMediaBinding {
	if kind == api.MediaArtifactDVDMenu && artifacts.dvdMenuSubject.MediaBinding.Valid() {
		return artifacts.dvdMenuSubject.MediaBinding
	}
	if artifacts.screenshotSubject.MediaBinding.Valid() {
		return artifacts.screenshotSubject.MediaBinding
	}
	return artifacts.dvdMenuSubject.MediaBinding
}

func (a workflowMediaPrivateArtifacts) imageForArtifact(
	snapshot api.MediaArtifactSet,
	artifactID api.PublicResourceID,
) (api.ScreenshotImage, error) {
	if image, ok := a.ArtifactImages[artifactID]; ok {
		return image, nil
	}
	screenshotIndex, menuIndex := 0, 0
	for _, artifact := range snapshot.Artifacts {
		switch artifact.Kind {
		case api.MediaArtifactScreenshot:
			if screenshotIndex >= len(a.Screenshots) {
				return api.ScreenshotImage{}, errors.New("workflow media screenshot resource is incomplete")
			}
			image := a.Screenshots[screenshotIndex]
			screenshotIndex++
			if artifact.ID == artifactID {
				return image, nil
			}
		case api.MediaArtifactDVDMenu:
			if menuIndex >= len(a.DVDMenus) {
				return api.ScreenshotImage{}, errors.New("workflow media DVD menu resource is incomplete")
			}
			image := a.DVDMenus[menuIndex].ScreenshotImage
			menuIndex++
			if artifact.ID == artifactID {
				return image, nil
			}
		case api.MediaArtifactHostedImage:
			if artifact.ID == artifactID {
				return api.ScreenshotImage{}, errors.New("workflow hosted image has no local content")
			}
		}
	}
	return api.ScreenshotImage{}, errors.New("workflow media artifact is unavailable")
}

func indexWorkflowMediaArtifacts(a *workflowMediaPrivateArtifacts, snapshot api.MediaArtifactSet) {
	if a.ArtifactImages == nil {
		a.ArtifactImages = make(map[api.PublicResourceID]api.ScreenshotImage)
	}
	if a.DVDMenuImages == nil {
		a.DVDMenuImages = make(map[api.PublicResourceID]api.DVDMenuCaptureImage)
	}
	screenshotIndex, menuIndex := 0, 0
	for _, artifact := range snapshot.Artifacts {
		switch artifact.Kind {
		case api.MediaArtifactScreenshot:
			if screenshotIndex < len(a.Screenshots) {
				a.ArtifactImages[artifact.ID] = a.Screenshots[screenshotIndex]
			}
			screenshotIndex++
		case api.MediaArtifactDVDMenu:
			if menuIndex < len(a.DVDMenus) {
				a.ArtifactImages[artifact.ID] = a.DVDMenus[menuIndex].ScreenshotImage
				a.DVDMenuImages[artifact.ID] = a.DVDMenus[menuIndex]
			}
			menuIndex++
		case api.MediaArtifactHostedImage:
		}
	}
}

func cloneScreenshotImageMap(values map[api.PublicResourceID]api.ScreenshotImage) map[api.PublicResourceID]api.ScreenshotImage {
	cloned := make(map[api.PublicResourceID]api.ScreenshotImage, len(values))
	maps.Copy(cloned, values)
	return cloned
}

func cloneUploadedImageMap(values map[api.PublicResourceID]api.UploadedImageLink) map[api.PublicResourceID]api.UploadedImageLink {
	cloned := make(map[api.PublicResourceID]api.UploadedImageLink, len(values))
	maps.Copy(cloned, values)
	return cloned
}

func cloneDVDMenuImageMap(values map[api.PublicResourceID]api.DVDMenuCaptureImage) map[api.PublicResourceID]api.DVDMenuCaptureImage {
	cloned := make(map[api.PublicResourceID]api.DVDMenuCaptureImage, len(values))
	maps.Copy(cloned, values)
	return cloned
}

func cloneHostedSourceMap(values map[api.PublicResourceID]api.PublicResourceID) map[api.PublicResourceID]api.PublicResourceID {
	cloned := make(map[api.PublicResourceID]api.PublicResourceID, len(values))
	maps.Copy(cloned, values)
	return cloned
}

func cloneWorkflowMediaPrivateArtifacts(value workflowMediaPrivateArtifacts) workflowMediaPrivateArtifacts {
	return workflowMediaPrivateArtifacts{
		Screenshots:       append([]api.ScreenshotImage(nil), value.Screenshots...),
		DVDMenus:          append([]api.DVDMenuCaptureImage(nil), value.DVDMenus...),
		ArtifactImages:    cloneScreenshotImageMap(value.ArtifactImages),
		DVDMenuImages:     cloneDVDMenuImageMap(value.DVDMenuImages),
		HostedImages:      cloneUploadedImageMap(value.HostedImages),
		HostedSources:     cloneHostedSourceMap(value.HostedSources),
		screenshotService: value.screenshotService,
		screenshotSubject: value.screenshotSubject,
		dvdMenuService:    value.dvdMenuService,
		dvdMenuSubject:    value.dvdMenuSubject,
		hostedRepository:  value.hostedRepository,
		mediaReuse:        value.mediaReuse,
		commitState:       &workflowMediaCommitState{pending: value.pendingDeletes()},
	}
}

func (b workflowMediaBuilder) Build(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	instructions api.MediaCaptureInstructions,
	_ time.Time,
) (api.MediaArtifactSet, any, error) {
	if err := ctx.Err(); err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media capture: %w", err)
	}
	if b.resolver == nil {
		return api.MediaArtifactSet{}, nil, errors.New("workflow media capture: subject resolver is required")
	}
	requirementsFingerprint, err := workflowMediaRequirementsFingerprint(projections.Projections)
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	captureFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Release      api.ReleaseRef
		ProjectionID api.TrackerReleaseProjectionSetID
		Revision     api.WorkflowRevision
		Instructions api.MediaCaptureInstructions
		Requirements api.WorkflowFingerprint
	}{release, projections.ID, projections.Revision, instructions, requirementsFingerprint})
	if err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media capture fingerprint: %w", err)
	}
	snapshot := api.MediaArtifactSet{
		CaptureFingerprint:      captureFingerprint,
		RequirementsFingerprint: requirementsFingerprint,
		Artifacts:               []api.MediaArtifact{},
		Status:                  api.StageStatusCompleted,
	}
	projectedScreenshots, projectedDVDMenus := projectedMediaRequirements(projections.Projections)
	screenshotCount, dvdMenuCount := 0, 0
	switch instructions.Purpose {
	case api.ScreenshotPurposeMenu:
	case api.ScreenshotPurposeFinal:
		screenshotCount = max(projectedScreenshots, instructions.ScreenshotCount)
		screenshotCount = max(screenshotCount, len(instructions.Selections))
		screenshotCount = max(screenshotCount, len(instructions.ManualFrames))
	case api.ScreenshotPurposePreview:
		screenshotCount = max(projectedScreenshots, instructions.ScreenshotCount)
	default:
		screenshotCount = max(projectedScreenshots, instructions.ScreenshotCount)
	}
	if instructions.CaptureDVDMenus {
		dvdMenuCount = instructions.MaxDVDMenuItems
		if dvdMenuCount <= 0 {
			dvdMenuCount = b.config.ScreenshotHandling.ResolvedMaxMenuItems()
		}
	}
	if screenshotCount == 0 && dvdMenuCount == 0 && projectedDVDMenus == 0 {
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:   "media",
			ItemID:  "media",
			Kind:    "media",
			Label:   "Media capture",
			Status:  api.StageStatusSkipped,
			Message: "No media capture is required.",
		})
		snapshot.Status = api.StageStatusSkipped
		return snapshot, workflowMediaPrivateArtifacts{
			ArtifactImages: make(map[api.PublicResourceID]api.ScreenshotImage),
			DVDMenuImages:  make(map[api.PublicResourceID]api.DVDMenuCaptureImage),
			HostedImages:   make(map[api.PublicResourceID]api.UploadedImageLink),
			HostedSources:  make(map[api.PublicResourceID]api.PublicResourceID),
		}, nil
	}
	privateArtifacts := workflowMediaPrivateArtifacts{
		screenshotService: b.screenshots,
		dvdMenuService:    b.dvdMenus,
		ArtifactImages:    make(map[api.PublicResourceID]api.ScreenshotImage),
		DVDMenuImages:     make(map[api.PublicResourceID]api.DVDMenuCaptureImage),
		HostedImages:      make(map[api.PublicResourceID]api.UploadedImageLink),
		HostedSources:     make(map[api.PublicResourceID]api.PublicResourceID),
	}
	if b.media != nil {
		privateArtifacts.hostedRepository = b.media.repo
		privateArtifacts.mediaReuse = b.media.mediaReuse
	}
	if screenshotCount > 0 {
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:   "screenshots",
			ItemID:  "screenshots",
			Kind:    "media",
			Label:   "Screenshots",
			Status:  api.StageStatusRunning,
			Total:   screenshotCount,
			Message: "Capturing screenshots.",
		})
		if b.screenshots == nil {
			return failedMediaSnapshot(snapshot, "Screenshot capture service is unavailable."), privateArtifacts, nil
		}
		purpose := instructions.Purpose
		if purpose == "" {
			purpose = api.ScreenshotPurposeFinal
		}
		input := api.MediaPlanInput{
			Release: release,
			Count:   screenshotCount,
			Purpose: purpose,
			Options: api.ScreenshotOverrides{ManualFrames: append([]int(nil), instructions.ManualFrames...)},
		}
		subject, err := b.resolver.ResolveScreenshotSubject(ctx, input)
		if err != nil {
			return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media resolve screenshot subject: %w", err)
		}
		privateArtifacts.screenshotSubject = subject
		selections := append([]api.ScreenshotSelection(nil), instructions.Selections...)
		var existingScreenshots []api.ScreenshotImage
		plan, err := b.screenshots.Plan(ctx, subject, screenshotCount)
		if err != nil {
			if ctx.Err() != nil {
				return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media screenshot plan: %w", ctx.Err())
			}
			return failedMediaSnapshot(snapshot, "Screenshot planning failed. Retry media capture."), privateArtifacts, nil
		}
		if len(selections) == 0 {
			selections = append(selections, plan.SuggestedSelections...)
			existingScreenshots = append(existingScreenshots, plan.ExistingScreenshots...)
		} else {
			var mergeErr error
			selections, existingScreenshots, mergeErr = mergeManualSelectionsWithDiscPlan(selections, plan)
			if mergeErr != nil {
				return failedMediaSnapshot(snapshot, "Screenshot selections must identify a prepared disc."), privateArtifacts, nil
			}
		}
		captureTotal := max(screenshotCount, len(selections)+len(existingScreenshots))
		if len(selections) == 0 && len(existingScreenshots) == 0 {
			snapshot.Status = api.StageStatusBlocked
			snapshot.RequiredActions = []api.RequiredAction{{
				Kind:   api.RequiredActionProvideTrackerInput,
				Prompt: "Choose screenshot frames, then retry media capture.",
			}}
			return snapshot, privateArtifacts, nil
		}
		capture := api.ScreenshotResult{Purpose: purpose}
		if len(selections) > 0 {
			capture, err = b.screenshots.Capture(ctx, subject, selections, purpose)
			if err != nil {
				if ctx.Err() != nil {
					return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media screenshot capture: %w", ctx.Err())
				}
				if errors.Is(err, internalerrors.ErrFrameCorruption) {
					api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
						Phase:   "screenshots",
						ItemID:  "screenshots",
						Kind:    "media",
						Label:   "Screenshots",
						Status:  api.StageStatusFailed,
						Total:   screenshotCount,
						Message: "Screenshot frame corruption detected. Repair source media before retrying.",
					})
					return failedMediaSnapshot(snapshot, "Screenshot frame corruption detected. Repair source media before retrying."), privateArtifacts, nil
				}
				return failedMediaSnapshot(snapshot, "Screenshot capture failed. Retry media capture."), privateArtifacts, nil
			}
		}
		images := mergeWorkflowScreenshotImages(existingScreenshots, capture.Images, plan.Discs, captureTotal)
		privateArtifacts.Screenshots = append(privateArtifacts.Screenshots, images...)
		captureStatus := api.StageStatusCompleted
		captureMessage := "Screenshot capture complete."
		if len(capture.Errors) > 0 {
			captureStatus = api.StageStatusPartial
			captureMessage = "Screenshot capture completed with partial coverage."
		}
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:     "screenshots",
			ItemID:    "screenshots",
			Kind:      "media",
			Label:     "Screenshots",
			Status:    captureStatus,
			Completed: len(images),
			Total:     captureTotal,
			Message:   captureMessage,
		})
		for index, image := range images {
			snapshot.Artifacts = append(
				snapshot.Artifacts,
				publicMediaArtifact(captureFingerprint, "screenshot", index, api.MediaArtifactScreenshot, purpose, image),
			)
		}
	}
	if dvdMenuCount > 0 {
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:   "dvd_menus",
			ItemID:  "dvd_menus",
			Kind:    "media",
			Label:   "DVD menus",
			Status:  api.StageStatusRunning,
			Total:   dvdMenuCount,
			Message: "Capturing DVD menus.",
		})
		if b.dvdMenus == nil {
			api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
				Phase:   "dvd_menus",
				ItemID:  "dvd_menus",
				Kind:    "media",
				Label:   "DVD menus",
				Status:  api.StageStatusFailed,
				Total:   dvdMenuCount,
				Message: "DVD menu capture service is unavailable.",
			})
			return failedMediaSnapshot(snapshot, "DVD menu capture service is unavailable."), privateArtifacts, nil
		}
		input := api.MediaPlanInput{Release: release}
		subject, err := b.resolver.ResolveDVDMenuSubject(ctx, input)
		if err != nil {
			return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media resolve DVD menu subject: %w", err)
		}
		privateArtifacts.dvdMenuSubject = subject
		if strings.EqualFold(strings.TrimSpace(subject.DiscType), "DVD") {
			capture, err := b.dvdMenus.Capture(ctx, subject, dvdMenuCount)
			if err != nil {
				if ctx.Err() != nil {
					return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media DVD menu capture: %w", ctx.Err())
				}
				api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
					Phase:   "dvd_menus",
					ItemID:  "dvd_menus",
					Kind:    "media",
					Label:   "DVD menus",
					Status:  api.StageStatusFailed,
					Total:   dvdMenuCount,
					Message: "DVD menu capture failed.",
				})
				return failedMediaSnapshot(snapshot, "DVD menu capture failed. Retry media capture."), privateArtifacts, nil
			}
			privateArtifacts.DVDMenus = append(privateArtifacts.DVDMenus, capture.Images...)
			captureStatus := api.StageStatusCompleted
			captureMessage := "DVD menu capture complete."
			if capture.Partial || len(capture.Warnings) > 0 {
				captureStatus = api.StageStatusPartial
				captureMessage = "DVD menu capture completed with partial coverage."
			}
			api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
				Phase:     "dvd_menus",
				ItemID:    "dvd_menus",
				Kind:      "media",
				Label:     "DVD menus",
				Status:    captureStatus,
				Completed: len(capture.Images),
				Total:     dvdMenuCount,
				Message:   captureMessage,
			})
			base := len(snapshot.Artifacts)
			for index, image := range capture.Images {
				artifact := publicMediaArtifact(
					captureFingerprint,
					"dvd-menu",
					base+index,
					api.MediaArtifactDVDMenu,
					api.ScreenshotPurposeMenu,
					image.ScreenshotImage,
				)
				artifact.Source = api.ScreenshotSelectionSourceDVDMenu
				snapshot.Artifacts = append(snapshot.Artifacts, artifact)
			}
		}
	}
	indexWorkflowMediaArtifacts(&privateArtifacts, snapshot)
	if err := b.persistReusableWorkflowMedia(ctx, snapshot, privateArtifacts); err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	applyWorkflowMediaMinimums(&snapshot, projectedScreenshots, projectedDVDMenus)
	return snapshot, privateArtifacts, nil
}

func applyWorkflowMediaMinimums(snapshot *api.MediaArtifactSet, requiredScreenshots, requiredMenus int) {
	selectedScreenshots, selectedMenus := 0, 0
	for _, artifact := range snapshot.Artifacts {
		if !artifact.Selected {
			continue
		}
		switch artifact.Kind {
		case api.MediaArtifactScreenshot:
			selectedScreenshots++
		case api.MediaArtifactDVDMenu:
			selectedMenus++
		case api.MediaArtifactHostedImage:
		}
	}
	if selectedScreenshots >= requiredScreenshots && selectedMenus >= requiredMenus {
		return
	}
	snapshot.Status = api.StageStatusBlocked
	snapshot.RequiredActions = append(snapshot.RequiredActions, api.RequiredAction{
		Kind:   api.RequiredActionProvideTrackerInput,
		Prompt: "Capture or select the required release images before continuing.",
	})
}

func mergeWorkflowScreenshotImages(
	existing []api.ScreenshotImage,
	captured []api.ScreenshotImage,
	discs []api.ScreenshotDiscPlan,
	limit int,
) []api.ScreenshotImage {
	images := make([]api.ScreenshotImage, 0, len(existing)+len(captured))
	byIndex := make(map[discFrameKey]int, cap(images))
	appendImages := func(values []api.ScreenshotImage) {
		for _, image := range values {
			key := discFrameKey{discID: image.DiscID, index: image.Index}
			if existingIndex, ok := byIndex[key]; ok {
				images[existingIndex] = image
				continue
			}
			byIndex[key] = len(images)
			images = append(images, image)
		}
	}
	appendImages(existing)
	appendImages(captured)
	discOrder := make(map[string]int, len(discs))
	for index, disc := range discs {
		discOrder[disc.DiscID] = index
	}
	slices.SortStableFunc(images, func(left, right api.ScreenshotImage) int {
		leftDisc, leftFound := discOrder[left.DiscID]
		rightDisc, rightFound := discOrder[right.DiscID]
		switch {
		case leftFound && !rightFound:
			return -1
		case !leftFound && rightFound:
			return 1
		case leftDisc < rightDisc:
			return -1
		case leftDisc > rightDisc:
			return 1
		case left.Index < right.Index:
			return -1
		case left.Index > right.Index:
			return 1
		default:
			return strings.Compare(left.Path, right.Path)
		}
	})
	if limit > 0 && len(images) > limit {
		images = images[:limit]
	}
	return images
}

func mergeManualSelectionsWithDiscPlan(
	selections []api.ScreenshotSelection,
	plan api.ScreenshotPlan,
) ([]api.ScreenshotSelection, []api.ScreenshotImage, error) {
	if len(plan.Discs) == 0 {
		return selections, nil, nil
	}
	discIDs := make(map[string]struct{}, len(plan.Discs))
	for _, disc := range plan.Discs {
		discIDs[disc.DiscID] = struct{}{}
	}
	provided := make(map[string]struct{}, len(selections))
	replaced := make(map[discFrameKey]struct{}, len(selections))
	for index := range selections {
		discID := strings.TrimSpace(selections[index].DiscID)
		if discID == "" && len(plan.Discs) == 1 {
			discID = plan.Discs[0].DiscID
			selections[index].DiscID = discID
		}
		if _, ok := discIDs[discID]; !ok {
			return nil, nil, internalerrors.ErrInvalidInput
		}
		provided[discID] = struct{}{}
		replaced[discFrameKey{discID: discID, index: selections[index].Index}] = struct{}{}
	}
	for _, disc := range plan.Discs {
		if _, ok := provided[disc.DiscID]; ok {
			continue
		}
		selections = append(selections, disc.SuggestedSelections...)
	}
	existing := make([]api.ScreenshotImage, 0, len(plan.ExistingScreenshots))
	for _, image := range plan.ExistingScreenshots {
		if _, ok := replaced[discFrameKey{discID: image.DiscID, index: image.Index}]; !ok {
			existing = append(existing, image)
		}
	}
	return selections, existing, nil
}

func (b workflowMediaBuilder) BuildIncremental(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	instructions api.MediaCaptureInstructions,
	existing *api.MediaArtifactSet,
	privateExisting any,
	now time.Time,
) (api.MediaArtifactSet, releaseworkflow.RetainedMediaResource, error) {
	var retained workflowMediaPrivateArtifacts
	if existing != nil {
		var ok bool
		retained, ok = privateExisting.(workflowMediaPrivateArtifacts)
		if !ok {
			return api.MediaArtifactSet{}, nil, errors.New("workflow media retained resource is incompatible")
		}
		retained = cloneWorkflowMediaPrivateArtifacts(retained)
	}
	if instructions.Purpose == api.ScreenshotPurposeFinal && len(instructions.Selections) > 0 && existing != nil {
		requestedScreenshotCount := max(instructions.ScreenshotCount, len(instructions.Selections))
		indexes := make(map[discFrameKey]struct{})
		for _, artifact := range existing.Artifacts {
			if artifact.Kind == api.MediaArtifactScreenshot {
				indexes[discFrameKey{discID: artifact.DiscID, index: artifact.Index}] = struct{}{}
			}
		}
		filtered := make([]api.ScreenshotSelection, 0, len(instructions.Selections))
		for _, selection := range instructions.Selections {
			key := discFrameKey{discID: selection.DiscID, index: selection.Index}
			if _, exists := indexes[key]; !exists {
				filtered = append(filtered, selection)
			}
		}
		instructions.Selections = filtered
		instructions.ScreenshotCount = len(filtered)
		projectedScreenshots, projectedDVDMenus := projectedMediaRequirements(projections.Projections)
		if len(filtered) == 0 && !instructions.CaptureDVDMenus &&
			countMediaArtifacts(existing.Artifacts, api.MediaArtifactScreenshot) >= max(projectedScreenshots, requestedScreenshotCount) &&
			countMediaArtifacts(existing.Artifacts, api.MediaArtifactDVDMenu) >= projectedDVDMenus {
			snapshot, cloneErr := existing.Clone()
			if cloneErr != nil {
				return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media clone existing capture: %w", cloneErr)
			}
			return snapshot, retained, nil
		}
	}
	captured, privateCaptured, err := b.Build(ctx, release, projections, instructions, now)
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	capturedRetained, ok := privateCaptured.(workflowMediaPrivateArtifacts)
	if !ok {
		return api.MediaArtifactSet{}, nil, errors.New("workflow media capture resource is incompatible")
	}
	if existing == nil {
		return captured, capturedRetained, nil
	}
	_, projectedDVDMenus := projectedMediaRequirements(projections.Projections)
	optionalMenuAttempt := instructions.CaptureDVDMenus && projectedDVDMenus == 0
	capturedMenuCount := countMediaArtifacts(captured.Artifacts, api.MediaArtifactDVDMenu)
	if optionalMenuAttempt && capturedMenuCount == 0 && len(captured.Failures) > 0 {
		captured.Status = existing.Status
		captured.RequiredActions = nil
	}
	combined := *existing
	combined.Artifacts = append([]api.MediaArtifact(nil), existing.Artifacts...)
	combined.HostAttempts = append([]api.HostedImageAttempt(nil), existing.HostAttempts...)
	combined.FailedHosts = append([]string(nil), existing.FailedHosts...)
	if instructions.CaptureDVDMenus && capturedMenuCount > 0 {
		removeAutomaticDVDMenuArtifacts(&combined, &retained)
	}
	noOp := len(captured.Artifacts) == 0 && len(captured.Failures) == 0 && len(captured.RequiredActions) == 0
	if noOp {
		combined.Failures = append([]api.WorkflowFailure(nil), existing.Failures...)
		combined.RequiredActions = append([]api.RequiredAction(nil), existing.RequiredActions...)
	} else {
		combined.Failures = append([]api.WorkflowFailure(nil), captured.Failures...)
		combined.RequiredActions = append([]api.RequiredAction(nil), captured.RequiredActions...)
	}
	known := make(map[api.PublicResourceID]struct{}, len(combined.Artifacts))
	maxOrder := -1
	for _, artifact := range combined.Artifacts {
		known[artifact.ID] = struct{}{}
		maxOrder = max(maxOrder, artifact.Order)
	}
	for _, artifact := range captured.Artifacts {
		if _, duplicate := known[artifact.ID]; duplicate {
			continue
		}
		maxOrder++
		artifact.Order = maxOrder
		combined.Artifacts = append(combined.Artifacts, artifact)
		known[artifact.ID] = struct{}{}
	}
	combined.RequirementsFingerprint = captured.RequirementsFingerprint
	if !noOp {
		combined.Status = captured.Status
	}
	combined.CaptureFingerprint = captured.CaptureFingerprint
	maps.Copy(retained.ArtifactImages, capturedRetained.ArtifactImages)
	maps.Copy(retained.DVDMenuImages, capturedRetained.DVDMenuImages)
	if len(capturedRetained.Screenshots) > 0 {
		retained.screenshotService = capturedRetained.screenshotService
		retained.screenshotSubject = capturedRetained.screenshotSubject
	}
	if len(capturedRetained.DVDMenus) > 0 {
		retained.dvdMenuService = capturedRetained.dvdMenuService
		retained.dvdMenuSubject = capturedRetained.dvdMenuSubject
	}
	rebuildWorkflowMediaLocalSlices(&retained, combined)
	return combined, retained, nil
}

// RestoreCompatible re-materializes a fresh current-generation media snapshot
// from independently verified reusable assets. It reads no legacy weak media
// records and does not reuse a prior workflow's opaque artifact authority.
func (b workflowMediaBuilder) RestoreCompatible(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	_ time.Time,
) (api.MediaArtifactSet, releaseworkflow.RetainedMediaResource, error) {
	if err := ctx.Err(); err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media restore: %w", err)
	}
	if b.media == nil || b.media.repo == nil || b.resolver == nil {
		return api.MediaArtifactSet{}, nil, errors.New("workflow media restore service is unavailable")
	}
	repository := b.media.mediaReuse
	if repository == nil {
		return api.MediaArtifactSet{}, nil, nil
	}
	subject, err := b.resolver.ResolveScreenshotSubject(ctx, api.MediaPlanInput{
		Release: release,
		Purpose: api.ScreenshotPurposeFinal,
	})
	if err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media resolve restore subject: %w", err)
	}
	binding := subject.MediaBinding
	if !binding.Valid() || !binding.CompatibilityKey.Valid() {
		return api.MediaArtifactSet{}, nil, errors.New("workflow media restore requires a verified source compatibility key")
	}
	assets, err := repository.LoadReusableMediaAssets(ctx, binding.CompatibilityKey)
	if err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media load reusable assets: %w", err)
	}
	if len(assets) == 0 {
		return api.MediaArtifactSet{}, nil, nil
	}
	snapshot, retained, err := b.mediaMutationBase(release, projections, nil, nil)
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	retained.screenshotService = b.screenshots
	retained.screenshotSubject = subject
	retained.hostedRepository = b.media.repo
	retained.mediaReuse = b.media.mediaReuse
	discNames := make(map[string]string, len(subject.Discs))
	for _, disc := range subject.Discs {
		discNames[disc.ID] = disc.Name
	}
	for _, asset := range assets {
		if err := ctx.Err(); err != nil {
			return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media restore canceled: %w", err)
		}
		if !asset.Valid() || asset.CompatibilityKey != binding.CompatibilityKey {
			continue
		}
		captureFingerprint, fingerprintErr := workflowMediaReuseCaptureFingerprint(b.config, binding.CompatibilityKey, asset.Kind, asset.Image)
		if fingerprintErr != nil || captureFingerprint != asset.CaptureFingerprint {
			continue
		}
		image := asset.Image
		image.DiscName = discNames[image.DiscID]
		contentSHA256, hashErr := workflowMediaContentSHA256(ctx, image.Path)
		localAvailable := hashErr == nil && strings.EqualFold(contentSHA256, asset.ContentSHA256)
		if localAvailable {
			materialized, materializeErr := b.materializeReusableMediaImage(ctx, binding, image, captureFingerprint, asset.ContentSHA256)
			if materializeErr != nil && !errors.Is(materializeErr, errReusableMediaUnavailable) {
				return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow media materialize reusable artifact: %w", materializeErr)
			}
			localAvailable = materializeErr == nil
			if localAvailable {
				image = materialized
			}
		}
		sourceID := restoredUnavailableSourceID(snapshot.CaptureFingerprint, asset)
		if localAvailable {
			artifact := publicMediaArtifact(
				snapshot.CaptureFingerprint,
				"restored-"+string(asset.Kind),
				len(snapshot.Artifacts),
				asset.Kind,
				image.Purpose,
				image,
			)
			artifact.Order = asset.Order
			artifact.Selected = asset.Selected
			if asset.Kind == api.MediaArtifactDVDMenu {
				artifact.Source = api.ScreenshotSelectionSourceDVDMenu
			}
			snapshot.Artifacts = append(snapshot.Artifacts, artifact)
			retained.ArtifactImages[artifact.ID] = image
			if asset.Kind == api.MediaArtifactDVDMenu {
				retained.DVDMenuImages[artifact.ID] = api.DVDMenuCaptureImage{ScreenshotImage: image}
			}
			sourceID = artifact.ID
		}
		for linkIndex, link := range asset.HostedLinks {
			accountScope, scopeErr := workflowMediaHostAccountScope(b.config, link.Host)
			if scopeErr != nil || accountScope != link.AccountScope || hostedImageURL(link) == "" {
				continue
			}
			link.SourcePath = binding.SourcePath
			link.PreparedMediaFingerprint = binding.PreparedMediaFingerprint
			link.PreparedGeneration = binding.PreparedGeneration
			hostedID := restoredHostedArtifactID(snapshot.CaptureFingerprint, sourceID, linkIndex, link)
			hosted := api.MediaArtifact{
				ID:        hostedID,
				Kind:      api.MediaArtifactHostedImage,
				Purpose:   image.Purpose,
				Selected:  asset.Selected,
				Order:     asset.Order,
				Source:    string(sourceID),
				SizeBytes: link.SizeBytes,
				Host:      strings.ToLower(strings.TrimSpace(link.Host)),
				URL:       hostedImageURL(link),
			}
			snapshot.Artifacts = append(snapshot.Artifacts, hosted)
			retained.HostedImages[hostedID] = link
			retained.HostedSources[hostedID] = sourceID
		}
	}
	if len(snapshot.Artifacts) == 0 {
		return api.MediaArtifactSet{}, nil, nil
	}
	rebuildWorkflowMediaLocalSlices(&retained, snapshot)
	if err := b.persistReusableWorkflowMedia(ctx, snapshot, retained); err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	requiredScreenshots, requiredMenus := projectedMediaRequirements(projections.Projections)
	applyWorkflowMediaMinimums(&snapshot, requiredScreenshots, requiredMenus)
	return snapshot, retained, nil
}

// RecordReusableMedia persists final workflow selection, order, and hosted
// link state after the workflow repository has committed a media mutation.
func (b workflowMediaBuilder) RecordReusableMedia(ctx context.Context, snapshot api.MediaArtifactSet, retained any) error {
	private, ok := retained.(workflowMediaPrivateArtifacts)
	if !ok {
		return errors.New("workflow reusable media retained resource is incompatible")
	}
	return b.persistReusableWorkflowMedia(ctx, snapshot, private)
}

// HasReusableMediaCommit reports whether reusable associations already reflect
// one exact committed media snapshot.
func (b workflowMediaBuilder) HasReusableMediaCommit(ctx context.Context, snapshot api.MediaArtifactSet) (bool, error) {
	if b.media == nil || b.media.mediaReuse == nil {
		return false, nil
	}
	repository, ok := b.media.mediaReuse.(api.MediaReuseCommitRepository)
	if !ok {
		return false, nil
	}
	committed, err := repository.HasReusableMediaCommit(ctx, api.ReusableMediaCommit{
		WorkflowID: snapshot.WorkflowID,
		MediaID:    snapshot.ID,
		Revision:   snapshot.Revision,
	})
	if err != nil {
		return false, fmt.Errorf("workflow media check reusable commit: %w", err)
	}
	return committed, nil
}

func restoredUnavailableSourceID(captureFingerprint api.WorkflowFingerprint, asset api.ReusableMediaAsset) api.PublicResourceID {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%s", captureFingerprint, asset.CaptureFingerprint, asset.ContentSHA256))
	return api.PublicResourceID("media_unavailable_" + hex.EncodeToString(sum[:])[:24])
}

func restoredHostedArtifactID(
	captureFingerprint api.WorkflowFingerprint,
	sourceID api.PublicResourceID,
	index int,
	link api.UploadedImageLink,
) api.PublicResourceID {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%s", captureFingerprint, sourceID, index, hostedImageKey(sourceID, link)))
	return api.PublicResourceID("hosted_" + hex.EncodeToString(sum[:])[:24])
}

func countMediaArtifacts(artifacts []api.MediaArtifact, kind api.MediaArtifactKind) int {
	count := 0
	for _, artifact := range artifacts {
		if artifact.Kind == kind {
			count++
		}
	}
	return count
}

func removeAutomaticDVDMenuArtifacts(snapshot *api.MediaArtifactSet, retained *workflowMediaPrivateArtifacts) {
	removed := make(map[api.PublicResourceID]struct{})
	for _, artifact := range snapshot.Artifacts {
		if artifact.Kind == api.MediaArtifactDVDMenu && artifact.Source == api.ScreenshotSelectionSourceDVDMenu {
			removed[artifact.ID] = struct{}{}
			if image, ok := retained.ArtifactImages[artifact.ID]; ok {
				retained.commitState.pending = append(retained.commitState.pending, workflowMediaPendingDelete{
					kind:    api.MediaArtifactDVDMenu,
					path:    image.Path,
					binding: workflowMediaBindingForKind(*retained, api.MediaArtifactDVDMenu),
				})
			}
		}
	}
	for artifactID, sourceID := range retained.HostedSources {
		if _, remove := removed[sourceID]; remove {
			removed[artifactID] = struct{}{}
		}
	}
	if len(removed) == 0 {
		return
	}
	snapshot.Artifacts = slices.DeleteFunc(snapshot.Artifacts, func(artifact api.MediaArtifact) bool {
		_, remove := removed[artifact.ID]
		return remove
	})
	for artifactID := range removed {
		delete(retained.ArtifactImages, artifactID)
		delete(retained.DVDMenuImages, artifactID)
		delete(retained.HostedImages, artifactID)
		delete(retained.HostedSources, artifactID)
	}
}

func rebuildWorkflowMediaLocalSlices(a *workflowMediaPrivateArtifacts, snapshot api.MediaArtifactSet) {
	a.Screenshots = nil
	a.DVDMenus = nil
	for _, artifact := range snapshot.Artifacts {
		image, ok := a.ArtifactImages[artifact.ID]
		if !ok {
			continue
		}
		switch artifact.Kind {
		case api.MediaArtifactScreenshot:
			a.Screenshots = append(a.Screenshots, image)
		case api.MediaArtifactDVDMenu:
			menu, ok := a.DVDMenuImages[artifact.ID]
			if !ok {
				menu = api.DVDMenuCaptureImage{ScreenshotImage: image}
			}
			a.DVDMenus = append(a.DVDMenus, menu)
		case api.MediaArtifactHostedImage:
		}
	}
}

func (b workflowMediaBuilder) Attach(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	existing *api.MediaArtifactSet,
	privateExisting any,
	attachments []releaseworkflow.StagedMediaAttachment,
	_ time.Time,
) (api.MediaArtifactSet, releaseworkflow.RetainedMediaResource, error) {
	if b.media == nil {
		return api.MediaArtifactSet{}, nil, errors.New("workflow media attachment service is unavailable")
	}
	snapshot, retained, err := b.mediaMutationBase(release, projections, existing, privateExisting)
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	contents := make([]menuImageContent, 0, len(attachments))
	for _, attachment := range attachments {
		switch {
		case attachment.Attachment.Kind == api.MediaArtifactDVDMenu &&
			attachment.Attachment.Purpose == api.ScreenshotPurposeMenu:
		case attachment.Attachment.Kind == api.MediaArtifactScreenshot &&
			attachment.Attachment.Purpose == api.ScreenshotPurposeFinal:
		default:
			return api.MediaArtifactSet{}, nil, errors.New("workflow media attachment must be a final screenshot or DVD menu image")
		}
		contents = append(contents, menuImageContent{
			contentType: attachment.Content.ContentType,
			bytes:       append([]byte(nil), attachment.Content.Bytes...),
			discID:      attachment.Attachment.DiscID,
		})
	}
	subject, images, err := b.media.importAcceptedMenuImageContents(ctx, api.MediaPlanInput{Release: release}, contents)
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	retained.dvdMenuService = b.dvdMenus
	retained.dvdMenuSubject = subject
	knownPaths := make(map[string]struct{}, len(retained.ArtifactImages))
	for _, image := range retained.ArtifactImages {
		knownPaths[strings.ToLower(filepath.Clean(image.Path))] = struct{}{}
	}
	for index, image := range images {
		pathKey := strings.ToLower(filepath.Clean(image.Path))
		if _, exists := knownPaths[pathKey]; exists {
			continue
		}
		attachment := attachments[index].Attachment
		source := "attached-screenshot"
		selectionSource := "comparison"
		if attachment.Kind == api.MediaArtifactDVDMenu {
			source = "attached-dvd-menu"
			selectionSource = api.ScreenshotSelectionSourceMenu
		}
		artifact := publicMediaArtifact(
			snapshot.CaptureFingerprint,
			source,
			len(snapshot.Artifacts),
			attachment.Kind,
			attachment.Purpose,
			image,
		)
		artifact.Order = attachment.Order
		artifact.Source = selectionSource
		snapshot.Artifacts = append(snapshot.Artifacts, artifact)
		retained.ArtifactImages[artifact.ID] = image
		knownPaths[pathKey] = struct{}{}
	}
	rebuildWorkflowMediaLocalSlices(&retained, snapshot)
	if err := b.persistReusableWorkflowMedia(ctx, snapshot, retained); err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	return snapshot, retained, nil
}

func (b workflowMediaBuilder) UploadImages(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	snapshot api.MediaArtifactSet,
	privateExisting any,
	artifactIDs []api.PublicResourceID,
	host string,
	retry bool,
	now time.Time,
) (api.MediaArtifactSet, releaseworkflow.RetainedMediaResource, []api.HostedImageAttempt, error) {
	if b.media == nil {
		return api.MediaArtifactSet{}, nil, nil, errors.New("workflow image hosting service is unavailable")
	}
	retained, ok := privateExisting.(workflowMediaPrivateArtifacts)
	if !ok {
		return api.MediaArtifactSet{}, nil, nil, errors.New("workflow media retained resource is incompatible")
	}
	retained = cloneWorkflowMediaPrivateArtifacts(retained)
	host = strings.ToLower(strings.TrimSpace(host))
	failedHost := slices.Contains(snapshot.FailedHosts, host)
	if failedHost && !retry {
		return api.MediaArtifactSet{}, nil, nil, errors.New("failed image host requires explicit retry")
	}
	selected := make(map[api.PublicResourceID]struct{}, len(artifactIDs))
	for _, artifactID := range artifactIDs {
		selected[artifactID] = struct{}{}
	}
	images := make([]api.ScreenshotImage, 0, len(artifactIDs))
	sourceByPath := make(map[string]api.PublicResourceID, len(artifactIDs))
	sourcePurposeByPath := make(map[string]api.ScreenshotPurpose, len(artifactIDs))
	for _, artifact := range snapshot.Artifacts {
		if _, requested := selected[artifact.ID]; !requested {
			continue
		}
		if artifact.Kind != api.MediaArtifactScreenshot && artifact.Kind != api.MediaArtifactDVDMenu {
			return api.MediaArtifactSet{}, nil, nil, errors.New("only local workflow media can be hosted")
		}
		if !artifact.Selected {
			return api.MediaArtifactSet{}, nil, nil, errors.New("only selected workflow media can be hosted")
		}
		image, imageOK := retained.ArtifactImages[artifact.ID]
		if !imageOK {
			return api.MediaArtifactSet{}, nil, nil, errors.New("workflow media artifact content is unavailable")
		}
		images = append(images, image)
		pathKey := strings.ToLower(normalizedUploadImagePath(image.Path))
		sourceByPath[pathKey] = artifact.ID
		sourcePurposeByPath[pathKey] = artifact.Purpose
	}
	if len(images) != len(artifactIDs) {
		return api.MediaArtifactSet{}, nil, nil, errors.New("workflow media upload selection is incomplete")
	}
	trackerNames := make([]string, 0, len(projections.Projections))
	for _, projection := range projections.Projections {
		trackerNames = append(trackerNames, string(projection.TrackerID))
	}
	excludedHosts := append([]string(nil), snapshot.FailedHosts...)
	if retry && host != "" {
		excludedHosts = slices.DeleteFunc(excludedHosts, func(candidate string) bool {
			return strings.EqualFold(strings.TrimSpace(candidate), host)
		})
	}
	if !retry {
		snapshot.Failures = slices.DeleteFunc(snapshot.Failures, func(failure api.WorkflowFailure) bool {
			return failure.Failure.Operation == api.OperationKindImageHosting &&
				(host == "" || strings.EqualFold(strings.TrimSpace(failure.Resource), host))
		})
	}
	effectFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Release       api.ReleaseRef
		ProjectionSet api.TrackerReleaseProjectionSetRef
		Media         api.MediaArtifactSetRef
		ArtifactIDs   []api.PublicResourceID
		Host          string
		Retry         bool
	}{
		Release:       release,
		ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: projections.Revision},
		Media:         api.MediaArtifactSetRef{ID: snapshot.ID, Revision: snapshot.Revision},
		ArtifactIDs:   artifactIDs,
		Host:          host,
		Retry:         retry,
	})
	if err != nil {
		return api.MediaArtifactSet{}, nil, nil, fmt.Errorf("workflow image hosting effect fingerprint: %w", err)
	}
	effectReceipt, err := api.BeginWorkflowExternalEffect(ctx, api.WorkflowExternalEffect{
		Kind:                api.WorkflowExternalEffectImageHosting,
		ScopeID:             imageHostingEffectScope(host, projections),
		SemanticFingerprint: effectFingerprint,
	})
	if err != nil {
		if errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
			return api.MediaArtifactSet{}, nil, nil, api.NewOperationError(api.OperationFailure{
				Code:      api.OperationFailureUnknownOutcome,
				Operation: api.OperationKindImageHosting,
				Message:   "Image hosting may have succeeded before interruption. Verify hosted assets before retrying.",
				Recovery:  api.OperationRecoveryConfirm,
			}, err)
		}
		return api.MediaArtifactSet{}, nil, nil, fmt.Errorf("workflow image hosting effect fence: %w", err)
	}
	if effectReceipt.AlreadySucceeded {
		return api.MediaArtifactSet{}, nil, nil, api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureUnknownOutcome,
			Operation: api.OperationKindImageHosting,
			Message:   "Image hosting completed previously, but its exact published result is unavailable. Verify hosted assets before retrying.",
			Recovery:  api.OperationRecoveryConfirm,
		}, api.ErrReleaseWorkflowEffectAlreadySucceeded)
	}
	result, err := b.media.uploadAcceptedImages(ctx, api.ImageHostingInput{
		Release:       release,
		Trackers:      trackerNames,
		Host:          host,
		ExcludedHosts: excludedHosts,
	}, images)
	receiptErr := api.CompleteWorkflowExternalEffect(ctx, effectReceipt, err == nil)
	if receiptErr != nil {
		return api.MediaArtifactSet{}, nil, nil, api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureUnknownOutcome,
			Operation: api.OperationKindImageHosting,
			Message:   "Image hosting may have succeeded, but its terminal receipt could not be retained. Verify hosted assets before retrying.",
			Recovery:  api.OperationRecoveryConfirm,
		}, receiptErr)
	}
	if err != nil {
		return api.MediaArtifactSet{}, nil, nil, err
	}
	knownHosted := make(map[string]struct{}, len(retained.HostedImages))
	for id, link := range retained.HostedImages {
		knownHosted[hostedImageKey(retained.HostedSources[id], link)] = struct{}{}
	}
	attempts := make([]api.HostedImageAttempt, 0, len(result.Attempts))
	for _, hostResult := range result.Attempts {
		trackerIDs := make([]api.TrackerID, 0, len(hostResult.Trackers))
		for _, tracker := range hostResult.Trackers {
			trackerID := api.TrackerID(strings.ToUpper(strings.TrimSpace(tracker)))
			if trackerID != "" {
				trackerIDs = append(trackerIDs, trackerID)
			}
		}
		attemptFingerprint, fingerprintErr := api.CanonicalWorkflowFingerprint(struct {
			Media       api.MediaArtifactSetRef
			ArtifactIDs []api.PublicResourceID
			Host        string
			UsageScope  string
			TrackerIDs  []api.TrackerID
			Fallback    bool
			Retry       bool
			AttemptedAt time.Time
		}{
			Media:       api.MediaArtifactSetRef{ID: snapshot.ID, Revision: snapshot.Revision},
			ArtifactIDs: artifactIDs,
			Host:        hostResult.Host,
			UsageScope:  hostResult.UsageScope,
			TrackerIDs:  trackerIDs,
			Fallback:    hostResult.Fallback,
			Retry:       retry,
			AttemptedAt: now,
		})
		if fingerprintErr != nil {
			return api.MediaArtifactSet{}, nil, nil, fmt.Errorf("workflow hosted-image attempt fingerprint: %w", fingerprintErr)
		}
		attempt := api.HostedImageAttempt{
			ID:          api.PublicResourceID("host_" + string(attemptFingerprint)[:24]),
			Media:       api.MediaArtifactSetRef{ID: snapshot.ID, Revision: snapshot.Revision},
			Host:        strings.ToLower(strings.TrimSpace(hostResult.Host)),
			UsageScope:  strings.TrimSpace(hostResult.UsageScope),
			TrackerIDs:  trackerIDs,
			Fallback:    hostResult.Fallback,
			Status:      api.StageStatusCompleted,
			ArtifactIDs: append([]api.PublicResourceID(nil), artifactIDs...),
			AttemptedAt: now,
		}
		if hostResult.Failure != nil {
			attempt.Status = api.StageStatusFailed
			if len(hostResult.Failure.Trackers) == 0 {
				attempt.Failures = append(attempt.Failures, hostedImageFailure("", hostResult.Failure.Host, hostResult.Failure.Message))
			} else {
				for _, tracker := range hostResult.Failure.Trackers {
					attempt.Failures = append(
						attempt.Failures,
						hostedImageFailure(api.TrackerID(tracker), hostResult.Failure.Host, hostResult.Failure.Message),
					)
				}
			}
		}
		for index, link := range hostResult.Links {
			pathKey := strings.ToLower(normalizedUploadImagePath(link.ImagePath))
			sourceID := sourceByPath[pathKey]
			if sourceID == "" {
				continue
			}
			key := hostedImageKey(sourceID, link)
			if _, duplicate := knownHosted[key]; duplicate {
				continue
			}
			sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%s", attempt.ID, index, key))
			artifactID := api.PublicResourceID("hosted_" + hex.EncodeToString(sum[:])[:24])
			artifact := api.MediaArtifact{
				ID:        artifactID,
				Kind:      api.MediaArtifactHostedImage,
				Purpose:   sourcePurposeByPath[pathKey],
				Selected:  true,
				Order:     len(snapshot.Artifacts),
				Source:    string(sourceID),
				SizeBytes: link.SizeBytes,
				Host:      strings.ToLower(strings.TrimSpace(link.Host)),
				URL:       hostedImageURL(link),
			}
			snapshot.Artifacts = append(snapshot.Artifacts, artifact)
			attempt.Results = append(attempt.Results, artifact)
			retained.HostedImages[artifactID] = link
			retained.HostedSources[artifactID] = sourceID
			knownHosted[key] = struct{}{}
		}
		attempts = append(attempts, attempt)
	}
	currentFailures := make([]api.WorkflowFailure, 0, len(result.Failures))
	for _, failure := range result.Failures {
		if len(failure.Trackers) == 0 {
			currentFailures = append(currentFailures, hostedImageFailure("", failure.Host, failure.Message))
			continue
		}
		for _, tracker := range failure.Trackers {
			currentFailures = append(currentFailures, hostedImageFailure(api.TrackerID(tracker), failure.Host, failure.Message))
		}
	}
	if retry {
		reconcileRetriedImageHostFailures(&snapshot, projections.Projections, attempts, currentFailures)
	} else {
		snapshot.Failures = append(snapshot.Failures, currentFailures...)
	}
	failedHosts := make(map[string]struct{}, len(snapshot.FailedHosts)+len(result.FailedHosts))
	for _, failed := range snapshot.FailedHosts {
		failedHosts[strings.ToLower(strings.TrimSpace(failed))] = struct{}{}
	}
	for _, failed := range result.FailedHosts {
		failedHosts[strings.ToLower(strings.TrimSpace(failed))] = struct{}{}
	}
	retrySucceeded := slices.ContainsFunc(attempts, func(candidate api.HostedImageAttempt) bool {
		return strings.EqualFold(candidate.Host, host) && candidate.Status == api.StageStatusCompleted
	})
	if retry && retrySucceeded && !slices.Contains(result.FailedHosts, host) {
		delete(failedHosts, host)
	}
	snapshot.FailedHosts = sortedNonEmptyKeys(failedHosts)
	if err := b.persistReusableWorkflowMedia(ctx, snapshot, retained); err != nil {
		return api.MediaArtifactSet{}, nil, nil, err
	}
	return snapshot, retained, attempts, nil
}

func (b workflowMediaBuilder) persistReusableWorkflowMedia(
	ctx context.Context,
	snapshot api.MediaArtifactSet,
	retained workflowMediaPrivateArtifacts,
) error {
	if b.media == nil || b.media.mediaReuse == nil {
		return nil
	}
	repository := b.media.mediaReuse
	binding := retained.screenshotSubject.MediaBinding
	if !binding.Valid() {
		binding = retained.dvdMenuSubject.MediaBinding
	}
	if !binding.Valid() || !binding.CompatibilityKey.Valid() {
		return nil
	}
	assets := make([]api.ReusableMediaAsset, 0, len(retained.ArtifactImages))
	for _, artifact := range snapshot.Artifacts {
		if artifact.Kind != api.MediaArtifactScreenshot && artifact.Kind != api.MediaArtifactDVDMenu {
			continue
		}
		image, exists := retained.ArtifactImages[artifact.ID]
		if !exists {
			return errors.New("workflow reusable media artifact content is unavailable")
		}
		contentSHA256, err := workflowMediaContentSHA256(ctx, image.Path)
		if err != nil {
			return fmt.Errorf("workflow media hash reusable artifact: %w", err)
		}
		captureFingerprint, err := workflowMediaReuseCaptureFingerprint(b.config, binding.CompatibilityKey, artifact.Kind, image)
		if err != nil {
			return fmt.Errorf("workflow media fingerprint reusable artifact: %w", err)
		}
		asset := api.ReusableMediaAsset{
			Binding:            binding,
			CompatibilityKey:   binding.CompatibilityKey,
			CaptureFingerprint: captureFingerprint,
			ContentSHA256:      contentSHA256,
			Kind:               artifact.Kind,
			Image:              image,
			Selected:           artifact.Selected,
			Order:              artifact.Order,
		}
		for hostedID, sourceID := range retained.HostedSources {
			if sourceID != artifact.ID {
				continue
			}
			link, exists := retained.HostedImages[hostedID]
			if !exists {
				continue
			}
			link.AccountScope, err = workflowMediaHostAccountScope(b.config, link.Host)
			if err != nil {
				return fmt.Errorf("workflow media fingerprint hosted account scope: %w", err)
			}
			asset.HostedLinks = append(asset.HostedLinks, link)
		}
		assets = append(assets, asset)
	}
	commit := api.ReusableMediaCommit{
		WorkflowID: snapshot.WorkflowID,
		MediaID:    snapshot.ID,
		Revision:   snapshot.Revision,
	}
	if commitRepository, ok := repository.(api.MediaReuseCommitRepository); ok && commit.Valid() {
		if err := commitRepository.CommitReusableMedia(ctx, binding, binding.CompatibilityKey, assets, commit); err != nil {
			return fmt.Errorf("workflow media commit reusable media: %w", err)
		}
		return nil
	}
	if err := repository.ReplaceReusableMediaAssets(ctx, binding, binding.CompatibilityKey, assets); err != nil {
		return fmt.Errorf("workflow media save reusable assets: %w", err)
	}
	return nil
}

func workflowMediaReuseCaptureFingerprint(
	cfg config.Config,
	compatibilityKey api.MediaCompatibilityKey,
	kind api.MediaArtifactKind,
	image api.ScreenshotImage,
) (api.WorkflowFingerprint, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Version           string
		CompatibilityKey  api.MediaCompatibilityKey
		Kind              api.MediaArtifactKind
		Purpose           api.ScreenshotPurpose
		DiscID            string
		Index             int
		TimestampSeconds  float64
		Width             int
		Height            int
		FrameOverlay      bool
		OverlayTextSize   int
		ToneMap           bool
		UseLibplacebo     bool
		FFmpegCompression int
		TonemapAlgorithm  string
		Desat             float64
	}{
		Version:           "media-capture-v1",
		CompatibilityKey:  compatibilityKey,
		Kind:              kind,
		Purpose:           image.Purpose,
		DiscID:            image.DiscID,
		Index:             image.Index,
		TimestampSeconds:  image.TimestampSeconds,
		Width:             image.Width,
		Height:            image.Height,
		FrameOverlay:      cfg.ScreenshotHandling.FrameOverlay,
		OverlayTextSize:   cfg.ScreenshotHandling.OverlayTextSize,
		ToneMap:           cfg.ScreenshotHandling.ToneMap,
		UseLibplacebo:     cfg.ScreenshotHandling.UseLibplacebo,
		FFmpegCompression: cfg.ScreenshotHandling.FFmpegCompression,
		TonemapAlgorithm:  cfg.ScreenshotHandling.TonemapAlgorithm,
		Desat:             cfg.ScreenshotHandling.Desat,
	})
	if err != nil {
		return "", fmt.Errorf("workflow media fingerprint reusable capture: %w", err)
	}
	return fingerprint, nil
}

func workflowMediaHostAccountScope(cfg config.Config, host string) (string, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Version string
		Host    string
		Config  config.ImageHostingConfig
	}{
		Version: "media-host-account-v1",
		Host:    strings.ToLower(strings.TrimSpace(host)),
		Config:  cfg.ImageHosting,
	})
	if err != nil {
		return "", fmt.Errorf("workflow media fingerprint host account scope: %w", err)
	}
	return string(fingerprint), nil
}

func workflowMediaContentSHA256(ctx context.Context, pathValue string) (string, error) {
	file, err := os.Open(pathValue)
	if err != nil {
		return "", fmt.Errorf("workflow media open reusable artifact: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("workflow media hash reusable artifact canceled: %w", err)
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			if _, err := hash.Write(buffer[:count]); err != nil {
				return "", fmt.Errorf("workflow media hash reusable artifact: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("workflow media read reusable artifact: %w", readErr)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// materializeReusableMediaImage gives a restored local image a source-owned
// lifetime before its reusable association can be published. Reused media may
// be compatible across sources, but a history deletion owns the old source's
// managed files and can run in another process.
func (b workflowMediaBuilder) materializeReusableMediaImage(
	ctx context.Context,
	binding api.PreparedMediaBinding,
	image api.ScreenshotImage,
	captureFingerprint api.WorkflowFingerprint,
	expectedSHA256 string,
) (api.ScreenshotImage, error) {
	tmpRoot, err := db.Subdir(b.config.MainSettings.DBPath, "tmp")
	if err != nil {
		return api.ScreenshotImage{}, fmt.Errorf("workflow media resolve reusable tmp root: %w", err)
	}
	reuseRoot, err := reusableMediaTempRoot(tmpRoot, binding.SourcePath)
	if err != nil {
		return api.ScreenshotImage{}, err
	}
	if err := os.MkdirAll(reuseRoot, 0o700); err != nil {
		return api.ScreenshotImage{}, fmt.Errorf("workflow media create reusable destination: %w", err)
	}
	if !pathing.IsWithinRoot(tmpRoot, reuseRoot) {
		return api.ScreenshotImage{}, errors.New("workflow media reusable destination escapes tmp root")
	}
	if pathing.IsWithinRoot(reuseRoot, image.Path) {
		return image, nil
	}
	destination := filepath.Join(reuseRoot,
		"reused-"+strings.ToLower(string(captureFingerprint))+"-"+strings.ToLower(expectedSHA256)+reusableMediaImageExtension(image.Path))
	if !pathing.IsWithinRoot(reuseRoot, destination) {
		return api.ScreenshotImage{}, errors.New("workflow media reusable image destination escapes root")
	}
	materialized, err := copyReusableMediaImage(ctx, image.Path, destination, expectedSHA256)
	if err != nil {
		return api.ScreenshotImage{}, err
	}
	image.Path = materialized
	return image, nil
}

// reusableMediaTempRoot returns the reserved first-level directory for local
// reusable media owned by one canonical source path. Its @ prefix cannot
// collide with the existing release-temp basename namespace.
func reusableMediaTempRoot(tmpRoot, sourcePath string) (string, error) {
	absSource, err := filepath.Abs(strings.TrimSpace(sourcePath))
	if err != nil {
		return "", fmt.Errorf("workflow media resolve reusable source path: %w", err)
	}
	sourceKey := preparedrelease.CanonicalSourceKey(absSource)
	if sourceKey == "" {
		return "", errors.New("workflow media reusable source path is required")
	}
	sourceDigest := sha256.Sum256([]byte(sourceKey))
	reuseRoot := filepath.Join(tmpRoot, "@reused-"+hex.EncodeToString(sourceDigest[:]))
	if !pathing.IsWithinRoot(tmpRoot, reuseRoot) {
		return "", errors.New("workflow media reusable destination escapes tmp root")
	}
	return reuseRoot, nil
}

func reusableMediaImageExtension(pathValue string) string {
	extension := strings.ToLower(filepath.Ext(filepath.Base(pathValue)))
	if !strings.HasPrefix(mime.TypeByExtension(extension), "image/") {
		return ""
	}
	return extension
}

func copyReusableMediaImage(ctx context.Context, sourcePath, destination, expectedSHA256 string) (string, error) {
	if existingSHA256, err := workflowMediaContentSHA256(ctx, destination); err == nil {
		if strings.EqualFold(existingSHA256, expectedSHA256) {
			return destination, nil
		}
		return "", errors.New("workflow media existing reusable artifact content differs")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("workflow media inspect reusable destination: %w", err)
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %w", errReusableMediaUnavailable, err)
		}
		return "", fmt.Errorf("workflow media open reusable source: %w", err)
	}
	defer source.Close()

	staged, err := os.CreateTemp(filepath.Dir(destination), ".reusable-media-*.partial")
	if err != nil {
		return "", fmt.Errorf("workflow media stage reusable artifact: %w", err)
	}
	stagedPath := staged.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = staged.Close()
			_ = os.Remove(stagedPath)
		}
	}()

	hash := sha256.New()
	buffer := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("workflow media copy reusable artifact canceled: %w", err)
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			if _, err := hash.Write(buffer[:count]); err != nil {
				return "", fmt.Errorf("workflow media hash copied reusable artifact: %w", err)
			}
			written, writeErr := staged.Write(buffer[:count])
			if writeErr != nil {
				return "", fmt.Errorf("workflow media write reusable artifact: %w", writeErr)
			}
			if written != count {
				return "", fmt.Errorf("workflow media write reusable artifact: %w", io.ErrShortWrite)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("workflow media read reusable artifact: %w", readErr)
		}
	}
	if actualSHA256 := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(actualSHA256, expectedSHA256) {
		return "", fmt.Errorf("%w: source changed during copy", errReusableMediaUnavailable)
	}
	if err := staged.Sync(); err != nil {
		return "", fmt.Errorf("workflow media sync reusable artifact: %w", err)
	}
	if err := staged.Close(); err != nil {
		return "", fmt.Errorf("workflow media close reusable artifact: %w", err)
	}
	if err := os.Rename(stagedPath, destination); err != nil {
		existingSHA256, existingErr := workflowMediaContentSHA256(ctx, destination)
		if existingErr == nil && strings.EqualFold(existingSHA256, expectedSHA256) {
			return destination, nil
		}
		return "", fmt.Errorf("workflow media publish reusable artifact: %w", err)
	}
	cleanup = false
	return destination, nil
}

func reconcileRetriedImageHostFailures(
	snapshot *api.MediaArtifactSet,
	projections []api.TrackerReleaseProjection,
	attempts []api.HostedImageAttempt,
	currentFailures []api.WorkflowFailure,
) {
	artifacts := make(map[api.PublicResourceID]api.MediaArtifact, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	allAttempts := make([]api.HostedImageAttempt, 0, len(snapshot.HostAttempts)+len(attempts))
	allAttempts = append(allAttempts, snapshot.HostAttempts...)
	allAttempts = append(allAttempts, attempts...)
	attemptApplies := func(attempt api.HostedImageAttempt, tracker string) bool {
		if !slices.ContainsFunc(attempt.TrackerIDs, func(candidate api.TrackerID) bool {
			return strings.EqualFold(strings.TrimSpace(string(candidate)), tracker)
		}) {
			return false
		}
		scope := normalizeImageUploadUsageScope(attempt.UsageScope)
		return scope == "global" || scope == "tracker:"+tracker
	}

	satisfied := make(map[string]struct{}, len(projections))
	for _, projection := range projections {
		tracker := strings.ToUpper(strings.TrimSpace(string(projection.TrackerID)))
		required := projection.Artifacts.ScreenshotCount
		if tracker == "" || !slices.ContainsFunc(attempts, func(attempt api.HostedImageAttempt) bool {
			return attempt.Status == api.StageStatusCompleted && attemptApplies(attempt, tracker)
		}) {
			continue
		}
		sources := make(map[api.PublicResourceID]struct{}, required)
		for _, attempt := range allAttempts {
			if !attemptApplies(attempt, tracker) {
				continue
			}
			for _, result := range attempt.Results {
				hosted, hostedOK := artifacts[result.ID]
				source, sourceOK := artifacts[api.PublicResourceID(strings.TrimSpace(hosted.Source))]
				if hostedOK && sourceOK && hosted.Selected && hosted.Kind == api.MediaArtifactHostedImage &&
					hosted.Purpose == api.ScreenshotPurposeFinal && source.Selected &&
					source.Kind == api.MediaArtifactScreenshot && source.Purpose == api.ScreenshotPurposeFinal {
					sources[source.ID] = struct{}{}
				}
			}
		}
		if len(sources) >= required {
			satisfied[tracker] = struct{}{}
		}
	}

	type failureKey struct {
		tracker string
		host    string
	}
	keyFor := func(failure api.WorkflowFailure) (failureKey, bool) {
		if failure.Failure.Operation != api.OperationKindImageHosting {
			return failureKey{}, false
		}
		return failureKey{
			tracker: strings.ToUpper(strings.TrimSpace(string(failure.TrackerID))),
			host:    strings.ToLower(strings.TrimSpace(failure.Resource)),
		}, true
	}
	replacements := make(map[failureKey]struct{}, len(currentFailures))
	for _, failure := range currentFailures {
		if key, ok := keyFor(failure); ok {
			replacements[key] = struct{}{}
		}
	}
	snapshot.Failures = slices.DeleteFunc(snapshot.Failures, func(failure api.WorkflowFailure) bool {
		key, ok := keyFor(failure)
		if !ok {
			return false
		}
		if key.tracker != "" {
			if _, ok := satisfied[key.tracker]; ok {
				return true
			}
		}
		_, replaced := replacements[key]
		return replaced
	})
	appended := make(map[failureKey]struct{}, len(currentFailures))
	for _, failure := range currentFailures {
		key, ok := keyFor(failure)
		if ok {
			if key.tracker != "" {
				if _, ok := satisfied[key.tracker]; ok {
					continue
				}
			}
			if _, duplicate := appended[key]; duplicate {
				continue
			}
			appended[key] = struct{}{}
		}
		snapshot.Failures = append(snapshot.Failures, failure)
	}
}

func imageHostingEffectScope(host string, projections api.TrackerReleaseProjectionSet) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host != "" {
		return host
	}
	trackerIDs := make([]string, 0, len(projections.Projections))
	for _, projection := range projections.Projections {
		trackerIDs = append(trackerIDs, string(projection.TrackerID))
	}
	slices.Sort(trackerIDs)
	return "required:" + strings.Join(trackerIDs, ",")
}

func (b workflowMediaBuilder) RemoveHostedImages(
	ctx context.Context,
	_ api.ReleaseRef,
	snapshot api.MediaArtifactSet,
	privateExisting any,
	artifactIDs []api.PublicResourceID,
	_ time.Time,
) (api.MediaArtifactSet, releaseworkflow.RetainedMediaResource, error) {
	if err := ctx.Err(); err != nil {
		return api.MediaArtifactSet{}, nil, fmt.Errorf("workflow remove hosted images: %w", err)
	}
	retained, ok := privateExisting.(workflowMediaPrivateArtifacts)
	if !ok {
		return api.MediaArtifactSet{}, nil, errors.New("workflow media retained resource is incompatible")
	}
	retained = cloneWorkflowMediaPrivateArtifacts(retained)
	removed := make(map[api.PublicResourceID]struct{}, len(artifactIDs))
	for _, artifactID := range artifactIDs {
		link, exists := retained.HostedImages[artifactID]
		if !exists {
			return api.MediaArtifactSet{}, nil, errors.New("workflow hosted image is unavailable")
		}
		binding := api.PreparedMediaBinding{
			SourcePath:               link.SourcePath,
			PreparedMediaFingerprint: link.PreparedMediaFingerprint,
			PreparedGeneration:       link.PreparedGeneration,
		}
		if !binding.Valid() {
			binding = retained.screenshotSubject.MediaBinding
		}
		if !binding.Valid() {
			binding = retained.dvdMenuSubject.MediaBinding
		}
		if !binding.Valid() {
			return api.MediaArtifactSet{}, nil, errors.New("workflow hosted image binding is unavailable")
		}
		retained.commitState.pending = append(retained.commitState.pending, workflowMediaPendingDelete{
			kind:    api.MediaArtifactHostedImage,
			path:    link.ImagePath,
			binding: binding,
			host:    link.Host,
		})
		delete(retained.HostedImages, artifactID)
		delete(retained.HostedSources, artifactID)
		removed[artifactID] = struct{}{}
	}
	artifacts := make([]api.MediaArtifact, 0, len(snapshot.Artifacts)-len(removed))
	for _, artifact := range snapshot.Artifacts {
		if _, remove := removed[artifact.ID]; !remove {
			artifacts = append(artifacts, artifact)
		}
	}
	snapshot.Artifacts = artifacts
	return snapshot, retained, nil
}

func (b workflowMediaBuilder) mediaMutationBase(
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	existing *api.MediaArtifactSet,
	privateExisting any,
) (api.MediaArtifactSet, workflowMediaPrivateArtifacts, error) {
	if existing != nil {
		retained, ok := privateExisting.(workflowMediaPrivateArtifacts)
		if !ok {
			return api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, errors.New("workflow media retained resource is incompatible")
		}
		return *existing, cloneWorkflowMediaPrivateArtifacts(retained), nil
	}
	requirements, err := workflowMediaRequirementsFingerprint(projections.Projections)
	if err != nil {
		return api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, err
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Release      api.ReleaseRef
		ProjectionID api.TrackerReleaseProjectionSetID
		Revision     api.WorkflowRevision
		Requirements api.WorkflowFingerprint
	}{release, projections.ID, projections.Revision, requirements})
	if err != nil {
		return api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, fmt.Errorf("workflow media fingerprint: %w", err)
	}
	return api.MediaArtifactSet{
		CaptureFingerprint:      fingerprint,
		RequirementsFingerprint: requirements,
		Artifacts:               []api.MediaArtifact{},
		Status:                  api.StageStatusCompleted,
	}, workflowMediaPrivateArtifacts{
		ArtifactImages:    make(map[api.PublicResourceID]api.ScreenshotImage),
		DVDMenuImages:     make(map[api.PublicResourceID]api.DVDMenuCaptureImage),
		HostedImages:      make(map[api.PublicResourceID]api.UploadedImageLink),
		HostedSources:     make(map[api.PublicResourceID]api.PublicResourceID),
		screenshotService: b.screenshots,
		dvdMenuService:    b.dvdMenus,
		hostedRepository:  b.media.repo,
		mediaReuse:        b.media.mediaReuse,
		commitState:       &workflowMediaCommitState{},
	}, nil
}

func hostedImageKey(sourceID api.PublicResourceID, link api.UploadedImageLink) string {
	return strings.Join([]string{
		string(sourceID),
		strings.ToLower(strings.TrimSpace(link.Host)),
		strings.TrimSpace(link.ImgURL),
		strings.TrimSpace(link.RawURL),
		strings.TrimSpace(link.WebURL),
	}, "\x00")
}

func hostedImageURL(link api.UploadedImageLink) string {
	for _, candidate := range []string{link.WebURL, link.ImgURL, link.RawURL} {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return ""
}

func hostedImageFailure(trackerID api.TrackerID, host string, message string) api.WorkflowFailure {
	return api.WorkflowFailure{
		Failure: api.OperationFailure{
			Code:      api.OperationFailureImageHostUnavailable,
			Operation: api.OperationKindImageHosting,
			Message:   strings.TrimSpace(message),
			Recovery:  api.OperationRecoveryRetry,
		},
		TrackerID: trackerID,
		Resource:  strings.ToLower(strings.TrimSpace(host)),
	}
}

func sortedNonEmptyKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result
}

func projectedMediaRequirements(projections []api.TrackerReleaseProjection) (int, int) {
	var screenshots int
	var dvdMenus int
	for _, projection := range projections {
		if projection.Artifacts.ScreenshotCount > screenshots {
			screenshots = projection.Artifacts.ScreenshotCount
		}
		if projection.Artifacts.DVDMenuCount > dvdMenus {
			dvdMenus = projection.Artifacts.DVDMenuCount
		}
	}
	return screenshots, dvdMenus
}

func workflowMediaRequirementsFingerprint(projections []api.TrackerReleaseProjection) (api.WorkflowFingerprint, error) {
	type trackerRequirements struct {
		TrackerID api.TrackerID
		Artifacts api.TrackerArtifactRequirements
	}
	requirements := make([]trackerRequirements, len(projections))
	for index, projection := range projections {
		requirements[index] = trackerRequirements{TrackerID: projection.TrackerID, Artifacts: projection.Artifacts}
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(requirements)
	if err != nil {
		return "", fmt.Errorf("workflow media requirements fingerprint: %w", err)
	}
	return fingerprint, nil
}

func publicMediaArtifact(
	fingerprint api.WorkflowFingerprint,
	kindKey string,
	index int,
	kind api.MediaArtifactKind,
	purpose api.ScreenshotPurpose,
	image api.ScreenshotImage,
) api.MediaArtifact {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d", fingerprint, kindKey, index))
	idFingerprint := hex.EncodeToString(sum[:])
	return api.MediaArtifact{
		ID:               api.PublicResourceID("media_" + idFingerprint[:24]),
		DiscID:           image.DiscID,
		DiscName:         image.DiscName,
		Kind:             kind,
		Purpose:          purpose,
		Selected:         true,
		Order:            index,
		Index:            image.Index,
		TimestampSeconds: image.TimestampSeconds,
		Source:           string(image.Purpose),
		Width:            image.Width,
		Height:           image.Height,
		SizeBytes:        image.SizeBytes,
		Host:             strings.TrimSpace(image.Host),
	}
}

func failedMediaSnapshot(snapshot api.MediaArtifactSet, message string) api.MediaArtifactSet {
	snapshot.Status = api.StageStatusFailed
	snapshot.Failures = append(snapshot.Failures, mediaFailure(message))
	return snapshot
}

func mediaFailure(message string) api.WorkflowFailure {
	return api.WorkflowFailure{Failure: api.OperationFailure{
		Code:      api.OperationFailureInternal,
		Operation: api.OperationKindMedia,
		Message:   message,
		Recovery:  api.OperationRecoveryRetry,
	}}
}
