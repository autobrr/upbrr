// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/services/db"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackerImageDownloadReferenceOutcomes(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	var portraitBytes, dvdBytes bytes.Buffer
	if err := png.Encode(&portraitBytes, image.NewGray(image.Rect(0, 0, 720, 1280))); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&dvdBytes, image.NewGray(image.Rect(0, 0, 352, 576))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name           string
		status         int
		contentType    string
		body           []byte
		length         int64
		height         int
		isDVD          bool
		writeFailure   bool
		unprovenClient bool
		wantReason     string
		wantReference  bool
	}{
		{
			name:          "decode unavailable",
			body:          []byte("unavailable"),
			wantReason:    "image_decode_failed",
			wantReference: true,
		},
		{
			name:          "empty body",
			wantReason:    "empty_image",
			wantReference: true,
		},
		{
			name:           "unproven client",
			body:           []byte("unavailable"),
			unprovenClient: true,
			wantReason:     "image_decode_failed",
		},
		{
			name:          "truncated body",
			body:          []byte("incomplete"),
			length:        100,
			wantReason:    "image_read_failed",
			wantReference: true,
		},
		{
			name:          "write unavailable",
			body:          imageBytes.Bytes(),
			writeFailure:  true,
			wantReason:    "image_write_failed",
			wantReference: true,
		},
		{
			name:       "http rejected",
			status:     403,
			wantReason: "http_status_403",
		},
		{
			name:        "mime rejected",
			contentType: "text/html",
			body:        []byte("unavailable"),
			wantReason:  "non_image_content_type",
		},
		{
			name:       "declared oversized",
			length:     unit3dMaxImageBytes + 1,
			wantReason: "image_too_large",
		},
		{
			name:       "budget exhausted",
			body:       bytes.Repeat([]byte("x"), unit3dMaxImageBytes),
			wantReason: "image_decode_failed",
		},
		{
			name:       "resolution rejected",
			body:       imageBytes.Bytes(),
			height:     1080,
			wantReason: "resolution_mismatch_actual_1_expected_1080",
		},
		{
			name:   "portrait image accepted",
			body:   portraitBytes.Bytes(),
			height: 720,
		},
		{
			name:       "portrait image below required tier",
			body:       portraitBytes.Bytes(),
			height:     1080,
			wantReason: "resolution_mismatch_actual_720_expected_1080",
		},
		{
			name:   "anamorphic DVD retains physical height",
			body:   dvdBytes.Bytes(),
			height: 576,
			isDVD:  true,
		},
		{
			name:   "narrow landscape retains accepted height",
			body:   dvdBytes.Bytes(),
			height: 576,
		},
		{name: "decoded saved", body: imageBytes.Bytes()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
				clientConn, serverConn := net.Pipe()
				go func() {
					defer serverConn.Close()
					request, err := http.ReadRequest(bufio.NewReader(serverConn))
					if err != nil {
						return
					}
					_ = request.Body.Close()
					contentType := tc.contentType
					if contentType == "" {
						contentType = "image/png"
					}
					status := tc.status
					if status == 0 {
						status = http.StatusOK
					}
					length := int64(len(tc.body))
					if tc.length > 0 {
						length = tc.length
					}
					response := &http.Response{
						StatusCode:    status,
						ProtoMajor:    1,
						ProtoMinor:    1,
						Header:        http.Header{"Content-Type": {contentType}},
						ContentLength: length,
						Body:          io.NopCloser(bytes.NewReader(tc.body)),
						Close:         true,
					}
					_ = response.Write(serverConn)
				}()
				return clientConn, nil
			}}
			client := trackerdata.Unit3DImageHTTPClient(&http.Client{Transport: transport})
			defer client.CloseIdleConnections()
			allow := !tc.unprovenClient && trackerdata.Unit3DImageClientSupportsReferences(client)
			out := filepath.Join(t.TempDir(), "image.png")
			if tc.writeFailure {
				out = filepath.Join(t.TempDir(), "absent", "image.png")
			}
			const rawURL = "http://93.184.216.34/full.png"
			reason, ref := downloadImageWithReference(t.Context(), client, rawURL, out, tc.height, tc.isDVD, allow)
			if reason != tc.wantReason || (ref != "") != tc.wantReference {
				t.Fatalf("outcome reason=%q ref=%q, want %q reference=%t", reason, ref, tc.wantReason, tc.wantReference)
			}
			if ref != "" && ref != rawURL {
				t.Fatal("receipt changed accepted URL")
			}
			if tc.wantReason != "" {
				if _, err := os.Stat(out); err == nil {
					t.Fatal("failed image manufactured local artifact")
				}
			} else if saved, err := os.ReadFile(out); err != nil || !bytes.Equal(saved, tc.body) {
				t.Fatalf("accepted image not retained: %v", err)
			}
			if strings.Contains(reason, "93.184") {
				t.Fatal("diagnostic leaked URL")
			}
		})
	}
}

func TestTrackerImageDownloadCachedFailureCannotMintReceipt(t *testing.T) {
	repo, err := db.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx, scope := evidence.WithScope(t.Context(), repo, filepath.Join(t.TempDir(), "source"), "source-fingerprint", api.ExternalFreshnessLoad, nil)
	const rawURL = "http://93.184.216.34/full.png"
	query := []any{"LST", rawURL, 1080, false, trackerImageResolutionPolicyVersion}
	if err := evidence.Attempt(ctx, "tracker.image", query, func() error { return errors.New("image_decode_failed") }); err == nil {
		t.Fatal("fixture failed to retain generic failure")
	}
	// A nil client makes any unexpected re-fetch fail loudly; no request is authorized.
	reference, err := attemptTrackerImageDownload(ctx, nil, "LST", rawURL, filepath.Join(t.TempDir(), "image.png"), 1080, false, true)
	if !errors.Is(err, evidence.ErrSuppressed) || reference != "" {
		t.Fatalf("cached generic failure regained receipt: reference=%q err=%v", reference, err)
	}
	if err := scope.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerImageDownloadReevaluatesLegacyPortraitFailure(t *testing.T) {
	repo, err := db.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx, scope := evidence.WithScope(t.Context(), repo, filepath.Join(t.TempDir(), "source"), "source-fingerprint", api.ExternalFreshnessLoad, nil)
	const rawURL = "http://93.184.216.34/full.png"
	legacyQuery := []any{"LST", rawURL, 720, false}
	if err := evidence.Attempt(ctx, "tracker.image", legacyQuery, func() error {
		return errors.New("resolution_mismatch_actual_1280_expected_720")
	}); err == nil {
		t.Fatal("fixture failed to retain legacy rejection")
	}
	var payload bytes.Buffer
	if err := png.Encode(&payload, image.NewGray(image.Rect(0, 0, 720, 1280))); err != nil {
		t.Fatal(err)
	}
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": {"image/png"}},
			ContentLength: int64(payload.Len()),
			Body:          io.NopCloser(bytes.NewReader(payload.Bytes())),
		}, nil
	})}
	out := filepath.Join(t.TempDir(), "portrait.png")
	reference, err := attemptTrackerImageDownload(ctx, client, "LST", rawURL, out, 720, false, false)
	if err != nil || reference != "" || requests != 1 {
		t.Fatalf("legacy verdict suppressed corrected download: requests=%d reference=%q err=%v", requests, reference, err)
	}
	if saved, err := os.ReadFile(out); err != nil || !bytes.Equal(saved, payload.Bytes()) {
		t.Fatalf("corrected portrait image not retained: %v", err)
	}
	if err := scope.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerImageDownloadRejectedRequestsHaveNoReceipt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		url      string
		canceled bool
	}{
		{name: "nonpublic", url: "http://127.0.0.1/full.png"},
		{name: "malformed", url: "not-an-image-url"},
		{
			name:     "canceled",
			url:      "http://93.184.216.34/full.png",
			canceled: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("unexpected image request")
			}}
			client := trackerdata.Unit3DImageHTTPClient(&http.Client{Transport: transport})
			defer client.CloseIdleConnections()
			reason, reference := downloadImageWithReference(ctx, client, tc.url, filepath.Join(t.TempDir(), "image.png"), 0, false, trackerdata.Unit3DImageClientSupportsReferences(client))
			if reason == "" || reference != "" {
				t.Fatalf("rejected request retained receipt: reason=%q reference=%q", reason, reference)
			}
		})
	}
}
