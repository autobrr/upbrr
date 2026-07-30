// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"

	"github.com/autobrr/upbrr/internal/core"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

// LifecycleOwner is the sole shutdown handle for resources behind a capability bundle.
// Capability interfaces intentionally omit Close so borrowing one never implies ownership.
type LifecycleOwner interface{ Close() error }

// ReleaseWorkflowCapability owns the authenticated browser workflow command/query boundary.
type ReleaseWorkflowCapability interface {
	ContinueReleaseWorkflow(context.Context, string, api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error)
	StartReleaseWorkflowUpload(context.Context, string, api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error)
	SubmitReleaseWorkflowUploadFeedback(
		context.Context,
		string,
		api.WorkflowID,
		api.ReleaseWorkflowUploadFeedback,
	) (releaseworkflow.CommandResult, error)
	ExecuteReleaseWorkflow(context.Context, string, releaseworkflow.Command) (releaseworkflow.CommandResult, error)
	StartReleaseWorkflow(context.Context, string, releaseworkflow.Command) (api.WorkflowOperationStatus, error)
	CurrentReleaseWorkflow(context.Context, string, api.WorkflowID) (releaseworkflow.CommandResult, error)
	ReleaseWorkflowOperation(context.Context, string, api.WorkflowID, api.WorkflowOperationID) (api.WorkflowOperationStatus, error)
	CancelReleaseWorkflowOperation(context.Context, string, api.WorkflowID, api.WorkflowOperationID) (api.WorkflowOperationStatus, error)
	ReleaseWorkflowMediaPlan(context.Context, string, api.WorkflowID) (api.MediaPlan, error)
	PreviewReleaseWorkflowFrame(context.Context, string, api.WorkflowID, api.WorkflowRevision, float64) (api.FramePreview, error)
	OpenReleaseWorkflowPreview(context.Context, string, api.WorkflowID, api.PublicResourceID) (releaseworkflow.MediaArtifactContent, error)
	StageReleaseWorkflowMediaResource(
		context.Context,
		string,
		api.WorkflowID,
		api.WorkflowRevision,
		releaseworkflow.StagedMediaContent,
	) (api.WorkflowResourceRef, error)
	OpenReleaseWorkflowMediaArtifact(
		context.Context,
		string,
		api.WorkflowID,
		api.MediaArtifactSetRef,
		api.PublicResourceID,
	) (releaseworkflow.MediaArtifactContent, error)
}

// DescriptionCapability renders raw description markup without mutating release state.
type DescriptionCapability interface {
	RenderDescription(context.Context, string) (string, error)
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

// DiagnosticProbeCapability reports availability of optional runtime tooling.
type DiagnosticProbeCapability interface {
	DVDMenuCapability(context.Context) (api.DVDMenuEngineInfo, error)
}

// CoreCapabilities is the explicit WebUI workflow bundle. Each field is a
// narrow view of one concrete core; lifecycle ownership remains separate.
// Fields are independently optional so consumers and tests can supply only the
// workflows they invoke. Production bindings populate every field.
type CoreCapabilities struct {
	ReleaseWorkflow ReleaseWorkflowCapability
	Description     DescriptionCapability
	Playlists       PlaylistCapability
	History         HistoryCapability
	DiagnosticProbe DiagnosticProbeCapability
}

// Available reports whether at least one capability is installed.
// It does not validate that a bundle contains every capability a caller needs.
func (c CoreCapabilities) Available() bool {
	return CapabilityAvailable(c.ReleaseWorkflow) || CapabilityAvailable(c.Description) || CapabilityAvailable(c.Playlists) ||
		CapabilityAvailable(c.History) || CapabilityAvailable(c.DiagnosticProbe)
}

// CapabilityAvailable reports whether capability contains a callable value,
// rejecting both nil interfaces and interfaces holding typed nil values.
func CapabilityAvailable(capability any) bool {
	return !capabilityIsNil(capability)
}

// BindCoreCapabilities exposes svc through every production capability and
// returns svc separately as its lifecycle owner. A nil service yields an empty
// bundle and nil owner; callers must close the returned owner exactly once.
func BindCoreCapabilities(svc *core.Core) (CoreCapabilities, LifecycleOwner) {
	if svc == nil {
		return CoreCapabilities{}, nil
	}
	return CoreCapabilities{
		ReleaseWorkflow: svc,
		Description:     svc,
		Playlists:       svc,
		History:         svc,
		DiagnosticProbe: svc,
	}, svc
}

var (
	_ ReleaseWorkflowCapability = (*core.Core)(nil)
	_ DescriptionCapability     = (*core.Core)(nil)
	_ PlaylistCapability        = (*core.Core)(nil)
	_ HistoryCapability         = (*core.Core)(nil)
	_ DiagnosticProbeCapability = (*core.Core)(nil)
	_ LifecycleOwner            = (*core.Core)(nil)
)
