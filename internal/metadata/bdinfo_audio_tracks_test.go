// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBDInfoCommentaryClassification(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		language string
		bitrate  string
		want     bool
	}{
		{"French", "257 kbps", true},
		{"French", "258 kbps", false},
		{"French", "259 kbps", false},
		{"French", "257.999 kb/s", true},
		{"French", "0.2579999 Mbps", true},
		{"French", "0.256 Mbps", true},
		{"French", "0.256 Mb/s", true},
		{"French", "0.258 Mb/s", false},
		{"French", "256,000 bps", true},
		{"French", "258000 b/s", false},
		{"French", "256000 b/s", true},
		{"French", "", false},
		{"French", "256", false},
		{"French", "0 kbps", false},
		{"French", "unknown", false},
		{"French", "256 Hz", false},
		{"", "192 kbps", false},
		{"Unknown", "192 kbps", false},
		{"X", "192 kbps", false},
	} {
		t.Run(test.language+"_"+test.bitrate, func(t *testing.T) {
			for _, prefix := range []string{"", "* "} {
				summary, _, _ := discparse.SplitBDInfoReport("QUICK SUMMARY:\n" + prefix + bdAudioSummary(test.language, test.bitrate))
				info := discparse.ParseBDInfoSummary(summary, "", "")
				if got := isBDInfoCommentary(info.Audio[0]); got != test.want {
					t.Fatalf("hidden=%t commentary=%t, want %t", prefix != "", got, test.want)
				}
				_, _, commentary := audioFromMedia(preparationstate.State{}, mediaInfoDoc{}, info)
				if commentary != test.want {
					t.Fatalf("audio commentary=%t, want %t", commentary, test.want)
				}
			}
		})
	}
}

func TestBDInfoAudioTracksPreserveDiscPlaylistAndHiddenIdentity(t *testing.T) {
	t.Parallel()
	summary := strings.Join([]string{
		bdAudioSummary("English", "640 kbps"),
		bdAudioSummary("French", "192 kbps"),
		"* " + bdAudioSummary("French", "192 kbps"),
		"* " + bdAudioSummary("German", "640 kbps"),
		bdAudioSummary("English", "192 kbps"),
	}, "\n")
	meta := bdInfoAudioState(summary)
	meta.Discs[0].Reports = append(meta.Discs[0].Reports, preparationstate.DiscReportResource{
		Playlist: api.PlaylistInfo{
			ID:     "playlist_2",
			DiscID: "disc_1",
			File:   "00002.MPLS",
		},
		Summary: bdAudioSummary("Japanese", "640 kbps"),
	})
	meta.Discs = append(meta.Discs, preparationstate.DiscResource{
		ID:   "disc_2",
		Type: "BDMV",
		Reports: []preparationstate.DiscReportResource{{
			Playlist: api.PlaylistInfo{
				ID:     "playlist_3",
				DiscID: "disc_2",
				File:   "00001.MPLS",
			},
			Summary: bdAudioSummary("Spanish", "640 kbps"),
		}},
	})
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"Audio","Language":"French","StreamOrder":"1"},{"@type":"Text","Language":"en","StreamOrder":"2"}]}}`)
	tracks, primary, audio, subtitles, err := mediaTrackFacts(meta, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 8 || primary != tracks[0].ID || !slices.Equal(audio, []string{"English", "German", "Japanese", "Spanish"}) ||
		!slices.Equal(subtitles, []string{"English"}) {
		t.Fatalf("tracks=%#v primary=%q languages=%v/%v", tracks, primary, audio, subtitles)
	}
	ids := make(map[string]bool)
	for index, track := range tracks[:7] {
		if track.ID == "" || ids[track.ID] || track.DiscID == "" || track.PlaylistID == "" || track.ManifestFingerprint == "" {
			t.Fatalf("missing or duplicate identity at %d: %#v", index, track)
		}
		ids[track.ID] = true
	}
	if tracks[1].Hidden || !tracks[2].Hidden || !tracks[1].Commentary || !tracks[2].Commentary ||
		!tracks[3].Hidden || tracks[3].Commentary || tracks[2].BitrateBitsPerSecond != 192_000 {
		t.Fatalf("hidden/commentary evidence = %#v", tracks[1:4])
	}
	if tracks[5].DiscID != "disc_1" || tracks[5].PlaylistID != "playlist_2" || tracks[6].DiscID != "disc_2" {
		t.Fatalf("disc/playlist identity = %#v", tracks[5:7])
	}
	meta.Discs[0].Reports[0], meta.Discs[0].Reports[1] = meta.Discs[0].Reports[1], meta.Discs[0].Reports[0]
	reordered, _, _, _, err := mediaTrackFacts(meta, doc)
	if err != nil {
		t.Fatal(err)
	}
	if reordered[0].ID != tracks[5].ID || reordered[1].ID != tracks[0].ID {
		t.Fatal("reordering selected reports changed stream identity")
	}
}

func TestDeriveBDInfoCommentaryRespectsManualFlagOnReprepare(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		bitrate  string
		override *bool
		want     bool
		noIDs    bool
	}{
		{"automatic", "192 kbps", nil, true, false},
		{"manual no", "192 kbps", new(false), false, false},
		{"manual yes", "640 kbps", new(true), true, false},
		{"automatic no", "640 kbps", nil, false, false},
		{"missing opaque IDs", "192 kbps", nil, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := bdInfoAudioState(bdAudioSummary("English", "640 kbps") + "\n" + bdAudioSummary("French", test.bitrate))
			meta.MetadataOverrides.Commentary = test.override
			if test.noIDs {
				meta.Discs[0].ID = ""
				meta.Discs[0].Reports[0].Playlist.ID = ""
			}
			service := NewService(&fakeRepo{}, WithConfig(config.Config{}))
			for generation := range 2 {
				var err error
				meta, err = service.deriveMediaFacts(t.Context(), meta)
				if err != nil {
					t.Fatal(err)
				}
				if meta.HasCommentary != test.want {
					t.Fatalf("generation %d commentary=%t, want %t", generation, meta.HasCommentary, test.want)
				}
				if meta.TrackCoverageComplete {
					t.Fatal("BDInfo playlist ordinals must not claim native decoder-stream coverage")
				}
				wantLanguages := []string{"English", "French"}
				if test.bitrate == "192 kbps" {
					wantLanguages = wantLanguages[:1]
				}
				if !slices.Equal(meta.AudioLanguages, wantLanguages) {
					t.Fatalf("generation %d languages=%v, want %v", generation, meta.AudioLanguages, wantLanguages)
				}
			}
		})
	}
}

func TestBDInfoAudioCorrectionsCannotReuseMediaInfoTrackIdentity(t *testing.T) {
	t.Parallel()
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"Audio","Language":"en","StreamOrder":"1"}]}}`)
	meta := bdInfoAudioState(bdAudioSummary("English", "640 kbps") + "\n" + bdAudioSummary("French", "192 kbps"))
	file := meta
	file.DiscType = ""
	fileTracks, _, _, _, err := mediaTrackFacts(file, doc)
	if err != nil {
		t.Fatal(err)
	}
	meta.MediaTracks, _, _, _, err = mediaTrackFacts(meta, doc)
	if err != nil {
		t.Fatal(err)
	}
	correction := api.TrackLanguageCorrection{
		TrackID:             fileTracks[0].ID,
		ManifestFingerprint: fileTracks[0].ManifestFingerprint,
		Languages:           []string{"Japanese"},
	}
	if err := applyTrackLanguageOverrides(&meta, []api.TrackLanguageCorrection{correction}); err == nil {
		t.Fatal("file-native identity unexpectedly corrected a BDInfo playlist stream")
	} else if _, ok := errors.AsType[*api.CorrectionConflictError](err); !ok {
		t.Fatalf("correction error = %v, want conflict", err)
	}
	correction.TrackID = meta.MediaTracks[0].ID
	correction.ManifestFingerprint = meta.MediaTracks[0].ManifestFingerprint
	if err := applyTrackLanguageOverrides(&meta, []api.TrackLanguageCorrection{correction}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(aggregateTrackLanguages(meta.MediaTracks, api.MediaTrackAudio), []string{"Japanese"}) ||
		!slices.Equal(meta.MediaTracks[1].Languages, []string{"French"}) {
		t.Fatalf("corrected tracks = %#v", meta.MediaTracks)
	}
}

func TestDeriveBDInfoCommentaryDoesNotReintroduceMediaInfoLanguages(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		summary  string
		language []string
	}{
		{"commentary only", bdAudioSummary("French", "192 kbps"), nil},
		{"same language main", bdAudioSummary("French", "192 kbps") + "\n" + bdAudioSummary("French", "640 kbps"), []string{"French"}},
		{"missing bitrate", bdAudioSummary("French", ""), []string{"French"}},
		{"unknown bitrate", bdAudioSummary("French", "unknown"), []string{"French"}},
		{"missing BDInfo language", bdAudioSummary("", "192 kbps"), []string{"French"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := bdInfoAudioState(test.summary)
			meta.MediaInfoJSONPath = filepath.Join(t.TempDir(), "mediainfo.json")
			if err := os.WriteFile(meta.MediaInfoJSONPath, []byte(`{"media":{"track":[{"@type":"Audio","Language":"French","Format":"AC-3"}]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			service := NewService(&fakeRepo{}, WithConfig(config.Config{}))
			actual, err := service.deriveMediaFacts(t.Context(), meta)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(actual.AudioLanguages, test.language) {
				t.Fatalf("audio=%v, want %v", actual.AudioLanguages, test.language)
			}
		})
	}
}

func TestDeriveBDInfoUnknownMainLanguageRequiresBoundCorrection(t *testing.T) {
	t.Parallel()
	meta := bdInfoAudioState(bdAudioSummary("", "640 kbps") + "\n" + bdAudioSummary("French", "192 kbps"))
	meta.MediaInfoJSONPath = filepath.Join(t.TempDir(), "mediainfo.json")
	if err := os.WriteFile(meta.MediaInfoJSONPath, []byte(`{"media":{"track":[{"@type":"Audio","Language":"English","Format":"AC-3"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewService(&fakeRepo{}, WithConfig(config.Config{}))
	inspected, err := service.deriveMediaFacts(t.Context(), meta)
	if err != nil {
		t.Fatal(err)
	}
	// A primary-file stream cannot fill an unidentified playlist stream by position.
	if len(inspected.AudioLanguages) != 0 || !inspected.HasCommentary || len(inspected.MediaTracks[0].Languages) != 0 {
		t.Fatalf("mixed unbound evidence = %#v", inspected.MediaTracks)
	}
	for _, mode := range []string{"aggregate", "track"} {
		t.Run(mode, func(t *testing.T) {
			corrected := meta
			if mode == "aggregate" {
				corrected.MetadataOverrides.AudioLanguages = new([]string{"English"})
			} else {
				corrected.MetadataOverrides.TrackLanguages = []api.TrackLanguageCorrection{{
					TrackID:             inspected.MediaTracks[0].ID,
					ManifestFingerprint: inspected.MediaTracks[0].ManifestFingerprint,
					Languages:           []string{"English"},
				}}
			}
			actual, err := service.deriveMediaFacts(t.Context(), corrected)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(actual.AudioLanguages, []string{"English"}) || !actual.HasCommentary {
				t.Fatalf("corrected languages=%v commentary=%t", actual.AudioLanguages, actual.HasCommentary)
			}
		})
	}
}

func bdInfoAudioState(summary string) preparationstate.State {
	return preparationstate.State{
		SourcePath: "Example.2026.1080p.BluRay-GRP",
		DiscType:   "BDMV",
		Release: api.ReleaseInfo{
			Category: "MOVIE",
			Title:    "Example",
			Year:     2026,
		},
		Discs: []preparationstate.DiscResource{{
			ID:   "disc_1",
			Type: "BDMV",
			Reports: []preparationstate.DiscReportResource{{
				Playlist: api.PlaylistInfo{
					ID:     "playlist_1",
					DiscID: "disc_1",
					File:   "00001.MPLS",
				}, Summary: summary,
			}},
		}},
	}
}

func bdAudioSummary(language, bitrate string) string {
	return fmt.Sprintf("Audio: %s / Dolby Digital Audio / 2.0 / 48 kHz / %s / 16-bit", language, bitrate)
}
