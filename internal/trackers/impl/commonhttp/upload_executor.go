// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package commonhttp

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers/submission"
)

const defaultSuccessBodyBytes int64 = 1024 * 1024

// PositiveTorrentID reads the numeric/string ID forms returned by JSON APIs.
// Missing, fractional, non-positive and non-scalar values have no identity.
func PositiveTorrentID(value any) string {
	var text string
	switch typed := value.(type) {
	case string:
		text = strings.TrimSpace(typed)
	case float64:
		text = strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
	if id, err := strconv.ParseUint(text, 10, 64); err != nil || id == 0 {
		return ""
	}
	return text
}

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

// UploadExecution contains one closed HTTP response projected for tracker-local
// parsing. Failure Body and Preview are bounded and redacted; successful Body
// retains parser input while Preview remains bounded and redacted.
type UploadExecution struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Preview    []byte
	FinalURL   string
	Success    bool // HTTP status candidate only; the adapter must establish the protocol outcome.
	// ProtocolBody is private parser input, never a diagnostic or public projection.
	// Failure bodies retain the bounded raw response; Body remains redacted.
	ProtocolBody []byte
	BodyComplete bool
}

// ExecuteUpload performs one prepared request and closes its response. It
// rejects oversized success bodies unless full reads are explicitly selected,
// clones response headers, and redacts the final URL and diagnostic previews.
// A read/close error still returns available response evidence for the adapter;
// headers can prove acceptance even when BodyComplete is false.
func ExecuteUpload(client *http.Client, req *http.Request, options UploadExecutionOptions) (UploadExecution, error) {
	if client == nil {
		return UploadExecution{}, submission.NewError(submission.NotSent, errors.New("tracker upload client is nil"))
	}
	if req == nil {
		return UploadExecution{}, submission.NewError(submission.NotSent, errors.New("tracker upload request is nil"))
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
	body, preview, complete, readErr := readExecutedUploadBody(resp.Body, success, options.SuccessBody, options.SuccessBodyLimit, previewLimit)
	closeErr := resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = redaction.RedactValue(resp.Request.URL.String(), nil)
	}
	protocolBody := body
	if success {
		preview = ResponseBodyPreview(body, previewLimit)
		preview = []byte(redaction.RedactValue(string(preview), nil))
	} else {
		body = append([]byte(nil), preview...)
	}
	result := UploadExecution{
		StatusCode:   resp.StatusCode,
		Header:       resp.Header.Clone(),
		Body:         body,
		Preview:      preview,
		FinalURL:     finalURL,
		Success:      success,
		ProtocolBody: protocolBody,
		BodyComplete: complete,
	}
	if readErr != nil || closeErr != nil {
		return result, fmt.Errorf("trackers: %s read upload response: %w", tracker, safeWrappedError(errors.Join(readErr, closeErr)))
	}
	return result, nil
}

func readExecutedUploadBody(
	body io.Reader,
	success bool,
	policy SuccessBodyPolicy,
	successLimit int64,
	previewLimit int64,
) ([]byte, []byte, bool, error) {
	if !success {
		payload, preview, complete, err := readHTTPErrorResponse(body, previewLimit)
		if err == nil && !complete {
			err = fmt.Errorf("upload response exceeds %d bytes", maxHTTPErrorResponseBytes)
		}
		return payload, preview, complete, err
	}
	if policy == FullSuccessBody {
		payload, err := io.ReadAll(body)
		if err != nil {
			return payload, nil, false, fmt.Errorf("read full upload success response: %w", err)
		}
		return payload, nil, true, nil
	}
	if successLimit <= 0 {
		successLimit = defaultSuccessBodyBytes
	}
	payload, err := io.ReadAll(io.LimitReader(body, successLimit+1))
	if err != nil {
		return payload, nil, false, fmt.Errorf("read bounded upload success response: %w", err)
	}
	if int64(len(payload)) > successLimit {
		return nil, nil, false, fmt.Errorf("successful upload response exceeds %d bytes", successLimit)
	}
	return payload, nil, true, nil
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
