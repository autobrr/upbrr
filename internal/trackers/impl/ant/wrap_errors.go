// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import "fmt"

func wrapTrackerResult[T any](value T, err error) (T, error) {
	if err != nil {
		return value, fmt.Errorf("trackers: %w", err)
	}
	return value, nil
}
