// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/pflag"

	"github.com/autobrr/upbrr/internal/apitoken"
	"github.com/autobrr/upbrr/internal/authmaterial"
)

//nolint:gosec // CLI help text contains no credential literal.
const apiTokenCommandUsage = `Usage: upbrr api-token <command> [options]

Commands:
  list      List persisted API token metadata
  revoke    Revoke an API token by ID

Run "upbrr api-token <command> --help" for command options.
`

type configAPITokenOptions struct {
	configPath string
}

func bindConfigAPITokenFlags(fs *pflag.FlagSet, opts *configAPITokenOptions) {
	fs.StringVar(&opts.configPath, "config", "", "Path to config file")
}

func runListAPITokensCommand(ctx context.Context, opts configAPITokenOptions, configProvided bool, output io.Writer) error {
	service, err := openAPITokenService(ctx, opts.configPath, configProvided)
	if err != nil {
		return err
	}
	records, err := service.List(ctx)
	if err != nil {
		return fmt.Errorf("list API credentials: %w", err)
	}
	if len(records) == 0 {
		fmt.Fprintln(output, "No API tokens configured.")
		return nil
	}
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tNAME\tOWNER\tSCOPES\tSTATUS\tCREATED")
	for _, record := range records {
		status := "active"
		if record.RevokedAt != nil {
			status = "revoked"
		}
		fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			record.ID,
			record.Name,
			record.OwnerID,
			joinAPITokenScopes(record.Scopes),
			status,
			record.CreatedAt.Format("2006-01-02 15:04:05Z07:00"),
		)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush API credential list: %w", err)
	}
	return nil
}

func runRevokeAPITokenCommand(ctx context.Context, opts configAPITokenOptions, configProvided bool, id string, output io.Writer) error {
	service, err := openAPITokenService(ctx, opts.configPath, configProvided)
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if err := service.Revoke(ctx, id); err != nil {
		return fmt.Errorf("revoke API credential: %w", err)
	}
	fmt.Fprintf(output, "Revoked API token %s.\n", id)
	return nil
}

func openAPITokenService(
	ctx context.Context,
	configPath string,
	configProvided bool,
) (*apitoken.Service, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open API credential store: %w", err)
	}
	dbPath, err := resolveExportDBPath(configPath, configProvided)
	if err != nil {
		return nil, err
	}
	repository, err := authmaterial.NewStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open API credential store: %w", err)
	}
	record, err := repository.Load()
	if err != nil {
		return nil, fmt.Errorf("load web auth for API credentials (create it with --create-auth or WebUI setup): %w", err)
	}
	if !record.AuthMaterial().IsUsable() {
		return nil, errors.New("load web auth for API credentials: web-auth.json is incomplete")
	}
	service, err := apitoken.NewService(repository)
	if err != nil {
		return nil, fmt.Errorf("create API credential service: %w", err)
	}
	return service, nil
}

func joinAPITokenScopes(scopes []apitoken.Scope) string {
	values := make([]string, len(scopes))
	for index, scope := range scopes {
		values[index] = string(scope)
	}
	return strings.Join(values, ",")
}
