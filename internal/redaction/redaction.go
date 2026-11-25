// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package redaction

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Block identifies a candidate JSON object's or array's half-open byte range
// within the source string.
type Block struct {
	Start int
	End   int
}

// DefaultSensitiveKeys is the JSON-key set used when a redaction call receives
// nil keys. Key matching ignores case and common separators and treats a key as
// sensitive when its normalized form contains a normalized entry.
var DefaultSensitiveKeys = map[string]struct{}{
	"token":         {},
	"passkey":       {},
	"password":      {},
	"auth":          {},
	"cookie":        {},
	"session":       {},
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

const textSensitiveField = `\b(anti[_-]?csrf[_-]?token|api[_-]?key|api[_-]?token|authorization|auth|auth[_-]?key|cookie|csrf|passkey|password|rss[_-]?key|secret|session(?:[_-]?id)?|token|torrent[_-]?pass)\b(\s*[:=]\s*)`

var (
	passkeyPathRe       = regexp.MustCompile(`(?i)/([A-Za-z0-9]{10,})(/announce(?:\.php)?)`) // /<passkey>/announce
	announcePathTokenRe = regexp.MustCompile(`(?i)(/announce(?:\.php)?/)([A-Za-z0-9]{10,})($|[/?#])`)
	apiPathTokenRe      = regexp.MustCompile(`(?i)(/api/torrents/)([A-Za-z0-9]{10,})($|[/?#"])`)
	proxyPathRe         = regexp.MustCompile(`(?i)(/proxy/)([^/\s?#"]+)`) // /proxy/<secret>
	urlRe               = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://|//)[^\s<>"']+`)
	urlUserinfoRe       = regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://|//)([^/@\s]+)@`)
	queryParamRe        = regexp.MustCompile(
		`(?i)([?&](anti[_-]?csrf[_-]?token|api[_-]?key|api[_-]?token|auth|auth[_-]?key|csrf|info[_-]?hash|key|passkey|password|rss[_-]?key|secret|session(?:[_-]?id)?|token|torrent[_-]?pass|uid|user|user[_-]?id|userid)=)(?:\[REDACTED\]|[^&\s,\[\])}"']+)`,
	)
	keyValueQuotedRe = regexp.MustCompile(`(?i)` + textSensitiveField + `("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')`)
	keyValuePlainRe  = regexp.MustCompile(`(?i)` + textSensitiveField + `(bearer\s+)?([^"'\s,;)\]}]+)`)
	keyValueJSONRe   = regexp.MustCompile(`(?i)` + textSensitiveField + `(?:bearer\s+)?$`)
	cookieHeaderRe   = regexp.MustCompile(`(?im)(^|[ \t(])(cookie\b[ \t]*[:=][ \t]*)(?:[^\s"'=;,{\[]+=|\[REDACTED\](?:[ \t]*;|[\[{]))[^\r\n]*`)
	cookieTailRe     = regexp.MustCompile(`(?i)(\bcookie\b\s*[:=]\s*\[REDACTED\])(?:[;]\s*[^,\r\n]+)+`)
	authTailRe       = regexp.MustCompile(`(?i)(\bauthorization\b\s*[:=]\s*bearer\s+\[REDACTED\])(?:,\s*[^,\s]+)+`)
	longHexTokenRe   = regexp.MustCompile(`\b[a-fA-F0-9]{32,}\b`)
)

// ExtractJSONBlocks returns balanced top-level object and array candidates in
// source order. Brackets inside quoted strings are ignored, and unterminated
// candidates are omitted.
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

// RedactValue redacts valid embedded JSON plus known secret-bearing URL, path,
// query, key/value, cookie, authorization, and long-hex patterns. URL components
// are decoded before matching sensitive keys and paths. Nil keys use
// [DefaultSensitiveKeys] for embedded JSON and URL values; other textual
// patterns use the package's fixed secret vocabulary.
// Optional known secrets are also removed from decoded JSON and URL components.
func RedactValue(value string, sensitiveKeys map[string]struct{}, secrets ...string) string {
	original := redactKnownSecrets(value, secrets)
	value = normalizeSecretText(original)
	keys := sensitiveKeys
	if keys == nil {
		keys = DefaultSensitiveKeys
	}

	var result strings.Builder
	end := 0
	for _, block := range ExtractJSONBlocks(value) {
		// A URL may contain a JSON-looking path or query value. Keep
		// that value with the URL so it is redacted in context.
		prefix := value[end:block.Start]
		// Leave JSON inside a plaintext Cookie header with the rest of the header.
		lineStart := end + strings.LastIndexAny(prefix, "\r\n") + 1
		if cookieHeaderRe.MatchString(value[lineStart : block.Start+1]) {
			continue
		}
		start := strings.LastIndexAny(prefix, " \t\n\r<>\"'") + 1
		if urlRe.MatchString(prefix[start:]) || strings.HasSuffix(prefix[start:], "://") {
			continue
		}
		var parsed any
		if err := json.Unmarshal([]byte(value[block.Start:block.End]), &parsed); err != nil {
			continue
		}
		if field := keyValueJSONRe.FindStringIndex(prefix); field != nil {
			result.WriteString(redactText(prefix[:field[0]], keys, secrets))
			result.WriteString(prefix[field[0]:])
			result.WriteString("[REDACTED]")
			end = block.End
			continue
		}
		data, err := json.Marshal(RedactPrivateInfo(parsed, keys, secrets...))
		if err != nil {
			continue
		}
		result.WriteString(redactText(prefix, keys, secrets))
		result.Write(data)
		end = block.End
	}
	output := redactText(value[end:], keys, secrets)
	if end != 0 {
		result.WriteString(output)
		output = result.String()
	}
	if output == value {
		return original
	}
	return output
}

func redactText(value string, keys map[string]struct{}, secrets []string) string {
	value = cookieHeaderRe.ReplaceAllString(value, "${1}${2}[REDACTED]")
	value = redactURLs(value, keys, secrets...)
	value = redactURLPath(value)
	value = urlUserinfoRe.ReplaceAllString(value, `${1}[REDACTED]@`)
	value = queryParamRe.ReplaceAllString(value, `${1}[REDACTED]`)
	value = keyValueQuotedRe.ReplaceAllStringFunc(value, redactQuotedKeyValue)
	value = keyValuePlainRe.ReplaceAllStringFunc(value, redactPlainKeyValue)
	value = cookieTailRe.ReplaceAllString(value, `${1}`)
	value = authTailRe.ReplaceAllString(value, `${1}`)
	value = longHexTokenRe.ReplaceAllString(value, `[REDACTED]`)
	return redactKnownSecrets(value, secrets)
}

func redactURLs(value string, keys map[string]struct{}, secrets ...string) string {
	var result strings.Builder
	offset := 0
	for {
		loc := urlRe.FindStringIndex(value[offset:])
		if loc == nil {
			if offset == 0 {
				return value
			}
			result.WriteString(value[offset:])
			return result.String()
		}
		start, end := offset+loc[0], offset+loc[1]
		if tag := strings.LastIndexByte(value[offset:start], '['); tag >= 0 {
			// Only interpret brackets as markup after a known BBCode tag.
			// Ordinary URL query values may contain literal brackets.
			end = start + bbcodeURLLength(value[offset+tag:start], value[start:end])
		}
		result.WriteString(value[offset:start])
		result.WriteString(redactURL(value[start:end], keys, secrets...))
		offset = end
	}
}

var (
	bbcodeCloseURL = regexp.MustCompile(`(?i)\[/url\]`)
	bbcodeCloseImg = regexp.MustCompile(`(?i)\[/img\]`)
	bbcodeOpenImg  = regexp.MustCompile(`(?i)^\[img(?:[= ][^\[\]]*)?\]$`)
)

func bbcodeURLLength(prefix, raw string) int {
	switch {
	case strings.EqualFold(prefix, "[url="):
		depth := 0
		for i, char := range raw {
			switch char {
			case '[':
				depth++
			case ']':
				if depth == 0 {
					return i
				}
				depth--
			}
		}
	case strings.EqualFold(prefix, "[url]"):
		if match := bbcodeCloseURL.FindStringIndex(raw); match != nil {
			return match[0]
		}
	case bbcodeOpenImg.MatchString(prefix):
		if match := bbcodeCloseImg.FindStringIndex(raw); match != nil {
			return match[0]
		}
	}
	return len(raw)
}

func redactKnownSecrets(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

// normalizeSecretText prevents terminal controls from hiding a secret key.
// Callers retain the original spelling when the redaction pass leaves the
// normalized text unchanged.
func normalizeSecretText(value string) string {
	if !strings.ContainsFunc(value, unicode.IsControl) && !strings.ContainsAny(value, "\u202a\u202b\u202d\u202e\u2066\u2067\u2068\u2069") {
		return value
	}
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || r == '\u202a' || r == '\u202b' || r == '\u202d' || r == '\u202e' ||
			r == '\u2066' || r == '\u2067' || r == '\u2068' || r == '\u2069') {
			return -1
		}
		return r
	}, ansi.Strip(value))
}

// redactURL preserves ordinary URL context while structurally redacting known
// sensitive userinfo, path, query, and fragment values.
func redactURL(raw string, sensitiveKeys map[string]struct{}, secrets ...string) string {
	trimmed := strings.TrimRight(raw, ".,;:)")
	suffix := strings.TrimPrefix(raw, trimmed)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return "[REDACTED]" + suffix
	}
	if suffix != "" && len(secrets) > 0 {
		full, fullErr := url.Parse(raw)
		if fullErr == nil {
			for _, secret := range secrets {
				if secret != "" && urlSecretCount(full, secret) > urlSecretCount(parsed, secret) {
					parsed, suffix = full, ""
					break
				}
			}
		}
	}
	if parsed.User != nil {
		parsed.User = url.User("[REDACTED]")
	}
	parsed.Path = redactURLPath(redactKnownSecrets(parsed.Path, secrets))
	parsed.RawPath = ""
	query, ok := redactURLValues(parsed.RawQuery, sensitiveKeys, secrets...)
	if !ok {
		parsed.RawQuery = "[REDACTED]"
	} else {
		parsed.RawQuery = query
	}
	if strings.ContainsAny(parsed.Fragment, "=&") {
		fragment, ok := redactURLValues(parsed.Fragment, sensitiveKeys, secrets...)
		if !ok {
			parsed.Fragment = "[REDACTED]"
		} else {
			decodedFragment, err := url.PathUnescape(fragment)
			if err != nil {
				parsed.Fragment = "[REDACTED]"
			} else {
				parsed.Fragment = decodedFragment
			}
		}
	}
	parsed.Fragment = redactKnownSecrets(parsed.Fragment, secrets)
	parsed.RawFragment = ""
	return strings.ReplaceAll(parsed.String(), url.QueryEscape("[REDACTED]"), "[REDACTED]") + suffix
}

// urlSecretCount detects whether trimming URL punctuation would split a known secret.
func urlSecretCount(value *url.URL, secret string) int {
	count := strings.Count(value.Path, secret)
	if decoded, err := url.QueryUnescape(value.RawQuery); err == nil {
		count += strings.Count(decoded, secret)
	}
	if !strings.ContainsAny(value.Fragment, "=&") {
		return count + strings.Count(value.Fragment, secret)
	}
	// Count literal semicolons as data; the sanitizer still rejects malformed queries.
	fragment, _ := url.ParseQuery(strings.ReplaceAll(value.Fragment, ";", "%3B"))
	if decoded, err := url.PathUnescape(fragment.Encode()); err == nil {
		count += strings.Count(decoded, secret)
	}
	for key, items := range fragment {
		count += strings.Count(key, secret)
		for _, item := range items {
			count += strings.Count(item, secret)
		}
	}
	return count
}

func redactURLPath(path string) string {
	path = passkeyPathRe.ReplaceAllString(path, `/[REDACTED]$2`)
	path = announcePathTokenRe.ReplaceAllString(path, `${1}[REDACTED]${3}`)
	path = apiPathTokenRe.ReplaceAllString(path, `${1}[REDACTED]${3}`)
	return proxyPathRe.ReplaceAllString(path, `${1}[REDACTED]`)
}

func redactURLValues(raw string, sensitiveKeys map[string]struct{}, secrets ...string) (string, bool) {
	if raw == "" {
		return "", true
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", false
	}
	decoded, _ := url.QueryUnescape(raw)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		count := strings.Count(decoded, secret)
		for key, items := range values {
			count -= strings.Count(key, secret) * len(items)
			for _, item := range items {
				count -= strings.Count(item, secret)
			}
		}
		// A complete token spanning fields cannot be removed as one component.
		if count > 0 {
			return "", false
		}
	}
	redacted := make(url.Values, len(values))
	for key, items := range values {
		if isSensitiveKey(key, sensitiveKeys) {
			items = []string{"[REDACTED]"}
		} else {
			for index, item := range items {
				items[index] = redactKnownSecrets(item, secrets)
			}
		}
		redacted[redactKnownSecrets(key, secrets)] = items
	}
	return redacted.Encode(), true
}

// redactQuotedKeyValue replaces the value part of a matched quoted secret
// key/value pair while preserving the original quote style.
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

// redactPlainKeyValue replaces the value part of a matched unquoted secret
// key/value pair without reprocessing an existing redaction marker.
func redactPlainKeyValue(value string) string {
	matches := keyValuePlainRe.FindStringSubmatch(value)
	if len(matches) < 5 {
		return value
	}
	if strings.EqualFold(matches[4], "[REDACTED") || strings.EqualFold(matches[4], "[REDACTED]") {
		return value
	}
	return matches[1] + matches[2] + matches[3] + "[REDACTED]"
}

// RedactPrivateInfo recursively redacts JSON-like maps, slices, and strings
// without mutating input maps or slices. Nil keys use [DefaultSensitiveKeys].
// Optional known secrets are removed from decoded string values and map keys.
func RedactPrivateInfo(data any, sensitiveKeys map[string]struct{}, secrets ...string) any {
	keys := sensitiveKeys
	if keys == nil {
		keys = DefaultSensitiveKeys
	}

	switch typed := data.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, value := range typed {
			redactedKey := redactText(normalizeSecretText(key), keys, secrets)
			if _, collision := redacted[redactedKey]; collision || isSensitiveKey(key, keys) {
				redacted[redactedKey] = "[REDACTED]"
				continue
			}
			redacted[redactedKey] = RedactPrivateInfo(value, keys, secrets...)
		}
		return redacted
	case []any:
		redacted := make([]any, 0, len(typed))
		for _, value := range typed {
			redacted = append(redacted, RedactPrivateInfo(value, keys, secrets...))
		}
		return redacted
	case string:
		typed = redactKnownSecrets(typed, secrets)
		var parsed any
		if err := json.Unmarshal([]byte(typed), &parsed); err == nil {
			redacted := RedactPrivateInfo(parsed, keys, secrets...)
			data, err := json.Marshal(redacted)
			if err == nil {
				return string(data)
			}
		}
		return RedactValue(typed, keys, secrets...)
	default:
		return data
	}
}

// isSensitiveKey compares normalized key spellings so common variants such as
// api_key, api-key, and apiKey share the same redaction behavior.
func isSensitiveKey(key string, keys map[string]struct{}) bool {
	if len(keys) == 0 {
		return false
	}
	lower := canonicalSensitiveKey(key)
	for candidate := range keys {
		normalized := canonicalSensitiveKey(candidate)
		if lower == "keywords" && normalized == "key" {
			continue
		}
		// CZT's user_descr holds the release description. Its text still goes through redaction.
		if strings.EqualFold(key, "user_descr") && normalized == "user" {
			continue
		}
		if strings.Contains(lower, normalized) {
			return true
		}
	}
	return false
}

// canonicalSensitiveKey removes separators and case from keys before matching
// them against the sensitive-key set.
func canonicalSensitiveKey(key string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(normalizeSecretText(key))))
}
