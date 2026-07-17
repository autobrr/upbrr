// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package commonhttp

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/redaction"
)

const defaultSuccessBodyBytes int64 = 1024 * 1024

// SuccessBodyPolicy selects bounded or protocol-required full reads for a
// successful tracker response.
type SuccessBodyPolicy uint8

const (
	// BoundedSuccessBody rejects successful response bodies above the configured limit.
	BoundedSuccessBody SuccessBodyPolicy = iota
	// FullSuccessBody permits a full successful response read for protocols that return artifacts inline.
	FullSuccessBody
)

// UploadExecutionOptions controls response classification and body bounds.
type UploadExecutionOptions struct {
	Tracker          string
	SuccessStatus    func(int) bool
	SuccessBody      SuccessBodyPolicy
	SuccessBodyLimit int64
	PreviewLimit     int64
}

// UploadExecution contains one closed HTTP response projected for tracker-local parsing.
type UploadExecution struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Preview    []byte
	FinalURL   string
	Success    bool
}

// ExecuteUpload performs one prepared request, closes its response, bounds all
// diagnostic data, and returns tracker-local parsing facts.
func ExecuteUpload(client *http.Client, req *http.Request, options UploadExecutionOptions) (UploadExecution, error) {
	if client == nil {
		return UploadExecution{}, errors.New("tracker upload client is nil")
	}
	if req == nil {
		return UploadExecution{}, errors.New("tracker upload request is nil")
	}
	tracker := strings.ToUpper(strings.TrimSpace(options.Tracker))
	if tracker == "" {
		tracker = "TRACKER"
	}
	successStatus := options.SuccessStatus
	if successStatus == nil {
		successStatus = func(status int) bool { return status >= http.StatusOK && status < http.StatusMultipleChoices }
	}
	previewLimit := options.PreviewLimit
	if previewLimit <= 0 {
		previewLimit = DefaultResponsePreviewBytes
	}

	// The caller constructs requests from tracker-profile endpoints; this helper
	// deliberately accepts an injected client/request pair for redirects and tests.
	resp, err := client.Do(req) //nolint:gosec // Tracker-owned endpoint request, not user-selected SSRF input.
	if err != nil {
		return UploadExecution{}, fmt.Errorf("trackers: %s upload request: %w", tracker, safeWrappedError(err))
	}
	success := successStatus(resp.StatusCode)
	body, readErr := readExecutedUploadBody(resp.Body, success, options.SuccessBody, options.SuccessBodyLimit, previewLimit)
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil {
		return UploadExecution{}, fmt.Errorf("trackers: %s read upload response: %w", tracker, errors.Join(readErr, closeErr))
	}

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = redaction.RedactValue(resp.Request.URL.String(), nil)
	}
	preview := ResponseBodyPreview(body, previewLimit)
	preview = []byte(redaction.RedactValue(string(preview), nil))
	if !success {
		body = append([]byte(nil), preview...)
	}
	return UploadExecution{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       body,
		Preview:    preview,
		FinalURL:   finalURL,
		Success:    success,
	}, nil
}

func readExecutedUploadBody(
	body io.Reader,
	success bool,
	policy SuccessBodyPolicy,
	successLimit int64,
	previewLimit int64,
) ([]byte, error) {
	if !success {
		payload, err := io.ReadAll(io.LimitReader(body, previewLimit))
		if err != nil {
			return nil, fmt.Errorf("read bounded upload failure response: %w", err)
		}
		return payload, nil
	}
	if policy == FullSuccessBody {
		payload, err := io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("read full upload success response: %w", err)
		}
		return payload, nil
	}
	if successLimit <= 0 {
		successLimit = defaultSuccessBodyBytes
	}
	payload, err := io.ReadAll(io.LimitReader(body, successLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded upload success response: %w", err)
	}
	if int64(len(payload)) > successLimit {
		return nil, fmt.Errorf("successful upload response exceeds %d bytes", successLimit)
	}
	return payload, nil
}

type wrappedSafeError struct {
	message string
	cause   error
}

func (e wrappedSafeError) Error() string { return e.message }

func (e wrappedSafeError) Unwrap() error { return e.cause }

func safeWrappedError(err error) error {
	if err == nil {
		return nil
	}
	return wrappedSafeError{message: redaction.RedactValue(err.Error(), nil), cause: err}
}
