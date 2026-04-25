//go:build windows

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import "testing"

func TestDecodePickerOutputUTF8(t *testing.T) {
	input := []byte("D:\\Movies\\A Bridge Too Far 1977 2160p AUS UHD Blu-ray DV HDR HEVC DTS-HD MA 5.1-CultFilms\u2122")
	got := decodePickerOutput(input)
	want := "D:\\Movies\\A Bridge Too Far 1977 2160p AUS UHD Blu-ray DV HDR HEVC DTS-HD MA 5.1-CultFilms\u2122"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestDecodePickerOutputUTF16LEBOM(t *testing.T) {
	path := "D:\\Movies\\A Bridge Too Far 1977 2160p AUS UHD Blu-ray DV HDR HEVC DTS-HD MA 5.1-CultFilms\u2122"
	bytes := []byte{0xFF, 0xFE}
	for _, r := range path {
		if r <= 0xFFFF {
			bytes = append(bytes, byte(r), byte(r>>8))
		}
	}
	got := decodePickerOutput(bytes)
	if got != path {
		t.Fatalf("expected %q, got %q", path, got)
	}
}
