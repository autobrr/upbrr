// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import "testing"

func TestCLIThreeDCorrectionPresence(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		flag := "--3d"
		if !enabled {
			flag = "--3d=false"
		}
		opts, visited, paths, err := parseCLIOptions([]string{flag, "--edition", "Extended", "example.mkv"})
		if err != nil {
			t.Fatal(err)
		}
		request, err := buildCLIRequest(opts, visited, paths, 0)
		if err != nil {
			t.Fatal(err)
		}
		values, err := buildCLIInputCorrectionValues(opts, visited)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []*bool{request.ReleaseNameOverrides.ThreeD, values.ReleaseName.ThreeD} {
			if value == nil || *value != enabled {
				t.Fatalf("3D correction = %v, want %t", value, enabled)
			}
		}
	}
	if _, _, _, err := parseCLIOptions([]string{"--3d", "--reset-input", "release_name.three_d", "example.mkv"}); err == nil {
		t.Fatal("simultaneous Set and Auto accepted")
	}
}
