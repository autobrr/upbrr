// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"

	"github.com/autobrr/upbrr/internal/core"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/pkg/api"
)

// LifecycleOwner is the sole shutdown handle for resources behind a capability bundle.
// Capability interfaces intentionally omit Close so borrowing one never implies ownership.
type LifecycleOwner interface{ Close() error }

// MetadataCapability prepares metadata previews for WebUI requests.
type MetadataCapability interface {
	FetchAcceptedMetadataPreview(context.Context, api.ReleaseRef) (api.MetadataPreview, error)
}

// ReleasePreparationCapability creates immutable canonical prepared facts.
type ReleasePreparationCapability interface {
	PrepareRelease(context.Context, api.PrepareInput) (api.PrepareResult, error)
}

// SelectionCapability applies an explicit Blu-ray source selection.
type SelectionCapability interface {
	SelectBlurayCandidate(context.Context, string, string) (api.MetadataPreview, error)
}

// PreparationCapability builds preparation views from prepared state.
type PreparationCapability interface {
	FetchAcceptedPreparationPreview(context.Context, api.DescriptionInput) (api.PreparationPreview, error)
}

// UploadReviewCapability builds one review and its execution-only outcomes.
type UploadReviewCapability interface {
	ReviewAcceptedUpload(context.Context, api.UploadReviewInput) (api.ReviewedUpload, error)
}

// DryRunCapability builds tracker payload previews without uploading.
type DryRunCapability interface {
	FetchAcceptedTrackerDryRun(context.Context, api.TrackerDryRunInput) (api.TrackerDryRunPreview, error)
}

// DuplicateExecutionCapability runs one accepted duplicate-check snapshot.
type DuplicateExecutionCapability interface {
	CheckAcceptedDupes(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error)
}

// UploadExecutionCapability runs one accepted reviewed upload snapshot.
type UploadExecutionCapability interface {
	RunAcceptedUpload(context.Context, api.UploadExecutionPlan) (api.Result, error)
}

// ScreenshotCapability owns screenshot planning, generation, selection, and deletion workflows.
type ScreenshotCapability interface {
	FetchAcceptedScreenshotPlan(context.Context, api.MediaPlanInput) (api.ScreenshotPlan, error)
	GenerateAcceptedScreenshots(context.Context, api.MediaPlanInput, []api.ScreenshotSelection) (api.ScreenshotResult, error)
	PreviewAcceptedScreenshotFrame(context.Context, api.MediaPlanInput, float64) (api.ScreenshotPreview, error)
	DeleteAcceptedScreenshot(context.Context, api.MediaPlanInput, string) error
	DeleteAcceptedTrackerImageURL(context.Context, api.ImageHostingInput, string) error
	SaveAcceptedFinalScreenshotSelections(context.Context, api.MediaPlanInput, []api.ScreenshotImage) error
	ImportAcceptedMenuImages(context.Context, api.MediaPlanInput, []string) error
}

// HostedImageCapability owns upload-candidate and hosted-image workflows.
type HostedImageCapability interface {
	ListAcceptedUploadCandidates(context.Context, api.ImageHostingInput) ([]api.ScreenshotImage, error)
	ListAcceptedUploadedImages(context.Context, api.ImageHostingInput) ([]api.UploadedImageLink, error)
	UploadAcceptedImages(context.Context, api.ImageHostingInput, []api.ScreenshotImage) (api.UploadImagesResult, error)
	DeleteAcceptedUploadedImage(context.Context, api.ImageHostingInput, string, string) error
}

// DVDCaptureCapability captures DVD menu images for a prepared request.
type DVDCaptureCapability interface {
	CaptureAcceptedDVDMenus(context.Context, api.MediaPlanInput) (api.DVDMenuCaptureResult, error)
}

// DVDCapability owns DVD menu capture and prepared-image maintenance.
type DVDCapability interface {
	DVDCaptureCapability
	ListAcceptedDVDMenuScreenshots(context.Context, api.MediaPlanInput) ([]api.ScreenshotImage, error)
	DeleteAcceptedDVDMenuScreenshot(context.Context, api.MediaPlanInput, string) error
}

// DescriptionCapability previews, renders, and persists prepared-release descriptions.
type DescriptionCapability interface {
	FetchAcceptedDescriptionBuilderPreview(context.Context, api.DescriptionInput) (api.DescriptionBuilderPreview, error)
	FetchAcceptedDescriptionBuilderGroupPreview(context.Context, api.DescriptionInput) (api.DescriptionBuilderGroup, error)
	RenderDescription(context.Context, string) (string, error)
	SaveAcceptedDescriptionOverride(context.Context, api.DescriptionInput, string) (api.DescriptionBuilderGroup, error)
}

// PlaylistCapability discovers BDMV playlists for one preparation source.
type PlaylistCapability interface {
	DiscoverPlaylists(context.Context, string) ([]api.PlaylistInfo, error)
}

// HistoryCapability reads and deletes persisted release history.
type HistoryCapability interface {
	ListHistory(context.Context) ([]api.HistoryEntry, error)
	GetHistoryOverview(context.Context, string) (api.HistoryOverview, error)
	DeleteHistoryRelease(context.Context, string) error
	DeleteAllHistoryReleases(context.Context) (int, error)
}

// PreparedGenerationTransfer moves one exact canonical generation without
// carrying workflow choices or outcomes.
type PreparedGenerationTransfer interface {
	ExportReleaseSeed(context.Context, api.ReleaseRef) (preparedrelease.Seed, error)
	ImportReleaseSeed(context.Context, preparedrelease.Seed) (api.ReleaseRef, error)
}

// DiagnosticProbeCapability reports availability of optional runtime tooling.
type DiagnosticProbeCapability interface {
	DVDMenuCapability(context.Context) (api.DVDMenuEngineInfo, error)
}

// CoreCapabilities is the explicit WebUI workflow bundle. Each field is a
// narrow view of one concrete core; lifecycle ownership remains separate.
// Fields are independently optional so consumers and tests can supply only the
// workflows they invoke. Production bindings populate every field.
type CoreCapabilities struct {
	Metadata                   MetadataCapability
	ReleasePreparation         ReleasePreparationCapability
	Selection                  SelectionCapability
	Preparation                PreparationCapability
	UploadReview               UploadReviewCapability
	DryRun                     DryRunCapability
	DuplicateExecution         DuplicateExecutionCapability
	UploadExecution            UploadExecutionCapability
	Screenshots                ScreenshotCapability
	HostedImages               HostedImageCapability
	DVD                        DVDCapability
	Description                DescriptionCapability
	Playlists                  PlaylistCapability
	History                    HistoryCapability
	PreparedGenerationTransfer PreparedGenerationTransfer
	DiagnosticProbe            DiagnosticProbeCapability
}

// Available reports whether at least one capability is installed.
// It does not validate that a bundle contains every capability a caller needs.
func (c CoreCapabilities) Available() bool {
	if CapabilityAvailable(c.Metadata) || CapabilityAvailable(c.ReleasePreparation) || CapabilityAvailable(c.Selection) ||
		CapabilityAvailable(c.Preparation) || CapabilityAvailable(c.UploadReview) || CapabilityAvailable(c.DryRun) ||
		CapabilityAvailable(c.DuplicateExecution) ||
		CapabilityAvailable(c.UploadExecution) {
		return true
	}
	if CapabilityAvailable(c.Screenshots) || CapabilityAvailable(c.HostedImages) || CapabilityAvailable(c.DVD) || CapabilityAvailable(c.Description) ||
		CapabilityAvailable(c.Playlists) || CapabilityAvailable(c.History) {
		return true
	}
	return CapabilityAvailable(c.PreparedGenerationTransfer) || CapabilityAvailable(c.DiagnosticProbe)
}

// CapabilityAvailable reports whether capability contains a callable value,
// rejecting both nil interfaces and interfaces holding typed nil values.
func CapabilityAvailable(capability any) bool {
	return !capabilityIsNil(capability)
}

// PreparedGenerationReady reports whether canonical generations can be
// prepared and transferred through this bundle.
func (c CoreCapabilities) PreparedGenerationReady() bool {
	return CapabilityAvailable(c.ReleasePreparation) && CapabilityAvailable(c.PreparedGenerationTransfer)
}

// PreparedUploadReady reports whether this bundle can receive state and execute an upload.
func (c CoreCapabilities) PreparedUploadReady() bool {
	return c.PreparedGenerationReady() && CapabilityAvailable(c.UploadExecution)
}

// PreparedDupeReady reports whether this bundle can receive state and execute a dupe check.
func (c CoreCapabilities) PreparedDupeReady() bool {
	return c.PreparedGenerationReady() && CapabilityAvailable(c.DuplicateExecution)
}

// PreparedDVDReady reports whether this bundle can receive state and execute DVD capture.
func (c CoreCapabilities) PreparedDVDReady() bool {
	return c.PreparedGenerationReady() && CapabilityAvailable(c.DVD)
}

// PreparedDryRunReady reports whether this bundle can receive state and execute a dry run.
func (c CoreCapabilities) PreparedDryRunReady() bool {
	return c.PreparedGenerationReady() && CapabilityAvailable(c.DryRun)
}

// BindCoreCapabilities exposes svc through every production capability and
// returns svc separately as its lifecycle owner. A nil service yields an empty
// bundle and nil owner; callers must close the returned owner exactly once.
func BindCoreCapabilities(svc *core.Core) (CoreCapabilities, LifecycleOwner) {
	if svc == nil {
		return CoreCapabilities{}, nil
	}
	return CoreCapabilities{
		Metadata:                   svc,
		ReleasePreparation:         svc,
		Selection:                  svc,
		Preparation:                svc,
		UploadReview:               svc,
		DryRun:                     svc,
		DuplicateExecution:         svc,
		UploadExecution:            svc,
		Screenshots:                svc,
		HostedImages:               svc,
		DVD:                        svc,
		Description:                svc,
		Playlists:                  svc,
		History:                    svc,
		PreparedGenerationTransfer: svc,
		DiagnosticProbe:            svc,
	}, svc
}

var (
	_ MetadataCapability           = (*core.Core)(nil)
	_ ReleasePreparationCapability = (*core.Core)(nil)
	_ SelectionCapability          = (*core.Core)(nil)
	_ PreparationCapability        = (*core.Core)(nil)
	_ UploadReviewCapability       = (*core.Core)(nil)
	_ DryRunCapability             = (*core.Core)(nil)
	_ DuplicateExecutionCapability = (*core.Core)(nil)
	_ UploadExecutionCapability    = (*core.Core)(nil)
	_ ScreenshotCapability         = (*core.Core)(nil)
	_ HostedImageCapability        = (*core.Core)(nil)
	_ DVDCapability                = (*core.Core)(nil)
	_ DVDCaptureCapability         = (*core.Core)(nil)
	_ DescriptionCapability        = (*core.Core)(nil)
	_ PlaylistCapability           = (*core.Core)(nil)
	_ HistoryCapability            = (*core.Core)(nil)
	_ PreparedGenerationTransfer   = (*core.Core)(nil)
	_ DiagnosticProbeCapability    = (*core.Core)(nil)
	_ LifecycleOwner               = (*core.Core)(nil)
)
