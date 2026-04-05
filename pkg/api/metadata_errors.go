// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"fmt"
	"strings"
)

const ErrCodeBDMVRescanRequired = "bdmv_rescan_required"

type BDMVRescanRequiredError struct {
	SourcePath        string
	SelectedPlaylists []string
	CachedPlaylists   []string
	MissingPlaylists  []string
}

func (e *BDMVRescanRequiredError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.MissingPlaylists) == 0 {
		return "BDMV rescan confirmation required"
	}
	return fmt.Sprintf("BDMV rescan confirmation required for playlist(s): %s", strings.Join(e.MissingPlaylists, ", "))
}
