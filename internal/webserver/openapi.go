// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	_ "embed"
	"errors"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed openapi.yaml
var openapiYAML []byte

type openAPIDocument map[string]any

var (
	embeddedOpenAPIOnce sync.Once
	embeddedOpenAPIDoc  openAPIDocument
	errEmbeddedOpenAPI  error
)

func openAPIYAMLSpec() ([]byte, error) {
	if len(openapiYAML) == 0 {
		return nil, errors.New("openapi spec is empty")
	}
	return openapiYAML, nil
}

func openAPIDocumentSpec() (openAPIDocument, error) {
	embeddedOpenAPIOnce.Do(func() {
		if len(openapiYAML) == 0 {
			errEmbeddedOpenAPI = errors.New("openapi spec is empty")
			return
		}
		errEmbeddedOpenAPI = yaml.Unmarshal(openapiYAML, &embeddedOpenAPIDoc)
	})
	return embeddedOpenAPIDoc, errEmbeddedOpenAPI
}
