// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTAudioFlagsPreviewAndSubmission(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	aac := api.MediaTrackFacts{
		Kind:       api.MediaTrackAudio,
		Role:       api.AudioRoleProgramme,
		AudioLabel: "AAC 2.0",
		Default:    true,
	}
	atmos := api.MediaTrackFacts{
		Kind:       api.MediaTrackAudio,
		Role:       api.AudioRoleProgramme,
		AudioLabel: "TrueHD 7.1 Atmos",
	}
	for _, tc := range []struct {
		name      string
		tracks    []api.MediaTrackFacts
		wantAtmos bool
	}{
		{"secondary Atmos", []api.MediaTrackFacts{aac, atmos}, true},
		{"reordered Atmos", []api.MediaTrackFacts{atmos, aac}, true},
		{"default Atmos", []api.MediaTrackFacts{aac, {
			Kind:       api.MediaTrackAudio,
			AudioLabel: "DD+ 5.1 Atmos",
			Default:    true,
		}}, true},
		{"multiple Atmos tracks", []api.MediaTrackFacts{aac, atmos, atmos}, true},
		{"alternate mix", []api.MediaTrackFacts{aac, {
			Kind:       api.MediaTrackAudio,
			Role:       api.AudioRoleAlternateMix,
			AudioLabel: "TrueHD 7.1 Atmos",
		}}, true},
		{"compatibility stream", []api.MediaTrackFacts{aac, {
			Kind:       api.MediaTrackAudio,
			Role:       api.AudioRoleCompatibility,
			Commentary: true,
			AudioLabel: "DD+ 5.1 Atmos",
		}}, true},
		{"commentary marker", []api.MediaTrackFacts{aac, {
			Kind:       api.MediaTrackAudio,
			Commentary: true,
			AudioLabel: "TrueHD 7.1 Atmos",
		}}, false},
		{"commentary role", []api.MediaTrackFacts{aac, {
			Kind:       api.MediaTrackAudio,
			Role:       api.AudioRoleCommentary,
			AudioLabel: "TrueHD 7.1 Atmos",
		}}, false},
		{"subtitle marker", []api.MediaTrackFacts{aac, {Kind: api.MediaTrackSubtitle, AudioLabel: "Atmos"}}, false},
		{"unsupported audio features", []api.MediaTrackFacts{{Kind: api.MediaTrackAudio, AudioLabel: "DTS:X Auro3D"}}, false},
		{"no inspected tracks", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mi, torrent := filepath.Join(dir, "MEDIAINFO.txt"), filepath.Join(dir, "Example.torrent")
			for path, body := range map[string]string{mi: "General\nUnique ID : 123", torrent: "synthetic torrent"} {
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			meta := api.UploadSubject{
				ReleaseName:       "Example.Movie.2026.1080p.BluRay-GRP",
				Identity:          api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
				SourcePath:        filepath.Join(dir, "Example.mkv"),
				TorrentPath:       torrent,
				MediaInfoTextPath: mi,
				Audio:             "AAC 2.0",
				LanguageFacts:     api.LanguageFacts{Tracks: slices.Clone(tc.tracks)},
				HasCommentary:     true,
				Edition:           "Extended",
				Is3D:              "3D",
				HDR:               "HDR DV",
				Distributor:       "Criterion",
				Type:              "REMUX",
			}
			// A naming override cannot establish Atmos when inspected tracks do not.
			if !tc.wantAtmos {
				meta.Audio = "TrueHD 7.1 Atmos"
			}
			want := []string{"Extended"}
			if tc.wantAtmos {
				want = append(want, "Atmos")
			}
			want = append(want, "Commentary", "3D", "HDR10", "DV", "Criterion", "Remux")
			if got := resolveFlags(meta); !slices.Equal(got, want) {
				t.Fatalf("flags = %v, want %v", got, want)
			}
			input := trackers.PreparationInput{
				Tracker:       "ANT",
				Intent:        trackers.PreparationIntentUpload,
				Meta:          meta,
				TrackerConfig: config.TrackerConfig{APIKey: "synthetic-key"},
				Logger:        api.NopLogger{},
				Projection: &api.TrackerReleaseProjection{
					TrackerID:         "ANT",
					Readiness:         api.ReadinessStatusReady,
					UploadReady:       true,
					UploadReleaseName: meta.ReleaseName,
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
				if got := req.MultipartForm.Value["flags[]"]; !slices.Equal(got, want) {
					t.Fatalf("submitted flags = %v, want %v", got, want)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"status":"success"}`)),
				}, nil
			})
			plan, failure := trackers.PrepareAdapter(t.Context(), input, nil, prepareUpload)
			if failure != nil {
				t.Fatal(failure)
			}
			if calls != 0 {
				t.Fatal("preparation submitted a request")
			}
			preview := plan.DryRun()
			if got := preview.Payload["flags[]"]; got != strings.Join(want, ",") {
				t.Fatalf("preview flags = %v, want %v", got, want)
			}
			// Submission must retain the prepared flags even if its input is changed.
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
