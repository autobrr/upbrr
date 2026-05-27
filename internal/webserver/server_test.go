// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"strings"
	"testing"
)

func TestNewRejectsDevelopmentNoAuthOnNonLoopbackHost(t *testing.T) {
	_, err := New(Options{
		CLIConfig:         CLIConfig{Host: "0.0.0.0"},
		DevelopmentNoAuth: true,
	})
	if err == nil {
		t.Fatal("expected development no-auth on non-loopback host to fail")
	}
	if !strings.Contains(err.Error(), "--dev-no-auth requires a loopback host") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsDevelopmentNoAuthHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want bool
	}{
		{host: "localhost", want: true},
		{host: "localhost:7480", want: true},
		{host: "127.0.0.1", want: true},
		{host: "::1", want: true},
		{host: "[::1]", want: true},
		{host: "[::1]:7480", want: true},
		{host: "0.0.0.0", want: false},
		{host: "::", want: false},
		{host: "192.168.1.20", want: false},
		{host: "example.com", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			if got := isDevelopmentNoAuthHost(tc.host); got != tc.want {
				t.Fatalf("isDevelopmentNoAuthHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestIsLoopbackHostPort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want bool
	}{
		{host: "localhost:5173", want: true},
		{host: "127.0.0.1:7480", want: true},
		{host: "[::1]:7480", want: true},
		{host: "0.0.0.0:7480", want: false},
		{host: "192.168.1.20:7480", want: false},
		{host: "example.com:7480", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			if got := isLoopbackHostPort(tc.host); got != tc.want {
				t.Fatalf("isLoopbackHostPort(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}
