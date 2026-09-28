// SPDX-License-Identifier: GPL-2.0-or-later

package host

import "testing"

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
