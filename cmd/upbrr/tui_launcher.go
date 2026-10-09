// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/pflag"

	"github.com/autobrr/upbrr/pkg/api"
)

type cliLaunchRequest struct {
	Sources       []string
	SourceIndexes []int
	Arguments     string
	Changed       bool
	Reply         chan cliLaunchResult
}
type cliLaunchResult struct{ err error }

// Original source authority stays with the driver, outside editable projections.
func resolveCLILaunchSources(original []string, draft cliLaunchRequest) []string {
	sources := slices.Clone(draft.Sources)
	for index, source := range sources {
		if index >= len(draft.SourceIndexes) {
			continue
		}
		originalIndex := draft.SourceIndexes[index]
		if originalIndex >= 0 && originalIndex < len(original) && source == safeTerminalText(original[originalIndex]) {
			sources[index] = original[originalIndex]
		}
	}
	return sources
}

func cliArgumentTokens(args []string) []string {
	var opts cliOptions
	fs := pflag.NewFlagSet("draft", pflag.ContinueOnError)
	bindUploadFlags(fs, &opts)
	flags, _ := partitionUploadArgs(fs, args)
	return flags
}

func sensitiveCLIArgument(name, value string) bool {
	name = strings.ToLower(name)
	for _, key := range []string{"password", "token", "passkey", "cookie", "username", "api-key", "auth-key", "infohash", "torrenthash"} {
		if strings.Contains(name, key) {
			return true
		}
	}
	return safeTerminalText(value) != value || strings.Contains(value, "://")
}

// Opaque original values stay with the driver. The editor receives only safe
// tokens, and a count of protected options; changing them requires a restart.
func cliDraftArguments(args []string) (string, []string, map[string]bool) {
	var opts cliOptions
	fs := pflag.NewFlagSet("draft", pflag.ContinueOnError)
	bindUploadFlags(fs, &opts)
	var safe, opaque []string
	flags := cliArgumentTokens(args)
	protected := make(map[string]bool)
	aliases := cliFlagAliases()
	// First identify protected options, then retain every occurrence and alias
	// together in original order so moving the opaque vector preserves intent.
	for pass := range 2 {
		for index := 0; index < len(flags); index++ {
			token := flags[index]
			name, value, attached := strings.Cut(strings.TrimLeft(token, "-"), "=")
			flag := fs.Lookup(name)
			end := index + 1
			if !attached && flag != nil && flag.NoOptDefVal == "" && end < len(flags) {
				value = flags[end]
				end++
			}
			canonical := name
			if alias, ok := aliases[name]; ok {
				canonical = alias
			}
			switch {
			case pass == 0:
				// The literal-backslash tokenizer cannot represent both quote kinds.
				if sensitiveCLIArgument(name, value) || (strings.Contains(value, "\"") && strings.Contains(value, "'")) {
					protected[canonical] = true
				}
			case protected[canonical]:
				opaque = append(opaque, flags[index:end]...)
			default:
				safe = append(safe, flags[index:end]...)
			}
			index = end - 1
		}
	}
	return joinCLIDraftTokens(safe), opaque, protected
}

func joinCLIDraftTokens(tokens []string) string {
	quoted := make([]string, len(tokens))
	for index, token := range tokens {
		if token == "" || strings.ContainsAny(token, " \t\n\"'") {
			if !strings.Contains(token, "\"") {
				token = "\"" + token + "\""
			} else {
				token = "'" + token + "'"
			}
		}
		quoted[index] = token
	}
	return strings.Join(quoted, " ")
}

// validateCLILaunch reparses edited flags and sources through the upload parser,
// retaining protected original tokens. Presentation, interaction mode, and command
// routing stay fixed for this invocation. Errors contain no rejected argument values.
func validateCLILaunch(original []string, draft cliLaunchRequest) ([]string, error) {
	if len(draft.Sources) == 0 || slices.ContainsFunc(draft.Sources, func(value string) bool { return strings.TrimSpace(value) == "" }) {
		return nil, errors.New("input: provide at least one source path")
	}
	flags := cliArgumentTokens(original)
	if draft.Changed {
		if safeTerminalText(draft.Arguments) != draft.Arguments {
			return nil, errors.New("cli arguments: sensitive or terminal-control text is not accepted; use protected configuration and restart")
		}
		tokens, err := splitInteractiveCLIArgs(draft.Arguments)
		if err != nil {
			return nil, errors.New("cli arguments: close each quoted value; backslashes are literal")
		}
		var opts cliOptions
		fs := pflag.NewFlagSet("draft", pflag.ContinueOnError)
		bindUploadFlags(fs, &opts)
		fs.SetOutput(io.Discard)
		var positional []string
		flags, positional = partitionUploadArgs(fs, tokens)
		if len(positional) != 0 || slices.Contains(tokens, "--") {
			return nil, errors.New("cli arguments: put source paths in the Input fields")
		}
		_, opaque, protected := cliDraftArguments(original)
		for index, token := range flags {
			if !strings.HasPrefix(token, "-") {
				continue
			}
			name, value, _ := strings.Cut(strings.TrimLeft(token, "-"), "=")
			if value == "" && index+1 < len(flags) {
				value = flags[index+1]
			}
			if sensitiveCLIArgument(name, value) {
				return nil, errors.New("cli arguments: protected options require configuration and a restart")
			}
		}
		if parseErr := fs.Parse(flags); parseErr != nil {
			return nil, errors.New("cli arguments: invalid upload flags or values")
		}
		for name := range canonicalChangedFlags(fs, cliFlagAliases()) {
			if protected[name] {
				return nil, errors.New("cli arguments: protected original options require a restart to change")
			}
		}
		flags = append(flags, opaque...)
		args := append(slices.Clone(flags), "--")
		args = append(args, draft.Sources...)
		next, _, _, parseErr := parseCLIOptions(args)
		prior, _, _, priorErr := parseCLIOptions(original)
		if parseErr != nil || priorErr != nil {
			return nil, errors.New("cli arguments: invalid upload flags or values")
		}
		if next.UI != prior.UI || next.UIKeepOpen != prior.UIKeepOpen || next.interactionMode() != prior.interactionMode() || next.ShowVersion ||
			next.CreateAuth ||
			next.Cleanup ||
			next.ImportConfigPath != "" ||
			next.ExportConfigPath != "" ||
			next.AudioAnalysisOnly ||
			next.ExportConfigPlaintext {
			return nil, errors.New("cli arguments: presentation, interaction and command route are fixed; restart to change them")
		}
	}
	args := append(slices.Clone(flags), "--")
	args = append(args, draft.Sources...)
	opts, _, _, err := parseCLIOptions(args)
	if err != nil {
		return nil, errors.New("cli arguments: invalid upload flags or values")
	}
	if opts.interactionMode() != api.InteractionModeInteractive {
		return nil, errors.New("cli arguments: launch requires interactive mode")
	}
	if opts.QueueName != "" && len(draft.Sources) != 1 {
		return nil, errors.New("input: --queue requires exactly one queue root path")
	}
	if len(opts.TrackLanguages) > 0 && (len(draft.Sources) != 1 || opts.QueueName != "") {
		return nil, errors.New("input: track corrections require exactly one source")
	}
	return args, nil
}

func cliProtectedArgumentNotice(opaque []string) string {
	if len(opaque) == 0 {
		return ""
	}
	return fmt.Sprintf("\nProtected arguments retained outside the editor (%d tokens).", len(opaque))
}
