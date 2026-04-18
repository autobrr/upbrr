// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

type RunOptions struct {
	StartupContext context.Context
	Assets         fs.FS
	ConfigPath     string
	ConfigProvided bool
}

func Run(opts RunOptions) error {
	assets, err := resolveAssets(opts.Assets)
	if err != nil {
		return err
	}

	app, err := NewAppWithContext(opts.StartupContext, opts.ConfigPath, opts.ConfigProvided)
	if err != nil {
		return err
	}

	if err := wails.Run(&options.App{
		Title:     "upbrr",
		Width:     1200,
		Height:    820,
		MinWidth:  900,
		MinHeight: 700,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	}); err != nil {
		return fmt.Errorf("gui: run: %w", err)
	}

	return nil
}
