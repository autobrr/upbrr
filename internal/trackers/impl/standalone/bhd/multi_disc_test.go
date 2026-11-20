// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBHDBDInfoDescriptionAndMultipart(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"single report", "additional playlist", "multiple discs", "later primary", "identical reports", "final edit"} {
		t.Run(mode, func(t *testing.T) {
			meta := bhdMultiDiscSubject()
			wantMedia := "BDINFO ONE"
			wantExtra := "Disc 2 — 00001.MPLS\nBDINFO TWO"
			switch mode {
			case "single report", "final edit":
				meta.Disc.Items = meta.Disc.Items[:1]
				meta.Discs = meta.Discs[:1]
				wantExtra = ""
			case "additional playlist":
				meta.Disc.Items[0].Reports = append(meta.Disc.Items[0].Reports, api.DiscReportFacts{
					Playlist: api.PlaylistInfo{
						ID:     "disc-one:00002.MPLS",
						DiscID: "disc-one",
						File:   "00002.MPLS",
					},
					Summary: "BDINFO TWO",
				})
				meta.Disc.PrimaryReportID = "disc-one:00002.MPLS"
				meta.Discs[0].Reports = append(meta.Discs[0].Reports, api.DiscReportResource{
					Playlist:    api.PlaylistInfo{ID: "disc-one:00002.MPLS"},
					Summary:     "BDINFO TWO",
					SummaryPath: filepath.Join(t.TempDir(), "BDINFO.00002.txt"),
				})
				meta.Disc.Items = meta.Disc.Items[:1]
				meta.Discs = meta.Discs[:1]
				wantMedia = "BDINFO TWO"
				wantExtra = "Disc 1 — 00001.MPLS\nBDINFO ONE"
			case "later primary":
				meta.Disc.PrimaryDiscID = "disc-two"
				meta.Disc.PrimaryReportID = "disc-two:00001.MPLS"
				wantMedia = "BDINFO TWO"
				wantExtra = "Disc 1 — 00001.MPLS\nBDINFO ONE"
			case "identical reports":
				meta.Disc.Items[1].Reports[0].Summary = "BDINFO ONE"
				wantExtra = "Disc 2 — 00001.MPLS\nBDINFO ONE"
			}
			tmp := t.TempDir()
			meta.TorrentPath = filepath.Join(tmp, "Example.torrent")
			if err := os.WriteFile(meta.TorrentPath, []byte("synthetic torrent"), 0o600); err != nil {
				t.Fatal(err)
			}
			meta.Source = "BluRay"
			meta.ReleaseName = "Example Movie 2026 1080p BluRay-GRP"
			meta.Identity = api.ExternalIdentity{
				TMDBID:   123,
				IMDBID:   456,
				Category: "MOVIE",
			}
			const userText = "[spoiler=Notes][code]User-supplied report[/code][/spoiler]"
			assets := trackers.DescriptionAssets{
				Description: userText,
				Final:       mode == "final edit",
				Screenshots: []api.ScreenshotImage{{RawURL: "https://images.example/screen.png"}},
				MenuImages:  []api.ScreenshotImage{{RawURL: "https://images.example/menu.png"}},
			}
			input := trackers.PreparationInput{
				Tracker: "BHD",
				Meta:    meta,
				Assets:  &assets,
				Projection: &api.TrackerReleaseProjection{
					TrackerID:         "BHD",
					Readiness:         api.ReadinessStatusReady,
					UploadReady:       true,
					UploadReleaseName: meta.ReleaseName,
				},
				TrackerConfig: config.TrackerConfig{APIKey: "synthetic-token"},
			}
			preview, err := prepareDescription(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			state, err := prepareUploadState(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if state.mediaDump != wantMedia || preview.Description != state.description {
				t.Fatalf("primary or preview mismatch: media=%q preview=%q description=%q", state.mediaDump, preview.Description, state.description)
			}
			if mode == "multiple discs" {
				if path := resolveMediaPath(meta, ""); path != `C:\private\disc-one\BDINFO.00001.txt` {
					t.Fatalf("dry-run media path = %q", path)
				}
			}
			description := state.description
			if mode == "final edit" {
				if description != userText {
					t.Fatalf("final edit changed: %q", description)
				}
			} else {
				for _, token := range []string{userText, "[img width=350]https://images.example/screen.png[/img]", "[img width=350]https://images.example/menu.png[/img]", "Disc Menus"} {
					if strings.Count(description, token) != 1 {
						t.Fatalf("lost or repeated %q: %q", token, description)
					}
				}
			}
			if wantExtra == "" {
				if strings.Contains(description, "BDINFO") {
					t.Fatalf("primary repeated in description: %q", description)
				}
			} else if strings.Count(description, wantExtra) != 1 || strings.Count(description, "BDINFO ") != 1 {
				t.Fatalf("additional report missing, duplicated or unlabelled: %q", description)
			}
			body, contentType, err := buildMultipartPayload(state.fields, state.mediaDump, state.torrentPath)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://tracker.example/upload", bytes.NewReader(body))
			request.Header.Set("Content-Type", contentType)
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = request.MultipartForm.RemoveAll() })
			file, _, err := request.FormFile("mediainfo")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			media, err := io.ReadAll(file)
			if err != nil || string(media) != wantMedia || request.FormValue("description") != description {
				t.Fatalf("multipart media=%q description=%q err=%v", media, request.FormValue("description"), err)
			}
		})
	}
}

func TestBHDValidationBlocksMissingSecondDiscReport(t *testing.T) {
	t.Parallel()

	meta := bhdMultiDiscSubject()
	meta.Disc.Items[1].Reports[0].Summary = ""
	partial := api.NewTrackerValidationSubject(meta, "BHD")
	if !hasBHDValidationRule(bhdRequiredAssetFailures(partial), "bhd_required_assets_bdinfo") {
		t.Fatalf("missing second-disc BDInfo was not blocked: %+v", bhdRequiredAssetFailures(partial))
	}

	meta.Disc.Items[1].Reports[0].Summary = "BDINFO TWO"
	complete := api.NewTrackerValidationSubject(meta, "BHD")
	if hasBHDValidationRule(bhdRequiredAssetFailures(complete), "bhd_required_assets_bdinfo") {
		t.Fatalf("complete BDInfo remained blocked: %+v", bhdRequiredAssetFailures(complete))
	}
	meta.Disc.Items[0].Reports[0].Summary = ""
	if _, err := resolveMediaDump(meta, ""); err == nil {
		t.Fatal("missing primary BDInfo was accepted")
	}
}

func TestBHDDiscSectionPreservesDVDAndOmitsLegacyPrimary(t *testing.T) {
	t.Parallel()
	dvd := api.UploadSubject{DiscType: "DVD", DVDVOBMediaInfoText: "DVD VOB MediaInfo"}
	if got := buildDiscSection(dvd); got != "[spoiler=VOB MediaInfo][code]DVD VOB MediaInfo[/code][/spoiler]" {
		t.Fatalf("DVD section changed: %q", got)
	}
	legacy := api.UploadSubject{DiscType: "BDMV", Disc: api.DiscFacts{Summary: "Legacy primary BDInfo"}}
	if got := buildDiscSection(legacy); got != "" {
		t.Fatalf("legacy primary repeated: %q", got)
	}
}

func hasBHDValidationRule(failures []api.RuleFailure, rule string) bool {
	for _, failure := range failures {
		if failure.Rule == rule {
			return true
		}
	}
	return false
}

func bhdMultiDiscSubject() api.UploadSubject {
	return api.UploadSubject{
		DiscType: "BDMV",
		Disc: api.DiscFacts{
			PrimaryDiscID:   "disc-one",
			PrimaryReportID: "disc-one:00001.MPLS",
			Items: []api.DiscItemFacts{
				{
					ID:   "disc-one",
					Name: "Disc 1",
					Type: "BDMV",
					Reports: []api.DiscReportFacts{{
						Playlist: api.PlaylistInfo{
							ID:     "disc-one:00001.MPLS",
							DiscID: "disc-one",
							File:   "00001.MPLS",
						},
						Summary: "BDINFO ONE",
					}},
				},
				{
					ID:   "disc-two",
					Name: "Disc 2",
					Type: "BDMV",
					Reports: []api.DiscReportFacts{{
						Playlist: api.PlaylistInfo{
							ID:     "disc-two:00001.MPLS",
							DiscID: "disc-two",
							File:   "00001.MPLS",
						},
						Summary: "BDINFO TWO",
					}},
				},
			},
		},
		Discs: []api.DiscEvidenceResource{
			{
				ID:   "disc-one",
				Name: "Disc 1",
				Type: "BDMV",
				Reports: []api.DiscReportResource{{
					Playlist:    api.PlaylistInfo{ID: "disc-one:00001.MPLS"},
					Summary:     "BDINFO ONE",
					SummaryPath: `C:\private\disc-one\BDINFO.00001.txt`,
				}},
			},
			{
				ID:   "disc-two",
				Name: "Disc 2",
				Type: "BDMV",
				Reports: []api.DiscReportResource{{
					Playlist:    api.PlaylistInfo{ID: "disc-two:00001.MPLS"},
					Summary:     "BDINFO TWO",
					SummaryPath: `C:\private\disc-two\BDINFO.00001.txt`,
				}},
			},
		},
		ExactMedia: &api.ExactMediaAssets{},
	}
}
