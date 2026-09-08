// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial/authfixture"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSearchPreservesHTMLFailureDetail(t *testing.T) {
	t.Parallel()
	dbPath := newFLAuthTestDB(t)
	authfixture.Write(t, dbPath)
	if err := cookies.SaveTrackerCookieMap(t.Context(), dbPath, "FL", map[string]string{"session": "synthetic"}); err != nil {
		t.Fatalf("save cookies: %v", err)
	}
	searcher := dupeSearcher{
		cfg: config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := `<head><script>` + strings.Repeat("private-script ", 6000) + `</script></head><div class="error">Search unavailable</div>`
			return &http.Response{
StatusCode: http.StatusServiceUnavailable,
 Body: io.NopCloser(strings.NewReader(body)),
 Header: make(http.Header),
 Request: req,
}, nil
		})},
	}
	result := searcher.Search(t.Context(), api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
	if result.Disposition() != dupe.DispositionFailed || result.Code() != dupe.FailureResponseStatus || result.Cause() == nil {
		t.Fatalf("search classification changed: disposition=%v code=%s cause=%v", result.Disposition(), result.Code(), result.Cause())
	}
	if !strings.Contains(result.Cause().Error(), "Search unavailable") || strings.Contains(result.Cause().Error(), "private-script") {
		t.Fatalf("search lost safe response detail: %v", result.Cause())
	}
}
