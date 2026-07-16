// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ApplicationInfo describes the running build and optional runtime capability
// probes returned by WebUI entrypoints.
type ApplicationInfo struct {
	Version         string `json:"version"`
	BuildIdentifier string `json:"buildIdentifier"`
	GoVersion       string `json:"goVersion"`
	GOOS            string `json:"goos"`
	GOARCH          string `json:"goarch"`
	Uptime          string `json:"uptime"`
	UptimeSeconds   int64  `json:"uptimeSeconds"`
	// DVDMenuEngine contains path-free engine and FFmpeg probe metadata.
	DVDMenuEngine DVDMenuEngineInfo `json:"dvdMenuEngine"`
	// DVDMenuCapabilityStatus is available, incompatible, or unavailable.
	DVDMenuCapabilityStatus string `json:"dvdMenuCapabilityStatus"`
	// DVDMenuCapabilityMessage is the user-facing reason for the capability status.
	DVDMenuCapabilityMessage string `json:"dvdMenuCapabilityMessage"`
}

var (
	applicationInfoMu    sync.RWMutex
	applicationVersion   string
	applicationBuildID   string
	applicationStartedAt = time.Now()
)

// SetApplicationBuild stores trimmed build metadata returned by
// [CurrentApplicationInfo].
func SetApplicationBuild(version string, buildIdentifier string) {
	applicationInfoMu.Lock()
	defer applicationInfoMu.Unlock()

	applicationVersion = strings.TrimSpace(version)
	applicationBuildID = strings.TrimSpace(buildIdentifier)
}

// CurrentApplicationInfo returns process build, platform, and uptime metadata.
// Optional capability fields remain zero until an entrypoint probes them.
func CurrentApplicationInfo() ApplicationInfo {
	uptime := max(time.Since(applicationStartedAt), 0)

	version, buildIdentifier := resolvedApplicationBuild()
	return ApplicationInfo{
		Version:         version,
		BuildIdentifier: buildIdentifier,
		GoVersion:       runtime.Version(),
		GOOS:            runtime.GOOS,
		GOARCH:          runtime.GOARCH,
		Uptime:          formatApplicationUptime(uptime),
		UptimeSeconds:   int64(uptime / time.Second),
	}
}

func resolvedApplicationBuild() (string, string) {
	applicationInfoMu.RLock()
	version := applicationVersion
	buildIdentifier := applicationBuildID
	applicationInfoMu.RUnlock()

	info, ok := debug.ReadBuildInfo()
	if !ok {
		if version == "" {
			version = "dev"
		}
		return version, buildIdentifier
	}

	if version == "" {
		candidate := strings.TrimSpace(info.Main.Version)
		if candidate != "" && candidate != "(devel)" {
			version = candidate
		} else {
			version = "dev"
		}
	}

	if buildIdentifier == "" {
		revision := strings.TrimSpace(buildSetting(info, "vcs.revision"))
		if len(revision) > 12 {
			revision = revision[:12]
		}
		if revision != "" {
			buildIdentifier = revision
			if strings.EqualFold(strings.TrimSpace(buildSetting(info, "vcs.modified")), "true") {
				buildIdentifier += "-dirty"
			}
		}
	}

	return version, buildIdentifier
}

func buildSetting(info *debug.BuildInfo, key string) string {
	if info == nil {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}

func formatApplicationUptime(uptime time.Duration) string {
	totalSeconds := int64(uptime / time.Second)
	days := totalSeconds / (24 * 60 * 60)
	totalSeconds %= 24 * 60 * 60
	hours := totalSeconds / (60 * 60)
	totalSeconds %= 60 * 60
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60

	parts := make([]string, 0, 4)
	if days > 0 {
		parts = append(parts, strconv.FormatInt(days, 10)+"d")
	}
	if hours > 0 || len(parts) > 0 {
		parts = append(parts, strconv.FormatInt(hours, 10)+"h")
	}
	if minutes > 0 || len(parts) > 0 {
		parts = append(parts, strconv.FormatInt(minutes, 10)+"m")
	}
	parts = append(parts, strconv.FormatInt(seconds, 10)+"s")

	return strings.Join(parts, " ")
}
