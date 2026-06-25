// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

type TrackerAuthCapability struct {
	TrackerID          string   `json:"trackerID"`
	DisplayName        string   `json:"displayName"`
	AuthKind           string   `json:"authKind"`
	SupportsCookieFile bool     `json:"supportsCookieFile"`
	SupportsLogin      bool     `json:"supportsLogin"`
	SupportsAutoLogin  bool     `json:"supportsAutoLogin"`
	SupportsTOTP       bool     `json:"supportsTOTP"`
	SupportsManual2FA  bool     `json:"supportsManual2FA"`
	RequiresAPIKey     bool     `json:"requiresAPIKey"`
	RequiresPasskey    bool     `json:"requiresPasskey"`
	Notes              []string `json:"notes"`
}

type TrackerAuthStatus struct {
	TrackerID        string `json:"trackerID"`
	DisplayName      string `json:"displayName"`
	State            string `json:"state"`
	CookieCount      int    `json:"cookieCount"`
	LastCheckedAt    string `json:"lastCheckedAt"`
	LastError        string `json:"lastError"`
	EncryptedStorage bool   `json:"encryptedStorage"`
	Needs2FA         bool   `json:"needs2FA"`
	ChallengeID      string `json:"challengeID"`
	Message          string `json:"message"`
}

type TrackerAuthLoginRequest struct {
	Code string `json:"code"`
}
