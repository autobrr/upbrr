// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediainfo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/autobrr/upbrr/internal/pathing/layout"
)

// reportIdentity is committed last. Its digests prevent a partial/interleaved
// report pair from granting reuse after a crash or restart.
type reportIdentity struct {
	Path        string `json:"path"`
	Fingerprint string `json:"fingerprint"`
	Size        int64  `json:"size"`
	Modified    int64  `json:"modified"`
	Text        string `json:"text"`
	JSON        string `json:"json"`
}

func mediaInfoIdentity(target, fingerprint string) (reportIdentity, error) {
	path, err := layout.CanonicalSourcePath(target)
	if err != nil {
		return reportIdentity{}, fmt.Errorf("mediainfo: resolve source identity: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return reportIdentity{}, fmt.Errorf("mediainfo: inspect source identity: %w", err)
	}
	return reportIdentity{
		Path:        path,
		Fingerprint: fingerprint,
		Size:        info.Size(),
		Modified:    info.ModTime().UnixNano(),
	}, nil
}

func reportDigest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func matchingReports(dir string, identity reportIdentity) bool {
	payload, err := os.ReadFile(filepath.Join(dir, "mediainfo.identity.json"))
	if err != nil {
		return false
	}
	var stored reportIdentity
	if json.Unmarshal(payload, &stored) != nil {
		return false
	}
	text, textErr := os.ReadFile(filepath.Join(dir, "mediainfo.txt"))
	data, jsonErr := os.ReadFile(filepath.Join(dir, "MediaInfo.json"))
	identity.Text, identity.JSON = reportDigest(text), reportDigest(data)
	return textErr == nil && jsonErr == nil && validReports(text, data) && stored == identity
}

func validReports(text, data []byte) bool {
	var doc conformanceDoc
	return len(bytes.TrimSpace(text)) > 0 && json.Unmarshal(data, &doc) == nil && len(doc.Media.Track) > 0
}

func commitReports(ctx context.Context, dir string, identity reportIdentity, text, data []byte) error {
	if !validReports(text, data) {
		return errors.New("mediainfo: invalid report pair")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mediainfo: publish canceled: %w", err)
	}
	identity.Text, identity.JSON = reportDigest(text), reportDigest(data)
	payload, err := json.Marshal(identity)
	if err != nil {
		return fmt.Errorf("mediainfo: encode report identity: %w", err)
	}
	for _, output := range []struct {
		name string
		data []byte
	}{
		{"mediainfo.txt", text}, {"MediaInfo.json", data}, {"mediainfo.identity.json", payload},
	} {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("mediainfo: publish canceled: %w", err)
		}
		if err := writeMediaInfoText(filepath.Join(dir, output.name), output.data); err != nil {
			return fmt.Errorf("mediainfo: publish %s: %w", output.name, err)
		}
	}
	return nil
}
