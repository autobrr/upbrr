// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ant"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTBDInfoAudioFlagsPreviewAndSubmission(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	const ordinary = "Audio: English / Dolby Digital Audio / 2.0 / 48 kHz / 640 kbps / 16-bit"
	const atmos = "Audio: English / Dolby TrueHD Audio / Dolby Atmos / 7.1 / 48 kHz / 4000 kbps / 24-bit"
	const commentary = "Audio: English / Dolby Digital Plus Audio / Dolby Atmos / 2.0 / 48 kHz / 192 kbps / 16-bit"
	for _, tc := range []struct {
		name  string
		lines []string
		want  []string
	}{
		{"secondary Atmos", []string{ordinary, atmos}, []string{"Atmos"}},
		{"primary Atmos", []string{atmos, ordinary}, []string{"Atmos"}},
		{"multiple Atmos", []string{ordinary, atmos, atmos}, []string{"Atmos"}},
		{"commentary only", []string{ordinary, commentary}, []string{"Commentary"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := "Playlist: 00001.MPLS\nLength: 01:30:00.000\n" + strings.Join(tc.lines, "\n")
			state := bdInfoAudioState(summary)
			tracks, _, _, _, err := mediaTrackFacts(state, mediaInfoDoc{})
			if err != nil {
				t.Fatal(err)
			}
			// Use the real BDInfo producer and canonical projection, rather than
			// supplying feature-bearing track labels in the fixture.
			facts := mediafacts.ResolveLanguages(api.MediaFacts{Tracks: tracks})
			audio, _, hasCommentary := audioFromMedia(state, mediaInfoDoc{}, discparse.ParseBDInfoSummary(summary, "", ""))
			torrent := filepath.Join(t.TempDir(), "Example.torrent")
			if err := os.WriteFile(torrent, []byte("synthetic torrent"), 0o600); err != nil {
				t.Fatal(err)
			}
			input := trackers.PreparationInput{
				Tracker:       "ANT",
				Intent:        trackers.PreparationIntentUpload,
				TrackerConfig: config.TrackerConfig{APIKey: "synthetic-key"},
				Logger:        api.NopLogger{},
				Meta: api.UploadSubject{
					ReleaseName:   "Example.Movie.2026.1080p.BluRay-GRP",
					Identity:      api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
					Release:       state.Release,
					SourcePath:    state.SourcePath,
					TorrentPath:   torrent,
					DiscType:      "BDMV",
					Type:          "DISC",
					Disc:          api.DiscFacts{Summary: summary},
					LanguageFacts: facts,
					Audio:         audio,
					HasCommentary: hasCommentary,
				},
			}
			calls := 0
			http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if err := req.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := req.MultipartForm.RemoveAll(); err != nil {
						t.Error(err)
					}
				}()
				if got := req.MultipartForm.Value["flags[]"]; !slices.Equal(got, tc.want) {
					t.Fatalf("submitted flags = %v, want %v", got, tc.want)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"status":"success"}`)),
				}, nil
			})
			plan, failure := ant.New().Prepare(t.Context(), input)
			if failure != nil {
				t.Fatal(failure)
			}
			if got := plan.DryRun().Payload["flags[]"]; got != strings.Join(tc.want, ",") {
				t.Fatalf("preview flags = %q, want %v", got, tc.want)
			}
			if calls != 0 {
				t.Fatal("preparation submitted a request")
			}
			clear(input.Meta.LanguageFacts.Tracks)
			if _, err := plan.Submit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("submission calls = %d, want 1", calls)
			}
		})
	}
}
