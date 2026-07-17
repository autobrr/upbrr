// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
)

func TestLoginSessionBootstrapsHiddenFieldsAndFollowsRedirect(t *testing.T) {
	t.Parallel()

	handlerErr := make(chan error, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case loginPagePath:
			http.SetCookie(w, &http.Cookie{
				Name:  "bootstrap",
				Value: "ready",
				Path:  "/",
			})
			_, _ = w.Write([]byte(`<form><input type="hidden" name="token" value="login-token"><input type="hidden" name="blank" value=""></form>`))
		case takeLoginPath:
			if err := r.ParseForm(); err != nil {
				handlerErr <- fmt.Errorf("parse login form: %w", err)
				return
			}
			if r.FormValue("username") != "user" || r.FormValue("password") != "pass" || r.FormValue("ssl") != "yes" || r.FormValue("token") != "login-token" {
				handlerErr <- errors.New("login form did not preserve credentials and hidden fields")
			}
			if r.Form.Has("blank") {
				handlerErr <- errors.New("empty hidden field should not be submitted")
			}
			if cookie, err := r.Cookie("bootstrap"); err != nil || cookie.Value != "ready" {
				handlerErr <- errors.New("login bootstrap cookie missing")
			}
			if r.Referer() != "http://"+r.Host+loginPagePath {
				handlerErr <- fmt.Errorf("unexpected login referer %q", r.Referer())
			}
			http.SetCookie(w, &http.Cookie{
				Name:  "session",
				Value: "authenticated",
				Path:  "/",
			})
			http.Redirect(w, r, "/index.php", http.StatusFound)
		case "/index.php":
			if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "authenticated" {
				_, _ = w.Write([]byte(`<form action="/login.php"></form>`))
				return
			}
			_, _ = w.Write([]byte(`<a href="logout.php">Logout</a>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := loginSessionAt(context.Background(), config.TrackerConfig{
		Username: "user",
		Password: "pass",
	}, server.URL)
	if err != nil {
		t.Fatalf("LoginSession: %v", err)
	}
	if client == nil || client.Jar == nil {
		t.Fatal("expected authenticated client with cookie jar")
	}
	close(handlerErr)
	for err := range handlerErr {
		t.Error(err)
	}
}

func TestLoginSessionRejectsRedirectWithoutAuthenticatedMarker(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case loginPagePath:
			_, _ = w.Write([]byte(`<input type="hidden" name="token" value="login-token">`))
		case takeLoginPath:
			http.Redirect(w, r, "/home.php", http.StatusFound)
		case "/home.php":
			_, _ = w.Write([]byte(`<form action="/login.php"><input name="username"></form>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := loginSessionAt(context.Background(), config.TrackerConfig{
		Username: "user",
		Password: "pass",
	}, server.URL)
	if !errors.Is(err, ErrLoginFailed) {
		t.Fatalf("expected ErrLoginFailed, got %v", err)
	}
	if client != nil {
		t.Fatal("expected rejected login to return no client")
	}
}

func TestLoginSessionRejectsWeakAuthenticatedMarkers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		finalPath string
		body      string
	}{
		{
			name:      "public index path without logout anchor",
			finalPath: "/index.php",
			body:      `<form action="/login.php"><input name="username"></form>`,
		},
		{
			name:      "incidental logout text off index",
			finalPath: "/help.php",
			body:      `<p>Read the logout.php troubleshooting guide.</p>`,
		},
		{
			name:      "logout anchor off index",
			finalPath: "/home.php",
			body:      `<a href="/logout.php">Logout</a>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case loginPagePath:
					_, _ = w.Write([]byte(`<input type="hidden" name="token" value="login-token">`))
				case takeLoginPath:
					http.Redirect(w, r, tt.finalPath, http.StatusFound)
				case tt.finalPath:
					_, _ = w.Write([]byte(tt.body))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)

			client, err := loginSessionAt(context.Background(), config.TrackerConfig{
				Username: "user",
				Password: "pass",
			}, server.URL)
			if !errors.Is(err, ErrLoginFailed) {
				t.Fatalf("expected ErrLoginFailed, got %v", err)
			}
			if client != nil {
				t.Fatal("expected weak marker login to return no client")
			}
		})
	}
}
