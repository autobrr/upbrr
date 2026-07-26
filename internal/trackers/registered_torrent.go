// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/autobrr/upbrr/pkg/api"
)

// RegisteredTorrentMaxBytes is the largest tracker-returned torrent accepted
// for local persistence or client injection.
const RegisteredTorrentMaxBytes = 8 << 20

// PersistRegisteredTorrent validates exact tracker-returned metainfo bytes and
// atomically installs them with user-only permissions.
func PersistRegisteredTorrent(outputPath string, payload []byte) error {
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		return errors.New("trackers: registered torrent output path is required")
	}
	if err := validateRegisteredTorrentPayload(payload); err != nil {
		return err
	}
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("trackers: create registered torrent dir: %w", err)
	}
	temp, err := os.CreateTemp(dir, filepath.Base(outputPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("trackers: create temp registered torrent: %w", err)
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("trackers: restrict temp registered torrent: %w", err)
	}
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return fmt.Errorf("trackers: write temp registered torrent: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("trackers: sync temp registered torrent: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("trackers: close temp registered torrent: %w", err)
	}
	if err := replaceStagedTorrent(tempPath, outputPath); err != nil {
		return fmt.Errorf("trackers: replace registered torrent: %w", err)
	}
	removeTemp = false
	return nil
}

// DownloadRegisteredTorrent enforces a successful bounded response before
// validating and atomically persisting the exact response bytes.
func DownloadRegisteredTorrent(
	ctx context.Context,
	client *http.Client,
	request *http.Request,
	outputPath string,
) error {
	if client == nil {
		return errors.New("trackers: registered torrent HTTP client is required")
	}
	if request == nil {
		return errors.New("trackers: registered torrent request is required")
	}
	request = request.Clone(ctx)
	//nolint:gosec // Tracker adapters own and validate the authenticated endpoint.
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("trackers: download registered torrent: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("trackers: registered torrent download returned status %d", response.StatusCode)
	}
	payload, err := readRegisteredTorrentPayload(response.Body)
	if err != nil {
		return err
	}
	if err := PersistRegisteredTorrent(outputPath, payload); err != nil {
		return fmt.Errorf("trackers: persist downloaded registered torrent: %w", err)
	}
	return nil
}

// ReadRegisteredTorrent validates and returns exact bytes from a retained local
// registered artifact.
func ReadRegisteredTorrent(torrentPath string) ([]byte, error) {
	torrentPath = strings.TrimSpace(torrentPath)
	if torrentPath == "" {
		return nil, errors.New("trackers: registered torrent path is required")
	}
	file, err := os.Open(torrentPath)
	if err != nil {
		return nil, fmt.Errorf("trackers: open registered torrent: %w", err)
	}
	defer file.Close()
	payload, err := readRegisteredTorrentPayload(file)
	if err != nil {
		return nil, err
	}
	if err := validateRegisteredTorrentPayload(payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// LogRegisteredTorrentUnavailable reports a stable post-submit local failure
// without copying private URLs, paths, credentials, or remote errors to logs.
func LogRegisteredTorrentUnavailable(logger api.Logger, tracker string) {
	if logger == nil {
		return
	}
	logger.Warnf(
		"trackers: registered artifact unavailable tracker=%s artifact=registered_torrent decision=failed",
		normalizeTrackerName(tracker),
	)
}

// PersistReconstructedRegisteredTorrent rebuilds a tracker-registered artifact
// after known remote success. Failure is retained as missing local authority
// instead of being reclassified as a failed tracker submission.
func PersistReconstructedRegisteredTorrent(
	logger api.Logger,
	tracker string,
	sourcePath string,
	outputPath string,
	announceURL string,
	comment string,
	source string,
) string {
	if strings.TrimSpace(announceURL) == "" || strings.TrimSpace(outputPath) == "" {
		LogRegisteredTorrentUnavailable(logger, tracker)
		return ""
	}
	if err := WritePersonalizedTorrent(sourcePath, outputPath, announceURL, comment, source); err != nil {
		LogRegisteredTorrentUnavailable(logger, tracker)
		return ""
	}
	return outputPath
}

func readRegisteredTorrentPayload(reader io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, RegisteredTorrentMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("trackers: read registered torrent: %w", err)
	}
	if len(payload) > RegisteredTorrentMaxBytes {
		return nil, fmt.Errorf("trackers: registered torrent exceeds %d bytes", RegisteredTorrentMaxBytes)
	}
	if len(payload) == 0 {
		return nil, errors.New("trackers: registered torrent is empty")
	}
	return payload, nil
}

func validateRegisteredTorrentPayload(payload []byte) error {
	if len(payload) == 0 {
		return errors.New("trackers: registered torrent is empty")
	}
	if len(payload) > RegisteredTorrentMaxBytes {
		return fmt.Errorf("trackers: registered torrent exceeds %d bytes", RegisteredTorrentMaxBytes)
	}
	torrentMeta, err := metainfo.Load(bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("trackers: decode registered torrent metainfo: %w", err)
	}
	if _, err := torrentMeta.UnmarshalInfo(); err != nil {
		return fmt.Errorf("trackers: decode registered torrent info: %w", err)
	}
	return nil
}
