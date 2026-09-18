// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
)

func TestBTNGroupOnlyUploadRetainsSuccessOnAPIError(t *testing.T) {
	t.Parallel()
	var apiCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload.php" {
			http.Redirect(w, r, "/torrents.php?id=123", http.StatusFound)
			return
		}
		if r.URL.Path == "/rpc" {
			apiCalls.Add(1)
			_, _ = fmt.Fprint(w, `{"error":{"code":-32004,"message":"private upstream detail","data":"secret-token"},"result":null}`)
			return
		}
		_, _ = fmt.Fprint(w, "torrent group")
	}))
	defer server.Close()
	req := newBTNUploadTestRequest(t)
	req, failure := trackers.PrepareInputWithReleaseNamePolicy(req, Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatal(failure)
	}
	logger := &captureBTNLogger{}
	req.Logger = logger
	summary, err := submitPreparedUpload(t.Context(), req, uploadContext{
		baseURL: server.URL,
		uploadURL: server.URL + "/upload.php",
		apiURL: server.URL + "/rpc",
		apiToken: "test-api-token",
		client: server.Client(),
	}, filepath.Join(t.TempDir(), "registered.torrent"), nil, "application/octet-stream")
	if err != nil || summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
		t.Fatalf("accepted upload must remain successful: summary=%+v err=%v", summary, err)
	}
	torrent := summary.UploadedTorrents[0]
	if torrent.TorrentURL != server.URL+"/torrents.php?id=123" || torrent.TorrentID != "" || torrent.TorrentPath != "" {
		t.Fatalf("unexpected artifact identity after failed lookup: %+v", torrent)
	}
	logs := strings.Join(logger.warnings, "\n")
	if !strings.Contains(logs, "getTorrents rejected request code=-32004") {
		t.Fatal("missing API failure diagnostic")
	}
	if strings.Contains(logs, "private upstream detail") || strings.Contains(logs, "secret-token") || strings.Contains(logs, "test-api-token") {
		t.Fatal("API diagnostic exposed private response data")
	}
	if apiCalls.Load() != 1 {
		t.Fatalf("API errors must not retry: calls=%d", apiCalls.Load())
	}
}

func TestBTNAPIVisibilityRetriesAreBounded(t *testing.T) {
	t.Parallel()
	for _, intermediate := range []bool{false, true} {
		t.Run(fmt.Sprintf("intermediate=%t", intermediate), func(t *testing.T) {
			t.Parallel()
			var apiCalls, uploadCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/upload.php":
					uploadCalls.Add(1)
					if intermediate {
						_, _ = fmt.Fprint(w, `<p>You need to download the torrent file.</p><form action="/torrents.php?id=123"></form>`)
					} else {
						http.Redirect(w, r, "/torrents.php?id=123", http.StatusFound)
					}
				case "/rpc":
					apiCalls.Add(1)
					_, _ = fmt.Fprint(w, `{"result":{"results":"0"}}`)
				default:
					http.Error(w, "detail unavailable", http.StatusInternalServerError)
				}
			}))
			defer server.Close()
			req, failure := trackers.PrepareInputWithReleaseNamePolicy(newBTNUploadTestRequest(t), Profile().ReleaseNamePolicy)
			if failure != nil {
				t.Fatal(failure)
			}
			summary, err := submitPreparedUpload(t.Context(), req, uploadContext{
				baseURL: server.URL,
				uploadURL: server.URL + "/upload.php",
				apiURL: server.URL + "/rpc",
				apiToken: "test-api-token",
				client: server.Client(),
			}, filepath.Join(t.TempDir(), "registered.torrent"), nil, "application/octet-stream")
			if err != nil || summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 || summary.UploadedTorrents[0].TorrentPath != "" {
				t.Fatalf("accepted upload must survive exhausted lookup: summary=%+v err=%v", summary, err)
			}
			if uploadCalls.Load() != 1 || apiCalls.Load() != 4 {
				t.Fatalf("unexpected retry counts: uploads=%d searches=%d", uploadCalls.Load(), apiCalls.Load())
			}
		})
	}
}

type btnCancelRetryLogger struct {
	captureBTNLogger
	cancel context.CancelFunc
}

func (l *btnCancelRetryLogger) Infof(string, ...any) {
	l.cancel()
}

func TestBTNAPIVisibilityWaitIsCancellable(t *testing.T) {
	t.Parallel()
	var apiCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiCalls.Add(1)
		_, _ = fmt.Fprint(w, `{"result":{"results":"0"}}`)
	}))
	defer server.Close()
	req, failure := trackers.PrepareInputWithReleaseNamePolicy(newBTNUploadTestRequest(t), Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatal(failure)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req.Logger = &btnCancelRetryLogger{cancel: cancel}
	_, _, err := resolveAndDownloadViaAPI(ctx, server.URL, "test-api-token", req, "123", filepath.Join(t.TempDir(), "registered.torrent"))
	if !errors.Is(err, context.Canceled) || apiCalls.Load() != 1 {
		t.Fatalf("cancellation failed: calls=%d err=%v", apiCalls.Load(), err)
	}
}

func TestBTNGroupOnlyUploadWaitsForAPIVisibility(t *testing.T) {
	t.Parallel()
	var apiSearchCalls, uploadCalls atomic.Int32
	req, failure := trackers.PrepareInputWithReleaseNamePolicy(newBTNUploadTestRequest(t), Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatal(failure)
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	handlerErrs := newHTTPHandlerErrorRecorder(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload.php":
			uploadCalls.Add(1)
			http.Redirect(w, r, "/torrents.php?id=123", http.StatusFound)
		case "/rpc":
			var rpc struct {
				Method string `json:"method"`
				Params struct {
					Key string `json:"key"`
					ID string `json:"id"`
					Search map[string]string `json:"search"`
				} `json:"params"`
			}
			if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
				handlerErrs.Errorf("decode request: %v", err)
				return
			}
			if rpc.Params.Key != "test-api-token" {
				handlerErrs.Errorf("missing named API key")
			}
			switch rpc.Method {
			case "getTorrents":
				if rpc.Params.Search["group_id"] != "123" || rpc.Params.Search["release"] != releaseName {
					handlerErrs.Errorf("incorrect upload lookup filter: %v", rpc.Params.Search)
				}
				if apiSearchCalls.Add(1) < 3 {
					_, _ = fmt.Fprint(w, `{"result":{"results":"0"}}`)
					return
				}
				_, _ = fmt.Fprintf(w, `{"result":{"results":"1","torrents":{"456":{"TorrentID":"456","GroupID":"123","ReleaseName":%q}}}}`, releaseName)
			case "getTorrentById":
				if rpc.Params.ID != "456" {
					handlerErrs.Errorf("incorrect torrent lookup ID: %q", rpc.Params.ID)
				}
				_, _ = fmt.Fprintf(w, `{"result":{"DownloadURL":"http://%s/download"}}`, r.Host)
			default:
				handlerErrs.Errorf("unexpected method: %s", rpc.Method)
			}
		case "/download":
			_, _ = w.Write(btnRegisteredTorrentFixture())
		default:
			_, _ = fmt.Fprint(w, "torrent group")
		}
	}))
	defer server.Close()
	outputPath := filepath.Join(t.TempDir(), "registered.torrent")
	summary, err := submitPreparedUpload(t.Context(), req, uploadContext{
		baseURL: server.URL,
		uploadURL: server.URL + "/upload.php",
		apiURL: server.URL + "/rpc",
		apiToken: "test-api-token",
		client: server.Client(),
	}, outputPath, nil, "application/octet-stream")
	handlerErrs.Check()
	if err != nil || summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
		t.Fatalf("upload result: %+v err=%v", summary, err)
	}
	torrent := summary.UploadedTorrents[0]
	if torrent.TorrentID != "456" || torrent.TorrentPath != outputPath || torrent.TorrentURL != server.URL+"/torrents.php?id=123&torrentid=456" {
		t.Fatalf("incorrect registered artifact: %+v", torrent)
	}
	if _, err := os.Stat(torrent.TorrentPath); err != nil {
		t.Fatalf("registered torrent unavailable for injection: %v", err)
	}
	if uploadCalls.Load() != 1 || apiSearchCalls.Load() != 3 {
		t.Fatalf("unexpected retry counts: uploads=%d searches=%d", uploadCalls.Load(), apiSearchCalls.Load())
	}
}
