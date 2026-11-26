// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

type uploadWarningLogger struct {
	api.NopLogger
	warnings []string
}

func (l *uploadWarningLogger) Warnf(format string, args ...any) {
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
}

func TestUploadRequiresAcceptedIdentity(t *testing.T) {
	t.Parallel()
	const missingAnnounce = "Announce URL is missing from the torrent you uploaded."
	for _, test := range []struct {
		name      string
		status    int
		location  string
		body      string
		api       bool
		outcome   submission.Outcome
		truncated bool
	}{
		{"login redirect", 302, "/login", "", false, submission.Unknown, false},
		{"accepted redirect with failed body", 302, "/successfulupload?torrentID=123", "partial", false, submission.Accepted, true},
		{"web redirect is not API receipt", 302, "/successfulupload?torrentID=123", "", true, submission.Unknown, false},
		{"zero API id", 200, "", "0", true, submission.Unknown, false},
		{"error status with numeric body", 400, "", "123", true, submission.Unknown, false},
		{"missing announce rejected", 400, "", missingAnnounce, true, submission.Rejected, false},
		{"contradictory response", 400, "", `{"success":true,"torrentID":123,"message":"` + missingAnnounce + `"}`, true, submission.Unknown, false},
		{"truncated missing announce response", 400, "", missingAnnounce, true, submission.Unknown, true},
		{"server error with missing announce message", 500, "", missingAnnounce, true, submission.Unknown, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				if test.truncated {
					w.Header().Set("Content-Length", "100")
				}
				w.Header().Set("Location", test.location)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := server.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			logger := &uploadWarningLogger{}
			ctx := logging.WithOperationLogger(t.Context(), logger)
			summary, err := submitPreparedUpload(ctx, trackers.PreparationInput{
				Logger: api.NopLogger{}, TrackerConfig: config.TrackerConfig{APIUpload: test.api},
			}, uploadState{endpoint: server.URL}, client, nil, "application/octet-stream", nil, "")
			accepted := test.outcome == submission.Accepted
			if outcome := submission.OutcomeOf(err); outcome != test.outcome || (summary.Uploaded == 1) != accepted || requests.Load() != 1 {
				t.Fatalf("outcome=%v uploaded=%d requests=%d error=%v", outcome, summary.Uploaded, requests.Load(), err)
			}
			if accepted {
				if len(logger.warnings) != 1 || !strings.Contains(logger.warnings[0], "registered artifact unavailable tracker=TL") {
					t.Fatalf("execution logger missed registered artifact outcome: %v", logger.warnings)
				}
			}
		})
	}
}
