// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBluRayRegionRequired(t *testing.T) {
	for _, tc := range []struct {
		disc, region string
		blocked      bool
	}{
		{"BDMV", "", true}, {" bdmv ", "  ", true}, {"BDMV", "Unknown Country", true},
		{"BDMV", "A", true}, {"BDMV", "0", true}, {"BDMV", "-1", true},
		{"BDMV", "USA", false}, {"BDMV", " gbr ", false}, {"BDMV", "991", false},
		{"DVD", "", false}, {"HDDVD", "", false}, {"", "", false},
	} {
		t.Run(tc.disc+"/"+tc.region, func(t *testing.T) {
			for _, resolution := range []string{"1080p", "2160p"} {
				subject := ulcxValidationSubject()
				subject.DiscType = tc.disc
				subject.Region = tc.region
				subject.Release.Resolution = resolution
				failures, err := Profile().ValidationPolicy.Check(t.Context(), subject, api.NopLogger{})
				if err != nil {
					t.Fatal(err)
				}
				blocked := false
				for _, failure := range failures {
					if failure.Rule != "ulcx_bluray_region" {
						continue
					}
					blocked = true
					for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
						if !trackers.RuleFailureBlocksExecution(failure, mode, true) {
							t.Fatalf("region failure was waivable: %+v", failure)
						}
					}
				}
				if blocked != tc.blocked {
					t.Fatalf("%s region block=%t, want %t", resolution, blocked, tc.blocked)
				}
			}
		})
	}
}

func TestBluRayRegionPreparationAndReadiness(t *testing.T) {
	site := unit3d.SiteProfile{ResolveRegionID: func(value string) string {
		switch value {
		case "GBR":
			return "978"
		case "CUSTOM":
			return "979"
		case "991":
			return "1"
		}
		return ""
	}}
	definition := unit3d.NewWithProfile(profileWithTaxonomy(site))
	dir := t.TempDir()
	report := filepath.Join(dir, "BDInfo.txt")
	torrent := filepath.Join(dir, "example.torrent")
	for _, path := range []string{report, torrent} {
		if err := os.WriteFile(path, []byte("Synthetic disc evidence"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ region, want string }{{"USA", "229"}, {"GBR", "978"}, {"CUSTOM", "979"}, {"991", "991"}, {"", ""}, {"A", ""}} {
		t.Run(tc.region, func(t *testing.T) {
			name := "Example Release 2026-GRP"
			subject := api.UploadSubject{
Assessments: api.ReleaseAssessments{MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent},
 SourcePath: filepath.Join(dir, "Example"),
 ReleaseName: name,
				Type: "DISC",
 DiscType: "BDMV",
 Region: tc.region,
				MediaInfoTextPath: report,
 TorrentPath: torrent,
				Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
				Release:  api.ReleaseInfo{
Title: "Example",
 Year: 2026,
 Resolution: "1080p",
},
			}
			for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
				plan, failure := definition.Prepare(t.Context(), trackers.PreparationInput{
					Intent: trackers.PreparationIntentDryRun,
 Tracker: "ULCX",
 Logger: api.NopLogger{},
 ExecutionMode: mode,
					RequestedUploadName: &name,
 TrackerConfig: config.TrackerConfig{APIKey: "test-key"},
					Assets: &trackers.DescriptionAssets{Description: "Synthetic description", Final: true},
 Meta: subject,
				})
				if tc.want == "" {
					if failure == nil || !strings.Contains(failure.Error(), "ulcx_bluray_region") {
						t.Fatalf("invalid region was not blocked: %v", failure)
					}
				} else {
					if failure != nil {
						t.Fatal(failure)
					}
					if got := plan.DryRun().Payload["region_id"]; got != tc.want {
						t.Fatalf("region_id=%q, want %q", got, tc.want)
					}
					if err := plan.Release(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.want != "" {
				return
			}
			registry := trackers.NewRegistry()
			if err := registry.Register(definition); err != nil {
				t.Fatal(err)
			}
			projector, err := trackers.NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, projections, err := projector.Build(t.Context(), api.ReleaseSnapshot{}, subject, []api.TrackerID{"ULCX"}, nil, nil, api.WorkflowExecutionModeDebug)
			if err != nil {
				t.Fatal(err)
			}
			projection := projections.Projections[0]
			if projection.Readiness != api.ReadinessStatusIneligible || projection.DupeReady || projection.UploadReady {
				t.Fatalf("invalid region crossed readiness: %+v", projection)
			}
		})
	}
}
