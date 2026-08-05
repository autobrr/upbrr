// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

func releaseWorkflowUploadOptionSchemaHash() (string, error) {
	var document struct {
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(releaseWorkflowOpenAPI, &document); err != nil {
		return "", fmt.Errorf("decode upload option schema: %w", err)
	}
	const root = "CreateReleaseWorkflowUploadRequest"
	if document.Components.Schemas[root] == nil {
		return "", fmt.Errorf("upload option schema %s is unavailable", root)
	}
	selected := make(map[string]any)
	pending := []string{root}
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if _, exists := selected[name]; exists {
			continue
		}
		value, exists := document.Components.Schemas[name]
		if !exists {
			return "", fmt.Errorf("upload option schema reference %s is unavailable", name)
		}
		selected[name] = value
		for _, ref := range releaseWorkflowSchemaReferences(value) {
			if _, exists := selected[ref]; !exists {
				pending = append(pending, ref)
			}
		}
	}
	payload, err := json.Marshal(selected)
	if err != nil {
		return "", fmt.Errorf("encode upload option schema: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func releaseWorkflowSchemaReferences(value any) []string {
	references := make([]string, 0)
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if key == "$ref" {
					if ref, ok := child.(string); ok {
						const prefix = "#/components/schemas/"
						if name := strings.TrimPrefix(ref, prefix); name != ref && name != "" {
							references = append(references, name)
						}
					}
					continue
				}
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	return references
}

func serveReleaseWorkflowSwaggerUI(w http.ResponseWriter) {
	page := bytes.ReplaceAll(releaseWorkflowSwaggerUITemplate, swaggerUIVersionPlaceholder, []byte(swaggerUIVersion))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page)
}
