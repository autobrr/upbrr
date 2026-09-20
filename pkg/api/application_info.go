// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"cmp"
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
	// TestRuntime is present only when the process enforces live-testing restrictions.
	TestRuntime     *TestRuntimeInfo `json:"testRuntime,omitempty"`
	Version         string           `json:"version"`
	BuildIdentifier string           `json:"buildIdentifier"`
	// BuildTime is the VCS commit time in RFC3339 UTC, when embedded in the binary.
	BuildTime string `json:"buildTime"`
	// Dependencies lists the autobrr modules linked into this binary.
	Dependencies  []ApplicationDependency `json:"dependencies"`
	GoVersion     string                  `json:"goVersion"`
	GOOS          string                  `json:"goos"`
	GOARCH        string                  `json:"goarch"`
	Uptime        string                  `json:"uptime"`
	UptimeSeconds int64                   `json:"uptimeSeconds"`
	// DVDMenuEngine contains path-free engine and FFmpeg probe metadata.
	DVDMenuEngine DVDMenuEngineInfo `json:"dvdMenuEngine"`
	// DVDMenuCapabilityStatus is available, incompatible, or unavailable.
	DVDMenuCapabilityStatus string `json:"dvdMenuCapabilityStatus"`
	// DVDMenuCapabilityMessage is the user-facing reason for the capability status.
	DVDMenuCapabilityMessage string `json:"dvdMenuCapabilityMessage"`
}

// ApplicationDependency identifies a linked autobrr module and its display version.
type ApplicationDependency struct {
	Path string `json:"path"`
	// Version is a release tag or commit and UTC date, including replacement metadata.
	Version string `json:"version"`
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

	build, _ := debug.ReadBuildInfo()
	version, buildIdentifier := resolvedApplicationBuild(build)
	buildTime := ""
	if date, err := time.Parse(time.RFC3339, buildSetting(build, "vcs.time")); err == nil {
		buildTime = date.UTC().Format(time.RFC3339)
	}
	return ApplicationInfo{
		Version:         version,
		BuildIdentifier: buildIdentifier,
		BuildTime:       buildTime,
		Dependencies:    applicationDependencies(build),
		GoVersion:       runtime.Version(),
		GOOS:            runtime.GOOS,
		GOARCH:          runtime.GOARCH,
		Uptime:          formatApplicationUptime(uptime),
		UptimeSeconds:   int64(uptime / time.Second),
	}
}

func resolvedApplicationBuild(info *debug.BuildInfo) (string, string) {
	applicationInfoMu.RLock()
	version := applicationVersion
	buildIdentifier := applicationBuildID
	applicationInfoMu.RUnlock()

	if info == nil {
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

func applicationDependencies(build *debug.BuildInfo) []ApplicationDependency {
	dependencies := make([]ApplicationDependency, 0)
	if build == nil {
		return dependencies
	}
	for _, dependency := range build.Deps {
		if !strings.HasPrefix(dependency.Path, "github.com/autobrr/") {
			continue
		}
		version := applicationModuleVersion(cmp.Or(dependency.Version, "unknown"))
		if dependency.Replace != nil {
			version += " (replacement: " + applicationModuleVersion(cmp.Or(dependency.Replace.Version, "local build")) + ")"
		}
		dependencies = append(dependencies, ApplicationDependency{Path: dependency.Path, Version: version})
	}
	return dependencies
}

func applicationModuleVersion(version string) string {
	prefix, revision, ok := strings.CutLast(strings.TrimSuffix(version, "+incompatible"), "-")
	if !ok || len(revision) != 12 {
		return version
	}
	_, timestamp, _ := strings.CutLast(prefix, "-")
	if _, suffix, found := strings.CutLast(timestamp, "."); found {
		timestamp = suffix
	}
	date, err := time.Parse("20060102150405", timestamp)
	if err != nil {
		return version
	}
	return revision + " (" + date.UTC().Format("2006-01-02 15:04:05 UTC") + ")"
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
