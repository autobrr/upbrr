// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// ConnectionTestTarget selects one integration in a settings draft. Instance is
// a zero-based Arr slot, a torrent-client name, an image-host ID, or a tracker ID.
type ConnectionTestTarget struct {
	Kind     string `json:"kind"`
	Instance string `json:"instance,omitempty"`
}

// ConnectionTestRequest carries the same encrypted secret envelopes as settings
// saves. Payload is decoded only on the server and must never be logged.
type ConnectionTestRequest struct {
	Target  ConnectionTestTarget `json:"target"`
	Payload string               `json:"payload"`
}

// ConnectionTestResult contains only a fixed outcome and operator guidance.
// It never includes endpoints, credentials, or remote response/error text.
type ConnectionTestResult struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
