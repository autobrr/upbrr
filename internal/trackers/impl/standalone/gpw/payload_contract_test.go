// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"bytes"
	"mime"
	"mime/multipart"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCapturedMultipartUsesGPWContract(t *testing.T) {
	for _, test := range []struct{ codec, container, wantCodec, wantContainer, codecOther, containerOther, encode string }{
		{"x264", "mkv", "x264", "MKV", "", "", ""}, {"x265", "mp4", "x265", "MP4", "", "", ""},
		{"AVC", "m2ts", "H.264", "m2ts", "", "", ""}, {"HEVC", "vob", "H.265", "VOB IFO", "", "", ""},
		{"VC-1", "Example Container", "Other", "Other", "VC-1", "Example Container", ""},
		{"AVC", "mkv", "x264", "MKV", "", "", "Hi10P x264"}, {"AVC", "mkv", "H.264", "MKV", "", "", "Hi10P H.264"},
		{"AVC", "mkv", "H.264", "MKV", "", "", "Hi10P"}, {"VC-1", "mkv", "Other", "MKV", "VC-1", "", "Hi10P"},
	} {
		name := test.codec
		if test.encode != "" {
			name = test.encode
		}
		t.Run(name, func(t *testing.T) {
			meta := api.UploadSubject{
				VideoCodec:                 test.codec,
				VideoEncode:                test.encode,
				Container:                  test.container,
				HardcodedSubs:              true,
				SubtitleLanguages:          []string{"English", "fr", "zh-Hans", "zh-Hant", "pt-BR", "pt", "zh", "en"},
				HardcodedSubtitleLanguages: []string{"Japanese"},
				Identity:                   api.ExternalIdentity{IMDBID: 1234567, TMDBID: 7654321},
				Release:                    api.ReleaseInfo{Title: "Example Release"},
				EffectiveMetadata:          api.EffectiveMetadata{Year: 2026, YearProvenance: api.FactProvenanceManual},
			}
			answers := map[string]string{
				"director_name": "Example Director",
				"director_imdb": "nm0000001",
				"tags":          "Drama, Comedy",
				"poster_url":    "https://images.example/poster.jpg",
			}
			form := buildForm(trackers.PreparationInput{Meta: meta}, "description", "", answers)
			if reason := validateFields("", form); reason != "" {
				t.Fatal(reason)
			}
			body, contentType, err := commonhttp.BuildMultipartPayloadMulti(form, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, params, _ := mime.ParseMediaType(contentType)
			parsed, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).ReadForm(1 << 20)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parsed.RemoveAll() }()
			for key, want := range map[string][]string{
				"codec":           {test.wantCodec},
				"container":       {test.wantContainer},
				"codec_other":     {test.codecOther},
				"container_other": {test.containerOther},
				"artists[]":       {"Example Director"},
				"artists_sub[]":   {""},
				"artist_ids[]":    {"nm0000001"},
				"importance[]":    {"1"},
				"tags[]":          {"drama", "comedy"},
				"data_source":     {"imdb"},
				"identifier":      {"tt1234567"},
				"imdb":            {"tt1234567"},
				"tmdb":            {"7654321"},
				"subtitle_type":   {"2"},
				"subtitles[]":     {"japanese"},
			} {
				if !reflect.DeepEqual(parsed.Value[key], want) {
					t.Fatalf("field %s did not match the published contract", key)
				}
			}
			if _, old := parsed.Value["artist_subs[]"]; old {
				t.Fatal("legacy artist typo retained")
			}
			if _, scalar := parsed.Value["tags"]; scalar {
				t.Fatal("scalar tags retained")
			}
		})
	}
}

func TestProviderPairAndExplicitClears(t *testing.T) {
	meta := api.UploadSubject{Identity: api.ExternalIdentity{TMDBID: 7654321}, Release: api.ReleaseInfo{Title: "Example Release"}}
	form := buildForm(trackers.PreparationInput{Meta: meta}, "description", "", nil)
	if previewFields(form)["subtitle_type"] != "3" || !reflect.DeepEqual(form["subtitles[]"], []string{""}) {
		t.Fatal("subtitle-free payload omitted the array required by GPW")
	}
	if previewFields(form)["data_source"] != "tmdb" || previewFields(form)["identifier"] != "7654321" || previewFields(form)["no_imdb_link"] != "1" {
		t.Fatal("TMDB-only identity was paired as IMDb")
	}
	manual := meta
	manual.Identity = api.ExternalIdentity{}
	manualForm := buildForm(trackers.PreparationInput{Meta: manual}, "description", "", nil)
	if fields := previewFields(manualForm); fields["data_source"] != "manual" || fields["identifier"] != "" {
		t.Fatal("manual source must preserve an empty provider identifier")
	}
	for _, complete := range []map[string][]string{form, manualForm} {
		for _, key := range []string{"image", "artists[]", "artist_ids[]", "tags[]"} {
			complete[key] = []string{"example"}
		}
		complete["year"] = []string{"2026"}
		if reason := validateFields("", complete); reason != "" {
			t.Fatal(reason)
		}
	}
	meta.Identity.IMDBID = 1234567
	for _, answers := range []map[string]string{{"data_source": "imdb", "identifier": "7654321"}, {"data_source": "tmdb", "identifier": "tt1234567"}, {"data_source": "unknown"}, {"data_source": "tmdb", "identifier": ""}} {
		form := buildForm(trackers.PreparationInput{Meta: meta}, "description", "", answers)
		// Supply the independent group prerequisites so the provider error is exercised.
		for _, key := range []string{"image", "artists[]", "artist_ids[]", "tags[]", "year"} {
			form[key] = []string{"example"}
		}
		if validateFields("", form) == "" {
			t.Fatal("invalid provider pair accepted")
		}
	}
	fields := buildFields(trackers.PreparationInput{Meta: meta}, config.TrackerConfig{}, "", map[string]string{"poster_url": "", "tags": ""})
	if fields["image"] != "" || fields["tags[]"] != "" {
		t.Fatal("explicit group answer clear was replaced")
	}
	if fields := buildForm(trackers.PreparationInput{Meta: meta}, "description", "123", nil); len(fields["imdb"]) != 0 || len(fields["artists[]"]) != 0 {
		t.Fatal("existing group included new-group metadata")
	}
}

func TestSubtitleLanguageContracts(t *testing.T) {
	for _, test := range []struct {
		name            string
		soft, hardcoded []string
		hard            bool
		want            []string
		blocked         bool
	}{
		{"soft", []string{"English", "fr", "zh-Hans", "zh-Hant", "pt-BR", "pt", "zh", "en"}, nil, false, []string{"english", "french", "chinese_simplified", "chinese_traditional", "brazilian_port", "portuguese"}, false},
		{"hardcoded", []string{"English"}, []string{"Japanese"}, true, []string{"japanese"}, false},
		{"unmapped soft", []string{"zh"}, nil, false, []string{""}, true},
		{"unmapped hardcoded", []string{"English"}, []string{"zh"}, true, []string{""}, true},
		{"missing hardcoded", []string{"English"}, nil, true, []string{""}, true},
		{"none", nil, nil, false, []string{""}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := api.UploadSubject{
				SubtitleLanguages:          test.soft,
				HardcodedSubs:              test.hard,
				HardcodedSubtitleLanguages: test.hardcoded,
			}
			form := buildForm(trackers.PreparationInput{Meta: meta}, "description", "123", nil)
			if !reflect.DeepEqual(form["subtitles[]"], test.want) || (validateFields("123", form) != "") != test.blocked {
				t.Fatalf("subtitle selection or constructibility changed: %v", form["subtitles[]"])
			}
		})
	}
}
