// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"github.com/autobrr/upbrr/pkg/api"
	"slices"
	"testing"
)

func TestCLIIndependentEditionControls(t *testing.T) {
	for _, test := range []struct {
		args         []string
		presentation string
		features     []api.ReleaseFeature
	}{
		{[]string{"--presentation", "Open Matte", "--release-features", "hybrid,4k_restoration", "--edition", "Custom cut"}, "Open Matte", []api.ReleaseFeature{api.ReleaseFeatureHybrid, api.ReleaseFeature4KRestoration}},
		{[]string{"--presentation=", "--release-features=", "--edition", "Custom cut"}, "", []api.ReleaseFeature{}},
	} {
		opts, visited, paths, err := parseCLIOptions(append(test.args, "example.mkv"))
		if err != nil {
			t.Fatal(err)
		}
		req, err := buildCLIRequest(opts, visited, paths, 0)
		if err != nil {
			t.Fatal(err)
		}
		got := req.ReleaseNameOverrides
		if got.Presentation == nil || *got.Presentation != test.presentation || got.ReleaseFeatures == nil || !slices.Equal(*got.ReleaseFeatures, test.features) || got.Edition == nil || *got.Edition != "Custom cut" {
			t.Fatalf("request=%+v", got)
		}
		values, err := buildCLIInputCorrectionValues(opts, visited)
		if err != nil {
			t.Fatal(err)
		}
		if values.ReleaseName.ReleaseFeatures == nil || !slices.Equal(*values.ReleaseName.ReleaseFeatures, test.features) {
			t.Fatalf("correction=%+v", values)
		}
	}
	for _, args := range [][]string{
		{"--release-features", "unsupported", "example.mkv"},
		{"--presentation", "Unknown IMAX", "example.mkv"},
		{"--release-features=hybrid", "--reset-input", "release_name.release_features", "example.mkv"},
	} {
		if _, _, _, err := parseCLIOptions(args); err == nil {
			t.Fatalf("invalid controls accepted: %v", args)
		}
	}
	opts, visited, paths, err := parseCLIOptions([]string{"--repack", "Extended", "example.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 0)
	if err != nil {
		t.Fatal(err)
	}
	if req.ReleaseNameOverrides.Repack != nil || req.ReleaseNameOverrides.Edition == nil || *req.ReleaseNameOverrides.Edition != "Extended" {
		t.Fatal("legacy repack alias changed")
	}
}
