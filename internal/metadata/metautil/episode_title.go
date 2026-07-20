// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metautil

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// releaseNameToken stores a normalized alphanumeric token with byte offsets
// into the original release name so removals can preserve untouched text.
type releaseNameToken struct {
	value string
	start int
	end   int
}

// RemoveEpisodeTitleFromReleaseName removes episodeTitle from releaseName when
// it appears as a complete token sequence. Substrings inside larger words are
// left unchanged, and surrounding release-name separators are collapsed.
func RemoveEpisodeTitleFromReleaseName(releaseName string, episodeTitle string) string {
	start, end, ok := episodeTitleSpan(releaseName, episodeTitle)
	if !ok {
		return releaseName
	}

	leftEnd := trimReleaseNameSeparatorsRight(releaseName, start)
	rightStart := trimReleaseNameSeparatorsLeft(releaseName, end)
	before := releaseName[:leftEnd]
	after := releaseName[rightStart:]
	if strings.TrimSpace(before) == "" {
		return strings.TrimLeftFunc(after, isReleaseNameSeparator)
	}
	if strings.TrimSpace(after) == "" {
		return strings.TrimRightFunc(before, isReleaseNameSeparator)
	}

	separator := releaseNameJoinSeparator(releaseName[leftEnd:start], releaseName[end:rightStart], after)
	return before + separator + after
}

// ReleaseNameContainsEpisodeTitle reports whether episodeTitle appears in value
// as a complete token sequence.
func ReleaseNameContainsEpisodeTitle(value string, episodeTitle string) bool {
	_, _, ok := episodeTitleSpan(value, episodeTitle)
	return ok
}

// episodeTitleSpan returns the original byte span of the first token-sequence
// match between value and episodeTitle.
func episodeTitleSpan(value string, episodeTitle string) (int, int, bool) {
	valueTokens := releaseNameTokens(value)
	titleTokens := releaseNameTokens(episodeTitle)
	if len(valueTokens) == 0 || len(titleTokens) == 0 || len(titleTokens) > len(valueTokens) {
		return 0, 0, false
	}

	for start := 0; start <= len(valueTokens)-len(titleTokens); start++ {
		matched := true
		for offset, titleToken := range titleTokens {
			if valueTokens[start+offset].value != titleToken.value {
				matched = false
				break
			}
		}
		if matched {
			end := start + len(titleTokens) - 1
			return valueTokens[start].start, valueTokens[end].end, true
		}
	}
	return 0, 0, false
}

func releaseNameTokens(value string) []releaseNameToken {
	tokens := make([]releaseNameToken, 0)
	tokenStart := -1
	var builder strings.Builder
	for idx, current := range value {
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			if tokenStart == -1 {
				tokenStart = idx
			}
			builder.WriteRune(unicode.ToLower(current))
			continue
		}
		if tokenStart != -1 {
			tokens = append(tokens, releaseNameToken{
				value: builder.String(),
				start: tokenStart,
				end:   idx,
			})
			builder.Reset()
			tokenStart = -1
		}
	}
	if tokenStart != -1 {
		tokens = append(tokens, releaseNameToken{
			value: builder.String(),
			start: tokenStart,
			end:   len(value),
		})
	}
	return tokens
}

func trimReleaseNameSeparatorsRight(value string, end int) int {
	for end > 0 {
		current, size := lastRuneBefore(value, end)
		if current == 0 || !isReleaseNameSeparator(current) {
			break
		}
		end -= size
	}
	return end
}

func trimReleaseNameSeparatorsLeft(value string, start int) int {
	for start < len(value) {
		current, size := firstRuneAt(value, start)
		if current == 0 || !isReleaseNameSeparator(current) {
			break
		}
		start += size
	}
	return start
}

// releaseNameJoinSeparator chooses one separator for the gap left after title
// removal, preferring the style already used around the removed span.
func releaseNameJoinSeparator(left string, right string, after string) string {
	if strings.Contains(right, "-") && !strings.ContainsAny(after, ". _") {
		return "-"
	}
	if strings.Contains(left, ".") || strings.Contains(right, ".") {
		return "."
	}
	if strings.Contains(left, "_") || strings.Contains(right, "_") {
		return "_"
	}
	if strings.Contains(left, "-") || strings.Contains(right, "-") {
		return "-"
	}
	return " "
}

func isReleaseNameSeparator(value rune) bool {
	return unicode.IsSpace(value) || value == '.' || value == '_' || value == '-'
}

func firstRuneAt(value string, idx int) (rune, int) {
	current, size := utf8.DecodeRuneInString(value[idx:])
	return current, size
}

func lastRuneBefore(value string, end int) (rune, int) {
	current, size := utf8.DecodeLastRuneInString(value[:end])
	return current, size
}
