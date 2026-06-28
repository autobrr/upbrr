// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"strings"
	"testing"
)

func TestNormalizeBaseURLAcceptsCanonicalInputs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: ""},
		{name: "root", raw: "/", want: ""},
		{name: "path without slash", raw: "upbrr", want: "/upbrr"},
		{name: "path with slash", raw: "/upbrr/", want: "/upbrr"},
		{name: "path query fragment stripped", raw: "/upbrr/?token=secret#frag", want: "/upbrr"},
		{name: "absolute https", raw: " https://example.test/upbrr/ ", want: "https://example.test/upbrr/"},
		{name: "absolute http", raw: "http://example.test/upbrr", want: "http://example.test/upbrr/"},
		{name: "absolute ipv6", raw: "http://[::1]:7480/upbrr?token=secret#frag", want: "http://[::1]:7480/upbrr/"},
		{name: "absolute root query stripped", raw: "https://example.test/?token=secret#frag", want: "https://example.test/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeBaseURL(tc.raw)
			if err != nil {
				t.Fatalf("NormalizeBaseURL(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestNormalizeBaseURLRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "protocol relative", raw: "//example.test/upbrr", want: "protocol-relative"},
		{name: "javascript", raw: "javascript:alert(1)", want: "http or https"},
		{name: "file", raw: "file:///tmp/upbrr", want: "http or https"},
		{name: "ftp", raw: "ftp://example.test/upbrr", want: "http or https"},
		{name: "missing host", raw: "https:/upbrr", want: "must include a host"},
		{name: "userinfo", raw: "https://user@example.test/upbrr", want: "userinfo"},
		{name: "path traversal", raw: "/upbrr/../admin", want: "path traversal"},
		{name: "encoded traversal", raw: "/upbrr/%2e%2e/admin", want: "path traversal"},
		{name: "query only", raw: "?base=/upbrr", want: "must include a path"},
		{name: "fragment only", raw: "#upbrr", want: "must include a path"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NormalizeBaseURL(tc.raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NormalizeBaseURL(%q) error = %v, want substring %q", tc.raw, err, tc.want)
			}
		})
	}
}

func TestExternalBasePathNormalizesBaseURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: ""},
		{name: "root absolute url", raw: "https://example.test/", want: ""},
		{name: "absolute url path", raw: " https://example.test/upbrr/ ", want: "/upbrr"},
		{name: "path with slash", raw: "/upbrr/", want: "/upbrr"},
		{name: "path without slash", raw: "upbrr", want: "/upbrr"},
		{name: "nested path", raw: "https://example.test/tools/upbrr/?token=ignored", want: "/tools/upbrr"},
		{name: "query only", raw: "https://example.test?token=ignored", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := externalBasePath(tc.raw); got != tc.want {
				t.Fatalf("externalBasePath(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestJoinBasePath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		base   string
		suffix string
		want   string
	}{
		{base: "", suffix: "/api/events", want: "/api/events"},
		{base: "/", suffix: "/api/events", want: "/api/events"},
		{base: "/upbrr", suffix: "/api/events", want: "/upbrr/api/events"},
		{base: "/upbrr/", suffix: "api/events", want: "/upbrr/api/events"},
	}

	for _, tc := range cases {
		t.Run(tc.base+" "+tc.suffix, func(t *testing.T) {
			t.Parallel()
			if got := joinBasePath(tc.base, tc.suffix); got != tc.want {
				t.Fatalf("joinBasePath(%q, %q) = %q, want %q", tc.base, tc.suffix, got, tc.want)
			}
		})
	}
}
