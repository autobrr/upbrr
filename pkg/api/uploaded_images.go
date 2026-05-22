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
	UploadedAt time.Time
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

// UploadImagesResult aggregates image upload outcomes across target hosts.
// Links contains successfully uploaded image links and is populated when one
// or more host uploads succeed.
// Failures contains host-level upload failures and is populated when one or
// more target hosts fail.
type UploadImagesResult struct {
	Links    []UploadedImageLink
	Failures []UploadImageHostFailure
}
