// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/bluraycom"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCorrectionChoices(t *testing.T) {
	t.Parallel()
	choices, err := CorrectionChoices()
	if err != nil {
		t.Fatal(err)
	}
	byField := make(map[string]map[string]string)
	for field, values := range choices {
		byField[field] = make(map[string]string)
		for _, choice := range values {
			if choice.Value == "" || choice.Label == "" || byField[field][choice.Value] != "" {
				t.Fatalf("invalid or duplicate %s choice: %#v", field, choice)
			}
			byField[field][choice.Value] = choice.Label
		}
	}
	services := metautil.ServiceCodeMap()
	serviceValues := make(map[string]bool)
	for _, value := range services {
		serviceValues[value] = true
		if byField["Service"][value] == "" {
			t.Errorf("service catalog omitted %q", value)
		}
	}
	if len(byField["Service"]) != len(serviceValues) {
		t.Fatal("service catalog contains values absent from the naming alias map")
	}
	if byField["Service"]["AMZN"] != "Amazon Prime Video (AMZN)" || byField["Service"]["NF"] != "Netflix (NF)" {
		t.Fatal("service choices must include full names and stored tokens")
	}
	for value := range byField["Category"] {
		if normalized, normalizeErr := api.NormalizeCanonicalCategory(value); normalizeErr != nil || string(normalized) != value {
			t.Errorf("category %q is not canonical: %v", value, normalizeErr)
		}
	}
	for value := range byField["Type"] {
		if normalizeReleaseType(value) != value {
			t.Errorf("release type %q is not canonical", value)
		}
	}
	for value := range byField["Source"] {
		if !isKnownReleaseSource(value) {
			t.Errorf("source %q is not recognized by metadata naming", value)
		}
	}
	if byField["Source"]["Blu-ray 3D"] == "" {
		t.Fatal("source catalog omitted the supported 3D Blu-ray source")
	}
	for _, value := range []string{"480i", "576i", "1080i", "2160p", "8640p"} {
		if byField["Resolution"][value] == "" {
			t.Errorf("resolution catalog omitted %q", value)
		}
	}
	for value := range byField["Resolution"] {
		if strings.Contains(value, "$") || value == "PN.Selector" {
			t.Errorf("resolution catalog exposed a parser substitution or non-video value %q", value)
		}
	}
	if byField["Region"]["R0"] != "Global (R0)" || byField["Region"]["R1"] != "United States (R1)" {
		t.Fatal("region labels must not repeat parser-provided codes")
	}
	for _, value := range bluraycom.CountryRegionCodes() {
		if byField["Region"][value] == "" {
			t.Errorf("region catalog omitted provider code %q", value)
		}
	}
}
