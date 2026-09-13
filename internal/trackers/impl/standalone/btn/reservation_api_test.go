// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBTNReservationAPIScope(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		origin   string
		release  string
		group    string
		imdbOnly bool
		wantErr  string
	}{
		{
			name:    "internal group",
			origin:  "Internal",
			release: "Example.Show.S01E01.1080p-NTb",
			wantErr: "2-hour reservation",
		},
		{
			name:    "pending internal classification",
			origin:  "None",
			release: "Example.Show.S01E01.1080p-NTb",
			wantErr: "2-hour reservation",
		},
		{
			name:     "IMDb binding",
			origin:   "Internal",
			release:  "Example.Show.S01E01.1080p-NTb",
			imdbOnly: true,
			wantErr:  "2-hour reservation",
		},
		{
			name:    "other group",
			origin:  "Internal",
			release: "Example.Show.S01E01.1080p-GRP",
		},
		{
			name:    "scene origin",
			origin:  "Scene",
			release: "Example.Show.S01E01.1080p-NTb",
		},
		{
			name:    "p2p origin",
			origin:  "P2P",
			release: "Example.Show.S01E01.1080p-NTb",
		},
		{
			name:    "different season",
			origin:  "Internal",
			release: "Example.Show.S02E01.1080p-NTb",
		},
		{
			name:    "unknown origin",
			origin:  "",
			release: "Example.Show.S01E01.1080p-NTb",
			wantErr: "valid origin",
		},
		{
			name:    "missing group",
			origin:  "Internal",
			release: "Example.Show.S01E01.1080p",
			wantErr: "release group",
		},
		{
			name:    "source hyphen without group",
			origin:  "Internal",
			release: "Example.Show.S01E01.1080p.WEB-DL",
			wantErr: "release group",
		},
		{
			name:    "explicit group without suffix",
			origin:  "Internal",
			release: "Example.Show.S01E01.1080p.WEB-DL",
			group:   "NTb",
			wantErr: "2-hour reservation",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := btnReservationTestInput()
			if tt.imdbOnly {
				req.Meta.Identity.TVDBID = 0
				req.Meta.Identity.IMDBID = 1234567
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var rpc struct {
					Version any    `json:"jsonrpc"`
					ID      string `json:"id"`
					Method  string `json:"method"`
					Params  struct {
						Search map[string]json.RawMessage `json:"search"`
					} `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
					t.Errorf("decode request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if rpc.Version != nil || rpc.ID == "" || rpc.Method != "getTorrents" {
					t.Error("expected documented BTN v1 envelope")
				}
				var origins []string
				if err := json.Unmarshal(rpc.Params.Search["origin"], &origins); err != nil || !slices.Equal(origins, []string{"Internal", "None"}) {
					t.Errorf("reservation origin filter=%v err=%v", origins, err)
				}
				key, id := "tvdb", "123456"
				if tt.imdbOnly {
					key, id = "imdb", "1234567"
				}
				if len(rpc.Params.Search) != 3 || string(rpc.Params.Search[key]) != fmt.Sprintf("%q", id) || string(rpc.Params.Search["category"]) != `"Episode"` {
					t.Errorf("unexpected search filters: %v", rpc.Params.Search)
				}
				_, _ = fmt.Fprintf(w, `{"result":{"results":"1","torrents":{"1":{"ReleaseName":%q,"ReleaseGroup":%q,"Origin":%q,"Time":"%d"}}}}`, tt.release, tt.group, tt.origin, time.Now().Unix())
			}))
			defer server.Close()
			err := checkBTNSeasonPackReservation(t.Context(), uploadContext{apiURL: server.URL, apiToken: strings.Repeat("x", 30)}, req)
			if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("reservation error=%v want=%q", err, tt.wantErr)
			}
		})
	}
}
