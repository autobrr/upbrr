package logpolicy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const countFixture = `package sample
import format "fmt"
type logger struct{}
func (logger) Tracef(string, ...interface{}) {}
func (logger) Debugf(string, ...any) {}
func (logger) Infof(string, ...any) {}
func (logger) Warnf(string, ...any) {}
func (logger) Errorf(string, ...any) {}
type unrelated struct{}
func (unrelated) Infof(string, ...any) {}
func example(log logger, other unrelated) {
  _ = format.Errorf("not a log")
  // log.Errorf("not a call")
  _ = "log.Errorf(\"not a call\")"
  other.Infof("not a logger")
%s
}
`

func TestCountAddedPRDiff(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func() string {
		t.Helper()
		git("add", ".")
		git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture")
		return git("rev-parse", "HEAD")
	}
	git("init", "-q")
	write("go.mod", "module example.invalid/counts\n\ngo 1.26.0\n")
	write("sample.go", strings.Replace(countFixture, "%s", "  log.Infof(\"old\")\n  log.Warnf(\n    \"unchanged\",\n  )", 1))
	base := commit()
	write("sample.go", strings.Replace(countFixture, "%s", `  log.Errorf("changed level")
  log.Warnf(
    // added comment alone is not a logging statement
    "unchanged",
  )
  log.Tracef("first"); log.Debugf("second")
  var sink interface { Debugf(string, ...any) } = log
  sink.Debugf("narrow logger")
  log.Infof(
    "multiline %d",
    1,
  )`, 1))
	// Windows-only source is analyzed too, without counting shared files twice.
	write("windows_windows.go", "package sample\nfunc windows(log logger) { log.Warnf(\"windows\") }\n")
	write("tagged.go", "//go:build e2e\n\npackage sample\nfunc tagged(log logger) { log.Tracef(\"tagged\") }\n")
	head := commit()
	want := AddedCounts{
		Trace: 2,
		Debug: 2,
		Info:  1,
		Warn:  1,
		Error: 1,
	}
	got, err := CountAdded(t.Context(), root, base, head)
	if err != nil || got != want {
		t.Fatalf("full diff counts = %+v, %v; want %+v", got, err, want)
	}
	// Renames and a later comment-only commit still use the full original PR diff.
	git("mv", "sample.go", "renamed.go")
	write("comment.go", "package sample\n// a later commit\n")
	head = commit()
	got, err = CountAdded(t.Context(), root, base, head)
	if err != nil || got != want {
		t.Fatalf("successive head = %+v, %v; want %+v", got, err, want)
	}
	// Removing all newly added calls must produce zero, not retain old counts.
	write("renamed.go", strings.Replace(countFixture, "%s", "  // removed all calls", 1))
	if err := os.Remove(filepath.Join(root, "windows_windows.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "tagged.go")); err != nil {
		t.Fatal(err)
	}
	head = commit()
	got, err = CountAdded(t.Context(), root, base, head)
	if err != nil || got != (AddedCounts{}) {
		t.Fatalf("removals = %+v, %v; want zero", got, err)
	}
	write("renamed.go", "package sample\nfunc invalid(\n")
	head = commit()
	if got, err := CountAdded(t.Context(), root, base, head); err == nil || got != (AddedCounts{}) {
		t.Fatalf("analysis failure must not return usable counts: %+v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CountAdded(ctx, root, base, head); err == nil {
		t.Fatal("canceled analysis must fail")
	}
}
