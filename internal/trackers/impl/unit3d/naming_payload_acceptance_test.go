// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Site naming is tested at the registry boundary; this checks the shared
// reviewed-name field and the serializer used by upload preparation.
func TestUnit3DReviewedNameReachesSerializedPayload(t *testing.T) {
	const automatic = "Example Movie 2026 REPACK2 1080p 3D WEB-DL AAC 2.0 H.264-GRP"
	for _, tracker := range []string{"BLU", "AITHER"} {
		for _, name := range []string{automatic, "Exact Manual 3D Half SBS Name"} {
			t.Run(tracker+"/"+name, func(t *testing.T) {
				input := trackers.PreparationInput{
					Tracker: tracker,
					Meta: api.UploadSubject{
						ReleaseName: "Canonical name differs from reviewed upload",
						Type:        "WEBDL",
						Source:      "WEB",
						Identity: api.ExternalIdentity{
							Category: api.CanonicalCategoryMovie,
							TMDBID:   12345,
						},
						Release: api.ReleaseInfo{Category: "MOVIE", Resolution: "1080p"},
					},
					Projection: &api.TrackerReleaseProjection{
						TrackerID:         api.TrackerID(tracker),
						Readiness:         api.ReadinessStatusReady,
						UploadReady:       true,
						UploadReleaseName: name,
						DuplicateCriteria: api.TrackerDuplicateCriteria{Name: automatic},
					},
				}
				reviewed, err := input.ReviewedUploadName()
				if err != nil {
					t.Fatal(err)
				}
				fields, err := buildUnit3DData(input, reviewed, "Description", "MediaInfo", "")
				if err != nil {
					t.Fatal(err)
				}
				torrent := filepath.Join(t.TempDir(), "synthetic.torrent")
				if err := os.WriteFile(torrent, []byte("synthetic torrent"), 0o600); err != nil {
					t.Fatal(err)
				}
				body, contentType, err := buildMultipartPayload(fields, torrent, "", api.NopLogger{})
				if err != nil {
					t.Fatal(err)
				}
				_, parameters, err := mime.ParseMediaType(contentType)
				if err != nil {
					t.Fatal(err)
				}
				form, err := multipart.NewReader(strings.NewReader(body), parameters["boundary"]).ReadForm(1 << 20)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := form.RemoveAll(); err != nil {
						t.Error(err)
					}
				})
				if !slices.Equal(form.Value["name"], []string{name}) || fields["name"] != name {
					t.Fatalf("serialized name=%q, fields=%q, want %q", form.Value["name"], fields["name"], name)
				}
				if len(form.File["torrent"]) != 1 || input.Projection.DuplicateCriteria.Name != automatic {
					t.Fatal("serialization lost the torrent or changed the separate search name")
				}
			})
		}
	}
}
