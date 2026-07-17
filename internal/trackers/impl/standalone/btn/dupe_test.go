// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func adapterEvidence(result dupe.AdapterResult) ([]api.DupeEntry, []string, error) {
	return result.Entries(), result.Notes(), result.Cause()
}

func btnTestInt(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func btnTestString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func TestBTNHandlerSkipsWithoutAPIKey(t *testing.T) {
	t.Parallel()

	handler := dupe.NewAdapter(New(), "BTN", config.Config{}, http.DefaultClient, nil)
	result := handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
		},
	})
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingCredentials || result.SafeMessage() != "missing api_key for tracker" {
		t.Fatalf("unexpected result disposition=%v code=%q message=%q", result.Disposition(), result.Code(), result.SafeMessage())
	}
}

func TestBTNHandlerSkipsNonTV(t *testing.T) {
	t.Parallel()

	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), http.DefaultClient, nil)
	result := handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "MOVIE",
		},
	})
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunUnsupportedContent || result.SafeMessage() != "BTN only supports TV dupe search" {
		t.Fatalf("unexpected result disposition=%v code=%q message=%q", result.Disposition(), result.Code(), result.SafeMessage())
	}
}

func TestBTNHandlerUsesTrackerIDFirst(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"torrents":{}}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)

	_, notes, err := adapterEvidence(handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		TrackerIDs: map[string]string{"btn": "1234"},
		Identity: api.ExternalIdentity{
			Category: "TV",
			IMDBID:   7654321,
			TVDBID:   8899,
		},
		Release: api.ReleaseInfo{Title: "Ignored"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %v", notes)
	}
	filter := payloads.lastFilter(t)
	assertBTNFilterValue(t, filter, "id", "1234")
	if _, ok := filter["imdb"]; ok {
		t.Fatalf("did not expect imdb when btn id is present: %#v", filter)
	}
	if _, ok := filter["tvdb"]; ok {
		t.Fatalf("did not expect tvdb when btn id is present: %#v", filter)
	}
	if _, ok := filter["searchstr"]; ok {
		t.Fatalf("did not expect searchstr when btn id is present: %#v", filter)
	}
}

func TestBTNHandlerFallsBackToIMDb(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"torrents":{}}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)

	_, _, err := adapterEvidence(handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
			IMDBID:   1234567,
		},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	filter := payloads.lastFilter(t)
	assertBTNFilterValue(t, filter, "imdb", "tt1234567")
}

func TestBTNHandlerFallsBackToTVDB(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"torrents":{}}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)

	_, _, err := adapterEvidence(handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
			TVDBID:   998877,
		},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	filter := payloads.lastFilter(t)
	if got := btnTestInt(filter["tvdb"]); got != 998877 {
		t.Fatalf("expected tvdb 998877, got %#v", filter["tvdb"])
	}
}

func TestBTNHandlerFallsBackToTitleSearch(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"torrents":{}}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)

	_, _, err := adapterEvidence(handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
		},
		Release: api.ReleaseInfo{Title: "Example Show"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	filter := payloads.lastFilter(t)
	assertBTNFilterValue(t, filter, "searchstr", "Example Show")
}

func TestBTNHandlerNormalizesEntries(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"torrents":{"777":{"GroupID":"333","ReleaseName":"Example.Show.S01E01.1080p.WEB-DL.HDR.DV","Size":12345,"Resolution":"1080p","Source":"WEB-DL","HDR":"HDR10","DolbyVision":"DV"}}}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)

	entries, notes, err := adapterEvidence(handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
		},
		Release: api.ReleaseInfo{Title: "Example Show"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %v", notes)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Name != "Example.Show.S01E01.1080p.WEB-DL.HDR.DV" {
		t.Fatalf("unexpected name: %#v", entry)
	}
	if entry.ID != "777" {
		t.Fatalf("unexpected id: %#v", entry)
	}
	if entry.Link != "https://broadcasthe.net/torrents.php?id=333&torrentid=777" {
		t.Fatalf("unexpected link: %#v", entry)
	}
	if !entry.SizeKnown || entry.SizeBytes != 12345 {
		t.Fatalf("unexpected size fields: %#v", entry)
	}
	if entry.Res != "1080p" {
		t.Fatalf("unexpected resolution: %#v", entry)
	}
	if entry.Type != "WEB-DL" {
		t.Fatalf("unexpected type: %#v", entry)
	}
	if len(entry.Flags) != 2 || entry.Flags[0] != "HDR10" || entry.Flags[1] != "DV" {
		t.Fatalf("unexpected flags: %#v", entry.Flags)
	}
}

func TestBTNHandlerAPIErrorReturnsNoDupes(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"error":{"message":"bad request"}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)

	entries, notes, err := adapterEvidence(handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
		},
		Release: api.ReleaseInfo{Title: "Example Show"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %v", notes)
	}
}

type btnPayloadCapture struct {
	client  *http.Client
	payload map[string]any
	mu      sync.Mutex
}

func captureBTNPayloads(t *testing.T, response string) *btnPayloadCapture {
	t.Helper()

	capture := &btnPayloadCapture{}
	capture.client = &http.Client{
		Transport: btnRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, fmt.Errorf("read BTN payload request body: %w", err)
			}
			_ = req.Body.Close()

			capture.mu.Lock()
			defer capture.mu.Unlock()
			if err := json.Unmarshal(body, &capture.payload); err != nil {
				return nil, fmt.Errorf("unmarshal BTN payload request body: %w", err)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(response)),
				Request:    req,
			}, nil
		}),
	}
	return capture
}

func (c *btnPayloadCapture) lastFilter(t *testing.T) map[string]any {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()

	params, ok := c.payload["params"].([]any)
	if !ok || len(params) < 2 {
		t.Fatalf("expected JSON-RPC params, got %#v", c.payload)
	}
	filter, ok := params[1].(map[string]any)
	if !ok {
		t.Fatalf("expected filter map, got %#v", params[1])
	}
	return filter
}

func configWithBTNAPIKey() config.Config {
	return config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"BTN": {APIKey: strings.Repeat("x", 30)},
			},
		},
	}
}

func assertBTNFilterValue(t *testing.T, filter map[string]any, key string, want string) {
	t.Helper()
	if got := btnTestString(filter[key]); got != want {
		t.Fatalf("expected %s=%q, got %#v", key, want, filter)
	}
}

type btnRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn btnRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
