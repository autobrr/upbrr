// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

const privateResourceKindRegisteredArtifactAuthority = "releaseworkflow/registered-artifact-authority/v1"

// MarshalPrivateResource persists private registered torrent paths and
// authenticated URLs only inside the owner-scoped private vault.
func (a RegisteredArtifactAuthority) MarshalPrivateResource() (string, []byte, error) {
	if err := a.validate(); err != nil {
		return "", nil, err
	}
	payload, err := json.Marshal(a)
	if err != nil {
		return "", nil, fmt.Errorf("marshal registered artifact authority: %w", err)
	}
	return privateResourceKindRegisteredArtifactAuthority, payload, nil
}

func decodeRegisteredArtifactAuthority(payload []byte) (any, error) {
	var authority RegisteredArtifactAuthority
	if err := json.Unmarshal(payload, &authority); err != nil {
		return nil, fmt.Errorf("decode registered artifact authority: %w", err)
	}
	if err := authority.validate(); err != nil {
		return nil, err
	}
	return authority, nil
}

func (a RegisteredArtifactAuthority) validate() error {
	if strings.TrimSpace(a.ClientSubject.SourcePath) == "" {
		return errors.New("registered artifact authority source is required")
	}
	if len(a.Torrents) == 0 {
		return errors.New("registered artifact authority torrents are required")
	}
	for trackerID, torrent := range a.Torrents {
		normalized := api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if normalized == "" || normalized != trackerID {
			return fmt.Errorf("registered artifact authority tracker %q is invalid", trackerID)
		}
		if !strings.EqualFold(strings.TrimSpace(torrent.Tracker), string(trackerID)) {
			return fmt.Errorf("registered artifact authority tracker %s does not match torrent", trackerID)
		}
		if strings.TrimSpace(torrent.Path) == "" && strings.TrimSpace(torrent.URL) == "" {
			return fmt.Errorf("registered artifact authority tracker %s has no exact torrent", trackerID)
		}
		if torrent.CrossSeed {
			return fmt.Errorf("registered artifact authority tracker %s cannot be a cross-seed", trackerID)
		}
	}
	return nil
}

func cloneRegisteredArtifactAuthority(authority RegisteredArtifactAuthority) RegisteredArtifactAuthority {
	authority.ClientSubject.FileList = append([]string(nil), authority.ClientSubject.FileList...)
	authority.Torrents = maps.Clone(authority.Torrents)
	return authority
}

func mergeRegisteredArtifactAuthorities(
	left RegisteredArtifactAuthority,
	right RegisteredArtifactAuthority,
) RegisteredArtifactAuthority {
	if len(left.Torrents) == 0 {
		return cloneRegisteredArtifactAuthority(right)
	}
	merged := cloneRegisteredArtifactAuthority(left)
	if len(right.Torrents) > 0 {
		merged.ClientSubject = cloneRegisteredArtifactAuthority(right).ClientSubject
		maps.Copy(merged.Torrents, right.Torrents)
	}
	return merged
}

func registeredArtifactAuthorityPrivateResourceID(id api.UploadResultID) string {
	return "registered-artifacts:" + string(id)
}
