package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRepositoryFlagsHardcodedRootsInFilepathCalls(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/sample/sample_test.go", `package sample

import "path/filepath"

func check() {
	_ = filepath.Join("C:\\shared", "file.mkv")
	_ = filepath.Join("\\\\server\\share", "file.mkv")
	_ = filepath.Clean("/tmp/file.mkv")
}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("expected 3 violations, got %d: %#v", len(violations), violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation.Message, "hardcoded OS-rooted local paths") {
			t.Fatalf("unexpected violation message: %q", violation.Message)
		}
	}
}

func TestCheckRepositoryAllowsSlashFixturesOutsideFilepathCalls(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/sample/sample_test.go", `package sample

import "path/filepath"

func check(base string) {
	_ = filepath.Join(base, "file.mkv")
	_ = "/media/file.mkv"
	_ = "https://example.invalid/path/file.mkv"
	_ = "C:\\fixture\\file.mkv"
}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryAllowsURLPathsInFilepathCalls(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/sample/sample_test.go", `package sample

import "path/filepath"

func check() {
	_ = filepath.Join("/api", "v1")
	_ = filepath.Join("/upload", "file")
}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsLocalPathStringBuilders(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/sample/sample_test.go", `package sample

import (
	"fmt"
	"strings"
	"testing"
)

func check(t *testing.T, dbPath string, fileParts []string) {
	_ = t.TempDir() + "/upbrr.db"
	_ = dbPath + "\\state.db"
	_ = fmt.Sprintf("%s/%s", dbPath, "state.db")
	_ = strings.Join(fileParts, "/")
}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 4 {
		t.Fatalf("expected 4 violations, got %d: %#v", len(violations), violations)
	}
	messages := strings.Join(violationMessages(violations), "\n")
	for _, want := range []string{"string concatenation", "fmt.Sprintf", "strings.Join"} {
		if !strings.Contains(messages, want) {
			t.Fatalf("expected %q violation, got %q", want, messages)
		}
	}
}

func TestPathSignalClassifiesTorrentPaths(t *testing.T) {
	if isLocalPathName("torrentPath") {
		t.Fatalf("torrentPath should not be classified as a local path signal")
	}
	if isLocalPathName("torrentContentPath") {
		t.Fatalf("torrentContentPath should not be classified as a local path signal")
	}
	if !isSlashDataPathName("torrentContentPath") {
		t.Fatalf("torrentContentPath should be classified as slash-delimited data")
	}
	if !isLocalPathName("torrentFilePath") {
		t.Fatalf("torrentFilePath should remain classified as a local path signal")
	}
}

func TestCheckRepositoryFlagsWrongPathPackageForPathKind(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/sample/sample.go", `package sample

import (
	"net/url"
	"path"
	"path/filepath"
)

func check(state struct{ torrentPath string }, torrentContentPath string, torrentFilePath string, raw string) {
	_ = path.Base(state.torrentPath)
	_ = path.Base(torrentContentPath)
	_ = path.Base(torrentFilePath)
	parsed, _ := url.Parse(raw)
	_ = filepath.Base(parsed.Path)
	_ = filepath.Base(torrentContentPath)
}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("expected 3 violations, got %d: %#v", len(violations), violations)
	}
	messages := strings.Join(violationMessages(violations), "\n")
	if !strings.Contains(messages, "use filepath for local filesystem paths") {
		t.Fatalf("expected path package violation, got %q", messages)
	}
	if !strings.Contains(messages, "slash-delimited torrent/API/URL paths") {
		t.Fatalf("expected filepath slash-data violation, got %q", messages)
	}
}

func TestCheckRepositoryFlagsFilesystemCallsWithSlashData(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/sample/sample.go", `package sample

import "os"

func check(apiPath string) {
	_, _ = os.ReadFile(apiPath)
}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "filepath.FromSlash") {
		t.Fatalf("expected FromSlash violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryFlagsSlashAssertionsAgainstLocalPaths(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/sample/sample_test.go", `package sample

import "strings"

func check(dbPath string) bool {
	return strings.Contains(dbPath, "/tmp/upbrr.db")
}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "filepath.ToSlash") {
		t.Fatalf("expected ToSlash violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryFlagsAdHocPathGuards(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/sample/sample.go", `package sample

func pathWithinRoot(root string, target string) bool {
	return len(root) <= len(target)
}

func samePath(left string, right string) bool {
	return left == right
}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("expected 2 violations, got %d: %#v", len(violations), violations)
	}
	messages := strings.Join(violationMessages(violations), "\n")
	if !strings.Contains(messages, "internal/pathing") {
		t.Fatalf("expected pathutil violation, got %q", messages)
	}
}

func TestCheckRepositoryFlagsLexicalRootTargetPathEquality(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/sample/sample.go", `package sample

func check(absRoot string, absTarget string) bool {
	return absTarget == absRoot
}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %#v", len(violations), violations)
	}
	if !strings.Contains(violations[0].Message, "pathutil.SamePath") {
		t.Fatalf("expected SamePath violation, got %q", violations[0].Message)
	}
}

func TestCheckRepositoryAllowsPathutilPathGuards(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/pathing/pathutil.go", `package pathing

func IsWithinRoot(root string, target string) bool {
	return root == target
}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryAllowsIntentionalPathPolicyComments(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/sample/sample_test.go", `package sample

import "testing"

func check(t *testing.T) string {
	//pathpolicy:allow exercising checker fixture
	return t.TempDir() + "/upbrr.db"
}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %#v", violations)
	}
}

func TestCheckRepositoryFlagsInvalidPathPolicyAllows(t *testing.T) {
	root := t.TempDir()
	writeSample(t, root, "internal/sample/sample.go", `package sample

//pathpolicy:allow
func blankReason() {}

//pathpolicy:allow no longer needed
func stale() {}
`)

	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("CheckRepository returned error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("expected 2 violations, got %d: %#v", len(violations), violations)
	}
	messages := strings.Join(violationMessages(violations), "\n")
	if !strings.Contains(messages, "requires a reason") || !strings.Contains(messages, "unused") {
		t.Fatalf("expected invalid allow violations, got %q", messages)
	}
}

func violationMessages(violations []Violation) []string {
	messages := make([]string, 0, len(violations))
	for _, violation := range violations {
		messages = append(messages, violation.Message)
	}
	return messages
}

func writeSample(t *testing.T, root string, path string, content string) {
	t.Helper()

	fullPath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir sample dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}
}
