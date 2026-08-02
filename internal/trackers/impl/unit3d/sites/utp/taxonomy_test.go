// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package utp

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestTypeID(t *testing.T) {
	cases := map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"ENCODE": "3",
		"WEBDL":  "4",
		"WEBRIP": "5",
		"HDTV":   "6",
	}
	for typeValue, want := range cases {
		meta := api.UploadSubject{Type: typeValue}
		if got := typeID(meta); got != want {
			t.Fatalf("type %q: expected %q, got %q", typeValue, want, got)
		}
	}
	// Unknown type falls back to ENCODE (3).
	if got := typeID(api.UploadSubject{Type: "MYSTERY"}); got != "3" {
		t.Fatalf("expected unknown type fallback=3, got %q", got)
	}
}

func TestResolutionID(t *testing.T) {
	cases := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1080p": "3",
		"1080i": "4",
		// Uppercase variants must still map through the lowercase lookup.
		"1080P": "3",
		"2160P": "2",
	}
	for resolution, want := range cases {
		meta := api.UploadSubject{Release: api.ReleaseInfo{Resolution: resolution}}
		if got := resolutionID(meta); got != want {
			t.Fatalf("resolution %q: expected %q, got %q", resolution, want, got)
		}
	}
	// Every other resolution files under Other (11).
	for _, resolution := range []string{"720p", "576p", ""} {
		meta := api.UploadSubject{Release: api.ReleaseInfo{Resolution: resolution}}
		if got := resolutionID(meta); got != "11" {
			t.Fatalf("resolution %q: expected fallback=11, got %q", resolution, got)
		}
	}
}
