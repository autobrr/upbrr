//go:build !windows

// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import "errors"

type unsupportedNativePicker struct{}

var errNativeBrowseWindowsOnly = errors.New("native browse is only available on Windows")

func newNativePicker() nativePicker {
	return unsupportedNativePicker{}
}

func (unsupportedNativePicker) BrowseFile() (string, error) {
	return "", errNativeBrowseWindowsOnly
}

func (unsupportedNativePicker) BrowseImageFiles() ([]string, error) {
	return nil, errNativeBrowseWindowsOnly
}

func (unsupportedNativePicker) BrowseFolder() (string, error) {
	return "", errNativeBrowseWindowsOnly
}
