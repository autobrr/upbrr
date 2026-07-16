// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// TrackerFieldSchema describes one ordered tracker configuration field.
type TrackerFieldSchema struct {
	JSONKey    string
	YAMLKey    string
	Default    any
	Activation bool
}

// TrackerSchema describes the ordered configuration surface for one supported tracker.
type TrackerSchema struct {
	Name   string
	Fields []TrackerFieldSchema
}

var (
	orderedTrackerSchemaOnce sync.Once
	orderedTrackerSchemas    []TrackerSchema
	errOrderedTrackerSchema  error
)

var trackerActivationYAMLKeys = map[string]struct{}{
	"api_key":         {},
	"ApiUser":         {},
	"ApiKey":          {},
	"username":        {},
	"password":        {},
	"passkey":         {},
	"announce_url":    {},
	"my_announce_url": {},
}

// OrderedTrackerSchemas returns a defensive copy of the tracker schemas declared
// by the embedded example config, preserving tracker and field declaration order.
func OrderedTrackerSchemas() ([]TrackerSchema, error) {
	orderedTrackerSchemaOnce.Do(func() {
		orderedTrackerSchemas, errOrderedTrackerSchema = parseOrderedTrackerSchemas(EmbeddedExampleYAML())
	})
	if errOrderedTrackerSchema != nil {
		return nil, errOrderedTrackerSchema
	}
	return cloneTrackerSchemas(orderedTrackerSchemas), nil
}

// TrackerConfigured reports whether any schema activation field has a non-empty
// value. Configured state is intentionally weaker than authentication readiness.
func TrackerConfigured(cfg TrackerConfig, schema TrackerSchema) bool {
	values, err := trackerConfigToJSONMap(cfg)
	if err != nil {
		return false
	}
	for _, field := range schema.Fields {
		if !field.Activation {
			continue
		}
		value, ok := values[field.JSONKey]
		if ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
			return true
		}
	}
	return false
}

func parseOrderedTrackerSchemas(data []byte) ([]TrackerSchema, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("config: parse tracker catalog: %w", err)
	}
	root := document.Content
	if len(root) != 1 || root[0].Kind != yaml.MappingNode {
		return nil, errors.New("config: tracker catalog root must be a mapping")
	}
	trackersNode := mappingValue(root[0], "trackers")
	if trackersNode == nil || trackersNode.Kind != yaml.MappingNode {
		return nil, errors.New("config: tracker catalog is missing trackers mapping")
	}

	initTrackerTagMetadata()
	schemas := make([]TrackerSchema, 0, len(trackersNode.Content)/2)
	seen := make(map[string]struct{}, len(trackersNode.Content)/2)
	for index := 0; index+1 < len(trackersNode.Content); index += 2 {
		name := strings.ToUpper(strings.TrimSpace(trackersNode.Content[index].Value))
		if name == "" || name == "DEFAULT_TRACKERS" || name == "PREFERRED_TRACKER" {
			continue
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("config: duplicate tracker catalog entry %q", name)
		}
		seen[name] = struct{}{}

		entry := trackersNode.Content[index+1]
		if entry.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("config: tracker catalog entry %s must be a mapping", name)
		}
		fields := make([]TrackerFieldSchema, 0, len(entry.Content)/2+2)
		activationCount := 0
		for fieldIndex := 0; fieldIndex+1 < len(entry.Content); fieldIndex += 2 {
			yamlKey := strings.TrimSpace(entry.Content[fieldIndex].Value)
			if strings.EqualFold(yamlKey, "url") {
				return nil, fmt.Errorf("config: tracker catalog entry %s contains deprecated url field", name)
			}
			jsonKey, ok := trackerYAMLToJSON[yamlKey]
			if !ok {
				return nil, fmt.Errorf("config: tracker catalog entry %s has unknown field %q", name, yamlKey)
			}
			var defaultValue any
			if err := entry.Content[fieldIndex+1].Decode(&defaultValue); err != nil {
				return nil, fmt.Errorf("config: decode tracker catalog default %s.%s: %w", name, yamlKey, err)
			}
			_, activation := trackerActivationYAMLKeys[yamlKey]
			if activation {
				activationCount++
				if value, ok := defaultValue.(string); !ok || value != "" {
					return nil, fmt.Errorf("config: tracker catalog activation default %s.%s must be empty", name, yamlKey)
				}
			}
			fields = append(fields, TrackerFieldSchema{
				JSONKey:    jsonKey,
				YAMLKey:    yamlKey,
				Default:    defaultValue,
				Activation: activation,
			})
		}
		if activationCount == 0 {
			return nil, fmt.Errorf("config: tracker catalog entry %s has no activation field", name)
		}
		fields = appendMissingGlobalTrackerFields(fields)
		schemas = append(schemas, TrackerSchema{Name: name, Fields: fields})
	}
	return schemas, nil
}

func appendMissingGlobalTrackerFields(fields []TrackerFieldSchema) []TrackerFieldSchema {
	present := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		present[field.JSONKey] = struct{}{}
	}
	for _, field := range []TrackerFieldSchema{
		{
			JSONKey: "FaviconURL",
			YAMLKey: "favicon_url",
			Default: "",
		},
		{
			JSONKey: "ImageHost",
			YAMLKey: "image_host",
			Default: "",
		},
		{
			JSONKey: "TorrentClient",
			YAMLKey: "torrent_client",
			Default: "",
		},
	} {
		if _, ok := present[field.JSONKey]; !ok {
			fields = append(fields, field)
		}
	}
	return fields
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func cloneTrackerSchemas(schemas []TrackerSchema) []TrackerSchema {
	cloned := make([]TrackerSchema, len(schemas))
	for index, schema := range schemas {
		cloned[index] = TrackerSchema{
			Name:   schema.Name,
			Fields: append([]TrackerFieldSchema(nil), schema.Fields...),
		}
	}
	return cloned
}
