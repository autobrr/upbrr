// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	qbittorrent "github.com/autobrr/go-qbittorrent"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/pkg/api"
)

// qbitInjectionArtifact keeps private announce URLs in memory solely for exact
// readback comparison. They must never enter receipts or diagnostic text.
type qbitInjectionArtifact struct {
	hash     string
	trackers map[string]struct{}
}

func loadQbitInjectionArtifact(path string) (*qbitInjectionArtifact, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("clients: qbit injection verification requires a local torrent artifact")
	}
	meta, err := metainfo.LoadFromFile(path)
	if err != nil {
		return nil, errors.New("clients: qbit injection verification could not read torrent metainfo")
	}
	info, err := meta.UnmarshalInfo()
	if err != nil || len(meta.InfoBytes) == 0 || !info.HasV1() {
		return nil, errors.New("clients: qbit injection verification requires valid v1 torrent metainfo")
	}
	artifact := &qbitInjectionArtifact{hash: meta.HashInfoBytes().HexString(), trackers: make(map[string]struct{})}
	for _, tier := range meta.UpvertedAnnounceList() {
		for _, tracker := range tier {
			if tracker = strings.TrimSpace(tracker); tracker != "" {
				artifact.trackers[tracker] = struct{}{}
			}
		}
	}
	return artifact, nil
}

// installed proves the infohash exists even for a trackerless prepared torrent,
// then requires every effective announce URL from the exact local artifact.
func (a *qbitInjectionArtifact) installed(ctx context.Context, client *qbittorrent.Client) (bool, error) {
	torrents, err := client.GetTorrentsCtx(ctx, qbittorrent.TorrentFilterOptions{Hashes: []string{a.hash}})
	if err != nil {
		return false, fmt.Errorf("read injected torrent: %w", err)
	}
	found := false
	for _, torrent := range torrents {
		if strings.EqualFold(torrent.Hash, a.hash) {
			found = true
			break
		}
	}
	if !found || len(a.trackers) == 0 {
		return found, nil
	}
	trackers, err := client.GetTorrentTrackersCtx(ctx, a.hash)
	if err != nil {
		return false, fmt.Errorf("read injected torrent trackers: %w", err)
	}
	present := make(map[string]struct{}, len(trackers))
	for _, tracker := range trackers {
		present[strings.TrimSpace(tracker.Url)] = struct{}{}
	}
	for tracker := range a.trackers {
		if _, ok := present[tracker]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// waitInstalled allows asynchronous client adds to become visible. An add is
// sent only once; failed or incomplete readback leaves the durable attempt open
// because client writes or tracker merging may already have occurred.
func (a *qbitInjectionArtifact) waitInstalled(ctx context.Context, client *qbittorrent.Client) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		installed, err := a.installed(ctx, client)
		if installed {
			return nil
		}
		if err != nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("clients: qbit injection could not verify the required torrent trackers: %w", api.ErrReleaseWorkflowEffectOutcomeUnknown)
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("clients: qbit injection readback failed: %w", api.ErrReleaseWorkflowEffectOutcomeUnknown)
}
