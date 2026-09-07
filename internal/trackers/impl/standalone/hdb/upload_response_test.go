// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
)

func TestSubmitPreparedUploadErrorResponse(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "errors beyond old header limit",
			body: `<html><head><title>Upload :: HDBits</title>` + strings.Repeat(`<link href="/favicon">`, 100) +
				`</head><body><nav>Browse torrents</nav><p>Upload form</p><div class="errors"><ul>` +
				`<li>Invalid <b>category</b>.</li><li>IMDb &amp; TVDB IDs are required.</li></ul></div></body></html>`,
			want: "Invalid category . IMDb & TVDB IDs are required.",
		},
		{
			name: "unmarked error page",
			body: `<html><head><title>Error</title></head><body><h1>Upload failed!</h1>` +
				`<table><tr><td>A torrent with this infohash already exists.</td></tr></table></body></html>`,
			want: "Upload failed! A torrent with this infohash already exists.",
		},
		{
			name: "alert excludes controls and executable content",
			body: `<p>Upload</p><div role="alert">Invalid category<input value="private-form-value">` +
				`<textarea>private-description</textarea><script>private-script</script><style>private-style</style>` +
				`<span hidden>private-hidden</span><span aria-hidden="true">private-aria</span></div>`,
			want: "Invalid category",
		},
		{
			name: "secrets stay redacted",
			body: `<div class="error">Invalid passkey=synthetic-secret</div>`,
			want: "Invalid passkey=[REDACTED]",
		},
		{
			name: "empty response",
			want: "no readable error message in upload response",
		},
		{
			name: "oversized response",
			body: `<div class="error">Invalid category</div><!--` + strings.Repeat("x", 1<<20),
			want: "response exceeded 1 MiB; Invalid category",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			summary, err := submitPreparedUpload(t.Context(), trackers.PreparationInput{}, preparedUploadState{
				uploadURL: server.URL + hdbUploadPath,
				client: server.Client(),
			})
			want := "trackers: HDB upload failed status=200 url=" + server.URL + hdbUploadPath + ": " + tt.want
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
			if summary.Uploaded != 0 || len(summary.UploadedTorrents) != 0 {
				t.Fatalf("failed response returned registration authority: %+v", summary)
			}
		})
	}
}
