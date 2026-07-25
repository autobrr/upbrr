// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
)

//go:embed openapi/release-workflow-v1.json
var releaseWorkflowOpenAPI []byte

//go:embed openapi/swagger-ui.html
var releaseWorkflowSwaggerUITemplate []byte

const swaggerUIVersion = "5.11.0"

var swaggerUIVersionPlaceholder = []byte("{{SWAGGER_UI_VERSION}}")

func (s *Server) serveReleaseWorkflowOpenAPI(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/vnd.oai.openapi+json;version=3.1")
	document, err := releaseWorkflowOpenAPIDocument(s.externalBasePath())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "OpenAPI document unavailable"})
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(document)
}

func releaseWorkflowOpenAPIDocument(basePath string) ([]byte, error) {
	if externalBasePath(basePath) == "" {
		return bytes.Clone(releaseWorkflowOpenAPI), nil
	}
	var document map[string]any
	if err := json.Unmarshal(releaseWorkflowOpenAPI, &document); err != nil {
		return nil, fmt.Errorf("decode embedded OpenAPI document: %w", err)
	}
	document["servers"] = []any{
		map[string]any{"url": joinBasePath(basePath, "/api/v1")},
	}
	adjusted, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode base-path OpenAPI document: %w", err)
	}
	return append(adjusted, '\n'), nil
}

func serveReleaseWorkflowSwaggerUI(w http.ResponseWriter) {
	page := bytes.ReplaceAll(releaseWorkflowSwaggerUITemplate, swaggerUIVersionPlaceholder, []byte(swaggerUIVersion))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page)
}
