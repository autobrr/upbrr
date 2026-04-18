package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const (
	programName         = "commitmsgcheck"
	headerMaxLength     = 115
	bodyMaxLineLength   = 115
	footerMaxLineLength = 115
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

	// headerPattern accepts `<type>(<scope>)!?: <subject>` with optional scope and
	// breaking-change marker. The regex allows any letters in the type so we can
	// surface a specific "must be lower-case" error; allowedTypes enforces the final
	// set. The literal ` ` after `:` covers the minimum — a following TrimLeft check
	// in validateHeader rejects multiple spaces.
	headerPattern = regexp.MustCompile(`^([a-zA-Z]+)(?:\(([^()\r\n]+)\))?(!)?: (.+)$`)

	// Matches `<token>: <value>` or `<token> #<issue>` footer lines. Tokens are
	// kebab-case per Conventional Commits, with `BREAKING CHANGE` as the one
	// whitespace-containing exception.
	footerPattern = regexp.MustCompile(`^(BREAKING[ -]CHANGE|[A-Za-z][A-Za-z-]*)(: | #).+$`)

	// Matches any BREAKING CHANGE / BREAKING-CHANGE footer prefix so we can detect
	// present-but-empty descriptions that footerPattern otherwise rejects generically.
	breakingPrefixPattern = regexp.MustCompile(`^BREAKING[ -]CHANGE:\s*(.*)$`)
)

type validationResult struct {
	errors   []string
	warnings []string
}

type parsedMessage struct {
	header string
	body   []string
	footer []string
}

func main() {
	os.Exit(run())
}

func run() int {
	var (
		from = flag.String("from", "", "base commit (exclusive) for range validation")
		to   = flag.String("to", "", "head commit (inclusive) for range validation")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [--from <base> --to <head>] <commit-msg-file>\n", programName)
		flag.PrintDefaults()
	}
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
			flag.Usage()
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
	// --end-of-options (git ≥ 2.24) stops option parsing so a `-`-prefixed user input
	// is treated as a revision rather than a flag. This guards against both accidental
	// misuse and CodeQL-style command-injection concerns.
	cmd := exec.Command("git", "rev-list", "--reverse", "--end-of-options", revisionRange)
	output, err := cmd.Output()
	if err != nil {
		return 1, fmt.Errorf(
			"list commits in %s: %w (if this is a shallow clone, run with `fetch-depth: 0` or `git fetch --unshallow`)",
			revisionRange, commandError(err),
		)
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
	cmd := exec.Command("git", "show", "-s", "--format=%B", "--end-of-options", sha)
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

	validateHeader(header, &result)

	parsed := splitBodyFooter(lines)
	validateBody(parsed.body, &result)
	validateFooter(parsed.footer, &result)
	validateBreakingChange(header, parsed.footer, &result)

	return result
}

func validateHeader(header string, result *validationResult) {
	if header == "" {
		result.errors = append(result.errors, "header must not be empty")
		return
	}

	if header != strings.TrimSpace(header) {
		result.errors = append(result.errors, "header must not start or end with whitespace")
	}

	if len(header) > headerMaxLength {
		result.errors = append(result.errors, fmt.Sprintf("header exceeds %d characters (%d)", headerMaxLength, len(header)))
	}

	match := headerPattern.FindStringSubmatch(header)
	if match == nil {
		diagnoseHeader(header, result)
		return
	}

	commitType := match[1]
	subject := match[4]

	loweredType := strings.ToLower(commitType)
	_, typeAllowed := allowedTypes[loweredType]
	switch {
	case commitType != loweredType && typeAllowed:
		// Correct type but wrong case — report case only, no "not allowed" noise.
		result.errors = append(result.errors, fmt.Sprintf("type %q must be lower-case", commitType))
	case !typeAllowed:
		result.errors = append(result.errors, fmt.Sprintf("type %q is not allowed (expected one of: %s)", commitType, allowedTypesList()))
	}

	switch {
	case strings.TrimSpace(subject) == "":
		result.errors = append(result.errors, "subject must not be empty")
	case hasLeadingWhitespace(subject):
		// The regex already consumed the single separator space after `:`, so any
		// leading whitespace on the captured subject (space, tab, NBSP, etc.) means
		// the "exactly one space after ':'" contract is violated.
		result.errors = append(result.errors, "header must have exactly one space after ':' (found extra whitespace)")
	case strings.HasSuffix(subject, "."):
		result.errors = append(result.errors, "subject must not end with '.'")
	case startsWithUpper(subject):
		result.errors = append(result.errors, "subject must not start with an uppercase letter")
	}
}

func hasLeadingWhitespace(s string) bool {
	for _, r := range s {
		return unicode.IsSpace(r)
	}
	return false
}

// diagnoseHeader emits a more specific error than "header must match" when possible.
func diagnoseHeader(header string, result *validationResult) {
	colonIdx := strings.Index(header, ":")
	switch {
	case colonIdx < 0:
		result.errors = append(result.errors, "header is missing ': ' between type and subject (expected `<type>(<scope>)!?: <subject>`)")
	case colonIdx == 0:
		result.errors = append(result.errors, "header is missing a type before ':' (expected `<type>: <subject>`)")
	case colonIdx == len(header)-1:
		result.errors = append(result.errors, "subject must not be empty")
	case header[colonIdx+1] != ' ':
		result.errors = append(result.errors, "header must have exactly one space after ':'")
	case colonIdx+2 < len(header) && header[colonIdx+2] == ' ':
		result.errors = append(result.errors, "header must have exactly one space after ':' (found multiple)")
	default:
		result.errors = append(result.errors, "header must match `<type>(<scope>)!?: <subject>`")
	}
}

func validateBody(lines []string, result *validationResult) {
	for idx, line := range lines {
		if line == "" {
			continue
		}
		if len(line) > bodyMaxLineLength {
			result.warnings = append(result.warnings, fmt.Sprintf("body line %d exceeds %d characters (%d)", idx+1, bodyMaxLineLength, len(line)))
		}
	}
}

func validateFooter(lines []string, result *validationResult) {
	for idx, line := range lines {
		if line == "" {
			continue
		}
		// BREAKING CHANGE handling is delegated to validateBreakingChange.
		if breakingPrefixPattern.MatchString(line) {
			if len(line) > footerMaxLineLength {
				result.warnings = append(result.warnings, fmt.Sprintf("footer line %d exceeds %d characters (%d)", idx+1, footerMaxLineLength, len(line)))
			}
			continue
		}
		if !footerPattern.MatchString(line) {
			result.errors = append(result.errors, fmt.Sprintf("footer line %d does not match `<token>: <value>` or `<token> #<issue>`: %q", idx+1, line))
			continue
		}
		if len(line) > footerMaxLineLength {
			result.warnings = append(result.warnings, fmt.Sprintf("footer line %d exceeds %d characters (%d)", idx+1, footerMaxLineLength, len(line)))
		}
	}
}

// validateBreakingChange enforces that a `!` marker in the header OR a BREAKING CHANGE
// footer is accompanied by a non-empty description in the footer, per Conventional
// Commits §13.
func validateBreakingChange(header string, footer []string, result *validationResult) {
	hasBangMarker := false
	if match := headerPattern.FindStringSubmatch(header); match != nil && match[3] == "!" {
		hasBangMarker = true
	}

	breakingDescFound := false
	breakingDescEmpty := false
	for _, line := range footer {
		match := breakingPrefixPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		breakingDescFound = true
		if strings.TrimSpace(match[1]) == "" {
			breakingDescEmpty = true
		}
	}

	if breakingDescFound && breakingDescEmpty {
		result.errors = append(result.errors, "BREAKING CHANGE footer must have a description")
	}

	if hasBangMarker && !breakingDescFound {
		result.warnings = append(result.warnings, "header uses '!' breaking marker but no `BREAKING CHANGE:` footer explains the impact")
	}
}

// splitBodyFooter classifies lines[1:] into body and footer. The last contiguous block
// of non-empty lines (separated from the body by a blank line) is treated as a footer
// iff every line in that block matches footerPattern. Otherwise everything is body.
func splitBodyFooter(lines []string) parsedMessage {
	if len(lines) <= 1 {
		return parsedMessage{header: lines[0]}
	}

	rest := lines[1:]
	// Drop leading blank line separating header from body.
	for len(rest) > 0 && rest[0] == "" {
		rest = rest[1:]
	}

	// Find the start of the last non-empty block.
	lastBlockStart := len(rest)
	for i := len(rest) - 1; i >= 0; i-- {
		if rest[i] == "" {
			lastBlockStart = i + 1
			break
		}
		lastBlockStart = i
	}

	lastBlock := rest[lastBlockStart:]
	if len(lastBlock) == 0 || !isFooterBlock(lastBlock) {
		return parsedMessage{header: lines[0], body: rest}
	}

	// lastBlockStart-1 is the blank line separator (if any). Drop trailing empties
	// from body.
	bodyEnd := lastBlockStart
	for bodyEnd > 0 && rest[bodyEnd-1] == "" {
		bodyEnd--
	}

	return parsedMessage{
		header: lines[0],
		body:   rest[:bodyEnd],
		footer: lastBlock,
	}
}

func isFooterBlock(lines []string) bool {
	for _, line := range lines {
		if line == "" {
			continue
		}
		if footerPattern.MatchString(line) {
			continue
		}
		// Allow present-but-empty BREAKING CHANGE lines so validateBreakingChange can
		// report the specific "must have a description" error instead of the generic
		// "does not match" footer error.
		if breakingPrefixPattern.MatchString(line) {
			continue
		}
		return false
	}
	return true
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

func allowedTypesList() string {
	names := make([]string, 0, len(allowedTypes))
	for name := range allowedTypes {
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
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
