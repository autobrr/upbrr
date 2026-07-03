// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDefinitionName(t *testing.T) {
	t.Parallel()

	def := New()
	if def.Name() != "BTN" {
		t.Fatalf("expected BTN, got %q", def.Name())
	}
}

func TestApplyBTNNameMapping(t *testing.T) {
	t.Parallel()

	name := "Example.Show.S01E01.1080p.WEB-DL.x265-GRP"
	mapped := applyBTNNameMapping(name, "H.265", "WEB-DL")
	if mapped == "" {
		t.Fatalf("expected mapped name")
	}
	if mapped != "Example.Show.S01E01.1080p.WEB-DL.H.265-GRP" {
		t.Fatalf("unexpected mapped name: %s", mapped)
	}
}

func TestCleanAndNormalizeBTNName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "spaces become dots",
			input:    "Example Show S01E01 1080p Web-DL DD+ 5.1 x265-GRP",
			expected: "Example.Show.S01E01.1080p.Web-DL.DDP5.1.x265-GRP",
		},
		{
			name:     "DDP Atmos compacts before generic DDP channel",
			input:    "Some.Movie.2023.DDP.5.1.Atmos.x264",
			expected: "Some.Movie.2023.DDPA5.1.x264",
		},
		{
			name:     "duplicate dots collapse after DD channel",
			input:    "Another.Show..S02E03.DD.2.0.x264",
			expected: "Another.Show.S02E03.DD2.0.x264",
		},
		{
			name:     "AC3 and DTS channel joins",
			input:    "Test.AC3.5.1.and.DTS.5.1.Show",
			expected: "Test.AC35.1.and.DTS5.1.Show",
		},
		{
			name:     "TrueHD Atmos compacts before generic TrueHD channel",
			input:    "Movie.TrueHD.7.1.Atmos.x264",
			expected: "Movie.TrueHDA7.1.x264",
		},
		{
			name:     "DDP channel joins",
			input:    "Movie.DDP.5.1.x264",
			expected: "Movie.DDP5.1.x264",
		},
		{
			name:     "AAC channel joins",
			input:    "Movie.AAC.2.0.x264",
			expected: "Movie.AAC2.0.x264",
		},
		{
			name:     "FLAC channel joins",
			input:    "Movie.FLAC.2.0.x264",
			expected: "Movie.FLAC2.0.x264",
		},
		{
			name:     "TrueHD channel joins case-insensitively",
			input:    "Movie.truehd.7.1.x264",
			expected: "Movie.TrueHD7.1.x264",
		},
		{
			name:     "PCM channel joins case-insensitively",
			input:    "Movie.pcm.2.0.x264",
			expected: "Movie.PCM2.0.x264",
		},
		{
			name:     "LPCM channel joins case-insensitively",
			input:    "Movie.lpcm.2.0.x264",
			expected: "Movie.LPCM2.0.x264",
		},
		{
			name:     "non-alphanumeric chars become dots",
			input:    "Movie:Title[Cut].DDP.5.1-GRP",
			expected: "Movie.Title.Cut.DDP5.1-GRP",
		},
		{
			name:     "diacritics are removed",
			input:    "Éxample Shōw S01E01",
			expected: "Example.Show.S01E01",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := cleanAndNormalizeBTNName(tc.input)
			if result != tc.expected {
				t.Errorf("cleanAndNormalizeBTNName(%q) = %q; expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestResolveUploadNameGroupTag(t *testing.T) {
	tests := []struct {
		name     string
		meta     api.PreparedMetadata
		expected string
	}{
		{
			name: "Valid group tag in meta.Tag",
			meta: api.PreparedMetadata{
				ReleaseName: "Example.Show.S01E01.1080p.Web-DL.x265-GRP",
				Tag:         "GRP",
			},
			expected: "Example.Show.S01E01.1080p.Web-DL.x265-GRP",
		},
		{
			name: "Valid group tag appended to ReleaseNameNoTag",
			meta: api.PreparedMetadata{
				ReleaseNameNoTag: "Example.Show.S01E01.1080p.Web-DL",
				Tag:              "GRP",
			},
			expected: "Example.Show.S01E01.1080p.Web-DL-GRP",
		},
		{
			name: "Missing group tag",
			meta: api.PreparedMetadata{
				ReleaseName: "Example.Show.S01E01.1080p.Web-DL.x265",
				Tag:         "",
			},
			expected: "Example.Show.S01E01.1080p.Web-DL.x265-NOGRP",
		},
		{
			name: "Missing parsed tag preserves existing release suffix",
			meta: api.PreparedMetadata{
				ReleaseName: "Example.Show.S01E01.1080p.Web-DL.x265-GRP",
				Tag:         "",
			},
			expected: "Example.Show.S01E01.1080p.Web-DL.x265-GRP",
		},
		{
			name: "Unknown group tag in meta.Tag",
			meta: api.PreparedMetadata{
				ReleaseName: "Example.Show.S01E01.1080p.Web-DL.x265",
				Tag:         "nogrp",
			},
			expected: "Example.Show.S01E01.1080p.Web-DL.x265-NOGRP",
		},
		{
			name: "Existing unknown group tag in ReleaseName",
			meta: api.PreparedMetadata{
				ReleaseName: "Example.Show.S01E01.1080p.Web-DL.x265-unknown",
				Tag:         "unknown",
			},
			expected: "Example.Show.S01E01.1080p.Web-DL.x265-NOGRP",
		},
		{
			name: "Generated episode title absent from filename stripped",
			meta: api.PreparedMetadata{
				Filename:     "Example.Show.S01E01.1080p.WEB-DL.x265-GRP.mkv",
				ReleaseName:  "Example.Show.S01E01.Episode.One.1080p.WEB-DL.x265-GRP",
				EpisodeTitle: "Episode One",
				Tag:          "GRP",
			},
			expected: "Example.Show.S01E01.1080p.WEB-DL.x265-GRP",
		},
		{
			name: "Filename episode title preserved",
			meta: api.PreparedMetadata{
				Filename:     "Example.Show.S01E01.Episode.One.1080p.WEB-DL.x265-GRP.mkv",
				ReleaseName:  "Example.Show.S01E01.Episode.One.1080p.WEB-DL.x265-GRP",
				EpisodeTitle: "Episode One",
				Tag:          "GRP",
			},
			expected: "Example.Show.S01E01.Episode.One.1080p.WEB-DL.x265-GRP",
		},
		{
			name: "Episode title substring preserved",
			meta: api.PreparedMetadata{
				Filename:     "Example.Show.S01E01.1080p.WEB-DL.x265-GRP.mkv",
				ReleaseName:  "Example.Show.S01E01.Someone.1080p.WEB-DL.x265-GRP",
				EpisodeTitle: "One",
				Tag:          "GRP",
			},
			expected: "Example.Show.S01E01.Someone.1080p.WEB-DL.x265-GRP",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := resolveUploadName(tc.meta)
			if result != tc.expected {
				t.Errorf("resolveUploadName() = %q; expected %q", result, tc.expected)
			}
		})
	}
}

func TestResolveCountryIDUsesExactAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		meta     api.PreparedMetadata
		expected string
	}{
		{
			name: "TVDB alpha3 exact",
			meta: api.PreparedMetadata{ExternalMetadata: api.ExternalMetadata{
				TVDB: &api.TVDBMetadata{OriginalCountry: "usa"},
			}},
			expected: "2",
		},
		{
			name: "TMDB alpha2 exact",
			meta: api.PreparedMetadata{ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{OriginCountry: []string{"GB"}},
			}},
			expected: "12",
		},
		{
			name: "IMDB compound alias",
			meta: api.PreparedMetadata{ExternalMetadata: api.ExternalMetadata{
				IMDB: &api.IMDBMetadata{Country: "Trinidad and Tobago"},
			}},
			expected: "75",
		},
		{
			name: "punctuation normalized exact alias",
			meta: api.PreparedMetadata{ExternalMetadata: api.ExternalMetadata{
				IMDB: &api.IMDBMetadata{Country: "Bosnia & Herzegovina"},
			}},
			expected: "64",
		},
		{
			name: "ambiguous partial name rejected",
			meta: api.PreparedMetadata{ExternalMetadata: api.ExternalMetadata{
				IMDB: &api.IMDBMetadata{Country: "Korea"},
			}},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveCountryID(tc.meta); got != tc.expected {
				t.Fatalf("expected country id %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestResolveCountryIDSupportsBTNCountryOptions(t *testing.T) {
	t.Parallel()

	countries := map[string]string{
		"Afghanistan":                    "51",
		"Albania":                        "62",
		"Algeria":                        "32",
		"Andorra":                        "65",
		"Angola":                         "33",
		"Antigua Barbuda":                "86",
		"Arab League":                    "107",
		"Argentina":                      "19",
		"Australia":                      "20",
		"Austria":                        "34",
		"Bahamas":                        "79",
		"Bangladesh":                     "83",
		"Barbados":                       "82",
		"Belgium":                        "16",
		"Belize":                         "31",
		"Bosnia Herzegovina":             "64",
		"Brazil":                         "18",
		"Brunei":                         "113",
		"Bulgaria":                       "100",
		"Burkina Faso":                   "57",
		"Cambodia":                       "81",
		"Canada":                         "5",
		"Chile":                          "48",
		"China":                          "8",
		"Colombia":                       "95",
		"Congo":                          "50",
		"Costa Rica":                     "98",
		"Croatia":                        "93",
		"Cuba":                           "49",
		"Czech Republic":                 "43",
		"Denmark":                        "10",
		"Dominican Republic":             "38",
		"Ecuador":                        "78",
		"Egypt":                          "99",
		"Estonia":                        "94",
		"Fiji":                           "102",
		"Finland":                        "4",
		"France":                         "6",
		"Germany":                        "7",
		"Greece":                         "39",
		"Guatemala":                      "40",
		"Honduras":                       "76",
		"Hong Kong":                      "30",
		"Hungary":                        "71",
		"Iceland":                        "59",
		"India":                          "67",
		"Indonesia":                      "111",
		"Iran":                           "106",
		"Ireland":                        "13",
		"Isle de Muerte":                 "101",
		"Israel":                         "41",
		"Italy":                          "9",
		"Jamaica":                        "28",
		"Japan":                          "17",
		"Kiribati":                       "55",
		"Kuwait":                         "104",
		"Kyrgyzstan":                     "77",
		"Laos":                           "84",
		"Latvia":                         "97",
		"Lebanon":                        "96",
		"Lithuania":                      "66",
		"Luxembourg":                     "29",
		"Macedonia":                      "103",
		"Malaysia":                       "37",
		"Mexico":                         "24",
		"Nauru":                          "60",
		"Netherlands":                    "15",
		"Netherlands Antilles":           "68",
		"New Zealand":                    "21",
		"Nigeria":                        "58",
		"North Korea":                    "92",
		"Norway":                         "11",
		"Pakistan":                       "42",
		"Paraguay":                       "87",
		"Peru":                           "80",
		"Philippines":                    "56",
		"Poland":                         "14",
		"Portugal":                       "23",
		"Puerto Rico":                    "47",
		"Romania":                        "72",
		"Russia":                         "3",
		"Saudi Arabia":                   "108",
		"Scotland":                       "109",
		"Senegal":                        "90",
		"Serbia":                         "44",
		"Seychelles":                     "45",
		"Singapore":                      "25",
		"Slovakia":                       "110",
		"Slovenia":                       "61",
		"South Africa":                   "26",
		"South Korea":                    "27",
		"Spain":                          "22",
		"Sri Lanka":                      "105",
		"Sweden":                         "1",
		"Switzerland":                    "54",
		"Taiwan":                         "46",
		"Thailand":                       "89",
		"Togo":                           "91",
		"Trinidad & Tobago":              "75",
		"Turkey":                         "52",
		"Turkmenistan":                   "63",
		"Ukraine":                        "69",
		"Union of Soviet Socialist Repu": "88",
		"United Kingdom":                 "12",
		"United States of America":       "2",
		"Uruguay":                        "85",
		"Uzbekistan":                     "53",
		"Vanuatu":                        "73",
		"Venezuela":                      "70",
		"Vietnam":                        "74",
		"Wales":                          "112",
		"Western Samoa":                  "36",
		"Yugoslavia":                     "35",
	}

	for name, expected := range countries {
		meta := api.PreparedMetadata{ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{Country: name},
		}}
		if got := resolveCountryID(meta); got != expected {
			t.Fatalf("expected BTN country %q to resolve to %q, got %q", name, expected, got)
		}
	}
}

func TestValidateBTNAPIDownloadURLAllowsOnlySameOriginPrivateFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if err := validateBTNAPIDownloadURL(ctx, "http://127.0.0.1:1/rpc", "http://127.0.0.1:1/mock-download"); err != nil {
		t.Fatalf("expected same-origin private download URL to be allowed: %v", err)
	}
	if err := validateBTNAPIDownloadURL(ctx, "http://127.0.0.1:1/rpc", "http://127.0.0.1:2/mock-download"); err == nil {
		t.Fatalf("expected cross-origin private download URL to be rejected")
	}
	if err := validateBTNAPIDownloadURL(ctx, "ftp://127.0.0.1/rpc", "ftp://127.0.0.1/mock-download"); err == nil {
		t.Fatalf("expected unsupported same-origin scheme to be rejected")
	}
}

func TestBTNAPIDownloadOriginRejectsSameHostPrivateRebind(t *testing.T) {
	ctx := context.Background()
	lookupCalls := 0
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		lookupCalls++
		if lookupCalls == 1 {
			return []net.IPAddr{{IP: net.IPv4(93, 184, 216, 34)}}, nil
		}
		return []net.IPAddr{{IP: net.IPv4(10, 0, 0, 7)}}, nil
	}

	origin, err := newBTNAPIDownloadOriginWithLookup(ctx, "https://api.example.test/rpc", lookup)
	if err != nil {
		t.Fatalf("pin BTN API origin: %v", err)
	}
	if err := origin.validateDownloadURL(ctx, "https://api.example.test/download"); err == nil {
		t.Fatalf("expected same-host private rebind to be rejected")
	}
}

func TestBTNDropdownMappingsPreferMetadataThenAutofill(t *testing.T) {
	t.Parallel()

	autofillFields := map[string]string{
		"format":     "MP4",
		"bitrate":    "H.264",
		"media":      "HDTV",
		"resolution": "2160p",
	}
	metadata := api.PreparedMetadata{
		Container:   "mkv",
		VideoEncode: "x265",
		Source:      "WEB-DL",
		Type:        "WEBDL",
		Release:     api.ReleaseInfo{Resolution: "1080p"},
	}
	if got := mapContainer(metadata, autofillFields); got != "MKV" {
		t.Fatalf("expected metadata format, got %q", got)
	}
	if got := mapCodec(metadata, autofillFields); got != "H.265" {
		t.Fatalf("expected metadata bitrate, got %q", got)
	}
	if got := mapSource(metadata, autofillFields); got != "WEB-DL" {
		t.Fatalf("expected metadata media, got %q", got)
	}
	if got := mapResolution(metadata, autofillFields); got != "1080p" {
		t.Fatalf("expected metadata resolution, got %q", got)
	}

	invalidMetadata := api.PreparedMetadata{
		Container:   "unknown-container",
		VideoEncode: "unknown-codec",
		Source:      "unknown-source",
		Type:        "unknown-type",
		Release:     api.ReleaseInfo{Resolution: "unknown-resolution"},
	}
	if got := mapContainer(invalidMetadata, autofillFields); got != "MP4" {
		t.Fatalf("expected autofill format, got %q", got)
	}
	if got := mapCodec(invalidMetadata, autofillFields); got != "H.264" {
		t.Fatalf("expected autofill bitrate, got %q", got)
	}
	if got := mapSource(invalidMetadata, autofillFields); got != "HDTV" {
		t.Fatalf("expected autofill media, got %q", got)
	}
	if got := mapResolution(invalidMetadata, autofillFields); got != "2160p" {
		t.Fatalf("expected autofill resolution, got %q", got)
	}
	if got := mapResolution(api.PreparedMetadata{Release: api.ReleaseInfo{Resolution: "Portable Device"}}, autofillFields); got != "Portable Device" {
		t.Fatalf("expected metadata portable-device resolution, got %q", got)
	}
	if got := mapResolution(api.PreparedMetadata{Release: api.ReleaseInfo{Resolution: "Mixed"}}, autofillFields); got != "Mixed" {
		t.Fatalf("expected metadata mixed resolution, got %q", got)
	}
}

func TestDecodeBTNAPIJSONRejectsDuplicateKeysAndLargeBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		payload     []byte
		expectedErr string
	}{
		{
			name:        "root duplicate",
			payload:     []byte(`{"result":{},"result":{}}`),
			expectedErr: `duplicate JSON object key "result"`,
		},
		{
			name:        "nested duplicate",
			payload:     []byte(`{"result":{"DownloadURL":"https://example.test/one","DownloadURL":"https://example.test/two"}}`),
			expectedErr: `duplicate JSON object key "DownloadURL" at "result"`,
		},
		{
			name:        "oversized",
			payload:     bytes.Repeat([]byte(" "), btnAPIJSONMaxBytes+1),
			expectedErr: "response body exceeds",
		},
		{
			name:        "multiple values",
			payload:     []byte(`{} {}`),
			expectedErr: "multiple JSON values",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var decoded struct {
				Result map[string]any `json:"result"`
			}
			err := decodeBTNAPIJSON(bytes.NewReader(tc.payload), &decoded)
			if err == nil || !strings.Contains(err.Error(), tc.expectedErr) {
				t.Fatalf("expected error containing %q", tc.expectedErr)
			}
		})
	}

	var decoded struct {
		Result struct {
			DownloadURL string `json:"DownloadURL"`
		} `json:"result"`
	}
	if err := decodeBTNAPIJSON(strings.NewReader(`{"result":{"DownloadURL":"https://example.test/download"}}`), &decoded); err != nil {
		t.Fatalf("decode valid BTN API JSON: %v", err)
	}
	if decoded.Result.DownloadURL != "https://example.test/download" {
		t.Fatalf("unexpected decoded DownloadURL")
	}
}

func TestBTNUploadPayloadUsesCanonicalSeasonEpisodeOnly(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV"},
		Release:     api.ReleaseInfo{Season: 4, Episode: 9},
	}

	if got := resolveUploadType(meta); got != "Season" {
		t.Fatalf("expected upload type Season without canonical episode, got %q", got)
	}
	desc := buildAlbumDesc(meta, map[string]string{"album_desc": "fallback overview"})
	for _, value := range []string{"Season: 0", "Episode: 0"} {
		if !strings.Contains(desc, value) {
			t.Fatalf("expected album description to contain %q, got %q", value, desc)
		}
	}
	if strings.Contains(desc, "Season: 4") || strings.Contains(desc, "Episode: 9") {
		t.Fatalf("album description used parsed fallback values: %q", desc)
	}
	if got := btnTVPayloadMetadataMessage(meta); got != "canonical TV season/episode missing; BTN upload requires TVDB or metadata season/episode ints and ignores parsed season/episode fallback; refresh metadata or correct canonical season/episode before upload" {
		t.Fatalf("unexpected metadata message %q", got)
	}
}

func TestBTNUploadTypeUsesCanonicalEpisode(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV"},
		SeasonInt:   4,
		EpisodeInt:  9,
		Release:     api.ReleaseInfo{Season: 1, Episode: 2},
	}

	if got := resolveUploadType(meta); got != "Episode" {
		t.Fatalf("expected upload type Episode, got %q", got)
	}
	if got := btnTVPayloadMetadataMessage(meta); got != "" {
		t.Fatalf("unexpected metadata message %q", got)
	}
}

func TestResolveBTNTVSeasonEpisodePrefersTVDBThenMetadata(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		SeasonInt:  2,
		EpisodeInt: 7,
		ExternalMetadata: api.ExternalMetadata{
			TVDB: &api.TVDBMetadata{EpisodeSeason: 4, EpisodeNumber: 12},
		},
	}

	season, episode := resolveBTNTVSeasonEpisode(meta)
	if season != 4 || episode != 12 {
		t.Fatalf("expected TVDB season/episode 4/12, got %d/%d", season, episode)
	}
	if got := btnTVPayloadMetadataMessage(api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV"},
		ExternalMetadata: api.ExternalMetadata{
			TVDB: &api.TVDBMetadata{EpisodeSeason: 4, EpisodeNumber: 12},
		},
	}); got != "" {
		t.Fatalf("expected TVDB season/episode to satisfy BTN metadata check, got %q", got)
	}

	meta.ExternalMetadata.TVDB = nil
	season, episode = resolveBTNTVSeasonEpisode(meta)
	if season != 2 || episode != 7 {
		t.Fatalf("expected metadata season/episode 2/7, got %d/%d", season, episode)
	}
}
