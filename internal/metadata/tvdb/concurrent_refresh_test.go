package tvdb

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentUnauthorizedRefresh(t *testing.T) {
	for _, firstRefreshFails := range []bool{false, true} {
		name := "reuse successful refresh"
		if firstRefreshFails {
			name = "recover after failed refresh"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			firstSent := make(chan struct{})
			secondSent := make(chan struct{})
			releaseSecond := make(chan struct{})
			var logins atomic.Int32
			client := NewClient(&http.Client{Transport: tvdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/login" {
					n := logins.Add(1)
					if (firstRefreshFails && n == 1) || (!firstRefreshFails && n > 1) {
						return tvdbTestResponse(http.StatusServiceUnavailable, ""), nil
					}
					return tvdbTestResponse(http.StatusOK, `{"data":{"token":"fresh-token"}}`), nil
				}
				if req.Header.Get("Authorization") == "Bearer old-token" {
					var ready <-chan struct{}
					if req.URL.Path == "/first" {
						close(firstSent)
						ready = secondSent
					} else {
						close(secondSent)
						ready = releaseSecond
					}
					select {
					case <-ready:
						return tvdbTestResponse(http.StatusUnauthorized, ""), nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				if req.Header.Get("Authorization") != "Bearer fresh-token" {
					t.Error("metadata retry did not use a valid refreshed token")
					return tvdbTestResponse(http.StatusUnauthorized, ""), nil
				}
				return tvdbTestResponse(http.StatusOK, `{}`), nil
			})}, nil, "synthetic-key", "")
			client.baseURL = "https://tvdb.test"
			client.authToken = "old-token"
			firstResult := make(chan error, 1)
			secondResult := make(chan error, 1)
			go func() {
				var target map[string]any
				firstResult <- client.uncachedJSON(ctx, "/first", nil, &target)
			}()
			select {
			case <-firstSent:
			case <-ctx.Done():
				t.Fatal("first request did not start")
			}
			go func() {
				var target map[string]any
				secondResult <- client.uncachedJSON(ctx, "/second", nil, &target)
			}()
			firstErr := <-firstResult
			// Deliver the other old-token 401 after the first refresh has finished.
			close(releaseSecond)
			secondErr := <-secondResult
			if (firstErr != nil) != firstRefreshFails || secondErr != nil {
				t.Errorf("unexpected request results: first=%v second=%v", firstErr, secondErr)
			}
			wantLogins := int32(1)
			if firstRefreshFails {
				wantLogins = 2
			}
			if logins.Load() != wantLogins {
				t.Errorf("login attempts=%d, want %d", logins.Load(), wantLogins)
			}
			if client.token() != "fresh-token" {
				t.Error("the successful refreshed token was not retained")
			}
		})
	}
}
