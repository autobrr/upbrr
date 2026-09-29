// SPDX-License-Identifier: GPL-2.0-or-later

package host

import "testing"

func TestDirectImageURLUsesWsrvSource(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		proxy string
		want  string
	}{
		{
			proxy: "https://wsrv.nl/?n=-1&ll&url=https%3A%2F%2Fthumbs2.imgbox.com%2F13%2Fae%2Fshot_t.png",
			want:  "https://images2.imgbox.com/13/ae/shot_o.png",
		},
		{
			proxy: "https://wsrv.aither.cc/?n=-1&ll&url=https%3A%2F%2Fimg.blutopia.cc%2F2026%2F06%2F14%2Fshot.png",
			want:  "https://img.blutopia.cc/2026/06/14/shot.png",
		},
		{proxy: "https://wsrv.nl/?url=not-a-url", want: ""},
		{proxy: "https://wsrv.nl/?n=-1", want: ""},
		{proxy: "https://wsrv.nl.evil.example/?url=https%3A%2F%2Fexample.org%2Fshot.png", want: "https://wsrv.nl.evil.example/?url=https%3A%2F%2Fexample.org%2Fshot.png"},
	} {
		if got := DirectImageURL(test.proxy); got != test.want {
			t.Errorf("DirectImageURL(%q) = %q, want %q", test.proxy, got, test.want)
		}
	}
}

func TestIsSourceOnlyURL(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		url  string
		want bool
	}{
		{"https://wsrv.nl/?url=https%3A%2F%2Fexample.org%2Fimage.png", true},
		{"https://wsrv.aither.cc/?url=https%3A%2F%2Fexample.org%2Fimage.png", true},
		{"https://passthepopcorn.me/static/image.jpg", true},
		{"http://www.passthepopcorn.me/static/image.jpg", true},
		{"https://notpassthepopcorn.me/image.jpg", false},
		{"https://pixhost.to/images/image.jpg", false},
	} {
		if got := IsSourceOnlyURL(test.url); got != test.want {
			t.Errorf("IsSourceOnlyURL(%q) = %t, want %t", test.url, got, test.want)
		}
	}
}
