// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// CSVList has two legal YAML shapes: a scalar string split on commas, or a
// YAML sequence. Anything else must error. These tests protect the split
// because the tracker default lists are the most-edited config fields.

type csvListWrapper struct {
	Items CSVList `yaml:"items"`
}

func TestCSVListFromScalar(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"items: A, B, C":                {"A", "B", "C"},
		"items: \" A , B , C \"":        {"A", "B", "C"},
		"items: single":                 {"single"},
		"items: \"\"":                   {},
		"items: ' , , '":                {},
		"items: A,,B":                   {"A", "B"},
		"items: 'A,B'":                  {"A", "B"},
		"items: |\n  A,\n  B":           {"A", "B"}, // literal block scalar
		"items: \"日本, Deutsch, ASCII\"": {"日本", "Deutsch", "ASCII"},
	}
	for input, want := range cases {
		t.Run(strings.ReplaceAll(input, "\n", "\\n"), func(t *testing.T) {
			var w csvListWrapper
			if err := yaml.Unmarshal([]byte(input), &w); err != nil {
				t.Fatalf("parse %q: %v", input, err)
			}
			if !reflect.DeepEqual([]string(w.Items), want) {
				t.Fatalf("got %v want %v", []string(w.Items), want)
			}
		})
	}
}

func TestCSVListFromSequence(t *testing.T) {
	t.Parallel()

	input := "items:\n  - A\n  - B\n  - C\n"
	var w csvListWrapper
	if err := yaml.Unmarshal([]byte(input), &w); err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"A", "B", "C"}
	if !reflect.DeepEqual([]string(w.Items), want) {
		t.Fatalf("got %v want %v", w.Items, want)
	}
}

// A sequence with a nested mapping item must error — we don't silently stringify.
func TestCSVListSequenceWithMapping(t *testing.T) {
	t.Parallel()

	input := "items:\n  - A\n  - {nested: map}\n"
	var w csvListWrapper
	if err := yaml.Unmarshal([]byte(input), &w); err == nil {
		t.Fatalf("expected error for mapping inside CSVList sequence")
	}
}

// A mapping supplied in place of a list or scalar must error with a useful
// message.
func TestCSVListMappingRejected(t *testing.T) {
	t.Parallel()

	input := "items:\n  k: v\n"
	var w csvListWrapper
	err := yaml.Unmarshal([]byte(input), &w)
	if err == nil {
		t.Fatalf("expected error for mapping where list expected")
	}
	if !strings.Contains(err.Error(), "expected list or string") {
		t.Fatalf("expected clear error, got %v", err)
	}
}

// An explicit null scalar must leave the CSVList zero-valued without error.
// Otherwise `default_trackers: ~` in example.yaml would crash on load.
func TestCSVListNullScalar(t *testing.T) {
	t.Parallel()

	input := "items: ~\n"
	var w csvListWrapper
	if err := yaml.Unmarshal([]byte(input), &w); err != nil {
		t.Fatalf("null scalar should not error: %v", err)
	}
	if len(w.Items) != 0 {
		t.Fatalf("null should yield empty list, got %v", w.Items)
	}
}

// A missing key must leave the CSVList as its Go zero value (nil slice).
func TestCSVListMissing(t *testing.T) {
	t.Parallel()

	input := "other: ignored\n"
	var w csvListWrapper
	if err := yaml.Unmarshal([]byte(input), &w); err != nil {
		t.Fatalf("missing key should not error: %v", err)
	}
	if w.Items != nil {
		t.Fatalf("expected nil, got %v", w.Items)
	}
}
