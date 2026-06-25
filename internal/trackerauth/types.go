// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	SessionStateReady        = "ready"
	SessionStateAuthRequired = "auth_required"
	SessionStateNeeds2FA     = "needs_2fa"
	SessionStateUnsupported  = "unsupported"
)

type EnsureRequest struct {
	TrackerID string
	Config    config.TrackerConfig
	DBPath    string
	AutoLogin bool
	Login     api.TrackerAuthLoginRequest
}

type Session struct {
	TrackerID   string
	State       string
	Cookies     map[string]string
	Token       string
	ChallengeID string
	Message     string
}

type Adapter interface {
	Capability() api.TrackerAuthCapability
	Status(ctx context.Context, cfg config.TrackerConfig, dbPath string) (api.TrackerAuthStatus, error)
	Validate(ctx context.Context, cfg config.TrackerConfig, dbPath string) (Session, error)
	Login(ctx context.Context, cfg config.TrackerConfig, dbPath string, req api.TrackerAuthLoginRequest) (Session, error)
	Submit2FA(ctx context.Context, challengeID string, code string) (Session, error)
	Delete(ctx context.Context, dbPath string) error
}

type AuthRequiredError struct {
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

type Needs2FAError struct {
	TrackerID   string
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

type ValidationError struct {
	TrackerID        string
	ConfirmedInvalid bool
	Transient        bool
	Reason           string
	Err              error
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
	return strings.ToUpper(strings.TrimSpace(trackerID))
}

func newUnknownTrackerError(trackerID string) error {
	return fmt.Errorf("tracker auth: unknown tracker %s", strings.TrimSpace(trackerID))
}
