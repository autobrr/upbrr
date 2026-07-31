// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed defaults/example.yaml
var embeddedExampleYAML []byte

var (
	embeddedDefaultOnce     sync.Once
	embeddedDefaultTemplate *Config
	errEmbeddedDefault      error
)

// EmbeddedExampleYAML returns a defensive copy of the embedded example config.
func EmbeddedExampleYAML() []byte {
	if len(embeddedExampleYAML) == 0 {
		return nil
	}
	copied := make([]byte, len(embeddedExampleYAML))
	copy(copied, embeddedExampleYAML)
	return copied
}

func loadEmbeddedDefaultTemplate() (*Config, error) {
	embeddedDefaultOnce.Do(func() {
		if len(embeddedExampleYAML) == 0 {
			errEmbeddedDefault = errors.New("embedded default config is empty")
			return
		}
		var cfg Config
		if err := yaml.Unmarshal(embeddedExampleYAML, &cfg); err != nil {
			errEmbeddedDefault = fmt.Errorf("parse embedded default config: %w", err)
			return
		}
		embeddedDefaultTemplate = &cfg
	})
	return embeddedDefaultTemplate, errEmbeddedDefault
}

func loadEmbeddedDefaultConfigRaw() (*Config, error) {
	template, err := loadEmbeddedDefaultTemplate()
	if err != nil {
		return nil, err
	}
	return cloneEmbeddedDefaultConfig(template), nil
}

func cloneEmbeddedDefaultConfig(template *Config) *Config {
	if template == nil {
		return nil
	}

	cloned := *template
	cloned.ClientSetup.InjectClients = slices.Clone(template.ClientSetup.InjectClients)
	cloned.ClientSetup.SearchClients = slices.Clone(template.ClientSetup.SearchClients)
	cloned.Trackers.DefaultTrackers = slices.Clone(template.Trackers.DefaultTrackers)
	cloned.Trackers.Trackers = make(map[string]TrackerConfig, len(template.Trackers.Trackers))
	for name, tracker := range template.Trackers.Trackers {
		cloned.Trackers.Trackers[name] = cloneEmbeddedTrackerConfig(tracker)
	}
	cloned.TorrentClients = make(map[string]TorrentClientConfig, len(template.TorrentClients))
	for name, client := range template.TorrentClients {
		cloned.TorrentClients[name] = cloneEmbeddedTorrentClientConfig(client)
	}
	return &cloned
}

func cloneEmbeddedTrackerConfig(tracker TrackerConfig) TrackerConfig {
	tracker.InternalGroups = slices.Clone(tracker.InternalGroups)
	tracker.InjectDelay = cloneDefaultPointer(tracker.InjectDelay)
	tracker.Unknown = cloneDefaultUnknownMap(tracker.Unknown)
	return tracker
}

func cloneEmbeddedTorrentClientConfig(client TorrentClientConfig) TorrentClientConfig {
	client.Tags = slices.Clone(client.Tags)
	client.LinkedFolder = slices.Clone(client.LinkedFolder)
	client.LocalPath = slices.Clone(client.LocalPath)
	client.RemotePath = slices.Clone(client.RemotePath)
	client.AutomaticManagementPaths = slices.Clone(client.AutomaticManagementPaths)
	client.QbitTagsValue = slices.Clone(client.QbitTagsValue)
	client.TLSSkipVerify = cloneDefaultPointer(client.TLSSkipVerify)
	client.AllowFallback = cloneDefaultPointer(client.AllowFallback)
	client.VerifyWebUICertificate = cloneDefaultPointer(client.VerifyWebUICertificate)
	return client
}

func cloneDefaultPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneDefaultUnknownMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = cloneDefaultUnknownValue(value)
	}
	return cloned
}

func cloneDefaultUnknownValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneDefaultUnknownMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneDefaultUnknownValue(item)
		}
		return cloned
	case []string:
		return slices.Clone(typed)
	default:
		return value
	}
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
