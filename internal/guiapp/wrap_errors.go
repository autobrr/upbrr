// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import "fmt"

func wrapGUIError(err error) error {
	if err != nil {
		return fmt.Errorf("gui: %w", err)
	}
	return nil
}

func wrapGUIResult[T any](value T, err error) (T, error) {
	if err != nil {
		return value, fmt.Errorf("gui: %w", err)
	}
	return value, nil
}
