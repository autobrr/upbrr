// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackerLookupReplayCannotRebindImportedAcceptance(t *testing.T) {
	repo, err := db.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	const previousURL = "http://93.184.216.34/previous.png"
	const inspectedURL = "http://93.184.216.34/inspected.png"
	source := filepath.Join(t.TempDir(), "Synthetic.mkv")
	query := []any{"tracker-data-v1", "LST", "42", false, true}
	calls := 0
	fetch := func() (trackerdata.Result, error) {
		calls++
		return trackerdata.Result{
			TrackerID:                           "42",
			TMDBID:                              123456,
			Images:                              []bbcode.Image{{RawURL: inspectedURL}},
			UninspectedImages:                   []api.ImportedScreenshotReference{{RawURL: previousURL}},
			ImportedImagePolicyVersion:          api.ImportedScreenshotEvidencePolicyVersion,
			ImportedImageDescriptionFingerprint: "original-description-and-both-selected-urls",
		}, errors.New("partial tracker lookup")
	}
	meta := preparationstate.State{
		SourcePath:        source,
		SourceFingerprint: "source-fingerprint",
		Policy:            preparationstate.CollectionPolicy{KeepImages: true},
	}
	record := api.TrackerMetadata{Tracker: "LST", TrackerID: "42"}
	ctx, scope := evidence.WithScope(t.Context(), repo, source, meta.SourceFingerprint, api.ExternalFreshnessLoad, nil)
	initial, err := lookupTrackerDataEvidence(ctx, query, false, false, true, fetch)
	if err == nil || importedScreenshotEvidenceFromResult(meta, record, initial) == nil || calls != 1 {
		t.Fatalf("current partial lookup lost receipt: result=%+v calls=%d err=%v", initial, calls, err)
	}
	if err := scope.Err(); err != nil {
		t.Fatal(err)
	}

	// A new collection keeps the same source and query, as a corrected generation does.
	ctx, scope = evidence.WithScope(t.Context(), repo, source, meta.SourceFingerprint, api.ExternalFreshnessReuse, nil)
	replayed, err := lookupTrackerDataEvidence(ctx, query, false, false, true, fetch)
	if !errors.Is(err, evidence.ErrSuppressed) || calls != 1 || importedScreenshotEvidenceFromResult(meta, record, replayed) != nil {
		t.Fatalf("cached typed partial regained acceptance or fetched: result=%+v calls=%d err=%v", replayed, calls, err)
	}
	if replayed.TMDBID != initial.TMDBID || replayed.ImportedImagePolicyVersion != initial.ImportedImagePolicyVersion ||
		replayed.ImportedImageDescriptionFingerprint != initial.ImportedImageDescriptionFingerprint {
		t.Fatal("receipt expiry discarded reusable metadata or descriptor provenance")
	}

	// Descriptor provenance grants no acceptance, but an actual second image
	// request can independently earn a current receipt without repeating lookup.
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		clientConn, serverConn := net.Pipe()
		go func() {
			defer serverConn.Close()
			request, err := http.ReadRequest(bufio.NewReader(serverConn))
			if err != nil {
				return
			}
			_ = request.Body.Close()
			_, _ = io.WriteString(serverConn, "HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nContent-Length: 11\r\nConnection: close\r\n\r\nunavailable")
		}()
		return clientConn, nil
	}}
	client := trackerdata.Unit3DImageHTTPClient(&http.Client{Transport: transport})
	defer client.CloseIdleConnections()
	reference, err := attemptTrackerImageDownload(ctx, client, "LST", replayed.Images[0].RawURL,
		filepath.Join(t.TempDir(), "image.png"), 0, false, trackerdata.Unit3DImageClientSupportsReferences(client))
	if err == nil || reference != inspectedURL {
		t.Fatalf("fresh image request did not earn its own receipt: reference=%q err=%v", reference, err)
	}
	replayed.UninspectedImages = append(replayed.UninspectedImages, api.ImportedScreenshotReference{RawURL: reference})
	current := importedScreenshotEvidenceFromResult(meta, record, replayed)
	if current == nil || len(current.References) != 1 || current.References[0].RawURL != inspectedURL || calls != 1 {
		t.Fatalf("fresh image receipt mixed with old acceptance: %+v calls=%d", current, calls)
	}
	legacy := replayed
	legacy.ImportedImagePolicyVersion = ""
	if importedScreenshotEvidenceFromResult(meta, record, legacy) != nil {
		t.Fatal("fresh image request manufactured missing descriptor policy")
	}
	legacy = replayed
	legacy.ImportedImageDescriptionFingerprint = ""
	if importedScreenshotEvidenceFromResult(meta, record, legacy) != nil {
		t.Fatal("fresh image request manufactured missing descriptor fingerprint")
	}
	// A generic cached image failure cannot re-earn that reference.
	reference, err = attemptTrackerImageDownload(ctx, nil, "LST", inspectedURL,
		filepath.Join(t.TempDir(), "image.png"), 0, false, true)
	if !errors.Is(err, evidence.ErrSuppressed) || reference != "" {
		t.Fatalf("cached image failure regained receipt: reference=%q err=%v", reference, err)
	}
	if err := scope.Err(); err != nil {
		t.Fatal(err)
	}
}
