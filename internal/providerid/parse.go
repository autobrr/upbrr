// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package providerid

import (
	"strconv"
	"strings"
)

// ParseDecimal returns a nonnegative decimal identifier, or zero for absent,
// malformed or out-of-range values. Fractional and exponent forms are rejected.
func ParseDecimal(value string) int {
	id, err := strconv.ParseUint(strings.TrimSpace(value), 10, strconv.IntSize-1)
	if err != nil {
		return 0
	}
	return int(id)
}
