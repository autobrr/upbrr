// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package redaction

import "testing"

func TestRedactValueURLPatterns(t *testing.T) {
	t.Parallel()

	input := "https://tracker.example/0123456789abcdef/announce?passkey=secret&token=abc&info_hash=deadbeef&authkey=private&auth-key=private2&apiKey=api-secret&api-token=api-token-secret&rss_key=rss-secret&torrent-pass=torrent-secret&AntiCsrfToken=csrf-secret&uid=123"
	output := RedactValue(input, nil)

	if output == input {
		t.Fatal("expected redaction to change fixture")
	}
	if contains(output, "0123456789abcdef") {
		t.Fatal("expected passkey redacted")
	}
	for _, secret := range []string{"secret", "token=abc", "authkey=private", "auth-key=private2", "api-secret", "api-token-secret", "rss-secret", "torrent-secret", "csrf-secret", "uid=123"} {
		if contains(output, secret) {
			t.Fatal("expected sensitive query param redacted")
		}
	}
	if !contains(output, "apiKey=[REDACTED]") || !contains(output, "auth-key=[REDACTED]") || !contains(output, "torrent-pass=[REDACTED]") {
		t.Fatal("expected query params redacted")
	}
}

func TestRedactValueStructurallyRedactsEncodedURLSecrets(t *testing.T) {
	t.Parallel()

	input := "https://synthetic-user:synthetic-password@tracker.example/%61nnounce/%30%31%32%33%34%35%36%37%38%39abcdef?pass%6bey=synthetic-passkey&session=synthetic-session&page=2#token=synthetic-token"
	output := RedactValue(input, nil)

	for _, secret := range []string{"synthetic-user", "synthetic-password", "0123456789abcdef", "synthetic-passkey", "synthetic-session", "synthetic-token"} {
		if contains(output, secret) {
			t.Fatal("expected URL secret redacted")
		}
	}
	for _, marker := range []string{"https://[REDACTED]@tracker.example/announce/[REDACTED]", "passkey=[REDACTED]", "session=[REDACTED]", "page=2", "#token=[REDACTED]"} {
		if !contains(output, marker) {
			t.Fatalf("expected structural URL context %q", marker)
		}
	}
}

func TestRedactValueUsesCustomSensitiveKeysForURLValues(t *testing.T) {
	t.Parallel()

	keys := map[string]struct{}{"diagnosticcode": {}}
	input := "https://tracker.example/diagnostics?diagnostic_code=query-secret&page=2#diagnostic-code=fragment-secret"
	output := RedactValue(input, keys)

	for _, secret := range []string{"query-secret", "fragment-secret"} {
		if contains(output, secret) {
			t.Fatal("expected custom URL secret redacted")
		}
	}
	if !contains(output, "page=2") {
		t.Fatal("expected ordinary URL query value preserved")
	}
}

func TestRedactValueSessionKeyVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "plain underscore", input: "session_id=plain-underscore-secret"},
		{name: "plain hyphen", input: "session-id=plain-hyphen-secret"},
		{name: "plain camel", input: "sessionId=plain-camel-secret"},
		{name: "quoted underscore", input: `session_id="quoted-underscore-secret"`},
		{name: "quoted hyphen", input: `session-id="quoted-hyphen-secret"`},
		{name: "quoted camel", input: `sessionId="quoted-camel-secret"`},
		{name: "query underscore", input: "error?session_id=query-underscore-secret"},
		{name: "query hyphen", input: "error?session-id=query-hyphen-secret"},
		{name: "query camel", input: "error?sessionId=query-camel-secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			output := RedactValue(tt.input, nil)
			if contains(output, "secret") || !contains(output, "[REDACTED]") {
				t.Fatal("expected session value redacted")
			}
		})
	}
}

func TestRedactValueFailsClosedOnMalformedSensitiveURLQuery(t *testing.T) {
	t.Parallel()

	output := RedactValue("https://tracker.example/diagnostics?pass%6bey=%ZZ&page=2", nil)
	if contains(output, "pass%6bey") || contains(output, "%ZZ") {
		t.Fatal("expected malformed sensitive URL query redacted")
	}
	if !contains(output, "?[REDACTED]") {
		t.Fatal("expected malformed sensitive URL query redaction marker")
	}
}

func TestRedactValueAnnouncePathToken(t *testing.T) {
	t.Parallel()

	input := "https://tracker.example/announce/0123456789abcdef"
	output := RedactValue(input, nil)

	if contains(output, "0123456789abcdef") {
		t.Fatal("expected announce path token redacted")
	}
}

func TestRedactValueTrackerLookupRequestErrors(t *testing.T) {
	t.Parallel()

	input := "trackerdata: bhd request: Post \"https://beyond-hd.me/api/torrents/bhdSecretKey123\": dial tcp timeout; unit3d: request: Get \"https://aither.cc/api/torrents/filter?api_token=aitherSecretKey123&file_name=Release.Name\": context deadline exceeded"
	output := RedactValue(input, nil)

	if contains(output, "bhdSecretKey123") || contains(output, "aitherSecretKey123") {
		t.Fatal("expected request error secrets redacted")
	}
	if !contains(output, "/api/torrents/[REDACTED]") || !contains(output, "api_token=[REDACTED]") {
		t.Fatal("expected redacted request error shape preserved")
	}
}

func TestRedactValueProxyPath(t *testing.T) {
	t.Parallel()

	input := "https://example.com/proxy/secret/api/v2/torrents"
	output := RedactValue(input, nil)

	if contains(output, "/proxy/secret/") {
		t.Fatal("expected proxy secret redacted")
	}
}

func TestRedactValueBareProxyPath(t *testing.T) {
	t.Parallel()

	input := "clients: connecting to qbit http://127.0.0.1:7476/proxy/secret"
	output := RedactValue(input, nil)

	if contains(output, "/proxy/secret") {
		t.Fatal("expected bare proxy secret redacted")
	}
	if !contains(output, "/proxy/[REDACTED]") {
		t.Fatal("expected proxy path shape preserved")
	}
}

func TestRedactValuePlainKeyValuePairs(t *testing.T) {
	t.Parallel()

	input := `api_key: tracker-secret api-key=hyphen-secret apiToken: camel-secret auth_key=auth-secret rss-key=rss-secret torrentPass=torrent-secret AntiCsrfToken=csrf-secret token=plain-token session=session-secret Authorization=Bearer bearer-secret cookie: "cookie-secret" message=kept`
	output := RedactValue(input, nil)

	for _, secret := range []string{"tracker-secret", "hyphen-secret", "camel-secret", "auth-secret", "rss-secret", "torrent-secret", "csrf-secret", "plain-token", "session-secret", "bearer-secret", "cookie-secret"} {
		if contains(output, secret) {
			t.Fatal("expected sensitive key/value redacted")
		}
	}
	for _, marker := range []string{"api_key: [REDACTED]", "api-key=[REDACTED]", "apiToken: [REDACTED]", "auth_key=[REDACTED]", "rss-key=[REDACTED]", "torrentPass=[REDACTED]", "AntiCsrfToken=[REDACTED]", "token=[REDACTED]", "Authorization=Bearer [REDACTED]", `cookie: "[REDACTED]"`, "message=kept"} {
		if !contains(output, marker) {
			t.Fatal("expected redaction marker preserved")
		}
	}
}

func TestRedactValueDelimitedAuthAndCookieTails(t *testing.T) {
	t.Parallel()

	input := `Cookie: uid=first-secret; session=second-secret, Authorization=Bearer alpha-secret,beta-secret token=third-secret`
	output := RedactValue(input, nil)

	for _, secret := range []string{"first-secret", "second-secret", "alpha-secret", "beta-secret", "third-secret"} {
		if contains(output, secret) {
			t.Fatal("expected delimited auth and cookie tails redacted")
		}
	}
}

func TestRedactValueDoesNotReredactRedactedQueryValues(t *testing.T) {
	t.Parallel()

	input := "tracker=https://tracker.example/upload?api_key=secret-key&passkey=secret-pass state=ready, count=4]"
	output := RedactValue(input, nil)
	second := RedactValue(output, nil)

	if contains(second, "[REDACTED]]") {
		t.Fatal("expected already-redacted query values to stay stable")
	}
	if !contains(second, "api_key=[REDACTED]&passkey=[REDACTED] state=ready, count=4]") {
		t.Fatal("expected query values redacted once")
	}
	if second != output {
		t.Fatal("second redaction changed output")
	}
}

func TestRedactValueURLUserinfo(t *testing.T) {
	t.Parallel()

	input := `primary=https://policy-user:policy-password@host.example secondary=//other-user:other-password@proxy.example/path`
	output := RedactValue(input, nil)

	for _, secret := range []string{"policy-user", "policy-password", "other-user", "other-password"} {
		if contains(output, secret) {
			t.Fatal("expected URL userinfo redacted")
		}
	}
	for _, marker := range []string{"https://[REDACTED]@host.example", "//[REDACTED]@proxy.example/path"} {
		if !contains(output, marker) {
			t.Fatal("expected URL authority preserved around redacted userinfo")
		}
	}
}

func TestRedactValueQuotedKeyValuePairsWithEscapedQuotes(t *testing.T) {
	t.Parallel()

	input := `token="alpha\"bravo" password='charlie\'delta' message=kept`
	output := RedactValue(input, nil)

	for _, secret := range []string{"alpha", "bravo", "charlie", "delta"} {
		if contains(output, secret) {
			t.Fatal("expected quoted secret redacted")
		}
	}
	for _, marker := range []string{`token="[REDACTED]"`, `password='[REDACTED]'`, "message=kept"} {
		if !contains(output, marker) {
			t.Fatal("expected quoted redaction marker preserved")
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
		t.Fatal("expected token redacted")
	}
	for _, key := range []string{"apiKey", "auth_key", "torrentPass", "AntiCsrfToken"} {
		if redacted[key] != "[REDACTED]" {
			t.Fatal("expected secret field redacted")
		}
	}
	nested, ok := redacted["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested redacted value to be map[string]any")
	}
	if nested["password"] != "[REDACTED]" {
		t.Fatal("expected password redacted")
	}
	if nested["rss-key"] != "[REDACTED]" {
		t.Fatal("expected rss-key redacted")
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
