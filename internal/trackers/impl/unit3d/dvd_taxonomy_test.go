// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// DVDTestPayload exposes the production payload builder to external tests using
// real site profiles without introducing an import cycle or runtime test seam.
func DVDTestPayload(input trackers.PreparationInput, name string, site SiteProfile) (map[string]string, error) {
	return buildUnit3DData(input, name, "", "", "", site)
}

func TestDVDCanonicalTypeIDs(t *testing.T) {
	for _, test := range []struct {
		releaseType string
		want        string
	}{
		{releaseType: "ENCODE", want: "3"},
		{releaseType: "DVDRIP", want: "3"},
		{releaseType: "REMUX", want: "2"},
		{releaseType: "DISC", want: "1"},
	} {
		for _, source := range []string{"DVD", "PAL DVD", "NTSC DVD"} {
			t.Run(test.releaseType+"/"+source, func(t *testing.T) {
				meta := api.UploadSubject{
					Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
					Type:     test.releaseType,
					Source:   source,
				}
				data, err := buildUnit3DData(trackers.PreparationInput{Tracker: "EXAMPLE", Meta: meta}, "Reviewed DVD Name", "", "", "")
				if err != nil {
					t.Fatal(err)
				}
				if data["type_id"] != test.want {
					t.Fatalf("type_id = %q, want %q", data["type_id"], test.want)
				}
			})
		}
	}
}
