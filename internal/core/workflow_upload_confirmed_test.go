// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowUploadExecutionAlreadySucceededSkipsClientInjection(t *testing.T) {
	t.Parallel()

	clients := &dryRunClientService{}
	execution := &workflowUploadExecution{
		plan: &workflowRetainedUploadPlanFake{results: []trackers.RetainedTrackerResult{{
			Tracker:          "ALPHA",
			AlreadySucceeded: true,
			Summary:          api.UploadSummary{Uploaded: 1},
		}}},
		clients: clients,
	}

	results, err := execution.Execute(t.Context(), nil)
	if err != nil {
		t.Fatalf("execute workflow upload: %v", err)
	}
	if len(results) != 1 || results[0].Status != api.StageStatusCompleted ||
		results[0].SubmissionStatus != api.StageStatusCompleted ||
		results[0].ClientInjectionStatus != api.StageStatusSkipped ||
		results[0].ClientInjectionMessage != "Client injection skipped because the tracker submission was already completed." ||
		results[0].ClientInjected || results[0].CrossSeeded || results[0].RemoteID != "" || results[0].RemoteURL != "" ||
		len(results[0].Failures) != 0 || len(clients.injections) != 0 || len(execution.registeredArtifacts) != 0 {
		t.Fatalf("already-succeeded execution results=%#v injections=%#v artifacts=%#v", results, clients.injections, execution.registeredArtifacts)
	}
}

func TestWorkflowUploadExecutionPreservesRegisteredPageIDs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		noSeed     bool
		download   string
		injectErr  error
		wantClient api.StageStatus
	}{
		{
			name:       "no seed",
			noSeed:     true,
			wantClient: api.StageStatusSkipped,
		},
		{name: "missing artifact", wantClient: api.StageStatusFailed},
		{
			name:       "injected",
			download:   "https://tracker.invalid/download/456",
			wantClient: api.StageStatusCompleted,
		},
		{
			name:       "injection failed",
			download:   "https://tracker.invalid/download/456",
			injectErr:  errors.New("client unavailable"),
			wantClient: api.StageStatusFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			execution := &workflowUploadExecution{
				plan: &workflowRetainedUploadPlanFake{results: []trackers.RetainedTrackerResult{{
					Tracker: "ALPHA",
					Summary: api.UploadSummary{UploadedTorrents: []api.UploadedTorrent{{
						Tracker:     "ALPHA",
						TorrentID:   "456",
						TorrentURL:  "https://user:secret@tracker.invalid/torrents.php?id=123&torrentid=456&passkey=private#secret",
						DownloadURL: tc.download,
					}}},
				}}},
				noSeed:  tc.noSeed,
				clients: &dryRunClientService{injectErr: tc.injectErr},
			}
			results, err := execution.Execute(t.Context(), nil)
			if err != nil {
				t.Fatalf("execute workflow upload: %v", err)
			}
			if len(results) != 1 || results[0].SubmissionStatus != api.StageStatusCompleted ||
				results[0].ClientInjectionStatus != tc.wantClient || results[0].RemoteID != "456" ||
				results[0].RemoteURL != "https://tracker.invalid/torrents.php?id=123&torrentid=456" {
				t.Fatalf("registered page outcome = %#v", results)
			}
		})
	}
}

func TestWorkflowRemoteURLKeepsOnlyPublicPageFields(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ input, want string }{
		{"https://tracker.invalid/details.php?id=42&uploaded=1&token=private", "https://tracker.invalid/details.php?id=42"},
		{"https://tracker.invalid/torrents/42?passkey=private", "https://tracker.invalid/torrents/42"},
		{"https://tracker.invalid/torrents.php?id=token%3Dprivate&torrentid=42", "https://tracker.invalid/torrents.php?torrentid=42"},
		{"https://tracker.invalid/torrents.php?id=1&id=2&torrentid=", "https://tracker.invalid/torrents.php"},
		{"https://tracker.invalid/details.php?id=abc-123", "https://tracker.invalid/details.php?id=abc-123"},
		{"https://tracker.invalid/details.php?id=18446744073709551616", "https://tracker.invalid/details.php?id=18446744073709551616"},
		{"https://tracker.invalid/details.php?id=42&token=%zz", "https://tracker.invalid/details.php"},
		{"/details.php?id=42", ""},
		{"file:///details.php?id=42", ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeWorkflowRemoteURL(tc.input); got != tc.want {
				t.Fatalf("public remote URL = %q, want %q", got, tc.want)
			}
		})
	}
}
func TestRegisteredPublicPageURLPreservation(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"https://tracker.invalid/index.php?page=torrent-details&id=abc123", "https://tracker.invalid/index.php?id=abc123&page=torrent-details"},
		{"https://tracker.invalid/index.php?page=torrent-details&id=42", "https://tracker.invalid/index.php?id=42&page=torrent-details"},
		{"https://tracker.invalid/details.php?hash=abc123", "https://tracker.invalid/details.php?hash=abc123"},
		{"https://tracker.invalid/details.php?hash=" + strings.Repeat("a", 40), "https://tracker.invalid/details.php?hash=" + strings.Repeat("a", 40)},
		{"https://tracker.invalid/details.php?id=abc-123", "https://tracker.invalid/details.php?id=abc-123"},
	} {
		execution := &workflowUploadExecution{
			plan: &workflowRetainedUploadPlanFake{results: []trackers.RetainedTrackerResult{{
				Tracker: "ALPHA",
				Summary: api.UploadSummary{UploadedTorrents: []api.UploadedTorrent{{Tracker: "ALPHA", TorrentURL: tc.value}}},
			}}},
			noSeed: true,
		}
		results, err := execution.Execute(t.Context(), nil)
		if err != nil {
			t.Fatalf("execute registered upload: %v", err)
		}
		if len(results) != 1 || results[0].RemoteURL != tc.want {
			t.Errorf("registered producer page %q became %#v", tc.value, results)
		}
	}
}
