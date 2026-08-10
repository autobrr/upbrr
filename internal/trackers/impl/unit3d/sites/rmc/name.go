// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

var disallowedNameCharsRegex = regexp.MustCompile(`[^A-Za-z0-9 ._-]+`)

// buildName strips the AKA segment sourced from the release's alternate title
// and any character RMC's upload form rejects from the standard generated
// name.
func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := markerName(meta)
	if name == "" {
		return ""
	}
	if alt := strings.TrimSpace(meta.Release.Alt); alt != "" {
		name = removeAKASegment(name, alt)
	}
	return sanitizeName(name)
}

func markerName(meta api.UploadSubject) string {
	if name := strings.TrimSpace(meta.ReleaseName); name != "" {
		return name
	}
	return strings.TrimSpace(meta.ReleaseNameNoTag)
}

// removeAKASegment drops a " AKA <alt> " marker inserted by shared naming
// around the release's alternate title, joining the surrounding parts.
func removeAKASegment(name, alt string) string {
	marker := "AKA " + alt
	index := strings.Index(name, marker)
	if index < 0 {
		return name
	}
	before := strings.TrimRight(name[:index], " ")
	after := strings.TrimLeft(name[index+len(marker):], " ")
	return strings.TrimSpace(strings.Join(strings.Fields(before+" "+after), " "))
}

func sanitizeName(name string) string {
	cleaned := disallowedNameCharsRegex.ReplaceAllString(name, "")
	return strings.TrimSpace(strings.Join(strings.Fields(cleaned), " "))
}
