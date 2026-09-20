// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"time"
)

// ConfigImpact identifies the workflow projection family affected by an
// effective runtime configuration change.
type ConfigImpact string

const (
	ConfigImpactProvider            ConfigImpact = "provider"
	ConfigImpactTrackers            ConfigImpact = "trackers"
	ConfigImpactDescription         ConfigImpact = "description"
	ConfigImpactScreenshotSelection ConfigImpact = "screenshot_selection"
	ConfigImpactScreenshotCapture   ConfigImpact = "screenshot_capture"
	ConfigImpactImageHosting        ConfigImpact = "image_hosting"
	ConfigImpactClientInjection     ConfigImpact = "client_injection"
	ConfigImpactPresentation        ConfigImpact = "presentation"
)

// ConfigImpactDetail carries the narrow affected tracker lanes when a
// tracker-scoped setting changes. An empty TrackerIDs applies to the complete
// tracker projection because selection or global policy changed.
type ConfigImpactDetail struct {
	Kind       ConfigImpact `json:"kind"`
	TrackerIDs []TrackerID  `json:"trackerIds,omitempty"`
}

// ConfigActivationStatus reports whether the saved effective configuration is
// active, waiting for workflow cleanup, or terminally failed while the prior
// active generation remains in use.
type ConfigActivationStatus string

const (
	ConfigActivationActive  ConfigActivationStatus = "active"
	ConfigActivationPending ConfigActivationStatus = "pending"
	ConfigActivationFailed  ConfigActivationStatus = "failed"
)

// ConfigActivationFailureCode identifies the safe activation stage that
// prevented a deferred candidate from becoming active. It deliberately omits
// the underlying error because candidates and adapter errors may contain
// credentials or local paths.
type ConfigActivationFailureCode string

const (
	ConfigActivationFailureNormalize       ConfigActivationFailureCode = "normalize"
	ConfigActivationFailureValidateStored  ConfigActivationFailureCode = "validate_stored"
	ConfigActivationFailureValidateRuntime ConfigActivationFailureCode = "validate_runtime"
	ConfigActivationFailureBuild           ConfigActivationFailureCode = "build"
	ConfigActivationFailureCookies         ConfigActivationFailureCode = "cookies"
	ConfigActivationFailurePersist         ConfigActivationFailureCode = "persist"
)

// ConfigActivation is the safe, pollable status of the effective config.
// Candidate configuration is deliberately excluded because it may contain
// credentials.
type ConfigActivation struct {
	Status            ConfigActivationStatus      `json:"status"`
	ActivationID      string                      `json:"activationId,omitzero"`
	ActiveGeneration  uint64                      `json:"activeGeneration"`
	Fingerprint       WorkflowFingerprint         `json:"-"`
	PendingGeneration uint64                      `json:"pendingGeneration,omitzero"`
	FailureCode       ConfigActivationFailureCode `json:"failureCode,omitzero"`
	Impacts           []ConfigImpact              `json:"impacts"`
	UpdatedAt         time.Time                   `json:"updatedAt"`
}

// ErrConfigActivationPending reports that a different validated effective
// configuration is already waiting for durable workflow cleanup.
var ErrConfigActivationPending = errors.New("config activation: pending")

// ErrConfigActivationChanged reports that an admitted runtime bundle no
// longer matches the durable effective configuration.
var ErrConfigActivationChanged = errors.New("config activation: generation changed")

// ConfigActivationPendingError carries the safe status for a second settings
// save without exposing its pending candidate or credentials.
type ConfigActivationPendingError struct {
	Activation ConfigActivation
}

func (e *ConfigActivationPendingError) Error() string {
	return fmt.Sprintf("%s: %s", ErrConfigActivationPending, e.Activation.ActivationID)
}

func (e *ConfigActivationPendingError) Unwrap() error {
	return ErrConfigActivationPending
}
