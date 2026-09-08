// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestBTNAutofillResponseErrors(t *testing.T) {
	t.Parallel()

	const failureFields = `<input name="artist" value="Autofill Fail"><input name="title" value="Autofill Fail">`
	header := `<head><script>` + strings.Repeat("private-script ", 6000) + `</script></head>`
	for _, testCase := range []struct {
		name        string
		status      int
		body        string
		lookup      string
		want        string
		wantFailure bool
		partial     bool
	}{
		{
			name:        "tvdb explicit failure with late message",
			status:      http.StatusOK,
			body:        header + failureFields + `<div class="error">Episode lookup unavailable</div>`,
			lookup:      "tvdb",
			want:        "Episode lookup unavailable",
			wantFailure: true,
		},
		{
			name:        "release name explicit failure",
			status:      http.StatusOK,
			body:        failureFields + `<div role="alert">No matching release found</div>`,
			lookup:      "release_name",
			want:        "No matching release found",
			wantFailure: true,
		},
		{
			name:   "http error beyond preview",
			status: http.StatusServiceUnavailable,
			body:   header + `<div class="errors">Metadata service unavailable</div>`,
			lookup: "tvdb",
			want:   "Metadata service unavailable",
		},
		{
			name:   "invalid form retains reason and redacts decoded secret",
			status: http.StatusOK,
			body:   `<div class="error">Invalid passkey&#61;synthetic-secret</div><textarea>private-description</textarea>`,
			lookup: "tvdb",
			want:   "Invalid passkey=[REDACTED]",
		},
		{
			name:   "oversized response does not become a valid form",
			status: http.StatusOK,
			body:   `<div class="error">Response too large</div><!--` + strings.Repeat("x", 1<<20),
			lookup: "tvdb",
			want:   "response exceeded 1 MiB",
		},
		{
			name:    "partial success-status response retains diagnostic",
			status:  http.StatusOK,
			body:    `<div class="error">Metadata response interrupted</div>`,
			lookup:  "tvdb",
			want:    "Metadata response interrupted",
			partial: true,
		},
		{
			name:    "partial HTTP error retains diagnostic",
			status:  http.StatusServiceUnavailable,
			body:    `<div class="error">Metadata response interrupted</div>`,
			lookup:  "release_name",
			want:    "Metadata response interrupted",
			partial: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if testCase.partial {
					w.Header().Set("Content-Length", strconv.Itoa(len(testCase.body)+1))
				}
				w.WriteHeader(testCase.status)
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()
			payload := url.Values{"scene_yesno": {"No"}}
			if testCase.lookup == "release_name" {
				payload.Set("scene_yesno", "Yes")
			}
			fields, err := requestBTNAutofillFields(t.Context(), uploadContext{client: server.Client(), uploadURL: server.URL}, payload, "Episode")
			if err == nil || fields != nil || !strings.Contains(err.Error(), testCase.want) || !strings.Contains(err.Error(), "lookup="+testCase.lookup) {
				t.Fatalf("fields=%v error=%v, want %q with lookup=%s", fields, err, testCase.want, testCase.lookup)
			}
			if errors.Is(err, errBTNExplicitAutofillFailure) != testCase.wantFailure {
				t.Fatalf("explicit failure classification changed: %v", err)
			}
			if !strings.Contains(err.Error(), "status="+strconv.Itoa(testCase.status)) {
				t.Fatalf("response lost HTTP status: %v", err)
			}
			if testCase.partial && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("partial response lost read cause: %v", err)
			}
			for _, privateValue := range []string{"private-script", "private-description", "synthetic-secret"} {
				if strings.Contains(err.Error(), privateValue) {
					t.Fatalf("response exposed excluded content: %v", err)
				}
			}
		})
	}
}

func TestBTNPreparationPreservesAutofillErrorDetail(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<input name="artist" value="Autofill Fail"><div class="error">Series mapping unavailable</div>`)
	}))
	defer server.Close()
	req := newBTNDryRunTestRequest(t, "")
	_, err := prepareUploadData(t.Context(), req, uploadContext{client: server.Client(), uploadURL: server.URL})
	if !errors.Is(err, errBTNExplicitAutofillFailure) || !strings.Contains(err.Error(), "Series mapping unavailable") {
		t.Fatalf("preparation lost tracker response detail: %v", err)
	}
}
