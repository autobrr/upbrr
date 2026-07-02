// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/autobrr/upbrr/internal/guiapp"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	version         = "dev"
	buildIdentifier = ""
)

func main() {
	api.SetApplicationBuild(version, buildIdentifier)

	configPath := flag.String("config", "", "Path to config file")
	flag.Parse()

	configFlagProvided := false
	flag.CommandLine.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configFlagProvided = true
		}
	})

	if err := guiapp.Run(guiapp.RunOptions{
		Assets:         nil,
		ConfigPath:     *configPath,
		ConfigProvided: configFlagProvided,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", logging.SanitizeMessage(err.Error()))
		os.Exit(1)
	}
}
