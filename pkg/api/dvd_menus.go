// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "context"

// DVDMenuDiscovery describes how the navigation engine found a menu screen.
type DVDMenuDiscovery string

// DVD menu discovery sources serialized across CLI and WebUI.
const (
	// DVDMenuDiscoveryReachable marks a screen reached by deterministic VM navigation.
	DVDMenuDiscoveryReachable DVDMenuDiscovery = "reachable"
	// DVDMenuDiscoveryStructural marks an interactive screen found only in the IFO inventory.
	DVDMenuDiscoveryStructural DVDMenuDiscovery = "structural"
)

// DVDMenuCaptureResult reports persisted captures plus bounded traversal state.
type DVDMenuCaptureResult struct {
	// SourcePath is the host filesystem path of the prepared DVD source.
	SourcePath string
	// Images contains captures persisted for SourcePath in display order.
	Images []DVDMenuCaptureImage
	// SelectedLanguage is the menu language requested from the DVD engine.
	SelectedLanguage string
	// Region is the DVD region selection used for capture; zero means no override.
	Region int
	// DiscoveredMenus is the number of structurally inventoried menu programs.
	DiscoveredMenus int
	// VisitedStates is the number of distinct VM states evaluated during traversal.
	VisitedStates int
	// VisitedButtons is the number of authored button commands evaluated.
	VisitedButtons int
	// MaxItems is the configured upper bound on persisted automatic captures.
	MaxItems int
	// Complete reports that every selected screen was captured without coverage warnings.
	Complete bool
	// Partial reports that warnings prevented complete navigation or rendering coverage.
	Partial bool
	// Truncated reports that MaxItems prevented one or more eligible captures.
	Truncated bool
	// Warnings contains deduplicated, stable, path-free coverage diagnostics.
	Warnings []DVDMenuCaptureWarning
	// Engine describes the pure-Go engine and FFmpeg capability used for capture.
	Engine DVDMenuEngineInfo
}

// DVDMenuCaptureImage is one stored menu screenshot and its discovery source.
type DVDMenuCaptureImage struct {
	// ScreenshotImage contains the persisted local image metadata.
	ScreenshotImage
	// Discovery records whether navigation or structural inventory found the screen.
	Discovery DVDMenuDiscovery
}

// DVDMenuCaptureWarning is a stable, redacted partial-capture diagnostic.
type DVDMenuCaptureWarning struct {
	// Code is a stable machine-readable warning identifier.
	Code string
	// Message is a redacted user-facing description of the incomplete coverage.
	Message string
}

// DVDMenuEngineInfo reports pure-Go engine and external FFmpeg capability.
type DVDMenuEngineInfo struct {
	// EngineVersion identifies the bundled pure-Go DVD navigation engine.
	EngineVersion string
	// SchemaVersion identifies the capture metadata contract version.
	SchemaVersion int
	// SupportedFeatures lists the engine stages available in this build.
	SupportedFeatures []string
	// FFmpegVersion is the bounded first line of FFmpeg version output.
	FFmpegVersion string
	// FFmpegDVDVideo reports whether the required dvdvideo demuxer options are available.
	FFmpegDVDVideo bool
	// MissingFFmpegOptions lists required dvdvideo options absent from the probe.
	MissingFFmpegOptions []string
}

// DVDMenuCapabilityProvider is an optional Core diagnostic contract. Keeping
// it separate from Core avoids forcing unrelated test/runtime adapters to
// implement a capability probe.
type DVDMenuCapabilityProvider interface {
	// DVDMenuCapability probes the current FFmpeg executable without exposing its path.
	DVDMenuCapability(ctx context.Context) (DVDMenuEngineInfo, error)
}
