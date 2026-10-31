// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png" // register PNG decoder for tracker image validation
	"io"
	"net/http"
	"os"
	"strings"

	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
)

const unit3dMaxImageBytes = 20 * 1024 * 1024

// Version geometry verdicts independently from source-bound preparation facts.
const trackerImageResolutionPolicyVersion = 2

var validateTrackerArtifactImageURL = trackerdata.ValidateUnit3DImageURL

// attemptTrackerImageDownload captures reference evidence only when this attempt
// executes the downloader. Replayed generic failures cannot mint a receipt.
func attemptTrackerImageDownload(
	ctx context.Context,
	client *http.Client,
	tracker, rawURL, outPath string,
	expectedHeight int,
	isDVD, allowReference bool,
) (string, error) {
	var reference string
	query := []any{tracker, rawURL, expectedHeight, isDVD}
	if expectedHeight > 0 && !isDVD {
		query = append(query, trackerImageResolutionPolicyVersion)
	}
	err := evidence.Attempt(ctx, "tracker.image", query, func() error {
		reason, accepted := downloadImageWithReference(ctx, client, rawURL, outPath, expectedHeight, isDVD, allowReference)
		reference = accepted
		if reason != "" {
			return errors.New(reason)
		}
		return nil
	})
	if err != nil {
		return reference, fmt.Errorf("tracker image attempt: %w", err)
	}
	return reference, nil
}

// downloadImage fetches the validated direct image source into a private
// artifact. It returns a URL-free failure reason for operator diagnostics.
func downloadImage(ctx context.Context, client *http.Client, rawURL, outPath string, expectedHeight int, isDVD bool) string {
	reason, _ := downloadImageWithReference(ctx, client, rawURL, outPath, expectedHeight, isDVD, false)
	return reason
}

// downloadImageWithReference also returns a current response-policy receipt
// when local inspection or persistence is unavailable. allowReference is
// captured from the security-wrapped client before concurrent requests begin.
func downloadImageWithReference(
	ctx context.Context,
	client *http.Client,
	rawURL, outPath string,
	expectedHeight int,
	isDVD, allowReference bool,
) (string, string) {
	requestURL := imagehost.DirectImageURL(rawURL)
	if requestURL == "" {
		return "invalid_proxy_source", ""
	}
	requestURL = trackers.PTPDescriptionImageDownloadURL(requestURL)
	if err := validateTrackerArtifactImageURL(ctx, requestURL); err != nil {
		return "invalid_or_nonpublic_url", ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "invalid_request", ""
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "request_timeout", ""
		}
		return "request_failed", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("http_status_%d", resp.StatusCode), ""
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "image") {
		return "non_image_content_type", ""
	}
	if resp.ContentLength > 0 && resp.ContentLength > unit3dMaxImageBytes {
		return "image_too_large", ""
	}
	limited := &io.LimitedReader{R: resp.Body, N: unit3dMaxImageBytes}
	acceptedReference := func() string {
		if allowReference && limited.N > 0 && ctx.Err() == nil &&
			resp.Request != nil && resp.Request.Context().Err() == nil {
			return requestURL
		}
		return ""
	}
	payload, err := io.ReadAll(limited)
	if err != nil {
		return "image_read_failed", acceptedReference()
	}
	if len(payload) == 0 {
		return "empty_image", acceptedReference()
	}
	if resp.ContentLength > 0 && int64(len(payload)) < resp.ContentLength {
		return "incomplete_image", acceptedReference()
	}
	imgConfig, _, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil {
		return "image_decode_failed", acceptedReference()
	}
	if expectedHeight > 0 {
		actualHeight := imgConfig.Height
		if !isDVD && actualHeight > expectedHeight {
			actualHeight = min(imgConfig.Width, imgConfig.Height)
		}
		if err := validateImageResolution(actualHeight, expectedHeight, isDVD); err != nil {
			return fmt.Sprintf("resolution_mismatch_actual_%d_expected_%d", actualHeight, expectedHeight), ""
		}
	}
	if err := os.WriteFile(outPath, payload, 0o600); err != nil {
		return "image_write_failed", acceptedReference()
	}
	return "", ""
}

func parseResolutionHeight(resolution string) int {
	resMap := map[string]int{
		"8640p": 8640,
		"4320p": 4320,
		"2160p": 2160,
		"1440p": 1440,
		"1080p": 1080,
		"1080i": 1080,
		"720p":  720,
		"576p":  576,
		"576i":  576,
		"480p":  480,
		"480i":  480,
	}
	return resMap[strings.TrimSpace(resolution)]
}

func validateImageResolution(actualHeight int, expectedHeight int, isDVD bool) error {
	if expectedHeight <= 0 {
		return nil
	}
	lowerBound := int(float64(expectedHeight) * 0.70)
	upperBound := expectedHeight
	if isDVD {
		upperBound = int(float64(expectedHeight) * 1.30)
	}
	if actualHeight < lowerBound || actualHeight > upperBound {
		return fmt.Errorf("resolution %dp outside allowed range (%d-%d)", actualHeight, lowerBound, upperBound)
	}
	return nil
}
