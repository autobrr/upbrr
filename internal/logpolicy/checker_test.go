package logpolicy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRepositoryFlagsStdlibAndBareLogs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

import (
	"fmt"
)

type logger struct{}

func (logger) Errorf(string, ...any) {}

func check(log logger, err error) {
	fmt.Printf("bad: %v", err)
	log.Errorf("%v", err)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("expected 2 violations, got %d: %#v", len(violations), violations)
	}

	messages := []string{violations[0].Message, violations[1].Message}
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, "project logger") {
		t.Fatalf("expected stdlib logging violation, got %q", joined)
	}
	if !strings.Contains(joined, "bare format string") {
		t.Fatalf("expected bare format string violation, got %q", joined)
	}
}

func TestCheckRepositoryAllowsTestStdlibOutputWithoutSensitiveArgs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	mainContent := `package sample

type logger struct{}

func (logger) Errorf(string, ...any) {}

func check(log logger, err error) {
	log.Errorf("sample failed: %v", err)
}
`
	testContent := `package sample

import "fmt"

func checkTest() {
	fmt.Printf("test output")
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(mainContent), 0o600); err != nil {
		t.Fatalf("write main sample file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample_test.go"), []byte(testContent), 0o600); err != nil {
		t.Fatalf("write test sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsBTNCookieHeaderPatternInTests(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"net/http"
	"strings"
)

type recorder struct{}

func (recorder) Errorf(string, ...any) {}

func check(r *http.Request, handlerErrs recorder) {
	if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=one") {
		handlerErrs.Errorf("expected cookie one, got %q", got)
	}
	if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=two") {
		handlerErrs.Errorf("expected cookie two, got %q", got)
	}
	if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=three") {
		handlerErrs.Errorf("expected cookie three, got %q", got)
	}
	if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=four") {
		handlerErrs.Errorf("expected cookie four, got %q", got)
	}
	if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=five") {
		handlerErrs.Errorf("expected cookie five, got %q", got)
	}
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 5 {
		t.Fatalf("expected 5 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "sensitive HTTP header output") {
			t.Fatalf("expected sensitive header violation, got %q", violation.Message)
		}
	}
}

func TestCheckRepositoryAllowsCookieHeaderStateAssertionInTests(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"net/http"
	"strings"
)

type recorder struct{}

func (recorder) Errorf(string, ...any) {}

func check(r *http.Request, handlerErrs recorder) {
	if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=one") {
		handlerErrs.Errorf("expected session cookie")
	}
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryAllowsRedactedSensitiveHeaderOutput(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"net/http"

	"github.com/autobrr/upbrr/internal/redaction"
)

func check(t testingT, r *http.Request) {
	got := r.Header.Get("Authorization")
	t.Fatalf("expected auth, got %q", redaction.RedactValue(got, nil))
}

type testingT interface {
	Fatalf(string, ...any)
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsSensitiveHeaderFormQueryAndBodyOutput(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"fmt"
	"io"
	"net/http"
)

func check(t testingT, r *http.Request, resp *http.Response) error {
	auth := r.Header.Get("Authorization")
	t.Fatalf("expected auth, got %q", auth)
	if got := r.FormValue("passkey"); got != "pass" {
		return fmt.Errorf("expected passkey, got %q", got)
	}
	if got := r.FormValue("api-token"); got != "secret" {
		return fmt.Errorf("expected api-token, got %q", got)
	}
	if got := r.FormValue("anti_csrf_token"); got != "secret" {
		return fmt.Errorf("expected anti_csrf_token, got %q", got)
	}
	if got := r.URL.Query().Get("secret"); got != "secret" {
		return fmt.Errorf("expected secret, got %q", got)
	}
	if got := r.URL.Query().Get("apikey"); got != "secret" {
		return fmt.Errorf("expected apikey, got %q", got)
	}
	if got := r.URL.Query().Get("api-key"); got != "secret" {
		return fmt.Errorf("expected api-key, got %q", got)
	}
	if got := r.URL.Query().Get("apiToken"); got != "secret" {
		return fmt.Errorf("expected apiToken, got %q", got)
	}
	if got := r.URL.Query().Get("auth_key"); got != "secret" {
		return fmt.Errorf("expected auth_key, got %q", got)
	}
	if got := r.URL.Query().Get("rss-key"); got != "secret" {
		return fmt.Errorf("expected rss-key, got %q", got)
	}
	if got := r.URL.Query().Get("torrent-pass"); got != "secret" {
		return fmt.Errorf("expected torrent-pass, got %q", got)
	}
	body, _ := io.ReadAll(resp.Body)
	t.Fatalf("unexpected response body %s", string(body))
	return nil
}

type testingT interface {
	Fatalf(string, ...any)
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 12 {
		t.Fatalf("expected 12 violations, got %d: %#v", len(violations), violations)
	}
}

func TestCheckRepositoryFlagsUnboundedResponseBodyReads(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

import (
	"fmt"
	"io"
	"net/http"

	"github.com/autobrr/upbrr/internal/redaction"
)

func unsafe(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("sample: http %d: %s", resp.StatusCode, redaction.RedactValue(string(body), nil))
}

func viaHelper(resp *http.Response) error {
	body, _ := readAndCloseResponseBody(resp)
	return fmt.Errorf("sample: http %d: %s", resp.StatusCode, safeResponsePreview(body))
}

func uploadError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return UploadHTTPError("GPW", resp.StatusCode, body)
}

func safe(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("sample: http %d: %s", resp.StatusCode, redaction.RedactValue(string(body), nil))
}

func readAndCloseResponseBody(*http.Response) ([]byte, error) { return nil, nil }
func safeResponsePreview([]byte) string { return "" }
func UploadHTTPError(string, int, []byte) error { return nil }
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("expected 3 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "must be bounded before redaction") {
			t.Fatalf("expected bounded-read violation, got %q", violation.Message)
		}
	}
}

func TestCheckRepositoryDoesNotTreatReadAllErrorAsResponseBody(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

import (
	"io"
	"net/http"

	"github.com/autobrr/upbrr/internal/redaction"
)

func check(resp *http.Response) string {
	body, err := io.ReadAll(resp.Body)
	_ = body
	if err != nil {
		return redaction.RedactValue(err.Error(), nil)
	}
	return ""
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryKeepsSensitiveBindingsLexicallyScoped(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"net/http"
	"testing"
)

func checkInnerSensitiveDoesNotLeak(t *testing.T, r *http.Request) {
	got := "safe"
	if true {
		got := r.Header.Get("Cookie")
		_ = got
	}
	t.Fatalf("expected safe value, got %q", got)
}

func checkInnerSafeDoesNotClearOuterSensitive(t *testing.T, r *http.Request) {
	got := r.Header.Get("Cookie")
	if true {
		got := "safe"
		t.Logf("inner value: %q", got)
	}
	t.Fatalf("expected cookie, got %q", got)
}

func checkRangeSensitiveDoesNotLeak(t *testing.T, r *http.Request) {
	got := "safe"
	for _, got := range r.Cookies() {
		_ = got
	}
	t.Fatalf("expected safe value, got %q", got)
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "HTTP header") {
		t.Fatalf("expected outer sensitive cookie violation, got %#v", violations[0])
	}
}

func TestCheckRepositoryFlagsNonFormatTestOutputMethods(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"io"
	"net/http"
	"testing"
)

func check(t *testing.T, r *http.Request, resp *http.Response) {
	auth := r.Header.Get("Authorization")
	t.Fatal(auth)
	cookies := []*http.Cookie{{Name: "session", Value: "secret"}}
	t.Error(cookies)
	body, _ := io.ReadAll(resp.Body)
	t.Log(body)
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("expected 3 violations, got %d: %#v", len(violations), violations)
	}
}

func TestCheckRepositoryFlagsFatalCallsInHTTPTestHandlers(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerFatal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ok" {
			t.Fatal("unexpected path")
		}
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	t.Cleanup(server.Close)
}

func TestNormalFatal(t *testing.T) {
	if false {
		t.Fatal("ordinary test goroutine failure")
	}
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("expected 2 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "request goroutine") {
			t.Fatalf("expected handler fatal violation, got %q", violation.Message)
		}
	}
}

func TestCheckRepositoryAllowsHandlerErrorsRecordedOutsideRequestGoroutine(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerErrorRecorder(t *testing.T) {
	var handlerErr error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ok" {
			handlerErr = errUnexpectedPath
			return
		}
	}))
	t.Cleanup(server.Close)
	if handlerErr != nil {
		t.Fatalf("handler error: %v", handlerErr)
	}
}

var errUnexpectedPath error
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryAllowsLogpolicyAllowForNonHeaderSensitiveOutput(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"fmt"
	"net/http"
)

func check(r *http.Request) error {
	if got := r.URL.Query().Get("secret"); got != "fixture-secret" {
		//logpolicy:allow fake fixture secret is required to diagnose parser shape
		return fmt.Errorf("expected secret, got %q", got)
	}
	return nil
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryReportsMissingAndUnusedLogpolicyAllow(t *testing.T) {
	root := t.TempDir()
	content := `package sample

func check() {
	//logpolicy:allow
	_ = "missing reason"
	//logpolicy:allow unused fake fixture reason
	_ = "unused"
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("expected 2 violations, got %d: %#v", len(violations), violations)
	}
	joined := violations[0].Message + "\n" + violations[1].Message
	if !strings.Contains(joined, "must include a reason") || !strings.Contains(joined, "unused logpolicy allow") {
		t.Fatalf("expected missing and unused allow violations, got %q", joined)
	}
}

func TestCheckRepositoryFlagsCookieContainerHelperOutput(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"context"
	"net/http"

	"github.com/autobrr/upbrr/internal/cookies"
)

func check(t testingT, dbPath string) {
	values, _ := cookies.LoadTrackerCookieMap(context.Background(), dbPath, "BTN")
	t.Fatalf("expected cookies, got %#v", values)
	httpCookies := cookies.CookieMapToHTTPCookies(map[string]string{"session": "abc"}, "example.test")
	t.Fatalf("expected cookies, got %#v", httpCookies)
	t.Fatalf("expected cookie, got %#v", &http.Cookie{Name: "session", Value: "abc"})
}

type testingT interface {
	Fatalf(string, ...any)
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("expected 3 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "cookie output") {
			t.Fatalf("expected cookie output violation, got %q", violation.Message)
		}
	}
}

func TestCheckRepositoryFlagsSecretConfigFieldOutput(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type config struct {
	TMDBAPI string
	Trackers trackerSet
}
type trackerSet struct{ Trackers map[string]trackerConfig }
type trackerConfig struct {
	APIKey string
	APIToken string
	AntiCSRFToken string
	AuthKey string
	Passkey string
	RSSKey string
	Secret string
	TorrentPass string
	AnnounceURL string
	URL string
}
type metaInfo struct{ Announce string }

func check(t testingT, cfg config, meta metaInfo) {
	t.Fatalf("TMDBAPI mismatch: got %q", cfg.TMDBAPI)
	t.Fatalf("tracker API key mismatch: got %q", cfg.Trackers.Trackers["BTN"].APIKey)
	t.Fatalf("tracker API token mismatch: got %q", cfg.Trackers.Trackers["BTN"].APIToken)
	t.Fatalf("tracker anti-csrf token mismatch: got %q", cfg.Trackers.Trackers["BTN"].AntiCSRFToken)
	t.Fatalf("tracker auth key mismatch: got %q", cfg.Trackers.Trackers["BTN"].AuthKey)
	t.Fatalf("tracker passkey mismatch: got %q", cfg.Trackers.Trackers["CZT"].Passkey)
	t.Fatalf("tracker RSS key mismatch: got %q", cfg.Trackers.Trackers["BTN"].RSSKey)
	t.Fatalf("tracker secret mismatch: got %q", cfg.Trackers.Trackers["BTN"].Secret)
	t.Fatalf("tracker torrent pass mismatch: got %q", cfg.Trackers.Trackers["BTN"].TorrentPass)
	t.Fatalf("tracker announce mismatch: got %q", cfg.Trackers.Trackers["CZT"].AnnounceURL)
	t.Fatalf("tracker URL mismatch: got %q", cfg.Trackers.Trackers["BTN"].URL)
	t.Fatalf("torrent announce mismatch: got %q", meta.Announce)
}

type testingT interface {
	Fatalf(string, ...any)
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 12 {
		t.Fatalf("expected 12 violations, got %d: %#v", len(violations), violations)
	}
}

func TestCheckRepositoryFlagsSensitiveHelperReturnOutput(t *testing.T) {
	root := t.TempDir()
	content := `package sample

func check(t testingT) {
	apiKey := loadStoredRTFAPIKey()
	t.Fatalf("stored token: %q", apiKey)
	apiToken := refreshAPIToken()
	t.Fatalf("refreshed token: %q", apiToken)
	authKey := extractMTVAuthKey()
	t.Fatalf("auth key: %q", authKey)
	rssKey := getRSSKey()
	t.Fatalf("rss key: %q", rssKey)
	torrentPass := readTorrentPass()
	t.Fatalf("torrent pass: %q", torrentPass)
}

func loadStoredRTFAPIKey() string { return "" }
func refreshAPIToken() string { return "" }
func extractMTVAuthKey() string { return "" }
func getRSSKey() string { return "" }
func readTorrentPass() string { return "" }

type testingT interface {
	Fatalf(string, ...any)
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 5 {
		t.Fatalf("expected 5 violations, got %d: %#v", len(violations), violations)
	}
}

func TestCheckRepositoryFlagsSecretBearingURLOutput(t *testing.T) {
	root := t.TempDir()
	content := `package sample

func check(t testingT, cfg trackerConfig) {
	endpoint := "https://tracker.test/api?api_key=" + cfg.APIKey + "&action=upload"
	t.Fatalf("endpoint: %s", endpoint)
	hyphenEndpoint := "https://tracker.test/api?api-key=" + cfg.APIKey + "&action=upload"
	t.Fatalf("endpoint: %s", hyphenEndpoint)
	camelEndpoint := "https://tracker.test/api?apiToken=" + cfg.APIKey + "&action=upload"
	t.Fatalf("endpoint: %s", camelEndpoint)
	authEndpoint := "https://tracker.test/api?auth-key=" + cfg.APIKey + "&action=upload"
	t.Fatalf("endpoint: %s", authEndpoint)
	rssEndpoint := "https://tracker.test/api?rss_key=" + cfg.APIKey + "&action=upload"
	t.Fatalf("endpoint: %s", rssEndpoint)
	torrentPassEndpoint := "https://tracker.test/api?torrent-pass=" + cfg.APIKey + "&action=upload"
	t.Fatalf("endpoint: %s", torrentPassEndpoint)
}

type trackerConfig struct{ APIKey string }
type testingT interface {
	Fatalf(string, ...any)
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 6 {
		t.Fatalf("expected 6 violations, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "secret config field output") {
		t.Fatalf("expected secret field violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryFlagsSensitiveFixtureLogBufferDump(t *testing.T) {
	root := t.TempDir()
	content := `package sample

func check(t testingT, logs string) {
	if !contains(logs, "tracker ready") {
		t.Fatalf("expected tracker ready in logs: %s", logs)
	}
	if contains(logs, "hunter2") {
		t.Fatalf("logs leaked password")
	}
}

func checkCombinedLogs(t testingT, infoLog string, traceLog string, warnLog string) {
	allLogs := infoLog + "\n" + traceLog + "\n" + warnLog
	if contains(allLogs, "secret-api-key") {
		t.Fatalf("combined logs leaked secret: %s", allLogs)
	}
}

func checkArtifact(t testingT, text string) {
	if contains(text, "secret-key") {
		t.Fatalf("artifact leaked secret body: %s", text)
	}
}

func contains(string, string) bool { return false }

type testingT interface {
	Fatalf(string, ...any)
}
`
	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("expected 3 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "sensitive output") {
			t.Fatalf("expected sensitive output violation, got %q", violation.Message)
		}
	}
}

func TestCheckRepositoryFlagsRedactionFixtureOutputDump(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "redaction"), 0o755); err != nil {
		t.Fatalf("mkdir internal redaction: %v", err)
	}

	content := `package redaction

func check(t testingT) {
	input := "token=secret"
	output := RedactValue(input, nil)
	if output == input {
		t.Fatalf("expected redaction, got %q", output)
	}
	for _, secret := range []string{"secret"} {
		if contains(output, secret) {
			t.Fatalf("expected %q redacted", secret)
		}
	}
	redacted := RedactPrivateInfo(map[string]any{"token": "secret"}, nil).(map[string]any)
	if redacted["token"] != "[REDACTED]" {
		t.Fatalf("expected token redacted, got %#v", redacted["token"])
	}
}

type testingT interface {
	Fatalf(string, ...any)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "redaction", "redaction_test.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("expected 3 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "sensitive output") {
			t.Fatalf("expected sensitive output violation, got %q", violation.Message)
		}
	}
}

func TestCheckRepositoryFlagsFrontendEncryptedEnvelopeMatcherOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}
	testDir := filepath.Join(root, "gui", "frontend", "src", "hooks")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		t.Fatalf("mkdir frontend test dir: %v", err)
	}

	content := `import { expect, it } from "vitest";

it("preserves encrypted URL", () => {
  const encryptedURL: string = "upbrr-enc:v1:encrypted-btn-url";
  const payload = { URL: encryptedURL };
  expect(payload.URL).toBe(
    encryptedURL,
  );
  expect(payload.URL).toEqual("upbrr-enc:v1:literal-envelope");
  expect(payload.URL === encryptedURL).toBe(true);
  createElement(
    "pre",
    { "data-testid": "payload" },
    state.buildSavePayload() ?? "",
  );
});
`

	if err := os.WriteFile(filepath.Join(testDir, "useSettingsState.test.ts"), []byte(content), 0o644); err != nil {
		t.Fatalf("write frontend test file: %v", err)
	}
	tsxContent := `import { expect, it } from "vitest";

it("renders payload", () => {
  return <pre data-testid="payload">{state.buildSavePayload()}</pre>;
});

it("renders payload with expression attribute", () => {
  return (
    <pre data-testid={"payload"}>
      {state.buildSavePayload()}
    </pre>
  );
});
`

	if err := os.WriteFile(filepath.Join(testDir, "useSettingsState.test.tsx"), []byte(tsxContent), 0o644); err != nil {
		t.Fatalf("write frontend tsx test file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 5 {
		t.Fatalf("expected 5 violations, got %d: %#v", len(violations), violations)
	}
	messages := make([]string, 0, len(violations))
	for _, violation := range violations {
		messages = append(messages, violation.Message)
	}
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, "encrypted envelope") {
		t.Fatalf("expected encrypted envelope violation, got %q", joined)
	}
	if !strings.Contains(joined, "raw save payloads into the DOM") {
		t.Fatalf("expected raw payload DOM violation, got %q", joined)
	}
}

func TestCheckRepositoryFlagsRawDryRunDetailsOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "upbrr"), 0o755); err != nil {
		t.Fatalf("mkdir cmd upbrr: %v", err)
	}

	content := `package main

import "fmt"

type dryRun struct {
	Endpoint string
	Payload map[string]string
	Files []dryRunFile
}

type dryRunFile struct {
	Path string
}

func printDryRunDetails(entry dryRun, key string) {
	fmt.Printf("Endpoint: %s\n", entry.Endpoint)
	fmt.Printf("- %s: %s\n", key, entry.Payload[key])
	for _, file := range entry.Files {
		fmt.Printf("- torrent [present]: %s\n", file.Path)
	}
}
`

	if err := os.WriteFile(filepath.Join(root, "cmd", "upbrr", "interactive.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("expected 3 violations, got %d: %#v", len(violations), violations)
	}
	messages := make([]string, 0, len(violations))
	for _, violation := range violations {
		messages = append(messages, violation.Message)
	}
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, "dry-run endpoint output") {
		t.Fatalf("expected endpoint redaction violation, got %q", joined)
	}
	if !strings.Contains(joined, "dry-run payload output") {
		t.Fatalf("expected payload redaction violation, got %q", joined)
	}
	if !strings.Contains(joined, "dry-run file path output") {
		t.Fatalf("expected file path output violation, got %q", joined)
	}
}

func TestCheckRepositoryAllowsRedactedDryRunDetailsOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "upbrr"), 0o755); err != nil {
		t.Fatalf("mkdir cmd upbrr: %v", err)
	}

	content := `package main

import "fmt"

type dryRun struct {
	Endpoint string
	Payload map[string]string
	Files []dryRunFile
}

type dryRunFile struct {
	Path string
}

func safeDryRunEndpoint(value string) string { return value }
func formatDryRunPayloadValue(key string, value string) string { return value }
func formatDryRunFilePath(value string) string { return value }

func printDryRunDetails(entry dryRun, key string) {
	fmt.Printf("Endpoint: %s\n", safeDryRunEndpoint(entry.Endpoint))
	fmt.Printf("- %s: %s\n", key, formatDryRunPayloadValue(key, entry.Payload[key]))
	for _, file := range entry.Files {
		fmt.Printf("- torrent [present]: %s\n", formatDryRunFilePath(file.Path))
	}
}
`

	if err := os.WriteFile(filepath.Join(root, "cmd", "upbrr", "interactive.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsLocalPathCLIOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "upbrr"), 0o755); err != nil {
		t.Fatalf("mkdir cmd upbrr: %v", err)
	}

	content := `package main

import "fmt"

type preview struct {
	SourcePath string
}

func printReleaseDetails(p preview, sourcePath string) {
	fmt.Printf("Source: %s\n", p.SourcePath)
	fmt.Printf("Input: %s\n", sourcePath)
	fmt.Println(p.SourcePath)
	fmt.Print(sourcePath)
}
`

	if err := os.WriteFile(filepath.Join(root, "cmd", "upbrr", "interactive.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 4 {
		t.Fatalf("expected 4 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "local filesystem path output") {
			t.Fatalf("expected local path output violation, got %q", violation.Message)
		}
	}
}

func TestCheckRepositoryAllowsLabeledLocalPathCLIOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "upbrr"), 0o755); err != nil {
		t.Fatalf("mkdir cmd upbrr: %v", err)
	}

	content := `package main

import "fmt"

type preview struct {
	SourcePath string
}

func formatPathLabel(value string) string { return value }

func printReleaseDetails(p preview, sourcePath string) {
	fmt.Printf("Source: %s\n", formatPathLabel(p.SourcePath))
	fmt.Printf("Input: %s\n", formatPathLabel(sourcePath))
	fmt.Println(formatPathLabel(p.SourcePath))
	fmt.Print(formatPathLabel(sourcePath))
}
`

	if err := os.WriteFile(filepath.Join(root, "cmd", "upbrr", "interactive.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryAllowsGenericCLIOutputNamesWithoutPathSource(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "upbrr"), 0o755); err != nil {
		t.Fatalf("mkdir cmd upbrr: %v", err)
	}

	content := `package main

import "fmt"

func printDecision(candidate string, target string, output string, guessed string) {
	selected := candidate
	result := output
	fmt.Printf("candidate=%s target=%s output=%s guessed=%s\n", selected, target, result, guessed)
}
`

	if err := os.WriteFile(filepath.Join(root, "cmd", "upbrr", "interactive.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsGenericCLIOutputNamesFromPathSources(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "upbrr"), 0o755); err != nil {
		t.Fatalf("mkdir cmd upbrr: %v", err)
	}

	content := `package main

import (
	"fmt"
	"path/filepath"
)

func printPath(root string) {
	candidate := filepath.Join(root, "candidate")
	target := candidate
	output := target
	guessed := output
	fmt.Println(candidate)
	fmt.Println(target)
	fmt.Println(output)
	fmt.Println(guessed)
}
`

	if err := os.WriteFile(filepath.Join(root, "cmd", "upbrr", "interactive.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 4 {
		t.Fatalf("expected 4 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "local filesystem path output") {
			t.Fatalf("expected local path output violation, got %q", violation.Message)
		}
	}
}

func TestCheckRepositoryFlagsProjectLoggerWithoutCentralPathSanitization(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "logging"), 0o755); err != nil {
		t.Fatalf("mkdir internal logging: %v", err)
	}

	content := `package logging

import "fmt"

type Logger struct{}

func (l *Logger) logf(format string, args ...any) {
	formatted := fmt.Sprintf(format, args...)
	_ = formatted
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "logging", "logger.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write logger file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "SanitizeMessage") {
		t.Fatalf("expected central sanitizer violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryAllowsProjectLoggerWithCentralPathSanitization(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "logging"), 0o755); err != nil {
		t.Fatalf("mkdir internal logging: %v", err)
	}

	content := `package logging

import "fmt"

type Logger struct{}

func SanitizeMessage(message string) string { return message }

func (l *Logger) logf(format string, args ...any) {
	formatted := SanitizeMessage(fmt.Sprintf(format, args...))
	_ = formatted
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "logging", "logger.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write logger file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestProjectLoggerURLPathPreservationFlagsSanitizerRegression(t *testing.T) {
	t.Parallel()

	violations := checkProjectLoggerURLPathPreservation(
		token.NewFileSet(),
		"internal/logging/logger.go",
		token.NoPos,
		func(string) string { return "url=https://img.example.com/[local path]" },
	)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "preserve URL path segments") {
		t.Fatalf("expected URL path preservation violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryFlagsRawTerminalErrorOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "upbrr"), 0o755); err != nil {
		t.Fatalf("mkdir cmd upbrr: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "gui"), 0o755); err != nil {
		t.Fatalf("mkdir gui: %v", err)
	}

	cliContent := `package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fmt.Fprintln(os.Stderr, err)
	}
}

func run() error { return nil }
`
	guiContent := `package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fmt.Fprintln(os.Stderr, err)
	}
}

func run() error { return nil }
`

	if err := os.WriteFile(filepath.Join(root, "cmd", "upbrr", "main.go"), []byte(cliContent), 0o600); err != nil {
		t.Fatalf("write CLI sample file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "gui", "main.go"), []byte(guiContent), 0o600); err != nil {
		t.Fatalf("write GUI sample file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(guiContent), 0o600); err != nil {
		t.Fatalf("write internal sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 6 {
		t.Fatalf("expected 6 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "terminal error/warning output") {
			t.Fatalf("expected terminal diagnostic violation, got %q", violation.Message)
		}
	}
}

func TestCheckRepositoryAllowsSanitizedTerminalOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "upbrr"), 0o755); err != nil {
		t.Fatalf("mkdir cmd upbrr: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "gui"), 0o755); err != nil {
		t.Fatalf("mkdir gui: %v", err)
	}

	content := `package main

import (
	"fmt"
	"os"

	"github.com/autobrr/upbrr/internal/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", logging.SanitizeMessage(err.Error()))
		fmt.Fprintln(os.Stderr, logging.SanitizeMessage(err.Error()))
	}
	for _, w := range warnings() {
		fmt.Fprintf(os.Stderr, "warning: %s\n", logging.SanitizeMessage(w))
	}
}

func run() error { return nil }
func warnings() []string { return nil }
`

	if err := os.WriteFile(filepath.Join(root, "cmd", "upbrr", "main.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write CLI sample file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "gui", "main.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write GUI sample file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write internal sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsRawTerminalDiagnosticFields(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "upbrr"), 0o755); err != nil {
		t.Fatalf("mkdir cmd upbrr: %v", err)
	}

	content := `package main

import (
	"fmt"
	"os"
)

type diagnostic struct {
	Warning string
	Message string
	Status string
	Error string
}

func printDiagnostic(status diagnostic) {
	fmt.Fprint(os.Stderr, status.Warning)
	fmt.Fprintln(os.Stderr, status.Message)
	fmt.Fprintln(os.Stderr, status.Status)
	fmt.Fprintln(os.Stderr, status.Error)
}
`

	if err := os.WriteFile(filepath.Join(root, "cmd", "upbrr", "main.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write CLI sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 4 {
		t.Fatalf("expected 4 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "terminal error/warning output") {
			t.Fatalf("expected terminal diagnostic violation, got %q", violation.Message)
		}
	}
}

func TestCheckRepositoryAllowsSanitizedTerminalDiagnosticFields(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "upbrr"), 0o755); err != nil {
		t.Fatalf("mkdir cmd upbrr: %v", err)
	}

	content := `package main

import (
	"fmt"
	"os"

	"github.com/autobrr/upbrr/internal/logging"
)

type diagnostic struct {
	Warning string
	Message string
	Status string
	Error string
}

func printDiagnostic(status diagnostic) {
	fmt.Fprint(os.Stderr, logging.SanitizeMessage(status.Warning))
	fmt.Fprintln(os.Stderr, logging.SanitizeMessage(status.Message))
	fmt.Fprintln(os.Stderr, logging.SanitizeMessage(status.Status))
	fmt.Fprintln(os.Stderr, logging.SanitizeMessage(status.Error))
}
`

	if err := os.WriteFile(filepath.Join(root, "cmd", "upbrr", "main.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write CLI sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsRawResponseBodyLogging(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Tracef(string, ...any) {}

func check(log logger, body []byte) {
	log.Tracef("sample response body: %s", string(body))
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "redacted") {
		t.Fatalf("expected redaction violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryAllowsRedactedResponseBodyLogging(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

import redaction "github.com/autobrr/upbrr/internal/redaction"

type logger struct{}

func (logger) Tracef(string, ...any) {}

func check(log logger, body []byte) {
	log.Tracef("sample response body: %s", redaction.RedactValue(string(body), nil))
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsRawResponseJSONParseErrorOutput(t *testing.T) {
	root := t.TempDir()

	content := `package sample

import (
	"encoding/json"
	"fmt"
)

func check(response struct{ Result struct{ Torrents json.RawMessage } }) error {
	var torrents map[string]map[string]any
	if err := json.Unmarshal(response.Result.Torrents, &torrents); err != nil {
		return fmt.Errorf("sample parse torrents search response: %w", err)
	}
	return nil
}
`

	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "JSON parse errors") {
		t.Fatalf("expected response JSON parse error violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryAllowsRedactedResponseJSONParseErrorOutput(t *testing.T) {
	root := t.TempDir()

	content := `package sample

import (
	"encoding/json"
	"fmt"

	"github.com/autobrr/upbrr/internal/redaction"
)

func check(response struct{ Result struct{ Torrents json.RawMessage } }) error {
	var torrents map[string]map[string]any
	if err := json.Unmarshal(response.Result.Torrents, &torrents); err != nil {
		return fmt.Errorf("sample parse torrents search response: %s", redaction.RedactValue(err.Error(), nil))
	}
	return nil
}
`

	writeInternalFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryAllowsAssignedRedactedResponseBodyLogging(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

import redaction "github.com/autobrr/upbrr/internal/redaction"

type logger struct{}

func (logger) Tracef(string, ...any) {}

func check(log logger, body []byte) {
	redacted := redaction.RedactValue(string(body), nil)
	first, second := redaction.RedactPrivateInfo(string(body)), redaction.RedactValue(string(body), nil)
	log.Tracef("sample response body: %s", redacted)
	log.Tracef("sample response body: %s", first)
	log.Tracef("sample response body: %s", second)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsHelperResponseBodyError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

import "fmt"

func check() error {
	body, _, err := postForm()
	if err != nil {
		return err
	}
	bodyStr := string(body)
	return fmt.Errorf("sample http %d: %s", 500, bodyStr)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "redacted") {
		t.Fatalf("expected redaction violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryFlagsUnboundedHelperResponseBodyPreview(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

import "fmt"

func check() error {
	body, err := readAndCloseResponseBody()
	if err != nil {
		return err
	}
	return fmt.Errorf("sample response: %s", safeResponsePreview(body))
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "must be bounded before redaction") {
		t.Fatalf("expected bounded-read violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryFlagsRawUsernameLogging(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type user struct{ Username string }
type logger struct{}

func (logger) Errorf(string, ...any) {}

func check(log logger, current user) {
	log.Errorf("auth upgrade failed username=%s", current.Username)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "username log arguments") {
		t.Fatalf("expected username redaction violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryAllowsRedactedUsernameLogging(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

import redaction "github.com/autobrr/upbrr/internal/redaction"

type user struct{ Username string }
type logger struct{}

func (logger) Errorf(string, ...any) {}

func check(log logger, current user) {
	username := redaction.RedactValue(current.Username, nil)
	log.Errorf("auth upgrade failed username=%s", username)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsRawErrorsInAuthSensitiveLogs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Errorf(string, ...any) {}

func check(log logger, err error) {
	log.Errorf("auth upgrade failed incident=%s: %v", "auth_upgrade_failed", err)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "auth-sensitive log arguments") {
		t.Fatalf("expected auth-sensitive raw error violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryFlagsRawErrorLogFields(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Warnf(string, ...any) {}

func check(log logger, err error) {
	log.Warnf("core: upload prepared blocked err=%v", err)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "raw error log fields") {
		t.Fatalf("expected raw error log field violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryAllowsRedactedErrorLogFields(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

import "github.com/autobrr/upbrr/internal/redaction"

type logger struct{}

func (logger) Warnf(string, ...any) {}

func check(log logger, err error) {
	log.Warnf("core: upload prepared blocked err=%s", redaction.RedactValue(err.Error(), nil))
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryAllowsBooleanErrorStateLogFields(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Debugf(string, ...any) {}

func check(log logger, detailsErr error) {
	log.Debugf("metadata: scene nfo downloaded details_error=%t", detailsErr != nil)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsUploadRejectionErrorOutputInTests(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "services", "imagehosting"), 0o755); err != nil {
		t.Fatalf("mkdir imagehosting sample: %v", err)
	}

	content := `package imagehosting

import (
	"context"
	"strings"
)

type uploader struct{}

func (u uploader) Upload(context.Context, string) (string, error) {
	return "", nil
}

func TestImgboxUploadRejectedUsesFallbackAfterSanitizingError(t testingT) {
	_, err := uploader{}.Upload(context.Background(), "shot.png")
	if err == nil {
		t.Fatal("expected rejected upload to fail")
	}
	if !strings.Contains(err.Error(), "imgbox upload rejected: unknown error") {
		t.Fatalf("expected unknown error fallback, got %v", err)
	}
	if strings.Contains(err.Error(), "imgbox upload rejected:  ") {
		t.Fatalf("rejection message must not be whitespace-only: %v", err)
	}
}

type testingT interface {
	Fatal(...any)
	Fatalf(string, ...any)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "services", "imagehosting", "uploaders_test.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("expected 2 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "raw errors") {
			t.Fatalf("expected raw error violation, got %q", violation.Message)
		}
	}
}

func TestCheckRepositoryAllowsStableCodesInAuthSensitiveLogs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

import redaction "github.com/autobrr/upbrr/internal/redaction"

type user struct{ Username string }
type logger struct{}

func (logger) Errorf(string, ...any) {}

func check(log logger, current user) {
	log.Errorf(
		"auth upgrade failed incident=%s username=%s",
		"auth_upgrade_failed",
		redaction.RedactValue(current.Username, nil),
	)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsInfofErrorOrientedMessages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Infof(string, ...any) {}

func check(log logger, err error) {
	log.Infof("upload failed: %v", err)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "error-oriented") {
		t.Fatalf("expected error-oriented info violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryFlagsInfofOverlyVerboseMessages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Infof(string, ...any) {}

func check(log logger) {
	log.Infof("sample response body dump for diagnostics and support triage: %s", "...")
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "overly verbose") {
		t.Fatalf("expected overly verbose info violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryAllowsHealthyInfofMessages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Infof(string, ...any) {}

func check(log logger, tracker string) {
	log.Infof("upload completed tracker=%s", tracker)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryAllowsInfofErrorMetricsContext(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Infof(string, ...any) {}

func check(log logger, rate float64) {
	log.Infof("upload error rate=%.2f", rate)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryInfofVerbosityBoundary(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	atBoundary := strings.Repeat("a", maxInfoFormatLength)
	aboveBoundary := strings.Repeat("b", maxInfoFormatLength+1)
	content := "package sample\n\n" +
		"type logger struct{}\n\n" +
		"func (logger) Infof(string, ...any) {}\n\n" +
		"func check(log logger) {\n" +
		"\tlog.Infof(\"" + atBoundary + "\")\n" +
		"\tlog.Infof(\"" + aboveBoundary + "\")\n" +
		"}\n"

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "overly verbose") {
		t.Fatalf("expected overly verbose info violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryFlagsDebugfExecutionFlowMessages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Debugf(string, ...any) {}

func check(log logger) {
	log.Debugf("trackers: unit3d desc part=template len=%d", 100)
	log.Debugf("trackers: description assets start tracker=%s source=%s", "MTV", "/path/to/file")
	log.Debugf("trackers: description assets resolved desc_len=%d screenshots=%d", 1000, 4)
	log.Debugf("trackers: description assets tracker urls source=db tracker=%s records=%d filtered=%d", "AR", 10, 4)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 4 {
		t.Fatalf("expected 4 violations, got %d: %#v", len(violations), violations)
	}
	for _, v := range violations {
		if !strings.Contains(v.Message, "execution flow reporting") {
			t.Fatalf("expected execution flow violation, got %q", v.Message)
		}
	}
}

func TestCheckRepositoryAllowsHealthyDebugfMessages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Debugf(string, ...any) {}

func check(log logger, tracker string) {
	log.Debugf("tracker %s selected due to user preference override", tracker)
	log.Debugf("metadata: media languages audio=%v subs=%v", []string{"eng"}, []string{"eng", "spa"})
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsInfofExecutionFlowMessages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Infof(string, ...any) {}

func check(log logger) {
	log.Infof("tmdb: metadata loaded id=110492 title=\"Peacemaker\" year=2022 type=Scripted")
	log.Infof("tvmaze: search selected id=50603 imdb=13146488 tvdb=391153 candidates=1")
	log.Infof("tvdb: episodes cache hit series_id=391153 language=orig episodes=30")
	log.Infof("tvmaze: episode lookup id=50603 season=2 episode=6 series=\"Peacemaker\"")
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 4 {
		t.Fatalf("expected 4 violations, got %d: %#v", len(violations), violations)
	}
	for _, v := range violations {
		if !strings.Contains(v.Message, "execution flow reporting") {
			t.Fatalf("expected execution flow violation, got %q", v.Message)
		}
	}
}

func TestCheckRepositoryFlagsInfofRoutineCheckMessages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Infof(string, ...any) {}

func check(log logger, path string) {
	log.Infof("dupechecking: ULCX checked for %s raw=0 filtered=0 dupes=false", path)
	log.Infof("dupechecking: MTV checked for %s raw=0 filtered=0 dupes=false", path)
	log.Infof("dupechecking: NBL checked for %s raw=12 filtered=0 dupes=false", path)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("expected 3 violations, got %d: %#v", len(violations), violations)
	}
	for _, v := range violations {
		if !strings.Contains(v.Message, "routine check result") {
			t.Fatalf("expected routine check violation, got %q", v.Message)
		}
	}
}

func TestCheckRepositoryDebugfSkippedWithReason(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Debugf(string, ...any) {}

func check(log logger, path string) {
	log.Debugf("dupechecking: skipped AZ for %s due to rules: rule check failed: major English-language content belongs on PrivateHD", path)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsInfofTroubleshootingDetailMessages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Infof(string, ...any) {}

func check(log logger, tracker string, host string, path string, title string) {
	log.Infof("trackers: preparation built description for %s", tracker)
	log.Infof("image hosting: starting batch upload to %s", host)
	log.Infof("metadata: BTN claim window expired for %q (hours_since_air=%.2f threshold=%d)", title, 4614.31, 48)
	log.Infof("mediainfo: analyzing %s", path)
	log.Infof("clients: no default search client set; searching all qBittorrent clients (%d)", 1)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 5 {
		t.Fatalf("expected 5 violations, got %d: %#v", len(violations), violations)
	}
	for _, v := range violations {
		if !strings.Contains(v.Message, "use Debugf") {
			t.Fatalf("expected use Debugf violation, got %q", v.Message)
		}
	}
}

func TestCheckRepositoryFlagsDebugfErrorOrientedMessages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Debugf(string, ...any) {}

func check(log logger, tracker string) {
	log.Debugf("unit3d: %s search failed (status=%d)", tracker, 429)
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "error-oriented") {
		t.Fatalf("expected error-oriented debug violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryFlagsDebugfClientExecutionFlowMessages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}

	content := `package sample

type logger struct{}

func (logger) Debugf(string, ...any) {}

func check(log logger, path string, client string, hash string) {
	log.Debugf("clients: pathed search clients=%s", client)
	log.Debugf("clients: pathed search running for client %s", client)
	log.Debugf("clients: searching qBittorrent client %s for %s", client, path)
	log.Debugf("clients: qbittorrent searching via qBittorrent proxy")
	log.Debugf("clients: qbittorrent fetched %d torrents", 3)
	log.Debugf("clients: qbittorrent name-matched %d of %d torrents", 3, 3)
	log.Debugf("clients: qbittorrent matched %d torrents", 3)
	log.Debugf("clients: qbittorrent selected hash %s (preferred=%q)", hash, "no_constraints")
	log.Debugf("clients: validated exported torrent for %s (piece=%d)", hash, 4194304)
	log.Debugf("clients: pathed search client %s results matches=%d trackerMatch=%t preferred=%q", client, 3, true, "no_constraints")
	log.Debugf("clients: stopping pathed search after %s (preferred=%q)", client, "no_constraints")
}
`

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 11 {
		t.Fatalf("expected 11 violations, got %d: %#v", len(violations), violations)
	}
	for _, v := range violations {
		if !strings.Contains(v.Message, "execution flow reporting") {
			t.Fatalf("expected execution flow debug violation, got %q", v.Message)
		}
	}
}

func TestCheckRepositoryFlagsWorkflowFunctionWithoutLogging(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"errors"
	"fmt"
)

type logger struct{}
type client struct{}

func (client) Upload(string) error { return nil }

func uploadTracker(log logger, c client, path string) error {
	if path == "" {
		return errors.New("missing path")
	}
	for _, candidate := range []string{path} {
		if err := c.Upload(candidate); err != nil {
			return fmt.Errorf("upload candidate: %w", err)
		}
	}
	return nil
}
`
	writeInternalProductionFixture(t, root, content)
	summary := summarizeFixtureFunction(t, content, "uploadTracker")
	if !summary.isWorkflowLike() {
		t.Fatalf("expected workflow-like summary, got loggerAccess=%t workflowName=%t operations=%d branches=%d loops=%d errorReturns=%d scoreThreshold=%d", summary.loggerAccess, summary.workflowName, summary.operationCalls, summary.branches, summary.loops, summary.errorReturns, workflowLogScoreThreshold)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, "internal", "sample", "sample.go"), content, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "uploadTracker" {
			parsedSummary := summarizeWorkflowFunction(fn)
			if !parsedSummary.isWorkflowLike() {
				t.Fatalf("expected parsed workflow-like summary, got loggerAccess=%t workflowName=%t operations=%d branches=%d loops=%d errorReturns=%d", parsedSummary.loggerAccess, parsedSummary.workflowName, parsedSummary.operationCalls, parsedSummary.branches, parsedSummary.loops, parsedSummary.errorReturns)
			}
		}
	}
	workflowViolations := checkWorkflowLoggingCoverage(fset, "internal/sample/sample.go", file, map[int]*logpolicyAllow{})
	if len(workflowViolations) == 0 {
		t.Fatalf("expected direct workflow violation")
	}
	fileViolations, err := checkFile(token.NewFileSet(), root, filepath.Join(root, "internal", "sample", "sample.go"))
	if err != nil {
		t.Fatalf("checkFile returned error: %v", err)
	}
	if len(fileViolations) == 0 {
		t.Fatalf("expected direct checkFile violation")
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "workflow-like function has no logging") {
		t.Fatalf("expected missing workflow logging violation, got %q", violations[0].Message)
	}
}

func summarizeFixtureFunction(t *testing.T, content string, name string) workflowLogSummary {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "sample.go", content, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return summarizeWorkflowFunction(fn)
		}
	}
	t.Fatalf("function %s not found", name)
	return workflowLogSummary{}
}

func TestCheckRepositoryAllowsWorkflowLoggingSuppression(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import (
	"errors"
	"fmt"
)

type logger struct{}
type client struct{}

func (client) Upload(string) error { return nil }

//logpolicy:allow workflow delegates logging to caller with per-item context
func uploadTracker(log logger, c client, path string) error {
	if path == "" {
		return errors.New("missing path")
	}
	for _, candidate := range []string{path} {
		if err := c.Upload(candidate); err != nil {
			return fmt.Errorf("upload candidate: %w", err)
		}
	}
	return nil
}
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsWorkflowBranchErrorWithoutWarnLogging(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type logger struct{}
type client struct{}
type result struct{}

func (logger) Infof(string, ...any) {}
func (client) Upload(string) error { return nil }

func uploadTracker(log logger, c client, path string) (result, error) {
	log.Infof("upload started path=%s", path)
	if path == "" {
		return result{}, errMissingPath
	}
	if err := c.Upload(path); err != nil {
		return result{}, err
	}
	return result{}, nil
}

var errMissingPath error
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "without Warnf/Errorf") {
		t.Fatalf("expected branch error logging violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryAllowsWorkflowBranchErrorWithWarnLogging(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type logger struct{}
type client struct{}
type result struct{}

func (logger) Infof(string, ...any) {}
func (logger) Warnf(string, ...any) {}
func (client) Upload(string) error { return nil }

func uploadTracker(log logger, c client, path string) (result, error) {
	log.Infof("upload started path=%s", path)
	if path == "" {
		log.Warnf("upload blocked reason=missing_path")
		return result{}, errMissingPath
	}
	if err := c.Upload(path); err != nil {
		log.Warnf("upload blocked reason=client_error")
		return result{}, err
	}
	return result{}, nil
}

var errMissingPath error
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsExportedReceiverWorkflowBranchError(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type logger struct{}
type Service struct{ logger logger }
type client struct{}

func (logger) Infof(string, ...any) {}
func (client) Upload(string) error { return nil }

func (s *Service) Upload(ctx context, c client, path string) error {
	s.logger.Infof("upload started path=%s", path)
	if path == "" {
		return errMissingPath
	}
	if err := c.Upload(path); err != nil {
		return err
	}
	return nil
}

type context struct{}
var errMissingPath error
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "without Warnf/Errorf") {
		t.Fatalf("expected branch error logging violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryAllowsUnexportedReceiverWorkflowBranchError(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type logger struct{}
type service struct{ logger logger }
type client struct{}

func (logger) Infof(string, ...any) {}
func (client) Upload(string) error { return nil }

func (s *service) Upload(ctx context, c client, path string) error {
	s.logger.Infof("upload started path=%s", path)
	if path == "" {
		return errMissingPath
	}
	if err := c.Upload(path); err != nil {
		return err
	}
	return nil
}

type context struct{}
var errMissingPath error
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryAllowsClientReceiverWorkflowBranchError(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type logger struct{}
type Client struct{ logger logger }
type remote struct{}

func (logger) Infof(string, ...any) {}
func (remote) Request(string) error { return nil }

func (c *Client) Upload(ctx context, r remote, path string) error {
	c.logger.Infof("upload started path=%s", path)
	if path == "" {
		return errMissingPath
	}
	if err := r.Request(path); err != nil {
		return err
	}
	return nil
}

type context struct{}
var errMissingPath error
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryAllowsReceiverWorkflowContextualErrorReturns(t *testing.T) {
	root := t.TempDir()
	content := `package sample

import "fmt"

type logger struct{}
type Service struct{ logger logger }
type client struct{}
type result struct{}

func (logger) Infof(string, ...any) {}
func (client) Upload(string) error { return nil }

func (s *Service) Upload(ctx context, c client, path string) (result, error) {
	s.logger.Infof("upload started path=%s", path)
	if path == "" {
		return result{}, fmt.Errorf("missing path")
	}
	if err := c.Upload(path); err != nil {
		return result{}, fmt.Errorf("upload: %w", err)
	}
	return result{}, nil
}

type context struct{}
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsWorkflowDecisionWithoutDecisionLogging(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type logger struct{}
type client struct{}

func (logger) Infof(string, ...any) {}
func (logger) Warnf(string, ...any) {}
func (client) Upload(string) error { return nil }

func uploadTracker(log logger, c client, path string, skipUpload bool) error {
	log.Infof("upload started path=%s", path)
	if skipUpload {
		return nil
	}
	if err := c.Upload(path); err != nil {
		log.Warnf("upload failed path=%s", path)
		return err
	}
	return nil
}
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "workflow decision lacks logging") {
		t.Fatalf("expected workflow decision logging violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryAllowsWorkflowDecisionLogging(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type logger struct{}
type client struct{}

func (logger) Infof(string, ...any) {}
func (logger) Warnf(string, ...any) {}
func (logger) Debugf(string, ...any) {}
func (client) Upload(string) error { return nil }

func uploadTracker(log logger, c client, path string, skipUpload bool) error {
	log.Infof("upload started path=%s", path)
	if skipUpload {
		log.Debugf("upload decision=skip path=%s", path)
		return nil
	}
	if err := c.Upload(path); err != nil {
		log.Warnf("upload failed path=%s", path)
		return err
	}
	return nil
}
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsWorkflowLogsWithoutStableFields(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type logger struct{}
type client struct{}

func (logger) Infof(string, ...any) {}
func (client) Upload(string) {}

func uploadTracker(log logger, c client, paths []string) error {
	log.Infof("upload selected %s %d", paths[0], len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		c.Upload(path)
	}
	return nil
}
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "stable key=value fields") {
		t.Fatalf("expected stable-field violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryAllowsWorkflowLogsWithStableFields(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type logger struct{}
type client struct{}

func (logger) Infof(string, ...any) {}
func (client) Upload(string) {}

func uploadTracker(log logger, c client, paths []string) error {
	log.Infof("upload selected path=%s count=%d", paths[0], len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		c.Upload(path)
	}
	return nil
}
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsWarnfRoutineProgressMessages(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type logger struct{}

func (logger) Warnf(string, ...any) {}

func check(log logger, tracker string) {
	log.Warnf("upload completed tracker=%s", tracker)
}
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "routine progress") {
		t.Fatalf("expected routine progress warning violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryFlagsTracefUserOutcomeMessages(t *testing.T) {
	root := t.TempDir()
	content := `package sample

type logger struct{}

func (logger) Tracef(string, ...any) {}

func check(log logger, tracker string) {
	log.Tracef("upload completed tracker=%s", tracker)
}
`
	writeInternalProductionFixture(t, root, content)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "user-visible outcome") {
		t.Fatalf("expected trace outcome violation, got %q", violations[0].Message)
	}
}

func writeInternalProductionFixture(t *testing.T, root string, content string) {
	t.Helper()

	dir := filepath.Join(root, "internal", "sample")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func writeInternalFixture(t *testing.T, root string, content string) {
	t.Helper()

	dir := filepath.Join(root, "internal", "sample")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
