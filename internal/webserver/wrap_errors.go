// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import "fmt"

func wrapWebError(err error) error {
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}
	return nil
}

func wrapWebResult[T any](value T, err error) (T, error) {
	if err != nil {
		return value, fmt.Errorf("web: %w", err)
	}
	return value, nil
}
