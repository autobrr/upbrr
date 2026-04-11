// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package legacy

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// LegacyConfig holds the parsed sections from a legacy Upload Assistant config.py.
type LegacyConfig struct {
	Default        map[string]any
	Trackers       map[string]any
	TorrentClients map[string]any
}

// ParseLegacyConfig extracts the `config = { ... }` assignment from the raw
// content of a legacy Upload Assistant config.py file and returns the parsed
// structure.
func ParseLegacyConfig(data []byte) (*LegacyConfig, error) {
	src := string(data)

	dictSrc, err := extractConfigDict(src)
	if err != nil {
		return nil, err
	}

	p := newParser(dictSrc)
	val, err := p.parseValue()
	if err != nil {
		return nil, fmt.Errorf("legacy config: %w", err)
	}

	root, ok := val.(map[string]any)
	if !ok {
		return nil, errors.New("legacy config: top-level value is not a dictionary")
	}

	result := &LegacyConfig{
		Default:        make(map[string]any),
		Trackers:       make(map[string]any),
		TorrentClients: make(map[string]any),
	}

	for key, value := range root {
		switch strings.ToUpper(key) {
		case "DEFAULT":
			if m, ok := value.(map[string]any); ok {
				result.Default = m
			}
		case "TRACKERS":
			if m, ok := value.(map[string]any); ok {
				result.Trackers = m
			}
		case "TORRENT_CLIENTS":
			if m, ok := value.(map[string]any); ok {
				result.TorrentClients = m
			}
		}
	}

	return result, nil
}

// extractConfigDict finds the `config = { ... }` assignment in the source and
// returns the dict literal portion starting from `{`.
func extractConfigDict(src string) (string, error) {
	idx := 0
	for idx < len(src) {
		pos := strings.Index(src[idx:], "config")
		if pos < 0 {
			break
		}
		pos += idx

		// Ensure `config` is not part of a larger identifier.
		if pos > 0 {
			r, _ := utf8.DecodeLastRuneInString(src[:pos])
			if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
				idx = pos + len("config")
				continue
			}
		}
		after := pos + len("config")
		if after < len(src) {
			r, _ := utf8.DecodeRuneInString(src[after:])
			if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
				idx = after
				continue
			}
		}

		rest := skipWhitespaceAndComments(src[after:])
		if len(rest) == 0 || rest[0] != '=' {
			idx = after
			continue
		}
		rest = skipWhitespaceAndComments(rest[1:])

		if len(rest) == 0 || rest[0] != '{' {
			idx = after
			continue
		}

		return rest, nil
	}

	return "", errors.New("legacy config: could not find 'config = {' assignment")
}

// skipWhitespaceAndComments returns src with leading ASCII whitespace and
// Python line comments removed. Comments run to the next newline.
func skipWhitespaceAndComments(src string) string {
	i := 0
	for i < len(src) {
		ch := src[i]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			i++
			continue
		}
		if ch == '#' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		break
	}
	return src[i:]
}

// parser is a minimal recursive-descent parser for Python dict/list/scalar
// literals.
type parser struct {
	src string
	pos int
}

func newParser(src string) *parser {
	return &parser{src: src, pos: 0}
}

func (p *parser) skipWhitespaceAndComments() {
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			p.pos++
			continue
		}
		if ch == '#' {
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		break
	}
}

func (p *parser) peek() (byte, bool) {
	p.skipWhitespaceAndComments()
	if p.pos >= len(p.src) {
		return 0, false
	}
	return p.src[p.pos], true
}

func (p *parser) expect(ch byte) error {
	p.skipWhitespaceAndComments()
	if p.pos >= len(p.src) {
		return fmt.Errorf("expected '%c' but reached end of input", ch)
	}
	if p.src[p.pos] != ch {
		return fmt.Errorf("expected '%c' but got '%c' at position %d", ch, p.src[p.pos], p.pos)
	}
	p.pos++
	return nil
}

func (p *parser) parseValue() (any, error) {
	ch, ok := p.peek()
	if !ok {
		return nil, errors.New("unexpected end of input")
	}

	switch ch {
	case '{':
		return p.parseDict()
	case '[':
		return p.parseList()
	case '(':
		return p.parseTuple()
	case '\'', '"':
		return p.parseString()
	default:
		return p.parseScalar()
	}
}

func (p *parser) parseDict() (map[string]any, error) {
	if err := p.expect('{'); err != nil {
		return nil, err
	}

	result := make(map[string]any)
	for {
		ch, ok := p.peek()
		if !ok {
			return nil, errors.New("unterminated dict")
		}
		if ch == '}' {
			p.pos++
			return result, nil
		}

		key, err := p.parseValue()
		if err != nil {
			return nil, fmt.Errorf("dict key: %w", err)
		}
		keyStr := fmt.Sprintf("%v", key)

		if err := p.expect(':'); err != nil {
			return nil, err
		}

		val, err := p.parseValue()
		if err != nil {
			return nil, fmt.Errorf("dict value for key %q: %w", keyStr, err)
		}
		result[keyStr] = val

		ch, ok = p.peek()
		if !ok {
			return nil, errors.New("unterminated dict")
		}
		if ch == ',' {
			p.pos++
			continue
		}
		if ch == '}' {
			continue
		}
		return nil, fmt.Errorf("expected ',' or '}' in dict at position %d", p.pos)
	}
}

func (p *parser) parseList() ([]any, error) {
	if err := p.expect('['); err != nil {
		return nil, err
	}

	var result []any
	for {
		ch, ok := p.peek()
		if !ok {
			return nil, errors.New("unterminated list")
		}
		if ch == ']' {
			p.pos++
			return result, nil
		}

		val, err := p.parseValue()
		if err != nil {
			return nil, fmt.Errorf("list element: %w", err)
		}
		result = append(result, val)

		ch, ok = p.peek()
		if !ok {
			return nil, errors.New("unterminated list")
		}
		if ch == ',' {
			p.pos++
			continue
		}
		if ch == ']' {
			continue
		}
		return nil, fmt.Errorf("expected ',' or ']' in list at position %d", p.pos)
	}
}

func (p *parser) parseTuple() ([]any, error) {
	if err := p.expect('('); err != nil {
		return nil, err
	}

	var result []any
	for {
		ch, ok := p.peek()
		if !ok {
			return nil, errors.New("unterminated tuple")
		}
		if ch == ')' {
			p.pos++
			return result, nil
		}

		val, err := p.parseValue()
		if err != nil {
			return nil, fmt.Errorf("tuple element: %w", err)
		}
		result = append(result, val)

		ch, ok = p.peek()
		if !ok {
			return nil, errors.New("unterminated tuple")
		}
		if ch == ',' {
			p.pos++
			continue
		}
		if ch == ')' {
			continue
		}
		return nil, fmt.Errorf("expected ',' or ')' in tuple at position %d", p.pos)
	}
}

func (p *parser) parseString() (string, error) {
	p.skipWhitespaceAndComments()
	if p.pos >= len(p.src) {
		return "", errors.New("expected string but reached end of input")
	}

	quote := p.src[p.pos]
	if quote != '\'' && quote != '"' {
		return "", fmt.Errorf("expected quote but got '%c'", quote)
	}

	// Check for triple-quoted strings.
	if p.pos+2 < len(p.src) && p.src[p.pos+1] == quote && p.src[p.pos+2] == quote {
		return p.parseTripleQuotedString(quote)
	}

	p.pos++ // skip opening quote
	var sb strings.Builder
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == '\\' {
			p.pos++
			if p.pos >= len(p.src) {
				return "", errors.New("unterminated string escape")
			}
			escaped := p.src[p.pos]
			switch escaped {
			case '\\':
				sb.WriteByte('\\')
			case '\'':
				sb.WriteByte('\'')
			case '"':
				sb.WriteByte('"')
			case 'n':
				sb.WriteByte('\n')
			case 'r':
				sb.WriteByte('\r')
			case 't':
				sb.WriteByte('\t')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(escaped)
			}
			p.pos++
			continue
		}
		if ch == quote {
			p.pos++ // skip closing quote
			return sb.String(), nil
		}
		sb.WriteByte(ch)
		p.pos++
	}
	return "", errors.New("unterminated string")
}

func (p *parser) parseTripleQuotedString(quote byte) (string, error) {
	p.pos += 3 // skip opening triple quotes
	tripleEnd := string([]byte{quote, quote, quote})
	idx := strings.Index(p.src[p.pos:], tripleEnd)
	if idx < 0 {
		return "", errors.New("unterminated triple-quoted string")
	}
	result := p.src[p.pos : p.pos+idx]
	p.pos += idx + 3
	return result, nil
}

func (p *parser) parseScalar() (any, error) {
	p.skipWhitespaceAndComments()
	if p.pos >= len(p.src) {
		return nil, errors.New("expected scalar but reached end of input")
	}

	start := p.pos
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == ',' || ch == '}' || ch == ']' || ch == ')' || ch == ':' || ch == '#' {
			break
		}
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			break
		}
		p.pos++
	}

	token := strings.TrimSpace(p.src[start:p.pos])
	if token == "" {
		return nil, fmt.Errorf("empty scalar at position %d", start)
	}

	switch token {
	case "True":
		return true, nil
	case "False":
		return false, nil
	case "None":
		return nil, nil
	}

	if i, err := strconv.ParseInt(token, 10, 64); err == nil {
		return int(i), nil
	}

	if f, err := strconv.ParseFloat(token, 64); err == nil {
		return f, nil
	}

	return token, nil
}
