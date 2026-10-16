// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import "testing"

func TestBoundaryDiagnosticRejectsExportedFilmGrain(t *testing.T) {
	for _, sideData := range []string{
		"Film grain parameters: type h274; model_id: 0",
		"Film grain parameters: type av1;",
		"Film grain parameters: type none;",
		"Film grain parameters: malformed",
		"Film grain parameters",
	} {
		t.Run(sideData, func(t *testing.T) {
			var diagnostic boundaryDiagnostic
			_, err := diagnostic.Write([]byte("[showinfo@boundary @ 000001] side data - " + sideData + "\n"))
			if err == nil {
				t.Fatal("exported film grain admitted without synthesis")
			}
		})
	}
	for _, sideData := range []string{"Mastering display metadata", "Content light level metadata", "Dolby Vision RPU Data", "HDR Dynamic Metadata SMPTE2094-40 (HDR10+)"} {
		var diagnostic boundaryDiagnostic
		if _, err := diagnostic.Write([]byte("[showinfo@boundary @ 000001] side data - " + sideData + ": data\n")); err != nil {
			t.Fatalf("ordinary HDR side data rejected: %v", err)
		}
	}
}
