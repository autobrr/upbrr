package main

import (
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

	result := validateMessage("feat!: drop Go 1.19 support\n")
	if len(result.errors) != 0 {
		t.Fatalf("expected no validation errors, got %v", result.errors)
	}
}

func TestValidateMessageRejectsInvalidType(t *testing.T) {
	t.Parallel()

	result := validateMessage("feature(config): add YAML importer\n")
	if len(result.errors) == 0 {
		t.Fatal("expected invalid type to fail validation")
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

	bodyLine := strings.Repeat("a", lineMaxLength+1)
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
