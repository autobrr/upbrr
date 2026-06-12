// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import "fmt"

func wrapTrackerError(err error) error {
	if err != nil {
		return fmt.Errorf("trackers: %w", err)
	}
	return nil
}
