// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/imagehosting"
	"github.com/autobrr/upbrr/internal/metadata/bluraycom"
	"github.com/autobrr/upbrr/pkg/api"
)

type connectionTestKey struct {
	owner  string
	target api.ConnectionTestTarget
}

// TestConnection probes one draft integration without saving or activating the
// draft. Secret decryption always uses this server's open database location.
// Concurrent requests for the same browser session and target are rejected.
func (b *Backend) TestConnection(ctx context.Context, owner string, req api.ConnectionTestRequest) api.ConnectionTestResult {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch req.Target.Kind {
	case "tracker":
		req.Target.Instance = strings.ToUpper(strings.TrimSpace(req.Target.Instance))
	case "image_host":
		req.Target.Instance = strings.ToLower(strings.TrimSpace(req.Target.Instance))
	}
	if b == nil || b.repo == nil {
		return api.ConnectionTestResult{Code: "unavailable", Message: "Settings storage is unavailable. Try again after the server is ready."}
	}
	if ctx.Err() != nil {
		return connectiontest.RequestFailure(ctx, ctx.Err())
	}
	if len(req.Payload) > configImportMaxBytes {
		return api.ConnectionTestResult{Code: "invalid_configuration", Message: "The settings draft exceeds the connection test limit."}
	}
	key := connectionTestKey{owner: owner, target: req.Target}
	b.connectionTestMu.Lock()
	if b.connectionTests[key] {
		b.connectionTestMu.Unlock()
		return api.ConnectionTestResult{Code: "in_progress", Message: "A connection test is already running for this integration."}
	}
	if b.connectionTests == nil {
		b.connectionTests = make(map[connectionTestKey]bool)
	}
	if b.connectionStorageReads == nil {
		b.connectionStorageReads = make(chan struct{}, 1)
	}
	storageReads := b.connectionStorageReads
	b.connectionTests[key] = true
	b.connectionTestMu.Unlock()
	defer func() {
		b.connectionTestMu.Lock()
		delete(b.connectionTests, key)
		b.connectionTestMu.Unlock()
	}()

	draft, err := config.ImportFromJSON(req.Payload)
	if err != nil {
		return api.ConnectionTestResult{Code: "invalid_configuration", Message: "The settings draft could not be decoded. Reload Settings and try again."}
	}
	// Decrypt only credentials relevant to this probe. A browser-supplied path
	// must never select auth material or a database.
	selected := &config.Config{}
	var descriptor trackers.Descriptor
	switch req.Target.Kind {
	case "tmdb":
		selected.MainSettings.TMDBAPI = draft.MainSettings.TMDBAPI
	case "sonarr", "radarr":
		selected.ArrIntegration = draft.ArrIntegration
		selectedKey := -1
		if len(req.Target.Instance) == 1 && req.Target.Instance[0] >= '0' && req.Target.Instance[0] <= '3' {
			selectedKey = int(req.Target.Instance[0] - '0')
			if req.Target.Kind == "radarr" {
				selectedKey += 4
			}
		}
		arr := &selected.ArrIntegration
		keys := [...]*string{
			&arr.SonarrAPIKey, &arr.SonarrAPIKey1, &arr.SonarrAPIKey2, &arr.SonarrAPIKey3,
			&arr.RadarrAPIKey, &arr.RadarrAPIKey1, &arr.RadarrAPIKey2, &arr.RadarrAPIKey3,
		}
		for index, value := range keys {
			if index != selectedKey {
				*value = ""
			}
		}
	case "image_host":
		if req.Target.Instance == "zipline" {
			selected.ImageHosting.ZiplineURL = draft.ImageHosting.ZiplineURL
			selected.ImageHosting.ZiplineAPIKey = draft.ImageHosting.ZiplineAPIKey
		}
		if req.Target.Instance == "samaritano" {
			selected.ImageHosting.SamaritanoAPI = draft.ImageHosting.SamaritanoAPI
		}
	case "tracker":
		registry, catalogErr := trackerimpl.NewRegistry()
		if catalogErr != nil {
			return api.ConnectionTestResult{Code: "unavailable", Message: "The tracker catalog is unavailable."}
		}
		var ok bool
		descriptor, ok = registry.LookupDescriptor(req.Target.Instance)
		if !ok {
			return api.ConnectionTestResult{Code: "unsupported", Message: "This tracker is not in the supported catalog. No request was sent."}
		}
		if tracker, found := config.TrackerConfigByName(draft.Trackers.Trackers, req.Target.Instance); found && descriptor.ConnectionProbe.DraftConfig != nil {
			selected.Trackers.Trackers = map[string]config.TrackerConfig{req.Target.Instance: descriptor.ConnectionProbe.DraftConfig(tracker)}
		}
	case "torrent_client":
		if client, ok := draft.TorrentClients[req.Target.Instance]; ok {
			// Config's JSON clone retains only the UI certificate flag. Capture
			// the effective legacy/current precedence before decrypting secrets.
			verifyCertificate := !client.QbitTLSSkipVerify()
			client.VerifyWebUICertificate = &verifyCertificate
			selected.TorrentClients = map[string]config.TorrentClientConfig{req.Target.Instance: client}
		}
	}
	selected.MainSettings.DBPath = b.repo.DBPath()
	needsStorage, err := config.HasEncryptedSecrets(selected)
	if err == nil {
		if needsStorage {
			draft, err = readConnectionStorage(ctx, storageReads, func() (*config.Config, error) {
				return config.DecryptConfigSecrets(selected)
			})
		} else {
			draft, err = config.DecryptConfigSecrets(selected)
		}
	}
	if ctx.Err() != nil {
		return connectiontest.RequestFailure(ctx, ctx.Err())
	}
	if err != nil {
		return api.ConnectionTestResult{
			Code:    "invalid_configuration",
			Message: "Stored credentials could not be restored. Reload Settings or enter the credentials again.",
		}
	}
	if req.Target.Kind == "bluray" {
		return connectiontest.Run(ctx, bluraycom.TestConnection)
	}
	if req.Target.Kind == "image_host" {
		return connectiontest.Run(ctx, func(ctx context.Context, client *http.Client) api.ConnectionTestResult {
			return imagehosting.TestConnection(ctx, draft.ImageHosting, req.Target.Instance, client)
		})
	}
	if req.Target.Kind == "tracker" {
		if descriptor.ConnectionProbe.Test == nil {
			message := descriptor.ConnectionProbe.UnverifiedReason
			if message == "" {
				message = "A safe draft connection test has not yet been verified for this tracker. No request was sent."
			}
			return api.ConnectionTestResult{
				Code:    "unverified",
				Message: message,
			}
		}
		input := trackers.ConnectionProbeInput{Config: draft.Trackers.Trackers[req.Target.Instance]}
		useCookies := descriptor.ConnectionProbe.StoredCookies
		if descriptor.ConnectionProbe.CredentialsOverrideCookies &&
			(strings.TrimSpace(input.Config.Username) != "" || strings.TrimSpace(input.Config.Password) != "") {
			useCookies = false
		}
		if useCookies {
			endpoint, parseErr := url.Parse(descriptor.BaseURL)
			if parseErr != nil || endpoint.Hostname() == "" {
				return api.ConnectionTestResult{Code: "unavailable", Message: "The tracker catalog endpoint is unavailable."}
			}
			input.Cookies, err = readConnectionStorage(ctx, storageReads, func() (map[string]string, error) {
				return cookies.SnapshotTrackerCookieMap(ctx, b.repo.RawDB(), b.repo.DBPath(), descriptor.Name, endpoint.Hostname())
			})
			if ctx.Err() != nil {
				return connectiontest.RequestFailure(ctx, ctx.Err())
			}
			if err != nil && !errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
				return api.ConnectionTestResult{
					Code:    "unavailable",
					Message: "The stored session could not be read safely. Import current browser cookies and try again.",
				}
			}
		}
		return connectiontest.Run(ctx, func(ctx context.Context, client *http.Client) api.ConnectionTestResult {
			return descriptor.ConnectionProbe.Test(ctx, input, client)
		})
	}
	return connectiontest.Test(ctx, *draft, req.Target)
}

// readConnectionStorage bounds caller waiting and admits one storage operation
// per backend. OS-blocked filesystem I/O may outlive cancellation; retain its
// slot until I/O and cleanup finish so later requests cannot accumulate workers.
// The callback must only read local storage; provider requests stay in the caller.
func readConnectionStorage[T any](ctx context.Context, slot chan struct{}, read func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, fmt.Errorf("connection storage: %w", err)
	}
	select {
	case slot <- struct{}{}:
	case <-ctx.Done():
		return zero, fmt.Errorf("connection storage: %w", ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		<-slot
		return zero, fmt.Errorf("connection storage: %w", err)
	}
	type result struct {
		value T
		err   error
	}
	completed := make(chan result, 1)
	go func() {
		defer func() { <-slot }()
		value, err := read()
		completed <- result{value: value, err: err}
	}()
	select {
	case <-ctx.Done():
		return zero, fmt.Errorf("connection storage: %w", ctx.Err())
	case got := <-completed:
		if err := ctx.Err(); err != nil {
			return zero, fmt.Errorf("connection storage: %w", err)
		}
		return got.value, got.err
	}
}
