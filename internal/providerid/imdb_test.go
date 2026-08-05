// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package providerid

import "testing"

func TestIMDbFormatsProviderContracts(t *testing.T) {
	tests := []struct {
		name       string
		id         IMDb
		decimal    string
		digits     string
		prefixed   string
		canonicalURL string
	}{
		{
			name:         "negative is absent",
			id:           -1,
			decimal:      "",
			digits:       "",
			prefixed:     "",
			canonicalURL: "",
		},
		{
			name:         "zero is absent",
			id:           0,
			decimal:      "",
			digits:       "",
			prefixed:     "",
			canonicalURL: "",
		},
		{
			name:         "short identifier is padded",
			id:           456,
			decimal:      "456",
			digits:       "0000456",
			prefixed:     "tt0000456",
			canonicalURL: "https://www.imdb.com/title/tt0000456",
		},
		{
			name:         "seven digit identifier is preserved",
			id:           1234567,
			decimal:      "1234567",
			digits:       "1234567",
			prefixed:     "tt1234567",
			canonicalURL: "https://www.imdb.com/title/tt1234567",
		},
		{
			name:         "long identifier is not truncated",
			id:           12345678,
			decimal:      "12345678",
			digits:       "12345678",
			prefixed:     "tt12345678",
			canonicalURL: "https://www.imdb.com/title/tt12345678",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.id.Decimal(); got != test.decimal {
				t.Fatalf("Decimal() = %q, want %q", got, test.decimal)
			}
			if got := test.id.Digits(); got != test.digits {
				t.Fatalf("Digits() = %q, want %q", got, test.digits)
			}
			if got := test.id.Prefixed(); got != test.prefixed {
				t.Fatalf("Prefixed() = %q, want %q", got, test.prefixed)
			}
			if got := test.id.URL(); got != test.canonicalURL {
				t.Fatalf("URL() = %q, want %q", got, test.canonicalURL)
			}
		})
	}
}
