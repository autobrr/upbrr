// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package redaction

import (
	"encoding/json"
	"regexp"
	"strings"
)

type Block struct {
	Start int
	End   int
}

var DefaultSensitiveKeys = map[string]struct{}{
	"token":         {},
	"passkey":       {},
	"password":      {},
	"auth":          {},
	"cookie":        {},
	"csrf":          {},
	"email":         {},
	"username":      {},
	"user":          {},
	"key":           {},
	"info_hash":     {},
	"anticsrftoken": {},
	"torrent_pass":  {},
	"popcron":       {},
}

var (
	passkeyPathRe       = regexp.MustCompile(`(?i)/([A-Za-z0-9]{10,})(/announce(?:\.php)?)`) // /<passkey>/announce
	announcePathTokenRe = regexp.MustCompile(`(?i)(/announce(?:\.php)?/)([A-Za-z0-9]{10,})($|[/?#])`)
	apiPathTokenRe      = regexp.MustCompile(`(?i)(/api/torrents/)([A-Za-z0-9]{10,})($|[/?#"])`)
	proxyPathRe         = regexp.MustCompile(`(?i)(/proxy/)([^/\s?#"]+)`) // /proxy/<secret>
	queryParamRe        = regexp.MustCompile(`(?i)([?&](api[_-]?key|api[_-]?token|auth|authkey|info_hash|key|passkey|rsskey|token|torrent_pass|uid|user|user_id|userid)=)[^&]+`)
	keyValueQuotedRe    = regexp.MustCompile(`(?i)\b(api[_-]?key|api[_-]?token|authorization|auth|authkey|cookie|csrf|passkey|password|secret|token|torrent_pass)\b(\s*[:=]\s*)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')`)
	keyValuePlainRe     = regexp.MustCompile(`(?i)\b(api[_-]?key|api[_-]?token|authorization|auth|authkey|cookie|csrf|passkey|password|secret|token|torrent_pass)\b(\s*[:=]\s*)(bearer\s+)?([^"'\s,;)\]}]+)`)
	longHexTokenRe      = regexp.MustCompile(`\b[a-fA-F0-9]{32,}\b`)
)

// ExtractJSONBlocks returns candidate JSON substrings based on bracket counting.
func ExtractJSONBlocks(text string) []Block {
	blocks := make([]Block, 0)
	stack := make([]rune, 0)
	start := -1
	inString := false
	var stringChar rune
	escape := false

	for idx, ch := range text {
		if escape {
			escape = false
			continue
		}

		if inString {
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == stringChar {
				inString = false
			}
			continue
		}

		if ch == '"' || ch == '\'' {
			inString = true
			stringChar = ch
			continue
		}

		if ch == '{' || ch == '[' {
			if len(stack) == 0 {
				start = idx
			}
			stack = append(stack, ch)
			continue
		}

		if (ch == '}' || ch == ']') && len(stack) > 0 {
			top := stack[len(stack)-1]
			if (ch == '}' && top == '{') || (ch == ']' && top == '[') {
				stack = stack[:len(stack)-1]
				if len(stack) == 0 && start >= 0 {
					blocks = append(blocks, Block{Start: start, End: idx + 1})
					start = -1
				}
			}
		}
	}

	return blocks
}

// RedactValue redacts sensitive content in a string.
func RedactValue(value string, sensitiveKeys map[string]struct{}) string {
	keys := sensitiveKeys
	if keys == nil {
		keys = DefaultSensitiveKeys
	}

	blocks := ExtractJSONBlocks(value)
	if len(blocks) > 0 {
		for i := len(blocks) - 1; i >= 0; i-- {
			block := blocks[i]
			if block.Start < 0 || block.End > len(value) || block.Start >= block.End {
				continue
			}
			segment := value[block.Start:block.End]
			var parsed any
			if err := json.Unmarshal([]byte(segment), &parsed); err != nil {
				continue
			}
			redacted := RedactPrivateInfo(parsed, keys)
			data, err := json.Marshal(redacted)
			if err != nil {
				continue
			}
			value = value[:block.Start] + string(data) + value[block.End:]
		}
	}

	value = passkeyPathRe.ReplaceAllString(value, `/[REDACTED]$2`)
	value = announcePathTokenRe.ReplaceAllString(value, `${1}[REDACTED]${3}`)
	value = apiPathTokenRe.ReplaceAllString(value, `${1}[REDACTED]${3}`)
	value = proxyPathRe.ReplaceAllString(value, `${1}[REDACTED]`)
	value = queryParamRe.ReplaceAllString(value, `${1}[REDACTED]`)
	value = keyValueQuotedRe.ReplaceAllStringFunc(value, redactQuotedKeyValue)
	value = keyValuePlainRe.ReplaceAllString(value, `${1}${2}${3}[REDACTED]`)
	value = longHexTokenRe.ReplaceAllString(value, `[REDACTED]`)

	_ = keys
	return value
}

func redactQuotedKeyValue(value string) string {
	matches := keyValueQuotedRe.FindStringSubmatchIndex(value)
	if len(matches) < 8 || matches[6] < 0 || matches[7] <= matches[6] {
		return value
	}
	quoted := value[matches[6]:matches[7]]
	if len(quoted) < 2 {
		return value
	}
	return value[:matches[6]] + quoted[:1] + "[REDACTED]" + quoted[len(quoted)-1:]
}

// RedactPrivateInfo recursively redacts sensitive values in JSON-like data.
func RedactPrivateInfo(data any, sensitiveKeys map[string]struct{}) any {
	keys := sensitiveKeys
	if keys == nil {
		keys = DefaultSensitiveKeys
	}

	switch typed := data.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, value := range typed {
			if isSensitiveKey(key, keys) {
				redacted[key] = "[REDACTED]"
				continue
			}
			redacted[key] = RedactPrivateInfo(value, keys)
		}
		return redacted
	case []any:
		redacted := make([]any, 0, len(typed))
		for _, value := range typed {
			redacted = append(redacted, RedactPrivateInfo(value, keys))
		}
		return redacted
	case string:
		var parsed any
		if err := json.Unmarshal([]byte(typed), &parsed); err == nil {
			redacted := RedactPrivateInfo(parsed, keys)
			data, err := json.Marshal(redacted)
			if err == nil {
				return string(data)
			}
		}
		return RedactValue(typed, keys)
	default:
		return data
	}
}

func isSensitiveKey(key string, keys map[string]struct{}) bool {
	if len(keys) == 0 {
		return false
	}
	lower := strings.ToLower(key)
	for candidate := range keys {
		if strings.Contains(lower, candidate) {
			return true
		}
	}
	return false
}
