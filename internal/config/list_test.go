// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStringListUnmarshalJSONAcceptsString(t *testing.T) {
	t.Parallel()

	var client TorrentClientConfig
	if err := json.Unmarshal([]byte(`{"LinkedFolder":"D:\\UA_Linked","LocalPath":"D:\\Media","RemotePath":"Z:\\Media"}`), &client); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(client.LinkedFolder) != 1 || client.LinkedFolder[0] != `D:\UA_Linked` {
		t.Fatalf("unexpected linked_folder: %#v", client.LinkedFolder)
	}
	if len(client.LocalPath) != 1 || client.LocalPath[0] != `D:\Media` {
		t.Fatalf("unexpected local_path: %#v", client.LocalPath)
	}
	if len(client.RemotePath) != 1 || client.RemotePath[0] != `Z:\Media` {
		t.Fatalf("unexpected remote_path: %#v", client.RemotePath)
	}
}

func TestStringListUnmarshalYAMLAcceptsString(t *testing.T) {
	t.Parallel()

	var client TorrentClientConfig
	if err := yaml.Unmarshal([]byte("linked_folder: D:\\UA_Linked\n"), &client); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(client.LinkedFolder) != 1 || client.LinkedFolder[0] != `D:\UA_Linked` {
		t.Fatalf("unexpected linked_folder: %#v", client.LinkedFolder)
	}
}
