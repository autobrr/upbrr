// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type fakeAdapter struct {
	capability api.TrackerAuthCapability
	validate   func() (Session, error)
	login      func() (Session, error)
	submit     func(context.Context, string, string) (Session, error)
	deleted    bool
}

func (a *fakeAdapter) Capability() api.TrackerAuthCapability {
	return a.capability
}

func (a *fakeAdapter) Status(context.Context, config.TrackerConfig, string) (api.TrackerAuthStatus, error) {
	return api.TrackerAuthStatus{TrackerID: a.capability.TrackerID}, nil
}

func (a *fakeAdapter) Validate(context.Context, config.TrackerConfig, string) (Session, error) {
	return a.validate()
}

func (a *fakeAdapter) Login(context.Context, config.TrackerConfig, string, api.TrackerAuthLoginRequest) (Session, error) {
	return a.login()
}

func (a *fakeAdapter) Submit2FA(ctx context.Context, challengeID string, code string) (Session, error) {
	if a.submit != nil {
		return a.submit(ctx, challengeID, code)
	}
	return Session{TrackerID: a.capability.TrackerID, State: SessionStateReady}, nil
}

func (a *fakeAdapter) Delete(context.Context, string) error {
	a.deleted = true
	return nil
}

func TestEnsureSessionReturnsValidStoredSession(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "FAKE", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{TrackerID: "FAKE", State: SessionStateReady}, nil
		},
	}
	service := &Service{adapters: map[string]Adapter{"FAKE": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	session, err := service.EnsureSession(context.Background(), EnsureRequest{TrackerID: "fake"})
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if session.TrackerID != "FAKE" || session.State != SessionStateReady {
		t.Fatalf("unexpected session: %#v", session)
	}
	if adapter.deleted {
		t.Fatal("did not expect valid cookies to be deleted")
	}
}

func TestEnsureSessionDeletesConfirmedInvalidAndLogsIn(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "FAKE", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{}, &ValidationError{TrackerID: "FAKE", ConfirmedInvalid: true, Err: errors.New("expired")}
		},
		login: func() (Session, error) {
			return Session{TrackerID: "FAKE", State: SessionStateReady}, nil
		},
	}
	service := &Service{adapters: map[string]Adapter{"FAKE": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	_, err := service.EnsureSession(context.Background(), EnsureRequest{
		TrackerID: "FAKE",
		Config:    config.TrackerConfig{Username: "user", Password: "pass"},
		AutoLogin: true,
	})
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if !adapter.deleted {
		t.Fatal("expected confirmed-invalid session to be deleted before login")
	}
}

func TestEnsureSessionKeepsCookiesOnTransientValidationFailure(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "FAKE", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{}, &ValidationError{TrackerID: "FAKE", Transient: true, Err: errors.New("timeout")}
		},
	}
	service := &Service{adapters: map[string]Adapter{"FAKE": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	_, err := service.EnsureSession(context.Background(), EnsureRequest{TrackerID: "FAKE", AutoLogin: true})
	if err == nil {
		t.Fatal("expected transient validation error")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || !validationErr.Transient {
		t.Fatalf("expected transient validation error, got %v", err)
	}
	if adapter.deleted {
		t.Fatal("transient validation failure must not delete stored session")
	}
}

func TestEnsureSessionKeepsCookiesOnPTPMissingAntiCSRFToken(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "PTP", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{}, classifyAdapterError("PTP", errors.New("trackers: PTP anti csrf token not found"))
		},
	}
	service := &Service{adapters: map[string]Adapter{"PTP": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	_, err := service.EnsureSession(context.Background(), EnsureRequest{TrackerID: "PTP", AutoLogin: true})
	if err == nil {
		t.Fatal("expected transient validation error")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.ConfirmedInvalid {
		t.Fatalf("expected non-confirmed validation error, got %v", err)
	}
	if adapter.deleted {
		t.Fatal("PTP parser miss must not delete stored session")
	}
}

func TestEnsureSessionKeepsCookiesOnPTPAuthKeyNotFoundText(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "PTP", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{}, classifyAdapterError("PTP", errors.New("trackers: PTP auth key not found"))
		},
	}
	service := &Service{adapters: map[string]Adapter{"PTP": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	_, err := service.EnsureSession(context.Background(), EnsureRequest{
		TrackerID: "PTP",
		Config:    config.TrackerConfig{Username: "user", Password: "pass"},
		AutoLogin: true,
	})
	if err == nil {
		t.Fatal("expected transient validation error")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || !validationErr.Transient || validationErr.ConfirmedInvalid {
		t.Fatalf("expected transient non-confirmed validation error, got %v", err)
	}
	if adapter.deleted {
		t.Fatal("free-text auth key miss must not delete stored session")
	}
}

func TestEnsureSessionKeepsCookiesOnInvalidLookingTransientAdapterText(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "MTV", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{}, classifyAdapterError("MTV", errors.New("temporary upstream failure: cookie invalid"))
		},
	}
	service := &Service{adapters: map[string]Adapter{"MTV": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	_, err := service.EnsureSession(context.Background(), EnsureRequest{TrackerID: "MTV", AutoLogin: true})
	if err == nil {
		t.Fatal("expected transient validation error")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || !validationErr.Transient || validationErr.ConfirmedInvalid {
		t.Fatalf("expected transient non-confirmed validation error, got %v", err)
	}
	if adapter.deleted {
		t.Fatal("transient invalid-looking text must not delete stored session")
	}
}

func TestClassifyAdapterErrorKeepsWrappedContextCancellationTransient(t *testing.T) {
	t.Parallel()

	err := classifyAdapterError("MTV", fmt.Errorf("cookie invalid after retry: %w", context.Canceled))
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || !validationErr.Transient || validationErr.ConfirmedInvalid {
		t.Fatalf("expected context cancellation to stay transient, got %v", err)
	}
}

func TestEnsureSessionCreatesManual2FAChallenge(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "FAKE", SupportsLogin: true, SupportsManual2FA: true},
		validate: func() (Session, error) {
			return Session{}, &Needs2FAError{TrackerID: "FAKE"}
		},
	}
	service := &Service{adapters: map[string]Adapter{"FAKE": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	session, err := service.EnsureSession(context.Background(), EnsureRequest{TrackerID: "FAKE"})
	if err == nil {
		t.Fatal("expected Needs2FAError")
	}
	var needsErr *Needs2FAError
	if !errors.As(err, &needsErr) {
		t.Fatalf("expected Needs2FAError, got %v", err)
	}
	if session.ChallengeID == "" || needsErr.ChallengeID != session.ChallengeID {
		t.Fatalf("expected challenge id in session and error, got session=%#v err=%#v", session, needsErr)
	}
	if _, ok := service.challenges.Get(session.ChallengeID); !ok {
		t.Fatal("expected challenge to be stored")
	}
}
