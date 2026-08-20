// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"net/http"
	"net/url"
	"testing"
)

func TestSessionCookieJarIsolatesExternalHosts(t *testing.T) {
	t.Parallel()

	jar, err := newSessionCookieJar("https://tracker.example", []*http.Cookie{
		{
			Name:   "session",
			Value:  "original",
			Domain: "tracker.example",
			Path:   "/",
		},
	})
	if err != nil {
		t.Fatalf("newSessionCookieJar: %v", err)
	}
	trackerURL, err := url.Parse("https://tracker.example/upload")
	if err != nil {
		t.Fatalf("parse tracker URL: %v", err)
	}
	externalURL, err := url.Parse("https://images.example/screenshot.png")
	if err != nil {
		t.Fatalf("parse external URL: %v", err)
	}

	if cookies := jar.Cookies(externalURL); len(cookies) != 0 {
		t.Fatalf("external cookies = %#v, want none", cookies)
	}
	jar.SetCookies(externalURL, []*http.Cookie{
		{
			Name:   "session",
			Value:  "poisoned",
			Domain: "tracker.example",
			Path:   "/",
		},
	})
	cookies := jar.Cookies(trackerURL)
	if len(cookies) != 1 || cookies[0].Value != "original" {
		t.Fatalf("tracker cookies = %#v, want original session", cookies)
	}
}
