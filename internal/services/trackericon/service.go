// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackericon

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
)

const maxIconBytes int64 = 1 << 20

var (
	iconLookupIPAddr = net.DefaultResolver.LookupIPAddr
	iconHTTPClient   = &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("trackericon: too many redirects")
			}
			return validateIconURL(req.Context(), req.URL)
		},
	}
)

// GetTrackerIcon resolves the domain icon, caches it under the dbPath directory, and returns its Base64 Data URL.
func GetTrackerIcon(ctx context.Context, dbPath string, domain string, customURL string) (string, error) {
	sanitized := SafeDomainFilename(domain)
	if sanitized == "" {
		return "", errors.New("invalid domain")
	}
	if parsed := normalizeIconURL(customURL); parsed != nil {
		sum := sha256.Sum256([]byte(parsed.String()))
		sanitized += "-" + hex.EncodeToString(sum[:])[:16]
	}

	iconDir, err := db.Subdir(dbPath, "tracker-icons")
	if err != nil {
		return "", fmt.Errorf("trackericon: resolve subdir: %w", err)
	}

	filePath := filepath.Join(iconDir, sanitized)

	// Helper to load file content and return data URL
	loadAndEncode := func(path string) (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("trackericon: read file: %w", err)
		}
		if len(data) == 0 {
			return "", errors.New("empty icon file (negative cached)")
		}
		mime := DetectIconContentType(data)
		if !isAllowedIconContentType(mime) {
			return "", fmt.Errorf("trackericon: cached icon has unsupported content type %q", mime)
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		return "data:" + mime + ";base64," + encoded, nil
	}

	// Check if already cached
	if info, err := os.Stat(filePath); err == nil {
		if info.Size() == 0 {
			// Negative cache hit (domain failed to fetch in previous sessions)
			if time.Since(info.ModTime()) < 24*time.Hour {
				return "", errors.New("icon failed to download in a previous attempt (negative cached)")
			}
			// Older than 24h: allow fetching to try again
		} else {
			return loadAndEncode(filePath)
		}
	}

	// Not cached, let's fetch it.
	candidates := iconCandidates(domain, customURL)

	var fetchedData []byte
	for _, urlStr := range candidates {
		parsed, err := url.Parse(urlStr)
		if err != nil {
			continue
		}
		if err := validateIconURL(ctx, parsed); err != nil {
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "image/*")

		resp, err := iconHTTPClient.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			continue
		}

		data, err := readIconResponse(resp.Body)
		_ = resp.Body.Close()
		if err != nil || len(data) == 0 {
			continue
		}
		if !isAllowedIconContentType(DetectIconContentType(data)) {
			continue
		}

		fetchedData = data
		break
	}

	if len(fetchedData) == 0 {
		// Cache the failure as 0 bytes to prevent continuous spamming/network wait
		if err := writeIconCacheFile(filePath, []byte{}); err != nil {
			return "", errors.Join(errors.New("failed to fetch icon from all candidate URLs"), err)
		}
		return "", errors.New("failed to fetch icon from all candidate URLs")
	}

	// Cache successful download
	if err := writeIconCacheFile(filePath, fetchedData); err != nil {
		return "", fmt.Errorf("trackericon: cache icon: %w", err)
	}

	mime := DetectIconContentType(fetchedData)
	encoded := base64.StdEncoding.EncodeToString(fetchedData)
	return "data:" + mime + ";base64," + encoded, nil
}

func readIconResponse(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxIconBytes+1))
	if err != nil {
		return nil, fmt.Errorf("trackericon: read icon response: %w", err)
	}
	if int64(len(data)) > maxIconBytes {
		return nil, errors.New("trackericon: icon response exceeds 1 MiB")
	}
	return data, nil
}

func writeIconCacheFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp cache file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp cache file: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp cache file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync temp cache file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp cache file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("replace cache file: remove existing: %w", removeErr)
		}
		if renameErr := os.Rename(tmpPath, path); renameErr != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("replace cache file: %w", renameErr)
		}
	}

	return nil
}

func iconCandidates(domain string, customURL string) []string {
	var candidates []string
	appendCandidate := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range candidates {
			if existing == value {
				return
			}
		}
		candidates = append(candidates, value)
	}

	if parsed := normalizeIconURL(customURL); parsed != nil {
		appendCandidate(parsed.String())
		appendCandidate(iconRootFaviconURL(parsed))
	}
	appendCandidate("https://" + strings.TrimSpace(domain) + "/favicon.ico")
	appendCandidate("http://" + strings.TrimSpace(domain) + "/favicon.ico")
	appendCandidate("https://" + strings.TrimSpace(domain) + "/favicon.png")
	return candidates
}

func normalizeIconURL(raw string) *url.URL {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Hostname() == "" {
		return nil
	}
	return parsed
}

func iconRootFaviconURL(parsed *url.URL) string {
	if parsed == nil || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host + "/favicon.ico"
}

func validateIconURL(ctx context.Context, parsed *url.URL) error {
	if parsed == nil {
		return errors.New("trackericon: invalid URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("trackericon: unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.User != nil {
		return errors.New("trackericon: URL userinfo is not allowed")
	}
	host := parsed.Hostname()
	if host == "" {
		return errors.New("trackericon: URL host is required")
	}
	if isBlockedIconHostname(host) {
		return fmt.Errorf("trackericon: blocked URL host %q", host)
	}

	addrs, err := iconLookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("trackericon: resolve URL host: %w", err)
	}
	if len(addrs) == 0 {
		return errors.New("trackericon: URL host resolved no addresses")
	}
	for _, addr := range addrs {
		if isBlockedIconIP(addr.IP) {
			return fmt.Errorf("trackericon: blocked URL address %q", addr.IP.String())
		}
	}
	return nil
}

func isBlockedIconHostname(host string) bool {
	normalized := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	return normalized == "localhost" || strings.HasSuffix(normalized, ".localhost")
}

func isBlockedIconIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

func isAllowedIconContentType(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon", "image/bmp", "image/vnd.microsoft.icon":
		return true
	default:
		return false
	}
}

// SafeDomainFilename purges disallowed characters from domain strings for file system safety.
func SafeDomainFilename(domain string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(domain) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// DetectIconContentType detects the image format MIME type.
func DetectIconContentType(data []byte) string {
	ct := http.DetectContentType(data)
	if ct == "application/octet-stream" && isICOFile(data) {
		return "image/x-icon"
	}
	return ct
}

func isICOFile(data []byte) bool {
	if len(data) < 6 {
		return false
	}
	iconType := binary.LittleEndian.Uint16(data[2:4])
	count := binary.LittleEndian.Uint16(data[4:6])
	if (iconType != 1 && iconType != 2) || count == 0 {
		return false
	}
	return len(data) >= 6+int(count)*16
}
