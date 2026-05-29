// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import "fmt"

func wrapUpbrrError(err error) error {
	if err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}
	return nil
}

func wrapUpbrrResult2[A, B any](a A, b B, err error) (A, B, error) {
	if err != nil {
		return a, b, fmt.Errorf("upbrr: %w", err)
	}
	return a, b, nil
}
