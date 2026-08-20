// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/pflag"

	"github.com/autobrr/upbrr/internal/webserver"
)

const authCommandUsage = `Usage: upbrr auth <command> [options]

Commands:
  password       Change the WebUI password
  browse-roots  Replace the WebUI browse roots

Run "upbrr auth <command> --help" for command options.
`

type authPasswordOptions struct {
	configPath string
}

type authBrowseRootsOptions struct {
	configPath        string
	allowUnrestricted bool
}

func bindAuthPasswordFlags(fs *pflag.FlagSet, opts *authPasswordOptions) {
	fs.StringVar(&opts.configPath, "config", "", "Path to config file")
}

func bindAuthBrowseRootsFlags(fs *pflag.FlagSet, opts *authBrowseRootsOptions) {
	fs.StringVar(&opts.configPath, "config", "", "Path to config file")
	fs.BoolVar(&opts.allowUnrestricted, "allow-unrestricted", false, "Allow unrestricted host browsing instead of configured roots")
}

func runChangeAuthPasswordCommand(ctx context.Context, opts authPasswordOptions, configProvided bool, streams cliIO) error {
	dbPath, err := resolveExportDBPath(opts.configPath, configProvided)
	if err != nil {
		return err
	}
	streams = streams.normalized()
	reader := bufio.NewReader(streams.in)
	currentPassword, err := promptPassword(streams.in, reader, streams.out, "Current password: ", "change auth password")
	if err != nil {
		return err
	}
	newPassword, err := promptPassword(streams.in, reader, streams.out, "New password: ", "change auth password")
	if err != nil {
		return err
	}
	confirmation, err := promptPassword(streams.in, reader, streams.out, "Confirm new password: ", "change auth password")
	if err != nil {
		return err
	}
	if newPassword != confirmation {
		return errors.New("change auth password: passwords do not match")
	}
	backupPath, changeErr := webserver.ChangeAuthPassword(ctx, dbPath, currentPassword, newPassword)
	if changeErr != nil {
		changeErr = fmt.Errorf("change password: %w", changeErr)
	}
	if err := errors.Join(changeErr, writeAuthBackupPath(streams.out, backupPath)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(streams.out, "Password changed. Retained browser sessions were revoked."); err != nil {
		return fmt.Errorf("change auth password: write result: %w", err)
	}
	return nil
}

func runUpdateBrowseRootsCommand(
	ctx context.Context,
	opts authBrowseRootsOptions,
	configProvided bool,
	paths []string,
	output io.Writer,
) error {
	dbPath, err := resolveExportDBPath(opts.configPath, configProvided)
	if err != nil {
		return err
	}
	count, backupPath, updateErr := webserver.UpdateBrowseRoots(ctx, dbPath, paths, opts.allowUnrestricted)
	if updateErr != nil {
		updateErr = fmt.Errorf("replace browse roots: %w", updateErr)
	}
	if err := errors.Join(updateErr, writeAuthBackupPath(output, backupPath)); err != nil {
		return err
	}
	if opts.allowUnrestricted {
		_, err = fmt.Fprintln(output, "Enabled unrestricted host browsing.")
	} else {
		_, err = fmt.Fprintf(output, "Updated %d browse roots.\n", count)
	}
	if err != nil {
		return fmt.Errorf("update browse roots: write result: %w", err)
	}
	return nil
}

func writeAuthBackupPath(output io.Writer, backupPath string) error {
	if backupPath == "" {
		return nil
	}
	if _, err := fmt.Fprintf(
		output,
		"Web auth backup created: %s\n",
		//logpolicy:allow the auth backup command must report the exact operator-requested restore path
		backupPath,
	); err != nil {
		return fmt.Errorf("write auth backup result: %w", err)
	}
	return nil
}
