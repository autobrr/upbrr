// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package providerid

import "testing"

func TestParseBlurayRelease(t *testing.T) {
	for _, test := range []struct{ raw, target string }{
		{"123", "https://www.blu-ray.com/movies/movies.php?id=123"},
		{"https://WWW.BLU-RAY.COM/movies/Example/123/", "https://www.blu-ray.com/movies/Example/123/"},
		{"https://www.blu-ray.com/movies/Example/123/", "https://www.blu-ray.com/movies/Example/123/"},
		{"http://blu-ray.com/dvd/Example/123/#Review", "https://www.blu-ray.com/dvd/Example/123/"},
		{"https://www.blu-ray.com/movies/movies.php?id=123", "https://www.blu-ray.com/movies/movies.php?id=123"},
		{"https://www.blu-ray.com/dvd/movies.php?id=123", "https://www.blu-ray.com/dvd/movies.php?id=123"},
	} {
		id, target, err := ParseBlurayRelease(test.raw)
		if err != nil || id != "123" || target != test.target {
			t.Fatalf("%q: id=%s target=%s error=%v; want target=%s", test.raw, id, target, err, test.target)
		}
	}
	for _, raw := range []string{"0", "-1", "1e2", "01", "9223372036854775808", "https://www.blu-ray.com/Example/123/", "https://www.blu-ray.com/movies/releases.php?id=123", "https://www.blu-ray.com/movies/Example/0/", "https://www.blu-ray.com/movies/Example/123/?id=456", "https://www.blu-ray.com/movies/movies.php?id=123&id=456", "https://www.blu-ray.com:443/movies/Example/123/", "https://user@www.blu-ray.com/movies/Example/123/", "https://www.blu-ray.com.evil.invalid/movies/Example/123/", "https://evil.invalid/blu-ray.com/movies/Example/123/"} {
		if _, _, err := ParseBlurayRelease(raw); err == nil {
			t.Fatalf("accepted invalid release %q", raw)
		}
	}
}
