// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import "fmt"

func wrapCoreError(err error) error {
	if err != nil {
		return fmt.Errorf("core: %w", err)
	}
	return nil
}

func wrapCoreResult[T any](value T, err error) (T, error) {
	if err != nil {
		return value, fmt.Errorf("core: %w", err)
	}
	return value, nil
}
