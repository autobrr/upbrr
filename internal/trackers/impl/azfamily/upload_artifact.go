// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"bytes"
	"fmt"
	"os"

	"github.com/autobrr/go-torrent/metainfo"
)

// preparedUploadTorrent binds the form hash to the bytes sent during task creation,
// including when a media-addition decision delays that task.
type preparedUploadTorrent struct {
	path     string
	data     []byte
	infoHash string
}

func captureUploadTorrent(path string) (preparedUploadTorrent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return preparedUploadTorrent{}, fmt.Errorf("read torrent artifact: %w", err)
	}
	meta, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return preparedUploadTorrent{}, fmt.Errorf("decode torrent artifact: %w", err)
	}
	if _, err := meta.UnmarshalInfo(); err != nil {
		return preparedUploadTorrent{}, fmt.Errorf("decode torrent info: %w", err)
	}
	return preparedUploadTorrent{
		path:     path,
		data:     data,
		infoHash: meta.HashInfoBytes().String(),
	}, nil
}
