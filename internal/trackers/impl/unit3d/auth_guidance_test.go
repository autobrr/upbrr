// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

type guidanceLogger struct {
	api.NopLogger
	messages []string
}

func (l *guidanceLogger) Infof(format string, args ...any) {
	if message := fmt.Sprintf(format, args...); strings.Contains(message, "guidance:") {
		l.messages = append(l.messages, message)
	}
}

func TestAPIKeyGuidanceReporterDeduplicatesWithinEachUpload(t *testing.T) {
	t.Parallel()
	guidance := &trackers.APIKeyGuidance{Header: "X-Api-Key-Expires-At", Message: func(expiry trackers.APIKeyExpiry, _ time.Time) string {
		if !expiry.Reported {
			return "Review the account configuration."
		}
		return "Renew the account key."
	}}
	logger := &guidanceLogger{}
	input := trackers.PreparationInput{
		Tracker:       "EXAMPLE",
		TrackerConfig: config.TrackerConfig{APIKey: "synthetic-key"},
		Logger:        logger,
	}
	for range 2 {
		report := apiKeyGuidanceReporter(input, guidance)
		for range 3 {
			report(nil, http.StatusOK)
		}
	}
	if len(logger.messages) != 2 {
		t.Fatal("repeated responses duplicated advice or later upload lost it")
	}
	input.Projection = &api.TrackerReleaseProjection{PolicyDecisions: []api.TrackerPolicyDecision{{
		Code: trackers.APIKeyGuidanceCode, Message: "Review the account configuration.",
	}}}
	report := apiKeyGuidanceReporter(input, guidance)
	report(nil, 0)
	if len(logger.messages) != 2 {
		t.Fatal("shared projection advice was repeated in upload logs")
	}
	header := make(http.Header)
	header.Set(guidance.Header, "2026-11-20T12:00:00Z")
	for range 3 {
		report(header, http.StatusOK)
	}
	if len(logger.messages) != 3 {
		t.Fatal("newer response guidance was lost or duplicated")
	}
}

func TestUploadResponseCapturesExpiryWithoutChangingSubmissionOutcome(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		outcome submission.Outcome
	}{
		{"accepted", http.StatusOK, `{"success":true}`, submission.Accepted},
		{"rejected with expiry", http.StatusBadRequest, `{"success":false,"message":"Invalid torrent"}`, submission.Rejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guidance := &trackers.APIKeyGuidance{Header: "X-Api-Key-Expires-At", Message: func(expiry trackers.APIKeyExpiry, _ time.Time) string {
				return expiry.ExpiresAt.UTC().Format(time.RFC3339)
			}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(guidance.Header, "2026-11-20T12:00:00Z")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			logger := &guidanceLogger{}
			report := apiKeyGuidanceReporter(trackers.PreparationInput{
				Tracker:       "EXAMPLE",
				TrackerConfig: config.TrackerConfig{APIKey: "synthetic-key"},
				Logger:        logger,
			}, guidance)
			summary, err := submitUnit3DUpload(t.Context(), "EXAMPLE", "Example.Release.2026-GRP", "synthetic-key", server.URL,
				server.URL, "text/plain", "synthetic-payload", api.UploadSubject{}, t.TempDir(), logger, report)
			if submission.OutcomeOf(err) != tc.outcome || (summary.Uploaded == 1) != (tc.outcome == submission.Accepted) {
				t.Fatal("advisory changed the existing submission result")
			}
			if len(logger.messages) != 1 || !strings.Contains(logger.messages[0], "2026-11-20") {
				t.Fatal("upload response expiry was not presented")
			}
		})
	}
}
