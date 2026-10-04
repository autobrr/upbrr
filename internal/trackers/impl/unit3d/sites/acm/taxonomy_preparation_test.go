// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestACMTaxonomyPreparationUsesConfiguredResolvers(t *testing.T) {
	site := Profile().Site
	site.ResolveRegionID = func(value string) string {
		switch value {
		case "GBR":
			return "978"
		case "CUSTOM":
			return "979"
		case "991":
			return "1"
		}
		return ""
	}
	site.ResolveDistributorID = func(value string) string {
		switch value {
		case "Arrow":
			return "975"
		case "Custom Publisher":
			return "976"
		case "992":
			return "1"
		}
		return ""
	}
	definition := unit3d.NewWithProfile(profileWithTaxonomy(site))
	for _, test := range []struct{ region, distributor, wantRegion, wantDistributor string }{
		{"USA", "Criterion", "229", "218"}, {"GBR", "Arrow", "978", "975"},
		{"CUSTOM", "Custom Publisher", "979", "976"}, {"991", "992", "991", "992"},
	} {
		t.Run(test.region, func(t *testing.T) {
			dir := t.TempDir()
			mediaPath := filepath.Join(dir, "mediainfo.txt")
			torrentPath := filepath.Join(dir, "example.torrent")
			for _, path := range []string{mediaPath, torrentPath} {
				if err := os.WriteFile(path, []byte("Synthetic preview evidence"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, discType := range []string{"DVD", ""} {
				kind := "DISC"
				if discType == "" {
					kind = "WEBDL"
				}
				name := "Example Release 2026-GRP"
				plan, failure := definition.Prepare(t.Context(), trackers.PreparationInput{
					Intent: trackers.PreparationIntentDryRun,
 Tracker: "ACM",
 Logger: api.NopLogger{},
					RequestedUploadName: &name,
					TrackerConfig:       config.TrackerConfig{APIKey: "test-key"},
					Assets:              &trackers.DescriptionAssets{Description: "Synthetic description", Final: true},
					Meta: api.UploadSubject{
						SourcePath: filepath.Join(dir, "Example"),
 ReleaseName: name,
						Assessments:       api.ReleaseAssessments{MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent},
						MediaInfoTextPath: mediaPath,
 TorrentPath: torrentPath,
						Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
						Release:  api.ReleaseInfo{
Title: "Example",
 Year: 2026,
 Resolution: "480p",
 Size: "DVD5",
},
						Type:     kind,
 DiscType: discType,
 SourceSize: 4 * (1 << 30),
 Region: test.region,
 Distributor: test.distributor,
					},
				})
				if failure != nil {
					t.Fatalf("prepare %q: %v", discType, failure)
				}
				payload := plan.DryRun().Payload
				if payload["region_id"] != test.wantRegion || payload["distributor_id"] != test.wantDistributor {
					t.Fatalf("%q taxonomy lost during preparation: %v", discType, payload)
				}
				if err := plan.Release(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
