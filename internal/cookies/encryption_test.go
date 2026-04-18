// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"fmt"
	"strings"
	"testing"
)

func TestDeriveEncryptionKeyRejectsShortPassword(t *testing.T) {
	t.Parallel()

	_, err := DeriveEncryptionKey("short-pass", strings.Repeat("a", MinSaltLen))
	if err == nil {
		t.Fatal("expected error for short password")
	}
	if err.Error() != fmt.Sprintf("password must be at least %d characters", MinPasswordLen) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeriveEncryptionKeyRejectsShortSalt(t *testing.T) {
	t.Parallel()

	_, err := DeriveEncryptionKey("password1234", strings.Repeat("a", MinSaltLen-1))
	if err == nil {
		t.Fatal("expected error for short salt")
	}
	if err.Error() != fmt.Sprintf("salt must be at least %d bytes", MinSaltLen) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEncryptedCookieEncodeForStorageNilReceiver(t *testing.T) {
	t.Parallel()

	var encrypted *EncryptedCookie
	encoded := encrypted.EncodeForStorage()

	if encoded != (EncodedEncryptedCookie{}) {
		t.Fatalf("expected zero-value encoding for nil receiver, got %+v", encoded)
	}
}

func TestGenerateRandomBytesRejectsNonPositiveLength(t *testing.T) {
	t.Parallel()

	for _, length := range []int{0, -1} {
		t.Run(fmt.Sprintf("length_%d", length), func(t *testing.T) {
			_, err := GenerateRandomBytes(length)
			if err == nil {
				t.Fatalf("expected error for length %d", length)
			}
			if err.Error() != fmt.Sprintf("invalid length %d", length) {
				t.Fatalf("unexpected error for length %d: %v", length, err)
			}
		})
	}
}
