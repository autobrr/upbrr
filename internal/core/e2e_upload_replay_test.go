// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build e2e

package core

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type e2eReplayReporter struct {
	alreadySucceeded bool
	completions      int
}

func (r *e2eReplayReporter) Begin(context.Context, api.WorkflowExternalEffect) (api.WorkflowExternalEffectReceipt, error) {
	return api.WorkflowExternalEffectReceipt{EffectID: "submission", AlreadySucceeded: r.alreadySucceeded}, nil
}

func (r *e2eReplayReporter) Complete(context.Context, api.WorkflowExternalEffectReceipt, bool) error {
	r.completions++
	return nil
}

func TestE2EUploadReconcilesSucceededReceipt(t *testing.T) {
	for _, replay := range []bool{false, true} {
		name := "new submission"
		if replay {
			name = "succeeded receipt"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dbPath := filepath.Join(root, "history.sqlite")
			repo, err := db.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			payload := []byte("d4:infod6:lengthi1e4:name1:x12:piece lengthi1e6:pieces20:00000000000000000000ee")
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/upload":
					posts.Add(1)
					w.WriteHeader(http.StatusCreated)
				case "/download/e2e-123":
					_, _ = w.Write(payload)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			reporter := &e2eReplayReporter{alreadySucceeded: replay}
			ctx := api.WithWorkflowExternalEffectReporter(t.Context(), reporter)
			source := filepath.Join(root, "Example.Movie.2026.mkv")
			service := e2eTrackerService{
				endpoint: server.URL,
				repo:     repo,
				dbPath:   dbPath,
			}
			summary, err := service.Upload(ctx, api.UploadSubject{SourcePath: source, Trackers: []string{"ALPHA"}})
			if err != nil {
				t.Fatal(err)
			}
			wantPosts := int32(1)
			if replay {
				wantPosts = 0
			}
			if posts.Load() != wantPosts || reporter.completions != int(wantPosts) {
				t.Fatalf("posts=%d completions=%d, want %d", posts.Load(), reporter.completions, wantPosts)
			}
			if summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
				t.Fatalf("submission summary = %#v", summary)
			}
			registered := summary.UploadedTorrents[0]
			if registered.Tracker != "ALPHA" || registered.TorrentURL != server.URL+"/torrent/e2e-123" || registered.DownloadURL != server.URL+"/download/e2e-123" {
				t.Fatalf("registered torrent = %#v", registered)
			}
			stored, err := os.ReadFile(registered.TorrentPath)
			if err != nil || !bytes.Equal(stored, payload) {
				t.Fatalf("registered artifact bytes=%q err=%v", stored, err)
			}
			history, err := repo.ListUploadHistoryByPath(t.Context(), source)
			if err != nil || len(history) != 1 || history[0].Status != "uploaded" {
				t.Fatalf("history=%#v err=%v", history, err)
			}
		})
	}
}
