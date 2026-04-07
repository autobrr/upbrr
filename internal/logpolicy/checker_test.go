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
