// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rf

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	return addNoGroup(meta, "NoGroup")
}

func addNoGroup(meta api.UploadSubject, suffix string) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	name = strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))
	if tag != "" && !strings.EqualFold(tag, "nogrp") && !strings.EqualFold(tag, "nogroup") && !strings.EqualFold(tag, "unknown") &&
		!strings.EqualFold(tag, "-unk-") {
		return name
	}
	if name == "" || strings.HasSuffix(strings.ToUpper(name), "-"+strings.ToUpper(suffix)) {
		return name
	}
	return name + "-" + suffix
}
