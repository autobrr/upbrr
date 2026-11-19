// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	ascUploadPath     = "/torrents"
	ascUploadFormPath = "/torrents/upload"
	ascScreenshotPath = "/torrents/screenshots"
)

var ascRegisteredPath = regexp.MustCompile(`^/torrents/([1-9][0-9]*)/?$`)

func submitASC(
	ctx context.Context,
	client *http.Client,
	cookies map[string]string,
	fields map[string][]string,
	files, screenshots []commonhttp.FileField,
	artifactPath string,
) (api.UploadSummary, error) {
	session, err := startASCSubmitSession(ctx, client, cookies)
	if err != nil {
		return api.UploadSummary{}, submission.NewError(submission.NotSent, err)
	}
	paths := make([]string, 0, len(screenshots))
	for i, screenshot := range screenshots {
		path, err := postASCScreenshot(ctx, session, screenshot)
		if err != nil {
			return api.UploadSummary{}, submission.NewError(submission.NotSent, fmt.Errorf("trackers: ASC screenshot %d: %w", i+1, err))
		}
		paths = append(paths, path)
	}
	finalFields := make(map[string][]string, len(fields)+1)
	maps.Copy(finalFields, fields)
	finalFields["screenshot_paths[]"] = paths
	result, err := postASCForm(ctx, session, ascUploadPath, finalFields, files)
	location := ascSubmitRedirect(result)
	if match := ascRegisteredPath.FindStringSubmatch(location); match != nil {
		return finishASCSubmission(ctx, session, match[1], artifactPath), nil
	}
	if strings.TrimSuffix(location, "/") == "/login" || ascValidationRejected(result) {
		return api.UploadSummary{}, submission.NewError(submission.Rejected, fmt.Errorf("trackers: ASC rejected the upload (status %d)", result.StatusCode))
	}
	if err != nil {
		return api.UploadSummary{}, err
	}
	return api.UploadSummary{}, fmt.Errorf(
		"trackers: ASC upload response did not identify the torrent (status %d); check ASC before retrying",
		result.StatusCode,
	)
}

func startASCSubmitSession(ctx context.Context, client *http.Client, cookies map[string]string) (*http.Client, error) {
	if client == nil {
		return nil, errors.New("trackers: ASC submit client is required")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: ASC start upload session: %w", err)
	}
	origin := &url.URL{Scheme: "https", Host: siteHost}
	// Require a fresh token from the upload form instead of trusting a stored one.
	initialCookies := maps.Clone(cookies)
	delete(initialCookies, "XSRF-TOKEN")
	jar.SetCookies(origin, cookiepkg.CookieMapToHTTPCookies(initialCookies, siteHost))
	session := httpclient.CloneWithTimeout(client, httpclient.UploadTimeout)
	session.Jar = jar
	session.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, siteURL+ascUploadFormPath, nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: ASC build upload form request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html")
	result, err := commonhttp.ExecuteUpload(session, req, commonhttp.UploadExecutionOptions{Tracker: "ASC"})
	if err != nil {
		return nil, fmt.Errorf("trackers: ASC open upload form: %w", err)
	}
	if result.StatusCode != http.StatusOK || ascXSRFToken(session, req.URL) == "" {
		return nil, fmt.Errorf("trackers: ASC upload form did not establish a session (status %d)", result.StatusCode)
	}
	return session, nil
}

func ascXSRFToken(client *http.Client, target *url.URL) string {
	for _, cookie := range client.Jar.Cookies(target) {
		if cookie.Name == "XSRF-TOKEN" {
			value, err := url.QueryUnescape(cookie.Value)
			if err == nil {
				return strings.TrimSpace(value)
			}
			break
		}
	}
	return ""
}

func postASCScreenshot(ctx context.Context, client *http.Client, screenshot commonhttp.FileField) (string, error) {
	result, err := postASCForm(ctx, client, ascScreenshotPath, nil, []commonhttp.FileField{screenshot})
	if err != nil {
		return "", err
	}
	if !result.Success {
		return "", fmt.Errorf("server returned status %d", result.StatusCode)
	}
	var stored struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(result.Body, &stored) != nil || strings.TrimSpace(stored.Path) == "" {
		return "", errors.New("response did not identify the stored image")
	}
	return strings.TrimSpace(stored.Path), nil
}

func postASCForm(
	ctx context.Context,
	client *http.Client,
	path string,
	fields map[string][]string,
	files []commonhttp.FileField,
) (commonhttp.UploadExecution, error) {
	body, contentType, err := commonhttp.BuildMultipartPayloadMulti(fields, files)
	if err != nil {
		return commonhttp.UploadExecution{}, submission.NewError(submission.NotSent, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, siteURL+path, bytes.NewReader(body))
	if err != nil {
		return commonhttp.UploadExecution{}, submission.NewError(submission.NotSent, err)
	}
	token := ascXSRFToken(client, req.URL)
	if token == "" {
		return commonhttp.UploadExecution{}, submission.NewError(submission.NotSent, errors.New("trackers: ASC upload session has no XSRF token"))
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Xsrf-Token", token)
	req.Header.Set("Origin", siteURL)
	req.Header.Set("Referer", siteURL+ascUploadFormPath)
	result, err := commonhttp.ExecuteUpload(client, req, commonhttp.UploadExecutionOptions{Tracker: "ASC"})
	if err != nil {
		return result, fmt.Errorf("trackers: ASC submit form: %w", err)
	}
	return result, nil
}

func ascSubmitRedirect(result commonhttp.UploadExecution) string {
	if result.StatusCode != http.StatusFound && result.StatusCode != http.StatusSeeOther {
		return ""
	}
	reference, err := url.Parse(strings.TrimSpace(result.Header.Get("Location")))
	if err != nil || reference == nil || reference.User != nil || reference.Fragment != "" {
		return ""
	}
	base := &url.URL{
		Scheme: "https",
		Host:   siteHost,
		Path:   ascUploadPath,
	}
	target := base.ResolveReference(reference)
	if target.Scheme != base.Scheme || !strings.EqualFold(target.Host, base.Host) || target.User != nil || target.Opaque != "" {
		return ""
	}
	return target.EscapedPath()
}

func ascValidationRejected(result commonhttp.UploadExecution) bool {
	if result.StatusCode != http.StatusUnprocessableEntity || !result.BodyComplete {
		return false
	}
	var validation struct {
		Errors map[string][]string `json:"errors"`
	}
	return json.Unmarshal(result.ProtocolBody, &validation) == nil && len(validation.Errors) > 0
}

func finishASCSubmission(ctx context.Context, client *http.Client, id, artifactPath string) api.UploadSummary {
	endpoint := siteURL + ascUploadPath + "/" + id
	registeredPath := ""
	if artifactPath != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/download", nil)
		if err == nil {
			req.Header.Set("User-Agent", userAgent)
			if trackers.DownloadRegisteredTorrent(ctx, client, req, artifactPath) == nil {
				registeredPath = artifactPath
			}
		}
	}
	// The site rewrites the info dictionary. An accepted upload without its
	// exact registered torrent must skip client injection.
	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "ASC",
			TorrentID:   id,
			TorrentURL:  endpoint,
			TorrentPath: registeredPath,
		}},
	}
}
