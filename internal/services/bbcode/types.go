// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bbcode

type Image struct {
	ImgURL string
	RawURL string
	WebURL string
	Host   string // Normalized host name (e.g., "imgbb", "pixhost")
}

type Note struct {
	Kind    string
	Message string
}

type Artifact struct {
	Name    string
	Kind    string
	Content string
}

type Report struct {
	Description string
	Images      []Image
	Notes       []Note
	Artifacts   []Artifact
}
