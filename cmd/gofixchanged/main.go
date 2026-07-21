package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

var errFixDrift = errors.New("changed Go files need go fix")

func main() {
	write := flag.Bool("write", false, "apply fixes to changed Go files")
	flag.Parse()

	if err := run(context.Background(), *write, os.Stdout); err != nil {
		if !errors.Is(err, errFixDrift) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, write bool, stdout io.Writer) error {
	root, err := gitOutput(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root = strings.TrimSpace(root)

	changed, err := changedGoFiles(ctx, root)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		_, err = fmt.Fprintln(stdout, "No changed Go files")
		if err != nil {
			return fmt.Errorf("print no-changes result: %w", err)
		}
		return nil
	}

	diff, fixErr := goFixDiff(ctx, root, changedPackages(root, changed))
	filtered, err := filterDiff(diff, root, changed)
	if err != nil {
		return err
	}
	if len(filtered) == 0 {
		if fixErr != nil && !errors.Is(fixErr, errFixDrift) {
			return fixErr
		}
		return nil
	}
	if fixErr != nil && !errors.Is(fixErr, errFixDrift) {
		return fixErr
	}

	if write {
		return applyPatch(ctx, root, filtered)
	}
	if _, err := stdout.Write(filtered); err != nil {
		return fmt.Errorf("print go fix diff: %w", err)
	}
	return errFixDrift
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func changedGoFiles(ctx context.Context, root string) ([]string, error) {
	tracked, err := gitFileList(ctx, root, "diff", "--name-only", "-z", "--diff-filter=ACMR", "HEAD", "--", "*.go")
	if err != nil {
		return nil, fmt.Errorf("list modified Go files: %w", err)
	}
	untracked, err := gitFileList(ctx, root, "ls-files", "--others", "--exclude-standard", "-z", "--", "*.go")
	if err != nil {
		return nil, fmt.Errorf("list untracked Go files: %w", err)
	}
	return append(tracked, untracked...), nil
}

func gitFileList(ctx context.Context, root string, args ...string) ([]string, error) {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	parts := bytes.Split(output, []byte{0})
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			files = append(files, filepath.ToSlash(string(part)))
		}
	}
	return files, nil
}

func changedPackages(root string, files []string) []string {
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		dir := filepath.Dir(filepath.FromSlash(file))
		seen[filepath.Join(root, dir)] = struct{}{}
	}

	packages := make([]string, 0, len(seen))
	for pkg := range seen {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)
	return packages
}

func goFixDiff(ctx context.Context, root string, packages []string) ([]byte, error) {
	args := append([]string{"fix", "-diff", "-omitzero=false"}, packages...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stderr.Len() > 0 {
		return stdout.Bytes(), fmt.Errorf("go fix changed packages: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		if stdout.Len() > 0 {
			return stdout.Bytes(), errFixDrift
		}
		return nil, fmt.Errorf("go fix changed packages: %w", err)
	}
	return stdout.Bytes(), nil
}

func filterDiff(diff []byte, root string, changed []string) ([]byte, error) {
	wanted := make(map[string]struct{}, len(changed))
	for _, file := range changed {
		wanted[normalizedPath(file)] = struct{}{}
	}

	lines := splitLines(diff)
	var filtered bytes.Buffer
	for i := 0; i < len(lines); {
		if !bytes.HasPrefix(lines[i], []byte("--- ")) {
			i++
			continue
		}
		if i+1 >= len(lines) || !bytes.HasPrefix(lines[i+1], []byte("+++ ")) {
			return nil, errors.New("parse go fix diff: missing new-file header")
		}

		end := i + 2
		for end < len(lines) && !bytes.HasPrefix(lines[end], []byte("--- ")) {
			end++
		}
		rel, err := diffRelativePath(root, string(lines[i+1]), "+++ ", " (new)")
		if err != nil {
			return nil, err
		}
		if _, ok := wanted[normalizedPath(rel)]; ok {
			fmt.Fprintf(&filtered, "--- a/%s\n+++ b/%s\n", filepath.ToSlash(rel), filepath.ToSlash(rel))
			for _, line := range lines[i+2 : end] {
				filtered.Write(line)
			}
		}
		i = end
	}
	return filtered.Bytes(), nil
}

func splitLines(input []byte) [][]byte {
	if len(input) == 0 {
		return nil
	}
	lines := bytes.SplitAfter(input, []byte("\n"))
	if len(lines[len(lines)-1]) == 0 {
		return lines[:len(lines)-1]
	}
	return lines
}

func diffRelativePath(root, header, prefix, suffix string) (string, error) {
	value := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(header, prefix)), suffix)
	rel, err := filepath.Rel(root, value)
	if err != nil {
		return "", fmt.Errorf("resolve go fix diff path %q: %w", value, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("go fix diff path is outside repository: %s", value)
	}
	return filepath.ToSlash(rel), nil
}

func normalizedPath(value string) string {
	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if runtime.GOOS == "windows" {
		return strings.ToLower(value)
	}
	return value
}

func applyPatch(ctx context.Context, root string, patch []byte) error {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "apply", "--whitespace=nowarn", "-")
	cmd.Stdin = bytes.NewReader(patch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apply go fix changes: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
