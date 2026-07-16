// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package auth manages tracker login, cookie import, session validation,
// and encrypted auxiliary auth state for tracker implementations.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	// SessionStateReady means an adapter returned auth material ready for tracker requests.
	SessionStateReady = "ready"
	// SessionStateAuthRequired means no usable session is available without user auth material.
	SessionStateAuthRequired = "auth_required"
	// SessionStateNeeds2FA means the auth flow is paused until a manual 2FA code is submitted.
	SessionStateNeeds2FA = "needs_2fa"
	// SessionStateUnsupported means the tracker has no supported auth operation for the requested flow.
	SessionStateUnsupported = "unsupported"
)

// EnsureRequest describes one tracker auth validation or login attempt.
type EnsureRequest struct {
	// TrackerID is the tracker code to validate; comparisons are case-insensitive.
	TrackerID string
	// Config supplies credentials, URLs, passkeys, and OTP settings for the tracker.
	Config config.TrackerConfig
	// DBPath points at the application database used for encrypted cookie and state storage.
	DBPath string
	// AutoLogin allows EnsureSession to attempt credential login after a confirmed invalid stored session.
	AutoLogin bool
	// Login carries optional adapter-specific login input, such as a one-time 2FA code.
	Login api.TrackerAuthLoginRequest
}

// Session is the normalized result returned by a tracker auth adapter.
type Session struct {
	// TrackerID is the normalized tracker code that produced the session.
	TrackerID string
	// State reports whether the session is ready, blocked by auth, paused for 2FA, or unsupported.
	State string
	// Cookies contains session cookies returned by adapters that expose cookie maps.
	Cookies map[string]string
	// Token contains tracker-specific CSRF/auth tokens when an adapter exposes one.
	Token string
	// ChallengeID identifies a pending manual 2FA continuation.
	ChallengeID string
	// Message contains caller-visible detail about the current auth state.
	Message string
}

// Adapter validates and mutates tracker-specific auth material.
type Adapter interface {
	// Capability returns static auth support metadata for the tracker.
	Capability() api.TrackerAuthCapability
	// Status returns local auth state without forcing a remote login.
	Status(ctx context.Context, cfg config.TrackerConfig, dbPath string) (api.TrackerAuthStatus, error)
	// Validate checks whether stored auth material can be used for tracker requests.
	Validate(ctx context.Context, cfg config.TrackerConfig, dbPath string) (Session, error)
	// Login attempts credential-based auth and may persist refreshed auth material.
	Login(ctx context.Context, cfg config.TrackerConfig, dbPath string, req api.TrackerAuthLoginRequest) (Session, error)
	// Submit2FA retries auth with manual 2FA input for a service-verified challenge.
	Submit2FA(ctx context.Context, cfg config.TrackerConfig, dbPath string, req api.TrackerAuthLoginRequest) (Session, error)
	// Delete removes persisted auth material owned by the adapter.
	Delete(ctx context.Context, dbPath string) error
}

// AuthRequiredError reports that no usable session is available and caller-supplied auth material is required.
type AuthRequiredError struct { //nolint:revive // Preserve the established cross-package API during package relocation.
	TrackerID string
	Reason    string
	Err       error
}

func (e *AuthRequiredError) Error() string {
	return trackerAuthError("auth required", e.TrackerID, e.Reason, e.Err)
}

func (e *AuthRequiredError) Unwrap() error {
	return e.Err
}

// Needs2FAError reports that a manual 2FA code is required before auth can continue.
type Needs2FAError struct {
	TrackerID string
	// ChallengeID is set when the caller can continue the flow with Submit2FA.
	ChallengeID string
	Reason      string
	Err         error
}

func (e *Needs2FAError) Error() string {
	return trackerAuthError("2FA required", e.TrackerID, e.Reason, e.Err)
}

func (e *Needs2FAError) Unwrap() error {
	return e.Err
}

// UnsupportedAuthError reports that the requested auth operation is not implemented for the tracker.
type UnsupportedAuthError struct {
	TrackerID string
	Reason    string
	Err       error
}

func (e *UnsupportedAuthError) Error() string {
	return trackerAuthError("unsupported auth", e.TrackerID, e.Reason, e.Err)
}

func (e *UnsupportedAuthError) Unwrap() error {
	return e.Err
}

// ValidationError reports a failed remote or local validation check.
type ValidationError struct {
	TrackerID string
	// ConfirmedInvalid means stored auth material is known bad and may be deleted.
	ConfirmedInvalid bool
	// Transient means stored auth material should be preserved because the failure may be temporary.
	Transient bool
	// Submitted2FARejected means an adapter proved a submitted manual 2FA code reached the tracker and was rejected.
	Submitted2FARejected bool
	Reason               string
	Err                  error
}

func (e *ValidationError) Error() string {
	return trackerAuthError("validation failed", e.TrackerID, e.Reason, e.Err)
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}

func trackerAuthError(kind string, trackerID string, reason string, err error) string {
	parts := []string{"tracker auth"}
	if trimmed := strings.TrimSpace(trackerID); trimmed != "" {
		parts = append(parts, strings.ToUpper(trimmed))
	}
	parts = append(parts, kind)
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		parts = append(parts, trimmed)
	}
	msg := strings.Join(parts, ": ")
	if err != nil {
		msg += ": " + err.Error()
	}
	return msg
}

func asValidationError(err error) (*ValidationError, bool) {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return validationErr, true
	}
	return nil, false
}

func hasLoginCredentials(cfg config.TrackerConfig) bool {
	return strings.TrimSpace(cfg.Username) != "" && strings.TrimSpace(cfg.Password) != ""
}

func normalizeTrackerID(trackerID string) string {
	trimmed := strings.TrimSpace(trackerID)
	var builder strings.Builder
	builder.Grow(len(trimmed))
	for _, r := range trimmed {
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func newUnknownTrackerError(trackerID string) error {
	return fmt.Errorf("tracker auth: unknown tracker %s", strings.TrimSpace(trackerID))
}
