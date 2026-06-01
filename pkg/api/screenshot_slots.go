// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "time"

type ScreenshotSlot struct {
	SourcePath          string
	SlotOrder           int
	SourceKind          string
	OriginalKey         string
	OriginalURL         string
	OriginalHost        string
	ImagePath           string
	FromDescription     bool
	Tracker             string
	SectionKind         string
	RenderInScreenshots bool
	Variants            []ScreenshotSlotVariant
}

type ScreenshotSlotVariant struct {
	SourcePath string
	SlotOrder  int
	Host       string
	UsageScope string
	ImagePath  string
	ImgURL     string
	RawURL     string
	WebURL     string
	UploadedAt time.Time `ts_type:"string"`
}
