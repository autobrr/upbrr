// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/apitoken"
	"github.com/autobrr/upbrr/internal/authmaterial"
)

func TestAPITokenCLIListsAndRevokesPersistentTokens(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
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

	repository, err := authmaterial.NewStore(dbPath)
	if err != nil {
		t.Fatalf("open auth store: %v", err)
	}
	service, err := apitoken.NewService(repository)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	created, err := service.Create(ctx, apitoken.CreateInput{
		Name:    "CLI automation",
		OwnerID: "cli-owner",
		Scopes:  []apitoken.Scope{apitoken.ScopeWorkflowRead},
	})
	if err != nil {
		t.Fatalf("seed API token: %v", err)
	}
	id := created.Record.ID
	token := created.Token

	createResult := executeCLIForTest(ctx, t, []string{"api-token", "create", "--config", configPath})
	if createResult.code != 2 || !strings.Contains(createResult.stderr, `unknown api-token command "create"`) {
		t.Fatalf("removed create command result = %#v", createResult)
	}
	if strings.Contains(createResult.stdout, token) || strings.Contains(createResult.stderr, token) {
		t.Fatal("removed create command exposed plaintext token")
	}

	var listOutput bytes.Buffer
	if err := executeCLI(ctx, []string{"api-token", "list", "--config", configPath}, cliIO{out: &listOutput}); err != nil {
		t.Fatalf("list command: %v", err)
	}
	if !strings.Contains(listOutput.String(), "CLI automation") || !strings.Contains(listOutput.String(), id) {
		t.Fatalf("list output = %q", listOutput.String())
	}
	if strings.Contains(listOutput.String(), token) {
		t.Fatal("list output exposed plaintext token")
	}

	principal, ok, err := service.Authenticate(ctx, token)
	if err != nil || !ok || principal.OwnerID != "api:cli-owner" {
		t.Fatalf("authenticate principal=%#v ok=%t err=%v", principal, ok, err)
	}
	var revokeOutput bytes.Buffer
	if err := executeCLI(ctx, []string{"api-token", "revoke", "--config", configPath, id}, cliIO{out: &revokeOutput}); err != nil {
		t.Fatalf("revoke command: %v", err)
	}
	if !strings.Contains(revokeOutput.String(), id) {
		t.Fatalf("revoke output = %q", revokeOutput.String())
	}
	if _, ok, err := service.Authenticate(ctx, token); err != nil || ok {
		t.Fatalf("authenticate revoked token ok=%t err=%v", ok, err)
	}
}
