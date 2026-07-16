// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"fmt"
	"os"
)

func validateTorrentFileSize(path string, policy *trackerTorrentPolicy) error {
	if policy == nil || policy.maxTorrentBytes <= 0 {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat torrent %q: %w", path, err)
	}
	if info.Size() > policy.maxTorrentBytes {
		return fmt.Errorf("%s torrent file size %d exceeds max %d", policy.name, info.Size(), policy.maxTorrentBytes)
	}
	return nil
}
