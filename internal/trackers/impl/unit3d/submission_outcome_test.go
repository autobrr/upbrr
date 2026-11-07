// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadSubmissionOutcomes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		status  int
		body    string
		outcome submission.Outcome
	}{
		{"validation rejection", 400, `{"success":false,"message":"Torrent content path too long"}`, submission.Rejected},
		{"rejection with success status", 200, `{"success":false,"message":"Invalid torrent"}`, submission.Rejected},
		{"missing discriminator", 200, `{}`, submission.Unknown},
		{"unrecognized upstream failure", 503, `<html>Service unavailable</html>`, submission.Unknown},
		{"incomplete acceptance", 200, `{"success":true,`, submission.Unknown},
		{"accepted without downloadable artifact", 200, `{"success":true,"data":""}`, submission.Accepted},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			summary, err := submitUnit3DUpload(t.Context(), "EXAMPLE", "Example.Release.2026-GRP", "", server.URL,
				server.URL+"/upload", "application/octet-stream", "torrent", api.UploadSubject{}, "", api.NopLogger{})
			if got := submission.OutcomeOf(err); got != test.outcome {
				t.Fatalf("submission outcome=%v want=%v error=%v", got, test.outcome, err)
			}
			if (summary.Uploaded == 1) != (test.outcome == submission.Accepted) || requests.Load() != 1 {
				t.Fatalf("uploaded=%d requests=%d", summary.Uploaded, requests.Load())
			}
		})
	}
}
