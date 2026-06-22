package logpolicy

import (
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

func TestCheckRepositoryIgnoresTestsAndContextualLogs(t *testing.T) {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(mainContent), 0o644); err != nil {
		t.Fatalf("write main sample file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample_test.go"), []byte(testContent), 0o644); err != nil {
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

func TestCheckRepositoryFlagsRawDryRunDetailsOutput(t *testing.T) {
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
}

func printDryRunDetails(entry dryRun, key string) {
	fmt.Printf("Endpoint: %s\n", entry.Endpoint)
	fmt.Printf("- %s: %s\n", key, entry.Payload[key])
}
`

	if err := os.WriteFile(filepath.Join(root, "cmd", "upbrr", "interactive.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("expected 2 violations, got %d: %#v", len(violations), violations)
	}
	joined := violations[0].Message + "\n" + violations[1].Message
	if !strings.Contains(joined, "dry-run endpoint output") {
		t.Fatalf("expected endpoint redaction violation, got %q", joined)
	}
	if !strings.Contains(joined, "dry-run payload output") {
		t.Fatalf("expected payload redaction violation, got %q", joined)
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
}

func safeDryRunEndpoint(value string) string { return value }
func formatDryRunPayloadValue(key string, value string) string { return value }

func printDryRunDetails(entry dryRun, key string) {
	fmt.Printf("Endpoint: %s\n", safeDryRunEndpoint(entry.Endpoint))
	fmt.Printf("- %s: %s\n", key, formatDryRunPayloadValue(key, entry.Payload[key]))
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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

	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "sample.go"), []byte(content), 0o644); err != nil {
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
