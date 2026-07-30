// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package totp generates tracker-compatible one-time codes from OTP URIs.
package totp

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TOTP interoperability requires SHA-1.
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// FromURI generates a six-digit TOTP code using the current time.
func FromURI(otpURI string) (string, error) {
	return FromURIAt(otpURI, time.Now())
}

// FromURIAt generates a six-digit TOTP code at now. The helper accepts the
// standard secret and optional positive period query fields used by tracker
// OTP URIs.
func FromURIAt(otpURI string, now time.Time) (string, error) {
	trimmed := strings.TrimSpace(otpURI)
	if trimmed == "" {
		return "", errors.New("otp_uri not configured")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse otp_uri: %w", err)
	}
	secret := strings.TrimSpace(parsed.Query().Get("secret"))
	if secret == "" {
		return "", errors.New("otp_uri missing secret")
	}
	period := 30
	if value := strings.TrimSpace(parsed.Query().Get("period")); value != "" {
		if parsedValue, parseErr := strconv.Atoi(value); parseErr == nil && parsedValue > 0 {
			period = parsedValue
		}
	}
	unixTime := now.Unix()
	if unixTime < 0 {
		return "", errors.New("totp counter before unix epoch")
	}
	counterTime := unixTime / int64(period)
	decoder := base32.StdEncoding.WithPadding(base32.NoPadding)
	secretBytes, err := decoder.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("decode otp secret: %w", err)
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counterTime)) //nolint:gosec // Counter is non-negative after the epoch check above.
	mac := hmac.New(sha1.New, secretBytes)
	_, _ = mac.Write(buf)
	hash := mac.Sum(nil)
	offset := hash[len(hash)-1] & 0x0f
	code := (int(hash[offset])&0x7f)<<24 | int(hash[offset+1])<<16 | int(hash[offset+2])<<8 | int(hash[offset+3])
	return fmt.Sprintf("%06d", code%1000000), nil
}
