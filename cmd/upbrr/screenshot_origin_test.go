// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import "testing"

func TestCLICompositePreservesScreenshotCountOrigin(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		args      []string
		count     int
		isDefault bool
	}{
		{
			name:      "LST default",
			args:      []string{"--trackers=LST"},
			count:     7,
			isDefault: true,
		},
		{
			name:      "mixed default",
			args:      []string{"--trackers=LST,OTHER"},
			count:     7,
			isDefault: true,
		},
		{
			name:  "explicit same count",
			args:  []string{"--screens=7"},
			count: 7,
		},
		{
			name:  "single dash",
			args:  []string{"-screens=5"},
			count: 5,
		},
		{
			name:  "short alias",
			args:  []string{"-s=5"},
			count: 5,
		},
		{name: "explicit zero", args: []string{"--screens=0"}},
		{
			name:  "manual frames",
			args:  []string{"--manual_frames=100,200"},
			count: 7,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append(append([]string(nil), test.args...), "Example.Release.mkv")
			opts, visited, paths, err := parseCLIOptions(args)
			if err != nil {
				t.Fatal(err)
			}
			request, err := buildCLIRequest(opts, visited, paths, 7)
			if err != nil {
				t.Fatal(err)
			}
			mapped, err := mapCLICompositeUploadRequest(request, false, "count-origin", !visited["screens"])
			if err != nil {
				t.Fatal(err)
			}
			media := mapped.Media.Screenshots
			if request.Options.Screens != test.count || media.Count == nil || *media.Count != test.count || media.CountIsDefault != test.isDefault {
				t.Fatalf("mapped count = %#v, options count = %d", media, request.Options.Screens)
			}
		})
	}
}
