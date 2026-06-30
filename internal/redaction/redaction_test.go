// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package redaction

import "testing"

func TestRedactValueURLPatterns(t *testing.T) {
	t.Parallel()

	input := "https://tracker.example/0123456789abcdef/announce?passkey=secret&token=abc&info_hash=deadbeef&authkey=private&auth-key=private2&apiKey=api-secret&api-token=api-token-secret&rss_key=rss-secret&torrent-pass=torrent-secret&AntiCsrfToken=csrf-secret&uid=123"
	output := RedactValue(input, nil)

	if output == input {
		t.Fatalf("expected redaction, got %q", output)
	}
	if contains(output, "0123456789abcdef") {
		t.Fatalf("expected passkey redacted, got %q", output)
	}
	for _, secret := range []string{"secret", "token=abc", "authkey=private", "auth-key=private2", "api-secret", "api-token-secret", "rss-secret", "torrent-secret", "csrf-secret", "uid=123"} {
		if contains(output, secret) {
			t.Fatalf("expected query param %q redacted, got %q", secret, output)
		}
	}
	if !contains(output, "apiKey=[REDACTED]") || !contains(output, "auth-key=[REDACTED]") || !contains(output, "torrent-pass=[REDACTED]") {
		t.Fatalf("expected query params redacted, got %q", output)
	}
}

func TestRedactValueAnnouncePathToken(t *testing.T) {
	t.Parallel()

	input := "https://tracker.example/announce/0123456789abcdef"
	output := RedactValue(input, nil)

	if contains(output, "0123456789abcdef") {
		t.Fatalf("expected announce path token redacted, got %q", output)
	}
}

func TestRedactValueTrackerLookupRequestErrors(t *testing.T) {
	t.Parallel()

	input := "trackerdata: bhd request: Post \"https://beyond-hd.me/api/torrents/bhdSecretKey123\": dial tcp timeout; unit3d: request: Get \"https://aither.cc/api/torrents/filter?api_token=aitherSecretKey123&file_name=Release.Name\": context deadline exceeded"
	output := RedactValue(input, nil)

	if contains(output, "bhdSecretKey123") || contains(output, "aitherSecretKey123") {
		t.Fatalf("expected request error secrets redacted, got %q", output)
	}
	if !contains(output, "/api/torrents/[REDACTED]") || !contains(output, "api_token=[REDACTED]") {
		t.Fatalf("expected redacted request error shape preserved, got %q", output)
	}
}

func TestRedactValueProxyPath(t *testing.T) {
	t.Parallel()

	input := "https://example.com/proxy/secret/api/v2/torrents"
	output := RedactValue(input, nil)

	if contains(output, "/proxy/secret/") {
		t.Fatalf("expected proxy secret redacted, got %q", output)
	}
}

func TestRedactValueBareProxyPath(t *testing.T) {
	t.Parallel()

	input := "clients: connecting to qbit http://127.0.0.1:7476/proxy/secret"
	output := RedactValue(input, nil)

	if contains(output, "/proxy/secret") {
		t.Fatalf("expected bare proxy secret redacted, got %q", output)
	}
	if !contains(output, "/proxy/[REDACTED]") {
		t.Fatalf("expected proxy path shape preserved, got %q", output)
	}
}

func TestRedactValuePlainKeyValuePairs(t *testing.T) {
	t.Parallel()

	input := `api_key: tracker-secret api-key=hyphen-secret apiToken: camel-secret auth_key=auth-secret rss-key=rss-secret torrentPass=torrent-secret AntiCsrfToken=csrf-secret token=plain-token Authorization=Bearer bearer-secret cookie: "session-secret" message=kept`
	output := RedactValue(input, nil)

	for _, secret := range []string{"tracker-secret", "hyphen-secret", "camel-secret", "auth-secret", "rss-secret", "torrent-secret", "csrf-secret", "plain-token", "bearer-secret", "session-secret"} {
		if contains(output, secret) {
			t.Fatalf("expected %q redacted, got %q", secret, output)
		}
	}
	for _, marker := range []string{"api_key: [REDACTED]", "api-key=[REDACTED]", "apiToken: [REDACTED]", "auth_key=[REDACTED]", "rss-key=[REDACTED]", "torrentPass=[REDACTED]", "AntiCsrfToken=[REDACTED]", "token=[REDACTED]", "Authorization=Bearer [REDACTED]", `cookie: "[REDACTED]"`, "message=kept"} {
		if !contains(output, marker) {
			t.Fatalf("expected marker %q in %q", marker, output)
		}
	}
}

func TestRedactValueQuotedKeyValuePairsWithEscapedQuotes(t *testing.T) {
	t.Parallel()

	input := `token="alpha\"bravo" password='charlie\'delta' message=kept`
	output := RedactValue(input, nil)

	for _, secret := range []string{"alpha", "bravo", "charlie", "delta"} {
		if contains(output, secret) {
			t.Fatalf("expected %q redacted, got %q", secret, output)
		}
	}
	for _, marker := range []string{`token="[REDACTED]"`, `password='[REDACTED]'`, "message=kept"} {
		if !contains(output, marker) {
			t.Fatalf("expected marker %q in %q", marker, output)
		}
	}
}

func TestRedactPrivateInfoJSON(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"token":         "abc",
		"apiKey":        "api-secret",
		"auth_key":      "auth-secret",
		"torrentPass":   "torrent-secret",
		"AntiCsrfToken": "csrf-secret",
		"nested":        map[string]any{"password": "secret", "rss-key": "rss-secret"},
		"entries":       []any{"passkey", "value"},
	}

	redacted, ok := RedactPrivateInfo(input, nil).(map[string]any)
	if !ok {
		t.Fatalf("expected redacted value to be map[string]any")
	}
	if redacted["token"] != "[REDACTED]" {
		t.Fatalf("expected token redacted, got %#v", redacted["token"])
	}
	for _, key := range []string{"apiKey", "auth_key", "torrentPass", "AntiCsrfToken"} {
		if redacted[key] != "[REDACTED]" {
			t.Fatalf("expected %s redacted, got %#v", key, redacted[key])
		}
	}
	nested, ok := redacted["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested redacted value to be map[string]any")
	}
	if nested["password"] != "[REDACTED]" {
		t.Fatalf("expected password redacted, got %#v", nested["password"])
	}
	if nested["rss-key"] != "[REDACTED]" {
		t.Fatalf("expected rss-key redacted, got %#v", nested["rss-key"])
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) > 0 && (stringIndex(haystack, needle) >= 0)
}

func stringIndex(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
