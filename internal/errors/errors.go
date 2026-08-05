// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package errors defines sentinel failure classes shared across internal package boundaries.
package errors

import "errors"

// These sentinels may be wrapped; callers should classify them with [errors.Is].
var (
	ErrNotImplemented = errors.New("not implemented")
	ErrInvalidInput   = errors.New("invalid input")
	ErrNotFound       = errors.New("not found")
	ErrBannedGroup    = errors.New("banned group")
)
