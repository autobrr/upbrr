// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	_ "embed"
	"net/http"
)

//go:embed openapi/release-workflow-v1.json
var releaseWorkflowOpenAPI []byte

func serveReleaseWorkflowOpenAPI(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/vnd.oai.openapi+json;version=3.1")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(releaseWorkflowOpenAPI)
}
