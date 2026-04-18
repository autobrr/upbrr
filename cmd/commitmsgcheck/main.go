package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"unicode"
)

const (
	headerMaxLength = 115
	lineMaxLength   = 115
)

var (
	allowedTypes = map[string]struct{}{
		"build":    {},
		"chore":    {},
		"ci":       {},
		"docs":     {},
		"feat":     {},
		"fix":      {},
		"perf":     {},
		"refactor": {},
		"revert":   {},
		"style":    {},
		"test":     {},
	}

	headerPattern = regexp.MustCompile(`^([a-z]+)(?:\(([^()\r\n]+)\))?(!)?: (.+)$`)
)

type validationResult struct {
	errors   []string
	warnings []string
}

func main() {
	os.Exit(run())
}

func run() int {
	var (
		from = flag.String("from", "", "base commit (exclusive) for range validation")
		to   = flag.String("to", "", "head commit (inclusive) for range validation")
	)
	flag.Parse()

	switch {
	case *from != "" || *to != "":
		if *from == "" || *to == "" {
			fmt.Fprintln(os.Stderr, "both --from and --to are required when validating a commit range")
			return 2
		}

		exitCode, err := validateRange(*from, *to)
		if err != nil {
			fmt.Fprintf(os.Stderr, "commit message validation failed: %v\n", err)
			return 1
		}

		return exitCode
	default:
		if flag.NArg() != 1 {
			fmt.Fprintf(os.Stderr, "usage: %s [--from <base> --to <head>] <commit-msg-file>\n", os.Args[0])
			return 2
		}

		content, err := os.ReadFile(flag.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "read commit message: %v\n", err)
			return 1
		}

		result := validateMessage(string(content))
		printResult("commit message", result)
		if len(result.errors) > 0 {
			return 1
		}

		return 0
	}
}

func validateRange(from, to string) (int, error) {
	revisionRange := fmt.Sprintf("%s..%s", from, to)
	cmd := exec.Command("git", "rev-list", "--reverse", revisionRange)
	output, err := cmd.Output()
	if err != nil {
		return 1, fmt.Errorf("list commits in %s: %w", revisionRange, commandError(err))
	}

	shas := strings.Fields(string(output))
	exitCode := 0
	for _, sha := range shas {
		message, err := gitCommitMessage(sha)
		if err != nil {
			return 1, err
		}

		result := validateMessage(message)
		printResult("commit "+shortSHA(sha), result)
		if len(result.errors) > 0 {
			exitCode = 1
		}
	}

	return exitCode, nil
}

func gitCommitMessage(sha string) (string, error) {
	cmd := exec.Command("git", "show", "-s", "--format=%B", sha)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read commit %s: %w", shortSHA(sha), commandError(err))
	}

	return string(output), nil
}

func validateMessage(raw string) validationResult {
	lines := cleanMessage(raw)
	if len(lines) == 0 {
		return validationResult{errors: []string{"commit message is empty"}}
	}

	header := lines[0]
	if shouldIgnoreHeader(header) {
		return validationResult{}
	}

	result := validationResult{}

	if header != strings.TrimSpace(header) {
		result.errors = append(result.errors, "header must not start or end with whitespace")
	}

	if len(header) > headerMaxLength {
		result.errors = append(result.errors, fmt.Sprintf("header exceeds %d characters (%d)", headerMaxLength, len(header)))
	}

	match := headerPattern.FindStringSubmatch(header)
	if match == nil {
		result.errors = append(result.errors, "header must match `<type>(<scope>)!?: <subject>`")
		return appendLineWarnings(result, lines)
	}

	commitType := match[1]
	subject := match[4]

	if _, ok := allowedTypes[commitType]; !ok {
		result.errors = append(result.errors, fmt.Sprintf("type %q is not allowed", commitType))
	}

	if strings.TrimSpace(subject) == "" {
		result.errors = append(result.errors, "subject must not be empty")
	}

	if strings.HasSuffix(subject, ".") {
		result.errors = append(result.errors, "subject must not end with '.'")
	}

	if startsWithUpper(subject) {
		result.errors = append(result.errors, "subject must not start with an uppercase letter")
	}

	return appendLineWarnings(result, lines)
}

func appendLineWarnings(result validationResult, lines []string) validationResult {
	if len(lines) <= 1 {
		return result
	}

	for idx, line := range lines[1:] {
		if line == "" {
			continue
		}

		if len(line) > lineMaxLength {
			result.warnings = append(result.warnings, fmt.Sprintf("line %d exceeds %d characters (%d)", idx+2, lineMaxLength, len(line)))
		}
	}

	return result
}

func cleanMessage(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")

	lines := strings.Split(raw, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "# ------------------------ >8 ------------------------" {
			break
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		cleaned = append(cleaned, strings.TrimRight(line, " \t"))
	}

	for len(cleaned) > 0 && cleaned[len(cleaned)-1] == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}

	return cleaned
}

func shouldIgnoreHeader(header string) bool {
	switch {
	case strings.HasPrefix(header, "Merge "):
		return true
	case strings.HasPrefix(header, "fixup! "):
		return true
	case strings.HasPrefix(header, "squash! "):
		return true
	case strings.HasPrefix(header, "Revert \""):
		return true
	default:
		return false
	}
}

func startsWithUpper(value string) bool {
	for _, r := range value {
		if !unicode.IsLetter(r) {
			continue
		}

		return unicode.IsUpper(r)
	}

	return false
}

func printResult(label string, result validationResult) {
	for _, warning := range result.warnings {
		fmt.Fprintf(os.Stderr, "warning: %s: %s\n", label, warning)
	}

	if len(result.errors) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "%s is invalid:\n", label)
	for _, issue := range result.errors {
		fmt.Fprintf(os.Stderr, "  - %s\n", issue)
	}
}

func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}

	return sha[:7]
}

func commandError(err error) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return err
	}

	stderr := strings.TrimSpace(string(exitErr.Stderr))
	if stderr == "" {
		return err
	}

	return fmt.Errorf("%w: %s", err, stderr)
}
