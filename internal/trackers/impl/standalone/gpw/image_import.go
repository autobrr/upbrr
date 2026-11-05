// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"strings"

	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func importImageURL(ctx context.Context, client *http.Client, credential, source string) (string, error) {
	if strings.TrimSpace(credential) == "" {
		return "", errors.New("GPW image import requires an API key")
	}
	endpoint := baseURL + "/api.php?" + url.Values{"action": {"img_upload"}, "api_key": {credential}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(url.Values{"urls[]": {source}}.Encode()))
	if err != nil {
		return "", errors.New("GPW image import request is invalid")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "upbrr")
	privateClient := *client
	privateClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	result, err := commonhttp.ExecuteUpload(&privateClient, request, commonhttp.UploadExecutionOptions{Tracker: "GPW"})
	if err != nil {
		return "", unknownImageImport(err)
	}
	if result.StatusCode >= 300 && result.StatusCode < 400 {
		return "", errors.New("GPW image import refused an API redirect")
	}
	var response struct {
		Status   int `json:"status"`
		Response struct {
			Error string `json:"Error"`
			Files []struct {
				Name string `json:"name"`
			} `json:"files"`
		} `json:"response"`
	}
	if err := json.Unmarshal(result.ProtocolBody, &response); err != nil {
		return "", unknownImageImport(err)
	}
	if !result.Success || response.Status != http.StatusOK || response.Response.Error != "" {
		return "", errors.New("GPW image import was rejected")
	}
	if len(response.Response.Files) != 1 {
		return "", errors.New("GPW image import returned an invalid file count")
	}
	hosted := strings.TrimSpace(response.Response.Files[0].Name)
	parsed, err := url.Parse(hosted)
	if err != nil || parsed.User != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || imagehost.ExtractHost(hosted) != "kshare" {
		return "", errors.New("GPW image import returned an invalid destination")
	}
	return hosted, nil
}

func unknownImageImport(cause error) error {
	return api.NewOperationError(api.OperationFailure{
		Code:      api.OperationFailureUnknownOutcome,
		Operation: api.OperationKindImageHosting,
		Message:   "GPW image import may have succeeded. Verify hosted assets before retrying.",
		Recovery:  api.OperationRecoveryConfirm,
	},
		&diagnosticError{message: "GPW image import returned no terminal receipt", cause: cause})
}
