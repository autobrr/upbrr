// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !windows && !linux && !darwin

package screenshots

import "context"

func comparisonDeviceIdentity(context.Context) (comparisonDeviceGeneration, error) {
	return comparisonDeviceGeneration{}, nil
}
