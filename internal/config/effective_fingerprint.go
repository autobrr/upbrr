// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

// EffectiveConfigFingerprint identifies settings that can affect a running
// workflow. The database location is process-local plumbing, not effective
// workflow configuration.
func EffectiveConfigFingerprint(cfg Config) (api.WorkflowFingerprint, error) {
	cfg.MainSettings.DBPath = ""
	fingerprint, err := api.CanonicalWorkflowFingerprint(cfg)
	if err != nil {
		return "", fmt.Errorf("canonical effective config fingerprint: %w", err)
	}
	return fingerprint, nil
}
