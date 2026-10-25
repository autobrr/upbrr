// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"bytes"
	"encoding/json"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDBReviewedNameReachesSerializedPayload(t *testing.T) {
	for _, omitted := range []bool{false, true} {
		for _, requested := range []*string{nil, new("Exact Manual 3D Half SBS Name")} {
			t.Run("omitted="+strconv.FormatBool(omitted)+"/requested="+strconv.FormatBool(requested != nil), func(t *testing.T) {
				subject := hdbGeneratedVersionSubject()
				subject.Repack, subject.Is3D = "REPACK2", "3D"
				subject.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeature3DHalfSBS}
				subject.GeneratedName = &api.ReleaseNameDocument{
					Version: api.ReleaseNameDocumentVersionV1,
					Components: []api.ReleaseNameComponent{{
						Role:           api.NameRoleThreeD,
						Value:          "3D",
						AvailableValue: "3D",
						Present:        !omitted,
						Manual:         omitted,
					}},
				}
				before, err := json.Marshal(subject)
				if err != nil {
					t.Fatal(err)
				}
				prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
					Tracker:             "HDB",
					Meta:                subject,
					RequestedUploadName: requested,
				}, Profile().ReleaseNamePolicy)
				if failure != nil {
					t.Fatal(failure)
				}
				search := "Example Original 2026 REPACK2 1080p 3D WEB-DL DDP 5.1 H.264-GRP"
				if omitted {
					search = strings.Replace(search, " 3D", "", 1)
				}
				want := search
				if requested != nil {
					want = *requested
				}
				name, err := prepared.ReviewedUploadName()
				if err != nil || name != want {
					t.Fatalf("reviewed name=%q, want %q: %v", name, want, err)
				}
				fields := buildUploadFields(prepared.Meta, false, 1, 1, 1, "Description", name)
				torrent := filepath.Join(t.TempDir(), "synthetic.torrent")
				if err := os.WriteFile(torrent, []byte("synthetic torrent"), 0o600); err != nil {
					t.Fatal(err)
				}
				body, contentType, err := buildMultipartPayload(fields, torrent)
				if err != nil {
					t.Fatal(err)
				}
				_, parameters, err := mime.ParseMediaType(contentType)
				if err != nil {
					t.Fatal(err)
				}
				form, err := multipart.NewReader(bytes.NewReader(body), parameters["boundary"]).ReadForm(1 << 20)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := form.RemoveAll(); err != nil {
						t.Error(err)
					}
				})
				if fields["name"] != want || !slices.Equal(form.Value["name"], []string{want}) || len(form.File["file"]) != 1 {
					t.Fatalf("fields=%q serialized=%q files=%d, want %q", fields["name"], form.Value["name"], len(form.File["file"]), want)
				}
				after, err := json.Marshal(subject)
				if err != nil {
					t.Fatal(err)
				}
				if prepared.Projection.DuplicateCriteria.Name != search || !bytes.Equal(before, after) {
					t.Fatal("payload preparation changed automatic search naming or canonical facts")
				}
			})
		}
	}
}
