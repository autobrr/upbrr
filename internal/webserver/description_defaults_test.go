// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
)

func TestDescriptionDefaultsUsesConfiguredRenderersAndDraftWithoutLiveResources(t *testing.T) {
	t.Parallel()

	s := newAuthTestServer(t, filepath.Join(t.TempDir(), "description.db"))
	s.backend.cfg = config.Config{Trackers: config.TrackersConfig{
		DefaultTrackers: config.CSVList{"RHD"},
		Trackers: map[string]config.TrackerConfig{
			"AITHER": {APIKey: "fake-api-key"},
			"PTP":    {Username: "fake-user"},
		},
	}}
	mux := http.NewServeMux()
	s.registerAppRoutes(mux)
	active, err := s.sessions.Create("admin", false)
	if err != nil {
		t.Fatal(err)
	}
	const catalogPath = "/api/app/ListDescriptionDefaultsTrackers"
	const previewPath = "/api/app/PreviewDescriptionDefaults"
	list := func() []descriptionDefaultsTracker {
		t.Helper()
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, catalogPath, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: active.ID})
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, request)
		var choices []descriptionDefaultsTracker
		if err := json.NewDecoder(out.Body).Decode(&choices); err != nil || out.Code != http.StatusOK {
			t.Fatalf("sample catalog: status=%d decode=%v", out.Code, err)
		}
		return choices
	}
	choices := list()
	if len(choices) != 2 ||
		!slices.ContainsFunc(choices, func(entry descriptionDefaultsTracker) bool { return entry.Name == "AITHER" && len(entry.Items) > 0 }) ||
		!slices.ContainsFunc(choices, func(entry descriptionDefaultsTracker) bool { return entry.Name == "PTP" && len(entry.Items) > 0 }) {
		t.Fatalf("sample trackers must be configured, not merely default: %#v", choices)
	}
	render := func(tracker string, wantStatus int) string {
		t.Helper()
		data, err := json.Marshal(descriptionDefaultsRequest{
			Tracker: tracker,
			Draft: config.DescriptionSettingsConfig{
				SectionOrder:            config.DescriptionSectionOrder{"screenshots", "kept", "template", "mediainfo"},
				AudioPlotsLabel:         "Audio tracks",
				CustomDescriptionHeader: "[b]Draft introduction[/b][img]https://outside.invalid/test.png[/img]",
				Labels:                  map[string]string{"comparison.heading": "Original and encode"},
				ItemOptions: map[string]config.DescriptionItemOptions{
					"mediainfo": {Hidden: true}, // Required evidence must still be emitted.
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		out := serveLegacyRecoveryRequest(t.Context(), t, mux, active, previewPath, string(data))
		if out.Code != wantStatus {
			t.Fatalf("%s preview status = %d, want %d, body %s", tracker, out.Code, wantStatus, out.Body.String())
		}
		if wantStatus != http.StatusOK {
			return ""
		}
		var result string
		if err := json.NewDecoder(out.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}

	unit, ptp := render("AITHER", http.StatusOK), render("PTP", http.StatusOK)
	for name, rendered := range map[string]string{"AITHER": unit, "PTP": ptp} {
		if !strings.Contains(rendered, "Audio tracks") || !strings.Contains(rendered, "data:image/svg+xml;base64,") ||
			!strings.Contains(rendered, "Original and encode") {
			t.Fatalf("%s did not include the draft heading and offline asset", name)
		}
		if img, notes := strings.Index(rendered, "<img"), strings.Index(rendered, "Sample release notes"); img < 0 || notes < img {
			t.Fatalf("%s did not show reordered screenshots before notes", name)
		}
		if strings.Contains(rendered, "href=") || strings.Contains(rendered, "https://outside.invalid") ||
			strings.Contains(rendered, "src=\"https:") {
			t.Fatalf("%s preview retained a live URL", name)
		}
	}
	if !strings.Contains(unit, "Draft introduction") || strings.Contains(ptp, "Draft introduction") ||
		!strings.Contains(ptp, "Matroska") {
		t.Fatal("preview did not use the selected tracker's renderer")
	}
	if got := s.backend.currentConfig().Description.AudioPlotsLabel; got != "" {
		t.Fatalf("preview changed saved settings: %q", got)
	}
	render("RHD", http.StatusNotFound)

	s.backend.cfg.Trackers.Trackers = nil
	if choices := list(); choices == nil || len(choices) > 0 {
		t.Fatalf("empty configured state = %#v", choices)
	}
}

func TestDescriptionSampleWiringCoversRegisteredDescriptionModes(t *testing.T) {
	t.Parallel()
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, tracker := range registry.Names() {
		mode, _ := registry.LookupUploadContentMode(tracker)
		if !mode.UsesDescription() {
			continue
		}
		t.Run(tracker, func(t *testing.T) {
			def, _ := registry.Lookup(tracker)
			renderer, ok := def.(trackers.DescriptionSampleRenderer)
			if !ok || len(renderer.DescriptionLayout()) == 0 {
				t.Fatal("description mode requires a sample renderer and declared items")
			}
			for _, kind := range []string{"movie", "episode", "dvd", "bluray"} {
				sample, _, ok := buildDescriptionSample(kind)
				if !ok {
					t.Fatalf("sample %q not configured", kind)
				}
				defaults := config.DescriptionSettingsConfig{}
				if tracker == "HDS" && kind == "movie" {
					defaults.AudioPlotsLabel = "Custom audio heading"
				}
				raw, err := renderer.RenderDescriptionSample(t.Context(), sample, defaults, config.TrackerConfig{})
				if err != nil || strings.TrimSpace(raw) == "" {
					t.Errorf("sample %q has no description: %v", kind, err)
				}
				if tracker == "BHD" && kind == "bluray" && strings.Contains(raw, "[spoiler=BDINFO]") {
					t.Error("BHD preview must not inline the primary Blu-ray report")
				}
				if tracker == "ASC" && kind == "episode" && (!strings.Contains(raw, "Signals at Sea") || !strings.Contains(raw, "Temporada 1")) {
					t.Error("ASC TV preview must use the canonical category to render episode and season sections")
				}
				if defaults.AudioPlotsLabel != "" && !strings.Contains(raw, "[b]Custom audio heading[/b]") {
					t.Error("HDS must render the analysis heading without a spoiler tag")
				}
				if tracker == "PTP" {
					if kind == "dvd" && (!strings.Contains(raw, "MPEG Video") || strings.Contains(raw, "Matroska")) {
						t.Error("PTP DVD preview must render VOB evidence")
					}
					if kind == "bluray" && !strings.Contains(raw, "00001.MPLS") {
						t.Error("PTP Blu-ray preview must render playlist evidence")
					}
				}
			}
		})
	}
}
