// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (s *Service) EnsureSession(ctx context.Context, req EnsureRequest) (Session, error) {
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

	session, err := adapter.Validate(ctx, req.Config, req.DBPath)
	if err == nil {
		if strings.TrimSpace(session.State) == "" {
			session.State = SessionStateReady
		}
		if strings.TrimSpace(session.TrackerID) == "" {
			session.TrackerID = trackerID
		}
		return session, nil
	}

	if needs2FA, ok := asNeeds2FAError(err); ok {
		return s.needs2FASession(ctx, trackerID, adapter, needs2FA)
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

	capability := adapter.Capability()
	if !capability.SupportsLogin {
		if confirmedInvalid {
			return Session{}, &AuthRequiredError{TrackerID: trackerID, Reason: "stored session expired", Err: err}
		}
		return Session{}, &UnsupportedAuthError{TrackerID: trackerID, Reason: "credential login unsupported", Err: err}
	}
	if !req.AutoLogin {
		return Session{}, &AuthRequiredError{TrackerID: trackerID, Reason: "login required", Err: err}
	}
	if !hasLoginCredentials(req.Config) {
		return Session{}, &AuthRequiredError{TrackerID: trackerID, Reason: "username/password missing", Err: err}
	}

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

func (s *Service) needs2FASession(ctx context.Context, trackerID string, adapter Adapter, needsErr *Needs2FAError) (Session, error) {
	capability := adapter.Capability()
	if !capability.SupportsManual2FA {
		return Session{}, needsErr
	}
	challengeID := strings.TrimSpace(needsErr.ChallengeID)
	if challengeID == "" {
		challengeID = s.challenges.Create(ctx, trackerID)
	}
	needsErr.ChallengeID = challengeID
	return Session{
		TrackerID:   trackerID,
		State:       SessionStateNeeds2FA,
		ChallengeID: challengeID,
		Message:     "2FA code required",
	}, needsErr
}
