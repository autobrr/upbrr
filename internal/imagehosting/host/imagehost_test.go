// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package host

import "testing"

func TestExtractHost(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"imgbb standard", "https://ibb.co/abc123", "imgbb"},
		{"imgbb with i subdomain", "https://i.ibb.co/image.png", "imgbb"},
		{"pixhost standard", "https://pixhost.cc/abc123.png", "pixhost"},
		{"pixhost show", "https://pixhost.cc/show/123/456.png", "pixhost"},
		{"pixhost legacy", "https://pixhost.to/abc123.png", "pixhost"},
		{"imgbox", "https://imgbox.com/g/abc", "imgbox"},
		{"imgbox cdn", "https://cdn.imgbox.com/image.png", "imgbox"},
		{"beyondhd", "https://beyondhd.co/image/123", "bhd"},
		{"imagebam", "https://imagebam.com/view/abc", "bam"},
		{"onlyimage", "https://onlyimage.org/image.png", "onlyimage"},
		{"ptscreens", "https://ptscreens.com/abc.png", "ptscreens"},
		{"passtheimage", "https://img.passtheima.ge/abc.png", "passtheimage"},
		{"imgur", "https://imgur.com/abc", "imgur"},
		{"imgur with i subdomain", "https://i.imgur.com/abc.jpg", "imgur"},
		{"postimg", "https://postimg.cc/abc", "postimg"},
		{"digitalcore", "https://digitalcore.club/img/abc", "sharex"},
		{"digitalcore img subdomain", "https://img.digitalcore.club/abc.png", "sharex"},
		{"kshare", "https://kshare.club/abc.png", "kshare"},
		{"pterclub img", "https://img.pterclub.com/images/abc.png", "pterclub"},
		{"pterclub s3", "https://s3.pterclub.com/abc.png", "pterclub"},
		{"ilikeshots", "https://yes.ilikeshots.club/abc.png", "ilikeshots"},
		{"unknown host", "https://example.com/image.png", "example.com"},
		{"unknown subdomain", "https://static.example.com/image.png", "static.example.com"},
		{"empty string", "", ""},
		{"invalid url", "not-a-url", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractHost(tt.url)
			if got != tt.want {
				t.Errorf("ExtractHost(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}
