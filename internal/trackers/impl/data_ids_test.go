// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackerLookupExternalIDs(t *testing.T) {
	t.Parallel()
	registry := trackerimpl.MustNewRegistry()
	for _, supplier := range []struct {
		name       string
		response   string
		tmdb, tvdb bool
		mal        bool
	}{
		{
			name:     "HDB",
			response: `{"status":0,"data":[{"id":42,"imdb":{"id":%[1]s},"tvdb":{"id":%[1]s}}]}`,
			tvdb:     true,
		},
		{
			name:     "ANT",
			response: `{"item":[{"imdb":%[1]s,"tmdb":%[1]s}]}`,
			tmdb:     true,
		},
		{
			name:     "BHD",
			response: `{"status_code":1,"results":[{"id":42,"imdb_id":%[1]s,"tmdb_id":%[1]s}]}`,
			tmdb:     true,
		},
		{
			name:     "BTN",
			response: `{"result":{"torrents":{"42":{"GroupID":7,"ImdbID":%[1]s,"TvdbID":%[1]s}}}}`,
			tvdb:     true,
		},
		{name: "PTP", response: `{"GroupId":7,"ImdbId":%[1]s,"Torrents":[{"Id":42}]}`},
		{
			name:     "BLU",
			response: `{"data":[{"id":42,"attributes":{"imdb_id":%[1]s,"tmdb_id":%[1]s,"tvdb_id":%[1]s,"mal_id":%[1]s}}]}`,
			tmdb:     true,
			tvdb:     true,
			mal:      true,
		},
	} {
		t.Run(supplier.name, func(t *testing.T) {
			t.Parallel()
			for _, test := range []struct {
				name  string
				value string
				want  int64
			}{
				{
					name:  "numeric eight digits",
					value: `12345678`,
					want:  12345678,
				},
				{
					name:  "quoted eight digits",
					value: `"12345678"`,
					want:  12345678,
				},
				{
					name:  "small ID",
					value: `1`,
					want:  1,
				},
				{
					name:  "beyond float precision",
					value: `9007199254740993`,
					want:  9007199254740993,
				},
				{name: "null", value: `null`},
				{name: "fractional number", value: `12345678.5`},
				{name: "decimal notation", value: `12345678.0`},
				{name: "fractional string", value: `"12345678.5"`},
				{name: "exponent form", value: `1.2345678e7`},
				{name: "trailing junk", value: `"12345678junk"`},
				{name: "out of range number", value: `9223372036854775808`},
				{name: "out of range string", value: `"9223372036854775808"`},
				{name: "negative", value: `-12`},
				{name: "boolean", value: `true`},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						_, _ = fmt.Fprintf(w, supplier.response, test.value)
					}))
					t.Cleanup(server.Close)
					base, err := url.Parse(server.URL)
					if err != nil {
						t.Fatal(err)
					}
					client := server.Client()
					client.Transport = rewriteHostTransport{base: base, rt: client.Transport}
					token := strings.Repeat("x", 30)
					cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
						supplier.name: {
							APIKey:     token,
							BhdRSSKey:  token,
							Username:   "user",
							Passkey:    "key",
							PTPAPIUser: "user",
							PTPAPIKey:  "key",
						},
					}}}
					lookup := trackerdata.NewClientWithRegistry(cfg, api.NopLogger{}, client, registry)
					result, err := lookup.Lookup(t.Context(), supplier.name, "42", api.UploadSubject{}, "Example.Release.mkv", true, false)
					if err != nil {
						t.Fatal(err)
					}
					want := int(test.want)
					if strconv.IntSize == 32 && test.want > 2147483647 || supplier.name == "BLU" && strings.HasPrefix(test.value, `"`) {
						want = 0
					}
					if result.IMDBID != want || supplier.tmdb && result.TMDBID != want || supplier.tvdb && result.TVDBID != want || supplier.mal && result.MALID != want {
						t.Fatalf("external IDs = IMDb:%d TMDB:%d TVDB:%d MAL:%d, want %d for supplied fields",
							result.IMDBID, result.TMDBID, result.TVDBID, result.MALID, want)
					}
				})
			}
		})
	}
}
