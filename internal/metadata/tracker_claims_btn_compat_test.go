// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/services/db"
	btnimpl "github.com/autobrr/upbrr/internal/trackers/impl/standalone/btn"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	btnClaimedShowsURL    = "https://broadcasthe.net/forums.php?action=viewthread&threadid=30793"
	btnClaimedShowsPostID = "post1405482"
)

type btnClaimedShowsCache struct {
	FetchedAt int64    `json:"fetched_at"`
	SourceURL string   `json:"source_url"`
	PostID    string   `json:"post_id"`
	Titles    []string `json:"titles"`
}

type btnTrackerClaimProvider struct{}

func (btnTrackerClaimProvider) cachePath(dbPath string, _ string) (string, error) {
	path, err := db.FileInSubdir(dbPath, "cache", filepath.Join("banned", "BTN_claimed_releases.json"))
	if err != nil {
		return "", fmt.Errorf("resolve BTN claim cache path: %w", err)
	}
	return path, nil
}
func (btnTrackerClaimProvider) cacheTTL() time.Duration { return 48 * time.Hour }

func normalizeBTNTitle(value string) string { return btnimpl.NormalizeClaimTitle(value) }
func extractBTNClaimedShows(value string) map[string]struct{} {
	return btnimpl.ExtractClaimedShows(value)
}
func mirrorBTNCookiesForClaimedThread(client *http.Client) {
	btnimpl.MirrorCookiesForClaimedThread(client)
}
func btnClaimWindowExpired(meta preparationstate.State, graceHours int) (bool, int, float64) {
	return btnimpl.ClaimWindowExpired(api.UploadSubject{
		TVDBAiredDate:    meta.TVDBAiredDate,
		TVDBAirsTime:     meta.TVDBAirsTime,
		TVDBAirsTimezone: meta.TVDBAirsTimezone,
	}, graceHours)
}

func (s *Service) loadBTNClaimedTitles(ctx context.Context, cachePath string, ttl time.Duration) (map[string]struct{}, error) {
	titles, err := btnimpl.LoadClaimedTitles(ctx, s.cfg, s.logger, cachePath, ttl)
	if err != nil {
		return nil, fmt.Errorf("load BTN claimed titles: %w", err)
	}
	return titles, nil
}
func (s *Service) fetchBTNClaimedTitles(ctx context.Context) (map[string]struct{}, error) {
	titles, err := btnimpl.FetchClaimedTitles(ctx, s.cfg, s.logger)
	if err != nil {
		return nil, fmt.Errorf("fetch BTN claimed titles: %w", err)
	}
	return titles, nil
}
