// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackericon

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
)

var iconHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
}

// GetTrackerIcon resolves the domain icon, caches it under the dbPath directory, and returns its Base64 Data URL.
func GetTrackerIcon(ctx context.Context, dbPath string, domain string, customURL string) (string, error) {
	sanitized := SafeDomainFilename(domain)
	if sanitized == "" {
		return "", errors.New("invalid domain")
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

	// Not cached, let's fetch it!
	var candidates []string
	if customURL != "" {
		candidates = append(candidates, strings.TrimSuffix(customURL, "/")+"/favicon.ico")
	}
	candidates = append(candidates, "https://"+domain+"/favicon.ico")
	candidates = append(candidates, "http://"+domain+"/favicon.ico")
	candidates = append(candidates, "https://"+domain+"/favicon.png")

	var fetchedData []byte
	for _, urlStr := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
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

		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // max 1MB
		_ = resp.Body.Close()
		if err != nil || len(data) == 0 {
			continue
		}

		fetchedData = data
		break
	}

	if len(fetchedData) == 0 {
		// Cache the failure as 0 bytes to prevent continuous spamming/network wait
		_ = os.WriteFile(filePath, []byte{}, 0o600)
		return "", errors.New("failed to fetch icon from all candidate URLs")
	}

	// Cache successful download
	_ = os.WriteFile(filePath, fetchedData, 0o600)

	mime := DetectIconContentType(fetchedData)
	encoded := base64.StdEncoding.EncodeToString(fetchedData)
	return "data:" + mime + ";base64," + encoded, nil
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
	if ct == "application/octet-stream" {
		return "image/x-icon"
	}
	return ct
}
