// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers/impl/btn"
	"github.com/autobrr/upbrr/internal/trackers/impl/mtv"
	"github.com/autobrr/upbrr/internal/trackers/impl/ptp"
	"github.com/autobrr/upbrr/internal/trackers/impl/rtf"
	"github.com/autobrr/upbrr/pkg/api"
)

type sessionResolver func(context.Context, config.TrackerConfig, string, api.TrackerAuthLoginRequest) error

type trackerAdapter struct {
	capability api.TrackerAuthCapability
	resolve    sessionResolver
}

func defaultAdapters() map[string]Adapter {
	adapters := map[string]Adapter{}
	for _, spec := range builtInSpecs() {
		switch spec.id {
		case "AR":
			adapters[spec.id] = trackerAdapter{capability: validationOnlyCapability(spec), resolve: resolveARStoredSessionForTrackerAuth}
		case "BTN":
			adapters[spec.id] = trackerAdapter{capability: capabilityFromSpec(spec), resolve: btn.ResolveSessionForTrackerAuthLogin}
		case "FF":
			adapters[spec.id] = trackerAdapter{capability: capabilityFromSpec(spec), resolve: resolveFFSessionForTrackerAuth}
		case "FL":
			adapters[spec.id] = trackerAdapter{capability: capabilityFromSpec(spec), resolve: resolveFLSessionForTrackerAuth}
		case "HDB":
			adapters[spec.id] = trackerAdapter{capability: capabilityFromSpec(spec), resolve: resolveHDBStoredSessionForTrackerAuth}
		case "MTV":
			adapters[spec.id] = trackerAdapter{capability: capabilityFromSpec(spec), resolve: mtv.ResolveSessionForTrackerAuthLogin}
		case "PTP":
			adapters[spec.id] = trackerAdapter{capability: capabilityFromSpec(spec), resolve: ptp.ResolveSessionForTrackerAuthLogin}
		case "RTF":
			adapters[spec.id] = trackerAdapter{capability: capabilityFromSpec(spec), resolve: rtf.ResolveSessionForTrackerAuthLogin}
		case "THR":
			adapters[spec.id] = trackerAdapter{capability: capabilityFromSpec(spec), resolve: resolveTHRSessionForTrackerAuth}
		}
	}
	return adapters
}

// validationOnlyCapability keeps cookie import visible while hiding credential
// login actions for adapters that can only test existing auth material.
func validationOnlyCapability(spec trackerSpec) api.TrackerAuthCapability {
	capability := capabilityFromSpec(spec)
	capability.SupportsLogin = false
	capability.SupportsAutoLogin = false
	capability.SupportsTOTP = false
	capability.SupportsManual2FA = false
	return capability
}

func (s *Service) adapterFor(trackerID string) (Adapter, bool) {
	if s.adapters == nil {
		s.adapters = defaultAdapters()
	}
	adapter, ok := s.adapters[normalizeTrackerID(trackerID)]
	return adapter, ok
}

func (a trackerAdapter) Capability() api.TrackerAuthCapability {
	return a.capability
}

func (a trackerAdapter) Status(ctx context.Context, cfg config.TrackerConfig, dbPath string) (api.TrackerAuthStatus, error) {
	_ = cfg
	trackerID := normalizeTrackerID(a.capability.TrackerID)
	status := api.TrackerAuthStatus{
		TrackerID:        trackerID,
		DisplayName:      trackerID,
		State:            StateNotConfigured,
		LastCheckedAt:    time.Now().UTC().Format(time.RFC3339),
		EncryptedStorage: strings.TrimSpace(dbPath) != "",
	}
	if a.capability.SupportsCookieFile {
		values, err := cookies.LoadTrackerCookieMap(ctx, dbPath, trackerID)
		if err == nil && len(values) > 0 {
			status.CookieCount = len(values)
			status.State = StateHasCookies
		}
	}
	return status, nil
}

func (a trackerAdapter) Validate(ctx context.Context, cfg config.TrackerConfig, dbPath string) (Session, error) {
	if a.resolve == nil {
		return Session{}, &UnsupportedAuthError{TrackerID: a.capability.TrackerID, Reason: "remote validation unavailable"}
	}
	if err := a.resolve(ctx, cfg, dbPath, api.TrackerAuthLoginRequest{}); err != nil {
		return Session{}, classifyAdapterError(a.capability.TrackerID, err)
	}
	return Session{TrackerID: normalizeTrackerID(a.capability.TrackerID), State: SessionStateReady, Message: "session ready"}, nil
}

func (a trackerAdapter) Login(ctx context.Context, cfg config.TrackerConfig, dbPath string, req api.TrackerAuthLoginRequest) (Session, error) {
	if a.resolve == nil {
		return Session{}, &UnsupportedAuthError{TrackerID: a.capability.TrackerID, Reason: "remote login unavailable"}
	}
	if err := a.resolve(ctx, cfg, dbPath, req); err != nil {
		return Session{}, classifyAdapterError(a.capability.TrackerID, err)
	}
	return Session{TrackerID: normalizeTrackerID(a.capability.TrackerID), State: SessionStateReady, Message: "session ready"}, nil
}

func (a trackerAdapter) Submit2FA(ctx context.Context, cfg config.TrackerConfig, dbPath string, req api.TrackerAuthLoginRequest) (Session, error) {
	return a.Login(ctx, cfg, dbPath, req)
}

func (a trackerAdapter) Delete(ctx context.Context, dbPath string) error {
	if err := cookies.DeleteTrackerCookies(ctx, dbPath, normalizeTrackerID(a.capability.TrackerID)); err != nil {
		return fmt.Errorf("tracker auth: delete %s cookies: %w", normalizeTrackerID(a.capability.TrackerID), err)
	}
	return nil
}

// classifyAdapterError maps tracker adapter failures to typed auth errors used
// by EnsureSession. Only explicit 2FA-required failures become manual
// challenges; parser/layout failures and other remote failures remain transient.
func classifyAdapterError(trackerID string, err error) error {
	if err == nil {
		return nil
	}
	var authRequired *AuthRequiredError
	if errors.As(err, &authRequired) {
		return authRequired
	}
	var needs2FA *Needs2FAError
	if errors.As(err, &needs2FA) {
		return needs2FA
	}
	var unsupported *UnsupportedAuthError
	if errors.As(err, &unsupported) {
		return unsupported
	}
	if validation, ok := asValidationError(err); ok {
		return validation
	}
	if strings.EqualFold(trackerID, "BTN") && strings.Contains(strings.ToLower(err.Error()), "stored session confirmed invalid") {
		return &ValidationError{TrackerID: trackerID, ConfirmedInvalid: true, Reason: "stored session expired", Err: err}
	}
	if isSubmitted2FARejected(err) {
		return &ValidationError{TrackerID: trackerID, Transient: true, Submitted2FARejected: true, Reason: "submitted 2FA rejected", Err: err}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &ValidationError{TrackerID: trackerID, Transient: true, Reason: "remote validation unavailable", Err: err}
	}
	lower := strings.ToLower(err.Error())
	switch {
	case contains2FARequiredText(lower):
		return &Needs2FAError{TrackerID: trackerID, Reason: "2FA required", Err: err}
	case strings.Contains(lower, "username") || strings.Contains(lower, "password") || strings.Contains(lower, "announce_url") || strings.Contains(lower, "not configured"):
		return &AuthRequiredError{TrackerID: trackerID, Reason: "credentials missing", Err: err}
	default:
		return &ValidationError{TrackerID: trackerID, Transient: true, Reason: "remote validation failed", Err: err}
	}
}

func isSubmitted2FARejected(err error) bool {
	return errors.Is(err, ptp.ErrSubmitted2FARejected) || errors.Is(err, mtv.ErrSubmitted2FARejected) || errors.Is(err, btn.ErrSubmitted2FARejected)
}

// contains2FARequiredText matches only the standalone "2FA required" phrase so
// parser details such as token names or setup URLs do not create manual 2FA
// challenges.
func contains2FARequiredText(lower string) bool {
	const phrase = "2fa required"
	for offset := 0; offset < len(lower); {
		idx := strings.Index(lower[offset:], phrase)
		if idx < 0 {
			return false
		}
		start := offset + idx
		end := start + len(phrase)
		if isClassifierBoundary(lower, start-1) && isClassifierBoundary(lower, end) {
			return true
		}
		offset = start + 1
	}
	return false
}

func isClassifierBoundary(s string, idx int) bool {
	if idx < 0 || idx >= len(s) {
		return true
	}
	c := s[idx]
	return (c < 'a' || c > 'z') && (c < '0' || c > '9')
}
