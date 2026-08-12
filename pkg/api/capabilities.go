// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

const (
	// ReleaseWorkflowAPIVersion is the public composite-workflow contract version.
	ReleaseWorkflowAPIVersion = "1.0.0"
)

// ReleaseWorkflowCapabilities describes one authenticated server's safe public
// integration contract. It contains no credentials or secret-bearing endpoints.
type ReleaseWorkflowCapabilities struct {
	ApplicationVersion     string                              `json:"applicationVersion"`
	APIVersion             string                              `json:"apiVersion"`
	OwnerID                string                              `json:"ownerId"`
	Scopes                 []string                            `json:"scopes"`
	Features               ReleaseWorkflowCapabilityFeatures   `json:"features"`
	UploadOptionSchemaHash string                              `json:"uploadOptionSchemaHash"`
	Trackers               []ReleaseWorkflowCapabilityTracker  `json:"trackers"`
	TorrentClients         []ReleaseWorkflowCapabilityResource `json:"torrentClients"`
	ImageHosts             []ReleaseWorkflowCapabilityResource `json:"imageHosts"`
}

// ReleaseWorkflowCapabilityFeatures are stable feature gates for API clients.
type ReleaseWorkflowCapabilityFeatures struct {
	CompositeUpload                   bool `json:"compositeUpload"`
	TypedFeedback                     bool `json:"typedFeedback"`
	StrictEligibleTrackerContinuation bool `json:"strictEligibleTrackerContinuation"`
}

// ReleaseWorkflowCapabilityTracker is safe tracker catalog/runtime metadata.
type ReleaseWorkflowCapabilityTracker struct {
	ID           TrackerID                        `json:"id"`
	DisplayName  string                           `json:"displayName"`
	Configured   bool                             `json:"configured"`
	Default      bool                             `json:"default"`
	Capabilities TrackerCapabilityDescriptor      `json:"capabilities"`
	ConfigFields []ReleaseWorkflowCapabilityField `json:"configFields,omitempty"`
}

// ReleaseWorkflowCapabilityField describes one configurable field without its value.
type ReleaseWorkflowCapabilityField struct {
	Key        string `json:"key"`
	YAMLKey    string `json:"yamlKey"`
	Activation bool   `json:"activation"`
}

// ReleaseWorkflowCapabilityResource is one stable configured-resource choice.
type ReleaseWorkflowCapabilityResource struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Configured  bool   `json:"configured"`
}
