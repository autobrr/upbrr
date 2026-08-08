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

	payloads := captureBTNPayloads(t, `{"result":{"results":"0","torrents":{}}}`)
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
	if _, ok := filter["search"]; ok {
		t.Fatalf("did not expect search when btn id is present: %#v", filter)
	}
}

func TestBTNHandlerFallsBackToTitleWhenOnlyIMDbIsAvailable(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"results":"0","torrents":{}}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)

	result := handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
			IMDBID:   1234567,
		},
		Release: api.ReleaseInfo{Title: "Example Show"},
	})
	if err := result.Cause(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	filter := payloads.lastFilter(t)
	assertBTNFilterValue(t, filter, "search", "Example%Show")
	if result.SearchEvidence().WorkScope != dupe.WorkScopeTitle {
		t.Fatalf("expected title fallback evidence, got %#v", result.SearchEvidence())
	}
}

func TestBTNHandlerFallsBackToTVDB(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"results":"0","torrents":{}}}`)
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

func TestBTNHandlerPrefersBroadTitleSearch(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"results":"0","torrents":{}}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)

	_, _, err := adapterEvidence(handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
		},
		Release: api.ReleaseInfo{Title: "Example Release 2026"},
		Projection: &api.TrackerReleaseProjection{
			DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Exact Projected Search"},
		},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	filter := payloads.lastFilter(t)
	assertBTNFilterValue(t, filter, "search", "Example%Release%2026")
}

func TestBTNHandlerNormalizesEntries(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"results":"1","torrents":{"777":{"GroupID":"333","TVDBID":"1234567","IMDBID":"tt1234567","ReleaseName":"Example.Show.S01.1080p.WEB-DL.HDR.DV-NTb","Size":12345,"Category":"season","Resolution":"1080p","Source":"WEB-DL","Codec":"H.264","Container":"MKV","Origin":"P2P","GroupName":"NTb","HDR":"HDR10","DolbyVision":"DV"}}}}`)
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
	if entry.Name != "Example.Show.S01.1080p.WEB-DL.HDR.DV-NTb" {
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
	if entry.Type != "" || entry.Source != "WEB-DL" || entry.Category != "season" || !entry.Pack || entry.Codec != "H.264" || entry.Container != "MKV" {
		t.Fatalf("unexpected category/source/media mapping: %#v", entry)
	}
	if entry.ReleaseOrigin != "P2P" || entry.Group != "NTb" || !entry.Internal {
		t.Fatalf("unexpected origin/group mapping: %#v", entry)
	}
	if len(entry.ProviderIDs) != 3 || entry.ProviderIDs[0].Provider != "btn" || entry.ProviderIDs[0].Value != "333" ||
		entry.ProviderIDs[1].Provider != "tvdb" || entry.ProviderIDs[1].Value != "1234567" ||
		entry.ProviderIDs[2].Provider != "imdb" || entry.ProviderIDs[2].Value != "tt1234567" {
		t.Fatalf("unexpected provider IDs: %#v", entry.ProviderIDs)
	}
	if len(entry.Flags) != 2 || entry.Flags[0] != "HDR10" || entry.Flags[1] != "DV" {
		t.Fatalf("unexpected flags: %#v", entry.Flags)
	}
	if !entry.FlagsPresent || !entry.FlagsComplete {
		t.Fatalf("unexpected flag completeness: %#v", entry)
	}
}

func TestBTNTorrentMapsGroupEpisodeCoordinates(t *testing.T) {
	t.Parallel()

	entry := decodeBTNTorrent("777", map[string]any{
		"GroupName":   "S01E12",
		"ReleaseName": "[GRP] Example Show - 12 (1080p)",
	}).dupeEntry()
	if entry.Season != 1 || entry.Episode != 12 {
		t.Fatalf("group coordinates = season %d episode %d", entry.Season, entry.Episode)
	}
}

func TestBTNHandlerLeavesMissingOptionalEvidenceMissing(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"results":1,"torrents":{"777":{"ReleaseName":"Example.Show.S01E01.480p-GRP","Resolution":"SD","HDR":null,"DolbyVision":null}}}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)
	result := handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity:   api.ExternalIdentity{Category: "TV", TVDBID: 1234567},
	})
	entries := result.Entries()
	if result.Cause() != nil || len(entries) != 1 {
		t.Fatalf("unexpected result entries=%d cause=%v", len(entries), result.Cause())
	}
	entry := entries[0]
	if entry.Type != "" || entry.Source != "" || entry.Category != "" || entry.Codec != "" || entry.Container != "" ||
		entry.ReleaseOrigin != "" || entry.Group != "" || entry.Internal || entry.FlagsPresent || entry.FlagsComplete || len(entry.ProviderIDs) != 0 {
		t.Fatalf("missing optional evidence was inferred: %#v", entry)
	}
}

func TestBTNInternalGroupDetectionIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	if !isBTNInternalGroupName("ntb") || !isBTNInternalGroupName("-NTb") || !isBTNInternalGroupName(" -NTb ") || isBTNInternalGroupName("GRP") {
		t.Fatal("unexpected BTN internal-group classification")
	}
}

func TestBTNHandlerAPIErrorReturnsNoDupes(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"error":{"message":"bad request"}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)

	result := handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
		},
		Release: api.ReleaseInfo{Title: "Example Show"},
	})
	if result.Disposition() != dupe.DispositionFailed || result.Code() != dupe.FailureResponseStatus || result.Cause() == nil {
		t.Fatalf("unexpected result disposition=%v code=%q cause=%v", result.Disposition(), result.Code(), result.Cause())
	}
	if len(result.Entries()) != 0 {
		t.Fatalf("expected no entries, got %d", len(result.Entries()))
	}
	if len(result.Notes()) != 0 {
		t.Fatalf("expected no notes, got %v", result.Notes())
	}
}

func TestBTNHandlerPaginatesUntilReportedTotal(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloadSequence(t,
		`{"result":{"results":"3","torrents":{"101":{"ReleaseName":"Example.Show.S01E01.1080p-GRP"},"102":{"ReleaseName":"Example.Show.S01E01.720p-GRP"}}}}`,
		`{"result":{"results":3,"torrents":{"103":{"ReleaseName":"Example.Show.S01E01.2160p-GRP"}}}}`,
	)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)
	result := handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
			TVDBID:   998877,
		},
	})

	if result.Cause() != nil || len(result.Entries()) != 3 {
		t.Fatalf("unexpected result entries=%d cause=%v", len(result.Entries()), result.Cause())
	}
	evidence := result.SearchEvidence()
	if !evidence.Complete || evidence.Pages != 2 || !evidence.EffectiveComplete() {
		t.Fatalf("unexpected search evidence: %#v", evidence)
	}
	for index, wantOffset := range []int64{0, 2} {
		payload := payloads.payloadAt(t, index)
		if btnTestString(payload["method"]) != "getTorrents" {
			t.Fatalf("request %d did not use getTorrents", index)
		}
		params := btnPayloadParams(t, payload)
		if got := btnTestInt(params[2]); got != btnDupePageLimit {
			t.Fatalf("request %d limit=%d, want %d", index, got, btnDupePageLimit)
		}
		if got := btnTestInt(params[3]); got != wantOffset {
			t.Fatalf("request %d offset=%d, want %d", index, got, wantOffset)
		}
	}
}

func TestBTNHandlerPreservesEntriesAfterPartialRequestFailure(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"results":"3","torrents":{"101":{"ReleaseName":"Example.Show.S01E01.1080p-GRP"},"102":{"ReleaseName":"Example.Show.S01E01.720p-GRP"}}}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)
	result := handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
			TVDBID:   998877,
		},
	})

	evidence := result.SearchEvidence()
	if len(result.Entries()) != 2 || evidence.Complete || len(evidence.Warnings) != 1 || evidence.Warnings[0] != "BTN search stopped after a partial request failure" {
		t.Fatalf("unexpected partial search result entries=%d evidence=%#v", len(result.Entries()), evidence)
	}
}

func TestBTNHandlerUsesOneShotDailySearch(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"results":"2500","torrents":{"777":{"ReleaseName":"Example.Daily.2026.02.03.1080p-GRP"}}}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)
	result := handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath:       "x",
		DailyEpisodeDate: "2026-02-03",
		Identity: api.ExternalIdentity{
			Category: "TV",
			TVDBID:   998877,
		},
		Release: api.ReleaseInfo{Title: "Example Daily"},
	})

	if result.Cause() != nil || len(result.Entries()) != 1 || payloads.requestCount() != 1 {
		t.Fatalf("unexpected daily result entries=%d requests=%d cause=%v", len(result.Entries()), payloads.requestCount(), result.Cause())
	}
	filter := payloads.lastFilter(t)
	assertBTNFilterValue(t, filter, "tvdb", "998877")
	assertBTNFilterValue(t, filter, "search", "Example%Daily")
	assertBTNFilterValue(t, filter, "category", "Episode")
	assertBTNFilterValue(t, filter, "name", "2026.02.03%")
	evidence := result.SearchEvidence()
	if evidence.Complete || evidence.Pages != 1 || evidence.Scope != "daily_episode" || len(evidence.Warnings) != 1 {
		t.Fatalf("unexpected daily search evidence: %#v", evidence)
	}
}

func TestBTNHandlerRequiresReportedResultCountForCompleteness(t *testing.T) {
	t.Parallel()

	payloads := captureBTNPayloads(t, `{"result":{"torrents":{"777":{"ReleaseName":"Example.Show.S01E01.1080p-GRP"}}}}`)
	handler := dupe.NewAdapter(New(), "BTN", configWithBTNAPIKey(), payloads.client, nil)
	result := handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "x",
		Identity: api.ExternalIdentity{
			Category: "TV",
			TVDBID:   998877,
		},
	})

	evidence := result.SearchEvidence()
	if evidence.Complete || len(evidence.Warnings) != 1 || evidence.Warnings[0] != "BTN search response omitted a valid results count" {
		t.Fatalf("unexpected search evidence: %#v", evidence)
	}
}

type btnPayloadCapture struct {
	client    *http.Client
	payloads  []map[string]any
	responses []string
	mu        sync.Mutex
}

func captureBTNPayloads(t *testing.T, response string) *btnPayloadCapture {
	t.Helper()
	return captureBTNPayloadSequence(t, response)
}

func captureBTNPayloadSequence(t *testing.T, responses ...string) *btnPayloadCapture {
	t.Helper()

	capture := &btnPayloadCapture{responses: responses}
	capture.client = &http.Client{
		Transport: btnRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, fmt.Errorf("read BTN payload request body: %w", err)
			}
			_ = req.Body.Close()

			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				return nil, fmt.Errorf("unmarshal BTN payload request body: %w", err)
			}
			capture.mu.Lock()
			index := len(capture.payloads)
			if index >= len(capture.responses) {
				capture.mu.Unlock()
				return nil, fmt.Errorf("unexpected BTN request %d", index+1)
			}
			capture.payloads = append(capture.payloads, payload)
			response := capture.responses[index]
			capture.mu.Unlock()

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
	return btnPayloadFilter(t, c.payloadAt(t, c.requestCount()-1))
}

func (c *btnPayloadCapture) payloadAt(t *testing.T, index int) map[string]any {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()
	if index < 0 || index >= len(c.payloads) {
		t.Fatalf("BTN payload index %d out of range", index)
	}
	return c.payloads[index]
}

func (c *btnPayloadCapture) requestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.payloads)
}

func btnPayloadParams(t *testing.T, payload map[string]any) []any {
	t.Helper()
	params, ok := payload["params"].([]any)
	if !ok || len(params) != 4 {
		t.Fatal("expected four JSON-RPC params")
	}
	return params
}

func btnPayloadFilter(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	params := btnPayloadParams(t, payload)
	filter, ok := params[1].(map[string]any)
	if !ok {
		t.Fatal("expected BTN filter map")
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
