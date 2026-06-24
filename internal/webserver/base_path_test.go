// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import "testing"

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
