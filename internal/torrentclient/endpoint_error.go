// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/redaction"
)

// clientEndpointError keeps the cause available to errors.Is/As while its
// diagnostic text omits private client connection details.
type clientEndpointError struct {
	cause   error
	message string
}

func (e *clientEndpointError) Error() string { return e.message }
func (e *clientEndpointError) Unwrap() error { return e.cause }

func safeClientError(err error, endpoint string) error {
	if err == nil {
		return nil
	}
	var requestError *url.Error
	var networkError net.Error
	isRequest := errors.As(err, &requestError)
	isNetwork := errors.As(err, &networkError)
	message := err.Error()
	// retry-go v3, used by qBittorrent, predates standard multi-error unwrap.
	if retries, ok := errors.AsType[interface {
		error
		WrappedErrors() []error
	}](err); ok {
		for _, attempt := range retries.WrappedErrors() {
			if attempt != nil {
				message = strings.ReplaceAll(message, attempt.Error(), safeClientError(attempt, endpoint).Error())
			}
		}
	}
	endpoints := []string{endpoint}
	if isRequest || isNetwork {
		classification := "client request failed"
		switch {
		case errors.Is(err, context.Canceled):
			classification += ": canceled"
		case errors.Is(err, context.DeadlineExceeded):
			classification += ": deadline exceeded"
		case networkError != nil && networkError.Timeout():
			classification += ": timeout"
		default:
			classification += ": connection or transport error"
		}
		if isRequest {
			message = strings.ReplaceAll(err.Error(), requestError.Error(), classification)
			endpoints = append(endpoints, requestError.URL)
		} else {
			message = strings.ReplaceAll(err.Error(), networkError.Error(), classification)
		}
	}
	for _, endpoint := range endpoints {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" {
			continue
		}
		message = strings.ReplaceAll(message, endpoint, "[client endpoint]")
		if parsed, parseErr := url.Parse(endpoint); parseErr == nil && parsed.Host != "" {
			// qBittorrent joins API paths and replaces configured query values.
			parsed.RawQuery, parsed.Fragment = "", ""
			parsed.ForceQuery = false
			message = strings.ReplaceAll(message, parsed.String(), "[client endpoint]")
			if normalized, joinErr := url.JoinPath(parsed.String()); joinErr == nil {
				message = strings.ReplaceAll(message, normalized, "[client endpoint]")
			}
			message = strings.ReplaceAll(message, parsed.Host, "[client endpoint]")
			if hostname := parsed.Hostname(); hostname != "" {
				message = strings.ReplaceAll(message, hostname, "[client endpoint]")
			}
		}
	}
	message = redaction.RedactValue(message, nil)
	if message == err.Error() {
		return err
	}
	return &clientEndpointError{cause: err, message: message}
}
