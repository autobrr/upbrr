// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

type connectionRoundTripper func(*http.Request) (*http.Response, error)

func (fn connectionRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestStandaloneCookieProbesUseProtectedFormsWithoutSubmitting(t *testing.T) {
	t.Parallel()
	registry := MustNewRegistry()
	for _, tc := range []struct{ name, host, form string }{
		{"BJS", "bj-share.info", `<input name="auth" value="synthetic-private-token">`},
		{"BT", "brasiltracker.org", `<input name="auth" value="synthetic-private-token">`},
		{"HDT", "hd-torrents.me", `<input name="csrfToken" value="synthetic-private-token">`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			descriptor, ok := registry.LookupDescriptor(tc.name)
			if !ok || descriptor.ConnectionProbe.Test == nil || !descriptor.ConnectionProbe.StoredCookies {
				t.Fatal("missing session probe binding")
			}
			for _, tcBody := range []struct {
				body    string
				success bool
			}{
				{tc.form, true},
				{strings.Replace(tc.form, "synthetic-private-token", "   ", 1), false},
				{`<input name="username"><input name="password">`, false},
				{`<html>generic availability page</html>`, false},
			} {
				requests := 0
				client := &http.Client{Transport: connectionRoundTripper(func(r *http.Request) (*http.Response, error) {
					requests++
					cookie, err := r.Cookie("session")
					if r.Method != http.MethodGet || r.URL.Host != tc.host || r.URL.Path != "/upload.php" || err != nil || cookie.Value != "synthetic-session" || r.Body != nil {
						t.Error("unexpected operation or session")
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Set-Cookie": {"session=must-not-persist"}},
						Body:       io.NopCloser(strings.NewReader(tcBody.body)),
					}, nil
				})}
				input := trackers.ConnectionProbeInput{Cookies: map[string]string{"session": "synthetic-session"}}
				got := descriptor.ConnectionProbe.Test(context.Background(), input, client)
				if (got.Code == "success") != tcBody.success || requests != 1 || input.Cookies["session"] != "synthetic-session" {
					t.Fatalf("result=%#v requests=%d", got, requests)
				}
				if strings.Contains(got.Message, "synthetic-") {
					t.Fatal("private values leaked")
				}
			}
		})
	}
}

func TestAPIConnectionProbesUseOnlyBoundedReadContracts(t *testing.T) {
	t.Parallel()
	type responseSample struct {
		body    string
		success bool
	}
	for _, tc := range []struct {
		name, host, path, method, header, queryKey, response, rejected string
	}{
		{"ANT", "anthelion.me", "/api.php", http.MethodGet, "X-Api-Key", "", `{"response":{"offset":0,"total":0},"item":[]}`, `{"error":"synthetic-private-error","response":{"offset":0,"total":0},"item":[]}`},
		{"BHD", "beyond-hd.me", "/api/torrents/synthetic-key", http.MethodPost, "", "", `{"status_code":1,"success":true,"results":[]}`, `{"status_code":1,"success":false,"results":[]}`},
		{"DC", "digitalcore.club", "/api/v1/torrents/dupe-search", http.MethodGet, "X-Api-Key", "", `{"results":[],"index":0,"limit":1,"count":0,"total":0,"includesPending":false}`, `{"results":[],"index":0,"limit":1,"count":0,"total":0}`},
		{"GPW", "greatposterwall.com", "/api.php", http.MethodGet, "", "api_key", `{"status":200,"response":[]}`, `{"status":403,"response":[]}`},
		{"NBL", "nebulance.io", "/api.php", http.MethodGet, "", "api_key", `{"current_page":0,"total_pages":0,"count":0,"total_results":0,"items":[]}`, `{"error":"synthetic-private-error","current_page":0,"total_pages":0,"count":0,"total_results":0,"items":[]}`},
		{"SPD", "speedapp.io", "/api/torrent", http.MethodGet, "Authorization", "", `[]`, `null`},
		{"HDB", "hdbits.org", "/api/torrents", http.MethodPost, "", "", `{"status":0,"data":[]}`, `{"status":5,"data":[]}`},
		{"BTN", "api.broadcasthe.net", "/", http.MethodPost, "", "", `{"result":{"results":0,"torrents":{}}}`, `{"error":{"code":5},"result":{"results":0,"torrents":{}}}`},
		{"PTP", "passthepopcorn.me", "/torrents.php", http.MethodGet, "ApiKey", "", `{"Movies":[],"TotalResults":0,"Page":1}`, `{"error":"synthetic-private-error","Movies":[]}`},
		{"RTF", "retroflix.club", "/api/torrent", http.MethodGet, "Authorization", "", `[]`, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			descriptor, ok := MustNewRegistry().LookupDescriptor(tc.name)
			if !ok || descriptor.ConnectionProbe.Test == nil || descriptor.ConnectionProbe.StoredCookies {
				t.Fatal("missing API-only probe binding")
			}
			samples := []responseSample{
				{tc.response, true},
				{tc.rejected, false},
				{`{"error":"synthetic-private-error"}`, false},
				{`<html>generic availability page</html>`, false},
			}
			if tc.name == "BHD" {
				samples = append(samples, responseSample{`{"status_code":"1","success":1,"results":[]}`, true}, responseSample{`{"status_code":1,"success":"true","results":[]}`, true}, responseSample{`{"status_code":1,"success":null,"results":[]}`, false})
			}
			if tc.name == "HDB" {
				samples = append(samples, responseSample{`{"status":"0","data":[]}`, true})
			}
			for _, sample := range samples {
				requests := 0
				client := &http.Client{Transport: connectionRoundTripper(func(req *http.Request) (*http.Response, error) {
					requests++
					if req.Method != tc.method || req.URL.Host != tc.host || req.URL.Path != tc.path {
						t.Error("unexpected provider operation")
					}
					expectedKey := "synthetic-key"
					if tc.name == "PTP" {
						expectedKey = "synthetic-ptp-key"
					}
					if tc.header != "" && req.Header.Get(tc.header) != expectedKey {
						t.Error("wrong draft header credential")
					}
					if tc.queryKey != "" && req.URL.Query().Get(tc.queryKey) != "synthetic-key" {
						t.Error("wrong draft query credential")
					}
					switch tc.name {
					case "ANT":
						if req.URL.Query().Get("t") != "search" || req.URL.Query().Get("imdbid") != "0133093" || req.URL.Query().Get("limit") != "1" {
							t.Error("ANT did not bound a production read query")
						}
					case "BHD":
						var payload map[string]any
						if json.NewDecoder(req.Body).Decode(&payload) != nil || payload["action"] != "search" || payload["categories"] != "Movies" || payload["imdb_id"] != "tt0133093" {
							t.Error("BHD attempted a different API action")
						}
					case "DC":
						if req.URL.Query().Get("index") != "0" || req.URL.Query().Get("limit") != "1" || req.URL.Query().Get("releaseName") != "upbrr.connection.test.synthetic" {
							t.Error("DC did not bound its read")
						}
					case "NBL":
						if req.URL.Query().Get("action") != "search" || req.URL.Query().Get("release") != "upbrr.connection.test.synthetic" || req.URL.Query().Get("per_page") != "1" {
							t.Error("NBL did not bound its read")
						}
					case "SPD":
						if req.URL.Query().Get("search") != "upbrr.connection.test.synthetic" {
							t.Error("SPD changed the read query")
						}
					case "HDB":
						var payload map[string]any
						if json.NewDecoder(req.Body).Decode(&payload) != nil || payload["username"] != "synthetic-user" || payload["passkey"] != "synthetic-passkey" || payload["search"] != "upbrr.connection.test.synthetic" || payload["limit"] != float64(1) || payload["page"] != float64(0) {
							t.Error("HDB did not use entered read credentials")
						}
					case "BTN":
						var payload struct {
							Method string
							Params struct {
								Key             string
								Results, Offset int
								Search          map[string]string
							}
						}
						if json.NewDecoder(req.Body).Decode(&payload) != nil || payload.Method != "getTorrents" || payload.Params.Key != "synthetic-key" || payload.Params.Results != 1 || payload.Params.Offset != 0 || payload.Params.Search["search"] != "upbrr.connection.test.synthetic" {
							t.Error("BTN attempted a different API operation")
						}
					case "PTP":
						if req.Header.Get("Apiuser") != "synthetic-ptp-user" || req.URL.Query().Get("imdb") != "0133093" || req.URL.Query().Get("json") != "noredirect" {
							t.Error("PTP did not use the entered API identity")
						}
					case "RTF":
						if req.URL.Query().Get("search") != "upbrr.connection.test.synthetic" {
							t.Error("RTF changed its read query")
						}
					case "GPW":
						if req.URL.Query().Get("action") != "torrent" || req.URL.Query().Get("imdbID") != "tt0133093" {
							t.Error("GPW attempted a different API action")
						}
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sample.body))}, nil
				})}
				got := descriptor.ConnectionProbe.Test(t.Context(), trackers.ConnectionProbeInput{Config: config.TrackerConfig{
					APIKey:     "synthetic-key",
					Username:   "synthetic-user",
					Passkey:    "synthetic-passkey",
					PTPAPIUser: "synthetic-ptp-user",
					PTPAPIKey:  "synthetic-ptp-key",
				}}, client)
				if (got.Code == "success") != sample.success || requests != 1 {
					t.Fatalf("response=%#v requests=%d", got, requests)
				}
				if strings.Contains(got.Message, "synthetic-") {
					t.Fatal("private values leaked")
				}
			}
		})
	}
}

func TestDraftLoginProbesDoNotReuseOrPersistStoredSessions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, host, login, protected, marker string }{
		{"AR", "alpharatio.cc", "/login.php", "/torrents.php", `<a href="logout.php?auth=synthetic-auth">Logout</a>`},
		{"FF", "www.funfile.org", "/takelogin.php", "/upload.php", `<a href="friends.php">Friends</a>`},
		{"FL", "filelist.io", "/takelogin.php", "/index.php", `<a href="logout.php">Logout</a>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			descriptor, ok := MustNewRegistry().LookupDescriptor(tc.name)
			if !ok || descriptor.ConnectionProbe.Test == nil {
				t.Fatal("missing login probe")
			}
			for _, mode := range []string{"stored", "draft", "rejected", "partial", "login page"} {
				t.Run(mode, func(t *testing.T) {
					posts, reads := 0, 0
					client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: connectionRoundTripper(func(req *http.Request) (*http.Response, error) {
						if req.URL.Host != tc.host {
							t.Error("credential destination changed")
						}
						response := &http.Response{
							StatusCode: http.StatusOK,
							Header:     make(http.Header),
							Body:       io.NopCloser(strings.NewReader(tc.marker)),
						}
						switch {
						case tc.name == "FL" && req.Method == http.MethodGet && req.URL.Path == "/login.php":
							response.Body = io.NopCloser(strings.NewReader(`<input name="validator" value="synthetic-validator">`))
						case req.Method == http.MethodPost:
							posts++
							if req.URL.Path != tc.login || req.ParseForm() != nil || req.PostForm.Get("username") != "synthetic-user" || req.PostForm.Get("password") != "synthetic-password" {
								t.Error("wrong draft login form")
							}
							if req.Header.Get("Cookie") != "" {
								t.Error("fresh login reused stored cookies")
							}
							if tc.name == "FL" && req.PostForm.Get("validator") != "synthetic-validator" {
								t.Error("FL validator missing")
							}
							if mode == "rejected" {
								response.StatusCode = http.StatusUnauthorized
							} else {
								response.StatusCode = http.StatusFound
								response.Header.Set("Location", "https://must-not-follow.invalid/")
								response.Header.Set("Set-Cookie", "session=synthetic-fresh; Path=/; Secure")
							}
						case req.Method == http.MethodGet && req.URL.Path == tc.protected:
							reads++
							expected := "synthetic-fresh"
							if mode == "stored" || mode == "login page" {
								expected = "synthetic-stored"
							}
							cookie, err := req.Cookie("session")
							if err != nil || cookie.Value != expected {
								t.Error("wrong session used for protected read")
							}
							if mode == "login page" {
								response.Body = io.NopCloser(strings.NewReader(`<form action="login.php"><input name="password"></form>`))
							}
						default:
							t.Error("unexpected operation")
						}
						return response, nil
					})}
					input := trackers.ConnectionProbeInput{Cookies: map[string]string{"session": "synthetic-stored"}}
					if mode == "draft" || mode == "rejected" || mode == "partial" {
						input.Config.Username = "synthetic-user"
						if mode != "partial" {
							input.Config.Password = "synthetic-password"
						}
					}
					got := descriptor.ConnectionProbe.Test(t.Context(), input, client)
					wantSuccess := mode == "draft" || mode == "stored"
					if (got.Code == "success") != wantSuccess || input.Cookies["session"] != "synthetic-stored" || strings.Contains(got.Message, "synthetic-") {
						t.Fatalf("unexpected outcome %#v", got)
					}
					if (mode == "rejected" || mode == "partial") && reads != 0 {
						t.Fatal("failed login fell back to stored session")
					}
					if mode == "draft" && (posts != 1 || reads != 1) {
						t.Fatalf("posts=%d protected reads=%d", posts, reads)
					}
				})
			}
		})
	}
}

func TestReadOnlyCookieProbesRequireAuthenticatedResponses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, host, path, valid string }{
		{"HDS", "hd-space.org", "/index.php", `<a href="logout.php">Logout</a>`},
		{"PTS", "www.ptskit.org", "/index.php", `<a href="logout.php">Logout</a>`},
		{"IS", "immortalseed.me", "/browse.php", `<a href="logout.php">Logout</a>`},
		{"TL", "www.torrentleech.org", "/torrents/browse/list/query/upbrr.connection.test.synthetic", `{"torrentList":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			descriptor, ok := MustNewRegistry().LookupDescriptor(tc.name)
			if !ok || descriptor.ConnectionProbe.Test == nil || !descriptor.ConnectionProbe.StoredCookies {
				t.Fatal("missing cookie probe")
			}
			for _, body := range []string{tc.valid, `<form action="login.php"><input name="password"></form>`, `<html>Generic availability page</html>`, `{}`} {
				calls := 0
				client := &http.Client{Transport: connectionRoundTripper(func(req *http.Request) (*http.Response, error) {
					calls++
					cookie, err := req.Cookie("session")
					if req.Method != http.MethodGet || req.URL.Host != tc.host || req.URL.Path != tc.path || req.Body != nil || err != nil || cookie.Value != "synthetic-session" {
						t.Error("wrong read or cookie destination")
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(body)),
						Header:     http.Header{"Set-Cookie": {"session=must-not-save"}},
					}, nil
				})}
				input := trackers.ConnectionProbeInput{Config: config.TrackerConfig{Passkey: "synthetic-must-not-send"}, Cookies: map[string]string{"session": "synthetic-session"}}
				got := descriptor.ConnectionProbe.Test(t.Context(), input, client)
				if (got.Code == "success") != (body == tc.valid) || calls != 1 || input.Cookies["session"] != "synthetic-session" || strings.Contains(got.Message, "synthetic-") {
					t.Fatalf("unsafe or unexpected outcome: %#v", got)
				}
			}
		})
	}
}
