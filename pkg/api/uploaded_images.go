// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "time"

type UploadedImageLink struct {
	SourcePath string
	ImagePath  string
	Host       string
	UsageScope string
	ImgURL     string
	RawURL     string
	WebURL     string
	SizeBytes  int64
	UploadedAt time.Time `ts_type:"string"`
}

// UploadImageHostFailure describes a single host-level image upload failure
// returned in UploadImagesResult when one or more target hosts fail.
// Host is the failed image host name.
// UsageScope is the upload scope targeted for that host.
// Trackers lists trackers blocked by this host failure.
// Message contains the host failure reason.
type UploadImageHostFailure struct {
	Host       string
	UsageScope string
	Trackers   []string
	Message    string
}

// UploadImageHostAttemptResult retains one exact scheduled host/scope attempt,
// including failures later recovered by fallback.
type UploadImageHostAttemptResult struct {
	Host       string
	UsageScope string
	Trackers   []string
	Fallback   bool
	Links      []UploadedImageLink
	Failure    *UploadImageHostFailure
}

// UploadImagesResult aggregates image upload outcomes across target hosts.
// Links contains successfully uploaded image links and is populated when one
// or more host uploads succeed.
// Failures contains terminal host-level failures that still block one or more
// trackers after fallback attempts. FailedHosts contains every host whose
// attempt failed, including failures recovered by a fallback, so later workflow
// stages can avoid retrying them.
type UploadImagesResult struct {
	Links       []UploadedImageLink
	Failures    []UploadImageHostFailure
	FailedHosts []string
	Attempts    []UploadImageHostAttemptResult
}
