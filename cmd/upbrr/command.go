// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type cliIO struct {
	in     io.Reader
	out    io.Writer
	errOut io.Writer
}

func (streams cliIO) normalized() cliIO {
	if streams.in == nil {
		streams.in = strings.NewReader("")
	}
	if streams.out == nil {
		streams.out = io.Discard
	}
	if streams.errOut == nil {
		streams.errOut = io.Discard
	}
	return streams
}

func executeCLI(ctx context.Context, args []string, streams cliIO) error {
	streams = streams.normalized()
	originalArgs := append([]string(nil), args...)
	if len(args) == 0 {
		//nolint:contextcheck // Cobra injects ctx through ExecuteContext after command construction.
		cmd := newUploadRootCommand(streams, originalArgs)
		cmd.SetArgs([]string{})
		return executeCobraCommand(ctx, cmd)
	}

	switch args[0] {
	case "serve":
		var opts serveOptions
		fs := pflag.NewFlagSet("serve", pflag.ContinueOnError)
		bindServeFlags(fs, &opts)
		normalized := normalizeNonInterspersedArgs(fs, args[1:])
		//nolint:contextcheck // Cobra injects ctx through ExecuteContext after command construction.
		cmd := newRootCommand(streams, originalArgs)
		cmd.SetArgs(append([]string{"serve"}, normalized...))
		return executeCobraCommand(ctx, cmd)
	case "api-token":
		return executeAPITokenCLI(ctx, args, streams, originalArgs)
	default:
		//nolint:contextcheck // Cobra injects ctx through ExecuteContext after command construction.
		cmd := newUploadRootCommand(streams, originalArgs)
		flagArgs, positionalArgs := partitionUploadArgs(cmd.Flags(), args)
		normalized := append(make([]string, 0, len(flagArgs)+len(positionalArgs)+1), flagArgs...)
		normalized = append(normalized, "--")
		normalized = append(normalized, positionalArgs...)
		cmd.SetArgs(normalized)
		return executeCobraCommand(ctx, cmd)
	}
}

func executeAPITokenCLI(ctx context.Context, args []string, streams cliIO, originalArgs []string) error {
	if len(args) == 1 || apiTokenHelpToken(args[1]) {
		//nolint:contextcheck // Cobra injects ctx through ExecuteContext after command construction.
		cmd := newRootCommand(streams, originalArgs)
		cmd.SetArgs([]string{"api-token", "--help"})
		return executeCobraCommand(ctx, cmd)
	}

	childName := args[1]
	var fs *pflag.FlagSet
	switch childName {
	case "list", "revoke":
		var opts configAPITokenOptions
		fs = pflag.NewFlagSet("api-token "+childName, pflag.ContinueOnError)
		bindConfigAPITokenFlags(fs, &opts)
	default:
		return exitError(2, fmt.Errorf("unknown api-token command %q", childName))
	}
	normalized := normalizeNonInterspersedArgs(fs, args[2:])
	commandArgs := append([]string{"api-token", childName}, normalized...)
	//nolint:contextcheck // Cobra injects ctx through ExecuteContext after command construction.
	cmd := newRootCommand(streams, originalArgs)
	cmd.SetArgs(commandArgs)
	return executeCobraCommand(ctx, cmd)
}

// executeCobraCommand preserves Cobra's typed errors and compatibility text.
func executeCobraCommand(ctx context.Context, cmd *cobra.Command) error {
	//nolint:wrapcheck // Callers rely on cliExitError identity and exact legacy diagnostics.
	return cmd.ExecuteContext(ctx)
}

func apiTokenHelpToken(value string) bool {
	switch value {
	case "help", "-h", "--h", "-help", "--help":
		return true
	default:
		return false
	}
}

func newRootCommand(streams cliIO, originalArgs []string) *cobra.Command {
	cmd := newUploadRootCommand(streams, originalArgs)
	cmd.AddCommand(newServeCommand(streams), newAPITokenCommand(streams))
	return cmd
}

func newUploadRootCommand(streams cliIO, originalArgs []string) *cobra.Command {
	streams = streams.normalized()
	var bound cliOptions
	cmd := &cobra.Command{
		Use:           "upbrr [options] <input path>...",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, paths []string) error {
			visited := canonicalChangedFlags(cmd.Flags(), cliFlagAliases())
			if err := normalizeCLIOptions(&bound, visited); err != nil {
				return exitError(2, err)
			}
			return runUpload(cmd.Context(), originalArgs, bound, visited, paths, cliIO{
				in:     cmd.InOrStdin(),
				out:    cmd.OutOrStdout(),
				errOut: cmd.ErrOrStderr(),
			})
		},
	}
	configureCommand(cmd, streams, 2, "parse CLI options", func(cmd *cobra.Command) string {
		return formatFlagUsage(cmd.Flags(), "upbrr [options] <input path>...")
	})
	bindUploadFlags(cmd.Flags(), &bound)
	addExplicitHelpFlag(cmd)
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func newServeCommand(streams cliIO) *cobra.Command {
	streams = streams.normalized()
	var opts serveOptions
	cmd := &cobra.Command{
		Use:  "serve [options]",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context(), opts, canonicalChangedFlags(cmd.Flags(), nil))
		},
	}
	configureCommand(cmd, streams, 1, "parse serve options", func(cmd *cobra.Command) string {
		return formatFlagUsage(cmd.Flags(), "upbrr serve [options]")
	})
	bindServeFlags(cmd.Flags(), &opts)
	addExplicitHelpFlag(cmd)
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func newAPITokenCommand(streams cliIO) *cobra.Command {
	streams = streams.normalized()
	cmd := &cobra.Command{
		Use:  "api-token <command> [options]",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	configureCommand(cmd, streams, 2, "parse api-token options", func(*cobra.Command) string {
		return apiTokenCommandUsage
	})
	addExplicitHelpFlag(cmd)
	cmd.Flags().SetInterspersed(false)
	cmd.AddCommand(
		newAPITokenListCommand(streams),
		newAPITokenRevokeCommand(streams),
	)
	return cmd
}

func newAPITokenListCommand(streams cliIO) *cobra.Command {
	var opts configAPITokenOptions
	cmd := &cobra.Command{
		Use: "list [options]",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return exitError(2, errors.New("api-token list does not accept positional arguments"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runListAPITokensCommand(cmd.Context(), opts, cmd.Flags().Changed("config"), cmd.OutOrStdout())
		},
	}
	configureCommand(cmd, streams, 2, "parse api-token list options", func(cmd *cobra.Command) string {
		return formatFlagUsage(cmd.Flags(), "upbrr api-token list [options]")
	})
	bindConfigAPITokenFlags(cmd.Flags(), &opts)
	addExplicitHelpFlag(cmd)
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func newAPITokenRevokeCommand(streams cliIO) *cobra.Command {
	var opts configAPITokenOptions
	cmd := &cobra.Command{
		Use: "revoke [options] <token-id>",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return exitError(2, errors.New("api-token revoke requires exactly one token ID"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRevokeAPITokenCommand(cmd.Context(), opts, cmd.Flags().Changed("config"), args[0], cmd.OutOrStdout())
		},
	}
	configureCommand(cmd, streams, 2, "parse api-token revoke options", func(cmd *cobra.Command) string {
		return formatFlagUsage(cmd.Flags(), "upbrr api-token revoke [options] <token-id>")
	})
	bindConfigAPITokenFlags(cmd.Flags(), &opts)
	addExplicitHelpFlag(cmd)
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func configureCommand(
	cmd *cobra.Command,
	streams cliIO,
	exitCode int,
	parsePrefix string,
	helpText func(*cobra.Command) string,
) {
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.DisableSuggestions = true
	cmd.CompletionOptions.DisableDefaultCmd = true
	cmd.SetIn(streams.in)
	cmd.SetOut(streams.out)
	cmd.SetErr(streams.errOut)
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(cmd.OutOrStdout(), helpText(cmd))
	})
	cmd.SetUsageFunc(func(cmd *cobra.Command) error {
		_, err := io.WriteString(cmd.OutOrStdout(), helpText(cmd))
		if err != nil {
			return fmt.Errorf("write command usage: %w", err)
		}
		return nil
	})
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return exitError(exitCode, fmt.Errorf("%s: %w", parsePrefix, translatePFlagError(err)))
	})
}

func addExplicitHelpFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("help", false, "Show help and exit")
	_ = cmd.Flags().MarkHidden("help")
}

func translatePFlagError(err error) error {
	var notExist *pflag.NotExistError
	if errors.As(err, &notExist) {
		return fmt.Errorf("flag provided but not defined: -%s", notExist.GetSpecifiedName())
	}
	var valueRequired *pflag.ValueRequiredError
	if errors.As(err, &valueRequired) {
		return fmt.Errorf("flag needs an argument: -%s", valueRequired.GetSpecifiedName())
	}
	var invalidValue *pflag.InvalidValueError
	if errors.As(err, &invalidValue) {
		return fmt.Errorf(
			"invalid value %q for flag -%s: %w",
			invalidValue.GetValue(),
			invalidValue.GetFlag().Name,
			errors.Unwrap(invalidValue),
		)
	}
	var invalidSyntax *pflag.InvalidSyntaxError
	if errors.As(err, &invalidSyntax) {
		return fmt.Errorf("bad flag syntax: %s", invalidSyntax.GetSpecifiedFlag())
	}
	return err
}
