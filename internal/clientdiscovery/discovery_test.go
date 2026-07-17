// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clientdiscovery

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type recordingClient struct {
	calls  int
	input  api.ClientSubject
	result api.ClientSearchResult
	err    error
}

func (*recordingClient) Inject(context.Context, api.ClientSubject, api.TorrentResult) error {
	return nil
}

func (c *recordingClient) SearchPathedTorrents(_ context.Context, input api.ClientSubject) (api.ClientSearchResult, error) {
	c.calls++
	c.input = input
	return c.result, c.err
}

func TestDiscoverNormalizesAndDetachesCurrentEvidence(t *testing.T) {
	t.Parallel()

	clientName := " qbit "
	trackerName := " BTN "
	force := true
	client := &recordingClient{result: api.ClientSearchResult{
		InfoHash:          " hash ",
		TorrentPath:       " torrent/example.torrent ",
		TrackerIDs:        map[string]string{trackerName: " 123 ", "empty": " "},
		FoundTrackerMatch: true,
		TorrentComments: []api.TorrentMatch{{
			Name:           "Example.Release.2026",
			TrackerURLsRaw: []string{"https://tracker.invalid/announce"},
			TrackerURLs:    []api.TrackerMatch{{ID: "123", TrackerID: "btn"}},
		}},
		PieceSizeConstraint: " 4194304 ",
		FoundPreferredPiece: " preferred ",
		MatchedTrackers:     []string{" btn ", "AITHER", "BTN", ""},
	}}
	files := []string{"Example.Release.2026.mkv"}
	evidence, err := New(client, api.NopLogger{}).Discover(context.Background(), SearchInput{
		SourcePath:   " Example.Release.2026 ",
		FileList:     files,
		DiscType:     " BDMV ",
		Policy:       api.ClientSearchPolicy{Client: &clientName},
		ForceRecheck: &force,
	})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if client.calls != 1 || client.input.SourcePath != "Example.Release.2026" || client.input.DiscType != "BDMV" {
		t.Fatalf("client input = %#v calls=%d", client.input, client.calls)
	}
	if client.input.ClientOverrides.Client == nil || *client.input.ClientOverrides.Client != "qbit" {
		t.Fatalf("client selector = %#v", client.input.ClientOverrides.Client)
	}
	if client.input.ClientOverrides.ForceRecheck == nil || !*client.input.ClientOverrides.ForceRecheck {
		t.Fatalf("force recheck = %#v", client.input.ClientOverrides.ForceRecheck)
	}
	if evidence.InfoHash != "hash" || evidence.TorrentPath != "torrent/example.torrent" || evidence.TrackerIDs["btn"] != "123" {
		t.Fatalf("evidence = %#v", evidence)
	}
	if !slices.Equal(evidence.MatchedTrackers, []string{"AITHER", "BTN"}) {
		t.Fatalf("matched trackers = %#v", evidence.MatchedTrackers)
	}
	files[0] = "changed"
	client.result.TrackerIDs["BTN"] = "changed"
	client.result.TorrentComments[0].TrackerURLsRaw[0] = "changed"
	client.result.TorrentComments[0].TrackerURLs[0].ID = "changed"
	if client.input.FileList[0] != "Example.Release.2026.mkv" || evidence.TrackerIDs["btn"] != "123" ||
		evidence.TorrentComments[0].TrackerURLsRaw[0] == "changed" || evidence.TorrentComments[0].TrackerURLs[0].ID == "changed" {
		t.Fatalf("input/evidence aliases caller state: input=%#v evidence=%#v", client.input, evidence)
	}
}

func TestDiscoverSkipAndUnavailableAreSuccessfulEmptySnapshots(t *testing.T) {
	t.Parallel()

	client := &recordingClient{}
	evidence, err := New(client, api.NopLogger{}).Discover(context.Background(), SearchInput{
		SourcePath: "Example.Release.2026.mkv",
		Policy:     api.ClientSearchPolicy{Skip: true},
	})
	if err != nil || !reflect.DeepEqual(evidence, Evidence{}) || client.calls != 0 {
		t.Fatalf("skip evidence=%#v err=%v calls=%d", evidence, err, client.calls)
	}
	evidence, err = New(nil, api.NopLogger{}).Discover(context.Background(), SearchInput{SourcePath: "Example.Release.2026.mkv"})
	if err != nil || !reflect.DeepEqual(evidence, Evidence{}) {
		t.Fatalf("unavailable evidence=%#v err=%v", evidence, err)
	}
}

func TestDiscoverPreservesSearchErrorsAndCancellation(t *testing.T) {
	t.Parallel()

	searchErr := errors.New("search failed")
	_, err := New(&recordingClient{err: searchErr}, api.NopLogger{}).Discover(
		context.Background(),
		SearchInput{SourcePath: "Example.Release.2026.mkv"},
	)
	if !errors.Is(err, searchErr) {
		t.Fatalf("search error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &recordingClient{}
	_, err = New(client, api.NopLogger{}).Discover(ctx, SearchInput{SourcePath: "Example.Release.2026.mkv"})
	if !errors.Is(err, context.Canceled) || client.calls != 0 {
		t.Fatalf("canceled error=%v calls=%d", err, client.calls)
	}
}
