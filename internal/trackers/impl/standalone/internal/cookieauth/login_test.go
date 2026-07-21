// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookieauth

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCookieLoginResolverLifecycle(t *testing.T) {
	t.Parallel()

	transientErr := &trackerauth.ValidationError{
		TrackerID: "FF",
		Transient: true,
		Reason:    "unavailable",
	}
	tests := []struct {
		name              string
		load              func(context.Context, string) ([]*http.Cookie, error)
		validationErr     error
		hasCredentials    bool
		wantLoginCalls    int32
		wantValidationErr bool
		wantAuthRequired  bool
	}{
		{
			name: "valid stored cookies",
			load: func(context.Context, string) ([]*http.Cookie, error) {
				return []*http.Cookie{{Name: "session", Value: "valid"}}, nil
			},
			hasCredentials: true,
		},
		{
			name: "confirmed invalid cookies login",
			load: func(context.Context, string) ([]*http.Cookie, error) {
				return []*http.Cookie{{Name: "session", Value: "invalid"}}, nil
			},
			validationErr: &trackerauth.ValidationError{
				TrackerID:        "FF",
				ConfirmedInvalid: true,
				Reason:           "invalid",
			},
			hasCredentials: true,
			wantLoginCalls: 1,
		},
		{
			name: "transient validation failure",
			load: func(context.Context, string) ([]*http.Cookie, error) {
				return []*http.Cookie{{Name: "session", Value: "unknown"}}, nil
			},
			validationErr:     transientErr,
			hasCredentials:    true,
			wantValidationErr: true,
		},
		{
			name: "missing credentials",
			load: func(context.Context, string) ([]*http.Cookie, error) {
				return nil, cookies.ErrTrackerCookiesNotFound
			},
			wantAuthRequired: true,
		},
		{
			name: "missing cookies login",
			load: func(context.Context, string) ([]*http.Cookie, error) {
				return nil, cookies.ErrTrackerCookiesNotFound
			},
			hasCredentials: true,
			wantLoginCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var loginCalls atomic.Int32
			resolver := CookieLoginResolver(CookieLoginSpec{
				TrackerID: "FF",
				BaseURL:   "https://tracker.invalid",
				Load:      test.load,
				Validate: func(context.Context, string, []*http.Cookie) error {
					return test.validationErr
				},
				HasCredential: func(config.TrackerConfig) bool { return test.hasCredentials },
				Login: func(context.Context, config.TrackerConfig, string, string) error {
					loginCalls.Add(1)
					return nil
				},
			})
			err := resolver(context.Background(), config.TrackerConfig{}, "test.db", api.TrackerAuthLoginRequest{})
			if test.wantValidationErr && !errors.Is(err, transientErr) {
				t.Fatalf("resolver error = %v", err)
			}
			var authRequired *trackerauth.AuthRequiredError
			if test.wantAuthRequired != errors.As(err, &authRequired) {
				t.Fatalf("auth-required error = %v", err)
			}
			if !test.wantValidationErr && !test.wantAuthRequired && err != nil {
				t.Fatalf("resolver error = %v", err)
			}
			if loginCalls.Load() != test.wantLoginCalls {
				t.Fatalf("login calls = %d, want %d", loginCalls.Load(), test.wantLoginCalls)
			}
		})
	}
}
