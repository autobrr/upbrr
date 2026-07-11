// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestLookupMediaCodeSupportsTVDBOnlyTV(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ajax/movies/2" || r.URL.Query().Get("term") != "456789" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"77","tvdb":"456789"}]}`)
	}))
	t.Cleanup(server.Close)

	result, err := lookupMediaCode(context.Background(), siteDefinition{Name: "AZ", BaseURL: server.URL}, sessionState{client: server.Client()}, api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV", TVDBID: 456789},
	})
	if err != nil {
		t.Fatalf("lookup TVDB media: %v", err)
	}
	if result.MediaCode != "77" || result.Missing {
		t.Fatalf("unexpected lookup result: %#v", result)
	}
}
