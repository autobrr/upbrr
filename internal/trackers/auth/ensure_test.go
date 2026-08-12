// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type fakeAdapter struct {
	capability    api.TrackerAuthCapability
	validate      func() (Session, error)
	validateCtx   func(context.Context) (Session, error)
	login         func() (Session, error)
	submit        func(context.Context, config.TrackerConfig, string, api.TrackerAuthLoginRequest) (Session, error)
	validateCalls int
	loginCalls    int
	deleteCalls   int
}

func (a *fakeAdapter) Capability() api.TrackerAuthCapability {
	return a.capability
}

func (a *fakeAdapter) Status(context.Context, config.TrackerConfig, string) (api.TrackerAuthStatus, error) {
	return api.TrackerAuthStatus{TrackerID: a.capability.TrackerID}, nil
}

func (a *fakeAdapter) Validate(ctx context.Context, _ config.TrackerConfig, _ string) (Session, error) {
	a.validateCalls++
	if a.validateCtx != nil {
		return a.validateCtx(ctx)
	}
	if a.validate == nil {
		return Session{}, errors.New("unexpected validate")
	}
	return a.validate()
}

func (a *fakeAdapter) Login(context.Context, config.TrackerConfig, string, api.TrackerAuthLoginRequest) (Session, error) {
	a.loginCalls++
	if a.login == nil {
		return Session{}, errors.New("unexpected login")
	}
	return a.login()
}

func (a *fakeAdapter) Submit2FA(ctx context.Context, cfg config.TrackerConfig, dbPath string, req api.TrackerAuthLoginRequest) (Session, error) {
	if a.submit != nil {
		return a.submit(ctx, cfg, dbPath, req)
	}
	return Session{TrackerID: a.capability.TrackerID, State: SessionStateReady}, nil
}

func (a *fakeAdapter) Delete(context.Context, string) error {
	a.deleteCalls++
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

	session, err := service.EnsureSession(context.Background(), EnsureRequest{TrackerID: "fake", AutoLogin: true})
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if session.TrackerID != "FAKE" || session.State != SessionStateReady {
		t.Fatalf("unexpected session: %#v", session)
	}
	if adapter.deleteCalls != 0 {
		t.Fatal("did not expect valid cookies to be deleted")
	}
}

func TestEnsureSessionAutoLoginFalseStillValidatesStoredSession(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "FAKE", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{TrackerID: "FAKE", State: SessionStateReady}, nil
		},
		login: func() (Session, error) {
			return Session{TrackerID: "FAKE", State: SessionStateReady}, nil
		},
	}
	service := &Service{adapters: map[string]Adapter{"FAKE": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	session, err := service.EnsureSession(context.Background(), EnsureRequest{TrackerID: "FAKE"})
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if session.State != SessionStateReady {
		t.Fatalf("session state = %q, want %q", session.State, SessionStateReady)
	}
	if adapter.validateCalls != 1 || adapter.loginCalls != 0 || adapter.deleteCalls != 0 {
		t.Fatalf("AutoLogin=false calls: validate=%d login=%d delete=%d", adapter.validateCalls, adapter.loginCalls, adapter.deleteCalls)
	}
}

func TestEnsureSessionDeletesConfirmedInvalidAndLogsIn(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{
TrackerID: "FAKE",
 SupportsLogin: true,
 SupportsAutoLogin: true,
},
		validate: func() (Session, error) {
			return Session{}, &ValidationError{
				TrackerID:        "FAKE",
				ConfirmedInvalid: true,
				Err:              errors.New("expired"),
			}
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
	if adapter.deleteCalls == 0 {
		t.Fatal("expected confirmed-invalid session to be deleted before login")
	}
	if adapter.validateCalls != 1 || adapter.loginCalls != 1 || adapter.deleteCalls != 1 {
		t.Fatalf(
			"automatic login calls: validate=%d login=%d delete=%d",
			adapter.validateCalls,
			adapter.loginCalls,
			adapter.deleteCalls,
		)
	}
}

func TestEnsureSessionAutomaticLoginRequiresCapability(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "FAKE", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{}, &AuthRequiredError{TrackerID: "FAKE", Reason: "login required"}
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
	var authRequired *AuthRequiredError
	if !errors.As(err, &authRequired) {
		t.Fatalf("automatic login without capability error = %v", err)
	}
	if adapter.validateCalls != 1 || adapter.loginCalls != 0 || adapter.deleteCalls != 0 {
		t.Fatalf(
			"unsupported automatic login calls: validate=%d login=%d delete=%d",
			adapter.validateCalls,
			adapter.loginCalls,
			adapter.deleteCalls,
		)
	}
}

func TestEnsureSessionAutomaticLoginRequiresCredentials(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{
			TrackerID:         "FAKE",
			SupportsLogin:     true,
			SupportsAutoLogin: true,
		},
		validate: func() (Session, error) {
			return Session{}, &ValidationError{
				TrackerID:        "FAKE",
				ConfirmedInvalid: true,
				Err:              errors.New("expired"),
			}
		},
		login: func() (Session, error) {
			return Session{TrackerID: "FAKE", State: SessionStateReady}, nil
		},
	}
	service := &Service{adapters: map[string]Adapter{"FAKE": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	_, err := service.EnsureSession(context.Background(), EnsureRequest{TrackerID: "FAKE", AutoLogin: true})
	var authRequired *AuthRequiredError
	if !errors.As(err, &authRequired) {
		t.Fatalf("automatic login without credentials error = %v", err)
	}
	if adapter.validateCalls != 1 || adapter.loginCalls != 0 || adapter.deleteCalls != 1 {
		t.Fatalf(
			"missing-credential calls: validate=%d login=%d delete=%d",
			adapter.validateCalls,
			adapter.loginCalls,
			adapter.deleteCalls,
		)
	}
}

func TestEnsureSessionExplicitLoginAllowsManualOnlyCapability(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "FAKE", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{}, &AuthRequiredError{TrackerID: "FAKE", Reason: "login required"}
		},
		login: func() (Session, error) {
			return Session{TrackerID: "FAKE", State: SessionStateReady}, nil
		},
	}
	service := &Service{adapters: map[string]Adapter{"FAKE": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	session, err := service.ensureSession(context.Background(), EnsureRequest{
		TrackerID: "FAKE",
		Config:    config.TrackerConfig{Username: "user", Password: "pass"},
	}, ensureExplicitLogin)
	if err != nil {
		t.Fatalf("explicit login: %v", err)
	}
	if session.State != SessionStateReady || adapter.validateCalls != 1 || adapter.loginCalls != 1 {
		t.Fatalf("explicit login session/calls = %#v/%d/%d", session, adapter.validateCalls, adapter.loginCalls)
	}
}

func TestEnsureSessionKeepsCookiesOnTransientValidationFailure(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "FAKE", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{}, &ValidationError{
				TrackerID: "FAKE",
				Transient: true,
				Err:       errors.New("timeout"),
			}
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
	if adapter.deleteCalls != 0 {
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
	if adapter.deleteCalls != 0 {
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
	if adapter.deleteCalls != 0 {
		t.Fatal("free-text auth key miss must not delete stored session")
	}
}

func TestEnsureSessionKeepsCookiesOnInvalidLookingTransientAdapterText(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{TrackerID: "BTN", SupportsLogin: true},
		validate: func() (Session, error) {
			return Session{}, classifyAdapterError("BTN", errors.New("temporary upstream failure: cookie invalid"))
		},
	}
	service := &Service{adapters: map[string]Adapter{"BTN": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	_, err := service.EnsureSession(context.Background(), EnsureRequest{TrackerID: "BTN", AutoLogin: true})
	if err == nil {
		t.Fatal("expected transient validation error")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || !validationErr.Transient || validationErr.ConfirmedInvalid {
		t.Fatalf("expected transient non-confirmed validation error, got %v", err)
	}
	if adapter.deleteCalls != 0 {
		t.Fatal("transient invalid-looking text must not delete stored session")
	}
}

func TestClassifyAdapterErrorKeepsWrappedContextCancellationTransient(t *testing.T) {
	t.Parallel()

	err := classifyAdapterError("BTN", fmt.Errorf("cookie invalid after retry: %w", context.Canceled))
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || !validationErr.Transient || validationErr.ConfirmedInvalid {
		t.Fatalf("expected context cancellation to stay transient, got %v", err)
	}
}

func TestClassifyAdapterErrorRequiresExplicit2FARequiredText(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		message string
		want2FA bool
	}{
		"explicit missing code": {
			message: "trackers: BTN 2FA required but otp_uri invalid: empty otp_uri",
			want2FA: true,
		},
		"separator colon": {
			message: "trackers: BTN 2FA required: enter code",
			want2FA: true,
		},
		"separator punctuation": {
			message: "trackers: BTN 2FA required, enter code",
			want2FA: true,
		},
		"separator newline": {
			message: "trackers: BTN 2FA required\nenter code",
			want2FA: true,
		},
		"separator parentheses": {
			message: "trackers: BTN (2FA required)",
			want2FA: true,
		},
		"missing form token": {
			message: "trackers: BTN 2FA token not found",
		},
		"otp uri parser": {
			message: "trackers: BTN parse otp_uri: missing secret",
		},
		"tfa layout text": {
			message: "trackers: PTP tfa layout token missing",
		},
		"url path text": {
			message: "trackers: GET https://example.invalid/2fa/setup failed",
		},
		"prefixed phrase": {
			message: "trackers: BTN x2FA required",
		},
		"suffixed phrase": {
			message: "trackers: BTN 2FA requiredx",
		},
		"empty message": {},
		"whitespace message": {
			message: "   ",
		},
		"grouped parser text": {
			message: "trackers: BTN otp_uri contains token2FA required value",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := classifyAdapterError("BTN", errors.New(tt.message))
			var needsErr *Needs2FAError
			got2FA := errors.As(err, &needsErr)
			if got2FA != tt.want2FA {
				t.Fatalf("Needs2FA=%t, want %t for %v", got2FA, tt.want2FA, err)
			}
			if !tt.want2FA {
				var validationErr *ValidationError
				if !errors.As(err, &validationErr) || !validationErr.Transient {
					t.Fatalf("expected transient validation error, got %v", err)
				}
			}
		})
	}
}

func TestEnsureSessionCreatesManual2FAChallenge(t *testing.T) {
	t.Parallel()

	adapter := &fakeAdapter{
		capability: api.TrackerAuthCapability{
			TrackerID:         "FAKE",
			SupportsLogin:     true,
			SupportsManual2FA: true,
		},
		validate: func() (Session, error) {
			return Session{}, &Needs2FAError{TrackerID: "FAKE"}
		},
	}
	service := &Service{adapters: map[string]Adapter{"FAKE": adapter}, challenges: NewChallengeManager(defaultChallengeTTL)}

	session, err := service.EnsureSession(context.Background(), EnsureRequest{TrackerID: "FAKE", AutoLogin: true})
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
