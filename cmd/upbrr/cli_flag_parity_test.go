// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"testing"

	"github.com/spf13/pflag"
)

func TestCLIUploadAliasesTargetRegisteredFlags(t *testing.T) {
	t.Parallel()
	registered := commandFlagNames(newUploadRootCommand(cliIO{}, nil).Flags())
	for alias, target := range cliFlagAliases() {
		if _, exists := registered[alias]; !exists {
			t.Errorf("alias %q is not registered", alias)
		}
		if _, exists := registered[target]; !exists {
			t.Errorf("alias %q targets unregistered canonical flag %q", alias, target)
		}
	}
}

func commandFlagNames(fs *pflag.FlagSet) map[string]struct{} {
	result := make(map[string]struct{})
	fs.VisitAll(func(flag *pflag.Flag) {
		if flag.Name != "help" {
			result[flag.Name] = struct{}{}
		}
	})
	return result
}
