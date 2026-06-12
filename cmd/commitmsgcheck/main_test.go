package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateMessageValid(t *testing.T) {
	t.Parallel()

	result := validateMessage("fix(BT): correct duplicate search payload\n")
	if len(result.errors) != 0 {
		t.Fatalf("expected no validation errors, got %v", result.errors)
	}
}

func TestValidateMessageAllowsBreakingWithoutScope(t *testing.T) {
	t.Parallel()

	// Bang marker with no BREAKING CHANGE footer is allowed but warns.
	result := validateMessage("feat!: drop Go 1.19 support\n")
	if len(result.errors) != 0 {
		t.Fatalf("expected no validation errors, got %v", result.errors)
	}
	if len(result.warnings) != 1 {
		t.Fatalf("expected one warning about missing BREAKING CHANGE footer, got %v", result.warnings)
	}
}

func TestValidateMessageRejectsInvalidType(t *testing.T) {
	t.Parallel()

	result := validateMessage("feature(config): add YAML importer\n")
	if len(result.errors) == 0 {
		t.Fatal("expected invalid type to fail validation")
	}
}

func TestValidateMessageRejectsUppercaseType(t *testing.T) {
	t.Parallel()

	result := validateMessage("Fix(config): add YAML importer\n")
	if len(result.errors) != 1 {
		t.Fatalf("expected exactly one error (lower-case only, not 'not allowed'), got %v", result.errors)
	}
	if !containsSubstring(result.errors, "lower-case") {
		t.Fatalf("expected lower-case error, got %v", result.errors)
	}
}

func TestValidateMessageUppercaseUnknownTypeReportsNotAllowed(t *testing.T) {
	t.Parallel()

	// Unknown type in any case → report "not allowed" (no redundant lower-case error).
	result := validateMessage("Feature(config): add thing\n")
	if !containsSubstring(result.errors, "is not allowed") {
		t.Fatalf("expected not-allowed error, got %v", result.errors)
	}
	if containsSubstring(result.errors, "lower-case") {
		t.Fatalf("should not emit both errors for unknown uppercase type, got %v", result.errors)
	}
}

func TestValidateMessageRejectsUppercaseSubject(t *testing.T) {
	t.Parallel()

	result := validateMessage("fix(config): Add YAML importer\n")
	if len(result.errors) == 0 {
		t.Fatal("expected uppercase subject to fail validation")
	}
}

func TestValidateMessageRejectsTrailingPeriod(t *testing.T) {
	t.Parallel()

	result := validateMessage("docs: update README.\n")
	if len(result.errors) == 0 {
		t.Fatal("expected trailing period to fail validation")
	}
}

func TestValidateMessageWarnsOnLongBodyLine(t *testing.T) {
	t.Parallel()

	bodyLine := strings.Repeat("a", bodyMaxLineLength+1)
	result := validateMessage("fix: keep conventional commit validation local\n\n" + bodyLine + "\n")
	if len(result.errors) != 0 {
		t.Fatalf("expected no validation errors, got %v", result.errors)
	}

	if len(result.warnings) != 1 {
		t.Fatalf("expected one warning, got %v", result.warnings)
	}
}

func TestValidateMessageIgnoresMergeCommits(t *testing.T) {
	t.Parallel()

	result := validateMessage("Merge branch 'main' into feature/test\n")
	if len(result.errors) != 0 {
		t.Fatalf("expected merge commit to be ignored, got %v", result.errors)
	}
}

func TestValidateMessageIgnoresFixupAndSquash(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{
		"fixup! fix: earlier commit\n",
		"squash! fix: earlier commit\n",
		"Revert \"feat: add thing\"\n",
	} {
		result := validateMessage(msg)
		if len(result.errors) != 0 {
			t.Fatalf("%q: expected ignored, got %v", msg, result.errors)
		}
	}
}

func TestCleanMessageStripsGitComments(t *testing.T) {
	t.Parallel()

	lines := cleanMessage("fix: add validator\n\nBody\n# Please enter the commit message\n# ------------------------ >8 ------------------------\nignored\n")
	expected := []string{"fix: add validator", "", "Body"}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d: %v", len(expected), len(lines), lines)
	}

	for idx := range expected {
		if lines[idx] != expected[idx] {
			t.Fatalf("expected line %d to be %q, got %q", idx, expected[idx], lines[idx])
		}
	}
}

func TestCleanMessageNormalizesCRLF(t *testing.T) {
	t.Parallel()

	lines := cleanMessage("fix: x\r\n\r\nbody\r\n")
	expected := []string{"fix: x", "", "body"}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d: %v", len(expected), len(lines), lines)
	}
	for i, want := range expected {
		if lines[i] != want {
			t.Fatalf("line %d: want %q got %q", i, want, lines[i])
		}
	}
}

func TestDiagnoseHeaderMissingColon(t *testing.T) {
	t.Parallel()

	result := validateMessage("fix add validator\n")
	if !containsSubstring(result.errors, "missing ': '") {
		t.Fatalf("expected missing-colon diagnostic, got %v", result.errors)
	}
}

func TestDiagnoseHeaderMissingTypeBeforeColon(t *testing.T) {
	t.Parallel()

	result := validateMessage(": add validator\n")
	if !containsSubstring(result.errors, "missing a type") {
		t.Fatalf("expected missing-type diagnostic, got %v", result.errors)
	}
}

func TestDiagnoseHeaderEmptySubject(t *testing.T) {
	t.Parallel()

	result := validateMessage("fix:\n")
	if !containsSubstring(result.errors, "subject must not be empty") {
		t.Fatalf("expected empty-subject diagnostic, got %v", result.errors)
	}
}

func TestDiagnoseHeaderMissingSpaceAfterColon(t *testing.T) {
	t.Parallel()

	result := validateMessage("fix:add validator\n")
	if !containsSubstring(result.errors, "exactly one space after ':'") {
		t.Fatalf("expected missing-space diagnostic, got %v", result.errors)
	}
}

func TestDiagnoseHeaderDoubleSpaceAfterColon(t *testing.T) {
	t.Parallel()

	result := validateMessage("fix:  add validator\n")
	if !containsSubstring(result.errors, "exactly one space after ':'") {
		t.Fatalf("expected double-space diagnostic, got %v", result.errors)
	}
}

func TestDiagnoseHeaderTabAfterColonSpace(t *testing.T) {
	t.Parallel()

	// "fix: \tadd thing" — the regex consumes the single space after ':' and captures
	// "\tadd thing" as subject. validateHeader must still flag the stray tab.
	result := validateMessage("fix: \tadd validator\n")
	if !containsSubstring(result.errors, "exactly one space after ':'") {
		t.Fatalf("expected extra-whitespace diagnostic, got %v", result.errors)
	}
}

func TestDiagnoseHeaderNBSPAfterColonSpace(t *testing.T) {
	t.Parallel()

	// U+00A0 NO-BREAK SPACE after the single separator space should also be flagged.
	result := validateMessage("fix: \u00a0add validator\n")
	if !containsSubstring(result.errors, "exactly one space after ':'") {
		t.Fatalf("expected extra-whitespace diagnostic for NBSP, got %v", result.errors)
	}
}

func TestBodyFooterSplit(t *testing.T) {
	t.Parallel()

	raw := "fix: add validator\n\nBody line 1\nBody line 2\n\nCloses: #42\nSigned-off-by: Someone <someone@example.com>\n"
	lines := cleanMessage(raw)
	parsed := splitBodyFooter(lines)

	if parsed.header != "fix: add validator" {
		t.Fatalf("header mismatch: %q", parsed.header)
	}
	wantBody := []string{"Body line 1", "Body line 2"}
	if !equalSlices(parsed.body, wantBody) {
		t.Fatalf("body: want %v got %v", wantBody, parsed.body)
	}
	wantFooter := []string{"Closes: #42", "Signed-off-by: Someone <someone@example.com>"}
	if !equalSlices(parsed.footer, wantFooter) {
		t.Fatalf("footer: want %v got %v", wantFooter, parsed.footer)
	}
}

func TestBodyFooterSplitNoFooter(t *testing.T) {
	t.Parallel()

	raw := "fix: add validator\n\nJust a body paragraph with no footer.\n"
	parsed := splitBodyFooter(cleanMessage(raw))
	if len(parsed.footer) != 0 {
		t.Fatalf("expected empty footer, got %v", parsed.footer)
	}
	if len(parsed.body) != 1 || parsed.body[0] != "Just a body paragraph with no footer." {
		t.Fatalf("unexpected body: %v", parsed.body)
	}
}

func TestBreakingChangeFooterWithDescription(t *testing.T) {
	t.Parallel()

	result := validateMessage("feat: drop v1\n\nBody.\n\nBREAKING CHANGE: v1 endpoints removed\n")
	if len(result.errors) != 0 {
		t.Fatalf("expected no errors, got %v", result.errors)
	}
	if len(result.warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", result.warnings)
	}
}

func TestBreakingChangeFooterMissingDescription(t *testing.T) {
	t.Parallel()

	result := validateMessage("feat!: drop v1\n\nBody.\n\nBREAKING CHANGE:\n")
	if !containsSubstring(result.errors, "must have a description") {
		t.Fatalf("expected BREAKING CHANGE description error, got %v", result.errors)
	}
}

func TestBreakingChangeHyphenatedToken(t *testing.T) {
	t.Parallel()

	result := validateMessage("feat!: drop v1\n\nBody.\n\nBREAKING-CHANGE: v1 endpoints removed\n")
	if len(result.errors) != 0 {
		t.Fatalf("expected no errors, got %v", result.errors)
	}
	if len(result.warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", result.warnings)
	}
}

func TestBangWithoutFooterWarns(t *testing.T) {
	t.Parallel()

	result := validateMessage("feat!: drop v1\n\nBody without footer.\n")
	if len(result.errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.errors)
	}
	if !containsSubstring(result.warnings, "no `BREAKING CHANGE:` footer") {
		t.Fatalf("expected missing-footer warning, got %v", result.warnings)
	}
}

func TestMalformedFooterTreatedAsBody(t *testing.T) {
	t.Parallel()

	result := validateMessage("fix: thing\n\nbody\n\nNot a real footer line\nAlso not one\n")
	if len(result.errors) != 0 {
		t.Fatalf("body-only paragraphs should not error, got %v", result.errors)
	}
}

func TestFooterPartialMalformed(t *testing.T) {
	t.Parallel()

	result := validateMessage("fix: thing\n\nbody\n\nCloses: #1\nrandom nonsense\n")
	if len(result.errors) != 0 {
		t.Fatalf("expected no errors when last block isn't fully footer-shaped, got %v", result.errors)
	}
}

func TestFooterLongLineWarns(t *testing.T) {
	t.Parallel()

	longValue := strings.Repeat("x", footerMaxLineLength)
	result := validateMessage("fix: thing\n\nbody\n\nCo-Authored-By: " + longValue + "\n")
	if len(result.errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.errors)
	}
	if len(result.warnings) != 1 {
		t.Fatalf("expected one footer-length warning, got %v", result.warnings)
	}
	if !strings.Contains(result.warnings[0], "footer line") {
		t.Fatalf("expected footer-labeled warning, got %q", result.warnings[0])
	}
}

func TestValidateMessageEmptyMessage(t *testing.T) {
	t.Parallel()

	result := validateMessage("\n\n\n")
	if !containsSubstring(result.errors, "empty") {
		t.Fatalf("expected empty-message error, got %v", result.errors)
	}
}

func TestValidateMessageHeaderExceedsLimit(t *testing.T) {
	t.Parallel()

	subject := strings.Repeat("a", headerMaxLength)
	result := validateMessage("fix: " + subject + "\n")
	if !containsSubstring(result.errors, "header exceeds") {
		t.Fatalf("expected header-length error, got %v", result.errors)
	}
}

func TestShouldIgnoreHeader(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"Merge pull request":     true,
		"Merge branch":           true,
		"fixup! fix: x":          true,
		"squash! feat: x":        true,
		"Revert \"feat: add x\"": true,
		"feat: add x":            false,
		"revert: undo abc123":    false,
	}
	for header, want := range cases {
		if got := shouldIgnoreHeader(header); got != want {
			t.Errorf("shouldIgnoreHeader(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestCommandErrorUnwrapping(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main", ".")
	cmd := exec.CommandContext(context.Background(), "git", "show", "-s", "--format=%B", "nonexistent-ref-deadbeef")
	cmd.Dir = dir
	_, err := cmd.Output()
	if err == nil {
		t.Skip("expected git to fail; skipping")
	}
	wrapped := commandError(err)
	// Original ExitError.Error() is like "exit status 128". Wrapped should append stderr.
	if !strings.Contains(wrapped.Error(), "fatal") && !strings.Contains(wrapped.Error(), "unknown revision") && !strings.Contains(wrapped.Error(), "bad revision") {
		t.Fatalf("expected wrapped error to include git stderr, got %q", wrapped.Error())
	}
}

func TestValidateRangeUsesGitRevList(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main", ".")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-m", "chore: initial")
	writeFile(t, filepath.Join(dir, "a.txt"), "b")
	runGit(t, dir, "commit", "-am", "fix: second")
	writeFile(t, filepath.Join(dir, "a.txt"), "c")
	runGit(t, dir, "commit", "-am", "BAD MESSAGE")

	base := revParse(t, dir, "HEAD~2")
	head := revParse(t, dir, "HEAD")

	t.Chdir(dir)

	exitCode, err := validateRange(base, head)
	if err != nil {
		t.Fatalf("validateRange error: %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("expected exit code 1 due to bad commit, got %d", exitCode)
	}
}

func TestValidateRangeShallowCloneHint(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main", ".")
	t.Chdir(dir)
	_, err := validateRange("deadbeefdeadbeef", "cafebabecafebabe")
	if err == nil {
		t.Fatal("expected error for unknown refs")
	}
	if !strings.Contains(err.Error(), "shallow clone") {
		t.Fatalf("expected shallow-clone hint in error, got %v", err)
	}
}

// helpers

func containsSubstring(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func revParse(t *testing.T, dir, rev string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", rev)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", rev, err)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
