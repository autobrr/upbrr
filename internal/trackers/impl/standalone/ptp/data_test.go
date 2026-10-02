// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDataLookupResponseOutcomes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		status    int
		body      string
		wantError bool
	}{
		{
			name:      "server error",
			status:    http.StatusInternalServerError,
			body:      `private response`,
			wantError: true,
		},
		{
			name:      "API error",
			status:    http.StatusOK,
			body:      `{"Result":"Error","Error":"private response"}`,
			wantError: true,
		},
		{
			name:   "empty",
			status: http.StatusOK,
			body:   `{}`,
		},
		{
			name:   "not found",
			status: http.StatusNotFound,
			body:   `private response`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("action") == "get_description" {
					t.Error("empty or failed lookup requested a description")
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			lookup := newDataTestLookup(server)
			result, err := lookup.Lookup(t.Context(), trackers.DataLookupRequest{TrackerID: "42"})
			if (err != nil) != tt.wantError || result.HasData() {
				t.Fatalf("response outcome: result=%+v err=%v, wantError=%t", result, err, tt.wantError)
			}
			if err != nil && strings.Contains(err.Error(), "private response") {
				t.Fatalf("response content leaked: %v", err)
			}
		})
	}
}

func TestDataLookupPreservesDescriptionFailure(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name           string
		status         int
		transportError error
		wantError      bool
	}{
		{
			name:      "server error",
			status:    http.StatusInternalServerError,
			wantError: true,
		},
		{
			name:           "transport error",
			transportError: io.ErrUnexpectedEOF,
			wantError:      true,
		},
		{
			name:           "canceled",
			transportError: context.Canceled,
			wantError:      true,
		},
		{name: "empty", status: http.StatusOK},
		{name: "not found", status: http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("action") == "get_description" {
					w.WriteHeader(tt.status)
					return
				}
				_, _ = w.Write([]byte(`{"GroupId":"700","ImdbId":"1234567","Torrents":[{"Id":"42","InfoHash":"example-hash"}]}`))
			}))
			defer server.Close()
			lookup := newDataTestLookup(server)
			transport := lookup.http.Transport
			lookup.http.Transport = dataRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Query().Get("action") == "get_description" && tt.transportError != nil {
					return nil, tt.transportError
				}
				return transport.RoundTrip(req)
			})
			result, err := lookup.Lookup(t.Context(), trackers.DataLookupRequest{TrackerID: "42"})
			if (err != nil) != tt.wantError || result.IMDBID != 1234567 || result.TrackerID != "42" || result.InfoHash != "example-hash" ||
				result.TorrentURL != server.URL+"/torrents.php?id=700&torrentid=42" || result.Description != "" {
				t.Fatalf("description outcome: result=%+v err=%v, wantError=%t", result, err, tt.wantError)
			}
			if tt.transportError != nil && !errors.Is(err, tt.transportError) {
				t.Fatalf("description error lost cause: %v", err)
			}
		})
	}
}

func newDataTestLookup(server *httptest.Server) *dataLookup {
	return &dataLookup{
		cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"PTP": {PTPAPIUser: "user", PTPAPIKey: "key"},
		}}},
		http:     server.Client(),
		endpoint: server.URL + "/torrents.php",
	}
}

type dataRoundTripFunc func(*http.Request) (*http.Response, error)

func (f dataRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
