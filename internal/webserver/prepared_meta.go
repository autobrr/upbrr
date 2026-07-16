// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"errors"
	"reflect"
)

var (
	// ErrPreparedGenerationUnavailable reports an incomplete canonical generation bundle.
	ErrPreparedGenerationUnavailable = errors.New("prepared generation capability unavailable")
	// ErrPreparedUploadUnavailable reports a bundle missing upload execution or transfer support.
	ErrPreparedUploadUnavailable = errors.New("prepared upload capability unavailable")
	// ErrPreparedDupeUnavailable reports a bundle missing dupe execution or transfer support.
	ErrPreparedDupeUnavailable = errors.New("prepared dupe capability unavailable")
	// ErrPreparedDVDUnavailable reports a bundle missing DVD execution or transfer support.
	ErrPreparedDVDUnavailable = errors.New("prepared DVD capability unavailable")
	// ErrPreparedDryRunUnavailable reports a bundle missing dry-run execution or transfer support.
	ErrPreparedDryRunUnavailable = errors.New("prepared dry-run capability unavailable")
)

func capabilityIsNil(capability any) bool {
	if capability == nil {
		return true
	}
	value := reflect.ValueOf(capability)
	kind := value.Kind()
	isNilable := kind == reflect.Chan ||
		kind == reflect.Func ||
		kind == reflect.Interface ||
		kind == reflect.Map ||
		kind == reflect.Pointer ||
		kind == reflect.Slice
	return isNilable && value.IsNil()
}
