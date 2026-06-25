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
	"github.com/autobrr/upbrr/internal/trackers/impl/mtv"
	"github.com/autobrr/upbrr/internal/trackers/impl/ptp"
	"github.com/autobrr/upbrr/pkg/api"
)

type sessionResolver func(context.Context, config.TrackerConfig, string) error

type trackerAdapter struct {
	capability api.TrackerAuthCapability
	resolve    sessionResolver
}

func defaultAdapters() map[string]Adapter {
	adapters := map[string]Adapter{}
	for _, spec := range builtInSpecs() {
		switch spec.id {
		case "MTV":
			adapters[spec.id] = trackerAdapter{capability: capabilityFromSpec(spec), resolve: mtv.ResolveSessionForTrackerAuth}
		case "PTP":
			adapters[spec.id] = trackerAdapter{capability: capabilityFromSpec(spec), resolve: ptp.ResolveSessionForTrackerAuth}
		}
	}
	return adapters
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
	if err := a.resolve(ctx, cfg, dbPath); err != nil {
		return Session{}, classifyAdapterError(a.capability.TrackerID, err)
	}
	return Session{TrackerID: normalizeTrackerID(a.capability.TrackerID), State: SessionStateReady, Message: "session ready"}, nil
}

func (a trackerAdapter) Login(ctx context.Context, cfg config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) (Session, error) {
	return a.Validate(ctx, cfg, dbPath)
}

func (a trackerAdapter) Submit2FA(_ context.Context, _ string, _ string) (Session, error) {
	return Session{}, &UnsupportedAuthError{TrackerID: a.capability.TrackerID, Reason: "manual 2FA continuation unavailable"}
}

func (a trackerAdapter) Delete(ctx context.Context, dbPath string) error {
	if err := cookies.DeleteTrackerCookies(ctx, dbPath, normalizeTrackerID(a.capability.TrackerID)); err != nil {
		return fmt.Errorf("tracker auth: delete %s cookies: %w", normalizeTrackerID(a.capability.TrackerID), err)
	}
	return nil
}

func classifyAdapterError(trackerID string, err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "2fa") || strings.Contains(lower, "tfa") || strings.Contains(lower, "otp_uri"):
		return &Needs2FAError{TrackerID: trackerID, Reason: "2FA required", Err: err}
	case strings.Contains(lower, "username") || strings.Contains(lower, "password") || strings.Contains(lower, "announce_url") || strings.Contains(lower, "not configured"):
		return &AuthRequiredError{TrackerID: trackerID, Reason: "credentials missing", Err: err}
	case strings.Contains(lower, "auth key not found") || strings.Contains(lower, "anti csrf token not found") || strings.Contains(lower, "cookie invalid") || strings.Contains(lower, "no cookies found"):
		return &ValidationError{TrackerID: trackerID, ConfirmedInvalid: true, Reason: "stored session invalid", Err: err}
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		return &ValidationError{TrackerID: trackerID, Transient: true, Reason: "remote validation unavailable", Err: err}
	default:
		return &ValidationError{TrackerID: trackerID, Transient: true, Reason: "remote validation failed", Err: err}
	}
}
