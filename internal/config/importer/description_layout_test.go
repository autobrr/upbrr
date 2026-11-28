// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package importer

import (
	"encoding/json"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/autobrr/upbrr/internal/config"
)

func TestImportDescriptionSectionOrder(t *testing.T) {
	for _, format := range []struct {
		filename string
		marshal  func(any) ([]byte, error)
	}{
		{"config.yaml", yaml.Marshal},
		{"config.json", json.Marshal},
	} {
		t.Run(format.filename, func(t *testing.T) {
			for _, want := range []config.DescriptionSectionOrder{{"screenshots", "kept"}, {}} {
				cfg := config.Config{Description: config.DescriptionSettingsConfig{SectionOrder: want, AudioPlotsLabel: "Audio tracks"}}
				raw, err := format.marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				got, _, err := ImportFromContent(format.filename, raw)
				if err != nil {
					t.Fatal(err)
				}
				if got.Description.AudioPlotsLabel != "Audio tracks" {
					t.Fatalf("imported audio heading = %q", got.Description.AudioPlotsLabel)
				}
				if order := got.Description.SectionOrder; order == nil || !slices.Equal(order, want) {
					t.Fatalf("imported order = %#v, want %#v", order, want)
				}
			}
		})
	}
}
