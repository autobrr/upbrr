// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"strings"
)

// Normalize returns detached selection intent. Only manual mode accepts a
// release value; provider URL/ID validation belongs to the collection boundary.
func (s *BluraySelection) Normalize() (*BluraySelection, error) {
	if s == nil {
		return nil, nil
	}
	result := *s
	result.Release = strings.TrimSpace(result.Release)
	if result.Mode != "manual" && result.Mode != "skip" && result.Mode != "auto" {
		return nil, fmt.Errorf("invalid Blu-ray selection mode %q", result.Mode)
	}
	if (result.Mode == "manual") != (result.Release != "") {
		return nil, errors.New("blu-ray manual selection requires a release; skip and Auto require no release")
	}
	return &result, nil
}
