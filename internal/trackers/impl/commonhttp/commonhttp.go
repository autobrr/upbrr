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
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type FileField struct {
	FieldName   string
	FileName    string
	Path        string
	ContentType string
	Content     []byte
}

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
// database. When both sources are available, startup file cookies win on conflicts so
// a fresh startup bootstrap can override stale persisted values while still preserving
// DB-only cookies.
// A nil ctx is accepted and treated as context.Background(); callers should pass
// an explicit request-scoped context whenever possible.
func LoadCookiesForTracker(ctx context.Context, dbPath string, trackerID string, cookieStore CookieStore, encryptionKey []byte) (map[string]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var storeCookies map[string]string

	// Load database cookies first so startup cookie files can overwrite stale entries.
	if cookieStore != nil && len(encryptionKey) > 0 {
		cookies, err := cookieStore.GetAllTrackerCookies(ctx, trackerID, encryptionKey)
		if err != nil {
			return nil, fmt.Errorf("load tracker %s cookies from cookie store: %w", trackerID, err)
		}
		if len(cookies) > 0 {
			storeCookies = cookies
		}
	}

	// Load startup file cookies and let them override any persisted DB values.
	candidates := CookiePathCandidates(dbPath, trackerID, ".txt", ".json")
	for _, path := range candidates {
		switch filepath.Ext(path) {
		case ".txt":
			if cookies, err := LoadNetscapeCookies(path, ""); err == nil && len(cookies) > 0 {
				result := make(map[string]string, len(storeCookies)+len(cookies))
				for name, value := range storeCookies {
					result[name] = value
				}
				for _, c := range cookies {
					result[c.Name] = c.Value
				}
				return result, nil
			}
		case ".json":
			if cookies, err := LoadJSONCookieMap(path); err == nil && len(cookies) > 0 {
				if len(storeCookies) == 0 {
					return cookies, nil
				}

				result := make(map[string]string, len(storeCookies)+len(cookies))
				for name, value := range storeCookies {
					result[name] = value
				}
				for name, value := range cookies {
					result[name] = value
				}
				return result, nil
			}
		}
	}

	if len(storeCookies) > 0 {
		return storeCookies, nil
	}

	return nil, errors.New("no cookies found for tracker: " + trackerID)
}

func LoadNetscapeCookies(path string, expectedDomain string) ([]*http.Cookie, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	target := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(expectedDomain)), ".")
	scanner := bufio.NewScanner(file)
	cookies := make([]*http.Cookie, 0, 4)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") {
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
		value := strings.TrimSpace(strings.Join(fields[6:], "\t"))
		if name == "" || value == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{
			Domain: "." + domain,
			Path:   metautil.FirstNonEmptyTrimmed(strings.TrimSpace(fields[2]), "/"),
			Secure: strings.EqualFold(strings.TrimSpace(fields[3]), "TRUE"),
			Name:   name,
			Value:  value,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(cookies) == 0 {
		return nil, errors.New("no valid cookies found")
	}
	return cookies, nil
}

func LoadJSONCookieMap(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
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

func BuildMultipartPayload(fields map[string]string, files []FileField) ([]byte, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", err
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
			return nil, "", err
		}
		payload := file.Content
		if len(payload) == 0 {
			payload, err = os.ReadFile(strings.TrimSpace(file.Path))
			if err != nil {
				_ = writer.Close()
				return nil, "", err
			}
		}
		if _, err := part.Write(payload); err != nil {
			_ = writer.Close()
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

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
				return nil, "", err
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
			return nil, "", err
		}
		payload := file.Content
		if len(payload) == 0 {
			payload, err = os.ReadFile(strings.TrimSpace(file.Path))
			if err != nil {
				_ = writer.Close()
				return nil, "", err
			}
		}
		if _, err := part.Write(payload); err != nil {
			_ = writer.Close()
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func ApplyCookies(req *http.Request, cookies []*http.Cookie) {
	for _, cookie := range cookies {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		req.AddCookie(cookie)
	}
}

func WriteFailureArtifact(meta api.PreparedMetadata, dbPath string, tracker string, name string, body []byte, ext string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", nil
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", err
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return "", err
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
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

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
				return nil, "", err
			}
			return payload, match, nil
		}
	}
	return nil, "", errors.New("matching file not found")
}

func FileBytes(path string) ([]byte, error) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}
