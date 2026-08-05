// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type Validator struct {
	logger api.Logger
}

func NewValidator() *Validator {
	return &Validator{logger: api.NopLogger{}}
}

func NewValidatorWithLogger(logger api.Logger) *Validator {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Validator{logger: logger}
}

// ValidatePaths validates paths in input order and returns their absolute host
// forms without deduplication. It stops at the first invalid path or cancellation.
func (v *Validator) ValidatePaths(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, internalerrors.ErrInvalidInput
	}
	v.logger.Debugf("filesystem: validating %d paths", len(paths))

	result := make([]string, 0, len(paths))

	for _, path := range paths {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			return nil, fmt.Errorf("filesystem: empty path: %w", internalerrors.ErrInvalidInput)
		}

		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return nil, fmt.Errorf("filesystem: resolve path: %w", err)
		}
		v.logger.Tracef("filesystem: resolved %s", abs)

		if _, err := os.Stat(abs); err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("filesystem: path %q: %w", abs, internalerrors.ErrNotFound)
			}
			return nil, fmt.Errorf("filesystem: path %q: %w", abs, err)
		}

		result = append(result, abs)
	}
	v.logger.Debugf("filesystem: validated %d paths", len(result))

	return result, nil
}
