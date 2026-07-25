// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/autobrr/upbrr/internal/apitoken"
	"github.com/autobrr/upbrr/internal/authmaterial"
)

//nolint:gosec // CLI help text contains no credential literal.
const apiTokenCommandUsage = `Usage: upbrr api-token <command> [options]

Commands:
  create    Generate and persist a new API token
  list      List persisted API token metadata
  revoke    Revoke an API token by ID

Run "upbrr api-token <command> --help" for command options.
`

func runAPITokenCommand(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return &cliHelpError{usage: apiTokenCommandUsage}
	}
	switch args[0] {
	case "create":
		return runCreateAPITokenCommand(ctx, args[1:], output)
	case "list":
		return runListAPITokensCommand(ctx, args[1:], output)
	case "revoke":
		return runRevokeAPITokenCommand(ctx, args[1:], output)
	default:
		return exitError(2, fmt.Errorf("unknown api-token command %q", args[0]))
	}
}

func runCreateAPITokenCommand(ctx context.Context, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("api-token create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var configPath string
	var name string
	var ownerID string
	var rawScopes string
	fs.StringVar(&configPath, "config", "", "Path to config file")
	fs.StringVar(&name, "name", "CLI token", "Operator-facing token name")
	fs.StringVar(&ownerID, "owner", "default", "Workflow owner isolation key")
	fs.StringVar(&rawScopes, "scopes", joinAPITokenScopes(apitoken.AllScopes()), "Comma-separated API scopes")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return &cliHelpError{usage: formatFlagUsage(fs, "upbrr api-token create [options]")}
		}
		return exitError(2, fmt.Errorf("parse api-token create options: %w", err))
	}
	if fs.NArg() != 0 {
		return exitError(2, errors.New("api-token create does not accept positional arguments"))
	}
	scopes, err := parseAPITokenScopes(rawScopes)
	if err != nil {
		return exitError(2, err)
	}
	service, err := openAPITokenService(ctx, configPath, flagVisited(fs, "config"))
	if err != nil {
		return err
	}
	created, err := service.Create(ctx, apitoken.CreateInput{
		Name:    name,
		OwnerID: ownerID,
		Scopes:  scopes,
	})
	if err != nil {
		return fmt.Errorf("create API credential: %w", err)
	}
	fmt.Fprintf(output, "API token created.\nID: %s\nToken: %s\nStore this token now; it will not be shown again.\n", created.Record.ID, created.Token)
	return nil
}

func runListAPITokensCommand(ctx context.Context, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("api-token list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var configPath string
	fs.StringVar(&configPath, "config", "", "Path to config file")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return &cliHelpError{usage: formatFlagUsage(fs, "upbrr api-token list [options]")}
		}
		return exitError(2, fmt.Errorf("parse api-token list options: %w", err))
	}
	if fs.NArg() != 0 {
		return exitError(2, errors.New("api-token list does not accept positional arguments"))
	}
	service, err := openAPITokenService(ctx, configPath, flagVisited(fs, "config"))
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

func runRevokeAPITokenCommand(ctx context.Context, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("api-token revoke", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var configPath string
	fs.StringVar(&configPath, "config", "", "Path to config file")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return &cliHelpError{usage: formatFlagUsage(fs, "upbrr api-token revoke [options] <token-id>")}
		}
		return exitError(2, fmt.Errorf("parse api-token revoke options: %w", err))
	}
	if fs.NArg() != 1 {
		return exitError(2, errors.New("api-token revoke requires exactly one token ID"))
	}
	service, err := openAPITokenService(ctx, configPath, flagVisited(fs, "config"))
	if err != nil {
		return err
	}
	id := strings.TrimSpace(fs.Arg(0))
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

func parseAPITokenScopes(raw string) ([]apitoken.Scope, error) {
	scopes := make([]apitoken.Scope, 0)
	for value := range strings.SplitSeq(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			scopes = append(scopes, apitoken.Scope(value))
		}
	}
	normalized, err := apitoken.NormalizeScopes(scopes)
	if err != nil {
		return nil, fmt.Errorf("parse API credential scopes: %w", err)
	}
	return normalized, nil
}

func joinAPITokenScopes(scopes []apitoken.Scope) string {
	values := make([]string, len(scopes))
	for index, scope := range scopes {
		values[index] = string(scope)
	}
	return strings.Join(values, ",")
}

func flagVisited(fs *flag.FlagSet, name string) bool {
	visited := false
	fs.Visit(func(current *flag.Flag) {
		if current.Name == name {
			visited = true
		}
	})
	return visited
}
