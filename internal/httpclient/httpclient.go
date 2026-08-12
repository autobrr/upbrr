// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package httpclient

import (
	"net/http"
	"time"
)

const (
	// DefaultTimeout and UploadTimeout are whole-request deadlines assigned to [http.Client.Timeout].
	DefaultTimeout = 45 * time.Second
	UploadTimeout  = 60 * time.Second
)

// New returns a client whose timeout defaults to [DefaultTimeout] when timeout is non-positive.
func New(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &http.Client{Timeout: timeout}
}

// CloneWithTimeout shallow-copies base without mutating it and replaces its timeout.
// It preserves all other fields; referenced transport and cookie-jar state remain shared.
// A nil base creates a new client, and a non-positive timeout uses [DefaultTimeout].
func CloneWithTimeout(base *http.Client, timeout time.Duration) *http.Client {
	if base == nil {
		return New(timeout)
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	clone := *base
	clone.Timeout = timeout
	return &clone
}
