// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package legacy

import (
	"fmt"
	"os"

	"github.com/autobrr/upbrr/internal/config"
)

// ImportFromFile reads a legacy Upload Assistant config.py from disk, parses
// it, and converts it to the current config format. Returns the converted
// config and a list of non-fatal warnings.
func ImportFromFile(path string) (*config.Config, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read legacy config: %w", err)
	}
	return importFromData(data)
}

// ImportFromContent parses a legacy Upload Assistant config.py from raw bytes
// and converts it to the current config format. This is intended for web
// uploads where the file content is sent directly.
func ImportFromContent(data []byte) (*config.Config, []string, error) {
	return importFromData(data)
}

func importFromData(data []byte) (*config.Config, []string, error) {
	legacy, err := ParseLegacyConfig(data)
	if err != nil {
		return nil, nil, err
	}

	template, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("load embedded default config: %w", err)
	}

	cfg, warnings, err := Convert(legacy, template)
	if err != nil {
		return nil, nil, err
	}

	if err := config.MergeMissingTrackerDefaults(cfg); err != nil {
		return nil, nil, fmt.Errorf("merge tracker defaults: %w", err)
	}

	return cfg, warnings, nil
}
