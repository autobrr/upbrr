// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// StaffUploadToken is an operation-local credential, not upload authority.
// It is deliberately excluded from JSON and formatted diagnostics.
type StaffUploadToken struct{ value string }

// NewStaffUploadToken accepts a backend-supplied staff credential without
// persisting it or claiming that a tracker has authorized an exception.
func NewStaffUploadToken(value string) StaffUploadToken { return StaffUploadToken{value: value} }

// Secret is for a tracker's eventual protocol-specific authorization exchange.
// Callers must never copy the result into logs, errors, review or torrent data.
func (t StaffUploadToken) Secret() string { return t.value }

func (StaffUploadToken) String() string               { return "[REDACTED]" }
func (StaffUploadToken) GoString() string             { return "[REDACTED]" }
func (StaffUploadToken) MarshalJSON() ([]byte, error) { return []byte("null"), nil }
