// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/webserver"
)

type RunOptions struct {
	StartupContext context.Context
	Assets         fs.FS
	ConfigPath     string
	ConfigProvided bool
}

type RuntimeConfig struct {
	APIBaseURL          string `json:"apiBaseURL"`
	BearerToken         string `json:"bearerToken"`
	NativeBrowseEnabled bool   `json:"nativeBrowseEnabled"`
}

type DesktopRuntime struct {
	config RuntimeConfig
}

func (d *DesktopRuntime) GetRuntimeConfig() RuntimeConfig {
	if d == nil {
		return RuntimeConfig{}
	}
	return d.config
}

func Run(opts RunOptions) error {
	assets, err := resolveAssets(opts.Assets)
	if err != nil {
		return err
	}

	startupCtx := opts.StartupContext
	if startupCtx == nil {
		startupCtx = context.Background()
	}
	cfg, dbPath, err := configstore.Bootstrap(startupCtx, opts.ConfigPath, opts.ConfigProvided, true)
	if err != nil {
		return err
	}
	token, err := ensureDesktopAPIToken(dbPath)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("gui: start rest listener: %w", err)
	}
	baseURL := "http://" + listener.Addr().String()
	server, err := webserver.New(webserver.Options{
		StartupContext: startupCtx,
		Config:         cfg,
		CLIConfig: webserver.CLIConfig{
			Host:        "127.0.0.1",
			Port:        1,
			OpenBrowser: false,
			BaseURL:     baseURL,
		},
		Assets: assets,
	})
	if err != nil {
		_ = listener.Close()
		return err
	}
	serverCtx, stopServer := context.WithCancel(context.Background())
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ServeListener(serverCtx, listener)
	}()

	desktop := &DesktopRuntime{config: RuntimeConfig{
		APIBaseURL:          baseURL,
		BearerToken:         token,
		NativeBrowseEnabled: true,
	}}

	if err := wails.Run(&options.App{
		Title:     "upbrr",
		Width:     1200,
		Height:    820,
		MinWidth:  900,
		MinHeight: 700,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnShutdown: func(context.Context) {
			stopServer()
			_ = server.Close()
			<-serverErr
		},
		Bind: []interface{}{
			desktop,
		},
	}); err != nil {
		stopServer()
		_ = server.Close()
		return fmt.Errorf("gui: run: %w", err)
	}

	return nil
}

func ensureDesktopAPIToken(dbPath string) (string, error) {
	store, err := authmaterial.NewStore(dbPath)
	if err != nil {
		return "", err
	}
	exists, err := store.Exists()
	if err != nil {
		return "", err
	}
	if !exists {
		password, err := randomDesktopPassword()
		if err != nil {
			return "", err
		}
		if err := store.Bootstrap(authmaterial.DesktopUsername, password); err != nil {
			return "", err
		}
	}
	tokens, err := store.ListAPITokens()
	if err != nil {
		return "", err
	}
	for _, token := range tokens {
		isDesktopToken := token.Purpose == authmaterial.APITokenPurposeDesktop ||
			strings.EqualFold(token.Name, authmaterial.DesktopAPITokenName)
		if token.RevokedAt == nil && isDesktopToken {
			_ = store.RevokeAPIToken(token.ID)
		}
	}
	created, err := store.CreateDesktopAPIToken()
	if err != nil {
		return "", err
	}
	return created.Token, nil
}

func randomDesktopPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("gui: generate desktop auth password: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
