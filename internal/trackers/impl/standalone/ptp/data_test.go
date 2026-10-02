// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

func TestParsePTPResponseKeepsGroupForTorrentPage(t *testing.T) {
	t.Parallel()

	found := parsePTPResponse(map[string]any{
		"GroupId":  "700",
		"ImdbId":   "1234567",
		"Torrents": []any{map[string]any{"Id": "42", "ReleaseName": "Example.Release.2026-GRP"}},
	}, "42", "")
	if found.groupID != "700" || found.trackerID != "42" || found.imdbID != 1234567 {
		t.Fatalf("unexpected PTP torrent identity: %+v", found)
	}
	unmatched := parsePTPResponse(map[string]any{
		"GroupId":  "700",
		"Torrents": []any{map[string]any{"Id": "41"}},
	}, "42", "")
	if unmatched.groupID != "" {
		t.Fatalf("unmatched PTP torrent retained group page: %+v", unmatched)
	}
	searchMatch := parsePTPResponse(map[string]any{
		"GroupId": "700",
		"Torrents": []any{
			map[string]any{
				"Id":          "41",
				"ReleaseName": "Other.Release",
				"InfoHash":    "wrong",
			},
			map[string]any{
				"Id":          "42",
				"ReleaseName": "Example.Release.2026-GRP",
				"InfoHash":    "right",
			},
		},
	}, "", "Example.Release.2026-GRP")
	if searchMatch.trackerID != "42" || searchMatch.groupID != "700" || searchMatch.infoHash != "right" {
		t.Fatalf("PTP search selected wrong torrent: %+v", searchMatch)
	}
}

func TestDataLookupReturnsExactTorrentPage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("torrentid") != "42" {
			t.Errorf("torrent lookup query = %q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"GroupId":  "700",
			"ImdbId":   "1234567",
			"Torrents": []any{map[string]any{"Id": "42", "ReleaseName": "Example.Release.2026-GRP"}},
		})
	}))
	defer server.Close()

	lookup := &dataLookup{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"PTP": {PTPAPIUser: "user", PTPAPIKey: "key"},
		}}},
		http:     server.Client(),
		endpoint: server.URL + "/torrents.php",
	}
	result, err := lookup.Lookup(t.Context(), trackers.DataLookupRequest{TrackerID: "42", OnlyID: true})
	if err != nil || result.TorrentURL != server.URL+"/torrents.php?id=700&torrentid=42" {
		t.Fatalf("torrent page = %q, error = %v", result.TorrentURL, err)
	}
}

func TestDataLookupSearchesPastUnmatchedGroup(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("searchstr"); got != "Example.Release.2026-GRP" {
			t.Errorf("PTP search query = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Movies": []any{
				map[string]any{"GroupId": "700", "Torrents": []any{map[string]any{"Id": "41", "ReleaseName": "Other.Release"}}},
				map[string]any{"GroupId": "701", "Torrents": []any{map[string]any{"Id": "42", "ReleaseName": "Example.Release.2026-GRP"}}},
			},
		})
	}))
	defer server.Close()

	lookup := &dataLookup{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"PTP": {PTPAPIUser: "user", PTPAPIKey: "key"},
		}}},
		http:     server.Client(),
		endpoint: server.URL + "/torrents.php",
	}
	result, err := lookup.Lookup(t.Context(), trackers.DataLookupRequest{SearchName: "Example.Release.2026-GRP.mkv", OnlyID: true})
	if err != nil || result.TorrentURL != server.URL+"/torrents.php?id=701&torrentid=42" {
		t.Fatalf("torrent page = %q, error = %v", result.TorrentURL, err)
	}
}

func TestPTPSearchNameKeepsExtensionlessRelease(t *testing.T) {
	t.Parallel()
	if got := ptpSearchName("Example.Release.2026"); got != "Example.Release.2026" {
		t.Fatalf("extensionless release name = %q", got)
	}
}
