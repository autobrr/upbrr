// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// OWASP Password Storage Cheat Sheet Argon2id baseline.
	cookieArgon2Time        = 2
	cookieArgon2MemoryKB    = 19 * 1024
	cookieArgon2Parallelism = 1
	cookieArgon2KeyLen      = 32
)

// DeriveEncryptionKey derives a 256-bit encryption key from a password and salt using Argon2id.
// This follows the same pattern as password hashing in the auth package.
func DeriveEncryptionKey(password string, salt string) ([]byte, error) {
	password = strings.TrimSpace(password)
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	if len(salt) == 0 {
		return nil, errors.New("salt cannot be empty")
	}

	// Argon2id parameters follow OWASP guidance.
	key := argon2.IDKey(
		[]byte(password),
		[]byte(salt),
		cookieArgon2Time,
		cookieArgon2MemoryKB,
		cookieArgon2Parallelism,
		cookieArgon2KeyLen,
	)
	return key, nil
}

// GenerateRandomBytes generates cryptographically secure random bytes.
func GenerateRandomBytes(length int) ([]byte, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return bytes, nil
}

// EncryptedCookie holds the encrypted cookie data with its metadata.
type EncryptedCookie struct {
	Ciphertext []byte // AES-256-GCM encrypted value
	Nonce      []byte // 12-byte IV for GCM
	AuthTag    []byte // 16-byte AEAD authentication tag
}

// EncryptCookieValue encrypts a cookie value using AES-256-GCM.
// Returns the ciphertext, nonce, and authentication tag separately.
// Each call generates a unique nonce (IV) for security.
func EncryptCookieValue(plaintext string, key []byte) (*EncryptedCookie, error) {
	if len(key) != 32 {
		return nil, errors.New("encryption key must be 32 bytes (256 bits)")
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	// Create GCM mode (Galois/Counter Mode for authenticated encryption)
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate a unique nonce (IV) for this encryption
	// GCM typically uses 12-byte (96-bit) nonces
	nonce, err := GenerateRandomBytes(12)
	if err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and authenticate
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)

	// GCM.Seal appends the authentication tag to the ciphertext
	// We need to extract it for separate storage
	if len(ciphertext) < 16 {
		return nil, errors.New("invalid ciphertext length")
	}

	// The last 16 bytes are the auth tag
	authTag := ciphertext[len(ciphertext)-16:]
	actualCiphertext := ciphertext[:len(ciphertext)-16]

	return &EncryptedCookie{
		Ciphertext: actualCiphertext,
		Nonce:      nonce,
		AuthTag:    authTag,
	}, nil
}

// DecryptCookieValue decrypts a cookie value encrypted with AES-256-GCM.
func DecryptCookieValue(encrypted *EncryptedCookie, key []byte) (string, error) {
	if encrypted == nil {
		return "", errors.New("encrypted cookie is nil")
	}

	if len(key) != 32 {
		return "", errors.New("encryption key must be 32 bytes (256 bits)")
	}

	if len(encrypted.Nonce) != 12 {
		return "", errors.New("nonce must be 12 bytes")
	}

	if len(encrypted.AuthTag) != 16 {
		return "", errors.New("auth tag must be 16 bytes")
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Reconstruct the full ciphertext with auth tag for GCM.Open
	fullCiphertext := make([]byte, len(encrypted.Ciphertext)+len(encrypted.AuthTag))
	copy(fullCiphertext, encrypted.Ciphertext)
	copy(fullCiphertext[len(encrypted.Ciphertext):], encrypted.AuthTag)

	// Decrypt and verify
	plaintext, err := gcm.Open(nil, encrypted.Nonce, fullCiphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (possibly corrupted or tampered data): %w", err)
	}

	return string(plaintext), nil
}

// EncodedEncryptedCookie is a base64-encoded representation for database storage.
type EncodedEncryptedCookie struct {
	CiphertextB64 string
	NonceB64      string
	AuthTagB64    string
}

// EncodeForStorage encodes the encrypted cookie data to base64 strings for database storage.
func (ec *EncryptedCookie) EncodeForStorage() EncodedEncryptedCookie {
	return EncodedEncryptedCookie{
		CiphertextB64: base64.StdEncoding.EncodeToString(ec.Ciphertext),
		NonceB64:      base64.StdEncoding.EncodeToString(ec.Nonce),
		AuthTagB64:    base64.StdEncoding.EncodeToString(ec.AuthTag),
	}
}

// DecodeFromStorage decodes base64-encoded encrypted cookie data from database storage.
func DecodeFromStorage(encoded EncodedEncryptedCookie) (*EncryptedCookie, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded.CiphertextB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	nonce, err := base64.StdEncoding.DecodeString(encoded.NonceB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode nonce: %w", err)
	}

	authTag, err := base64.StdEncoding.DecodeString(encoded.AuthTagB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode auth tag: %w", err)
	}

	if len(nonce) != 12 {
		return nil, errors.New("stored nonce has wrong length")
	}

	if len(authTag) != 16 {
		return nil, errors.New("stored auth tag has wrong length")
	}

	return &EncryptedCookie{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		AuthTag:    authTag,
	}, nil
}
