// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"encoding/json"
	"errors"

	"gopkg.in/yaml.v3"
)

// CSVList accepts either a YAML sequence or a comma-separated scalar. Scalar
// items are trimmed and blanks removed; sequence values are preserved as read.
type CSVList []string

func (c *CSVList) UnmarshalYAML(value *yaml.Node) error {
	if c == nil {
		return errors.New("config: nil list")
	}
	//nolint:exhaustive // Non-list node kinds share the same validation failure.
	switch value.Kind {
	case yaml.ScalarNode:
		*c = CSVList(splitCSV(value.Value))
		return nil
	case yaml.SequenceNode:
		items := make([]string, 0, len(value.Content))
		for _, node := range value.Content {
			if node.Kind != yaml.ScalarNode {
				return errors.New("config: expected scalar list entry")
			}
			items = append(items, node.Value)
		}
		*c = CSVList(items)
		return nil
	case yaml.MappingNode:
		return errors.New("config: expected list or string")
	default:
		return errors.New("config: unsupported yaml node")
	}
}

// StringList accepts one string or a string sequence in JSON and YAML. Unlike
// [CSVList], a scalar is one item and is not split on commas.
type StringList []string

func (l *StringList) UnmarshalJSON(data []byte) error {
	if l == nil {
		return errors.New("config: nil string list")
	}
	var items []string
	if err := json.Unmarshal(data, &items); err == nil {
		*l = StringList(items)
		return nil
	}
	var item string
	if err := json.Unmarshal(data, &item); err == nil {
		*l = StringList{item}
		return nil
	}
	return errors.New("config: expected string list or string")
}

func (l *StringList) UnmarshalYAML(value *yaml.Node) error {
	if l == nil {
		return errors.New("config: nil string list")
	}
	//nolint:exhaustive // Non-list node kinds share the same validation failure.
	switch value.Kind {
	case yaml.ScalarNode:
		*l = StringList{value.Value}
		return nil
	case yaml.SequenceNode:
		items := make([]string, 0, len(value.Content))
		for _, node := range value.Content {
			if node.Kind != yaml.ScalarNode {
				return errors.New("config: expected scalar list entry")
			}
			items = append(items, node.Value)
		}
		*l = StringList(items)
		return nil
	case yaml.MappingNode:
		return errors.New("config: expected list or string")
	default:
		return errors.New("config: unsupported yaml node")
	}
}
