// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func newRecoverCommand(streams cliIO) *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:  "recover [options]",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCLIPresentation(cmd.Context(), streams, detectCLITerminal(streams, streams.out), false, false, false, nil, nil, false, false,
				func(runCtx context.Context, _, _ []string, nextStreams cliIO) error {
					return runRecoverCommand(runCtx, configPath, cmd.Flags().Changed("config"), nextStreams)
				})
		},
	}
	configureCommand(cmd, streams, 2, "parse recovery options", func(cmd *cobra.Command) string {
		return "Reconcile interrupted external effects when configuration changes block startup.\n" +
			"Check each remote outcome before confirming it did not complete.\n" +
			"Completed or uncertain outcomes must remain blocked; answer No.\n" +
			"Recovery exits without uploading, injecting torrents, or activating configuration.\n\n" +
			formatFlagUsage(cmd.Flags(), "upbrr recover [options]")
	})
	cmd.Flags().StringVar(&configPath, "config", "", "Path to config file selecting the existing database")
	addExplicitHelpFlag(cmd)
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func runRecoverCommand(ctx context.Context, configPath string, configProvided bool, streams cliIO) (result error) {
	dbPath, err := configstore.ResolveExistingDBPath(ctx, configPath, configProvided)
	if err != nil {
		return fmt.Errorf("select recovery database: %w", err)
	}
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open recovery database: %w", err)
	}
	var recovery *releaseworkflow.Recovery
	defer func() {
		if recovery != nil {
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := recovery.Shutdown(shutdownCtx); err != nil {
				result = errors.Join(result, fmt.Errorf("stop workflow recovery; repository remains open: %w", err))
				return
			}
		}
		result = errors.Join(result, repo.Close())
	}()
	if err := repo.MigrateContext(ctx); err != nil {
		return fmt.Errorf("migrate recovery database: %w", err)
	}
	scopes, err := repo.ListReleaseWorkflowRecoveryScopes(ctx)
	if err != nil {
		return fmt.Errorf("list interrupted workflows: %w", err)
	}
	if len(scopes) == 0 {
		fmt.Fprintln(streams.out, "No interrupted external effects require reconciliation.")
		return nil
	}
	persistent, err := releaseworkflow.NewPersistentRepository(repo)
	if err != nil {
		return fmt.Errorf("open retained workflow state: %w", err)
	}
	vault, err := releaseworkflow.NewPrivateArtifactVault(releaseworkflow.PrivateArtifactVaultRoot(repo.DBPath()))
	if err != nil {
		return fmt.Errorf("open retained workflow artifacts: %w", err)
	}
	recovery, err = releaseworkflow.NewRecovery(persistent, repo, vault)
	if err != nil {
		return fmt.Errorf("initialize workflow recovery: %w", err)
	}
	fmt.Fprintln(streams.out, "Check the tracker, image host, or torrent client before confirming any effect did not complete.")
	fmt.Fprintln(streams.out, "Completed or uncertain outcomes must remain blocked; answer No.")
	reader := bufio.NewReader(streams.in)
	for _, scope := range scopes {
		retained, err := recovery.Recover(ctx, scope)
		if err != nil {
			return fmt.Errorf("recover interrupted workflow: %w", err)
		}
		workflow := retained.Workflow
		source := "unavailable"
		if retained.SourcePath != "" {
			source = safeDiagnosticText(filepath.Base(retained.SourcePath))
		}
		fmt.Fprintf(streams.out, "Source: %s\n", source)
		for {
			action := pendingCLIWorkflowAction(workflow.RequiredActions, api.RequiredActionReconcileSubmission)
			if action == nil {
				break
			}
			fmt.Fprintf(streams.out, "Workflow %s: effect=%s scope=%s tracker=%s\n",
				safeDiagnosticText(string(scope.WorkflowID)), safeDiagnosticText(string(action.EffectKind)),
				safeDiagnosticText(action.EffectScopeID),
				safeDiagnosticText(strings.TrimSpace(string(action.TrackerID))))
			confirmed, err := promptYesNo(reader, streams.out, action.Prompt+" Confirm it did not complete? [y/N]: ", false)
			if err != nil {
				return fmt.Errorf("read recovery confirmation: %w", err)
			}
			if !confirmed {
				return errors.New("external outcome remains unresolved; no new submission was started")
			}
			workflow, err = recovery.ConfirmNotCompleted(ctx, scope, workflow.Revision, action.ID)
			if err != nil {
				return fmt.Errorf("reconcile interrupted workflow: %w", err)
			}
		}
	}
	remaining, err := repo.ListReleaseWorkflowRecoveryScopes(ctx)
	if err != nil {
		return fmt.Errorf("inspect remaining workflow effects: %w", err)
	}
	if len(remaining) != 0 {
		return errors.New("unresolved effects or submission fences remain without an actionable confirmation; retain the database for manual recovery")
	}
	fmt.Fprintln(streams.out, "Interrupted effects reconciled. Rerun the original command to activate configuration and continue.")
	return nil
}
