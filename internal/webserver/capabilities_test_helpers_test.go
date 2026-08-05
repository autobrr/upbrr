// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

func webTestCapabilities(service any) CoreCapabilities {
	return CoreCapabilities{
		ReleaseWorkflow: webCapabilityAs[ReleaseWorkflowCapability](service),
		Description:     webCapabilityAs[DescriptionCapability](service),
		Playlists:       webCapabilityAs[PlaylistCapability](service),
		History:         webCapabilityAs[HistoryCapability](service),
		DiagnosticProbe: webCapabilityAs[DiagnosticProbeCapability](service),
	}
}

func webCapabilityAs[T any](service any) T {
	capability, _ := service.(T)
	return capability
}
