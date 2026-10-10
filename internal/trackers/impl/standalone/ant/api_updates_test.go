// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTRegisteredDownloadAndMissingView(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	torrent := []byte("d4:infod6:lengthi1e4:name7:Example12:piece lengthi16384e6:pieces20:abcdefghijklmnopqrstee")
	fixture := filepath.Join(t.TempDir(), "fixture.torrent")
	if err := trackers.PersistRegisteredTorrent(fixture, torrent); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, response string
		downloadStatus int
		wantArtifact   bool
	}{
		{"modified download", `{"status":"success","message":"Torrent modified","download":"https://anthelion.me/download.php?id=7"}`, 200, true},
		{"download denied", `{"status":"success","message":"Torrent modified","download":"https://anthelion.me/download.php?id=7"}`, 403, false},
		{"modified without download scope", `{"status":"success","message":"Torrent modified"}`, 0, false},
		{"foreign download", `{"status":"success","download":"https://example.invalid/download"}`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body, status := tc.response, http.StatusOK
				if req.Method == http.MethodGet {
					if req.URL.Host != "anthelion.me" || req.Header.Get("X-Api-Key") != "test-key" {
						t.Fatal("unsafe registered download")
					}
					body, status = string(torrent), tc.downloadStatus
				}
				return &http.Response{
					StatusCode: status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			})
			path := filepath.Join(t.TempDir(), "registered.torrent")
			summary, err := submitPreparedUpload(
				t.Context(),
				api.NopLogger{},
				uploadState{fields: map[string]string{"api_key": "test-key"}},
				nil,
				"application/json",
				"",
				path,
			)
			if err != nil || summary.Uploaded != 1 {
				t.Fatalf("successful upload lost: summary=%+v err=%v", summary, err)
			}
			uploaded := summary.UploadedTorrents[0]
			if uploaded.TorrentURL != "" || (uploaded.TorrentPath != "") != tc.wantArtifact {
				t.Fatalf("wrong registration authority: %+v", uploaded)
			}
			if tc.wantArtifact {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, torrent) {
					t.Fatal("registered bytes changed")
				}
			}
		})
	}
}

func TestANTSlotProducersAgree(t *testing.T) {
	meta := api.UploadSubject{
		ReleaseName: "Example.Movie.2026.DVD-GRP",
		Type:        "DISC",
		DiscType:    "BDMV",
		Source:      "Blu-ray",
		Region:      "JPN",
		VideoCodec:  "AVC",
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		Edition:     "IMAX",
	}
	entries := antDupeEntries(map[string]any{"item": []any{map[string]any{
		"fileName":   "Example.Movie.2026.JPN.1080p.BluRay.AVC-GRP",
		"container":  "m2ts",
		"resolution": "1080p",
		"codec":      "AVC",
		"media":      "BluRay",
		"flags":      []any{"IMAX"},
	}}})
	if slot := resolveTargetSlot(meta); slot == "" || slot != entries[0].TrackerSlot {
		t.Fatalf("target slot %q differs from candidate %q", slot, entries[0].TrackerSlot)
	}
	for region, want := range map[string]string{
		"JP":  "JP",
		"JPN": "JP",
		"GER": "DE",
		"USA": "US",
		"EUR": "",
		"A":   "",
		"":    "",
	} {
		if got := discCountry(region); got != want {
			t.Fatalf("region %q: country %q, want %q", region, got, want)
		}
	}
}

func TestANTMediaSourceValues(t *testing.T) {
	for source, want := range map[string]string{
		"Blu-ray":   "BluRay",
		"Blu-ray3D": "BluRay",
		"BDMV":      "BluRay",
		"WEB-DL":    "WEB",
		"WEBRip":    "WEB",
		"DVD":       "DVD",
		"PAL":       "DVD",
		"NTSC":      "DVD",
		"PAL DVD":   "DVD",
		"NTSC DVD":  "DVD",
		"HD DVD":    "HDDVD",
		"LaserDisc": "LaserDisc",
		"HDTV":      "HDTV",
		"UHDTV":     "HDTV",
		"TV":        "TV",
		"VHS":       "VHS",
		"Unknown":   "Unknown",
		"Other":     "Other",
		"":          "Unknown",
	} {
		if got := resolveMediaSource(source); got != want {
			t.Fatalf("source %q: got %q, want %q", source, got, want)
		}
	}
}

func TestANTReviewedUploadNameReachesNativeComparison(t *testing.T) {
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mi := filepath.Join(dir, "MEDIAINFO.txt")
	if err := os.WriteFile(mi, []byte("General\nVideo"), 0o600); err != nil {
		t.Fatal(err)
	}
	const original = "Example.Movie.2026.1080p.BluRay.x264-GRP"
	for _, tc := range []struct{ name, requested, marker string }{
		{"IMAX", "Example.Movie.2026.IMAX.1080p.BluRay.x264-GRP", "IMAX"},
		{"3D", "Example.Movie.2026.3D.1080p.BluRay.x264-GRP", "3D"},
		{"compound source", "Example.Movie.2026-Blu-ray-3D", "3D"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requested := tc.requested
			projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{
				Tracker:             "ANT",
				RequestedUploadName: &requested,
				TrackerConfig:       config.TrackerConfig{APIKey: "test-key"},
				Meta: api.UploadSubject{
					ReleaseName:       original,
					Type:              "ENCODE",
					Source:            "BluRay",
					VideoCodec:        "H264",
					MediaInfoTextPath: mi,
					SourcePath:        filepath.Join(dir, original+".mkv"),
					Release: api.ReleaseInfo{
						Title:      "Example Movie",
						Year:       2026,
						Category:   "MOVIE",
						Resolution: "1080p",
					},
					Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
					ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
						TMDBID: 123,
						Title:  "Example Movie",
						Year:   2026,
						Genres: "Drama",
					}},
				},
			}, "input", "catalog", "config")
			if failure != nil {
				t.Fatal(failure)
			}
			if !projection.DupeReady || projection.UploadReleaseName != requested || projection.DuplicateCriteria.Name != original {
				t.Fatalf("unexpected projected names/readiness: %+v", projection)
			}
			entries := antDupeEntries(map[string]any{"item": []any{map[string]any{
				"files":      []any{map[string]any{"name": "Existing.2026." + tc.marker + ".1080p.BluRay.x264-OTHER.mkv"}},
				"resolution": "1080p",
				"codec":      "H264",
				"media":      "BluRay",
				"container":  "mkv",
				"flags":      []any{tc.marker},
			}}})
			result := dupe.Evaluate(projection.DuplicateTarget, []dupe.TrackerCandidate{dupe.NormalizeCandidate(entries[0], "ANT")}, *duplicatePolicy(), dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			if got := result.Candidates[0].Relation; got != api.DupeRelationManualReview {
				t.Fatalf("got %s, want manual review", got)
			}
			if !slices.Contains(projection.DuplicateTarget.Names, requested) {
				t.Fatal("submitted name missing from comparison evidence")
			}
		})
	}
}

func TestANTUpdatedUploadFields(t *testing.T) {
	dir := t.TempDir()
	mi := filepath.Join(dir, "MEDIAINFO.txt")
	if err := os.WriteFile(filepath.Join(dir, "Example.torrent"), []byte("test-torrent"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mi, []byte("General\nVideo"), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := api.UploadSubject{
		ReleaseName:                 "Example.Movie.2026.DVD-GRP",
		Identity:                    api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
		Source:                      "PAL",
		Type:                        "DISC",
		DiscType:                    "DVD",
		Region:                      "JPN",
		Scene:                       true,
		Audio:                       "DTS",
		TorrentPath:                 filepath.Join(dir, "Example.torrent"),
		MediaInfoTextPath:           mi,
		TrackerQuestionnaireAnswers: map[string]map[string]string{"ANT": {"requestid": "007"}},
	}
	state, err := prepareUploadState(t.Context(), trackers.PreparationInput{
		Tracker: "ANT",
		Projection: &api.TrackerReleaseProjection{
			TrackerID:         "ANT",
			Readiness:         api.ReadinessStatusReady,
			UploadReady:       true,
			UploadReleaseName: "Example.Movie.2026.DVD-GRP",
		},
		Meta:          meta,
		TrackerConfig: config.TrackerConfig{APIKey: "test-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"media":        "DVD",
		"disc_country": "JP",
		"scene":        "1",
		"requestid":    "7",
	} {
		if state.fields[key] != want {
			t.Fatalf("%s = %q, want %q", key, state.fields[key], want)
		}
	}
	for _, omitted := range []string{"audioformat", "censored"} {
		if _, exists := state.fields[omitted]; exists {
			t.Fatalf("unexpected field %s", omitted)
		}
	}
	for _, value := range []string{"-1", "0", "text", "1.5"} {
		if _, err := requestID(map[string]string{"requestid": value}); err == nil {
			t.Fatalf("accepted invalid request ID %q", value)
		}
	}
}

func TestANTEnglishDubUsesProgrammeLanguagesAndExcludesDiscs(t *testing.T) {
	meta := api.UploadSubject{Audio: "Dual-Audio", LanguageFacts: api.LanguageFacts{
		OriginalLanguagesKnown: true,
		OriginalLanguages:      []string{"Japanese"},
		ProgrammeLanguages:     []string{"Japanese", "English"},
	}}
	if !slices.Contains(resolveFlags(meta), "EnglishDub") {
		t.Fatal("missing EnglishDub flag")
	}
	meta.DiscType = "BDMV"
	if slices.Contains(resolveFlags(meta), "EnglishDub") {
		t.Fatal("full disc tagged EnglishDub")
	}
	meta.DiscType = ""
	meta.LanguageFacts.OriginalLanguages = []string{"English"}
	if slices.Contains(resolveFlags(meta), "EnglishDub") {
		t.Fatal("English original tagged EnglishDub")
	}
	meta.LanguageFacts.OriginalLanguages = []string{"Japanese"}
	meta.LanguageFacts.ProgrammeLanguages = []string{"English"}
	if slices.Contains(resolveFlags(meta), "EnglishDub") {
		t.Fatal("dub without original audio tagged EnglishDub")
	}
}

func TestANTSlotsAndEncodeClosingWEB(t *testing.T) {
	for _, tc := range []struct {
		name, target, candidate string
		want                    api.DupeRelation
	}{
		{"AV1 separate from encode", antSlot("ENCODE", "BluRay", "1080p", "AV1", nil, "", true), antSlot("ENCODE", "BluRay", "1080p", "H264", nil, "", true), api.DupeRelationCoexists},
		{"codec competes", antSlot("ENCODE", "PAL DVD", "1080p", "H265", nil, "", true), antSlot("ENCODE", "BluRay", "1080p", "H264", nil, "", true), api.DupeRelationSameSlot},
		{"1080 interlace same tier", antSlot("REMUX", "BluRay", "1080i", "H264", nil, "", true), antSlot("REMUX", "BluRay", "1080p", "H264", nil, "", true), api.DupeRelationSameSlot},
		{"disc countries coexist", antSlot("DISC", "BluRay", "1080p", "H264", nil, "JP", true), antSlot("DISC", "BluRay", "1080p", "H264", nil, "US", true), api.DupeRelationCoexists},
		{"edition sets differ", antSlot("ENCODE", "BluRay", "1080p", "H264", []string{"IMAX", "Extended"}, "", true), antSlot("ENCODE", "BluRay", "1080p", "H264", []string{"IMAX"}, "", true), api.DupeRelationCoexists},
		{"encode closes standard WEB", "WEB/1080/SDR//", "Encode/1080/SDR//IMAX", api.DupeRelationSameSlot},
		{"encode closes matching edition WEB", "WEB/1080/SDR//IMAX", "Encode/1080/SDR//IMAX", api.DupeRelationSameSlot},
		{"encode permits different edition WEB", "WEB/1080/SDR//EXTENDED", "Encode/1080/SDR//IMAX", api.DupeRelationCoexists},
		{"existing WEB permits encode", "Encode/1080/SDR//", "WEB/1080/SDR//", api.DupeRelationCoexists},
		{"AV1 does not close WEB", "WEB/1080/SDR//", "AV1/1080///", api.DupeRelationCoexists},
		{"2160 DV and HDR coexist", "WEB/2160/DV//", "WEB/2160/HDR//", api.DupeRelationCoexists},
		{"unknown does not prove equality", "?/1080/?//?", "?/1080/?//?", api.DupeRelationManualReview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := dupe.Evaluate(
				api.TrackerDuplicateTarget{TrackerSlot: tc.target},
				[]dupe.TrackerCandidate{{TrackerSlot: tc.candidate}},
				*duplicatePolicy(),
				dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID},
			)
			if r.Candidates[0].Relation != tc.want {
				t.Fatalf("got %s, want %s", r.Candidates[0].Relation, tc.want)
			}
		})
	}
}

func TestANTSearchUsesPageTotalsAndTMDBID(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Query().Get("tmdbid") != "123" || req.URL.Query().Get("tmdb") != "" {
			t.Fatal("incorrect TMDB query parameter")
		}
		count, offset := 100, 0
		if requests == 2 {
			count, offset = 1, 100
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(antSearchPageJSON(t, offset, count, count, true)))}, nil
	})}
	r := dupe.NewAdapter(
		New(),
		"ANT",
		antDupeTestConfig(),
		client,
		api.NopLogger{},
	).Search(
		context.Background(),
		api.DuplicateSubject{Identity: api.ExternalIdentity{TMDBID: 123}},
	)
	if !r.SearchEvidence().Complete || requests != 2 || len(r.Entries()) != 101 {
		t.Fatalf("incomplete paged search: %+v", r.SearchEvidence())
	}
}
