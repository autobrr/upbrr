package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestChangedPackages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	got := changedPackages(root, []string{
		"internal/metadata/thexem/changed.go",
		"internal/metadata/thexem/other.go",
		"internal/services/trackericon/changed.go",
	})
	want := []string{
		filepath.Join(root, "internal", "metadata", "thexem"),
		filepath.Join(root, "internal", "services", "trackericon"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changedPackages() = %v, want %v", got, want)
	}
}

func TestFilterDiffKeepsOnlyChangedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	changedPath := filepath.Join(root, "internal", "metadata", "thexem", "changed.go")
	unchangedPath := filepath.Join(root, "internal", "metadata", "thexem", "client.go")
	diff := strings.Join([]string{
		"--- " + changedPath + " (old)",
		"+++ " + changedPath + " (new)",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"--- " + unchangedPath + " (old)",
		"+++ " + unchangedPath + " (new)",
		"@@ -1 +1 @@",
		"-old runtime form",
		"+modern runtime form",
		"",
	}, "\n")

	got, err := filterDiff([]byte(diff), root, []string{"internal/metadata/thexem/changed.go"})
	if err != nil {
		t.Fatalf("filterDiff() error = %v", err)
	}
	gotText := string(got)
	if !strings.Contains(gotText, "+++ b/internal/metadata/thexem/changed.go") {
		t.Fatalf("filtered diff omitted changed file:\n%s", gotText)
	}
	if strings.Contains(gotText, "client.go") || strings.Contains(gotText, "modern runtime form") {
		t.Fatalf("filtered diff retained unchanged file:\n%s", gotText)
	}
}
