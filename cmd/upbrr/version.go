// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/services/dvdmenus"
	"github.com/autobrr/upbrr/internal/webserver"
	"github.com/autobrr/upbrr/pkg/api"
)

type cliDVDMenuCapability func(context.Context) (api.DVDMenuEngineInfo, error)

func (probe cliDVDMenuCapability) DVDMenuCapability(ctx context.Context) (api.DVDMenuEngineInfo, error) {
	return probe(ctx)
}

func showCLIVersion(ctx context.Context, output io.Writer) {
	service := dvdmenus.NewService(api.NopLogger{}, "", nil)
	info := webserver.CurrentApplicationInfo(ctx, cliDVDMenuCapability(service.Capability))
	printCLIVersion(output, info)
}

func printCLIVersion(output io.Writer, info api.ApplicationInfo) {
	fmt.Fprintf(output, "upbrr %s\n", cliApplicationVersion(info))
	buildLabel := cmp.Or(info.BuildIdentifier, "unknown")
	if date, err := time.Parse(time.RFC3339, info.BuildTime); err == nil && info.BuildIdentifier != "" {
		buildLabel += " (" + date.UTC().Format("2006-01-02 15:04:05 UTC") + ")"
	}
	fmt.Fprintf(output, "Build: %s\nGo: %s\nPlatform: %s/%s\n",
		buildLabel, info.GoVersion, info.GOOS, info.GOARCH)
	fmt.Fprintln(output, "\nAutobrr dependencies:")
	if len(info.Dependencies) == 0 {
		fmt.Fprintln(output, "  unavailable (module build information not embedded)")
	}
	for _, dependency := range info.Dependencies {
		fmt.Fprintf(output, "  %s %s\n", dependency.Path, dependency.Version)
	}
	engine := info.DVDMenuEngine
	fmt.Fprintf(output, "\nFFmpeg: %s\n", logging.SanitizeMessage(cmp.Or(engine.FFmpegVersion, "unavailable")))
	fmt.Fprintf(output, "DVD menu engine: %s\nDVD metadata schema: %d\n", engine.EngineVersion, engine.SchemaVersion)
	fmt.Fprintf(output, "DVD engine features: %s\n", strings.Join(engine.SupportedFeatures, ", "))
	fmt.Fprintf(output, "FFmpeg dvdvideo menu support: %s\n", info.DVDMenuCapabilityStatus)
	fmt.Fprintf(output, "  %s\n", logging.SanitizeMessage(info.DVDMenuCapabilityMessage))
}

func cliApplicationVersion(info api.ApplicationInfo) string {
	if info.Version != "" && info.Version != "dev" && info.Version != "(devel)" {
		return info.Version
	}
	if info.BuildIdentifier == "" {
		return cmp.Or(info.Version, "dev")
	}
	label := info.BuildIdentifier
	if date, err := time.Parse(time.RFC3339, info.BuildTime); err == nil {
		label += " (" + date.UTC().Format("2006-01-02 15:04:05 UTC") + ")"
	}
	return label
}
