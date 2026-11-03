// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"context"
	"errors"
	"net/http"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// testConnection reuses the session validator's response contract without
// opening storage, logging in, or persisting refreshed cookies.
func testConnection(ctx context.Context, input trackers.ConnectionProbeInput, client *http.Client) api.ConnectionTestResult {
	if len(input.Cookies) == 0 {
		return api.ConnectionTestResult{Code: "missing_configuration", Message: "Import this tracker's browser cookies before testing its session."}
	}
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, resolveBaseURL()+"/upload.php", nil, connectiontest.CookieHeaders(input.Cookies))
	if failure.Code != "" {
		return failure
	}
	// Request accepts only 200 responses and rejects every redirect. The
	// existing validator can therefore apply its body/login/challenge checks.
	token, err := validateHDTAuthResponse(&http.Response{StatusCode: http.StatusOK}, body)
	if err != nil {
		if failure, ok := errors.AsType[*trackers.AuthResolutionError](err); ok && failure.ConfirmedInvalid {
			return api.ConnectionTestResult{
				Code:    "credentials_rejected",
				Message: "The tracker did not accept the stored session. Import current browser cookies and test again.",
			}
		}
		return connectiontest.UnexpectedResponse()
	}
	if token == "" {
		return connectiontest.UnexpectedResponse()
	}
	return api.ConnectionTestResult{
		Code:    "success",
		Message: "The tracker accepted the stored session for its protected upload form. No upload was attempted; announce credentials were not tested.",
	}
}
