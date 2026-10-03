// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedUploadCapturesSceneNFO(t *testing.T) {
	const nfo = "  SYNTHETIC SCENE NFO\r\n  Keep spacing.\r\n"
	const description = "[code]Imported description NFO stays here[/code]"
	for _, tc := range []struct {
		name     string
		local    bool
		retained string
	}{
		{name: "local", local: true},
		{name: "retained", retained: "retained NFO"},
		{
			name:     "local precedence",
			local:    true,
			retained: "retained NFO",
		},
		{name: "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
			torrentPath := filepath.Join(tmp, "Example.torrent")
			nfoPath := filepath.Join(tmp, "Example.nfo")
			for path, content := range map[string]string{
				mediaInfoPath: "Synthetic MediaInfo",
				torrentPath:   "synthetic torrent",
				nfoPath:       nfo,
			} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			wantNFO := tc.retained
			if tc.local {
				wantNFO = nfo
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if err := r.ParseMultipartForm(4 << 20); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer func() {
					if err := r.MultipartForm.RemoveAll(); err != nil {
						t.Errorf("remove multipart temporary files: %v", err)
					}
				}()
				if got := r.FormValue("nfo"); got != wantNFO {
					t.Errorf("multipart nfo = %q, want %q", got, wantNFO)
				}
				if _, present := r.MultipartForm.Value["nfo"]; present != (wantNFO != "") {
					t.Errorf("NFO field present = %t, want %t", present, wantNFO != "")
				}
				if got := r.FormValue("description"); got != description {
					t.Errorf("description = %q, want %q", got, description)
				}
				if len(r.MultipartForm.File["file"]) != 1 || len(r.MultipartForm.File["mediainfo"]) != 1 {
					t.Error("missing existing upload attachments")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"status_code":1,"status_message":"Torrent saved as draft"}`)
			}))
			defer server.Close()
			oldBaseURL := bhdBaseURL
			bhdBaseURL = server.URL
			defer func() { bhdBaseURL = oldBaseURL }()
			input := trackers.PreparationInput{
				Tracker: "BHD",
				Intent:  trackers.PreparationIntentUpload,
				Meta: api.UploadSubject{
					SourcePath:        filepath.Join(tmp, "Example.mkv"),
					TorrentPath:       torrentPath,
					MediaInfoTextPath: mediaInfoPath,
					Identity: api.ExternalIdentity{
						TMDBID:   123,
						IMDBID:   456,
						Category: "MOVIE",
					},
					ReleaseName: "Example.2026.1080p.BluRay.DD+5.1.H264-GRP",
					Release:     api.ReleaseInfo{Resolution: "1080p"},
					Type:        "REMUX",
					Source:      "BluRay",
					Audio:       "DD+ 5.1",
				},
				Assets: &trackers.DescriptionAssets{
					Description: description,
					NFO:         tc.retained,
					Final:       true,
				},
				TrackerConfig: config.TrackerConfig{APIKey: "synthetic-token"},
				Logger:        api.NopLogger{},
			}
			if tc.local {
				input.Meta.SceneNFOPath = nfoPath
			}
			plan, failure := New().Prepare(context.Background(), input)
			if failure != nil {
				t.Fatal(failure)
			}
			preview := plan.DryRun()
			if got := preview.Payload["nfo"]; got != wantNFO {
				t.Errorf("preview nfo = %q, want %q", got, wantNFO)
			}
			if calls != 0 {
				t.Fatal("preparation submitted the upload")
			}
			if err := os.WriteFile(nfoPath, []byte("changed after preparation"), 0o600); err != nil {
				t.Fatal(err)
			}
			input.Assets.Description = "changed after preparation"
			input.Assets.NFO = "changed after preparation"
			summary, err := plan.Submit(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if summary.Uploaded != 1 || calls != 1 {
				t.Fatalf("uploaded=%d calls=%d", summary.Uploaded, calls)
			}
		})
	}
}

func TestDescriptionRetainsFramestorNFO(t *testing.T) {
	t.Parallel()
	const source = "[size=4]Synthetic release notes[/size]\r\nsecond line"
	meta := api.UploadSubject{Tag: "-FraMeSToR"}
	result, err := prepareDescription(t.Context(), trackers.PreparationInput{Meta: meta, Assets: &trackers.DescriptionAssets{Description: source}})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(source, "\r\n", "\n")
	if result.NFO != want {
		t.Fatalf("extracted NFO = %q, want %q", result.NFO, want)
	}
	if strings.Contains(result.Description, "[size=4]") {
		t.Fatal("description cleanup changed")
	}
	retained, err := prepareDescription(t.Context(), trackers.PreparationInput{Meta: meta, Assets: &trackers.DescriptionAssets{
		Description: result.Description,
		NFO:         result.NFO,
		Final:       true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if retained != result {
		t.Fatalf("final description/NFO rebuilt: %#v", retained)
	}
	nonFramestor, err := prepareDescription(t.Context(), trackers.PreparationInput{Assets: &trackers.DescriptionAssets{Description: source}})
	if err != nil {
		t.Fatal(err)
	}
	if nonFramestor.NFO != "" {
		t.Fatal("ordinary description incorrectly treated as NFO")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := prepareDescription(canceled, trackers.PreparationInput{Meta: meta}); err == nil {
		t.Fatal("canceled description preparation succeeded")
	}
}

func TestResolveNFOBoundaries(t *testing.T) {
	t.Parallel()
	const fallback = "retained NFO"
	for _, tc := range []struct {
		name, content string
		missing       bool
		want, wantErr string
	}{
		{name: "empty", want: fallback},
		{
			name:    "whitespace",
			content: " \r\n ",
			want:    fallback,
		},
		{
			name:    "missing",
			missing: true,
			wantErr: "open NFO",
		},
		{
			name:    "limit",
			content: strings.Repeat("x", maxNFOBytes),
			want:    strings.Repeat("x", maxNFOBytes),
		},
		{
			name:    "oversize",
			content: strings.Repeat("x", maxNFOBytes+1),
			wantErr: "exceeds size limit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scene.nfo")
			if !tc.missing {
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := resolveNFO(api.UploadSubject{SceneNFOPath: path}, fallback)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("NFO length=%d expected=%d err=%v", len(got), len(tc.want), err)
			}
		})
	}
	if _, err := resolveNFO(api.UploadSubject{}, strings.Repeat("x", maxNFOBytes+1)); err == nil {
		t.Fatal("oversize retained NFO accepted")
	}
}

func TestResolveNFOPrefersLocalOverOversizeFallback(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "scene.nfo")
	if err := os.WriteFile(path, []byte("local NFO"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveNFO(api.UploadSubject{SceneNFOPath: path}, strings.Repeat("x", maxNFOBytes+1))
	if err != nil || got != "local NFO" {
		t.Fatalf("local precedence: nfo=%q err=%v", got, err)
	}
}
