// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/apitoken"
	"github.com/autobrr/upbrr/internal/authmaterial"
)

func TestAPITokenCLIManagesPersistentTokens(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "upbrr.db")
	configPath := filepath.Join(tempDir, "config.yaml")
	configBody := "main_settings:\n  db_path: " + filepath.ToSlash(dbPath) + "\nscreenshot_handling:\n  screens: 1\n"
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("bootstrap auth: %v", err)
	}

	var createOutput bytes.Buffer
	if err := runAPITokenCommand(ctx, []string{
		"create",
		"--config", configPath,
		"--name", "CLI automation",
		"--owner", "cli-owner",
		"--scopes", "workflow:read",
	}, &createOutput); err != nil {
		t.Fatalf("create command: %v", err)
	}
	createdFields := strings.Fields(createOutput.String())
	if len(createdFields) < 8 {
		t.Fatal("create output is incomplete")
	}
	var id string
	var token string
	for index, value := range createdFields {
		if value == "ID:" && index+1 < len(createdFields) {
			id = createdFields[index+1]
		}
		if value == "Token:" && index+1 < len(createdFields) {
			token = createdFields[index+1]
		}
	}
	if id == "" || token == "" {
		t.Fatal("create output did not contain token ID and one-time token")
	}

	var listOutput bytes.Buffer
	if err := runAPITokenCommand(ctx, []string{"list", "--config", configPath}, &listOutput); err != nil {
		t.Fatalf("list command: %v", err)
	}
	if !strings.Contains(listOutput.String(), "CLI automation") || !strings.Contains(listOutput.String(), id) {
		t.Fatalf("list output = %q", listOutput.String())
	}
	if strings.Contains(listOutput.String(), token) {
		t.Fatal("list output exposed plaintext token")
	}

	repository, err := authmaterial.NewStore(dbPath)
	if err != nil {
		t.Fatalf("open auth store: %v", err)
	}
	service, err := apitoken.NewService(repository)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	principal, ok, err := service.Authenticate(ctx, token)
	if err != nil || !ok || principal.OwnerID != "api:cli-owner" {
		t.Fatalf("authenticate principal=%#v ok=%t err=%v", principal, ok, err)
	}
	var revokeOutput bytes.Buffer
	if err := runAPITokenCommand(ctx, []string{"revoke", "--config", configPath, id}, &revokeOutput); err != nil {
		t.Fatalf("revoke command: %v", err)
	}
	if !strings.Contains(revokeOutput.String(), id) {
		t.Fatalf("revoke output = %q", revokeOutput.String())
	}
}
