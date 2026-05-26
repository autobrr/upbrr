// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

type tagOverrideEntry struct {
	Type            string     `json:"type"`
	Source          string     `json:"source"`
	InName          string     `json:"in_name"`
	Template        string     `json:"template"`
	PersonalRelease boolString `json:"personalrelease"`
}

type boolString struct {
	Value bool
	Set   bool
}

func (b *boolString) UnmarshalJSON(data []byte) error {
	if b == nil {
		return errors.New("personalrelease: nil receiver")
	}
	if bytes.Equal(data, []byte("null")) {
		return nil
	}
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		b.Value = value
		b.Set = true
		return nil
	}
	var strValue string
	if err := json.Unmarshal(data, &strValue); err == nil {
		b.Value = strings.EqualFold(strings.TrimSpace(strValue), "true")
		b.Set = true
		return nil
	}
	return errors.New("personalrelease: invalid value")
}

func ApplyTagOverrides(path, currentTag, tagsPath string) (string, *api.TagOverride, error) {
	if strings.TrimSpace(tagsPath) == "" {
		return currentTag, nil, nil
	}

	data, err := os.ReadFile(tagsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return currentTag, nil, nil
		}
		return currentTag, nil, fmt.Errorf("metadata: read tag overrides: %w", err)
	}

	entries := map[string]tagOverrideEntry{}
	if err := json.Unmarshal(data, &entries); err != nil {
		return currentTag, nil, err
	}

	effectiveTag := currentTag
	for key, entry := range entries {
		if entry.InName == key && strings.Contains(path, key) {
			effectiveTag = "-" + key
			break
		}
	}

	if effectiveTag == "" {
		return currentTag, nil, nil
	}

	for key, entry := range entries {
		if strings.TrimPrefix(effectiveTag, "-") != key {
			continue
		}
		override := api.TagOverride{
			Type:            strings.TrimSpace(entry.Type),
			Source:          strings.TrimSpace(entry.Source),
			Template:        strings.TrimSpace(entry.Template),
			PersonalRelease: entry.PersonalRelease.Value,
		}
		if !entry.PersonalRelease.Set {
			override.PersonalRelease = false
		}
		return effectiveTag, &override, nil
	}

	return effectiveTag, nil, nil
}
