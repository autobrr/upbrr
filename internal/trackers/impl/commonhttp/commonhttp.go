// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package commonhttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// 700 chars keeps enough HTTP error context for short stack traces or JSON fragments
// while avoiding oversized single-line log entries and stored history details.
const maxHTTPErrorDetailLength = 700

const maxHTTPErrorDetailDepth = 10

// DefaultResponsePreviewBytes bounds tracker response bodies used for failure
// artifacts and error text.
const DefaultResponsePreviewBytes int64 = 64 * 1024

var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

// FileField describes one file part in a tracker multipart upload. FieldName is
// the form field; Content is used when present; otherwise Path is read from disk
// and FileName overrides the filename sent in the part. ContentType is carried
// for callers that need to retain media-type metadata, but the current builders
// use Go's default multipart file-part headers.
type FileField struct {
	FieldName   string
	FileName    string
	Path        string
	ContentType string
	Content     []byte
}

// CookiePathCandidates returns cleaned legacy cookie file paths beside dbPath
// for the supplied tracker name and extensions.
func CookiePathCandidates(dbPath string, name string, exts ...string) []string {
	candidates := make([]string, 0, len(exts))
	baseName := strings.TrimSpace(name)
	if strings.TrimSpace(dbPath) == "" || baseName == "" {
		return candidates
	}
	for _, ext := range exts {
		path, err := db.CookiePath(dbPath, baseName+ext)
		if err != nil {
			continue
		}
		candidates = append(candidates, filepath.Clean(path))
	}
	return candidates
}

// CookieStore interface for dependency injection of cookie storage (database or file-based).
// This allows tests and different implementations to be plugged in.
type CookieStore interface {
	GetAllTrackerCookies(ctx context.Context, trackerID string, key []byte) (map[string]string, error)
}

// LoadCookiesForTracker loads cookies for a tracker from startup cookie files and the
// database. When both sources are available, database cookies win on conflicts while
// preserving legacy-file-only cookies.
// Callers must pass an explicit request-scoped context so cancellation and
// deadlines are preserved through database cookie reads.
func LoadCookiesForTracker(ctx context.Context, dbPath string, trackerID string, cookieStore CookieStore, encryptionKey []byte) (map[string]string, error) {
	if ctx == nil {
		return nil, errors.New("commonhttp: context is required")
	}

	var storeCookies map[string]string

	// Load database cookies first so store errors are reported before legacy fallback.
	if cookieStore != nil && len(encryptionKey) > 0 {
		cookies, err := cookieStore.GetAllTrackerCookies(ctx, trackerID, encryptionKey)
		if err != nil {
			return nil, fmt.Errorf("load tracker %s cookies from cookie store: %w", trackerID, err)
		}
		if len(cookies) > 0 {
			storeCookies = cookies
		}
	}

	// Load startup file cookies as fallback/compatibility input, then let persisted
	// DB values win same-name conflicts.
	candidates := CookiePathCandidates(dbPath, trackerID, ".txt", ".json")
	for _, path := range candidates {
		switch filepath.Ext(path) {
		case ".txt":
			if cookies, err := LoadNetscapeCookies(path, ""); err == nil && len(cookies) > 0 {
				result := make(map[string]string, len(storeCookies)+len(cookies))
				for _, c := range cookies {
					result[c.Name] = c.Value
				}
				maps.Copy(result, storeCookies)
				return result, nil
			}
		case ".json":
			if cookies, err := LoadJSONCookieMap(path); err == nil && len(cookies) > 0 {
				if len(storeCookies) == 0 {
					return cookies, nil
				}

				result := make(map[string]string, len(storeCookies)+len(cookies))
				maps.Copy(result, cookies)
				maps.Copy(result, storeCookies)
				return result, nil
			}
		}
	}

	if len(storeCookies) > 0 {
		return storeCookies, nil
	}

	return nil, errors.New("no cookies found for tracker: " + trackerID)
}

// LoadNetscapeCookies reads Netscape-format cookies from path, optionally
// filtering by expectedDomain. It trims names and domains, preserves cookie
// values after the value column, and returns an error when no valid cookies
// match.
func LoadNetscapeCookies(path string, expectedDomain string) ([]*http.Cookie, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Netscape cookie file: %w", err)
	}
	defer file.Close()

	target := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(expectedDomain)), ".")
	scanner := bufio.NewScanner(file)
	cookies := make([]*http.Cookie, 0, 4)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}
		if strings.HasPrefix(trimmedLine, "#HttpOnly_") {
			line = line[strings.Index(line, "#HttpOnly_")+len("#HttpOnly_"):]
		} else if strings.HasPrefix(trimmedLine, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fields[0])), ".")
		if domain == "" {
			continue
		}
		if target != "" && domain != target && !strings.HasSuffix(domain, "."+target) {
			continue
		}
		name := strings.TrimSpace(fields[5])
		value := strings.Join(fields[6:], "\t")
		if name == "" || value == "" {
			continue
		}
		// #nosec G124 -- Netscape cookie import preserves outbound tracker cookie attributes.
		cookies = append(cookies, &http.Cookie{
			Domain: "." + domain,
			Path:   metautil.FirstNonEmptyTrimmed(strings.TrimSpace(fields[2]), "/"),
			Secure: strings.EqualFold(strings.TrimSpace(fields[3]), "TRUE"),
			Name:   name,
			Value:  value,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan Netscape cookie file: %w", err)
	}
	if len(cookies) == 0 {
		return nil, errors.New("no valid cookies found")
	}
	return cookies, nil
}

// LoadJSONCookieMap reads a JSON cookie map from path and returns non-empty
// trimmed names with non-empty values.
func LoadJSONCookieMap(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read JSON cookie file: %w", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("read JSON cookie file: unmarshal: %w", err)
	}
	result := make(map[string]string, len(decoded))
	for key, value := range decoded {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				result[name] = trimmed
			}
		case map[string]any:
			if rawValue, ok := typed["value"]; ok {
				if trimmed := strings.TrimSpace(fmt.Sprint(rawValue)); trimmed != "" {
					result[name] = trimmed
				}
			}
		}
	}
	if len(result) == 0 {
		return nil, errors.New("cookie file has no entries")
	}
	return result, nil
}

// BuildMultipartPayload encodes single-value fields and files as multipart
// form data and returns the payload with its Content-Type header value.
func BuildMultipartPayload(fields map[string]string, files []FileField) ([]byte, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("write multipart field %q: %w", key, err)
		}
	}
	for _, file := range files {
		if strings.TrimSpace(file.FieldName) == "" {
			continue
		}
		name := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(file.FileName), filepath.Base(strings.TrimSpace(file.Path)), "upload.bin")
		part, err := writer.CreateFormFile(file.FieldName, name)
		if err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("create multipart file %q: %w", file.FieldName, err)
		}
		payload := file.Content
		if len(payload) == 0 {
			payload, err = os.ReadFile(strings.TrimSpace(file.Path))
			if err != nil {
				_ = writer.Close()
				return nil, "", fmt.Errorf("read multipart file %q: %w", strings.TrimSpace(file.Path), err)
			}
		}
		if _, err := part.Write(payload); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("write multipart file %q: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

// BuildMultipartPayloadMulti encodes repeated field values and files as
// multipart form data and returns the payload with its Content-Type header
// value.
func BuildMultipartPayloadMulti(fields map[string][]string, files []FileField) ([]byte, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	for _, key := range keys {
		values := fields[key]
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				_ = writer.Close()
				return nil, "", fmt.Errorf("write multipart field %q: %w", key, err)
			}
		}
	}
	for _, file := range files {
		if strings.TrimSpace(file.FieldName) == "" {
			continue
		}
		name := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(file.FileName), filepath.Base(strings.TrimSpace(file.Path)), "upload.bin")
		part, err := writer.CreateFormFile(file.FieldName, name)
		if err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("create multipart file %q: %w", file.FieldName, err)
		}
		payload := file.Content
		if len(payload) == 0 {
			payload, err = os.ReadFile(strings.TrimSpace(file.Path))
			if err != nil {
				_ = writer.Close()
				return nil, "", fmt.Errorf("read multipart file %q: %w", strings.TrimSpace(file.Path), err)
			}
		}
		if _, err := part.Write(payload); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("write multipart file %q: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

// ApplyCookies adds non-empty cookies to req and ignores nil cookies or blank
// names and values.
func ApplyCookies(req *http.Request, cookies []*http.Cookie) {
	for _, cookie := range cookies {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		req.AddCookie(cookie)
	}
}

// ReadUploadResponseBody reads a full success-candidate response for parsers and
// always returns a bounded preview for diagnostics. Non-success responses are
// read only up to previewLimit.
func ReadUploadResponseBody(resp *http.Response, successCandidate bool, previewLimit int64) ([]byte, []byte, error) {
	if previewLimit <= 0 {
		previewLimit = DefaultResponsePreviewBytes
	}
	var (
		body []byte
		err  error
	)
	if successCandidate {
		body, err = io.ReadAll(resp.Body)
	} else {
		body, err = io.ReadAll(io.LimitReader(resp.Body, previewLimit))
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read upload response body: %w", err)
	}
	return body, ResponseBodyPreview(body, previewLimit), nil
}

// ResponseBodyPreview returns body unchanged when it fits the limit, otherwise
// it returns the prefix used for shareable diagnostics.
func ResponseBodyPreview(body []byte, previewLimit int64) []byte {
	if previewLimit <= 0 {
		previewLimit = DefaultResponsePreviewBytes
	}
	if int64(len(body)) <= previewLimit {
		return body
	}
	return body[:previewLimit]
}

// WriteFailureArtifact stores a tracker failure response under the release temp
// directory and returns the written path. It returns an empty path when dbPath or
// the source path is unavailable.
func WriteFailureArtifact(meta api.PreparedMetadata, dbPath string, tracker string, name string, body []byte, ext string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", nil
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return "", fmt.Errorf("create failure artifact dir: %w", err)
	}
	safeTracker := strings.ToUpper(strings.TrimSpace(tracker))
	if safeTracker == "" {
		safeTracker = "TRACKER"
	}
	filename := "[" + safeTracker + "]" + strings.TrimSpace(name)
	if strings.TrimSpace(ext) == "" {
		ext = ".txt"
	}
	path := filepath.Join(tmpDir, filename+ext)
	body = []byte(redaction.RedactValue(string(body), nil))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", fmt.Errorf("write failure artifact: %w", err)
	}
	return path, nil
}

// ReadOptionalFile returns file contents for a non-empty path or an empty string
// when the path is blank or unreadable.
func ReadOptionalFile(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return ""
	}
	return string(payload)
}

// ReadFirstMatching returns the first readable non-directory file matched by the
// supplied glob patterns.
func ReadFirstMatching(dir string, patterns ...string) ([]byte, string, error) {
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			continue
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			payload, err := os.ReadFile(match)
			if err != nil {
				return nil, "", fmt.Errorf("read matching file %q: %w", match, err)
			}
			return payload, match, nil
		}
	}
	return nil, "", errors.New("matching file not found")
}

// FileBytes reads the entire file at path and wraps open/read failures with the
// trimmed path.
func FileBytes(path string) ([]byte, error) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("open file %q: %w", strings.TrimSpace(path), err)
	}
	defer file.Close()
	payload, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", strings.TrimSpace(path), err)
	}
	return payload, nil
}

// HTTPError is a formatted tracker upload failure with redacted response detail.
type HTTPError struct {
	message string
}

// Error returns the formatted, redacted tracker upload failure message.
func (e HTTPError) Error() string {
	return e.message
}

// UploadHTTPError formats a tracker upload HTTP failure with redacted response
// detail extracted from body.
func UploadHTTPError(tracker string, status int, body []byte) HTTPError {
	detail := ExtractHTTPErrorDetail(body)
	tracker = strings.ToUpper(RedactErrorDetail(tracker))
	if detail == "" {
		return HTTPError{message: fmt.Sprintf("trackers: %s upload failed status=%d", tracker, status)}
	}
	return HTTPError{message: fmt.Sprintf("trackers: %s upload failed status=%d: %s", tracker, status, detail)}
}

// UploadHTTPErrorWithURL formats a tracker upload HTTP failure with the
// redacted final URL and response detail.
func UploadHTTPErrorWithURL(tracker string, status int, url string, body []byte) HTTPError {
	detail := ExtractHTTPErrorDetail(body)
	tracker = strings.ToUpper(RedactErrorDetail(tracker))
	url = RedactErrorDetail(url)
	if detail == "" {
		return HTTPError{message: fmt.Sprintf("trackers: %s upload failed status=%d url=%s", tracker, status, url)}
	}
	return HTTPError{message: fmt.Sprintf("trackers: %s upload failed status=%d url=%s: %s", tracker, status, url, detail)}
}

// RedactErrorDetail trims value after applying the repository secret redactor.
func RedactErrorDetail(value string) string {
	return strings.TrimSpace(redaction.RedactValue(value, nil))
}

// ExtractHTTPErrorDetail returns a compact, redacted error detail from plain
// text, HTML, or JSON response bodies.
func ExtractHTTPErrorDetail(body []byte) string {
	text := RedactErrorDetail(string(body))
	if text == "" {
		return ""
	}

	if detail := extractJSONErrorDetail([]byte(text)); detail != "" {
		return detail
	}
	for _, block := range redaction.ExtractJSONBlocks(text) {
		if block.Start < 0 || block.End > len(text) || block.Start >= block.End {
			continue
		}
		if detail := extractJSONErrorDetail([]byte(text[block.Start:block.End])); detail != "" {
			return detail
		}
	}

	return compactHTTPErrorText(text)
}

func extractJSONErrorDetail(body []byte) string {
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}
	return compactHTTPErrorText(formatErrorValue(decoded, "", 0))
}

func formatErrorValue(value any, key string, depth int) string {
	if depth >= maxHTTPErrorDetailDepth {
		return ""
	}

	switch typed := value.(type) {
	case map[string]any:
		for _, candidate := range []string{"errors", "message", "status_message", "detail", "error_description", "reason", "error"} {
			if nested, ok := valueForKey(typed, candidate); ok {
				if formatted := formatErrorValue(nested, candidate, depth+1); formatted != "" {
					return formatted
				}
			}
		}
		return formatErrorMap(typed, depth)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if formatted := formatErrorValue(item, "", depth+1); formatted != "" {
				parts = append(parts, formatted)
			}
		}
		return strings.Join(parts, "; ")
	case string:
		return RedactErrorDetail(typed)
	case nil:
		return ""
	default:
		if isBooleanStatusKey(key) {
			return ""
		}
		return RedactErrorDetail(fmt.Sprint(typed))
	}
}

func isBooleanStatusKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "success", "status", "error":
		return true
	default:
		return false
	}
}

func formatErrorMap(values map[string]any, depth int) string {
	if depth >= maxHTTPErrorDetailDepth {
		return ""
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.Contains(strings.ToLower(key), "token") || strings.Contains(strings.ToLower(key), "key") {
			continue
		}
		formatted := formatErrorValue(values[key], key, depth+1)
		if formatted == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(key)+": "+formatted)
	}
	return strings.Join(parts, "; ")
}

func valueForKey(values map[string]any, target string) (any, bool) {
	for key, value := range values {
		if strings.EqualFold(strings.TrimSpace(key), target) {
			return value, true
		}
	}
	return nil, false
}

func compactHTTPErrorText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = RedactErrorDetail(text)
	text = htmlTagPattern.ReplaceAllString(text, " ")
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= maxHTTPErrorDetailLength {
		return text
	}
	return strings.TrimSpace(text[:maxHTTPErrorDetailLength]) + "..."
}
