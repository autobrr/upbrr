// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// TrackerCatalogField describes one ordered tracker setting without exposing its current value.
type TrackerCatalogField struct {
	Key        string `json:"key"`
	YAMLKey    string `json:"yamlKey"`
	Default    any    `json:"default"`
	Activation bool   `json:"activation"`
}

// TrackerCatalogEntry combines tracker implementation identity with its config schema.
type TrackerCatalogEntry struct {
	Name              string                `json:"name"`
	Family            string                `json:"family"`
	BaseURL           string                `json:"baseURL"`
	UploadContentMode string                `json:"uploadContentMode"`
	Fields            []TrackerCatalogField `json:"fields"`
	Configured        bool                  `json:"configured"`
}

// TrackerCatalog is the complete settings manifest for supported trackers and
// the names of preserved inert entries that no implementation owns.
type TrackerCatalog struct {
	Entries     []TrackerCatalogEntry `json:"entries"`
	Unsupported []string              `json:"unsupported"`
}
