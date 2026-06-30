// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateFFStoredCookiesReadsFullSuccessBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("a", authPreviewBytes+32) + "friends.php"))
	}))
	defer server.Close()

	err := validateFFStoredCookies(context.Background(), server.URL, []*http.Cookie{{Name: "session", Value: "ok"}})
	if err != nil {
		t.Fatalf("expected marker beyond preview cap to validate session: %v", err)
	}
}
