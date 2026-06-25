// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import "testing"

func TestParseCookieContentJSONMap(t *testing.T) {
	got, err := ParseCookieContent("MTV.json", `{"session":"abc","nested":{"value":"def"}}`)
	if err != nil {
		t.Fatalf("ParseCookieContent: %v", err)
	}
	if got["session"] != "abc" || got["nested"] != "def" {
		t.Fatalf("unexpected cookies: %#v", got)
	}
}

func TestParseCookieContentNetscape(t *testing.T) {
	got, err := ParseCookieContent("PTP.txt", ".example.test\tTRUE\t/\tTRUE\t0\tsession\tabc\n")
	if err != nil {
		t.Fatalf("ParseCookieContent: %v", err)
	}
	if got["session"] != "abc" {
		t.Fatalf("unexpected cookies: %#v", got)
	}
}
