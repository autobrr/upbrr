// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"fmt"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
)

// mapReleaseWorkflowRequest keeps both HTTP adapters on the application-owned
// request mapping also used by the in-process CLI adapter.
func mapReleaseWorkflowRequest(request any) (releaseworkflow.Command, error) {
	command, err := releaseworkflow.CommandFromRequest(request)
	if err != nil {
		return nil, fmt.Errorf("map release workflow request: %w", err)
	}
	return command, nil
}
