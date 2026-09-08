// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDuplicateLoginPreservesHTMLFailureDetail(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusForbidden, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodGet {
					_, _ = io.WriteString(w, `<form><input name="token" value="synthetic"></form>`)
					return
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `<head><script>`+strings.Repeat("private-script ", 6000)+`</script></head><div class="error">Login unavailable</div>`)
			}))
			defer server.Close()
			cookies, err := thrLogin(t.Context(), server.Client(), server.URL, "synthetic-user", "synthetic-password")
			if err == nil || len(cookies) != 0 || !strings.Contains(err.Error(), "Login unavailable") || strings.Contains(err.Error(), "private-script") {
				t.Fatalf("cookies=%v error=%v", cookies, err)
			}
		})
	}
}
