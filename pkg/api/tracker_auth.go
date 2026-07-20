// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// TrackerAuthCapability describes the auth operations the UI can offer for one tracker.
type TrackerAuthCapability struct {
	// TrackerID is the normalized tracker code used in WebUI requests.
	TrackerID   string `json:"trackerID"`
	DisplayName string `json:"displayName"`
	// AuthKind is a compact capability label such as "cookies", "credential_login", or "api_key_cookies_login".
	AuthKind           string `json:"authKind"`
	SupportsCookieFile bool   `json:"supportsCookieFile"`
	SupportsLogin      bool   `json:"supportsLogin"`
	SupportsAutoLogin  bool   `json:"supportsAutoLogin"`
	SupportsTOTP       bool   `json:"supportsTOTP"`
	SupportsManual2FA  bool   `json:"supportsManual2FA"`
	// SupportsRemoteValidation reports whether Check Auth can execute a tracker-owned remote resolver.
	SupportsRemoteValidation bool     `json:"supportsRemoteValidation"`
	RequiresAPIKey           bool     `json:"requiresAPIKey"`
	RequiresPasskey          bool     `json:"requiresPasskey"`
	Notes                    []string `json:"notes"`
}

// TrackerAuthStatus reports the current local tracker auth state after a
// status, import, login, validation, 2FA, or delete action.
//
// WebUI routes and TypeScript consumers must expose the same JSON fields
// so status summaries, remediation messages, and Promise-visible errors stay in
// parity across browser entrypoints.
type TrackerAuthStatus struct {
	TrackerID   string `json:"trackerID"`
	DisplayName string `json:"displayName"`
	// State is one of the tracker auth state strings returned by the backend, such as "configured", "has_cookies", or "login_required".
	State       string `json:"state"`
	CookieCount int    `json:"cookieCount"`
	// LastCheckedAt is an RFC3339 UTC timestamp generated when the status is evaluated.
	LastCheckedAt string `json:"lastCheckedAt"`
	// LastError contains redacted failure detail when a local or remote auth check failed; consumers should display it with Message when distinct.
	LastError        string `json:"lastError"`
	EncryptedStorage bool   `json:"encryptedStorage"`
	Needs2FA         bool   `json:"needs2FA"`
	// ChallengeID is an opaque manual-2FA continuation token; it is empty unless Needs2FA is true.
	ChallengeID string `json:"challengeID"`
	// Message contains the stable user-facing status summary or next step.
	Message string `json:"message"`
}

// TrackerAuthLoginRequest carries optional login data for tracker auth flows.
type TrackerAuthLoginRequest struct {
	// Code is a one-time 2FA code for adapters that accept it during login.
	Code string `json:"code"`
}
