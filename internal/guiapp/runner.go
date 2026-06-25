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

// RunOptions configures Wails GUI startup.
type RunOptions struct {
	// Assets overrides the embedded or discovered frontend asset filesystem.
	Assets fs.FS
	// ConfigPath is passed to config bootstrap as the optional config file path.
	ConfigPath string
	// ConfigProvided reports whether ConfigPath came from an explicit user flag.
	ConfigProvided bool
}

// Run starts the Wails GUI with a background context.
func Run(opts RunOptions) error {
	return RunWithContext(context.Background(), opts)
}

// RunWithContext starts the Wails GUI after resolving assets and creating the
// bound app with ctx.
func RunWithContext(ctx context.Context, opts RunOptions) error {
	assets, err := resolveAssets(opts.Assets)
	if err != nil {
		return err
	}

	app, err := NewAppWithContext(ctx, opts.ConfigPath, opts.ConfigProvided)
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
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		Bind: []any{
			app,
		},
	}); err != nil {
		return fmt.Errorf("gui: run: %w", err)
	}

	return nil
}
