// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path" //nolint:depguard // Reads poster URL path extension, not local filesystem extension.
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildDescription(meta api.UploadSubject, trackerConfig config.TrackerConfig, appConfig config.Config, assets trackers.DescriptionAssets) string {
	baseDescription := strings.TrimSpace(assets.Description)
	if assets.Final {
		return baseDescription
	}
	if baseDescription != "" {
		report := CleanDescription(baseDescription, meta.DiscType)
		baseDescription = strings.TrimSpace(report.Description)
	}

	var sections []string
	if mediaSection, err := buildMediaSection(meta, appConfig.MainSettings.DBPath); err == nil && mediaSection != "" {
		sections = append(sections, mediaSection)
	}
	if strings.TrimSpace(baseDescription) != "" {
		sections = append(sections, convertDescription(baseDescription))
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") && strings.TrimSpace(meta.ServiceLongName) != "" && trackerConfig.AddWebSourceToDesc {
		sections = append(
			sections,
			fmt.Sprintf("[quote][align=center]This release is sourced from %s[/align][/quote]", strings.TrimSpace(meta.ServiceLongName)),
		)
	}
	if shots := buildScreenshotSection(meta, assets.Screenshots); shots != "" {
		sections = append(sections, shots)
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func buildScreenshotSection(meta api.UploadSubject, screenshots []api.ScreenshotImage) string {
	if len(screenshots) == 0 {
		return ""
	}
	minimum := max(len(meta.FileList), max(meta.Options.Screens, 3))
	allowed := make([]string, 0, len(screenshots))
	for _, screenshot := range screenshots {
		rawURL := strings.TrimSpace(screenshot.RawURL)
		if rawURL == "" {
			rawURL = strings.TrimSpace(screenshot.ImgURL)
		}
		if rawURL == "" {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(screenshot.Host))
		if host == "" {
			host = imagehost.ExtractHost(rawURL)
		}
		if !isPTPImageHost(host) {
			continue
		}
		allowed = append(allowed, "[img]"+rawURL+"[/img]")
	}
	if len(allowed) < minimum {
		return ""
	}
	return strings.Join(allowed, "\n")
}

func isPTPImageHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "pixhost", "imgbb", "onlyimage", "ptscreens", "passtheimage":
		return true
	default:
		return false
	}
}

func convertDescription(value string) string {
	replacer := strings.NewReplacer(
		"[spoiler", "[hide",
		"[/spoiler]", "[/hide]",
		"[center]", "[align=center]",
		"[/center]", "[/align]",
		"[left]", "[align=left]",
		"[/left]", "[/align]",
		"[right]", "[align=right]",
		"[/right]", "[/align]",
		"[h1]", "[u][b]",
		"[/h1]", "[/b][/u]",
		"[h2]", "[u][b]",
		"[/h2]", "[/b][/u]",
		"[h3]", "[u][b]",
		"[/h3]", "[/b][/u]",
	)
	return replacer.Replace(strings.TrimSpace(value))
}

func rehostPosterToSelectedHost(ctx context.Context, req trackers.PreparationInput, imageURL string) string {
	trimmedURL := strings.TrimSpace(imageURL)
	if trimmedURL == "" {
		return ""
	}
	if req.UploadImages == nil {
		return trimmedURL
	}
	if req.Meta.ImageHostOverrides.SkipUpload != nil && *req.Meta.ImageHostOverrides.SkipUpload {
		return trimmedURL
	}

	selectedHost := strings.ToLower(strings.TrimSpace(req.SelectedImageHost))
	if selectedHost == "" {
		return trimmedURL
	}
	if strings.EqualFold(strings.TrimSpace(imagehost.ExtractHost(trimmedURL)), selectedHost) {
		return trimmedURL
	}

	posterPath, err := downloadPoster(ctx, req.Meta, req.Runtime.DBPath, trimmedURL)
	if err != nil {
		logPosterRehostFailure(req.Logger, selectedHost, err)
		return trimmedURL
	}
	uploaded, err := req.UploadImages(ctx, []api.ScreenshotImage{{Path: posterPath}})
	if err != nil {
		logPosterRehostFailure(req.Logger, selectedHost, err)
		return trimmedURL
	}
	if len(uploaded) == 0 {
		logPosterRehostFailure(req.Logger, selectedHost, errors.New("upload returned no links"))
		return trimmedURL
	}
	uploadedURL := metautil.FirstNonEmptyTrimmed(uploaded[0].RawURL, uploaded[0].ImgURL, uploaded[0].WebURL)
	if strings.TrimSpace(uploadedURL) == "" {
		logPosterRehostFailure(req.Logger, selectedHost, errors.New("upload returned blank link"))
		return trimmedURL
	}
	if req.Logger != nil {
		req.Logger.Infof("trackers: PTP poster rehosted to %s", selectedHost)
	}
	return strings.TrimSpace(uploadedURL)
}

func downloadPoster(ctx context.Context, meta api.UploadSubject, dbPath string, imageURL string) (string, error) {
	if err := validatePosterURL(imageURL); err != nil {
		return "", err
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", fmt.Errorf("poster request: %w", err)
	}
	httpReq.Header.Set("User-Agent", ptpUserAgent)

	client := newPosterHTTPClient()
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("poster download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("poster download status=%d", resp.StatusCode)
	}

	const maxPosterBytes = 25 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPosterBytes+1))
	if err != nil {
		return "", fmt.Errorf("poster read: %w", err)
	}
	if len(body) == 0 {
		return "", errors.New("poster download returned empty body")
	}
	if len(body) > maxPosterBytes {
		return "", errors.New("poster exceeds maximum size")
	}

	posterPath := filepath.Join(tmpDir, "PTP_POSTER"+posterExtension(imageURL, resp.Header.Get("Content-Type")))
	if err := os.WriteFile(posterPath, body, 0o600); err != nil {
		return "", fmt.Errorf("poster write: %w", err)
	}
	return posterPath, nil
}

func posterExtension(imageURL string, contentType string) string {
	if parsed, err := url.Parse(imageURL); err == nil {
		switch ext := strings.ToLower(path.Ext(parsed.Path)); ext {
		case ".jpg", ".jpeg", ".png", ".webp":
			return ext
		}
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch mediaType {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/jpeg":
		return ".jpg"
	default:
		return ".jpg"
	}
}

func validatePosterURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("poster URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("poster URL must use http or https")
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return errors.New("poster URL host is required")
	}
	return nil
}

func newPublicPosterHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("poster dial address: %w", err)
			}
			target, err := resolvePublicPosterAddress(ctx, host, port)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, target)
		},
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}
}

func resolvePublicPosterAddress(ctx context.Context, host string, port string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok || !isPublicPosterIP(addr.Unmap()) {
			return "", fmt.Errorf("poster host %q resolves to non-public IP", host)
		}
		return net.JoinHostPort(addr.String(), port), nil
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", fmt.Errorf("poster DNS lookup %q: %w", host, err)
	}
	for _, candidate := range resolved {
		addr, ok := netip.AddrFromSlice(candidate.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if isPublicPosterIP(addr) {
			return net.JoinHostPort(addr.String(), port), nil
		}
	}
	return "", fmt.Errorf("poster host %q has no public IP addresses", host)
}

func isPublicPosterIP(ip netip.Addr) bool {
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return false
	}
	return !ipInPrefixes(ip, reservedPosterPrefixes)
}

func logPosterRehostFailure(logger api.Logger, host string, err error) {
	if logger == nil || err == nil {
		return
	}
	if strings.TrimSpace(host) == "" {
		logger.Warnf("trackers: PTP poster rehost failed: %v", err)
		return
	}
	logger.Warnf("trackers: PTP poster rehost to %s failed: %v", strings.TrimSpace(host), err)
}

func resolvePoster(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		if value := strings.TrimSpace(meta.ProviderMetadata.TMDB.Poster); value != "" {
			return value
		}
	}
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Cover)
	}
	return ""
}

func resolveOverview(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		if value := strings.TrimSpace(meta.ProviderMetadata.TMDB.Overview); value != "" {
			return value
		}
	}
	if meta.ProviderMetadata.IMDB != nil {
		if value := strings.TrimSpace(meta.ProviderMetadata.IMDB.Plot); value != "" {
			return value
		}
	}
	return ""
}

func resolveTrailer(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.YouTube)
	}
	return ""
}

func resolveDirectors(meta api.UploadSubject) []string {
	if meta.ProviderMetadata.TMDB != nil && len(meta.ProviderMetadata.TMDB.Directors) > 0 {
		return append([]string{}, meta.ProviderMetadata.TMDB.Directors...)
	}
	if meta.ProviderMetadata.IMDB != nil && len(meta.ProviderMetadata.IMDB.Directors) > 0 {
		names := make([]string, 0, len(meta.ProviderMetadata.IMDB.Directors))
		for _, person := range meta.ProviderMetadata.IMDB.Directors {
			if strings.TrimSpace(person.Name) != "" {
				names = append(names, strings.TrimSpace(person.Name))
			}
		}
		return names
	}
	return nil
}
