// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lcd

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return unit3d.LocalizedReleaseNamePolicy("unit3d/lcd/v2")
}
