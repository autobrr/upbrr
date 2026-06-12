// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !linux && !windows

package clients

import "errors"

func reflinkFile(_, _ string) error {
	return errors.New("reflink is not supported on this operating system")
}
