// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"errors"
	"fmt"

	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed defaults/example.yaml
var embeddedExampleYAML []byte

// EmbeddedExampleYAML returns a defensive copy of the embedded example config.
func EmbeddedExampleYAML() []byte {
	if len(embeddedExampleYAML) == 0 {
		return nil
	}
	copied := make([]byte, len(embeddedExampleYAML))
	copy(copied, embeddedExampleYAML)
	return copied
}

func loadEmbeddedDefaultConfigRaw() (*Config, error) {
	if len(embeddedExampleYAML) == 0 {
		return nil, errors.New("embedded default config is empty")
	}
	var cfg Config
	if err := yaml.Unmarshal(embeddedExampleYAML, &cfg); err != nil {
		return nil, fmt.Errorf("parse embedded default config: %w", err)
	}
	return &cfg, nil
}

// LoadEmbeddedDefaultConfig parses the embedded example and backfills any
// tracker defaults missing from its decoded shape.
func LoadEmbeddedDefaultConfig() (*Config, error) {
	cfg, err := loadEmbeddedDefaultConfigRaw()
	if err != nil {
		return nil, err
	}
	if _, err := MergeMissingTrackerDefaults(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
