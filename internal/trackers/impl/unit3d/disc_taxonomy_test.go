// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUnit3DDiscTaxonomyPayload(t *testing.T) {
	for _, discType := range []string{"BDMV", "DVD", "HDDVD"} {
		t.Run(discType, func(t *testing.T) {
			data, err := buildUnit3DData(trackers.PreparationInput{Tracker: "EXAMPLE", Meta: api.UploadSubject{
				Identity:    api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				Type:        "DISC",
				DiscType:    discType,
				Region:      " gbr ",
				Distributor: "Arrow",
			}}, "Example Disc", "description", "media", "bdinfo")
			if err != nil {
				t.Fatal(err)
			}
			if data["region_id"] != "78" || data["distributor_id"] != "75" {
				t.Fatalf("disc taxonomy = region %q distributor %q", data["region_id"], data["distributor_id"])
			}
		})
	}
}

func TestUnit3DDiscCatalogResolution(t *testing.T) {
	tests := []struct {
		value       string
		region      string
		distributor string
	}{
		{" usa ", "229", ""}, {"CZE", "244", ""}, {"Criterion", "", "218"}, {"aRrOw", "", "75"},
		{"9977", "9977", "9977"}, {"0", "", ""}, {"-1", "", ""}, {"", "", ""},
		{"A", "", ""}, {"B", "", ""}, {"C", "", ""}, {"Unknown publisher", "", ""},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			if got := RegionID(test.value); got != test.region {
				t.Errorf("region=%q want %q", got, test.region)
			}
			if got := DistributorID(test.value); got != test.distributor {
				t.Errorf("distributor=%q want %q", got, test.distributor)
			}
		})
	}
	if len(unit3DRegionIDs) != 244 || len(unit3DDistributorIDs) != 965 {
		t.Fatal("official default catalog incomplete")
	}
}

func TestUnit3DDiscTaxonomySiteOverrides(t *testing.T) {
	profile := SiteProfile{
		ResolveRegionID: func(value string) string {
			if value == "GBR" {
				return "978"
			}
			if value == "CUSTOM" {
				return "979"
			}
			return ""
		},
		ResolveDistributorID: func(value string) string {
			if value == "Arrow" {
				return "975"
			}
			if value == "Custom Publisher" {
				return "976"
			}
			return ""
		},
	}
	for _, test := range []struct{ region, distributor, wantRegion, wantDistributor string }{
		{"GBR", "Arrow", "978", "975"}, {"CUSTOM", "Custom Publisher", "979", "976"},
		{"USA", "Criterion", "229", "218"}, {"991", "992", "991", "992"}, {"B", "Unknown", "", ""},
	} {
		t.Run(test.region+test.distributor, func(t *testing.T) {
			data, err := buildUnit3DData(trackers.PreparationInput{Tracker: "EXAMPLE", Meta: api.UploadSubject{
				Identity:    api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				Type:        "DISC",
				DiscType:    "BDMV",
				Region:      test.region,
				Distributor: test.distributor,
			}}, "Example Disc", "description", "media", "bdinfo", profile)
			if err != nil {
				t.Fatal(err)
			}
			if data["region_id"] != test.wantRegion || data["distributor_id"] != test.wantDistributor {
				t.Fatalf("payload=%v", data)
			}
		})
	}
}

func TestUnit3DNonDiscTaxonomyUnchanged(t *testing.T) {
	data, err := buildUnit3DData(trackers.PreparationInput{Tracker: "EXAMPLE", Meta: api.UploadSubject{
		Identity:    api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		Type:        "WEBDL",
		Region:      "USA",
		Distributor: "Arrow",
	}}, "Example Web", "description", "media", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data["region_id"]; ok {
		t.Fatal("added disc region to non-disc payload")
	}
	if _, ok := data["distributor_id"]; ok {
		t.Fatal("added disc publisher to non-disc payload")
	}
}

func TestUnit3DUnknownDiscTaxonomyDiagnostics(t *testing.T) {
	logger := &discTaxonomyLogger{}
	data := map[string]string{}
	applyDiscTaxonomy(trackers.PreparationInput{
Tracker: "EXAMPLE",
 Logger: logger,
 Meta: api.UploadSubject{
		DiscType: "BDMV",
 Region: "private-country",
 Distributor: "private-publisher",
	},
}, data, SiteProfile{})
	logs := strings.Join(logger.messages, "\n")
	if len(data) != 0 || strings.Count(logs, "decision=omitted_unknown") != 2 {
		t.Fatalf("unknown taxonomy not diagnosed: data=%v logs=%s", data, logs)
	}
	if strings.Contains(logs, "private-") {
		t.Fatal("taxonomy diagnostics exposed source values")
	}
}

type discTaxonomyLogger struct {
	api.NopLogger
	messages []string
}

func (l *discTaxonomyLogger) Debugf(format string, args ...any) {
	l.messages = append(l.messages, fmt.Sprintf(format, args...))
}
