// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import "github.com/autobrr/upbrr/internal/trackers"

func (Definition) UploadArtifactPolicy() *trackers.UploadArtifactPolicy {
	return &trackers.UploadArtifactPolicy{Source: "[www.ptskit.org] PTSKIT"}
}
