// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"encoding/json/v2"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/redaction"
)

var (
	inputLogPrivateFieldEnd = regexp.MustCompile(`(?i)\b` + inputLogPrivateKeys + `"?\s*[:=]\s*$`)
	inputLogPrivateKeys     = `(?:group[_ -]?ids?|save[_ -]?path|seeds|seeders?|seeder[_ -]?count|num[_ -]?seeds|tracker[_ -]?ids?|extracted[_ -]?ids?|candidate[_ -]?ids?|(?:added[_ -]?)?torrent[_ -]?ids?)`
	inputLogPrivateKey      = regexp.MustCompile(`(?i)^` + inputLogPrivateKeys + `$`)
	inputLogPrivateField    = regexp.MustCompile(`(?i)(\b` + inputLogPrivateKeys +
		`"?\s*[:=]\s*)("(?:\\.|[^"\\])*"|'[^'\r\n]*'|\[[^\r\n]*?\](?:\]|$)?|[^\s"',;)}\]]+)`)
	inputLogSavePath    = regexp.MustCompile(`(?im)(\bsave[_ -]?path\s*[:=][ \t]*)[^"'\[\s][^\r\n]*?(\s+content[_ -]?path=|$)`)
	inputLogContextID   = regexp.MustCompile(`(?i)(\b(?:id|remote[_ -]?id)"?\s*[:=]\s*)("(?:\\.|[^"\\])*"|\[REDACTED\]|[^\s"',;:)}\]]+)`)
	inputLogASCID       = regexp.MustCompile(`(?i)(\bfor ID )[^:\s]+`)
	inputLogDupeSetIDs  = regexp.MustCompile(`(?i)(\bcandidates=)("(?:\\.|[^"\\])*")`)
	inputLogDupeRefs    = regexp.MustCompile(`(?i)(\bcandidate\[id=)(?:\[REDACTED\]|[^\s,"\\:\]]+)`)
	inputLogMissingDupe = regexp.MustCompile(`(?i)(\bcandidate=)[^\s,"\\:{}\]]+(:)`)
	inputLogTrackerIDs  = regexp.MustCompile(`(?im)(metadata: pathed tracker ids for .*?: )[^\r\n]+`)
	inputLogComment     = regexp.MustCompile(`(?im)(metadata: torrent .*?\bcomment=)"(?:\\.|[^"\\])*"`)
	inputLogLocation    = regexp.MustCompile(
		`(?i)[a-z][a-z0-9+.-]*://(?:\[[0-9a-f:]+\]|\[REDACTED\]|[^\s<>"'\\\[\]])+|(?:^|[\s"'=(:,\[])([a-z]:[\\/]+|\\\\+|/[^\s"'<>|{}[\],;)])`,
	)
	inputLogPathEnd      = regexp.MustCompile(`[\t\r\n"<>|]|\s+\(?[a-zA-Z_][\w.-]*=|\s+->\s+|\s+\([^\\/()]*[/\\][^()]*\)`)
	inputLogSourceLine   = regexp.MustCompile(`(?m)^((?:metadata: normalized path|mediainfo: analyzing) )([^\r\n]+)`)
	inputLogArtifactPath = regexp.MustCompile(
		`(?m)^(metadata: tracker (?:artifacts tracker=\S+ dir=|description (?:saved|exists) tracker=\S+ path=))([^\r\n]+?)( desc=(?:true|false) images=\d+ keepImages=(?:true|false)|$)`,
	)
	inputLogCachePath       = regexp.MustCompile(`(?m)^(mediainfo: checking cache at )([^\r\n]+?)( \(text=(?:true|false) json=(?:true|false)\))`)
	inputLogInjectionSource = regexp.MustCompile(
		`(?m)^(clients: injection input source=)([^\r\n]+?)( tracker=\S+ has_file=(?:true|false) has_url=(?:true|false) configured_clients=\d+)`,
	)
	inputLogBaseField  = regexp.MustCompile(`(?i)\b(?:source[_ -]?path|source|content[_ -]?path|ffmpeg|ffprobe)"?\s*[:=]\s*["']?$`)
	inputLogTorrentURL = regexp.MustCompile(
		`(?i)/(?:torrents?|details|download|view|viewtorrent|torrent-download|announce|browse/t)/[^/]+|/(?:t|browse)/[0-9]+(?:/|$)`,
	)
	inputLogTorrentPage = regexp.MustCompile(`(?i)/(?:torrents?(?:-details)?|details|download|viewtorrent)\.php$`)
	inputLogTorrentRef  = regexp.MustCompile(`(?i)(^|[\s"'=(])(/(?:torrents?|details|download|view)/)[0-9]+`)
	inputLogTorrentKey  = regexp.MustCompile(`(?i)([?&](?:torrent_?id|group_?id)=)(?:%5Bredacted%5D|\[redacted\]|[^&\s#"',;)}\]]+)`)
	inputLogQuoted      = regexp.MustCompile(`"(?:\\.|[^"\\])*"`)
)

// Apply the same rules to fresh scoped records and retained records written by
// older versions. This operates on diagnostic text, never on workflow inputs.
func sanitizeInputLog(message, dbRoot string, sources ...string) string {
	return sanitizeInputLogRecord(message, dbRoot, inputLogHasTrackerIDs(message), sources)
}

func inputLogHasTrackerIDs(message string) bool {
	return strings.HasPrefix(message, "metadata: tracker lookup ") || strings.HasPrefix(message, "unit3d: ") ||
		strings.HasPrefix(message, "trackers:") && strings.Contains(message, "response")
}

func sanitizeInputLogRecord(message, dbRoot string, trackerIDs bool, sources []string) string {
	message = sanitizeInputLogProducerPaths(message, dbRoot, sources)
	var out strings.Builder
	end := 0
	for _, block := range redaction.ExtractJSONBlocks(message) {
		prefix := message[end:block.Start]
		// A bracketed directory or a URL component can look like JSON.
		if block.End < len(message) && strings.ContainsAny(message[block.End:block.End+1], `/\`) {
			continue
		}
		tokenStart := strings.LastIndexAny(prefix, " \t\r\n<>\"'") + 1
		if strings.Contains(prefix[tokenStart:], "://") {
			continue
		}
		var value any
		if json.Unmarshal([]byte(message[block.Start:block.End]), &value) != nil {
			continue
		}
		if inputLogPrivateFieldEnd.MatchString(prefix) {
			value = "[REDACTED]"
		} else {
			value = sanitizeInputLogJSONValue(value, dbRoot, trackerIDs, sources)
		}
		encoded, err := json.Marshal(value, json.Deterministic(true))
		if err != nil {
			continue
		}
		out.WriteString(sanitizeInputLogText(prefix, dbRoot, trackerIDs, sources))
		out.Write(encoded)
		end = block.End
	}
	out.WriteString(sanitizeInputLogText(message[end:], dbRoot, trackerIDs, sources))
	return out.String()
}

func sanitizeInputLogJSONValue(value any, dbRoot string, trackerResponse bool, sources []string) any {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if inputLogPrivateKey.MatchString(key) || (trackerResponse && slices.Contains([]string{"id", "remote_id", "remoteid"}, strings.ToLower(key))) {
				value[key] = "[REDACTED]"
				continue
			}
			root := dbRoot
			if inputLogBaseField.MatchString(key + "=") {
				root = ""
			}
			value[key] = sanitizeInputLogJSONValue(item, root, trackerResponse, sources)
		}
	case []any:
		for i, item := range value {
			value[i] = sanitizeInputLogJSONValue(item, dbRoot, trackerResponse, sources)
		}
	case string:
		return sanitizeInputLogText(sanitizeInputLogProducerPaths(value, dbRoot, sources), dbRoot, trackerResponse, sources)
	}
	return value
}

func sanitizeInputLogText(message, dbRoot string, trackerResponse bool, sources []string) string {
	message = sanitizeInputLogFields(message, trackerResponse)
	message = sanitizeInputLogQuoted(message, dbRoot, trackerResponse, sources)
	return sanitizeInputLogLocations(message, dbRoot, sources)
}

func sanitizeInputLogFields(message string, trackerResponse bool) string {
	message = inputLogSavePath.ReplaceAllString(message, "${1}[REDACTED]${2}")
	message = maskInputLogFields(message, inputLogPrivateField)
	message = inputLogTrackerIDs.ReplaceAllString(message, "${1}[REDACTED]")
	message = inputLogComment.ReplaceAllString(message, `${1}"[REDACTED]"`)
	message = inputLogTorrentRef.ReplaceAllString(message, "${1}${2}[REDACTED]")
	if trackerResponse {
		message = maskInputLogFields(message, inputLogContextID)
	}
	if strings.HasPrefix(message, "dupechecking: ") {
		message = maskInputLogFields(message, inputLogDupeSetIDs)
		message = inputLogDupeRefs.ReplaceAllString(message, "${1}[REDACTED]")
		message = inputLogMissingDupe.ReplaceAllString(message, "${1}[REDACTED]${2}")
	}
	if strings.HasPrefix(message, "ASC filename ") {
		message = inputLogASCID.ReplaceAllString(message, "${1}[REDACTED]")
	}
	return inputLogTorrentKey.ReplaceAllString(message, "${1}[REDACTED]")
}

func maskInputLogFields(message string, pattern *regexp.Regexp) string {
	return pattern.ReplaceAllStringFunc(message, func(field string) string {
		match := pattern.FindStringSubmatch(field)
		if strings.HasPrefix(match[2], `"`) {
			return match[1] + `"[REDACTED]"`
		}
		return match[1] + "[REDACTED]"
	})
}

func sanitizeInputLogQuoted(message, dbRoot string, trackerResponse bool, sources []string) string {
	var out strings.Builder
	cursor := 0
	for _, match := range inputLogQuoted.FindAllStringIndex(message, -1) {
		start, end := match[0], match[1]
		if !strings.ContainsAny(message[start:end], "/\\") {
			continue
		}
		raw, err := strconv.Unquote(message[start:end])
		if err != nil {
			continue
		}
		root := dbRoot
		if inputLogBaseField.MatchString(message[:start]) {
			root = ""
		}
		var safe string
		if isAbsoluteLogPath(raw) {
			safe = inputLogPath(raw, root, sources)
		} else {
			safe = sanitizeInputLogLocations(sanitizeInputLogFields(raw, trackerResponse), root, sources)
		}
		if safe == raw {
			continue
		}
		encoded, err := json.Marshal(safe)
		if err == nil {
			out.WriteString(message[cursor:start])
			out.Write(encoded)
			cursor = end
		}
	}
	if cursor == 0 {
		return message
	}
	out.WriteString(message[cursor:])
	return out.String()
}

func sanitizeInputLogProducerPaths(message, dbRoot string, sources []string) string {
	// These producers emit unquoted paths with stable suffixes. Their paths
	// can include literal JSON and text that resembles other diagnostic fields.
	for _, pattern := range []*regexp.Regexp{inputLogCachePath, inputLogArtifactPath} {
		message = pattern.ReplaceAllStringFunc(message, func(field string) string {
			match := pattern.FindStringSubmatch(field)
			path := match[2]
			if strings.HasPrefix(path, "{db}/") || path == "{db}" {
				return field
			}
			if !isAbsoluteLogPath(path) {
				if absolute, err := filepath.Abs(path); err == nil {
					path = absolute
				}
			}
			return match[1] + inputLogPath(path, dbRoot, sources) + match[3]
		})
	}
	message = inputLogSourceLine.ReplaceAllStringFunc(message, func(field string) string {
		match := inputLogSourceLine.FindStringSubmatch(field)
		return match[1] + inputLogPath(match[2], "", nil)
	})
	return inputLogInjectionSource.ReplaceAllStringFunc(message, func(field string) string {
		match := inputLogInjectionSource.FindStringSubmatch(field)
		return match[1] + inputLogPath(match[2], "", nil) + match[3]
	})
}

func sanitizeInputLogLocations(message, dbRoot string, sources []string) string {
	for _, source := range sources {
		if source == "" || normalizeLogPath(source) == pathing.Base(source) || isAbsoluteLogPath(source) {
			continue
		}
		// Relative source spellings have no drive or root for the text scanner.
		message = strings.ReplaceAll(message, strconv.Quote(source), strconv.Quote(pathing.Base(source)))
		message = strings.ReplaceAll(message, source, pathing.Base(source))
	}

	locations := inputLogLocation.FindAllStringSubmatchIndex(message, -1)
	if len(locations) == 0 {
		return message
	}
	var out strings.Builder
	cursor := 0
	for i, location := range locations {
		start, end := location[0], location[1]
		if location[2] < 0 {
			out.WriteString(message[cursor:start])
			out.WriteString(sanitizeInputLogURL(message[start:end], message[:start]))
			cursor = end
			continue
		}
		start = location[2]
		// Keep BBCode closing tags in descriptions.
		if message[location[0]] == '[' && message[start] == '/' &&
			!strings.ContainsAny(strings.SplitN(message[start+1:], "]", 2)[0], "/\\ \t\r\n") {
			continue
		}
		limit := len(message)
		if i+1 < len(locations) {
			limit = locations[i+1][0]
		}
		if suffix := inputLogPathEnd.FindStringIndex(message[end:limit]); suffix != nil {
			limit = end + suffix[0]
		}
		if start > 0 && (message[start-1] == '"' || message[start-1] == '\'') {
			if closeQuote := strings.IndexByte(message[end:limit], message[start-1]); closeQuote >= 0 {
				limit = end + closeQuote
			}
		}
		path := strings.TrimRight(message[start:limit], " \t\r\n),];")
		out.WriteString(message[cursor:start])
		root := dbRoot
		if inputLogBaseField.MatchString(message[cursor:start]) {
			root = ""
		}
		out.WriteString(inputLogPath(path, root, sources))
		cursor = start + len(path)
	}
	out.WriteString(message[cursor:])
	return out.String()
}

func isAbsoluteLogPath(value string) bool {
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`) ||
		len(value) > 2 && value[1] == ':' && strings.ContainsRune(`/\`, rune(value[2]))
}

func normalizeLogPath(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\\`, `\`), `\`, "/")
}

func inputLogPath(value, dbRoot string, sources []string) string {
	normalized := normalizeLogPath(value)
	base := pathing.Base(normalized)
	if base == "" || base == "/" || strings.HasSuffix(base, ":") {
		return "[REDACTED]"
	}
	root := strings.TrimRight(normalizeLogPath(dbRoot), "/")
	fold := runtime.GOOS == "windows" || (len(normalized) > 1 && normalized[1] == ':')
	equal := func(a, b string) bool { return a == b || fold && strings.EqualFold(a, b) }
	for _, source := range sources {
		input := normalizeLogPath(source)
		if input == "" {
			continue
		}
		if !isAbsoluteLogPath(input) {
			if absolute, err := filepath.Abs(input); err == nil {
				input = normalizeLogPath(absolute)
			}
		}
		if equal(normalized, input) || len(normalized) > len(input) && equal(normalized[:len(input)+1], input+"/") {
			return base
		}
	}
	if root != "" {
		depth := strings.Count(root, "/")
		parts := strings.SplitN(normalized, "/", depth+2)
		prefix := strings.Join(parts[:len(parts)-1], "/")
		if equal(normalized, root) {
			return "{db}"
		}
		if equal(prefix, root) {
			relative := parts[len(parts)-1]
			dir, _, _ := strings.Cut(relative, "/")
			switch strings.ToLower(dir) {
			case "tmp", "cache", "logs", "nfo", "tracker-icons", "cookies":
				return "{db}/" + relative
			}
		}
	}
	return base
}

func sanitizeInputLogURL(raw, before string) string {
	trimmed := strings.TrimRight(raw, "),;")
	parsed, err := url.Parse(trimmed)
	if err == nil && parsed.Scheme == "file" {
		return inputLogPath(parsed.Path, "", nil) + raw[len(trimmed):]
	}
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return raw
	}
	private := inputLogTorrentURL.MatchString(parsed.Path) || strings.HasSuffix(before, "torrent URL: ")
	for key := range parsed.Query() {
		switch strings.ToLower(strings.ReplaceAll(key, "_", "")) {
		case "id":
			private = private || inputLogTorrentPage.MatchString(parsed.Path) || parsed.Query().Get("page") == "torrent-details"
		case "torrentid", "groupid":
			private = true
		}
	}
	if !private {
		return raw
	}
	return parsed.Scheme + "://" + parsed.Host + "/[REDACTED]" + raw[len(trimmed):]
}
