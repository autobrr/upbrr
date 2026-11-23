// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
)

var diagnosticURL = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://|//)[^\s<>"']+|(?:/[a-z0-9_.?%=&+-]+)+`)

type diagnosticError struct {
	message string
	cause   error
}

func (e *diagnosticError) Error() string { return e.message }
func (e *diagnosticError) Unwrap() error { return e.cause }

func uploadDiagnostic(result commonhttp.UploadExecution, secrets ...string) string {
	contentType, _, _ := mime.ParseMediaType(result.Header.Get("Content-Type"))
	switch contentType {
	case "application/json", "text/html", "text/plain", "application/octet-stream":
	default:
		contentType = "unknown"
	}
	destination := "[redacted destination]"
	if u, err := url.Parse(result.FinalURL); err == nil && u.Scheme == "https" && u.Host == "greatposterwall.com" && u.Path == "/api.php" {
		destination = baseURL + "/api.php"
	}
	preview := "[incomplete response body omitted]"
	if result.BodyComplete {
		// Decode and redact the complete bounded input before truncation. The
		// shared extractor omits hidden/form/script content from HTML.
		diagnosticSecrets := slices.Clone(secrets)
		if u, err := url.Parse(result.FinalURL); err == nil && u.Hostname() != "greatposterwall.com" && u.Hostname() != "" {
			diagnosticSecrets = append(diagnosticSecrets, u.Hostname())
		}
		preview = commonhttp.ExtractHTTPErrorDetail(result.ProtocolBody, diagnosticSecrets...)
		preview = strings.ReplaceAll(preview, `\/`, `/`)
		preview = diagnosticURL.ReplaceAllString(preview, "[redacted URL]")
		preview = string(commonhttp.ResponseBodyPreview([]byte(preview), commonhttp.DefaultResponsePreviewBytes))
		preview = strings.TrimSpace(preview)
	}
	return fmt.Sprintf("HTTP %d; content-type=%s; destination=%s; response=%s", result.StatusCode, contentType, destination, preview)
}
