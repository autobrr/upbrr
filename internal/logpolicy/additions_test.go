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
type pointerLogger struct { logger }
func (*pointerLogger) Warnf(string, ...any) {}
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
	write("sample.go", strings.Replace(countFixture, "%s", "  log.Infof(\"old\")\n  log.Warnf(\n    \"unchanged\",\n  )\n  log.Tracef(\"trailing\") // old", 1))
	base := commit()
	write("sample.go", strings.Replace(countFixture, "%s", `  log.Errorf("changed level")
  log.Warnf(
    // added comment alone is not a logging statement
    "unchanged",
  )
  log.Tracef("trailing") // updated comment on an added code line
  log.Tracef("first"); log.Debugf("second")
  var holder struct { logger pointerLogger }
  holder.logger.Warnf("addressable value field")
  var sink interface { Debugf(string, ...any) } = log
  sink.Debugf("narrow logger")
  log.Infof(
    "multiline %d",
    1,
  )`, 1))
	// Windows-only source is analyzed too, without counting shared files twice.
	write("windows_windows.go", "package sample\nfunc windows(log logger) { log.Warnf(\"windows\") }\n")
	write("tagged.go", "//go:build e2e\n\npackage sample\nfunc tagged(log logger) { log.Tracef(\"tagged\") }\n")
	// The same call is unrelated on Linux/macOS and a real logger on Windows.
	write("platform_other.go", "//go:build !windows\n\npackage sample\ntype platformLogger struct{}\nfunc (platformLogger) Warnf(string, ...any) {}\n")
	write("platform_windows.go", "package sample\ntype platformLogger struct { logger }\nfunc (*platformLogger) Warnf(string, ...any) {}\n")
	write("shared.go", "package sample\nfunc shared(log platformLogger) { log.Warnf(\"platform logger\") }\n")
	head := commit()
	want := AddedCounts{
		Trace: 3,
		Debug: 2,
		Info:  1,
		Warn:  3,
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
	for _, name := range []string{"windows_windows.go", "tagged.go", "platform_other.go", "platform_windows.go", "shared.go"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	head = commit()
	got, err = CountAdded(t.Context(), root, base, head)
	if err != nil || got != (AddedCounts{}) {
		t.Fatalf("removals = %+v, %v; want zero", got, err)
	}
	// No supported build context selects this file. Fail closed rather than
	// present a partial total as the complete PR diff.
	write("unsupported.go", "//go:build !linux && !darwin && !windows\n\npackage sample\nfunc unsupported(log logger) { log.Errorf(\"unsupported\") }\n")
	head = commit()
	if got, err := CountAdded(t.Context(), root, base, head); err == nil || got != (AddedCounts{}) || !strings.Contains(err.Error(), "category=unsupported-context") {
		t.Fatalf("unsupported context must fail without partial counts: %+v, %v", got, err)
	}
	if err := os.Remove(filepath.Join(root, "unsupported.go")); err != nil {
		t.Fatal(err)
	}
	write("renamed.go", "package sample\nfunc invalid() { private_fixture_marker() }\n")
	head = commit()
	if got, err := CountAdded(t.Context(), root, base, head); err == nil || got != (AddedCounts{}) ||
		!strings.Contains(err.Error(), "category=type os=linux tags=default") || strings.Contains(err.Error(), "private_fixture_marker") {
		t.Fatalf("type errors must have safe build context, without source text: %+v, %v", got, err)
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
