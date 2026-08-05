// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package uploadinput resolves transport-local composite upload inputs.
package uploadinput

import (
	"context"
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

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	descriptionMaxBytes = 1 << 20
	mediaMaxBytes       = 20 << 20
	mediaMaxResources   = 100
	mediaMaxTotal       = 100 << 20
)

// Resolve materializes file and URL description overrides as inline content and
// reads server-local media into private context state for
// [releaseworkflow.Module.StartUpload]. It rejects non-public description URLs
// and unsupported or oversized media. Returned request data contains no media
// bytes.
func Resolve(
	ctx context.Context,
	request api.CreateReleaseWorkflowUploadRequest,
) (context.Context, api.CreateReleaseWorkflowUploadRequest, error) {
	resolved, err := resolveDescriptions(ctx, request)
	if err != nil {
		return ctx, api.CreateReleaseWorkflowUploadRequest{}, err
	}
	comparisons, err := readMediaPaths(
		resolved.Media.Screenshots.ComparisonPaths,
		resolved.Media.Screenshots.ComparisonPrimaryIndex,
	)
	if err != nil {
		return ctx, api.CreateReleaseWorkflowUploadRequest{}, fmt.Errorf("comparison images: %w", err)
	}
	menus, err := readMediaPaths(resolved.Media.DVDMenus.MenuPaths, nil)
	if err != nil {
		return ctx, api.CreateReleaseWorkflowUploadRequest{}, fmt.Errorf("DVD menu images: %w", err)
	}
	if len(comparisons)+len(menus) > mediaMaxResources {
		return ctx, api.CreateReleaseWorkflowUploadRequest{}, errors.New("manual media exceeds the resource count limit")
	}
	total := 0
	for _, content := range append(append([]releaseworkflow.StagedMediaContent(nil), comparisons...), menus...) {
		total += len(content.Bytes)
	}
	if total > mediaMaxTotal {
		return ctx, api.CreateReleaseWorkflowUploadRequest{}, errors.New("manual media exceeds the total size limit")
	}
	if len(comparisons) > 0 || len(menus) > 0 {
		ctx = releaseworkflow.WithCompositeUploadMediaInputs(ctx, releaseworkflow.CompositeUploadMediaInputs{
			Comparisons: comparisons,
			DVDMenus:    menus,
		})
	}
	return ctx, resolved, nil
}

func readMediaPaths(
	values []string,
	primaryIndex *int,
) ([]releaseworkflow.StagedMediaContent, error) {
	if len(values) == 0 {
		return nil, nil
	}
	ordered := append([]string(nil), values...)
	if primaryIndex != nil {
		index := *primaryIndex - 1
		if index < 0 || index >= len(ordered) {
			return nil, errors.New("manual media primary index is out of range")
		}
		ordered = append([]string{ordered[index]}, append(ordered[:index], ordered[index+1:]...)...)
	}
	result := make([]releaseworkflow.StagedMediaContent, 0, len(ordered))
	total := 0
	appendContent := func(content releaseworkflow.StagedMediaContent) error {
		if len(result) >= mediaMaxResources {
			return errors.New("manual media exceeds the resource count limit")
		}
		total += len(content.Bytes)
		if total > mediaMaxTotal {
			return errors.New("manual media exceeds the total size limit")
		}
		result = append(result, content)
		return nil
	}
	for _, value := range ordered {
		path, err := filepath.Abs(filepath.Clean(strings.TrimSpace(value)))
		if err != nil || path == "" {
			return nil, errors.New("manual media path is invalid")
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, errors.New("manual media path could not be inspected")
		}
		if !info.IsDir() {
			content, readErr := readMediaFile(path)
			if readErr != nil {
				return nil, readErr
			}
			if appendErr := appendContent(content); appendErr != nil {
				return nil, appendErr
			}
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, errors.New("manual media directory could not be read")
		}
		added := 0
		for _, entry := range entries {
			if !entry.Type().IsRegular() {
				continue
			}
			content, readErr := readMediaFile(filepath.Join(path, entry.Name()))
			if readErr != nil {
				continue
			}
			if appendErr := appendContent(content); appendErr != nil {
				return nil, appendErr
			}
			added++
		}
		if added == 0 {
			return nil, errors.New("manual media directory contains no supported images")
		}
	}
	return result, nil
}

func readMediaFile(path string) (releaseworkflow.StagedMediaContent, error) {
	file, err := os.Open(path)
	if err != nil {
		return releaseworkflow.StagedMediaContent{}, errors.New("manual media file could not be opened")
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, mediaMaxBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > mediaMaxBytes {
		return releaseworkflow.StagedMediaContent{}, errors.New("manual media file must be between 1 byte and 20 MiB")
	}
	contentType := http.DetectContentType(payload)
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return releaseworkflow.StagedMediaContent{}, errors.New("manual media file must be PNG, JPEG, or WebP")
	}
	return releaseworkflow.StagedMediaContent{
		Name:        filepath.Base(path),
		Bytes:       payload,
		ContentType: contentType,
	}, nil
}

func resolveDescriptions(
	ctx context.Context,
	request api.CreateReleaseWorkflowUploadRequest,
) (api.CreateReleaseWorkflowUploadRequest, error) {
	for index := range request.Descriptions.Overrides {
		override := &request.Descriptions.Overrides[index]
		var (
			content string
			err     error
		)
		switch {
		case override.Inline != nil:
			if len(*override.Inline) > descriptionMaxBytes {
				return api.CreateReleaseWorkflowUploadRequest{}, errors.New("inline description exceeds the size limit")
			}
			continue
		case override.File != nil:
			content, err = readDescriptionFile(*override.File)
		case override.URL != nil:
			content, err = fetchDescriptionURL(ctx, *override.URL)
		default:
			return api.CreateReleaseWorkflowUploadRequest{}, errors.New("description override source is required")
		}
		if err != nil {
			return api.CreateReleaseWorkflowUploadRequest{}, err
		}
		override.Inline = &content
		override.File = nil
		override.URL = nil
	}
	return request, nil
}

func readDescriptionFile(value string) (string, error) {
	filePath := filepath.Clean(strings.TrimSpace(value))
	if filePath == "" || filePath == "." {
		return "", errors.New("description file path is required")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return "", errors.New("description file could not be opened")
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, descriptionMaxBytes+1))
	if err != nil {
		return "", errors.New("description file could not be read")
	}
	if len(payload) > descriptionMaxBytes {
		return "", errors.New("description file exceeds the size limit")
	}
	return string(payload), nil
}

func fetchDescriptionURL(ctx context.Context, value string) (string, error) {
	parsed, err := validateDescriptionURL(ctx, value)
	if err != nil {
		return "", err
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return "", errors.New("description URL transport is unavailable")
	}
	transport := defaultTransport.Clone()
	transport.DialContext = dialPublicDescriptionAddress
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("description URL redirect limit exceeded")
			}
			_, redirectErr := validateDescriptionURL(request.Context(), request.URL.String())
			return redirectErr
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", errors.New("description URL request is invalid")
	}
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("description URL could not be fetched")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", errors.New("description URL returned an unsuccessful status")
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, descriptionMaxBytes+1))
	if err != nil {
		return "", errors.New("description URL response could not be read")
	}
	if len(payload) > descriptionMaxBytes {
		return "", errors.New("description URL response exceeds the size limit")
	}
	return string(payload), nil
}

// validateDescriptionURL rejects credentials, unsupported schemes, and hosts
// whose current DNS answers include a non-public address.
func validateDescriptionURL(ctx context.Context, value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, errors.New("description URL must be a public http or https URL without userinfo")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("description URL host could not be resolved")
	}
	for _, address := range addresses {
		if !descriptionIPAllowed(address.IP) {
			return nil, errors.New("description URL host is not publicly routable")
		}
	}
	return parsed, nil
}

// dialPublicDescriptionAddress re-resolves at connection time and dials only a
// public address, preventing a validated host from rebinding to a private target.
func dialPublicDescriptionAddress(ctx context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("description URL address: %w", err)
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, errors.New("description URL host could not be resolved")
	}
	for _, candidate := range addresses {
		if !descriptionIPAllowed(candidate.IP) {
			continue
		}
		connection, dialErr := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(
			ctx,
			network,
			net.JoinHostPort(candidate.IP.String(), port),
		)
		if dialErr == nil {
			return connection, nil
		}
	}
	return nil, errors.New("description URL has no reachable public address")
}

func descriptionIPAllowed(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
}
