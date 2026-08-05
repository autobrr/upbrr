// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type ensureLoginMode uint8

const (
	ensureAutomaticLogin ensureLoginMode = iota
	ensureExplicitLogin
)

// EnsureSession validates stored tracker auth, optionally attempts capability-owned
// automatic login, and returns the ready session or a typed auth error.
func (s *Service) EnsureSession(ctx context.Context, req EnsureRequest) (Session, error) {
	return s.ensureSession(ctx, req, ensureAutomaticLogin)
}

func (s *Service) ensureSession(ctx context.Context, req EnsureRequest, mode ensureLoginMode) (Session, error) {
	if ctx == nil {
		return Session{}, errors.New("tracker auth: context is required")
	}
	trackerID := normalizeTrackerID(req.TrackerID)
	if trackerID == "" {
		return Session{}, errors.New("tracker auth: tracker id is required")
	}

	adapter, ok := s.adapterFor(trackerID)
	if !ok {
		return Session{}, newUnknownTrackerError(trackerID)
	}
	capability := adapter.Capability()
	s.logDebugf(
		"tracker auth: resolution start tracker=%s automatic_login_requested=%t automatic_login_supported=%t explicit_login=%t",
		trackerLogID(trackerID),
		req.AutoLogin,
		capability.SupportsAutoLogin,
		mode == ensureExplicitLogin,
	)

	session, err := adapter.Validate(ctx, req.Config, req.DBPath)
	if err == nil {
		s.logDebugf("tracker auth: stored session validation tracker=%s decision=ready", trackerLogID(trackerID))
		if strings.TrimSpace(session.State) == "" {
			session.State = SessionStateReady
		}
		if strings.TrimSpace(session.TrackerID) == "" {
			session.TrackerID = trackerID
		}
		return session, nil
	}
	s.logDebugf("tracker auth: stored session validation tracker=%s decision=not_ready", trackerLogID(trackerID))

	if needs2FA, ok := asNeeds2FAError(err); ok {
		return s.needs2FASession(ctx, trackerID, adapter, needs2FA)
	}

	authRequired, requiresAuth := asAuthRequiredError(err)
	if requiresAuth && !capability.SupportsLogin {
		return Session{}, authRequired
	}

	confirmedInvalid := false
	if validationErr, ok := asValidationError(err); ok {
		if validationErr.Transient && !validationErr.ConfirmedInvalid {
			return Session{}, validationErr
		}
		confirmedInvalid = validationErr.ConfirmedInvalid
	}
	if confirmedInvalid {
		if deleteErr := adapter.Delete(ctx, req.DBPath); deleteErr != nil {
			return Session{}, errors.Join(err, deleteErr)
		}
	}

	allowLogin := false
	switch mode {
	case ensureAutomaticLogin:
		allowLogin = req.AutoLogin && capability.SupportsAutoLogin
	case ensureExplicitLogin:
		allowLogin = capability.SupportsLogin
	}
	if !allowLogin {
		if requiresAuth {
			return Session{}, authRequired
		}
		if confirmedInvalid {
			return Session{}, &AuthRequiredError{
				TrackerID: trackerID,
				Reason:    "stored session expired",
				Err:       err,
			}
		}
		if capability.SupportsLogin {
			return Session{}, &AuthRequiredError{
				TrackerID: trackerID,
				Reason:    "automatic login unavailable",
				Err:       err,
			}
		}
		return Session{}, &UnsupportedAuthError{
			TrackerID: trackerID,
			Reason:    "credential login unsupported",
			Err:       err,
		}
	}
	if !hasLoginCredentials(req.Config) {
		return Session{}, &AuthRequiredError{
			TrackerID: trackerID,
			Reason:    "username/password missing",
			Err:       err,
		}
	}

	s.logDebugf("tracker auth: login resolution tracker=%s decision=attempt", trackerLogID(trackerID))
	session, loginErr := adapter.Login(ctx, req.Config, req.DBPath, req.Login)
	if loginErr != nil {
		if needs2FA, ok := asNeeds2FAError(loginErr); ok {
			return s.needs2FASession(ctx, trackerID, adapter, needs2FA)
		}
		return Session{}, fmt.Errorf("tracker auth: login %s: %w", trackerID, loginErr)
	}
	if strings.TrimSpace(session.State) == "" {
		session.State = SessionStateReady
	}
	if strings.TrimSpace(session.TrackerID) == "" {
		session.TrackerID = trackerID
	}
	return session, nil
}

func asNeeds2FAError(err error) (*Needs2FAError, bool) {
	var needsErr *Needs2FAError
	if errors.As(err, &needsErr) {
		return needsErr, true
	}
	return nil, false
}

func asAuthRequiredError(err error) (*AuthRequiredError, bool) {
	var authRequired *AuthRequiredError
	if errors.As(err, &authRequired) {
		return authRequired, true
	}
	return nil, false
}

func (s *Service) needs2FASession(ctx context.Context, trackerID string, adapter Adapter, needsErr *Needs2FAError) (Session, error) {
	capability := adapter.Capability()
	if !capability.SupportsManual2FA {
		return Session{}, needsErr
	}
	challengeID := strings.TrimSpace(needsErr.ChallengeID)
	if challengeID == "" {
		ownerKey, err := s.challengeOwnerKey(trackerID)
		if err != nil {
			return Session{}, err
		}
		challengeID = s.challengeManager().Create(ctx, trackerID, ownerKey)
	}
	needsErr.ChallengeID = challengeID
	return Session{
		TrackerID:   trackerID,
		State:       SessionStateNeeds2FA,
		ChallengeID: challengeID,
		Message:     "2FA code required",
	}, needsErr
}
