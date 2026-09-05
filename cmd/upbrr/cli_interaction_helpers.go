// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/logging"
)

func formatPathLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "(none)"
	}
	if label, ok := logging.DBRelativePathLabel(trimmed); ok {
		return label
	}
	return "[local path]"
}

func promptYesNo(reader *bufio.Reader, output io.Writer, prompt string, defaultYes bool) (bool, error) {
	line, err := promptLine(reader, output, prompt)
	if err != nil {
		return false, err
	}
	trimmed := strings.ToLower(strings.TrimSpace(line))
	if trimmed == "" {
		return defaultYes, nil
	}
	return trimmed == "y" || trimmed == "yes", nil
}

func promptLine(reader *bufio.Reader, output io.Writer, prompt string) (string, error) {
	if _, err := fmt.Fprint(output, prompt); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return line, nil
		}
		return "", fmt.Errorf("read prompt line: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func splitInteractiveCLIArgs(input string) ([]string, error) {
	args := make([]string, 0, len(strings.Fields(input)))
	var current strings.Builder
	quote := rune(0)
	tokenStarted := false
	quoteBoundary := true

	for _, r := range input {
		if quote == 0 {
			switch {
			case unicode.IsSpace(r):
				if tokenStarted {
					args = append(args, current.String())
					current.Reset()
					tokenStarted = false
				}
				quoteBoundary = true
				continue
			case quoteBoundary && (r == '"' || r == '\''):
				quote = r
				tokenStarted = true
				quoteBoundary = false
				continue
			}
		} else if r == quote {
			quote = 0
			quoteBoundary = false
			continue
		}

		current.WriteRune(r)
		tokenStarted = true
		quoteBoundary = r == '='
	}

	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if tokenStarted {
		args = append(args, current.String())
	}
	return args, nil
}

func copyVisited(input map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(input))
	maps.Copy(cloned, input)
	return cloned
}
